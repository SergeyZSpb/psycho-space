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
	"github.com/SergeyZSpb/psycho-space/internal/gameassets"
	"github.com/SergeyZSpb/psycho-space/internal/gamekhimki"
	"github.com/SergeyZSpb/psycho-space/internal/httpapi"
	"github.com/SergeyZSpb/psycho-space/internal/logging"
	"github.com/SergeyZSpb/psycho-space/internal/observability"
	"github.com/SergeyZSpb/psycho-space/internal/realtime"
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
	// «Смолтолк в Химках» AI judge: OpenAI-compatible LLM (Yandex Cloud /
	// DeepSeek). When not configured, the game's /attempt endpoint returns 503
	// (see handler). Each game is wired separately — nothing here is shared.
	gameKhimkiEval := gamekhimki.NewOpenAIEvaluator(cfg.LLM)
	slog.Info("game-khimki evaluator", "llm_configured", cfg.LLM.Enabled(), "model", cfg.LLM.Model)
	// Art blobs are shared infrastructure, not a game: one store serves every
	// game, scoped by game_key. The game is handed it as a narrow interface so
	// the dependency points at infrastructure and never back.
	gameAssetsSvc := gameassets.NewService(pool, gameassets.NewPostgresRepository())
	gameKhimkiSvc := gamekhimki.NewService(pool, gamekhimki.NewPostgresRepository(), gameKhimkiEval, gameAssetsSvc)
	settingsSvc := settings.NewService(pool)

	// The realtime hub outlives any single request. Its context is what every
	// socket is bound to, so cancelling it is what drains them on shutdown.
	hubCtx, stopHub := context.WithCancel(context.Background())
	defer stopHub()
	hub := realtime.NewHub()
	go hub.Run(hubCtx)
	vkClient := vk.New(cfg.VK.BaseURL, cfg.VK.AppID, cfg.VK.ServiceToken, cfg.VK.RedirectURI)

	var vkVerifier *vk.IDTokenVerifier
	if cfg.VK.VerifyIDToken {
		vkVerifier, err = vk.NewIDTokenVerifier(ctx, cfg.VK.JWKSURL, cfg.VK.Issuer)
		if err != nil {
			slog.Error("vk id_token verifier init failed", "err", err)
			os.Exit(1)
		}
		slog.Info("vk id_token verification enabled", "jwks", cfg.VK.JWKSURL)
	}

	srv := httpapi.NewServer(httpapi.Deps{
		Config:     cfg,
		Pool:       pool,
		WebFS:      web.DistFS(),
		VK:         vkClient,
		Accounts:   accounts,
		Sessions:   sessions,
		Wishlist:   wishlistSvc,
		GameKhimki: gameKhimkiSvc,
		GameAssets: gameAssetsSvc,
		Settings:   settingsSvc,
		VKVerifier: vkVerifier,

		Realtime:    hub,
		RealtimeCtx: hubCtx,
	})
	httpServer := httpapi.NewHTTPServer(cfg.HTTPAddr, srv.Handler(), httpapi.DefaultTimeouts())

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

	// Drain the WebSocket hub FIRST. http.Server.Shutdown does not close or
	// wait for hijacked connections — its own documentation says so — and this
	// service restarts on every deploy, several times a day. Without this every
	// connected player gets a bare TCP reset instead of a close frame, and the
	// client cannot tell a planned restart from a network fault.
	stopHub()
	select {
	case <-hub.Done():
	case <-time.After(5 * time.Second):
		slog.Warn("realtime hub did not drain in time")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		slog.Error("graceful shutdown failed", "err", err)
	}
}
