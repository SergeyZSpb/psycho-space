package httpapi

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// spaHandler serves static assets from fsys and falls back to index.html for
// any path that isn't a real file (client-side routes). When the SPA hasn't been
// built into the embed dir yet, it serves a built-in placeholder page.
func spaHandler(fsys fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(fsys))
	hasIndex := false
	if f, err := fsys.Open("index.html"); err == nil {
		_ = f.Close()
		hasIndex = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name != "" && name != "index.html" {
			if f, err := fsys.Open(name); err == nil {
				_ = f.Close()
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		if hasIndex {
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFileFS(w, r, fsys, "index.html")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(fallbackHTML))
	})
}

// fallbackHTML is shown only when the Vue SPA has not been built into the embed
// dir (e.g. a local `go run` without building the frontend). Production always
// embeds the real SPA.
const fallbackHTML = `<!doctype html>
<html lang="ru"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>психоспасе</title>
<style>:root{color-scheme:light dark}body{margin:0;min-height:100vh;display:grid;place-items:center;
font-family:system-ui,sans-serif;background:#0b1513;color:#d5f5ef}main{max-width:640px;padding:40px;text-align:center}
h1{color:#2dd4bf;text-shadow:0 0 20px #2dd4bf99}</style></head>
<body><main><h1>психоспасе</h1>
<p>это супер нейрослоп приложулька оххх оххх психоспасе</p>
<p style="opacity:.6;font-size:14px">frontend не собран — запусти <code>npm run build</code> в <code>web/</code></p>
</main></body></html>`
