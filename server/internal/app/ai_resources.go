package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/suma/suma/server/internal/ai"
	"github.com/suma/suma/server/internal/database"
)

func (r aiRuntime) Resources(ctx context.Context, nodeID string, in ai.ToolArgs) ([]ai.ResourceOption, error) {
	runtime, err := r.nodes.Runtime(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	out := []ai.ResourceOption{}
	add := func(id, name, kind, detail string) {
		if in.ID != "" && in.ID != id {
			return
		}
		if in.Query != "" && !strings.Contains(strings.ToLower(name), strings.ToLower(in.Query)) && id != in.Query {
			return
		}
		out = append(out, ai.ResourceOption{ID: id, Name: name, NodeID: nodeID, Kind: kind, Detail: detail})
	}
	switch in.Kind {
	case "container":
		rows, e := runtime.List(ctx)
		if e != nil {
			return nil, e
		}
		for _, row := range rows {
			add(row.ID, row.Name, "container", row.State)
		}
	case "image":
		rows, e := runtime.ListImages(ctx)
		if e != nil {
			return nil, e
		}
		for _, row := range rows {
			add(row.ID, strings.Join(row.Tags, ", "), "image", strings.Join(row.Digests, ", "))
		}
	case "network":
		rows, e := runtime.ListNetworks(ctx)
		if e != nil {
			return nil, e
		}
		for _, row := range rows {
			add(row.ID, row.Name, "network", row.Driver)
		}
	case "volume":
		rows, e := runtime.ListVolumes(ctx)
		if e != nil {
			return nil, e
		}
		for _, row := range rows {
			add(row.Name, row.Name, "volume", row.Driver)
		}
	case "project":
		svc, e := r.project(ctx, nodeID)
		if e != nil {
			return nil, e
		}
		rows, e := svc.List(ctx)
		if e != nil {
			return nil, e
		}
		for _, row := range rows {
			add(row.Name, row.Name, "project", row.Status)
		}
	case "task":
		var rows []database.Task
		if e := r.db.WithContext(ctx).Where("node_id = ?", nodeID).Order("created_at DESC").Limit(100).Find(&rows).Error; e != nil {
			return nil, e
		}
		for _, row := range rows {
			add(row.ID, row.Name, "task", row.Status)
		}
	case "cd":
		var rows []database.DeliveryReleaseDeployment
		if e := r.db.WithContext(ctx).Where("node_id = ?", nodeID).Order("updated_at DESC").Limit(100).Find(&rows).Error; e != nil {
			return nil, e
		}
		for _, row := range rows {
			add(fmt.Sprint(row.ReleaseID), fmt.Sprint(row.ReleaseID), "cd", row.Status)
		}
	case "node":
		node, e := r.nodes.Get(ctx, nodeID)
		if e != nil {
			return nil, e
		}
		add(node.ID, node.Name, "node", node.ConnectionType)
	default:
		return nil, ai.ErrInvalid
	}
	if len(out) > 200 {
		out = out[:200]
	}
	return out, nil
}
func (r aiRuntime) Draft(ctx context.Context, nodeID string, in ai.DraftInput) (ai.DraftResult, error) {
	svc, err := r.project(ctx, nodeID)
	if err != nil {
		return ai.DraftResult{}, err
	}
	content, revision, preview, err := svc.PrepareAgentDraft(ctx, in.Project, in.Compose, in.BaseRevision)
	return ai.DraftResult{Compose: content, BaseRevision: revision, Preview: json.RawMessage(preview)}, err
}
