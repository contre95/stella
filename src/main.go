package main

import (
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/contre95/stella/src/features/cards"
	"github.com/contre95/stella/src/features/config"
	"github.com/contre95/stella/src/features/hosting"
	"github.com/contre95/stella/src/features/logging"
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

	scheduler := srs.NewScheduler(
		schedulerParams(cfgManager.Get()),
		time.Local,
		time.Now,
		srs.NewMathRand(),
	)

	// 3. Feature services.
	cardsService := cards.NewService(db, scheduler, cfgManager)
	reviewService := review.NewService(db, scheduler)

	// 4. Handlers.
	uiHandler := ui.NewHandler(reviewService)
	cardsHandler := cards.NewHandler(cardsService)
	reviewHandler := review.NewHandler(reviewService, cfgManager)

	// 5. Routes + server.
	registrars := []hosting.RouteRegistrar{uiHandler, cardsHandler, reviewHandler}
	if cfgManager.Get().MCP.Enabled {
		registrars = append(registrars, mcppresenter.NewHTTPRegistrar(cfgManager.Get().Server.AppName, version, cardsService))
		slog.Info("mcp enabled", "path", "/mcp")
	}
	server := hosting.NewServer(cfgManager, registrars...)

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

// schedulerParams maps the review config onto the domain's scheduler params.
func schedulerParams(c *config.Config) srs.Params {
	return srs.Params{
		DayStartsAt:              c.Review.DayStartsAt,
		NonMemorisedCardsInHand:  c.Review.NonMemorisedCardsInHand,
		MaxScheduledIntervalDays: c.Review.MaxScheduledIntervalDays,
		RandomiseScheduledCards:  c.Review.RandomiseScheduledCards,
		RandomiseNewCards:        c.Review.RandomiseNewCards,
	}
}
