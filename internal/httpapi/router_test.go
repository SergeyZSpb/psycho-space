package httpapi

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func testWebFS() fs.FS {
	return fstest.MapFS{
		"index.html":        {Data: []byte("<html>psycho</html>")},
		"assets/app-abc.js": {Data: []byte("console.log(1)")},
	}
}

func newTestHandler() http.Handler {
	// nil pool/services: /healthz tolerates nil pool; these tests only exercise
	// ping and the SPA fallback.
	return NewServer(Deps{WebFS: testWebFS()}).Handler()
}

func TestPing(t *testing.T) {
	rr := httptest.NewRecorder()
	newTestHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/ping", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "pong") {
		t.Fatalf("body = %q, want pong", rr.Body.String())
	}
}

func TestHealthzNilPool(t *testing.T) {
	rr := httptest.NewRecorder()
	newTestHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
}

func TestSPAServesRealAsset(t *testing.T) {
	rr := httptest.NewRecorder()
	newTestHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/assets/app-abc.js", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "console.log") {
		t.Fatalf("did not serve the asset: %q", rr.Body.String())
	}
}

func TestSPAFallbackToIndex(t *testing.T) {
	rr := httptest.NewRecorder()
	newTestHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/wishlist", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "psycho") {
		t.Fatalf("fallback did not serve index.html: %q", rr.Body.String())
	}
}
