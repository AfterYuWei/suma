package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/suma/suma/server/internal/ai"
	composeService "github.com/suma/suma/server/internal/compose"
	"github.com/suma/suma/server/internal/database"
	networkService "github.com/suma/suma/server/internal/network"
	"github.com/suma/suma/server/internal/task"
	volumeService "github.com/suma/suma/server/internal/volume"
)

var aiResourceName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

type containerAIParams struct {
	Force         bool   `json:"force,omitempty"`
	RemoveVolumes bool   `json:"remove_volumes,omitempty"`
	Name          string `json:"name,omitempty"`
}
type imageAIParams struct {
	Force     bool   `json:"force,omitempty"`
	Reference string `json:"reference,omitempty"`
}
type projectAIParams struct {
	Force   bool   `json:"force,omitempty"`
	DraftID string `json:"draft_id,omitempty"`
}

func decodeParams(raw json.RawMessage, out any) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(out) != nil {
		return ai.ErrInvalid
	}
	return nil
}
func hasExtendedAction(action string) bool {
	switch action {
	case "container.pause", "container.unpause", "container.kill", "container.rename", "container.remove", "image.tag", "image.remove", "network.create", "network.remove", "volume.create", "volume.remove", "project.create", "project.save", "project.start", "project.stop", "project.restart", "project.down", "project.pull", "project.build", "project.remove", "project.takeover", "project.cleanup", "cleanup.cache":
		return true
	}
	return false
}
func (r aiRuntime) freezeExtended(ctx context.Context, nodeID string, req ai.OperationRequest, snap ai.Snapshot) (ai.Snapshot, error) {
	runtime, err := r.nodes.Runtime(ctx, nodeID)
	if err != nil {
		return snap, err
	}
	var details any
	named := func(label, name string) {
		snap.Confirmations = append(snap.Confirmations, ai.Confirmation{Key: "name", Label: label, Expected: name, Warning: "This action can delete resources or configuration; existing data may be lost"})
	}
	switch {
	case strings.HasPrefix(req.Action, "container."):
		var params containerAIParams
		if decodeParams(req.Parameters, &params) != nil || params.RemoveVolumes {
			return snap, errors.New("volume deletion requires its own named approval")
		}
		if params.Force && req.Action != "container.remove" || params.Name != "" && req.Action != "container.rename" {
			return snap, ai.ErrInvalid
		}
		container, err := runtime.Get(ctx, req.ResourceID)
		if err != nil {
			return snap, err
		}
		if container.ID != req.ResourceID {
			return snap, errors.New("full immutable container ID required")
		}
		state, err := runtime.AIContainerState(ctx, container.ID)
		if err != nil {
			return snap, err
		}
		details = state
		if req.Action == "container.rename" {
			if !aiResourceName.MatchString(params.Name) {
				return snap, ai.ErrInvalid
			}
			rows, err := runtime.List(ctx)
			if err != nil {
				return snap, err
			}
			for _, row := range rows {
				if row.Name == params.Name && row.ID != container.ID {
					return snap, errors.New("container name already exists")
				}
			}
		}
		snap.Impact = "Changes only this container. Pause/kill/removal interrupt service; removal deletes its writable layer. Volumes are preserved."
		if params.Force {
			snap.Confirmations = append(snap.Confirmations, ai.Confirmation{Key: "force", Label: "Force removal of a running container", Checkbox: true})
		}
	case req.Action == "image.tag" || req.Action == "image.remove":
		var params imageAIParams
		if decodeParams(req.Parameters, &params) != nil {
			return snap, ai.ErrInvalid
		}
		row, err := runtime.InspectImage(ctx, req.ResourceID)
		if err != nil {
			return snap, err
		}
		if row.ID != req.ResourceID {
			return snap, errors.New("full immutable image ID required")
		}
		details = map[string]any{"id": row.ID, "tags": row.Tags, "digests": row.Digests, "containers": row.Containers}
		if req.Action == "image.tag" {
			if params.Reference == "" || params.Force {
				return snap, ai.ErrInvalid
			}
			if _, err = validateImageReference(params.Reference); err != nil {
				return snap, err
			}
			destination, inspectErr := runtime.InspectImage(ctx, params.Reference)
			destinationID := ""
			if inspectErr == nil {
				destinationID = destination.ID
			} else {
				if _, err = runtime.ListImages(ctx); err != nil {
					return snap, err
				}
			}
			details = map[string]any{"id": row.ID, "tags": row.Tags, "digests": row.Digests, "containers": row.Containers, "destination_image_id": destinationID, "destination_reference": params.Reference}
		}
		if req.Action == "image.remove" && params.Reference != "" {
			return snap, ai.ErrInvalid
		}
		snap.Impact = "Changes a local image reference or deletes a local image; containers are not recreated. Deleted images require a separately approved pull."
		if params.Force {
			snap.Confirmations = append(snap.Confirmations, ai.Confirmation{Key: "force", Label: "Force image deletion", Checkbox: true})
		}
	case req.Action == "network.create":
		var params networkService.CreateRequest
		if decodeParams(req.Parameters, &params) != nil || params.Name != req.ResourceID || !aiResourceName.MatchString(params.Name) || params.Driver != "" && params.Driver != "bridge" && params.Driver != "macvlan" && params.Driver != "ipvlan" {
			return snap, ai.ErrInvalid
		}
		if params.Subnet != "" {
			_, subnet, err := net.ParseCIDR(params.Subnet)
			if err != nil {
				return snap, ai.ErrInvalid
			}
			if params.Gateway != "" && !subnet.Contains(net.ParseIP(params.Gateway)) {
				return snap, ai.ErrInvalid
			}
		} else if params.Gateway != "" {
			return snap, ai.ErrInvalid
		}
		rows, err := runtime.ListNetworks(ctx)
		if err != nil {
			return snap, err
		}
		for _, row := range rows {
			if row.Name == params.Name {
				return snap, errors.New("network name already exists")
			}
		}
		details = map[string]any{"absent": params.Name, "configuration": params}
		snap.Impact = "Creates one Docker network; existing containers are not attached automatically."
	case req.Action == "network.remove":
		if !emptyParameters(req.Parameters) {
			return snap, ai.ErrInvalid
		}
		row, err := runtime.InspectNetwork(ctx, req.ResourceID)
		if err != nil {
			return snap, err
		}
		if row.ID != req.ResourceID || row.Containers > 0 || row.Name == "bridge" || row.Name == "host" || row.Name == "none" {
			return snap, errors.New("network is built in or in use")
		}
		details = row
		snap.Impact = "Permanently deletes the frozen unused network."
	case req.Action == "volume.create":
		var params volumeService.CreateRequest
		if decodeParams(req.Parameters, &params) != nil || params.Name != req.ResourceID || !aiResourceName.MatchString(params.Name) {
			return snap, ai.ErrInvalid
		}
		if strings.Contains(params.Options["o"], "bind") {
			source := params.Options["device"]
			if !filepath.IsAbs(source) || strings.Contains(source, "$") {
				return snap, errors.New("bind volume requires a non-interpolated absolute device path")
			}
		}
		if len(params.Options) > 20 || len(params.Labels) > 20 {
			return snap, ai.ErrInvalid
		}
		rows, err := runtime.ListVolumes(ctx)
		if err != nil {
			return snap, err
		}
		for _, row := range rows {
			if row.Name == params.Name {
				return snap, errors.New("volume name already exists")
			}
		}
		details = map[string]any{"absent": params.Name, "configuration": params}
		snap.Impact = "Creates one volume; existing containers are not mounted automatically."
	case req.Action == "volume.remove":
		if !emptyParameters(req.Parameters) {
			return snap, ai.ErrInvalid
		}
		row, err := r.cleanup.ReviewVolumeDeletion(ctx, nodeID, req.ResourceID)
		if err != nil {
			return snap, err
		}
		details = row
		snap.Impact = "Permanently deletes this unused volume and all its data. There is no automatic recovery."
		named("Type the complete volume name to confirm data loss", req.ResourceID)
	case strings.HasPrefix(req.Action, "project."):
		svc, err := r.project(ctx, nodeID)
		if err != nil {
			return snap, err
		}
		var params projectAIParams
		if decodeParams(req.Parameters, &params) != nil {
			return snap, ai.ErrInvalid
		}
		if req.Action == "project.create" || req.Action == "project.save" || req.Action == "project.takeover" {
			if params.Force {
				return snap, ai.ErrInvalid
			}
			var draft database.AIComposeDraft
			if r.db.WithContext(ctx).Where("id = ? AND node_id = ?", params.DraftID, nodeID).First(&draft).Error != nil || draft.Project != req.ResourceID {
				return snap, ai.ErrScope
			}
			content, err := r.secrets.Decrypt(draft.Ciphertext)
			if err != nil || stateHash(content) != draft.ContentHash {
				return snap, ai.ErrConflict
			}
			if req.Action == "project.create" {
				projects, err := svc.List(ctx)
				if err != nil {
					return snap, err
				}
				for _, project := range projects {
					if project.Name == draft.Project {
						return snap, ai.ErrConflict
					}
				}
			} else if req.Action == "project.save" {
				project, err := svc.Get(ctx, draft.Project)
				if err != nil || !project.CanManage || project.Revision != draft.BaseRevision {
					return snap, ai.ErrConflict
				}
			} else {
				takeover, err := svc.BuildTakeoverDraft(ctx, draft.Project)
				if err != nil || takeover.Fingerprint != draft.BaseRevision {
					return snap, ai.ErrConflict
				}
				named("Type the Project name to confirm takeover", draft.Project)
			}
			if err = composeService.ValidateComposeBindMounts(content, r.isRemote(ctx, nodeID), true); err != nil {
				return snap, err
			}
			details = map[string]any{"draft_id": draft.ID, "content_hash": draft.ContentHash, "base_revision": draft.BaseRevision, "project": draft.Project, "preview": json.RawMessage(draft.PreviewJSON)}
			if composeService.ValidateComposeBindMounts(content, r.isRemote(ctx, nodeID), false) != nil {
				snap.Confirmations = append(snap.Confirmations, ai.Confirmation{Key: "docker_socket", Label: "Docker socket gives full Engine control", Checkbox: true})
			}
			snap.Impact = "Saves validated Compose configuration only. Deployment, pull and build each require a separate approval."
		} else {
			if params.DraftID != "" || params.Force && req.Action != "project.remove" {
				return snap, ai.ErrInvalid
			}
			project, err := svc.Get(ctx, req.ResourceID)
			if err != nil {
				return snap, err
			}
			state, err := runtime.InspectComposeProject(ctx, req.ResourceID)
			if err != nil {
				return snap, err
			}
			details = map[string]any{"name": project.Name, "revision": project.Revision, "path": project.Path, "can_manage": project.CanManage, "state_hash": stateHash(state)}
			if req.Action == "project.cleanup" {
				if project.CanManage {
					return snap, ai.ErrInvalid
				}
				named("Type the external Project name to confirm cleanup", req.ResourceID)
			} else if !project.CanManage {
				return snap, errors.New("Project is external; take over or explicitly clean it first")
			}
			if project.CanManage {
				if err = svc.ValidateAgentSources(req.ResourceID, project.Compose); err != nil {
					return snap, err
				}
			}
			if req.Action == "project.remove" {
				named("Type the Project name to confirm removal", req.ResourceID)
			}
			if composeService.ValidateComposeBindMounts(project.Compose, r.isRemote(ctx, nodeID), true) != nil {
				return snap, ai.ErrInvalid
			}
			if composeService.ValidateComposeBindMounts(project.Compose, r.isRemote(ctx, nodeID), false) != nil {
				snap.Confirmations = append(snap.Confirmations, ai.Confirmation{Key: "docker_socket", Label: "Docker socket gives full Engine control", Checkbox: true})
			}
			snap.Impact = "Changes this Project only. Stop/down/removal interrupt service; pull/build are explicit steps. Named volumes are preserved and require separate deletion approvals."
		}
	case req.Action == "cleanup.cache":
		return r.freezeCache(ctx, nodeID, req, snap)
	default:
		return snap, ai.ErrInvalid
	}
	snap.Details = json.RawMessage(jsonText(details))
	snap.Fingerprint = stateHash(details)
	return snap, nil
}
func (r aiRuntime) isRemote(ctx context.Context, nodeID string) bool {
	node, err := r.nodes.Get(ctx, nodeID)
	return err != nil || node.ConnectionType != "unix"
}
func (r aiRuntime) executeExtended(ctx context.Context, row database.AIOperation, snap ai.Snapshot, report task.Reporter) error {
	runtime, err := r.nodes.Runtime(ctx, row.NodeID)
	if err != nil {
		return err
	}
	var confirmations map[string]string
	_ = json.Unmarshal([]byte(row.ConfirmedJSON), &confirmations)
	switch row.Action {
	case "container.pause":
		return runtime.Pause(ctx, row.ResourceID)
	case "container.unpause":
		return runtime.Unpause(ctx, row.ResourceID)
	case "container.kill":
		return runtime.Kill(ctx, row.ResourceID)
	case "container.rename":
		var params containerAIParams
		_ = decodeParams(json.RawMessage(row.ParametersJSON), &params)
		return runtime.Rename(ctx, row.ResourceID, params.Name)
	case "container.remove":
		var params containerAIParams
		_ = decodeParams(json.RawMessage(row.ParametersJSON), &params)
		if params.Force {
			return runtime.ForceRemove(ctx, row.ResourceID, false)
		}
		return runtime.Remove(ctx, row.ResourceID, false)
	case "image.tag":
		var params imageAIParams
		_ = decodeParams(json.RawMessage(row.ParametersJSON), &params)
		return runtime.TagImage(ctx, row.ResourceID, params.Reference)
	case "image.remove":
		var params imageAIParams
		_ = decodeParams(json.RawMessage(row.ParametersJSON), &params)
		return runtime.RemoveImage(ctx, row.ResourceID, params.Force)
	case "network.create":
		var params networkService.CreateRequest
		_ = decodeParams(json.RawMessage(row.ParametersJSON), &params)
		_, err = runtime.CreateNetwork(ctx, params)
		return err
	case "network.remove":
		return runtime.RemoveNetwork(ctx, row.ResourceID)
	case "volume.create":
		var params volumeService.CreateRequest
		_ = decodeParams(json.RawMessage(row.ParametersJSON), &params)
		_, err = runtime.CreateVolume(ctx, params)
		return err
	case "volume.remove":
		if confirmations["name"] != row.ResourceID {
			return ai.ErrInvalid
		}
		if _, err = r.cleanup.ReviewVolumeDeletion(ctx, row.NodeID, row.ResourceID); err != nil {
			return err
		}
		return runtime.RemoveVolume(ctx, row.ResourceID)
	case "cleanup.cache":
		return r.executeCache(ctx, row, snap, report)
	}
	svc, err := r.project(ctx, row.NodeID)
	if err != nil {
		return err
	}
	var params projectAIParams
	_ = decodeParams(json.RawMessage(row.ParametersJSON), &params)
	switch row.Action {
	case "project.create", "project.save", "project.takeover":
		var draft database.AIComposeDraft
		if r.db.WithContext(ctx).First(&draft, "id = ?", params.DraftID).Error != nil {
			return ai.ErrConflict
		}
		content, err := r.secrets.Decrypt(draft.Ciphertext)
		if err != nil {
			return err
		}
		if row.Action == "project.takeover" {
			source, err := svc.BuildTakeoverDraft(ctx, draft.Project)
			if err != nil || source.Fingerprint != draft.BaseRevision {
				return ai.ErrConflict
			}
			_, err = svc.Takeover(ctx, draft.Project, composeService.TakeoverInput{Environment: source.Environment, Mode: composeService.TakeoverModeManual, Fingerprint: draft.BaseRevision, ConfirmationName: confirmations["name"], Compose: content})
			return err
		}
		_, err = svc.SaveAgentDraft(ctx, draft.Project, content, draft.BaseRevision, row.Action == "project.create")
		return err
	case "project.remove":
		if confirmations["name"] != row.ResourceID {
			return ai.ErrInvalid
		}
		if params.Force {
			return svc.ForceRemove(ctx, row.ResourceID, true)
		}
		return svc.Remove(ctx, row.ResourceID)
	case "project.cleanup":
		if confirmations["name"] != row.ResourceID {
			return ai.ErrInvalid
		}
		return svc.ApplyAgentCleanup(ctx, row.ResourceID, report)
	default:
		var details struct {
			Revision string `json:"revision"`
		}
		if json.Unmarshal(snap.Details, &details) != nil {
			return ai.ErrInvalid
		}
		return svc.ApplyAgentAction(ctx, row.ResourceID, strings.TrimPrefix(row.Action, "project."), details.Revision, confirmations["docker_socket"] == "true", report)
	}
}
func validateImageReference(ref string) (string, error) {
	if strings.TrimSpace(ref) != ref || strings.ContainsAny(ref, " \n\r\t") || len(ref) > 512 || strings.Contains(ref, "@") || !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9./_:\-]*$`).MatchString(ref) {
		return "", ai.ErrInvalid
	}
	return ref, nil
}

func (r aiRuntime) Verify(ctx context.Context, row database.AIOperation, snap ai.Snapshot) (ai.Verification, error) {
	runtime, err := r.nodes.Runtime(ctx, row.NodeID)
	if err != nil {
		return ai.Verification{}, err
	}
	out := ai.Verification{Evidence: []ai.Evidence{}}
	var evidence any
	if strings.HasPrefix(row.Action, "container.") {
		if row.Action == "container.remove" {
			rows, err := runtime.List(ctx)
			if err != nil {
				return out, err
			}
			out.Satisfied = true
			for _, item := range rows {
				if item.ID == row.ResourceID {
					out.Satisfied = false
				}
			}
			evidence = map[string]any{"container_id": row.ResourceID, "absent": out.Satisfied}
		} else {
			state, err := runtime.AIContainerState(ctx, row.ResourceID)
			if err != nil {
				return out, err
			}
			status, _ := state["status"].(string)
			expected := map[string]string{"container.start": "running", "container.stop": "exited", "container.restart": "running", "container.pause": "paused", "container.unpause": "running", "container.kill": "exited"}[row.Action]
			out.Satisfied = status == expected
			if row.Action == "container.restart" {
				var before map[string]any
				_ = json.Unmarshal(snap.Details, &before)
				out.Satisfied = out.Satisfied && state["started_at"] != before["started_at"]
			}
			if row.Action == "container.rename" {
				var params containerAIParams
				_ = decodeParams(json.RawMessage(row.ParametersJSON), &params)
				item, err := runtime.Get(ctx, row.ResourceID)
				if err != nil {
					return out, err
				}
				out.Satisfied = item.Name == params.Name
			}
			evidence = state
		}
	} else {
		switch row.Action {
		case "image.pull":
			item, err := runtime.InspectImage(ctx, row.ResourceID)
			if err != nil {
				return out, err
			}
			out.Satisfied = false
			for _, digest := range item.Digests {
				if digest == row.ResourceID || strings.HasSuffix(digest, strings.Split(row.ResourceID, "@")[1]) {
					out.Satisfied = true
				}
			}
			evidence = map[string]any{"id": item.ID, "digests": item.Digests}
		case "image.tag":
			var params imageAIParams
			_ = decodeParams(json.RawMessage(row.ParametersJSON), &params)
			item, err := runtime.InspectImage(ctx, params.Reference)
			if err != nil {
				return out, err
			}
			out.Satisfied = item.ID == row.ResourceID
			evidence = map[string]any{"id": item.ID, "tags": item.Tags}
		case "image.remove":
			rows, err := runtime.ListImages(ctx)
			if err != nil {
				return out, err
			}
			out.Satisfied = true
			for _, item := range rows {
				if item.ID == row.ResourceID {
					out.Satisfied = false
				}
			}
			evidence = map[string]any{"absent": out.Satisfied}
		case "network.create", "network.remove":
			rows, err := runtime.ListNetworks(ctx)
			if err != nil {
				return out, err
			}
			found := false
			for _, item := range rows {
				if item.ID == row.ResourceID || item.Name == row.ResourceID {
					found = true
					evidence = item
				}
			}
			out.Satisfied = found == (row.Action == "network.create")
		case "volume.create", "volume.remove":
			rows, err := runtime.ListVolumes(ctx)
			if err != nil {
				return out, err
			}
			found := false
			for _, item := range rows {
				if item.Name == row.ResourceID {
					found = true
					evidence = map[string]any{"name": item.Name, "driver": item.Driver, "used_by": item.UsedBy}
				}
			}
			out.Satisfied = found == (row.Action == "volume.create")
		case "cd.deploy", "cd.retry", "cd.rollback":
			var deployment database.DeliveryReleaseDeployment
			err := r.db.WithContext(ctx).Where("node_id = ? AND release_id = ?", row.NodeID, row.ResourceID).First(&deployment).Error
			if err != nil {
				return out, err
			}
			out.Satisfied = deployment.Status == "success" || deployment.Status == "deployed"
			evidence = deployment
		case "cleanup.protected", "cleanup.cache":
			return r.verifyCleanup(ctx, row, snap)
		default:
			svc, err := r.project(ctx, row.NodeID)
			if err != nil {
				return out, err
			}
			projects, err := svc.List(ctx)
			if err != nil {
				return out, err
			}
			found := false
			for _, project := range projects {
				if project.Name != row.ResourceID {
					continue
				}
				found = true
				evidence = map[string]any{"name": project.Name, "revision": project.Revision, "state": project.Status, "services": project.Services, "can_manage": project.CanManage}
				switch row.Action {
				case "project.create", "project.save", "project.takeover":
					var params projectAIParams
					_ = decodeParams(json.RawMessage(row.ParametersJSON), &params)
					var draft database.AIComposeDraft
					if r.db.WithContext(ctx).First(&draft, "id = ?", params.DraftID).Error == nil {
						out.Satisfied = project.CanManage && stateHash(project.Compose) == draft.ContentHash
					}
				case "project.up", "project.start", "project.restart", "project.update":
					out.Satisfied = project.Status == "running"
					if row.Action == "project.restart" {
						var before struct {
							StateHash string `json:"state_hash"`
						}
						_ = json.Unmarshal(snap.Details, &before)
						state, err := runtime.InspectComposeProject(ctx, row.ResourceID)
						if err != nil {
							return out, err
						}
						out.Satisfied = out.Satisfied && stateHash(state) != before.StateHash
					}
				case "project.stop", "project.down":
					out.Satisfied = project.Status == "stopped" || project.Containers == 0
				case "project.pull", "project.build":
					out.Satisfied = true
					var config struct {
						Services map[string]struct {
							Image string `yaml:"image"`
						} `yaml:"services"`
					}
					if yaml.Unmarshal([]byte(project.Compose), &config) != nil || len(config.Services) == 0 {
						out.Satisfied = false
					}
					for name, service := range config.Services {
						ref := service.Image
						if ref == "" {
							ref = project.Name + "-" + name
						}
						if _, err := runtime.InspectImage(ctx, ref); err != nil {
							out.Satisfied = false
						}
					}
				}
			}
			if row.Action == "project.remove" || row.Action == "project.cleanup" {
				out.Satisfied = !found
			}
		}
	}
	out.Summary = fmt.Sprintf("Actual state verified: %t", out.Satisfied)
	if evidence != nil {
		out.Evidence = append(out.Evidence, ai.Evidence{NodeID: row.NodeID, Source: "verify", Resource: row.ResourceID, Time: time.Now().UTC(), Content: jsonText(evidence)})
	}
	return out, nil
}
