// Command mcp runs Stella's Model Context Protocol server over HTTP
// (StreamableHTTP transport), exposing card operations to MCP clients.
// It is a second composition root alongside the web server in src/main.go,
// wiring the same cards service onto the same SQLite database.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/contre95/stella/src/features/cards"
	"github.com/contre95/stella/src/features/config"
	"github.com/contre95/stella/src/presenter/mcp"
	"github.com/contre95/stella/src/infra/database"
	"github.com/contre95/stella/src/srs"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const version = "0.1.0"

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	configPath := os.Getenv(config.EnvConfigPath)
	if configPath == "" {
		configPath = "config.yaml"
	}
	cfgManager, err := config.NewManager(configPath)
	if err != nil {
		slog.Error("failed to load config", "err", err)
		os.Exit(1)
	}

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

	cardsService := cards.NewService(db, scheduler, cfgManager)
	srv := mcp.NewServer(cfgManager.Get().Server.AppName, version, cardsService)

	handler := mcpsdk.NewStreamableHTTPHandler(func(_ *http.Request) *mcpsdk.Server {
		return srv
	}, nil)

	addr := fmt.Sprintf(":%d", cfgManager.Get().MCP.Port)
	httpServer := &http.Server{Addr: addr, Handler: handler}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		slog.Info("stella mcp server starting", "addr", addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("mcp server stopped", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down mcp server")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
	slog.Info("stella mcp server stopped")
}

// schedulerParams maps the review config onto the domain's scheduler params.
// Mirrors the web composition root in src/main.go.
func schedulerParams(c *config.Config) srs.Params {
	return srs.Params{
		DayStartsAt:              c.Review.DayStartsAt,
		NonMemorisedCardsInHand:  c.Review.NonMemorisedCardsInHand,
		MaxScheduledIntervalDays: c.Review.MaxScheduledIntervalDays,
		RandomiseScheduledCards:  c.Review.RandomiseScheduledCards,
		RandomiseNewCards:        c.Review.RandomiseNewCards,
	}
}
