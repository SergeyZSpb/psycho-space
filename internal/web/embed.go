// Package web embeds the built Vue SPA (web/dist, copied to dist/ here at build
// time) into the binary. A tracked placeholder dist/index.html keeps this
// compiling on a fresh clone that hasn't built the frontend yet.
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
