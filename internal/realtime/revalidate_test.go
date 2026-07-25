package realtime

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"
)

// fakeAuthorizer stands in for the database. Every property of the sweep worth
// testing — an authorizer that errors, one that is slow, one that names nobody —
// is reachable this way, and none of them is reachable through a real one.
type fakeAuthorizer struct {
	mu sync.Mutex
	// revoke is the set of connection ids to report as no longer authorized.
	revoke map[string]struct{}
	// err, when set, is returned instead of an answer.
	err error
	// block, when non-nil, is waited on before answering — so a test can hold
	// the sweep inside the authorizer and observe the hub meanwhile.
	block chan struct{}
	// asked records the credentials of every call, in order.
	asked [][]Credential
}

func newFakeAuthorizer(revoke ...string) *fakeAuthorizer {
	set := make(map[string]struct{}, len(revoke))
	for _, id := range revoke {
		set[id] = struct{}{}
	}
	return &fakeAuthorizer{revoke: set}
}

func (f *fakeAuthorizer) Revoked(_ context.Context, live []Credential) ([]string, error) {
	f.mu.Lock()
	f.asked = append(f.asked, append([]Credential(nil), live...))
	err, block := f.err, f.block
	f.mu.Unlock()

	if block != nil {
		<-block
	}
	if err != nil {
		return nil, err
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, cr := range live {
		if _, ok := f.revoke[cr.ConnID]; ok {
			out = append(out, cr.ConnID)
		}
	}
	return out, nil
}

func (f *fakeAuthorizer) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.asked)
}

// lastAsked returns the connection ids of the most recent call, sorted so an
// assertion does not depend on the hub's map iteration order.
func (f *fakeAuthorizer) lastAsked(t *testing.T) []string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.asked) == 0 {
		t.Fatal("the authorizer was never asked anything")
	}
	last := f.asked[len(f.asked)-1]
	out := make([]string, 0, len(last))
	for _, cr := range last {
		out = append(out, cr.ConnID)
	}
	sort.Strings(out)
	return out
}

func (f *fakeAuthorizer) setErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// startRevalidator runs a revalidator for the test's lifetime and returns the
// channel that drives its sweeps. The channel is unbuffered, which is what makes
// the tests below deterministic without a single sleep: a send is received only
// when Run is back at the top of its loop, so a *second* successful send proves
// the *first* sweep has finished.
func startRevalidator(t *testing.T, hub *Hub, auth Authorizer) chan time.Time {
	t.Helper()
	tick := make(chan time.Time)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		NewRevalidator(hub, auth).Run(ctx, tick)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("revalidator did not stop after context cancellation")
		}
	})
	return tick
}

// sweepNow fires one sweep and waits for it to finish, by firing a second tick
// that can only be received once the first sweep has returned.
func sweepNow(t *testing.T, tick chan time.Time) {
	t.Helper()
	for range 2 {
		select {
		case tick <- time.Now():
		case <-time.After(2 * time.Second):
			t.Fatal("the revalidator never took the tick")
		}
	}
}

func registerAll(t *testing.T, h *Hub, sinks ...*fakeSink) {
	t.Helper()
	for _, s := range sinks {
		if err := h.Register(context.Background(), s, "yard"); err != nil {
			t.Fatalf("register %s: %v", s.ID(), err)
		}
	}
}

// TestRevalidatorSweepsOnlyOnTheInjectedTick pins the shape ADR-034 established
// for the broadcast: nothing happens until the tick the caller owns fires, so no
// test here has to sleep and guess.
func TestRevalidatorSweepsOnlyOnTheInjectedTick(t *testing.T) {
	h, _ := startHub(t)
	auth := newFakeAuthorizer()
	tick := startRevalidator(t, h, auth)

	registerAll(t, h, newFakeSink("a", "acc-1"))

	if got := auth.calls(); got != 0 {
		t.Fatalf("the authorizer was asked %d times before any tick, want 0", got)
	}
	sweepNow(t, tick)
	if got := auth.calls(); got == 0 {
		t.Fatal("a tick produced no sweep")
	}
}

// TestRevalidatorClosesOnlyWhatTheAuthorizerNames is the core guarantee: the
// sweep is not a policy of its own, it closes exactly the connections it is told
// to and leaves every other socket alone.
func TestRevalidatorClosesOnlyWhatTheAuthorizerNames(t *testing.T) {
	h, _ := startHub(t)
	doomed := newFakeSink("doomed", "acc-1")
	safe := newFakeSink("safe", "acc-2")
	alsoSafe := newFakeSink("also-safe", "acc-3")
	registerAll(t, h, doomed, safe, alsoSafe)

	auth := newFakeAuthorizer("doomed")
	tick := startRevalidator(t, h, auth)
	sweepNow(t, tick)

	waitFor(t, "the revoked socket to close", func() bool {
		closed, code := doomed.closeState()
		return closed && code == CloseUnauthorized
	})
	// 4001 is terminal for the client — a reconnect loop on this would hammer the
	// handshake forever, so the code matters as much as the close.
	if _, code := doomed.closeState(); code != CloseUnauthorized {
		t.Fatalf("close code = %d, want %d (terminal)", code, CloseUnauthorized)
	}
	if got := doomed.closeReason(); got != ReasonUnauthorized {
		t.Fatalf("close reason = %q, want %q", got, ReasonUnauthorized)
	}
	for _, s := range []*fakeSink{safe, alsoSafe} {
		if closed, _ := s.closeState(); closed {
			t.Fatalf("%s was closed, but the authorizer did not name it", s.ID())
		}
	}

	// The room still works afterwards: closing a socket removed it from the hub
	// rather than leaving a dead entry behind.
	if err := h.Publish(context.Background(), "yard", []byte(`{"t":"after"}`)); err != nil {
		t.Fatalf("publish after a sweep: %v", err)
	}
	waitFor(t, "the survivors to keep receiving", func() bool {
		return safe.received() == 1 && alsoSafe.received() == 1
	})
	if doomed.received() != 0 {
		t.Fatalf("a closed connection received %d messages, want 0", doomed.received())
	}
}

// TestRevalidatorAsksOnlyAboutRegisteredConnections keeps the sweep's cost tied
// to what is actually connected, and — more importantly — stops it asking about
// a connection that has already gone, whose id could otherwise be reported back
// and acted on.
func TestRevalidatorAsksOnlyAboutRegisteredConnections(t *testing.T) {
	h, _ := startHub(t)
	staying := newFakeSink("staying", "acc-1")
	leaving := newFakeSink("leaving", "acc-2")
	registerAll(t, h, staying, leaving)
	h.Unregister(leaving)

	auth := newFakeAuthorizer()
	tick := startRevalidator(t, h, auth)
	sweepNow(t, tick)

	got := auth.lastAsked(t)
	if len(got) != 1 || got[0] != "staying" {
		t.Fatalf("the authorizer was asked about %v, want only [staying]", got)
	}
}

// TestRevalidatorAsksNothingWhenNobodyIsConnected: an idle process must not pay
// for a query every interval to be told there is nothing to check.
func TestRevalidatorAsksNothingWhenNobodyIsConnected(t *testing.T) {
	h, _ := startHub(t)
	auth := newFakeAuthorizer()
	tick := startRevalidator(t, h, auth)

	sweepNow(t, tick)

	if got := auth.calls(); got != 0 {
		t.Fatalf("the authorizer was asked %d times with nobody connected, want 0", got)
	}
}

// TestRevalidatorSurvivesAnAuthorizerError is the property that makes this safe
// to run every half-minute against a database: "I could not tell" must never be
// read as "everybody is revoked". A database blip disconnecting every player is
// a far worse failure than one interval of stale access.
func TestRevalidatorSurvivesAnAuthorizerError(t *testing.T) {
	h, _ := startHub(t)
	doomed := newFakeSink("doomed", "acc-1")
	bystander := newFakeSink("bystander", "acc-2")
	registerAll(t, h, doomed, bystander)

	auth := newFakeAuthorizer("doomed")
	auth.setErr(errors.New("database is having a moment"))
	tick := startRevalidator(t, h, auth)
	sweepNow(t, tick)

	for _, s := range []*fakeSink{doomed, bystander} {
		if closed, _ := s.closeState(); closed {
			t.Fatalf("%s was closed on an authorizer error; the sweep must close nobody", s.ID())
		}
	}

	// And the loop carried on rather than dying with the error: once the database
	// answers again, the same sweep does its job.
	auth.setErr(nil)
	sweepNow(t, tick)
	waitFor(t, "the revoked socket to close once the authorizer recovers", func() bool {
		closed, code := doomed.closeState()
		return closed && code == CloseUnauthorized
	})
	if closed, _ := bystander.closeState(); closed {
		t.Fatal("the recovered sweep closed a connection the authorizer did not name")
	}
}

// TestRevalidatorDoesNotBlockTheHubWhileTheAuthorizerIsSlow is why the sweep is
// three phases rather than one. The hub goroutine owns every room and fans out
// to every client; a database query executed there would freeze the yard for as
// long as the database took to answer.
func TestRevalidatorDoesNotBlockTheHubWhileTheAuthorizerIsSlow(t *testing.T) {
	h, _ := startHub(t)
	doomed := newFakeSink("doomed", "acc-1")
	bystander := newFakeSink("bystander", "acc-2")
	registerAll(t, h, doomed, bystander)

	auth := newFakeAuthorizer("doomed")
	auth.block = make(chan struct{})
	tick := startRevalidator(t, h, auth)

	// One tick, not sweepNow: the sweep is about to stall inside the authorizer,
	// so waiting for it to finish is exactly what this test must not do.
	select {
	case tick <- time.Now():
	case <-time.After(2 * time.Second):
		t.Fatal("the revalidator never took the tick")
	}
	waitFor(t, "the sweep to reach the authorizer", func() bool { return auth.calls() == 1 })

	// The hub answers both a query and a broadcast while the sweep is stuck.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := h.Members(ctx, "yard"); err != nil {
		t.Fatalf("the hub could not answer Members while the authorizer was slow: %v", err)
	}
	if err := h.Publish(ctx, "yard", []byte(`{"t":"still-alive"}`)); err != nil {
		t.Fatalf("the hub could not broadcast while the authorizer was slow: %v", err)
	}
	waitFor(t, "the broadcast to arrive during a stalled sweep", func() bool {
		return bystander.received() == 1 && doomed.received() == 1
	})

	// Releasing it completes the sweep normally.
	close(auth.block)
	waitFor(t, "the revoked socket to close once the authorizer answers", func() bool {
		closed, code := doomed.closeState()
		return closed && code == CloseUnauthorized
	})
}

// TestSessionCheckKeysOnTheSessionNotTheAccount is the case that has no
// account-level signal at all: two connections, one account, two sessions, and
// only one of the sessions has ended. Keying the sweep on the account would
// either close both sockets or neither.
func TestSessionCheckKeysOnTheSessionNotTheAccount(t *testing.T) {
	live := []Credential{
		{ConnID: "phone", SessionID: "sess-old", AccountID: "acc-1"},
		{ConnID: "laptop", SessionID: "sess-new", AccountID: "acc-1"},
	}
	check := SessionCheck(func(_ context.Context, ids []string) ([]string, error) {
		if len(ids) != 2 {
			t.Errorf("asked about %v, want both sessions", ids)
		}
		return []string{"sess-old"}, nil
	})

	got, err := check.Revoked(context.Background(), live)
	if err != nil {
		t.Fatalf("Revoked: %v", err)
	}
	if len(got) != 1 || got[0] != "phone" {
		t.Fatalf("closing %v, want only the connection on the ended session", got)
	}
}

// TestSessionCheckAsksOncePerSession: three tabs sharing one cookie are three
// sockets but one session, and the batched question must not repeat it.
func TestSessionCheckAsksOncePerSession(t *testing.T) {
	live := []Credential{
		{ConnID: "tab-1", SessionID: "sess-1", AccountID: "acc-1"},
		{ConnID: "tab-2", SessionID: "sess-1", AccountID: "acc-1"},
		{ConnID: "tab-3", SessionID: "sess-1", AccountID: "acc-1"},
		{ConnID: "other", SessionID: "sess-2", AccountID: "acc-2"},
	}
	var asked []string
	check := SessionCheck(func(_ context.Context, ids []string) ([]string, error) {
		asked = append([]string(nil), ids...)
		return []string{"sess-1"}, nil
	})

	got, err := check.Revoked(context.Background(), live)
	if err != nil {
		t.Fatalf("Revoked: %v", err)
	}
	sort.Strings(asked)
	if len(asked) != 2 || asked[0] != "sess-1" || asked[1] != "sess-2" {
		t.Fatalf("asked about %v, want each session exactly once", asked)
	}
	// One dead session takes every socket holding it — logging out on the laptop
	// also cuts the phone that shares the cookie.
	sort.Strings(got)
	want := []string{"tab-1", "tab-2", "tab-3"}
	if len(got) != len(want) {
		t.Fatalf("closing %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("closing %v, want %v", got, want)
		}
	}
}

// TestSessionCheckSkipsConnectionsWithNoSession: an unidentifiable connection is
// evidence of a wiring bug, never of revocation. Reading it the fail-closed way
// would turn one such bug into every player being disconnected every interval.
func TestSessionCheckSkipsConnectionsWithNoSession(t *testing.T) {
	live := []Credential{
		{ConnID: "nameless", SessionID: "", AccountID: "acc-1"},
		{ConnID: "normal", SessionID: "sess-1", AccountID: "acc-2"},
	}
	check := SessionCheck(func(_ context.Context, ids []string) ([]string, error) {
		if len(ids) != 1 || ids[0] != "sess-1" {
			t.Errorf("asked about %v, want only the identifiable session", ids)
		}
		return nil, nil
	})

	got, err := check.Revoked(context.Background(), live)
	if err != nil {
		t.Fatalf("Revoked: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("closing %v, want nobody", got)
	}
}

// TestSessionCheckPropagatesTheError keeps the "could not tell" signal intact
// all the way to the sweep, which is what stops it closing anybody.
func TestSessionCheckPropagatesTheError(t *testing.T) {
	wantErr := errors.New("query failed")
	check := SessionCheck(func(_ context.Context, _ []string) ([]string, error) {
		return nil, wantErr
	})

	got, err := check.Revoked(context.Background(), []Credential{
		{ConnID: "a", SessionID: "sess-1", AccountID: "acc-1"},
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	if got != nil {
		t.Fatalf("returned %v alongside an error, want nothing", got)
	}
}
