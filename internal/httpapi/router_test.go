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

// The VK redirect target is a page. VK navigates a browser to it with GET and a
// query string, so it must reach the SPA — the API endpoint that used to be
// registered as the redirect URL is POST-only and answered 405, which is exactly
// what a person saw when their browser fell back to redirect mode.
func TestVKRedirectTargetIsServedAsAPage(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/auth/redirect?code=c&device_id=d&state=s", nil)
	newTestHandler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — a browser cannot finish a VK login here", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "psycho") {
		t.Fatalf("did not serve index.html: %q", rr.Body.String())
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
