package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/google/go-containerregistry/pkg/name"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/suma/suma/server/internal/ai"
	cdService "github.com/suma/suma/server/internal/cd"
	"github.com/suma/suma/server/internal/cleanup"
	composeService "github.com/suma/suma/server/internal/compose"
	"github.com/suma/suma/server/internal/credential"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/imageupdate"
	nodeService "github.com/suma/suma/server/internal/node"
	"github.com/suma/suma/server/internal/projectlogs"
	"github.com/suma/suma/server/internal/redact"
	"github.com/suma/suma/server/internal/secret"
	"github.com/suma/suma/server/internal/task"
	"gorm.io/gorm"
)

type aiRuntime struct {
	secrets      *secret.Store
	db           *gorm.DB
	nodes        *nodeService.Service
	compose      *composeService.Service
	runner       *composeService.CLIRunner
	cd           *cdService.Service
	cleanup      *cleanup.Service
	tasks        *task.Service
	registries   *credential.RegistryService
	imageUpdates *imageupdate.Service
}

func jsonText(v any) string { b, _ := json.Marshal(v); return string(b) }
func stateHash(v any) string {
	sum := sha256.Sum256([]byte(jsonText(v)))
	return hex.EncodeToString(sum[:])
}
func (r aiRuntime) project(ctx context.Context, nodeID string) (*composeService.Service, error) {
	target, node, err := r.nodes.ComposeTarget(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	runtime, err := r.nodes.Runtime(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	return r.compose.ForNode(nodeID, node.Name, r.runner.ForTarget(target), runtime, node.ConnectionType == nodeService.ConnectionUnix), nil
}
func (r aiRuntime) Read(ctx context.Context, nodeID, name string, args ai.ToolArgs, lines, bytes int) (ai.Evidence, error) {
	e := ai.Evidence{Source: name, Resource: args.Kind + ":" + args.ID, Time: time.Now()}
	node, err := r.nodes.Get(ctx, nodeID)
	if err != nil {
		return e, err
	}
	if !node.Enabled {
		return e, ai.ErrScope
	}
	if name == "read_logs" && args.Kind != "container" && args.Kind != "task" {
		return e, errors.New("log tool requires a container or task ID")
	}
	if args.Kind == "task" {
		row, err := r.tasks.GetForNode(ctx, nodeID, args.ID)
		if err != nil {
			return e, err
		}
		if name == "read_logs" {
			logs, err := r.tasks.RecentLogsForNode(ctx, nodeID, row.ID, time.Now().Add(-15*time.Minute), lines, bytes)
			if err != nil {
				return e, err
			}
			var builder strings.Builder
			for _, log := range logs {
				builder.WriteString(log.CreatedAt.Format(time.RFC3339) + " " + log.Message + "\n")
				if builder.Len() >= bytes {
					break
				}
			}
			e.Content = redact.Bounded(builder.String(), bytes)
		} else {
			e.Content = jsonText(row)
		}
		return e, nil
	}
	if args.Kind == "cd" {
		var rows []database.DeliveryReleaseDeployment
		query := r.db.WithContext(ctx).Where("node_id = ?", nodeID)
		if args.ID != "" && args.ID != "all" {
			id, err := strconv.ParseUint(args.ID, 10, 32)
			if err != nil || id == 0 {
				return e, ai.ErrInvalid
			}
			query = query.Where("release_id = ?", id)
		}
		err := query.Order("updated_at DESC").Limit(10).Find(&rows).Error
		e.Content = jsonText(rows)
		return e, err
	}
	if args.Kind == "cleanup" {
		view, err := r.cleanup.Get(ctx, nodeID, false)
		if err != nil {
			return e, err
		}
		preview, err := r.cleanup.Preview(ctx, nodeID)
		if err != nil {
			e.Content = jsonText(map[string]any{"history": view, "missing": "Current cleanup inventory unavailable; do not infer candidates from history"})
			return e, nil
		}
		resources := []cleanup.Resource{}
		for _, resource := range preview.Resources {
			if resource.Kind == cleanup.Container || resource.Kind == cleanup.Image || resource.Kind == cleanup.Network {
				resources = append(resources, resource)
			}
		}
		e.Content = jsonText(map[string]any{"history": view, "generated_at": preview.GeneratedAt, "policy_version": preview.PolicyVersion, "resources": resources, "image_layers_bytes": preview.ImageLayersBytes, "usage": preview.Usage, "excluded_actions": []string{"volume deletion", "build cache pruning"}})
		return e, nil
	}
	if args.Kind == "node" {
		e.Content = jsonText(map[string]any{"id": node.ID, "name": node.Name, "connection_type": node.ConnectionType, "status": node.Status, "engine_id": node.EngineID, "engine_version": node.EngineVersion, "checked_at": node.LastCheckedAt})
		return e, nil
	}
	runtime, err := r.nodes.Runtime(ctx, nodeID)
	if err != nil {
		return e, err
	}
	if name == "read_logs" {
		if args.Kind != "container" {
			return e, errors.New("log tool requires a container ID")
		}
		detail, err := runtime.Get(ctx, args.ID)
		if err != nil {
			return e, err
		}
		var builder strings.Builder
		count := 0
		err = runtime.ReadProjectLogs(ctx, projectlogs.Source{ContainerID: detail.ID, ContainerName: detail.Name}, projectlogs.Query{Tail: lines, Since: time.Now().Add(-15 * time.Minute).Format(time.RFC3339Nano), Until: time.Now().Format(time.RFC3339Nano)}, func(rec projectlogs.Record) error {
			if count >= lines || builder.Len() >= bytes {
				return io.EOF
			}
			count++
			builder.WriteString(rec.Time.Format(time.RFC3339) + " " + rec.Text + "\n")
			return nil
		})
		if errors.Is(err, io.EOF) {
			err = nil
		}
		e.Content = redact.Bounded(builder.String(), bytes)
		return e, err
	}
	switch args.Kind {
	case "container":
		detail, err := runtime.Get(ctx, args.ID)
		if err != nil {
			return e, err
		}
		state, err := runtime.AIContainerState(ctx, detail.ID)
		if err != nil {
			return e, err
		}
		e.Content = jsonText(map[string]any{"id": detail.ID, "name": detail.Name, "image": detail.Image, "runtime_state": state, "status": detail.Status, "created": detail.Created, "restart_policy": detail.RestartPolicy, "project": detail.Labels["com.docker.compose.project"]})
	case "image":
		var local any
		imageID := args.ID
		if args.ID == "" || args.ID == "all" {
			rows, err := runtime.ListImages(ctx)
			if err != nil {
				return e, err
			}
			for i := range rows {
				rows[i].Labels = nil
			}
			local = rows
		} else {
			row, err := runtime.InspectImage(ctx, args.ID)
			if err != nil {
				return e, err
			}
			local = row
			imageID = row.ID
		}
		updates, err := r.imageUpdates.View(ctx, nodeID, "")
		if err != nil {
			e.Content = jsonText(map[string]any{"local": local, "missing": "Image update results unavailable; no registry check was started"})
			break
		}
		if args.ID != "" && args.ID != "all" {
			filtered := []imageupdate.Result{}
			for _, update := range updates.Results {
				if update.LocalImageID == imageID || update.Reference == args.ID {
					filtered = append(filtered, update)
				}
			}
			updates.Results = filtered
		}
		e.Content = jsonText(map[string]any{"local": local, "updates": updates, "note": "Uses existing registry checks and current Docker references; unchecked or stale results do not prove a new version"})
	case "network":
		row, err := runtime.InspectNetwork(ctx, args.ID)
		if err != nil {
			return e, err
		}
		e.Content = jsonText(row)
	case "volume":
		row, err := runtime.InspectVolume(ctx, args.ID)
		if err != nil {
			return e, err
		}
		e.Content = jsonText(row)
	case "project":
		svc, err := r.project(ctx, nodeID)
		if err != nil {
			return e, err
		}
		project, err := svc.Get(ctx, args.ID)
		if err != nil {
			return e, err
		}
		if !project.CanManage {
			draft, err := svc.BuildTakeoverDraft(ctx, project.Name)
			if err != nil {
				return e, err
			}
			e.Content = jsonText(map[string]any{"name": project.Name, "can_manage": false, "takeover_fingerprint": draft.Fingerprint, "compose": agentConfig(draft.Compose)})
			break
		}
		e.Content = jsonText(map[string]any{"name": project.Name, "revision": project.Revision, "services": project.Services, "containers": project.Containers, "can_manage": project.CanManage, "compose": agentConfig(project.Compose)})
	default:
		return e, ai.ErrInvalid
	}
	return e, nil
}

var pinnedReference = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9./_:\-]*@sha256:[a-f0-9]{64}$`)

func (r aiRuntime) Freeze(ctx context.Context, nodeID string, req ai.OperationRequest) (ai.Snapshot, error) {
	node, err := imageUpdateNode(r.nodes, r.db)(ctx, nodeID)
	if err != nil {
		return ai.Snapshot{}, err
	}
	if !node.Enabled || !node.Available {
		return ai.Snapshot{}, errors.New("authorized Docker node is unavailable")
	}
	runtime, err := r.nodes.Runtime(ctx, nodeID)
	if err != nil {
		return ai.Snapshot{}, err
	}
	snap := ai.Snapshot{RuntimeKey: node.RuntimeKey, Description: req.Action + " " + req.ResourceID}
	resolve := func(ctx context.Context, ref string) (string, error) {
		img, err := runtime.InspectImage(ctx, ref)
		return img.ID, err
	}
	var details any
	if hasExtendedAction(req.Action) {
		return r.freezeExtended(ctx, nodeID, req, snap)
	}
	if strings.HasPrefix(req.Action, "container.") {
		if !emptyParameters(req.Parameters) {
			return snap, ai.ErrInvalid
		}
		detail, err := runtime.Get(ctx, req.ResourceID)
		if err != nil {
			return snap, err
		}
		if detail.ID != req.ResourceID {
			return snap, errors.New("proposal requires the full immutable container ID")
		}
		state, err := runtime.AIContainerState(ctx, detail.ID)
		if err != nil {
			return snap, err
		}
		details = state
		snap.Impact = "Changes one container's running state. Stop/restart interrupts service. No data deletion. Recovery requires a separately approved start/restart."
	} else {
		switch req.Action {
		case "image.pull":
			if !emptyParameters(req.Parameters) || !pinnedReference.MatchString(req.ResourceID) {
				return snap, errors.New("pull requires a fixed sha256 digest reference")
			}
			details, _, err = r.pullConfiguration(ctx, nodeID, req.ResourceID, false)
			if err != nil {
				return snap, err
			}
			snap.Impact = "Downloads the fixed image digest, consumes storage and bandwidth, and does not recreate containers. Rebuild requires separate approval."
		case "project.up", "project.update":
			if !emptyParameters(req.Parameters) {
				return snap, ai.ErrInvalid
			}
			svc, err := r.project(ctx, nodeID)
			if err != nil {
				return snap, err
			}
			project, e := svc.Get(ctx, req.ResourceID)
			if e != nil {
				return snap, e
			}
			if e = svc.ValidateAgentSources(req.ResourceID, project.Compose); e != nil {
				return snap, e
			}
			review, err := svc.ReviewUpdate(ctx, req.ResourceID, resolve, true)
			if err != nil {
				return snap, err
			}
			state, err := runtime.InspectComposeProject(ctx, req.ResourceID)
			if err != nil {
				return snap, err
			}
			details = map[string]any{"review": review, "state_hash": stateHash(state)}
			if review.DockerSocket {
				snap.Confirmations = append(snap.Confirmations, ai.Confirmation{Key: "docker_socket", Label: "Docker socket grants full Engine control", Warning: "Socket access gives full control of the Docker Engine", Checkbox: true})
			}
			snap.Impact = "Recreates services from existing configuration with fixed local image IDs. Service interruption is possible; no implicit pull/build or automatic rollback. Recovery is a separate proposal."
		case "cd.deploy", "cd.retry", "cd.rollback":
			if !emptyParameters(req.Parameters) {
				return snap, ai.ErrInvalid
			}
			releaseNumber, err := strconv.ParseUint(req.ResourceID, 10, 32)
			if err != nil || releaseNumber == 0 {
				return snap, ai.ErrInvalid
			}
			review, err := r.cd.ReviewRelease(ctx, nodeID, uint(releaseNumber), req.Action == "cd.rollback", resolve)
			if err != nil {
				return snap, err
			}
			details = review
			snap.Impact = "Applies the frozen release to this node only, using fixed local images. May interrupt service. Pull/build and automatic rollback are disabled; retry/rollback requires new approval."
		case "cleanup.protected":
			var params struct {
				Candidates []cleanup.FrozenCandidate `json:"candidates"`
			}
			dec := json.NewDecoder(strings.NewReader(string(req.Parameters)))
			dec.DisallowUnknownFields()
			if dec.Decode(&params) != nil || req.ResourceID != nodeID {
				return snap, ai.ErrInvalid
			}
			review, err := r.cleanup.ReviewCandidates(ctx, nodeID, params.Candidates)
			if err != nil {
				return snap, err
			}
			details = review
			snap.Impact = "Permanently removes only the frozen stopped containers, unused images and networks. Rechecks references/protection before every deletion. Volumes and build cache are excluded. Deleted resources have no automatic undo."
			snap.Confirmations = []ai.Confirmation{{Key: "name", Label: "Type the node name to confirm cleanup", Expected: node.Name, Warning: "Frozen resources are permanently deleted and have no automatic recovery"}}
		default:
			return snap, ai.ErrInvalid
		}
	}
	snap.Details = json.RawMessage(jsonText(details))
	snap.Fingerprint = stateHash(details)
	return snap, nil
}
func (r aiRuntime) Execute(ctx context.Context, row database.AIOperation, snap ai.Snapshot, report task.Reporter) error {
	// Resolve and compare again at the adapter boundary. Never send an approved
	// action to a replacement Engine merely because it uses the same node ID.
	node, err := imageUpdateNode(r.nodes, r.db)(ctx, row.NodeID)
	if err != nil || !node.Enabled || !node.Available || node.RuntimeKey != snap.RuntimeKey {
		return ai.ErrConflict
	}
	runtime, err := r.nodes.Runtime(ctx, row.NodeID)
	if err != nil {
		return err
	}
	current, err := imageUpdateNode(r.nodes, r.db)(ctx, row.NodeID)
	if err != nil || current.RuntimeKey != snap.RuntimeKey {
		return ai.ErrConflict
	}
	resolve := func(ctx context.Context, ref string) (string, error) {
		img, err := runtime.InspectImage(ctx, ref)
		return img.ID, err
	}
	report(5, "Revalidated approved target")
	if hasExtendedAction(row.Action) {
		return r.executeExtended(ctx, row, snap, report)
	}
	switch row.Action {
	case "container.start":
		return runtime.Start(ctx, row.ResourceID)
	case "container.stop":
		return runtime.Stop(ctx, row.ResourceID)
	case "container.restart":
		return runtime.Restart(ctx, row.ResourceID)
	case "image.pull":
		details, material, err := r.pullConfiguration(ctx, row.NodeID, row.ResourceID, true)
		if err != nil || stateHash(details) != snap.Fingerprint {
			return ai.ErrConflict
		}
		var stream io.ReadCloser
		if material.ServerAddress == "" {
			stream, err = runtime.PullImage(ctx, row.ResourceID)
		} else {
			stream, err = runtime.PullImageAuthenticated(ctx, row.ResourceID, material.ServerAddress, material.Username, material.Secret, material.AuthType == credential.RegistryToken)
		}
		if err != nil {
			return errors.New("approved image pull failed; check the fixed digest and registry access")
		}
		defer stream.Close()
		dec := json.NewDecoder(stream)
		for {
			var msg struct {
				Error  string `json:"error"`
				Status string `json:"status"`
			}
			if err := dec.Decode(&msg); err != nil {
				if err == io.EOF {
					return nil
				}
				return err
			}
			if msg.Error != "" {
				return errors.New("approved image pull failed; check the fixed digest and registry access")
			}
			if msg.Status != "" {
				if material.Secret != "" {
					msg.Status = strings.ReplaceAll(msg.Status, material.Secret, "[redacted]")
				}
				report(50, msg.Status)
			}
		}
	case "project.up", "project.update":
		var data struct {
			Review composeService.ReviewedConfig `json:"review"`
		}
		if json.Unmarshal(snap.Details, &data) != nil {
			return ai.ErrInvalid
		}
		svc, err := r.project(ctx, row.NodeID)
		if err != nil {
			return err
		}
		return svc.ApplyUpdateReviewed(ctx, row.ResourceID, data.Review, report)
	case "cd.deploy", "cd.retry", "cd.rollback":
		var review cdService.ReviewedRelease
		if json.Unmarshal(snap.Details, &review) != nil {
			return ai.ErrInvalid
		}
		return r.cd.ApplyReleaseReviewed(ctx, review, resolve, row.TaskID, report)
	case "cleanup.protected":
		var review cleanup.ReviewedCleanup
		if json.Unmarshal(snap.Details, &review) != nil {
			return ai.ErrInvalid
		}
		return r.cleanup.ApplyCandidatesReviewed(ctx, row.NodeID, review, row.TaskID, cleanup.Actor{UserID: row.ApprovedBy}, report)
	}
	return ai.ErrInvalid
}

// Reuse an existing per-node registry mapping. Models cannot read, replace or
// create credentials; the selected credential identity is part of the preview.
func (r aiRuntime) pullConfiguration(ctx context.Context, nodeID, ref string, decrypt bool) (map[string]any, credential.RegistryMaterial, error) {
	details := map[string]any{"reference": ref, "credential_id": uint(0)}
	material := credential.RegistryMaterial{}
	parsed, err := name.ParseReference(ref, name.StrictValidation)
	if err != nil {
		return nil, material, ai.ErrInvalid
	}
	host := imageupdate.RegistryHost(parsed.Context().RegistryStr())
	var mapping database.ImageUpdateRegistryCredential
	err = r.db.WithContext(ctx).First(&mapping, "node_id = ? AND registry = ?", nodeID, host).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return details, material, nil
	}
	if err != nil {
		return nil, material, err
	}
	if r.registries == nil {
		return nil, material, ai.ErrScope
	}
	if err := r.registries.AuthorizedForNode(ctx, mapping.CredentialID, nodeID); err != nil {
		return nil, material, err
	}
	var row database.RegistryCredential
	if err := r.db.WithContext(ctx).First(&row, mapping.CredentialID).Error; err != nil {
		return nil, material, err
	}
	if imageupdate.RegistryHost(row.ServerAddress) != host {
		return nil, material, ai.ErrScope
	}
	details["credential_id"], details["credential_name"], details["credential_fingerprint"], details["credential_version"] = row.ID, row.Name, row.Fingerprint, row.UpdatedAt.UTC().Format(time.RFC3339Nano)
	if decrypt {
		material, err = r.registries.Material(ctx, row.ID)
		if err != nil {
			return nil, material, errors.New("registry credential unavailable")
		}
		var current database.RegistryCredential
		if r.db.WithContext(ctx).First(&current, row.ID).Error != nil || !current.UpdatedAt.Equal(row.UpdatedAt) || current.Fingerprint != row.Fingerprint {
			return nil, credential.RegistryMaterial{}, ai.ErrConflict
		}
	}
	return details, material, nil
}

func emptyParameters(raw json.RawMessage) bool {
	var fields map[string]json.RawMessage
	return json.Unmarshal(raw, &fields) == nil && fields != nil && len(fields) == 0
}

func agentConfig(content string) string {
	out, err := composeService.AgentConfig(content)
	if err != nil {
		return "Configuration unavailable"
	}
	return out
}
