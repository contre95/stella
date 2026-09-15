package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	// Embed the IANA timezone database so config timezones resolve on hosts
	// without system zoneinfo (NixOS, scratch/alpine containers). System files
	// still take precedence when present.
	_ "time/tzdata"

	"github.com/contre95/stella/src/features/auth"
	"github.com/contre95/stella/src/features/cards"
	"github.com/contre95/stella/src/features/config"
	"github.com/contre95/stella/src/features/devicesync"
	"github.com/contre95/stella/src/features/hosting"
	"github.com/contre95/stella/src/features/logging"
	"github.com/contre95/stella/src/features/portability"
	"github.com/contre95/stella/src/features/review"
	"github.com/contre95/stella/src/features/ui"
	"github.com/contre95/stella/src/infra/database"
	mcppresenter "github.com/contre95/stella/src/presenter/mcp"
	"github.com/contre95/stella/src/srs"
)

const version = "default"

func main() {
	// 1. Config + logging.
	configPath := os.Getenv(config.EnvConfigPath)
	if configPath == "" {
		configPath = "config.yaml"
	}
	cfgManager, err := config.NewManager(configPath)
	if err != nil {
		slog.Error("failed to load config", "err", err)
		os.Exit(1)
	}
	slog.SetDefault(logging.SetupLogger(cfgManager))

	// 2. Infra: database + scheduler.
	db, err := database.NewSqliteSRS(cfgManager.Get().DbPath)
	if err != nil {
		slog.Error("failed to open database", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	location, err := cfgManager.Get().Location()
	if err != nil {
		slog.Error("invalid timezone in config", "timezone", cfgManager.Get().Timezone, "err", err)
		os.Exit(1)
	}

	scheduler := srs.NewScheduler(
		schedulerParams(cfgManager.Get()),
		location,
		time.Now,
		srs.NewMathRand(),
	)

	// 3. Feature services. Auth runs OIDC discovery in multi-user mode, so the
	// identity provider must be reachable at startup.
	authService, err := auth.NewService(context.Background(), db, cfgManager)
	if err != nil {
		slog.Error("failed to set up auth", "err", err)
		os.Exit(1)
	}
	cardsService := cards.NewService(db, scheduler, cfgManager)
	reviewService := review.NewService(db, scheduler)
	portabilityService := portability.NewService(cardsService)

	// 4. Handlers.
	uiHandler := ui.NewHandler(reviewService)
	cardsHandler := cards.NewHandler(cardsService)
	reviewHandler := review.NewHandler(reviewService, cfgManager)
	portabilityHandler := portability.NewHandler(portabilityService, cardsHandler)

	// 5. Routes + server.
	tokenHandler := authService.NewTokenHandler()
	oauthConsentHandler := authService.NewOAuthConsentHandler()
	deviceSyncHandler := devicesync.NewHandler(devicesync.NewService(db, devicesync.X4Normalizer), authService, location)
	registrars := []hosting.RouteRegistrar{uiHandler, cardsHandler, portabilityHandler, reviewHandler, tokenHandler, oauthConsentHandler, deviceSyncHandler}
	if cfgManager.Get().MCP.Enabled {
		registrars = append(registrars, mcppresenter.NewHTTPRegistrar(cfgManager.Get().Server.AppName, version, cardsService, authService))
		slog.Info("mcp enabled", "path", "/mcp")
	}
	server := hosting.NewServer(cfgManager, authService, registrars...)

	go func() {
		if err := server.Start(); err != nil {
			slog.Error("server stopped", "err", err)
		}
	}()
	slog.Info("stella started", "port", cfgManager.Get().Server.Port)

	// Graceful shutdown.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit
	slog.Info("shutting down")
	_ = server.Shutdown()
}

func schedulerParams(c *config.Config) srs.Params {
	return srs.Params{
		DayStartsAt:              c.Review.DayStartsAt,
		NonMemorisedCardsInHand:  c.Review.NonMemorisedCardsInHand,
		MaxScheduledIntervalDays: c.Review.MaxScheduledIntervalDays,
		RandomiseScheduledCards:  c.Review.RandomiseScheduledCards,
		RandomiseNewCards:        c.Review.RandomiseNewCards,
	}
}
