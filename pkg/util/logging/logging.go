package logging

import (
	"log/slog"
	"os"
	"strings"
)

var logLevelMapping = map[string]slog.Level{
	"debug": slog.LevelDebug,
	"info":  slog.LevelInfo,
	"warn":  slog.LevelWarn,
	"error": slog.LevelError,
}

func InitDefault(nodeId string) {
	level := strings.ToLower(os.Getenv("LOG_LEVEL"))

	logLevel, ok := logLevelMapping[level]
	if !ok {
		logLevel = slog.LevelInfo
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	})).With("node_id", nodeId)
	slog.SetDefault(logger)
}
