package httpapi

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// spaHandler serves static assets from fsys and falls back to index.html for
// any path that isn't a real file, so client-side routes (e.g. /wishlist) load
// the SPA instead of 404ing.
func spaHandler(fsys fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(fsys))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		if f, err := fsys.Open(name); err == nil {
			_ = f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		// Unknown path → SPA entry point.
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r, fsys, "index.html")
	})
}
