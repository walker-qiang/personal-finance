package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Config bundles the runtime knobs needed by the server, seed loader and
// publish job. Everything is sourced from env vars with sane defaults so the
// `make` targets work without an .env file.
type Config struct {
	// DBPath is the SQLite file. Default: <repo>/state/finance.db
	DBPath string
	// PublishWorktree is the absolute path to the obsidian-wiki publish
	// worktree (created via `git worktree add`, see publish-workflow.md §2.1).
	PublishWorktree string
	// PublishPush controls whether the publish job runs `git push` after the
	// commit. Default false (M1 safety; flip with PUBLISH_PUSH=1).
	PublishPush bool
	// HTTPAddr is the listen address. Localhost-only by design (decision #32).
	HTTPAddr string
	// LogPath is where publish-job failures append (~/Library/Logs/personal/...).
	LogPath string
}

func Load() (*Config, error) {
	c := &Config{
		DBPath:          getenvDefault("FINANCE_DB_PATH", defaultDBPath()),
		PublishWorktree: getenvDefault("PUBLISH_WORKTREE", filepath.Join(os.Getenv("HOME"), "obsidian-wiki-publish-worktree")),
		PublishPush:     os.Getenv("PUBLISH_PUSH") == "1",
		HTTPAddr:        getenvDefault("HTTP_ADDR", "127.0.0.1:7001"),
		LogPath:         getenvDefault("PUBLISH_LOG", filepath.Join(os.Getenv("HOME"), "Library", "Logs", "personal", "personal-finance.log")),
	}
	if c.DBPath == "" {
		return nil, errors.New("FINANCE_DB_PATH is empty")
	}
	if c.PublishWorktree == "" {
		return nil, errors.New("PUBLISH_WORKTREE is empty")
	}
	if _, err := os.Stat(filepath.Dir(c.DBPath)); err != nil {
		if mkErr := os.MkdirAll(filepath.Dir(c.DBPath), 0o755); mkErr != nil {
			return nil, fmt.Errorf("cannot create db dir: %w", mkErr)
		}
	}
	if err := os.MkdirAll(filepath.Dir(c.LogPath), 0o755); err != nil {
		return nil, fmt.Errorf("cannot create log dir: %w", err)
	}
	return c, nil
}

func defaultDBPath() string {
	wd, err := os.Getwd()
	if err != nil {
		return "state/finance.db"
	}
	return filepath.Join(wd, "state", "finance.db")
}

func getenvDefault(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
