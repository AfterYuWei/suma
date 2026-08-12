package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/dockport/dockport/server/internal/api"
	"github.com/dockport/dockport/server/internal/audit"
	"github.com/dockport/dockport/server/internal/auth"
	composeService "github.com/dockport/dockport/server/internal/compose"
	"github.com/dockport/dockport/server/internal/config"
	"github.com/dockport/dockport/server/internal/database"
	"github.com/dockport/dockport/server/internal/docker"
	imageService "github.com/dockport/dockport/server/internal/image"
	monitorService "github.com/dockport/dockport/server/internal/monitor"
	networkService "github.com/dockport/dockport/server/internal/network"
	settingsService "github.com/dockport/dockport/server/internal/settings"
	systemService "github.com/dockport/dockport/server/internal/system"
	"github.com/dockport/dockport/server/internal/task"
	volumeService "github.com/dockport/dockport/server/internal/volume"
)

type App struct {
	logger *slog.Logger
	server *http.Server
	engine docker.Engine
}

func New(logger *slog.Logger) (*App, error) {
	cfg := config.Load()
	db, err := database.Open(cfg.DatabasePath)
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
	settings := settingsService.NewService(db, cfg)
	monitor := monitorService.NewService(engine, cfg.DatabasePath)
	system := systemService.NewService(engine, taskService)
	return &App{logger: logger, engine: engine, server: &http.Server{Addr: cfg.Address, Handler: api.NewRouter(api.Dependencies{Engine: engine, Containers: engine, Auth: authService, Audit: auditService, Tasks: taskService, Images: images, Networks: networkService.Service(engine), Volumes: volumeService.Service(engine), Compose: compose, Settings: settings, Monitor: monitor, System: system, CookieSecure: cfg.CookieSecure}), ReadHeaderTimeout: 10_000_000_000}}, nil
}

func (a *App) Run() error {
	a.logger.Info("DockPort listening", "address", a.server.Addr)
	if err := a.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("listen: %w", err)
	}
	return nil
}

func (a *App) Shutdown(ctx context.Context) error {
	serverErr := a.server.Shutdown(ctx)
	engineErr := a.engine.Close()
	if serverErr != nil {
		return serverErr
	}
	return engineErr
}
