// Package migrations embeds the SQL migration files so the server binary can
// apply them at startup without depending on files present on disk.
package migrations

import "embed"

// Files contains all *.sql migration files in lexicographic order.
//
//go:embed *.sql
var Files embed.FS
