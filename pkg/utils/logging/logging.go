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
	level := LogLevel()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	})).With("node_id", nodeId)
	slog.SetDefault(logger)
}

func LogLevel() slog.Level {
	level := strings.ToLower(os.Getenv("LOG_LEVEL"))

	logLevel, ok := logLevelMapping[level]
	if !ok {
		logLevel = slog.LevelInfo
	}
	return logLevel
}
