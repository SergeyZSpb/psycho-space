// Package logging configures the process-wide slog logger: JSON to stdout
// (captured by journald) and, when a log dir is set, additionally to a rotated
// file on disk (grep-friendly host logs).
package logging

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"gopkg.in/natefinch/lumberjack.v2"
)

// Setup builds the JSON logger and installs it as the slog default. If logDir is
// non-empty, logs are written to both stdout and logDir/app.log (rotated).
func Setup(logDir string, level slog.Level) *slog.Logger {
	var w io.Writer = os.Stdout
	if logDir != "" {
		rotator := &lumberjack.Logger{
			Filename:   filepath.Join(logDir, "app.log"),
			MaxSize:    50, // MB before rotation
			MaxBackups: 7,
			MaxAge:     30, // days
			Compress:   true,
		}
		w = io.MultiWriter(os.Stdout, rotator)
	}
	logger := slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)
	return logger
}
