//go:build integration

// Package integration runs the app against a real PostgreSQL (testcontainers)
// and a fake VK ID server. Run with: ./dev.sh integration
package integration

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"testing/fstest"
	"time"

	"github.com/SergeyZSpb/psycho-space/internal/account"
	"github.com/SergeyZSpb/psycho-space/internal/config"
	"github.com/SergeyZSpb/psycho-space/internal/crypto"
	"github.com/SergeyZSpb/psycho-space/internal/db"
	"github.com/SergeyZSpb/psycho-space/internal/httpapi"
	"github.com/SergeyZSpb/psycho-space/internal/session"
	"github.com/SergeyZSpb/psycho-space/internal/vk"
	"github.com/SergeyZSpb/psycho-space/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

var pool *pgxpool.Pool

func key(b byte) []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = b
	}
	return k
}

func TestMain(m *testing.M) {
	ctx := context.Background()
	container, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("psychospace"),
		postgres.WithUsername("psychospace"),
		postgres.WithPassword("psychospace"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(90*time.Second),
		),
	)
	if err != nil {
		fmt.Println("start postgres:", err)
		os.Exit(1)
	}
	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Println("connection string:", err)
		os.Exit(1)
	}
	pool, err = db.NewPool(ctx, connStr)
	if err != nil {
		fmt.Println("pool:", err)
		os.Exit(1)
	}
	if err := db.Migrate(ctx, pool, migrations.FS); err != nil {
		fmt.Println("migrate:", err)
		os.Exit(1)
	}

	code := m.Run()

	pool.Close()
	_ = testcontainers.TerminateContainer(container)
	os.Exit(code)
}

const vkRedirect = "https://psycho-space.ru/api/auth/vk/callback"

func newAccountService() *account.Service {
	enc, _ := crypto.NewEncryptor(key(1))
	bi, _ := crypto.NewBlindIndexer(key(2))
	return account.NewService(pool, account.NewPostgresRepository(), enc, bi)
}

func buildApp(vkBaseURL string) http.Handler {
	sessions := session.NewManager(pool, key(3), time.Hour, false)
	vkClient := vk.New(vkBaseURL, "app-1", "svc", vkRedirect)
	cfg := config.Config{
		Env: "dev",
		VK:  config.VK{AppID: "app-1", ServiceToken: "svc", RedirectURI: vkRedirect, BaseURL: vkBaseURL},
	}
	return httpapi.NewServer(httpapi.Deps{
		Config:   cfg,
		Pool:     pool,
		WebFS:    fstest.MapFS{"index.html": {Data: []byte("<html>psycho</html>")}},
		VK:       vkClient,
		Accounts: newAccountService(),
		Sessions: sessions,
	}).Handler()
}

// buildAppNoVK builds the app with VK intentionally unconfigured.
func buildAppNoVK() http.Handler {
	sessions := session.NewManager(pool, key(3), time.Hour, false)
	return httpapi.NewServer(httpapi.Deps{
		Config:   config.Config{Env: "dev"}, // VK empty → not configured
		Pool:     pool,
		WebFS:    fstest.MapFS{"index.html": {Data: []byte("x")}},
		VK:       vk.New("", "", "", ""),
		Accounts: newAccountService(),
		Sessions: sessions,
	}).Handler()
}

// fakeVK stands in for id.vk.ru: returns tokens then the profile.
func fakeVK(userID, first, last string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/oauth2/auth":
			fmt.Fprintf(w, `{"access_token":"AT","refresh_token":"RT","id_token":"IDT","expires_in":3600,"user_id":%s}`, userID)
		case "/oauth2/user_info":
			fmt.Fprintf(w, `{"user":{"user_id":"%s","first_name":%q,"last_name":%q,"avatar":"https://vk/av.jpg"}}`, userID, first, last)
		default:
			http.NotFound(w, r)
		}
	}))
}
