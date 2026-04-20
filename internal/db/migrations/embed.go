// Package migrations embeds all goose .sql files so that programs (cmd/import,
// future cmd/migrate, tests) can run migrations without depending on the
// `goose` CLI being on PATH or pointing at the right directory.
//
// The Makefile still drives day-to-day migrate via the goose binary because
// the human-facing developer flow (`make migrate-status`, etc.) is more
// ergonomic via the CLI. This embed is purely for in-process use.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
