package httpapi

import (
	"log/slog"
	"net/http"
	neturl "net/url"

	"github.com/go-chi/chi/v5"

	"github.com/SergeyZSpb/psycho-space/internal/gamevanyagotchi"
)

// available reports whether the game is wired, and answers 503 when it is not.
//
// Deps documents that a field may be nil in a caller that does not exercise its
// routes, so nil is an expected state rather than a bug — and without this the
// pet handlers would dereference it, panic, and be recovered into a bare 500
// that says nothing. The same shape the realtime and asset routes already use.
func (s *Server) gameVanyagotchiAvailable(w http.ResponseWriter, r *http.Request) bool {
	if s.d.GameVanyagotchi != nil {
		return true
	}
	writeError(w, r, http.StatusServiceUnavailable, "game_unavailable")
	return false
}

// handleGameVanyagotchiConfig serves the content catalogue: stats and their
// rates, the actions, the skins, the locations.
//
// Everything the client needs in order to render and label the game comes from
// here, so the SPA hardcodes no key and no number — which is what makes adding a
// stat or retitling an action a backend-only change.
func (s *Server) handleGameVanyagotchiConfig(w http.ResponseWriter, r *http.Request) {
	if !s.gameVanyagotchiAvailable(w, r) {
		return
	}
	cfg := s.d.GameVanyagotchi.Config()
	// Point each skin at its uploaded picture, if there is one. A skin with no
	// upload keeps an empty `Image` and the client draws the emoji over its
	// gradient — which is what every character in this game has looked like so
	// far, because until now nothing populated this field at all.
	//
	// Read here rather than in the catalogue because it is not content: which
	// keys have a picture is a fact about the blob store, and it changes when
	// somebody uploads one rather than when the game is redeployed. A failure is
	// logged and treated as "none uploaded", so a database blip costs the yard
	// its faces rather than its config.
	present, err := s.d.GameVanyagotchi.PresentArtKeys(r.Context(), cfg.GameKey)
	if err != nil {
		slog.WarnContext(r.Context(), "game-vanyagotchi asset keys lookup failed", "err", err)
		present = nil
	}
	for i := range cfg.Skins {
		if present[cfg.Skins[i].Key] {
			cfg.Skins[i].Image = GameAssetPath + cfg.GameKey + "/" + cfg.Skins[i].Key
		}
	}
	writeJSON(w, http.StatusOK, cfg)
}

// handleGameVanyagotchiState returns the caller's pet with every stat decayed to
// this instant.
//
// It is a GET that writes, which is worth naming rather than hiding: it creates
// the pet on first sight and records a death the first time one is observed.
// Both are idempotent and both are lazy on purpose — the alternative to writing
// on read is a background job, and this game has none by design.
func (s *Server) handleGameVanyagotchiState(w http.ResponseWriter, r *http.Request) {
	if !s.gameVanyagotchiAvailable(w, r) {
		return
	}
	viewer, _ := accountFromContext(r.Context())
	st, err := s.d.GameVanyagotchi.State(r.Context(), viewer.ID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal")
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// handleGameVanyagotchiAvatar redirects to the VK picture of the entity the
// roster calls {peer}, or answers 404 when there is none.
//
// KEYED ON THE PSEUDONYM, never on an account id, so this route publishes no
// more about a person than the roster already does — and the roster carries no
// URL at all, which is the point: a couple of hundred characters that never
// change have no business being re-sent five times a second to everyone, and a
// URL out of Postgres would have been the one durable thing on a frame whose
// identity is deliberately ephemeral (ADR-037).
//
// 404 IS AN ORDINARY ANSWER, not a failure. Every NPC gets one, because none of
// them is a person; so does anybody VK has no picture for. The client asks for
// every entity it draws and falls back to the catalogue art on a 404, which is
// what keeps it kind-agnostic — it has no idea which of the things it is drawing
// are people.
//
// A REDIRECT RATHER THAN A PROXY, deliberately and with a known limit. Streaming
// the bytes ourselves would also hide the viewer from VK's CDN, and that is a
// real difference — but the same pictures are already fetched straight from VK
// by the wishlist, the comments and the leaderboard, so proxying here alone
// would buy nothing while adding an egress path, a cache and a second way to
// fetch one face. If that changes, it changes for all four at once.
//
// Cacheable, and privately: the answer is about one person, so a shared cache
// must not keep it.
//
// HOW STALE A FACE CAN GET, stated because the half hour below invites the
// wrong conclusion. Three things cache: the browser (thirty minutes, here), the
// display cache (refreshed on every hello, so a reconnect re-reads it), and
// `accounts.avatar_url_enc` itself — which is written by `UpsertOnLogin` AT
// LOGIN AND NOWHERE ELSE. The third dominates: until its owner signs in again we
// serve the URL we were given then, however fresh the other two are. That cannot
// be fixed by polling VK, because ADR-006 discards the VK token once the profile
// is read and there is nothing left to ask with — a deliberate decision whose
// price is exactly this. When VK drops the old picture the CDN 404s and the
// client falls back to the catalogue art, so the failure is a plain Ваня rather
// than a broken image, and the wishlist and the leaderboard have always been
// stale in the same way.
func (s *Server) handleGameVanyagotchiAvatar(w http.ResponseWriter, r *http.Request) {
	if !s.gameVanyagotchiAvailable(w, r) {
		return
	}
	target, ok := s.d.GameVanyagotchi.AvatarFor(chi.URLParam(r, "peer"))
	// CHECKED BEFORE IT IS TRUSTED, even though it came from our own database.
	// It was stored exactly as VK returned it and validated nowhere on the way
	// in, so this handler is the first place anything has ever looked at it — and
	// an unvalidated redirect target is an open redirect, which turns our origin
	// into a credible-looking way to send somebody anywhere. Absolute https with
	// a host, or it did not happen: a value that fails is treated as no picture
	// at all, so the client falls back to the catalogue art exactly as it does
	// for an NPC. Deliberately NOT a host allowlist — VK's CDN hostnames are not
	// recorded anywhere in this project and a guessed list would fail closed on
	// every face the day they change one.
	if ok {
		u, err := neturl.Parse(target)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			slog.WarnContext(r.Context(), "gamevanyagotchi: refusing to redirect to a stored avatar that is not absolute https")
			ok = false
		} else {
			// Re-serialised from the parse rather than passed through, so what
			// leaves here is a URL this process built out of components it
			// checked, not a string it was handed.
			target = u.String()
		}
	}
	// Private, because the answer is about one person and a shared cache must not
	// keep it. THIRTY MINUTES for an answer, which is long relative to a session
	// on purpose: a face is the most static thing this game serves, and re-asking
	// costs a request per entity per player. It is not the limit on freshness
	// anyway — the paragraph above explains why the login is — so a shorter
	// window would buy traffic without buying currency.
	//
	// A MISS IS CACHED ONLY WHEN IT IS PERMANENT, and getting that wrong cost a
	// real bug. For an NPC or a thing on the ground there will never be a picture,
	// so the 404 is cached exactly like an answer — which is what stops a client
	// that cannot tell them apart from re-asking for every one on every reconnect.
	// For a PERSON the miss is transient: the avatar is read out of Postgres when
	// that account says hello, so a peer drawn before its owner's hello has landed
	// — a sleeper after a restart, most obviously — answers 404 and starts
	// answering with a picture moments later. Caching that left the face missing
	// for half an hour after it existed, and made two browsers disagree about the
	// same handle, because the one that asked early was replaying its own 404.
	if ok || gamevanyagotchi.NeverHasAFace(chi.URLParam(r, "peer")) {
		w.Header().Set("Cache-Control", "private, max-age=1800")
	} else {
		// Deliberately not a short max-age: there is no correct number, because
		// the miss ends when a human opens the app rather than after any interval.
		w.Header().Set("Cache-Control", "no-store")
	}
	if !ok {
		writeError(w, r, http.StatusNotFound, "no_avatar")
		return
	}
	// 302 rather than 301: the mapping is per-process and the picture changes
	// when its owner changes it, so nothing here is permanent.
	//nolint:gosec // G710: the target is checked immediately above — parsed, required to be
	// absolute https with a host, and re-serialised from that parse — so it is not the
	// unvalidated taint the analysis takes it for. Narrowing it further would mean an
	// allowlist of VK CDN hostnames, which this project does not know and would fail
	// closed on every face the day VK changed one.
	http.Redirect(w, r, target, http.StatusFound)
}

// There is no action handler here, and that absence is the design.
//
// A verb arrives over the socket instead, as one `vanyagotchi_do` frame, and is
// interpreted by the one `Service.Do` that a replay also goes through. What the
// server decided comes back as STATE — a line over the player's own Ваня and a
// push of the pet — never as a response body, because the 5 Hz full-state frame
// already reconciles the yard and a verb that owes a reply is a verb with two
// ways of being answered. See ADR-043.
