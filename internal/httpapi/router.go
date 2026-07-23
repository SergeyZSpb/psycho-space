// Package httpapi wires the chi router, middleware, and handlers.
package httpapi

import (
	"io/fs"
	"net/http"

	"github.com/SergeyZSpb/psycho-space/internal/account"
	"github.com/SergeyZSpb/psycho-space/internal/config"
	"github.com/SergeyZSpb/psycho-space/internal/session"
	"github.com/SergeyZSpb/psycho-space/internal/settings"
	"github.com/SergeyZSpb/psycho-space/internal/vk"
	"github.com/SergeyZSpb/psycho-space/internal/wishlist"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Deps bundles everything the handlers need. Fields may be nil in tests that
// don't exercise the corresponding routes.
type Deps struct {
	Config   config.Config
	Pool     *pgxpool.Pool
	WebFS    fs.FS
	VK       *vk.Client
	Accounts *account.Service
	Sessions *session.Manager
	Wishlist *wishlist.Service
	Settings *settings.Service
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
	r.Use(traceHeader)
	r.Use(requestLogger)
	r.Use(bodyLimit(1 << 20)) // 1 MiB request cap

	r.Get("/healthz", s.handleHealthz)

	r.Route("/api", func(r chi.Router) {
		r.Get("/ping", handlePing)

		r.Route("/auth", func(r chi.Router) {
			r.Get("/vk/state", s.handleVKState)
			r.Post("/vk/callback", s.handleVKCallback)
			r.Get("/me", s.handleMe)
			r.Post("/logout", s.handleLogout)
		})

		// Wishlist — approved users only. Items and comments are both upvotable.
		r.Route("/wishlist", func(r chi.Router) {
			r.Use(s.requireAuth)
			r.Get("/items", s.handleWishlistList)
			r.Post("/items", s.handleWishlistCreate)
			r.Post("/items/{id}/vote", s.handleVote)
			r.Delete("/items/{id}/vote", s.handleUnvote)
			r.Get("/items/{id}/comments", s.handleCommentList)
			r.Post("/items/{id}/comments", s.handleCommentCreate)
			r.Post("/comments/{id}/vote", s.handleCommentVote)
			r.Delete("/comments/{id}/vote", s.handleCommentUnvote)
		})

		// Admin — approve/block for admins; promote + settings for superadmin only.
		r.Route("/admin", func(r chi.Router) {
			r.Use(s.requireAuth)
			r.Use(s.requireAdmin)
			r.Get("/accounts", s.handleAdminList)
			r.Post("/accounts/{id}/approve", s.handleAdminApprove)
			r.Post("/accounts/{id}/block", s.handleAdminBlock)
			r.With(s.requireSuperadmin).Post("/accounts/{id}/promote", s.handleAdminPromote)
			r.Get("/settings", s.handleSettingsGet)
			r.With(s.requireSuperadmin).Put("/settings/open-registration", s.handleSetOpenRegistration)
		})
	})

	// Anything else is a SPA route — serve the embedded frontend.
	r.Handle("/*", spaHandler(s.d.WebFS))
	return r
}
