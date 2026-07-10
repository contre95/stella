package logging

import (
	"log/slog"
	"os"
	"time"

	"github.com/charmbracelet/log"
	"github.com/contre95/stella/src/features/config"
)

func SetupLogger(cm *config.Manager) *slog.Logger {
	cfg := cm.Get()

	var formatter log.Formatter
	switch cfg.Logger.Format {
	case "json":
		formatter = log.JSONFormatter
	case "text":
		formatter = log.TextFormatter
	default:
		formatter = log.LogfmtFormatter
	}

	level := log.InfoLevel
	switch cfg.Logger.Level {
	case "debug":
		level = log.DebugLevel
	case "warn":
		level = log.WarnLevel
	case "error":
		level = log.ErrorLevel
	}

	handler := log.NewWithOptions(os.Stderr, log.Options{
		ReportCaller:    true,
		ReportTimestamp: true,
		TimeFormat:      time.Kitchen,
		Prefix:          "Stella",
		Formatter:       formatter,
		Level:           level,
	})

	return slog.New(handler)
}
