package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/suma/suma/server/internal/agenthub"
	"github.com/suma/suma/server/internal/ai"
	"github.com/suma/suma/server/internal/api"
	"github.com/suma/suma/server/internal/audit"
	"github.com/suma/suma/server/internal/auth"
	cdService "github.com/suma/suma/server/internal/cd"
	"github.com/suma/suma/server/internal/cleanup"
	composeService "github.com/suma/suma/server/internal/compose"
	"github.com/suma/suma/server/internal/config"
	"github.com/suma/suma/server/internal/containerfiles"
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
	"github.com/suma/suma/server/internal/projectlogs"
	"github.com/suma/suma/server/internal/registry"
	"github.com/suma/suma/server/internal/secret"
	settingsService "github.com/suma/suma/server/internal/settings"
	systemService "github.com/suma/suma/server/internal/system"
	"github.com/suma/suma/server/internal/task"
	volumeService "github.com/suma/suma/server/internal/volume"
)

type App struct {
	notifications  *notification.Service
	ai             *ai.Service
	imageUpdates   *imageupdate.Service
	projectLogs    *projectlogs.Service
	cleanup        *cleanup.Service
	logger         *slog.Logger
	server         *http.Server
	engine         docker.Engine
	nodes          *nodeService.Service
	agents         *agenthub.Hub
	cd             *cdService.Service
	recoveryCancel context.CancelFunc
	recoveryWG     sync.WaitGroup
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
	continuousDelivery := cdService.NewService(db, gitClient, gitCredentials, runner, taskService, auditService, secretStore)
	continuousDelivery.SetTargetResolver(nodes)
	continuousDelivery.SetRegistryCredentials(registryCredentials)
	if err := continuousDelivery.Recover(context.Background()); err != nil {
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
			return cleanup.Protection{cleanup.Image: append(refs.Images, images...), cleanup.Network: refs.Networks, cleanup.Volume: refs.Volumes}, nil
		},
	})
	if err := cleanupService.Recover(context.Background()); err != nil {
		return nil, fmt.Errorf("recover cleanup: %w", err)
	}
	if setupToken != "" {
		logger.Info("SUMA initialization key", "setup_token", setupToken, "expires_at", setupExpires.UTC().Format(time.RFC3339))
	}
	notifications := notification.NewService(db, secretStore, notification.Dependencies{Audit: auditService, OnError: func(err error) { logger.Warn("notification service", "error", err) }})
	auditService.SetSink(auditNotifications(db, notifications))
	continuousDelivery.SetEventSink(notifications.Emit)
	taskService.SetEventSink(func(e event.Event) {
		var row database.Task
		if db.First(&row, "id = ?", e.TaskID).Error == nil {
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
	})
	controlled := aiRuntime{secrets: secretStore, db: db, nodes: nodes, compose: compose, runner: runner, cd: continuousDelivery, cleanup: cleanupService, tasks: taskService, registries: registryCredentials, imageUpdates: imageUpdates}
	assistant, err := ai.NewService(db, secretStore, taskService, ai.Dependencies{Audit: auditService, Read: controlled.Read, Check: controlled.Check, Resources: controlled.Resources, Verify: controlled.Verify, Draft: controlled.Draft, Query: controlled.Query, Freeze: controlled.Freeze, Execute: controlled.Execute, Emit: notifications.Emit, ActorValid: func(ctx context.Context, a ai.Actor) error {
		if a.BindingID != "" {
			binding, err := notifications.ValidateBinding(ctx, a.UserID, a.BindingID)
			if err != nil || binding.ExternalUserID != a.ExternalUserID || binding.ChatID != a.ChatID {
				return ai.ErrScope
			}
			return nil
		}
		return nil
	}})
	if err != nil {
		return nil, err
	}
	notifications.SetEventHandler(assistant.OnEvent)
	notifications.SetChatHandler(chatOperations(notifications, assistant))
	assistant.SetChatDelivery(chatWorkflowDelivery(notifications, assistant))
	notifications.Start()
	imageUpdates.Start()
	projectLogs := projectLogsService(nodes, compose, runner, db)
	nodes.Start()
	continuousDelivery.Start()
	cleanupService.Start()
	recoveryContext, recoveryCancel := context.WithCancel(context.Background())
	fileService := containerfiles.NewService(db, secretStore)
	application := &App{notifications: notifications, ai: assistant, imageUpdates: imageUpdates, projectLogs: projectLogs, cleanup: cleanupService, logger: logger, engine: engine, nodes: nodes, agents: agents, cd: continuousDelivery, recoveryCancel: recoveryCancel, server: &http.Server{Addr: cfg.Address, Handler: api.NewRouter(api.Dependencies{Notifications: notifications, AI: assistant, ImageUpdates: imageUpdates, ProjectLogs: projectLogs, Cleanup: cleanupService, Engine: engine, Containers: engine, Files: fileService, Auth: authService, Audit: auditService, Tasks: taskService, Images: images, Networks: networkService.Service(engine), Volumes: volumeService.Service(engine), Compose: compose, ComposeRunner: runner, CD: continuousDelivery, GitCredentials: gitCredentials, RegistryCredentials: registryCredentials, Settings: applicationSettings, Monitor: monitorService.NewService(engine, cfg.DataRoot), System: systemService.NewService(engine, taskService), Nodes: nodes, Agents: agents, AgentPublicURL: cfg.AgentPublicURL, CookieSecure: cfg.CookieSecure}), ReadHeaderTimeout: 10_000_000_000}}
	application.recoveryWG.Add(1)
	go func() {
		defer application.recoveryWG.Done()
		assistant.ReconcileInterrupted(recoveryContext)
	}()
	application.recoveryWG.Add(1)
	go func() {
		defer application.recoveryWG.Done()
		observeNotifications(recoveryContext, nodes, db, notifications, assistant.Expire)
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
	if a.ai != nil {
		a.ai.Stop()
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
