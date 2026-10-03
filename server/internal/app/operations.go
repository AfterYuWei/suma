package app

import (
	"context"
	"fmt"
	"github.com/goccy/go-yaml"
	composeService "github.com/suma/suma/server/internal/compose"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/docker"
	"github.com/suma/suma/server/internal/imageupdate"
	nodeService "github.com/suma/suma/server/internal/node"
	"github.com/suma/suma/server/internal/projectlogs"
	"gorm.io/gorm"
	"time"
)

func imageUpdateNode(nodes *nodeService.Service, db *gorm.DB) func(context.Context, string) (imageupdate.Node, error) {
	return func(ctx context.Context, id string) (imageupdate.Node, error) {
		view, err := nodes.Get(ctx, id)
		credentialID := uint(0)
		credentialVersion := ""
		if view.TLSCredentialID != nil {
			credentialID = *view.TLSCredentialID
			var material database.DockerTLSCredential
			if db.WithContext(ctx).Select("updated_at").First(&material, credentialID).Error == nil {
				credentialVersion = material.UpdatedAt.Format(time.RFC3339Nano)
			}
		}
		return imageupdate.Node{ID: id, Name: view.Name, Enabled: view.Enabled, Available: view.Status == "online", RuntimeKey: fmt.Sprintf("%s|%s|%s|%s|%d|%d|%v|%s", view.Endpoint, view.ConnectionType, view.EngineID, view.TLSMode, credentialID, view.UpdatedAt.UnixNano(), view.AgentConnectedAt, credentialVersion)}, err
	}
}

type imageUpdateRuntime struct {
	adapter *docker.Adapter
	db      *gorm.DB
}

func (r imageUpdateRuntime) UpdateInventory(ctx context.Context) (imageupdate.Inventory, error) {
	inventory, err := r.adapter.UpdateInventory(ctx)
	if err != nil {
		return inventory, err
	}
	var projects []database.DeliveryProject
	if err := r.db.WithContext(ctx).Select("id", "name", "deployment_name").Find(&projects).Error; err != nil {
		return inventory, err
	}
	names := map[string]string{}
	for _, p := range projects {
		name := p.DeploymentName
		if name == "" {
			name = fmt.Sprintf("suma-cd-%d", p.ID)
		}
		names[name] = p.Name
	}
	for i := range inventory.Containers {
		inventory.Containers[i].DeliveryProject = names[inventory.Containers[i].Project]
	}
	return inventory, nil
}
func projectLogsService(nodes *nodeService.Service, compose *composeService.Service, runner *composeService.CLIRunner, db *gorm.DB) *projectlogs.Service {
	current := func(ctx context.Context, id string) (*composeService.Service, error) {
		target, view, err := nodes.ComposeTarget(ctx, id)
		if err != nil {
			return nil, err
		}
		adapter, err := nodes.Runtime(ctx, id)
		if err != nil {
			return nil, err
		}
		return compose.ForNode(id, view.Name, runner.ForTarget(target), adapter, view.ConnectionType == nodeService.ConnectionUnix), nil
	}
	return projectlogs.NewService(projectlogs.Dependencies{
		RuntimeKey: func(ctx context.Context, id string) (string, error) {
			n, err := imageUpdateNode(nodes, db)(ctx, id)
			return n.RuntimeKey, err
		},
		Runtime: func(ctx context.Context, id string) (projectlogs.Runtime, error) { return nodes.Runtime(ctx, id) },
		Exists: func(ctx context.Context, id, name string) error {
			svc, err := current(ctx, id)
			if err != nil {
				return err
			}
			_, err = svc.Get(ctx, name)
			return err
		},
		Declared: func(ctx context.Context, id, name string) (map[string]bool, error) {
			svc, err := current(ctx, id)
			if err != nil {
				return nil, err
			}
			p, err := svc.Get(ctx, name)
			if err != nil {
				return nil, err
			}
			if !p.Managed {
				return nil, nil
			}
			var source struct {
				Services map[string]any `yaml:"services"`
			}
			if yaml.Unmarshal([]byte(p.Compose), &source) != nil {
				return nil, nil
			}
			declared := map[string]bool{}
			for name := range source.Services {
				declared[name] = true
			}
			return declared, nil
		},
	})
}
