// Package db opens the SQLite handle and exposes thin connection helpers.
// The migration tooling is goose (run via `make migrate` or cmd/migrate),
// not Go code, so application start-up does not auto-migrate. This makes
// schema changes explicit (契约 13/15: nothing slips into the facts layer
// without an authored step).
package db

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (no cgo)
)

// Open returns a SQLite handle with PRAGMAs sane for a single-process,
// single-user backend. WAL keeps the publish job (read-only dump) from
// blocking writers and vice versa.
func Open(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)", path)
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", path, err)
	}
	conn.SetMaxOpenConns(4) // allow concurrent reads; WAL handles write serialization
	conn.SetMaxIdleConns(2) // keep a few idle connections ready
	conn.SetConnMaxLifetime(10 * time.Minute)
	if err := conn.Ping(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ping sqlite %q: %w", path, err)
	}
	return conn, nil
}
