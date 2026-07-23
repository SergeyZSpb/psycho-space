// Command psycho-space is the single binary that serves the embedded Vue SPA
// and the JSON API. It loads config, connects to Postgres, applies migrations,
// then serves HTTP with graceful shutdown.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/SergeyZSpb/psycho-space/internal/account"
	"github.com/SergeyZSpb/psycho-space/internal/config"
	"github.com/SergeyZSpb/psycho-space/internal/crypto"
	"github.com/SergeyZSpb/psycho-space/internal/db"
	"github.com/SergeyZSpb/psycho-space/internal/httpapi"
	"github.com/SergeyZSpb/psycho-space/internal/logging"
	"github.com/SergeyZSpb/psycho-space/internal/observability"
	"github.com/SergeyZSpb/psycho-space/internal/session"
	"github.com/SergeyZSpb/psycho-space/internal/settings"
	"github.com/SergeyZSpb/psycho-space/internal/vk"
	"github.com/SergeyZSpb/psycho-space/internal/web"
	"github.com/SergeyZSpb/psycho-space/internal/wishlist"
	"github.com/SergeyZSpb/psycho-space/migrations"
)

func main() {
	cfg := config.MustLoad()
	logging.Setup(cfg.LogDir, slog.LevelInfo)
	slog.Info("starting psycho-space", "env", cfg.Env, "addr", cfg.HTTPAddr, "vk_configured", cfg.VK.Configured())

	ctx := context.Background()

	// Tracing: spans (and trace IDs) are always generated; export only when
	// PSYCHOSPACE_OTLP_ENDPOINT is set.
	shutdownTracer, err := observability.Init(ctx, "psycho-space", cfg.OTLPEndpoint)
	if err != nil {
		slog.Error("tracer init failed", "err", err)
		os.Exit(1)
	}
	defer func() { _ = shutdownTracer(context.Background()) }()

	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("database connection failed", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool, migrations.FS); err != nil {
		slog.Error("migrations failed", "err", err)
		os.Exit(1)
	}

	// Crypto + domain wiring.
	enc, err := crypto.NewEncryptor(cfg.EncKey)
	if err != nil {
		slog.Error("encryptor init failed", "err", err)
		os.Exit(1)
	}
	bi, err := crypto.NewBlindIndexer(cfg.HMACKey)
	if err != nil {
		slog.Error("blind indexer init failed", "err", err)
		os.Exit(1)
	}
	accounts := account.NewService(pool, account.NewPostgresRepository(), enc, bi)
	sessions := session.NewManager(pool, cfg.SessionKey, cfg.SessionTTL, cfg.CookieSecure())
	wishlistSvc := wishlist.NewService(pool, wishlist.NewPostgresRepository())
	settingsSvc := settings.NewService(pool)
	vkClient := vk.New(cfg.VK.BaseURL, cfg.VK.AppID, cfg.VK.ServiceToken, cfg.VK.RedirectURI)

	srv := httpapi.NewServer(httpapi.Deps{
		Config:   cfg,
		Pool:     pool,
		WebFS:    web.DistFS(),
		VK:       vkClient,
		Accounts: accounts,
		Sessions: sessions,
		Wishlist: wishlistSvc,
		Settings: settingsSvc,
	})
	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           observability.WrapHandler(srv.Handler(), "http.server"),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		slog.Info("http listening", "addr", cfg.HTTPAddr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http server failed", "err", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		slog.Error("graceful shutdown failed", "err", err)
	}
}
