// Package web embeds the built Vue SPA (web/dist, copied to dist/ here at build
// time) into the binary.
//
// dist/.gitkeep is tracked, and that is load-bearing rather than tidiness: the
// directive below is `all:dist`, whose `all:` prefix is what makes a dotfile
// count, so the tracked .gitkeep is the one thing that lets `go build ./...`
// succeed on a checkout where the frontend has never been built. Without it the
// Go toolchain fails at compile time with "pattern all:dist: no matching files
// found", which is what a fresh clone and every CI job that does not build the
// SPA first would hit. Serving is unaffected: spaHandler finds no index.html and
// serves its built-in placeholder page instead.
//
// The file is deleted by every SPA build (Vite empties this directory) and put
// back by the keepEmbedPlaceholder plugin in web/vite.config.ts, which is where
// the reasoning lives. If it ever goes missing from git, `go build` breaks
// before any test runs.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// DistFS returns the embedded SPA as a filesystem rooted at the dist directory.
func DistFS() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(err) // dist is always embedded (at least the placeholder)
	}
	return sub
}
