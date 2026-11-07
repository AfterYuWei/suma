package app

import (
	"context"
	"fmt"
	"time"

	"github.com/goccy/go-yaml"
	composeService "github.com/suma/suma/server/internal/compose"
	"github.com/suma/suma/server/internal/database"
	nodeService "github.com/suma/suma/server/internal/node"
	"github.com/suma/suma/server/internal/projectlogs"
	"gorm.io/gorm"
)

// nodeRuntimeKey derives the cache key that binds per-node observations to the
// current runtime configuration (endpoint, engine identity and TLS material).
func nodeRuntimeKey(nodes *nodeService.Service, db *gorm.DB) func(context.Context, string) (string, error) {
	return func(ctx context.Context, id string) (string, error) {
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
		return fmt.Sprintf("%s|%s|%s|%s|%d|%d|%v|%s", view.Endpoint, view.ConnectionType, view.EngineID, view.TLSMode, credentialID, view.UpdatedAt.UnixNano(), view.AgentConnectedAt, credentialVersion), err
	}
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
			return nodeRuntimeKey(nodes, db)(ctx, id)
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
