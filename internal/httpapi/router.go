// Package httpapi wires the chi router, middleware, and handlers. The Server
// struct holds handler dependencies and grows as later phases add services.
package httpapi

import (
	"io/fs"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Server carries the dependencies shared by HTTP handlers.
type Server struct {
	pool  *pgxpool.Pool
	webFS fs.FS
}

// NewServer builds the HTTP server dependencies.
func NewServer(pool *pgxpool.Pool, webFS fs.FS) *Server {
	return &Server{pool: pool, webFS: webFS}
}

// Handler builds the router with middleware and routes.
func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.RealIP)
	r.Use(requestLogger)
	r.Use(bodyLimit(1 << 20)) // 1 MiB request cap

	r.Get("/healthz", s.handleHealthz)

	r.Route("/api", func(r chi.Router) {
		r.Get("/ping", handlePing)
	})

	// Anything else is a SPA route — serve the embedded frontend.
	r.Handle("/*", spaHandler(s.webFS))
	return r
}
