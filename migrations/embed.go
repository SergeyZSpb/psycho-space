// Package migrations embeds the SQL migration files so they ship inside the
// binary and are applied automatically at startup.
package migrations

import "embed"

// FS holds every migration file, consumed by db.Migrate.
//
//go:embed *.sql
var FS embed.FS
