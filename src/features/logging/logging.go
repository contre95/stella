package logging

import (
	"log/slog"
	"os"
	"strings"

	"github.com/contre95/stella/src/features/config"
)

// SetupLogger builds a structured slog logger from config.
func SetupLogger(cm *config.Manager) *slog.Logger {
	cfg := cm.Get()
	level := slog.LevelInfo
	switch strings.ToLower(cfg.Logger.Level) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	return slog.New(handler)
}
