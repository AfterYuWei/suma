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
	"github.com/suma/suma/server/internal/taskcenter"
	volumeService "github.com/suma/suma/server/internal/volume"
)

type App struct {
	projectCD           *projectcd.Service
	containerMonitoring *containermonitor.Service
	notifications       *notification.Service
	projectLogs         *projectlogs.Service
	cleanup             *cleanup.Service
	taskCenter          *taskcenter.Service
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
	taskCenterKinds := taskcenter.NewRegistry()
	taskCenter := taskcenter.New(db, taskCenterKinds, taskcenter.Dependencies{
		OnError: func(err error) { logger.Warn("task center scheduler failed", "error", err) },
		NodeName: func(ctx context.Context, nodeID string) (string, error) {
			view, err := nodes.Get(ctx, nodeID)
			if err != nil {
				return "", err
			}
			return view.Name, nil
		},
	})
	imageUpdates := imageupdate.NewService(db, taskService, imageupdate.Dependencies{
		Audit:      auditService,
		Automation: taskCenter,
		ConfigRevision: func(ctx context.Context, nodeID string, params imageupdate.Params) (string, error) {
			view, err := nodes.Get(ctx, nodeID)
			if err != nil {
				return "", err
			}
			current, err := compose.ForNode(nodeID, "", runner, nil, false).ManagedConfiguration(params.Project)
			if err != nil {
				return "", err
			}
			if params.AutoApply {
				if err := composeService.ValidateComposeBindMounts(current.Compose, view.ConnectionType != nodeService.ConnectionUnix, params.AllowDockerSocket); err != nil {
					return "", fmt.Errorf("%w: %s", taskcenter.ErrInvalidParams, err)
				}
			}
			return current.Revision, nil
		},
		Node: func(ctx context.Context, id string) (imageupdate.Node, error) {
			view, err := nodes.Get(ctx, id)
			if err != nil {
				return imageupdate.Node{}, err
			}
			return imageupdate.Node{ID: view.ID, Name: view.Name, Enabled: view.Enabled}, nil
		},
		Runtime:     func(ctx context.Context, id string) (imageupdate.Runtime, error) { return nodes.Runtime(ctx, id) },
		Resolver:    registry.Adapter{},
		Credentials: registryCredentials,
		Compose: func(ctx context.Context, id string) (*composeService.Service, error) {
			target, view, err := nodes.ComposeTarget(ctx, id)
			if err != nil {
				return nil, err
			}
			adapter, err := nodes.Runtime(ctx, id)
			if err != nil {
				return nil, err
			}
			return compose.ForNode(id, view.Name, runner.ForTarget(target), adapter, view.ConnectionType == nodeService.ConnectionUnix), nil
		},
		OnError: func(err error) { logger.Warn("image update service", "error", err) },
	})
	if err := taskCenterKinds.Register(imageUpdates.Kind()); err != nil {
		return nil, fmt.Errorf("register image update kind: %w", err)
	}
	compose.SetScheduleRemoval(func(ctx context.Context, nodeID, subject string) error {
		return taskCenter.DeleteSchedules(ctx, imageupdate.KindImageUpdate, nodeID, subject)
	})
	continuousDelivery := cdService.NewService(db, gitClient, gitCredentials, runner, taskService, auditService, secretStore)
	continuousDelivery.SetTaskCenter(taskCenter)
	if err := taskCenterKinds.Register(continuousDelivery.AutomationKind()); err != nil {
		return nil, err
	}
	continuousDelivery.SetTargetResolver(nodes)
	continuousDelivery.SetRegistryCredentials(registryCredentials)
	if err := continuousDelivery.Recover(context.Background()); err != nil {
		return nil, err
	}
	projectGitClient, err := gitService.NewCLIClient(cfg.GitCommand, filepath.Join(cfg.GitRoot, "gitops"))
	if err != nil {
		return nil, err
	}
	projectCD := projectcd.New(db, projectGitClient, gitCredentials, registryCredentials, runner, taskService, auditService, secretStore, projectcd.Dependencies{Inspect: nodes.InspectComposeNamespace, PrepareTarget: nodes.ResolveDeploymentTarget,
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
	projectCD.SetTaskCenter(taskCenter)
	if err := taskCenterKinds.Register(projectCD.AutomationKind()); err != nil {
		return nil, err
	}
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
			current, err := nodeRuntimeKey(nodes, db)(ctx, id)
			return cleanup.Node{ID: view.ID, Name: view.Name, Enabled: view.Enabled, RuntimeKey: current}, err
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
	cleanupService.SetTaskCenter(taskCenter)
	if err := taskCenterKinds.Register(cleanupService.AutomationKind()); err != nil {
		return nil, err
	}
	if err := cleanupService.Recover(context.Background()); err != nil {
		return nil, fmt.Errorf("recover cleanup: %w", err)
	}
	if err := taskCenter.BackfillBusinessRules(context.Background(), cleanupService.BackfillAutomation, continuousDelivery.BackfillAutomation, projectCD.BackfillAutomation); err != nil {
		return nil, fmt.Errorf("convert automation rules: %w", err)
	}
	if setupToken != "" {
		logger.Info("SUMA initialization key", "setup_token", setupToken, "expires_at", setupExpires.UTC().Format(time.RFC3339))
	}
	notifications := notification.NewService(db, secretStore, notification.Dependencies{OnError: func(err error) { logger.Warn("notification service", "error", err) }})
	imageUpdates.SetEventSink(notifications.Emit)
	auditService.SetSink(auditNotifications(db, notifications.Emit))
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
	containerMonitoring, err := containermonitor.New(db, taskService, containermonitor.Dependencies{
		Nodes: func(ctx context.Context) ([]containermonitor.Node, error) {
			views, err := nodes.List(ctx)
			if err != nil {
				return nil, err
			}
			out := make([]containermonitor.Node, 0, len(views))
			for _, view := range views {
				current, err := nodeRuntimeKey(nodes, db)(ctx, view.ID)
				if err != nil {
					continue
				}
				out = append(out, containermonitor.Node{ID: view.ID, Name: view.Name, EngineID: view.EngineID, Enabled: view.Enabled, Agent: view.ConnectionType == nodeService.ConnectionAgent, RuntimeKey: current})
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
	projectLogs := projectLogsService(nodes, compose, runner, db)
	nodes.Start()
	if err := taskCenter.Start(); err != nil {
		return nil, fmt.Errorf("start task center: %w", err)
	}
	recoveryContext, recoveryCancel := context.WithCancel(context.Background())
	fileService := containerfiles.NewService(db, secretStore)
	application := &App{projectCD: projectCD, containerMonitoring: containerMonitoring, notifications: notifications, projectLogs: projectLogs, cleanup: cleanupService, taskCenter: taskCenter, logger: logger, engine: engine, nodes: nodes, agents: agents, cd: continuousDelivery, recoveryCancel: recoveryCancel, server: &http.Server{Addr: cfg.Address, Handler: api.NewRouter(api.Dependencies{ProjectCD: projectCD, ContainerMonitoring: containerMonitoring, Notifications: notifications, ProjectLogs: projectLogs, Cleanup: cleanupService, Engine: engine, Containers: engine, Files: fileService, Auth: authService, Audit: auditService, Tasks: taskService, Images: images, Networks: networkService.Service(engine), Volumes: volumeService.Service(engine), Compose: compose, ComposeRunner: runner, CD: continuousDelivery, GitCredentials: gitCredentials, RegistryCredentials: registryCredentials, Settings: applicationSettings, Monitor: monitorService.NewService(engine, cfg.DataRoot), System: systemService.NewService(engine, taskService), Nodes: nodes, TaskCenter: taskCenter, TaskCenterKinds: taskCenterKinds, ImageUpdates: imageUpdates, Agents: agents, AgentPublicURL: cfg.AgentPublicURL, CookieSecure: cfg.CookieSecure}), ReadHeaderTimeout: 10_000_000_000}}
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
	if a.taskCenter != nil {
		a.taskCenter.Stop()
	}
	if a.containerMonitoring != nil {
		a.containerMonitoring.Stop()
	}
	if a.projectLogs != nil {
		a.projectLogs.Stop()
	}
	if a.cleanup != nil {
		a.cleanup.Stop()
	}
	if a.recoveryCancel != nil {
		a.recoveryCancel()
		a.recoveryWG.Wait()
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
