// Package httpapi wires the chi router, middleware, and handlers.
package httpapi

import (
	"io/fs"
	"net/http"
	"time"

	"github.com/SergeyZSpb/psycho-space/internal/account"
	"github.com/SergeyZSpb/psycho-space/internal/config"
	"github.com/SergeyZSpb/psycho-space/internal/game"
	"github.com/SergeyZSpb/psycho-space/internal/session"
	"github.com/SergeyZSpb/psycho-space/internal/settings"
	"github.com/SergeyZSpb/psycho-space/internal/vk"
	"github.com/SergeyZSpb/psycho-space/internal/wishlist"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Deps bundles everything the handlers need. Fields may be nil in tests that
// don't exercise the corresponding routes.
type Deps struct {
	Config     config.Config
	Pool       *pgxpool.Pool
	WebFS      fs.FS
	VK         *vk.Client
	Accounts   *account.Service
	Sessions   *session.Manager
	Wishlist   *wishlist.Service
	Game       *game.Service
	Settings   *settings.Service
	VKVerifier *vk.IDTokenVerifier // nil = id_token verification disabled
}

// rateLimit builds a per-client-IP rate limiter that renders the canonical JSON
// error envelope (with trace_id) on 429.
func (s *Server) rateLimit(reqs int, window time.Duration) func(http.Handler) http.Handler {
	return httprate.Limit(reqs, window,
		httprate.WithKeyFuncs(httprate.KeyByIP),
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
	r.Use(middleware.RealIP)
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
			r.With(authLimit).Post("/vk/callback", s.handleVKCallback)
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

		// Game — approved users only. Dialog content is backend config; runs
		// (outcomes) feed the leaderboard.
		r.Route("/game", func(r chi.Router) {
			r.Use(s.requireAuth)
			r.Get("/config", s.handleGameConfig)
			// The judge calls the (paid) LLM, so cap it tightly per IP.
			r.With(s.rateLimit(10, time.Minute)).Post("/attempt", s.handleGameAttempt)
			r.Post("/runs", s.handleGameSubmitRun)
			r.Get("/runs/leaderboard", s.handleGameLeaderboard)
			r.Get("/runs/me", s.handleGameStats)
		})

		// Admin — approve/block for admins; promote + settings for superadmin only.
		r.Route("/admin", func(r chi.Router) {
			r.Use(s.requireAuth)
			r.Use(s.requireAdmin)
			r.Get("/accounts", s.handleAdminList)
			r.Post("/accounts/{id}/approve", s.handleAdminApprove)
			r.Post("/accounts/{id}/block", s.handleAdminBlock)
			r.With(s.requireSuperadmin).Post("/accounts/{id}/promote", s.handleAdminPromote)
			r.With(s.requireSuperadmin).Post("/accounts/{id}/demote", s.handleAdminDemote)
			r.Get("/settings", s.handleSettingsGet)
			r.With(s.requireSuperadmin).Put("/settings/open-registration", s.handleSetOpenRegistration)
		})
	})

	// Anything else is a SPA route — serve the embedded frontend.
	r.Handle("/*", spaHandler(s.d.WebFS))
	return r
}
