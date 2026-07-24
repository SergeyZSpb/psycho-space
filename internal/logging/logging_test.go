package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestContextHandlerStampsAccountID(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(contextHandler{slog.NewJSONHandler(&buf, nil)})

	// No holder in context -> "anonymous".
	logger.InfoContext(context.Background(), "startup")

	// Holder installed + filled -> the account id, on every subsequent line.
	ctx := WithAccountHolder(context.Background())
	logger.InfoContext(ctx, "before-auth") // holder present but empty -> anonymous
	SetAccountID(ctx, "acc-42")
	logger.InfoContext(ctx, "after-auth")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 log lines, got %d", len(lines))
	}
	acct := func(s string) string {
		var m map[string]any
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			t.Fatalf("bad json line %q: %v", s, err)
		}
		v, _ := m["account_id"].(string)
		return v
	}
	if got := acct(lines[0]); got != "anonymous" {
		t.Fatalf("line 0 account_id = %q; want anonymous", got)
	}
	if got := acct(lines[1]); got != "anonymous" {
		t.Fatalf("line 1 (holder empty) account_id = %q; want anonymous", got)
	}
	if got := acct(lines[2]); got != "acc-42" {
		t.Fatalf("line 2 account_id = %q; want acc-42", got)
	}
}

// The trace id is what the user is shown and asked to quote, so EVERY line
// written while serving a traced request has to carry it — not just the
// http_request summary. Without this, a reported trace id finds the status code
// and nothing about why.
func TestContextHandlerStampsTraceID(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(contextHandler{slog.NewJSONHandler(&buf, nil)})

	// Untraced context: no trace_id key at all, rather than an empty one.
	logger.InfoContext(context.Background(), "startup")

	// A traced context stamps the span's trace id.
	tracer := sdktrace.NewTracerProvider().Tracer("test")
	ctx, span := tracer.Start(context.Background(), "request")
	defer span.End()
	logger.InfoContext(ctx, "in-request")
	want := span.SpanContext().TraceID().String()

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 log lines, got %d", len(lines))
	}
	field := func(s, key string) (string, bool) {
		var m map[string]any
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			t.Fatalf("bad json line %q: %v", s, err)
		}
		v, ok := m[key].(string)
		return v, ok
	}
	if _, ok := field(lines[0], "trace_id"); ok {
		t.Error("an untraced line should carry no trace_id at all")
	}
	if got, _ := field(lines[1], "trace_id"); got != want {
		t.Fatalf("trace_id = %q; want the span's %q", got, want)
	}
}
