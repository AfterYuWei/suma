package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/suma/suma/server/internal/api"
	"github.com/suma/suma/server/internal/audit"
	"github.com/suma/suma/server/internal/auth"
	cdService "github.com/suma/suma/server/internal/cd"
	composeService "github.com/suma/suma/server/internal/compose"
	"github.com/suma/suma/server/internal/config"
	credentialService "github.com/suma/suma/server/internal/credential"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/docker"
	gitService "github.com/suma/suma/server/internal/git"
	imageService "github.com/suma/suma/server/internal/image"
	monitorService "github.com/suma/suma/server/internal/monitor"
	networkService "github.com/suma/suma/server/internal/network"
	nodeService "github.com/suma/suma/server/internal/node"
	"github.com/suma/suma/server/internal/secret"
	settingsService "github.com/suma/suma/server/internal/settings"
	systemService "github.com/suma/suma/server/internal/system"
	"github.com/suma/suma/server/internal/task"
	volumeService "github.com/suma/suma/server/internal/volume"
)

type App struct {
	logger         *slog.Logger
	server         *http.Server
	engine         docker.Engine
	nodes          *nodeService.Service
	cd             *cdService.Service
	recoveryCancel context.CancelFunc
	recoveryWG     sync.WaitGroup
}

func New(logger *slog.Logger) (*App, error) {
	cfg := config.Load()
	db, err := database.Open(cfg.DatabasePath)
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
	engine, err := docker.New(cfg.DockerHost)
	if err != nil {
		return nil, err
	}
	authService := auth.NewService(db, cfg.SessionMaxAge)
	auditService := audit.NewService(db)
	taskService := task.NewService(db)
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
	nodes.Start()
	continuousDelivery.Start()
	recoveryContext, recoveryCancel := context.WithCancel(context.Background())
	application := &App{logger: logger, engine: engine, nodes: nodes, cd: continuousDelivery, recoveryCancel: recoveryCancel, server: &http.Server{Addr: cfg.Address, Handler: api.NewRouter(api.Dependencies{Engine: engine, Containers: engine, Auth: authService, Audit: auditService, Tasks: taskService, Images: images, Networks: networkService.Service(engine), Volumes: volumeService.Service(engine), Compose: compose, ComposeRunner: runner, CD: continuousDelivery, GitCredentials: gitCredentials, RegistryCredentials: registryCredentials, Settings: settingsService.NewService(db, cfg), Monitor: monitorService.NewService(engine, cfg.DatabasePath), System: systemService.NewService(engine, taskService), Nodes: nodes, CookieSecure: cfg.CookieSecure}), ReadHeaderTimeout: 10_000_000_000}}
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
	if a.recoveryCancel != nil {
		a.recoveryCancel()
		a.recoveryWG.Wait()
	}
	if a.cd != nil {
		a.cd.Stop()
	}
	serverErr := a.server.Shutdown(ctx)
	engineErr := a.engine.Close()
	if a.nodes != nil {
		_ = a.nodes.Close()
	}
	if serverErr != nil {
		return serverErr
	}
	return engineErr
}
