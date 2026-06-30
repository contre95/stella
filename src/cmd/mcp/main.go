// Command mcp runs Stella's Model Context Protocol server over stdio, exposing
// card operations to MCP clients. It is a second composition root alongside the
// web server in src/main.go, wiring the same cards service onto the same SQLite
// database.
package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"math/rand"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/contre95/stella/src/features/cards"
	"github.com/contre95/stella/src/features/config"
	"github.com/contre95/stella/src/features/mcp"
	"github.com/contre95/stella/src/infra/database"
	"github.com/contre95/stella/src/srs"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const version = "0.1.0"

func main() {
	// Logs go to stderr: stdout is the JSON-RPC channel for the stdio transport,
	// so anything else written there corrupts the protocol stream.
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

	db, err := database.NewSqliteSRS(cfgManager.Get().Database.Path)
	if err != nil {
		slog.Error("failed to open database", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	scheduler := srs.NewScheduler(
		schedulerParams(cfgManager.Get()),
		time.Local,
		time.Now,
		srs.NewMathRand(rand.Int63()),
	)

	cardsService := cards.NewService(db, scheduler, cfgManager)
	srv := mcp.NewServer(cfgManager.Get().Server.AppName, version, cardsService)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	slog.Info("stella mcp server starting (stdio)")
	// Run blocks until the client disconnects or we're signalled. A client
	// closing the stdio stream surfaces as io.EOF and a signal as context
	// cancellation; both are normal shutdowns, not failures.
	err = srv.Run(ctx, &mcpsdk.StdioTransport{})
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) {
		slog.Error("mcp server stopped", "err", err)
		os.Exit(1)
	}
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
