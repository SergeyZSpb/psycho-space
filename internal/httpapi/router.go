// Package httpapi wires the chi router, middleware, and handlers.
package httpapi

import (
	"context"
	"io/fs"
	"net/http"
	"time"

	"github.com/SergeyZSpb/psycho-space/internal/account"
	"github.com/SergeyZSpb/psycho-space/internal/config"
	"github.com/SergeyZSpb/psycho-space/internal/gameassets"
	"github.com/SergeyZSpb/psycho-space/internal/gamefintech"
	"github.com/SergeyZSpb/psycho-space/internal/gamekhimki"
	"github.com/SergeyZSpb/psycho-space/internal/gamevanyadum"
	"github.com/SergeyZSpb/psycho-space/internal/gamevanyagotchi"
	"github.com/SergeyZSpb/psycho-space/internal/realtime"
	"github.com/SergeyZSpb/psycho-space/internal/session"
	"github.com/SergeyZSpb/psycho-space/internal/settings"
	"github.com/SergeyZSpb/psycho-space/internal/vk"
	"github.com/SergeyZSpb/psycho-space/internal/wishlist"
	"github.com/SergeyZSpb/psycho-space/internal/yandex"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Deps bundles everything the handlers need. Fields may be nil in tests that
// don't exercise the corresponding routes.
type Deps struct {
	Config config.Config
	Pool   *pgxpool.Pool
	WebFS  fs.FS
	VK     *vk.Client
	// Yandex is the second login provider. Both are always constructed; an
	// unconfigured one simply answers 503 rather than being absent, so there is
	// no nil check scattered through the auth path.
	Yandex     *yandex.Client
	Accounts   *account.Service
	Sessions   *session.Manager
	Wishlist   *wishlist.Service
	GameKhimki *gamekhimki.Service
	// GameVanyagotchi is the second game. The same service is also the
	// RealtimeHandler below — one game, two surfaces: an HTTP one for the pet
	// that outlives the process and a socket one for the plane that does not.
	GameVanyagotchi *gamevanyagotchi.Service
	// GameVanyadum is the third game — «ВАНЯДУМ», the shooter. Two surfaces
	// again, but they divide differently: HTTP serves the catalogue, the one
	// заброшка everybody is in, and the visits already recorded, while the
	// socket carries the twenty-hertz simulation AND the join, because being in
	// the room is being in the building.
	GameVanyadum *gamevanyadum.Service
	// GameFintech is the fourth game — «СИМУЛЯТОР ФИНТЕХА». Two surfaces again:
	// HTTP for the edges of a shift, and the socket for the twenty-hertz office
	// every occupant shares. Like the shooter it holds one shared world for the
	// whole process, which is why nothing here is keyed by a run id.
	GameFintech *gamefintech.Service
	// GameAssets is the shared art blob store — infrastructure, not a game, so
	// every game's art is served through this one dependency. nil disables the
	// asset route, which is the correct behaviour before anything is uploaded.
	GameAssets *gameassets.Service
	Settings   *settings.Service
	VKVerifier *vk.IDTokenVerifier // nil = id_token verification disabled
	// Realtime is the WebSocket hub; nil disables the endpoint. RealtimeCtx is
	// the hub's lifetime — NOT a request context, which is cancelled as soon as
	// the upgrade handler returns.
	Realtime    *realtime.Hub
	RealtimeCtx context.Context
	// RealtimeHandlers maps a room name to whatever reads its inbound frames.
	// They are interfaces, not games, so this file names no game and the upgrade
	// path stays game-agnostic; main decides which service sits behind which
	// room, and the keys of this map ARE the set of rooms a client may ask for
	// (see isKnownRoom). An empty map disables the socket for every room.
	RealtimeHandlers map[string]realtime.Handler
}

// rateLimit builds a per-client-IP rate limiter that renders the canonical JSON
// error envelope (with trace_id) on 429.
//
// The key comes from clientIP, which trusts X-Real-IP only from the local proxy
// — never the client-supplied X-Forwarded-For. See clientIP for why.
func (s *Server) rateLimit(reqs int, window time.Duration) func(http.Handler) http.Handler {
	return httprate.LimitBy(reqs, window,
		func(r *http.Request) (string, error) { return clientIP(r), nil },
		httprate.WithLimitHandler(func(w http.ResponseWriter, r *http.Request) {
			writeError(w, r, http.StatusTooManyRequests, "rate_limited")
		}),
	)
}

// Server carries handler dependencies.
type Server struct {
	d Deps
}

// NewServer builds the HTTP server dependencies.
func NewServer(d Deps) *Server { return &Server{d: d} }

// Handler builds the router with middleware and routes.
func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	// No middleware.RealIP: it rewrites r.RemoteAddr from the client-supplied
	// X-Forwarded-For, which made every per-IP rate limit forgeable. Rate limits
	// key off clientIP instead.
	r.Use(accountLogContext)
	r.Use(traceHeader)
	r.Use(requestLogger)
	r.Use(bodyLimit(1 << 20)) // 1 MiB request cap

	r.Get("/healthz", s.handleHealthz)

	r.Route("/api", func(r chi.Router) {
		r.Use(s.rateLimit(240, time.Minute)) // blanket per-IP guard

		r.Get("/ping", handlePing)

		r.Route("/auth", func(r chi.Router) {
			// Tighter limit on the abuse-sensitive login endpoints.
			authLimit := s.rateLimit(30, time.Minute)
			r.With(authLimit).Get("/vk/state", s.handleVKState)
			r.With(authLimit).Post("/vk/callback", s.handleOAuthCallback(s.vk()))
			// Yandex's state endpoint also returns the authorize URL, so the
			// client id and redirect URI never leave the server — see
			// handleYandexState.
			r.With(authLimit).Get("/yandex/state", s.handleYandexState)
			r.With(authLimit).Post("/yandex/callback", s.handleOAuthCallback(s.yandex()))
			r.Get("/me", s.handleMe)
			r.Post("/logout", s.handleLogout)
		})

		// Wishlist — approved users only. Items and comments are both upvotable.
		r.Route("/wishlist", func(r chi.Router) {
			r.Use(s.requireAuth)
			r.Get("/items", s.handleWishlistList)
			r.Post("/items", s.handleWishlistCreate)
			r.Delete("/items/{id}", s.handleDeleteItem)
			r.Post("/items/{id}/vote", s.handleVote)
			r.Delete("/items/{id}/vote", s.handleUnvote)
			r.Get("/items/{id}/comments", s.handleCommentList)
			r.Post("/items/{id}/comments", s.handleCommentCreate)
			r.Delete("/comments/{id}", s.handleDeleteComment)
			r.Post("/comments/{id}/vote", s.handleCommentVote)
			r.Delete("/comments/{id}/vote", s.handleCommentUnvote)
		})

		// Game «Смолтолк в Химках» — approved users only. Dialog content is
		// backend config; runs (outcomes) feed the leaderboard. Each game owns
		// its own path segment (/api/game-<name>), so a second game adds a
		// sibling route group and nothing here changes.
		//
		// The judge calls the (paid) LLM, so it is capped tightly per IP. Halved
		// from 10 when the model moved to deepseek-v4-flash: a turn costs roughly
		// twice as much there, so the same money per minute buys half the turns.
		// A human plays a handful of turns a minute at most; this only bites
		// someone hammering the endpoint.
		//
		// The pre-rename /api/game/* alias that shared this limiter is gone —
		// it was registered for exactly one deploy cycle so a cached SPA could
		// finish its run, and TestGameKhimkiLegacyPathAliasIsGone now pins its
		// absence. Nothing may be written against that prefix again.
		gameKhimkiAttemptLimit := s.rateLimit(5, time.Minute)
		r.Route("/game-khimki", func(r chi.Router) {
			// Approved users only. Art is NOT served from here — the blob store
			// is shared infrastructure at /api/game-assets/, below.
			r.Group(func(r chi.Router) {
				r.Use(s.requireAuth)
				r.Get("/config", s.handleGameKhimkiConfig)
				r.With(gameKhimkiAttemptLimit).Post("/attempt", s.handleGameKhimkiAttempt)
				r.Post("/runs", s.handleGameKhimkiSubmitRun)
				r.Get("/runs/leaderboard", s.handleGameKhimkiLeaderboard)
				r.Get("/runs/me", s.handleGameKhimkiStats)
			})
		})

		// Game «Ванягоччи» — approved users only. Its own path segment and its
		// own handlers: it shares no route, no table and no service code with
		// the game above, and deleting it is deleting this block along with its
		// package, its migration and its views.
		//
		// No LLM on any path here, ever — that rule is written into the game's
		// package doc and is the reason this group needs no tight per-endpoint
		// limiter the way /game-khimki/attempt does. The blanket 240/min above
		// is the guard, and every write below is idempotent or clamped.
		r.Route("/game-vanyagotchi", func(r chi.Router) {
			r.Use(s.requireAuth)
			// Two reads, and nothing that writes on purpose. A VERB DOES NOT
			// ARRIVE HERE: it travels over the socket as one `vanyagotchi_do`
			// frame, and the server answers with state rather than a response
			// body — see ADR-043. There is deliberately no HTTP form of it,
			// because a second way to press a button is a second thing to keep
			// in agreement with the first.
			r.Get("/config", s.handleGameVanyagotchiConfig)
			r.Get("/state", s.handleGameVanyagotchiState)
			// The face to draw on one entity, asked for by the pseudonym the
			// roster already published. A redirect or a 404, and the 404 is the
			// ordinary answer for every NPC.
			r.Get("/avatar/{peer}", s.handleGameVanyagotchiAvatar)
		})

		// Game «ВАНЯДУМ» — approved users only, and THREE READS. There is one
		// заброшка, always running, and joining it is opening the socket: the
		// room already carries an authenticated account, so a start endpoint
		// would be a second way in for the two to disagree about. Playing
		// happens entirely on the socket — input arrives as a frame and the
		// world goes back as a snapshot twenty times a second.
		//
		// No LLM on any path here either — that rule is written into the game's
		// package doc, and it matters more here than it did in the yard because
		// this game would otherwise want a generated line every trigger pull.
		r.Route("/game-vanyadum", func(r chi.Router) {
			r.Use(s.requireAuth)
			r.Get("/config", s.handleGameVanyadumConfig)
			r.Get("/world", s.handleGameVanyadumWorld)
			r.Get("/visits/me", s.handleGameVanyadumMyVisits)
		})

		// Game «СИМУЛЯТОР ФИНТЕХА» — approved users only, and again only the
		// EDGES of a shift: clocking in, resuming after a reload, walking out,
		// and reading the two boards the splash screen is built from. Playing
		// happens entirely on the socket.
		//
		// Starting a shift returns no geometry, because there is nothing
		// per-shift to send: the floor is in the catalogue the client already
		// fetched, and what the shift response adds is only its IDENTITY, so a
		// tab whose cached catalogue describes a floor that has since been
		// rebuilt knows to fetch it again.
		//
		// No LLM on any path here either — that rule is written into the game's
		// package doc, so the blanket 240/min limiter above is the whole guard
		// and no endpoint below needs a tighter one.
		r.Route("/game-fintech", func(r chi.Router) {
			r.Use(s.requireAuth)
			r.Get("/config", s.handleGameFintechConfig)
			r.Post("/shifts", s.handleGameFintechStart)
			r.Get("/shifts/current", s.handleGameFintechCurrent)
			r.Delete("/shifts/current", s.handleGameFintechLeave)
			r.Get("/shifts/me", s.handleGameFintechMyShifts)
			r.Get("/shifts/top", s.handleGameFintechTopShifts)
			// The face to draw on a colleague, asked for by the pseudonym his
			// frame already carried. A redirect or a 404 — never a URL on the
			// socket, which is what ADR-037 is for.
			r.Get("/avatar/{peer}", s.handleGameFintechAvatar)
		})

		// Game art — shared infrastructure, NOT a game. The blob store has
		// carried a game_key discriminator since it was created, so it was
		// always multi-game: one route and one handler serve every game, and a
		// new game adds neither. Public (art is not sensitive) and cacheable.
		// Its unprefixed name is the signal that it is game-agnostic.
		r.Get("/game-assets/{game}/{key}", s.handleGameAsset)

		// Realtime — approved users only. One socket per connection; the
		// handshake spends one token of the blanket limiter above and the
		// socket bounds itself thereafter (there is no request left to count).
		r.With(s.requireAuth).Get("/realtime", s.handleRealtime)

		// Admin — approve/block for admins; promote, forget + settings for
		// superadmin only.
		r.Route("/admin", func(r chi.Router) {
			r.Use(s.requireAuth)
			r.Use(s.requireAdmin)
			r.Get("/accounts", s.handleAdminList)
			r.Post("/accounts/{id}/approve", s.handleAdminApprove)
			r.Post("/accounts/{id}/block", s.handleAdminBlock)
			r.With(s.requireSuperadmin).Post("/accounts/{id}/promote", s.handleAdminPromote)
			r.With(s.requireSuperadmin).Post("/accounts/{id}/demote", s.handleAdminDemote)
			// Anonymise a person while keeping what they wrote. Superadmin only
			// and irreversible: it destroys the identity in place and frees the
			// blind index, so the same VK account logging in afterwards is a
			// brand-new pending one. See handleAdminForget.
			r.With(s.requireSuperadmin).Post("/accounts/{id}/forget", s.handleAdminForget)
			r.Get("/settings", s.handleSettingsGet)
			r.With(s.requireSuperadmin).Put("/settings/open-registration", s.handleSetOpenRegistration)
		})
	})

	// Anything else is a SPA route — serve the embedded frontend.
	r.Handle("/*", spaHandler(s.d.WebFS))
	return r
}
