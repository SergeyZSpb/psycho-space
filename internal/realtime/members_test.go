package realtime

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"
)

// memberIDs sorts the connection ids out of a member list, so an assertion does
// not depend on map iteration order.
func memberIDs(ms []Member) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.ConnID)
	}
	sort.Strings(out)
	return out
}

// TestMembersIsScopedToTheRoom is the property the roster broadcast rests on:
// asking who is in a room must not report somebody standing in a different one.
// Getting this wrong would publish one room's presence into another, which no
// other test would notice because fanout is scoped correctly.
func TestMembersIsScopedToTheRoom(t *testing.T) {
	h, _ := startHub(t)

	yardA, yardB, other := newFakeSink("a", "acc-1"), newFakeSink("b", "acc-2"), newFakeSink("c", "acc-3")
	for _, s := range []*fakeSink{yardA, yardB} {
		if err := h.Register(context.Background(), s, "yard"); err != nil {
			t.Fatalf("register %s: %v", s.ID(), err)
		}
	}
	if err := h.Register(context.Background(), other, "elsewhere"); err != nil {
		t.Fatalf("register other: %v", err)
	}

	got, err := h.Members(context.Background(), "yard")
	if err != nil {
		t.Fatalf("members: %v", err)
	}
	if ids := memberIDs(got); len(ids) != 2 || ids[0] != "a" || ids[1] != "b" {
		t.Fatalf("yard members = %v; want [a b]", ids)
	}
	for _, m := range got {
		if m.AccountID == "" {
			t.Fatalf("member %q carries no account id", m.ConnID)
		}
	}
}

// TestMembersReflectsLeaving pins that presence is derived, not remembered: a
// caller that rebuilds its view from Members on every tick must see somebody
// disappear without being told separately that they left.
func TestMembersReflectsLeaving(t *testing.T) {
	h, _ := startHub(t)

	a, b := newFakeSink("a", "acc-1"), newFakeSink("b", "acc-2")
	for _, s := range []*fakeSink{a, b} {
		if err := h.Register(context.Background(), s, "yard"); err != nil {
			t.Fatalf("register %s: %v", s.ID(), err)
		}
	}
	h.Unregister(a)

	waitFor(t, "the hub to drop the unregistered connection", func() bool {
		got, err := h.Members(context.Background(), "yard")
		return err == nil && len(got) == 1
	})
	got, err := h.Members(context.Background(), "yard")
	if err != nil {
		t.Fatalf("members: %v", err)
	}
	if got[0].ConnID != "b" {
		t.Fatalf("remaining member = %q; want b", got[0].ConnID)
	}
}

// TestMembersOfAnEmptyRoomIsEmptyNotAnError covers the every-tick case where
// nobody is connected. An error there would make a caller log once per tick
// forever, five times a second, for a situation that is entirely normal.
func TestMembersOfAnEmptyRoomIsEmptyNotAnError(t *testing.T) {
	h, _ := startHub(t)

	got, err := h.Members(context.Background(), "nobody-here")
	if err != nil {
		t.Fatalf("members of an empty room: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("members of an empty room = %v; want none", got)
	}
}

// TestMembersAfterShutdownReportsHubClosed pins the shutdown path. A caller
// broadcasting on a ticker races the drain on every deploy, and it needs to be
// able to tell "the hub is gone, stop" from a transient failure — otherwise it
// spins logging until its own context is cancelled.
func TestMembersAfterShutdownReportsHubClosed(t *testing.T) {
	h, cancel := startHub(t)
	cancel()
	select {
	case <-h.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("hub did not stop")
	}

	if _, err := h.Members(context.Background(), "yard"); !errors.Is(err, ErrHubClosed) {
		t.Fatalf("members after shutdown returned %v; want ErrHubClosed", err)
	}
}

// TestMembersDoesNotBlockForeverWithoutAHub guards the case where Run was never
// started. The command channel is unbuffered, so a naive implementation would
// hang the caller for good; honouring the context is what keeps a stuck hub from
// becoming a stuck request.
func TestMembersDoesNotBlockForeverWithoutAHub(t *testing.T) {
	h := NewHub() // deliberately not Run

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := h.Members(ctx, "yard"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("members with no hub running returned %v; want the context error", err)
	}
}
