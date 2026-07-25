package realtime

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// fakeSink stands in for a WebSocket. Every hub property worth testing —
// especially backpressure — is reachable this way and awkward or impossible to
// drive reliably through a real socket.
type fakeSink struct {
	id      string
	account string

	mu       sync.Mutex
	got      [][]byte
	full     bool // when true, TrySend always reports "buffer full"
	closed   bool
	closeArg int
	closeMsg string
}

func newFakeSink(id, account string) *fakeSink {
	return &fakeSink{id: id, account: account}
}

func (f *fakeSink) ID() string        { return f.id }
func (f *fakeSink) AccountID() string { return f.account }

func (f *fakeSink) TrySend(msg []byte) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.full {
		return false
	}
	f.got = append(f.got, msg)
	return true
}

func (f *fakeSink) Close(code int, reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return
	}
	f.closed, f.closeArg, f.closeMsg = true, code, reason
}

func (f *fakeSink) received() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.got)
}

func (f *fakeSink) closeState() (bool, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed, f.closeArg
}

// startHub runs a hub for the test's lifetime and returns it.
func startHub(t *testing.T) (*Hub, context.CancelFunc) {
	t.Helper()
	h := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	go h.Run(ctx)
	t.Cleanup(func() {
		cancel()
		select {
		case <-h.Done():
		case <-time.After(2 * time.Second):
			t.Error("hub did not stop after context cancellation")
		}
	})
	return h, cancel
}

// waitFor polls a condition instead of sleeping — the hub is a goroutine, so
// "has it processed my command yet" is a condition, not a duration.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestHubFanoutIsScopedToTheRoom(t *testing.T) {
	h, _ := startHub(t)

	a, b, other := newFakeSink("a", "acc-1"), newFakeSink("b", "acc-2"), newFakeSink("c", "acc-3")
	for _, s := range []*fakeSink{a, b} {
		if err := h.Register(context.Background(), s, "yard"); err != nil {
			t.Fatalf("register %s: %v", s.ID(), err)
		}
	}
	if err := h.Register(context.Background(), other, "elsewhere"); err != nil {
		t.Fatalf("register other: %v", err)
	}

	if err := h.Publish(context.Background(), "yard", []byte(`{"t":"hi"}`)); err != nil {
		t.Fatalf("publish: %v", err)
	}

	waitFor(t, "both yard members to receive", func() bool {
		return a.received() == 1 && b.received() == 1
	})
	if other.received() != 0 {
		t.Fatalf("a client in another room received %d messages, want 0", other.received())
	}
}

// TestHubDoesNotBlockOnASlowClient is the property that matters most: one phone
// on a bad connection must not freeze the room for everyone else.
func TestHubDoesNotBlockOnASlowClient(t *testing.T) {
	h, _ := startHub(t)

	slow, fast := newFakeSink("slow", "acc-1"), newFakeSink("fast", "acc-2")
	slow.full = true // never accepts anything

	for _, s := range []*fakeSink{slow, fast} {
		if err := h.Register(context.Background(), s, "yard"); err != nil {
			t.Fatalf("register %s: %v", s.ID(), err)
		}
	}

	for i := range maxOverflowsBeforeEvict {
		if err := h.Publish(context.Background(), "yard", fmt.Appendf(nil, `{"n":%d}`, i)); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}

	// The healthy client keeps receiving throughout.
	waitFor(t, "the fast client to receive every message", func() bool {
		return fast.received() == maxOverflowsBeforeEvict
	})
	// The slow one is evicted rather than allowed to stall the hub.
	waitFor(t, "the slow client to be evicted", func() bool {
		closed, code := slow.closeState()
		return closed && code == CloseTryAgainLater
	})

	// And the room still works afterwards.
	if err := h.Publish(context.Background(), "yard", []byte(`{"after":true}`)); err != nil {
		t.Fatalf("publish after eviction: %v", err)
	}
	waitFor(t, "the room to keep working after an eviction", func() bool {
		return fast.received() == maxOverflowsBeforeEvict+1
	})
}

func TestHubEnforcesPerAccountConnectionCap(t *testing.T) {
	h, _ := startHub(t)

	for i := range defaultMaxPerAccount {
		s := newFakeSink(fmt.Sprintf("s%d", i), "same-account")
		if err := h.Register(context.Background(), s, "yard"); err != nil {
			t.Fatalf("register %d: %v", i, err)
		}
	}

	extra := newFakeSink("one-too-many", "same-account")
	if err := h.Register(context.Background(), extra, "yard"); err != ErrTooManyConnections {
		t.Fatalf("register beyond the cap = %v, want ErrTooManyConnections", err)
	}

	// A different account is unaffected.
	if err := h.Register(context.Background(), newFakeSink("other", "other-account"), "yard"); err != nil {
		t.Fatalf("register for a different account: %v", err)
	}
}

func TestHubUnregisterStopsDelivery(t *testing.T) {
	h, _ := startHub(t)

	s := newFakeSink("a", "acc-1")
	if err := h.Register(context.Background(), s, "yard"); err != nil {
		t.Fatalf("register: %v", err)
	}
	h.Unregister(s)

	if err := h.Publish(context.Background(), "yard", []byte(`{"t":"hi"}`)); err != nil {
		t.Fatalf("publish: %v", err)
	}
	// Give the hub a chance to do the wrong thing before asserting it didn't:
	// register a second client and wait for *its* delivery as the barrier.
	witness := newFakeSink("witness", "acc-2")
	if err := h.Register(context.Background(), witness, "yard"); err != nil {
		t.Fatalf("register witness: %v", err)
	}
	if err := h.Publish(context.Background(), "yard", []byte(`{"t":"again"}`)); err != nil {
		t.Fatalf("publish again: %v", err)
	}
	waitFor(t, "the witness to receive", func() bool { return witness.received() == 1 })

	if got := s.received(); got != 0 {
		t.Fatalf("unregistered client received %d messages, want 0", got)
	}
}

// TestHubKickAccountClosesEverySocketForThatAccount covers the block path: the
// app revokes sessions immediately, and a live socket must not outlive that.
func TestHubKickAccountClosesEverySocketForThatAccount(t *testing.T) {
	h, _ := startHub(t)

	phone := newFakeSink("phone", "victim")
	laptop := newFakeSink("laptop", "victim")
	bystander := newFakeSink("bystander", "someone-else")
	for _, s := range []*fakeSink{phone, laptop, bystander} {
		if err := h.Register(context.Background(), s, "yard"); err != nil {
			t.Fatalf("register %s: %v", s.ID(), err)
		}
	}

	h.KickAccount("victim")

	for _, s := range []*fakeSink{phone, laptop} {
		waitFor(t, "the blocked account's socket to close", func() bool {
			closed, code := s.closeState()
			return closed && code == CloseUnauthorized
		})
	}
	if closed, _ := bystander.closeState(); closed {
		t.Fatal("kicking one account closed another account's connection")
	}
}

// TestHubDrainsOnShutdown is the second of the three latent problems: Go's
// http.Server.Shutdown does not close or wait for hijacked connections, so the
// hub has to do it — otherwise every deploy resets every player's socket with
// no close frame.
func TestHubDrainsOnShutdown(t *testing.T) {
	h := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	go h.Run(ctx)

	s := newFakeSink("a", "acc-1")
	if err := h.Register(context.Background(), s, "yard"); err != nil {
		t.Fatalf("register: %v", err)
	}

	cancel()

	select {
	case <-h.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("hub did not signal Done after shutdown")
	}

	closed, code := s.closeState()
	if !closed {
		t.Fatal("shutdown left a connection open")
	}
	if code != CloseGoingAway {
		t.Fatalf("close code = %d, want %d (going away, so the client knows to reconnect)", code, CloseGoingAway)
	}

	// Registering after shutdown fails cleanly rather than hanging.
	if err := h.Register(context.Background(), newFakeSink("late", "acc-2"), "yard"); err != ErrHubClosed {
		t.Fatalf("register after shutdown = %v, want ErrHubClosed", err)
	}
}
