package migrations

import "embed"

// Files contains all forward-only SQL migration files.
//
//go:embed *.sql
var Files embed.FS
