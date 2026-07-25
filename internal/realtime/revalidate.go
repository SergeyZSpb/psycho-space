package realtime

import (
	"context"
	"log/slog"
	"time"
)

// RevalidateInterval is how often live connections are re-checked. It is the
// worst-case delay between an authorisation ending and the socket that relied on
// it being cut, so it is a security bound rather than a tuning knob: long enough
// that a full sweep is one small query per half-minute, short enough that a
// revoked credential does not outlive its usefulness by anything a person would
// notice.
const RevalidateInterval = 30 * time.Second

// Credential is what the hub knows about one live connection's authorisation.
// It is a value type, like Member: an Authorizer decides who may stay connected
// and is deliberately not handed the ability to write to, or close, a socket.
//
// Both ids are opaque here. The hub never interprets either, and this package
// does not know what a session or an account is — it carries the two strings it
// was upgraded with so that whoever does know can be asked about them in one
// batch.
type Credential struct {
	ConnID    string
	SessionID string
	AccountID string
}

// Authorizer reports which of the currently-connected sockets may no longer be
// connected. It is given every live connection at once and returns the ids of
// those to close, so the check costs one round trip per sweep rather than one
// per socket.
//
// Returning an error means "I could not tell", and the sweep then closes
// NOBODY. That asymmetry is deliberate: a database blip is not evidence that
// anyone's authorisation ended, and treating it as such would disconnect every
// player in the process every time the database hiccuped. The cost of the
// opposite mistake is one extra interval of access for someone already being
// refused on every HTTP request.
type Authorizer interface {
	Revoked(ctx context.Context, live []Credential) ([]string, error)
}

// SessionCheck adapts a batched "are these still usable?" query to Authorizer:
// it is given session ids and returns the ones that may no longer act.
//
// It exists so the query can live in the package that owns the table, without
// that package having to import this one or learn that a WebSocket exists. This
// package supplies the mapping from sockets to sessions and back, which is the
// half that belongs here.
type SessionCheck func(ctx context.Context, sessionIDs []string) ([]string, error)

// Revoked implements Authorizer.
//
// A socket is judged by its own SESSION, not by its account. An expired session
// is the case with no account-level signal at all — the account is still
// perfectly valid, that one session is not — so keying this on the account would
// silently miss it.
func (c SessionCheck) Revoked(ctx context.Context, live []Credential) ([]string, error) {
	// One question per session rather than per connection: a phone, a laptop and
	// a stale tab share one cookie, so the three sockets an account may hold are
	// usually one session.
	ids := make([]string, 0, len(live))
	seen := make(map[string]struct{}, len(live))
	unidentified := 0
	for _, cr := range live {
		if cr.SessionID == "" {
			// A connection that cannot name its session cannot be judged. That is
			// evidence of a wiring bug, never of revocation — and the fail-closed
			// reading ("unknown, so close it") would turn one such bug into every
			// player being disconnected every half-minute. Count it and move on;
			// the upgrade path refuses a socket without a session id, so this
			// should be unreachable and is worth a log line if it ever is not.
			unidentified++
			continue
		}
		if _, dup := seen[cr.SessionID]; dup {
			continue
		}
		seen[cr.SessionID] = struct{}{}
		ids = append(ids, cr.SessionID)
	}
	if unidentified > 0 {
		slog.WarnContext(ctx, "realtime: live connection has no session id and cannot be revalidated",
			"count", unidentified)
	}
	if len(ids) == 0 {
		return nil, nil
	}

	dead, err := c(ctx, ids)
	if err != nil {
		return nil, err
	}
	if len(dead) == 0 {
		return nil, nil
	}
	deadSet := make(map[string]struct{}, len(dead))
	for _, id := range dead {
		deadSet[id] = struct{}{}
	}
	// Back from sessions to sockets: every connection holding a dead session
	// goes, which is what makes logging out on a laptop also cut the phone that
	// shares the cookie.
	out := make([]string, 0, len(dead))
	for _, cr := range live {
		if cr.SessionID == "" {
			continue
		}
		if _, bad := deadSet[cr.SessionID]; bad {
			out = append(out, cr.ConnID)
		}
	}
	return out, nil
}

// Revalidator periodically re-checks live connections and closes the ones whose
// authorisation no longer holds.
//
// It exists because a WebSocket outlives the request that authorised it. The
// in-process kick on block (Hub.KickAccount) is instant and deterministic, but
// it can only see what goes through the admin endpoint: a session that reaches
// its expires_at on its own, and a block applied straight in the database, have
// no in-process event to hang off at all. This is the backstop for both, and the
// two mechanisms are complements rather than alternatives.
//
// It reads and decides; it writes nothing. That is what keeps it on the right
// side of the rule that no state in this project advances because time passed —
// a sweep that runs late, early, twice, or not at all produces the same outcome
// as soon as it next runs.
type Revalidator struct {
	hub  *Hub
	auth Authorizer
}

// NewRevalidator builds a revalidator. Run must be called for it to do anything.
func NewRevalidator(hub *Hub, auth Authorizer) *Revalidator {
	return &Revalidator{hub: hub, auth: auth}
}

// Run sweeps once per tick until ctx is cancelled. It blocks; call it in its own
// goroutine.
//
// The tick is a parameter for the same two reasons the broadcast's is: main
// passes a time.Ticker, and a test fires a channel it owns, so no test in this
// package has to sleep and hope. Sweeps never overlap, because the next tick is
// only received once the previous sweep has returned.
func (rv *Revalidator) Run(ctx context.Context, tick <-chan time.Time) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick:
			rv.sweep(ctx)
		}
	}
}

// sweep runs one revalidation: snapshot the hub, ask off the hub goroutine, then
// ask the hub to close the losers.
//
// The middle step is the whole reason for the three-phase shape. The hub owns
// every room and fans out to every client from a single goroutine; a database
// query executed there would freeze the yard for as long as the database took to
// answer. So the hub is only ever touched by two cheap channel round trips that
// copy a slice, and the slow part happens here, on the revalidator's own
// goroutine.
func (rv *Revalidator) sweep(ctx context.Context) {
	live, err := rv.hub.liveCredentials(ctx)
	if err != nil {
		return // the hub has stopped, or ctx is done: there is nothing to sweep.
	}
	if len(live) == 0 {
		return // nobody is connected — do not pay for a query to learn that.
	}

	revoked, err := rv.auth.Revoked(ctx, live)
	if err != nil {
		// Log and carry on. See Authorizer: "I could not tell" must never be read
		// as "everybody is revoked".
		slog.ErrorContext(ctx, "realtime revalidation failed", "err", err, "live", len(live))
		return
	}
	if len(revoked) == 0 {
		return
	}

	slog.InfoContext(ctx, "realtime revalidation closing unauthorized sockets",
		"closing", len(revoked), "live", len(live))
	if err := rv.hub.closeConnections(ctx, revoked, CloseUnauthorized, ReasonUnauthorized); err != nil {
		slog.WarnContext(ctx, "realtime revalidation could not close sockets", "err", err)
	}
}
