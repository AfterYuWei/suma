package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

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
	logger *slog.Logger
	server *http.Server
	engine docker.Engine
	nodes  *nodeService.Service
	cd     *cdService.Service
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
	settings := settingsService.NewService(db, cfg)
	monitor := monitorService.NewService(engine, cfg.DatabasePath)
	system := systemService.NewService(engine, taskService)
	return &App{logger: logger, engine: engine, nodes: nodes, cd: continuousDelivery, server: &http.Server{Addr: cfg.Address, Handler: api.NewRouter(api.Dependencies{Engine: engine, Containers: engine, Auth: authService, Audit: auditService, Tasks: taskService, Images: images, Networks: networkService.Service(engine), Volumes: volumeService.Service(engine), Compose: compose, ComposeRunner: runner, CD: continuousDelivery, GitCredentials: gitCredentials, RegistryCredentials: registryCredentials, Settings: settings, Monitor: monitor, System: system, Nodes: nodes, CookieSecure: cfg.CookieSecure}), ReadHeaderTimeout: 10_000_000_000}}, nil
}

func (a *App) Run() error {
	a.logger.Info("SUMA listening", "address", a.server.Addr)
	if err := a.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("listen: %w", err)
	}
	return nil
}

func (a *App) Shutdown(ctx context.Context) error {
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
