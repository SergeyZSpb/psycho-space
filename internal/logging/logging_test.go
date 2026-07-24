package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
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
