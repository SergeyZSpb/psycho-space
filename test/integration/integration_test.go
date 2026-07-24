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
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/SergeyZSpb/psycho-space/internal/account"
	"github.com/SergeyZSpb/psycho-space/internal/config"
	"github.com/SergeyZSpb/psycho-space/internal/crypto"
	"github.com/SergeyZSpb/psycho-space/internal/db"
	"github.com/SergeyZSpb/psycho-space/internal/game"
	"github.com/SergeyZSpb/psycho-space/internal/httpapi"
	"github.com/SergeyZSpb/psycho-space/internal/observability"
	"github.com/SergeyZSpb/psycho-space/internal/session"
	"github.com/SergeyZSpb/psycho-space/internal/settings"
	"github.com/SergeyZSpb/psycho-space/internal/vk"
	"github.com/SergeyZSpb/psycho-space/internal/wishlist"
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
	// Generate trace IDs (no export) so error responses carry a real trace_id.
	if _, err := observability.Init(ctx, "psycho-space-test", ""); err != nil {
		fmt.Println("tracer:", err)
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
	h := httpapi.NewServer(httpapi.Deps{
		Config:   cfg,
		Pool:     pool,
		WebFS:    fstest.MapFS{"index.html": {Data: []byte("<html>psycho</html>")}},
		VK:       vkClient,
		Accounts: newAccountService(),
		Sessions: sessions,
		Wishlist: wishlist.NewService(pool, wishlist.NewPostgresRepository()),
		Game:     game.NewService(pool, game.NewPostgresRepository()),
		Settings: settings.NewService(pool),
	}).Handler()
	return observability.WrapHandler(h, "http.server")
}

// buildAppNoVK builds the app with VK intentionally unconfigured.
func buildAppNoVK() http.Handler {
	sessions := session.NewManager(pool, key(3), time.Hour, false)
	h := httpapi.NewServer(httpapi.Deps{
		Config:   config.Config{Env: "dev"}, // VK empty → not configured
		Pool:     pool,
		WebFS:    fstest.MapFS{"index.html": {Data: []byte("x")}},
		VK:       vk.New("", "", "", ""),
		Accounts: newAccountService(),
		Sessions: sessions,
	}).Handler()
	return observability.WrapHandler(h, "http.server")
}

// fakeVK stands in for id.vk.ru: returns tokens then the profile.
func fakeVK(userID, first, last string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/oauth2/auth":
			fmt.Fprintf(w, `{"access_token":"AT","refresh_token":"RT","id_token":"IDT","expires_in":3600,"user_id":%s}`, userID)
		case "/oauth2/user_info":
			// sex + birthday are part of VK's base right and arrive on every login.
			fmt.Fprintf(w, `{"user":{"user_id":"%s","first_name":%q,"last_name":%q,"avatar":"https://vk/av.jpg","sex":2,"birthday":"15.5.1990"}}`, userID, first, last)
		default:
			http.NotFound(w, r)
		}
	}))
}

// fakeVKDynamic mints a distinct user per login: the `code` sent in the callback
// IS the numeric user id, so one server can create many users (name = "User <id>").
func fakeVKDynamic() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/oauth2/auth":
			code := r.Form.Get("code")
			fmt.Fprintf(w, `{"access_token":"AT-%s","refresh_token":"RT","id_token":"IDT","expires_in":3600,"user_id":%s}`, code, code)
		case "/oauth2/user_info":
			uid := strings.TrimPrefix(r.Form.Get("access_token"), "AT-")
			fmt.Fprintf(w, `{"user":{"user_id":"%s","first_name":"User","last_name":%q,"avatar":"https://vk/av.jpg"}}`, uid, uid)
		default:
			http.NotFound(w, r)
		}
	}))
}

// accountIDByUID looks up an account's id by its VK user id (via blind index).
func accountIDByUID(t *testing.T, uid string) string {
	t.Helper()
	bi, _ := crypto.NewBlindIndexer(key(2))
	var id string
	if err := pool.QueryRow(context.Background(),
		`SELECT id::text FROM accounts WHERE vk_user_ref = $1`, bi.Index(uid)).Scan(&id); err != nil {
		t.Fatalf("accountIDByUID(%s): %v", uid, err)
	}
	return id
}

// roleOf reads an account's current role.
func roleOf(t *testing.T, id string) string {
	t.Helper()
	var role string
	if err := pool.QueryRow(context.Background(), `SELECT role FROM accounts WHERE id = $1::uuid`, id).Scan(&role); err != nil {
		t.Fatalf("roleOf: %v", err)
	}
	return role
}

// setRoleStatus sets role+status directly (mirrors the bootstrap superadmin script).
func setRoleStatus(t *testing.T, id, role, status string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`UPDATE accounts SET role=$2, status=$3, updated_at=now() WHERE id=$1::uuid`, id, role, status); err != nil {
		t.Fatalf("setRoleStatus: %v", err)
	}
}
