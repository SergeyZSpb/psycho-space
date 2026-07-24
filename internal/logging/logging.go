// Package logging configures the process-wide slog logger: JSON to stdout
// (captured by journald) and, when a log dir is set, additionally to a rotated
// file on disk (grep-friendly host logs).
package logging

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"gopkg.in/natefinch/lumberjack.v2"
)

type ctxKey struct{}

// accountHolder is a mutable, per-request cell for the account id. A pointer so
// middleware can fill it after logging has already begun; every log emitted with
// the request context (or a child) then reads the current value.
type accountHolder struct{ id string }

// WithAccountHolder installs a fresh account-id holder on ctx. Call once per
// request; SetAccountID fills it when the account is resolved.
func WithAccountHolder(ctx context.Context) context.Context {
	return context.WithValue(ctx, ctxKey{}, &accountHolder{})
}

// SetAccountID records the account id on the request's holder (no-op if absent).
func SetAccountID(ctx context.Context, id string) {
	if h, ok := ctx.Value(ctxKey{}).(*accountHolder); ok {
		h.id = id
	}
}

func accountID(ctx context.Context) string {
	if h, ok := ctx.Value(ctxKey{}).(*accountHolder); ok && h.id != "" {
		return h.id
	}
	return "anonymous"
}

// contextHandler stamps account_id (from the context holder, or "anonymous") on
// every record, so all log lines carry it.
type contextHandler struct{ slog.Handler }

func (h contextHandler) Handle(ctx context.Context, r slog.Record) error {
	r.AddAttrs(slog.String("account_id", accountID(ctx)))
	return h.Handler.Handle(ctx, r)
}

func (h contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return contextHandler{h.Handler.WithAttrs(attrs)}
}

func (h contextHandler) WithGroup(name string) slog.Handler {
	return contextHandler{h.Handler.WithGroup(name)}
}

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
	base := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})
	logger := slog.New(contextHandler{base})
	slog.SetDefault(logger)
	return logger
}
