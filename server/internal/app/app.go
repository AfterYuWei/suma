package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/suma/suma/server/internal/agenthub"
	"github.com/suma/suma/server/internal/api"
	"github.com/suma/suma/server/internal/audit"
	"github.com/suma/suma/server/internal/auth"
	cdService "github.com/suma/suma/server/internal/cd"
	"github.com/suma/suma/server/internal/cleanup"
	composeService "github.com/suma/suma/server/internal/compose"
	"github.com/suma/suma/server/internal/config"
	"github.com/suma/suma/server/internal/containerfiles"
	"github.com/suma/suma/server/internal/containermonitor"
	credentialService "github.com/suma/suma/server/internal/credential"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/docker"
	"github.com/suma/suma/server/internal/event"
	gitService "github.com/suma/suma/server/internal/git"
	imageService "github.com/suma/suma/server/internal/image"
	"github.com/suma/suma/server/internal/imageupdate"
	monitorService "github.com/suma/suma/server/internal/monitor"
	networkService "github.com/suma/suma/server/internal/network"
	nodeService "github.com/suma/suma/server/internal/node"
	"github.com/suma/suma/server/internal/notification"
	"github.com/suma/suma/server/internal/projectcd"
	"github.com/suma/suma/server/internal/projectlogs"
	"github.com/suma/suma/server/internal/registry"
	"github.com/suma/suma/server/internal/secret"
	settingsService "github.com/suma/suma/server/internal/settings"
	systemService "github.com/suma/suma/server/internal/system"
	"github.com/suma/suma/server/internal/task"
	volumeService "github.com/suma/suma/server/internal/volume"
)

type App struct {
	projectCD           *projectcd.Service
	containerMonitoring *containermonitor.Service
	notifications       *notification.Service
	imageUpdates        *imageupdate.Service
	projectLogs         *projectlogs.Service
	cleanup             *cleanup.Service
	logger              *slog.Logger
	server              *http.Server
	engine              docker.Engine
	nodes               *nodeService.Service
	agents              *agenthub.Hub
	cd                  *cdService.Service
	recoveryCancel      context.CancelFunc
	recoveryWG          sync.WaitGroup
}

func New(logger *slog.Logger) (*App, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	db, err := database.Open(cfg.DatabaseDSN)
	if err != nil {
		return nil, err
	}
	secretStore, err := secret.Open(cfg.SecretKeyFile)
	if err != nil {
		return nil, err
	}
	nodes, err := nodeService.NewService(db, secretStore, cfg.DockerHost)
	if err != nil {
		return nil, err
	}
	agents, err := agenthub.New()
	if err != nil {
		return nil, err
	}
	nodes.SetAgentHub(agents)
	engine, err := docker.New(cfg.DockerHost)
	if err != nil {
		return nil, err
	}
	authService := auth.NewService(db, cfg.SessionMaxAge, secretStore)
	setupToken, setupExpires, err := authService.PrepareSetup(context.Background())
	if err != nil {
		return nil, fmt.Errorf("prepare administrator initialization: %w", err)
	}
	auditService := audit.NewService(db)
	taskService := task.NewService(db)
	if err := taskService.RecoverInterrupted(context.Background()); err != nil {
		return nil, fmt.Errorf("recover tasks: %w", err)
	}
	images := imageService.NewService(engine, taskService)
	runner, err := composeService.NewRunner(cfg.ComposeCommand)
	if err != nil {
		return nil, err
	}
	compose, err := composeService.NewService(db, cfg.ComposeRoot, runner, taskService, engine)
	if err != nil {
		return nil, err
	}
	gitClient, err := gitService.NewCLIClient(cfg.GitCommand, cfg.GitRoot)
	if err != nil {
		return nil, err
	}
	gitCredentials := gitService.NewCredentialService(db, secretStore)
	registryCredentials := credentialService.NewRegistryService(db, secretStore)
	compose.SetRegistryCredentials(registryCredentials)
	continuousDelivery := cdService.NewService(db, gitClient, gitCredentials, runner, taskService, auditService, secretStore)
	continuousDelivery.SetTargetResolver(nodes)
	continuousDelivery.SetRegistryCredentials(registryCredentials)
	if err := continuousDelivery.Recover(context.Background()); err != nil {
		return nil, err
	}
	projectGitClient, err := gitService.NewCLIClient(cfg.GitCommand, filepath.Join(cfg.GitRoot, "gitops"))
	if err != nil {
		return nil, err
	}
	projectCD := projectcd.New(db, projectGitClient, gitCredentials, registryCredentials, runner, taskService, auditService, secretStore, projectcd.Dependencies{Inspect: nodes.InspectComposeNamespace,
		Project: func(ctx context.Context, id string) (*composeService.Service, error) {
			target, view, err := nodes.ComposeTarget(ctx, id)
			if err != nil {
				return nil, err
			}
			return compose.ForNode(id, view.Name, runner.ForTarget(target), nil, view.ConnectionType == nodeService.ConnectionUnix), nil
		},
		Target: func(ctx context.Context, id string) (composeService.Target, error) {
			target, _, err := nodes.ComposeTarget(ctx, id)
			return target, err
		},
	})
	compose.SetProjectCDAction(projectCD.Lifecycle)
	compose.SetProjectCDView(projectCD.ProjectView)
	if err := projectCD.Recover(context.Background()); err != nil {
		return nil, err
	}
	applicationSettings := settingsService.NewService(db, cfg)
	if err := applicationSettings.LoadSecurity(context.Background()); err != nil {
		return nil, fmt.Errorf("load security settings: %w", err)
	}
	cleanupService := cleanup.NewService(db, taskService, auditService, cleanup.Dependencies{
		OnError: func(err error) { logger.Warn("cleanup scheduler failed", "error", err) },
		Node: func(ctx context.Context, id string) (cleanup.Node, error) {
			view, err := nodes.Get(ctx, id)
			if err != nil {
				return cleanup.Node{}, err
			}
			current, err := imageUpdateNode(nodes, db)(ctx, id)
			return cleanup.Node{ID: view.ID, Name: view.Name, Enabled: view.Enabled, RuntimeKey: current.RuntimeKey}, err
		},
		Runtime: func(ctx context.Context, id string) (cleanup.Runtime, error) { return nodes.Runtime(ctx, id) },
		Timezone: func(ctx context.Context) string {
			values, err := applicationSettings.Get(ctx)
			if err != nil {
				return "UTC"
			}
			return values["general.timezone"]
		},
		Protection: func(ctx context.Context, id string) (cleanup.Protection, error) {
			target, view, err := nodes.ComposeTarget(ctx, id)
			if err != nil {
				return nil, err
			}
			current := compose.ForNode(id, view.Name, runner.ForTarget(target), nil, view.ConnectionType == nodeService.ConnectionUnix)
			refs, err := current.CleanupResourceReferences(ctx)
			if err != nil {
				return nil, err
			}
			images, err := continuousDelivery.CleanupImageReferences(ctx, id)
			if err != nil {
				return nil, err
			}
			gitrefs, err := projectCD.CleanupReferences(ctx, id)
			if err != nil {
				return nil, err
			}
			return cleanup.Protection{cleanup.Image: append(append(refs.Images, images...), gitrefs.Images...), cleanup.Network: append(refs.Networks, gitrefs.Networks...), cleanup.Volume: append(refs.Volumes, gitrefs.Volumes...)}, nil
		},
	})
	if err := cleanupService.Recover(context.Background()); err != nil {
		return nil, fmt.Errorf("recover cleanup: %w", err)
	}
	if setupToken != "" {
		logger.Info("SUMA initialization key", "setup_token", setupToken, "expires_at", setupExpires.UTC().Format(time.RFC3339))
	}
	notifications := notification.NewService(db, secretStore, notification.Dependencies{OnError: func(err error) { logger.Warn("notification service", "error", err) }})
	auditService.SetSink(auditNotifications(db, notifications.Emit))
	continuousDelivery.SetEventSink(notifications.Emit)
	taskService.SetEventSink(func(e event.Event) {
		var row database.Task
		if db.First(&row, "id = ?", e.TaskID).Error == nil {
			if row.Type == "image.updates.check" {
				return
			}
			if strings.HasPrefix(row.Type, "compose.") {
				e.Type = "project.completed"
			}
			if strings.HasPrefix(row.Type, "image.pull") && e.Type == "task.failed" {
				e.Type = "image.pull_failed"
			}
		}
		if e.Type != "task.completed" {
			notifications.Emit(e)
		}
	})
	imageUpdates := imageupdate.NewService(db, taskService, auditService, imageupdate.Dependencies{
		Emit: notifications.Emit,
		Node: imageUpdateNode(nodes, db),
		Runtime: func(ctx context.Context, id string) (imageupdate.Runtime, error) {
			adapter, err := nodes.Runtime(ctx, id)
			if err != nil {
				return nil, err
			}
			return imageUpdateRuntime{adapter: adapter, db: db}, nil
		},
		Resolver: registry.Adapter{}, Credentials: registryCredentials,
		Compose: func(ctx context.Context, id string) (*composeService.Service, error) {
			return imageUpdateCompose(ctx, nodes, compose, runner, id)
		},
		Projects: func(ctx context.Context, id string) ([]composeService.Project, error) {
			svc, err := imageUpdateCompose(ctx, nodes, compose, runner, id)
			if err != nil {
				return nil, err
			}
			return svc.List(ctx)
		},
		Nodes: func(ctx context.Context) ([]string, error) {
			rows, err := nodes.List(ctx)
			ids := []string{}
			for _, row := range rows {
				ids = append(ids, row.ID)
			}
			return ids, err
		},
		OnError: func(err error) { logger.Warn("project image update", "error", err) },
	})
	notifications.SetProjectUpdates(imageUpdates)
	containerMonitoring, err := containermonitor.New(db, taskService, containermonitor.Dependencies{
		Nodes: func(ctx context.Context) ([]containermonitor.Node, error) {
			views, err := nodes.List(ctx)
			if err != nil {
				return nil, err
			}
			out := make([]containermonitor.Node, 0, len(views))
			for _, view := range views {
				current, err := imageUpdateNode(nodes, db)(ctx, view.ID)
				if err != nil {
					continue
				}
				out = append(out, containermonitor.Node{ID: view.ID, Name: view.Name, EngineID: view.EngineID, Enabled: view.Enabled, Agent: view.ConnectionType == nodeService.ConnectionAgent, RuntimeKey: current.RuntimeKey})
			}
			return out, nil
		},
		Runtime: func(ctx context.Context, id string) (containermonitor.Runtime, error) { return nodes.Runtime(ctx, id) },
	})
	if err != nil {
		return nil, fmt.Errorf("initialize container monitoring: %w", err)
	}
	containerMonitoring.Start()
	notifications.Start()
	imageUpdates.Start()
	projectLogs := projectLogsService(nodes, compose, runner, db)
	nodes.Start()
	continuousDelivery.Start()
	projectCD.Start()
	cleanupService.Start()
	recoveryContext, recoveryCancel := context.WithCancel(context.Background())
	fileService := containerfiles.NewService(db, secretStore)
	application := &App{projectCD: projectCD, containerMonitoring: containerMonitoring, notifications: notifications, imageUpdates: imageUpdates, projectLogs: projectLogs, cleanup: cleanupService, logger: logger, engine: engine, nodes: nodes, agents: agents, cd: continuousDelivery, recoveryCancel: recoveryCancel, server: &http.Server{Addr: cfg.Address, Handler: api.NewRouter(api.Dependencies{ProjectCD: projectCD, ContainerMonitoring: containerMonitoring, Notifications: notifications, ImageUpdates: imageUpdates, ProjectLogs: projectLogs, Cleanup: cleanupService, Engine: engine, Containers: engine, Files: fileService, Auth: authService, Audit: auditService, Tasks: taskService, Images: images, Networks: networkService.Service(engine), Volumes: volumeService.Service(engine), Compose: compose, ComposeRunner: runner, CD: continuousDelivery, GitCredentials: gitCredentials, RegistryCredentials: registryCredentials, Settings: applicationSettings, Monitor: monitorService.NewService(engine, cfg.DataRoot), System: systemService.NewService(engine, taskService), Nodes: nodes, Agents: agents, AgentPublicURL: cfg.AgentPublicURL, CookieSecure: cfg.CookieSecure}), ReadHeaderTimeout: 10_000_000_000}}
	application.recoveryWG.Add(1)
	go func() {
		defer application.recoveryWG.Done()
		observeNotifications(recoveryContext, nodes, db, notifications)
	}()
	application.recoveryWG.Add(1)
	go func() {
		defer application.recoveryWG.Done()
		if err := fileService.PruneAll(recoveryContext); err != nil {
			logger.Warn("prune container file history", "error", err)
		}
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-recoveryContext.Done():
				return
			case <-ticker.C:
				if err := fileService.PruneAll(recoveryContext); err != nil {
					logger.Warn("prune container file history", "error", err)
				}
			}
		}
	}()
	application.recoveryWG.Add(1)
	go func() {
		defer application.recoveryWG.Done()
		recoverShadowPreviews(recoveryContext, logger, nodes, compose, runner)
	}()
	return application, nil
}

func recoverShadowPreviews(ctx context.Context, logger *slog.Logger, nodes *nodeService.Service, service *composeService.Service, runner *composeService.CLIRunner) {
	views, err := nodes.List(ctx)
	if err != nil {
		logger.Warn("list nodes for shadow preview recovery", "error", err)
		return
	}
	for _, view := range views {
		if !view.Enabled {
			continue
		}
		target, _, err := nodes.ComposeTarget(ctx, view.ID)
		if err != nil {
			logger.Warn("resolve node for shadow preview recovery", "node_id", view.ID, "error", err)
			continue
		}
		recoveryContext, cancel := context.WithTimeout(ctx, 30*time.Second)
		current := service.ForNode(view.ID, view.Name, runner.ForTarget(target), nil, view.ConnectionType == nodeService.ConnectionUnix)
		err = current.RecoverShadowPreviews(recoveryContext)
		cancel()
		if err != nil {
			logger.Warn("recover shadow previews", "node_id", view.ID, "error", err)
		}
	}
}

func (a *App) Run() error {
	a.logger.Info("SUMA listening", "address", a.server.Addr)
	if err := a.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("listen: %w", err)
	}
	return nil
}

func (a *App) Shutdown(ctx context.Context) error {
	if a.containerMonitoring != nil {
		a.containerMonitoring.Stop()
	}
	if a.projectLogs != nil {
		a.projectLogs.Stop()
	}
	if a.imageUpdates != nil {
		a.imageUpdates.Stop()
	}
	if a.cleanup != nil {
		a.cleanup.Stop()
	}
	if a.recoveryCancel != nil {
		a.recoveryCancel()
		a.recoveryWG.Wait()
	}
	if a.projectCD != nil {
		a.projectCD.Stop()
	}
	if a.cd != nil {
		a.cd.Stop()
	}
	if a.notifications != nil {
		a.notifications.Stop()
	}
	serverErr := a.server.Shutdown(ctx)
	engineErr := a.engine.Close()
	if a.agents != nil {
		_ = a.agents.Close()
	}
	if a.nodes != nil {
		_ = a.nodes.Close()
	}
	if serverErr != nil {
		return serverErr
	}
	return engineErr
}
