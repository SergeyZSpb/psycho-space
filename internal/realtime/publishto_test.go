package realtime

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestPublishToReachesOnlyTheNamedConnection is the whole contract of a unicast.
// It exists so a service can answer one client a question the rest of the room
// has no business hearing — and the failure that matters is the quiet one, where
// a "unicast" is really a broadcast and nobody notices because the extra
// recipients simply ignore a type they do not recognise.
func TestPublishToReachesOnlyTheNamedConnection(t *testing.T) {
	h, _ := startHub(t)

	target := newFakeSink("target", "acc-1")
	sameAccount := newFakeSink("same-account", "acc-1") // a second device
	otherRoom := newFakeSink("other-room", "acc-2")
	for _, s := range []*fakeSink{target, sameAccount} {
		if err := h.Register(context.Background(), s, "yard"); err != nil {
			t.Fatalf("register %s: %v", s.ID(), err)
		}
	}
	if err := h.Register(context.Background(), otherRoom, "elsewhere"); err != nil {
		t.Fatalf("register other: %v", err)
	}

	if err := h.PublishTo(context.Background(), "target", []byte(`{"t":"you"}`)); err != nil {
		t.Fatalf("publish to: %v", err)
	}

	waitFor(t, "the named connection to receive", func() bool { return target.received() == 1 })
	// The other connection of the SAME account must not receive it either: the
	// hub addresses a socket, not a person, and a service that wants both says so
	// by naming both.
	if n := sameAccount.received(); n != 0 {
		t.Fatalf("another connection of the same account received %d messages; want 0", n)
	}
	if n := otherRoom.received(); n != 0 {
		t.Fatalf("a connection in another room received %d messages; want 0", n)
	}
}

// TestPublishToAnUnknownConnectionIsANoOp covers the ordinary race rather than an
// error case: the socket a caller is answering can go away between the frame
// that named it and the reply being composed. That must cost nothing and must
// not disturb the hub, which is why the second half of this test proves the room
// still works afterwards.
func TestPublishToAnUnknownConnectionIsANoOp(t *testing.T) {
	h, _ := startHub(t)

	present := newFakeSink("present", "acc-1")
	if err := h.Register(context.Background(), present, "yard"); err != nil {
		t.Fatalf("register: %v", err)
	}

	if err := h.PublishTo(context.Background(), "never-existed", []byte(`{"t":"you"}`)); err != nil {
		t.Fatalf("publish to an unknown connection = %v; want no error", err)
	}
	// A connection that has gone is the same case as one that never was.
	h.Unregister(present)
	waitFor(t, "the hub to drop the unregistered connection", func() bool {
		got, err := h.Members(context.Background(), "yard")
		return err == nil && len(got) == 0
	})
	if err := h.PublishTo(context.Background(), "present", []byte(`{"t":"you"}`)); err != nil {
		t.Fatalf("publish to a departed connection = %v; want no error", err)
	}
	if n := present.received(); n != 0 {
		t.Fatalf("a departed connection received %d messages; want 0", n)
	}

	// And the hub is still serving everybody else.
	witness := newFakeSink("witness", "acc-2")
	if err := h.Register(context.Background(), witness, "yard"); err != nil {
		t.Fatalf("register witness: %v", err)
	}
	if err := h.Publish(context.Background(), "yard", []byte(`{"t":"after"}`)); err != nil {
		t.Fatalf("publish after the no-ops: %v", err)
	}
	waitFor(t, "the room to keep working", func() bool { return witness.received() == 1 })
}

// TestPublishToAfterShutdownReportsHubClosed is the deploy path. A game answers
// a handshake from a read pump that outlives the hub's drain by a moment, and it
// has to be able to tell "the hub is gone" from a delivery — otherwise it cannot
// know whether to stop.
func TestPublishToAfterShutdownReportsHubClosed(t *testing.T) {
	h, cancel := startHub(t)
	cancel()
	select {
	case <-h.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("hub did not stop")
	}

	// Deterministic only because the command channel is unbuffered: a buffered
	// one would still accept the send here, and select would choose between that
	// and the closed done channel at random.
	if err := h.PublishTo(context.Background(), "anyone", []byte(`{"t":"you"}`)); !errors.Is(err, ErrHubClosed) {
		t.Fatalf("publish to after shutdown returned %v; want ErrHubClosed", err)
	}
}

// TestPublishToDoesNotBlockForeverWithoutAHub guards the case where Run was
// never started. The command channel is unbuffered, so a naive implementation
// would hang its caller for good — and that caller is a read pump, which would
// take one player's socket down with it.
func TestPublishToDoesNotBlockForeverWithoutAHub(t *testing.T) {
	h := NewHub() // deliberately not Run

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := h.PublishTo(ctx, "anyone", []byte(`{"t":"you"}`)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("publish to with no hub running returned %v; want the context error", err)
	}
}

// TestPublishToEvictsAClientThatWillNotTakeIt pins that a unicast obeys the same
// backpressure policy as a broadcast. One rule for "this client is behind" is the
// point: a second, gentler path would let a wedged socket sit in the hub for as
// long as nobody happened to broadcast to it.
func TestPublishToEvictsAClientThatWillNotTakeIt(t *testing.T) {
	h, _ := startHub(t)

	stuck := newFakeSink("stuck", "acc-1")
	stuck.full = true // never accepts anything
	if err := h.Register(context.Background(), stuck, "yard"); err != nil {
		t.Fatalf("register: %v", err)
	}

	for i := range maxOverflowsBeforeEvict {
		if err := h.PublishTo(context.Background(), "stuck", []byte(`{"t":"you"}`)); err != nil {
			t.Fatalf("publish to %d: %v", i, err)
		}
	}

	waitFor(t, "the stuck client to be evicted", func() bool {
		closed, code := stuck.closeState()
		return closed && code == CloseTryAgainLater
	})
}
