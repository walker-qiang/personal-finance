// cmd/server runs the personal-finance HTTP backend on localhost:7001 (decision
// #32: localhost-only, no public exposure).
//
// Flags:
//
//	-publish-once   Run the publish job once and exit (no HTTP server).
//	                Useful for `make publish-dry` or a launchd job.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"os"

	"github.com/gin-gonic/gin"

	"github.com/walker-qiang/personal-finance/internal/config"
	"github.com/walker-qiang/personal-finance/internal/db"
	"github.com/walker-qiang/personal-finance/internal/db/store"
	"github.com/walker-qiang/personal-finance/internal/handler"
	"github.com/walker-qiang/personal-finance/internal/publish"
)

func main() {
	var publishOnce bool
	flag.BoolVar(&publishOnce, "publish-once", false, "run publish job once and exit")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	conn, err := db.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("db open: %v", err)
	}
	defer conn.Close()

	st := store.New(conn)
	job := publish.New(cfg, st)

	if publishOnce {
		res := job.Run(context.Background())
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(res)
		if !res.OK {
			os.Exit(1)
		}
		return
	}

	if os.Getenv("GIN_MODE") == "" {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.Use(gin.Recovery(), gin.LoggerWithWriter(os.Stderr))

	api := &handler.API{Store: st, Job: job}
	api.Register(r)

	log.Printf("personal-finance: listening on http://%s (db=%s, worktree=%s, push=%v)",
		cfg.HTTPAddr, cfg.DBPath, cfg.PublishWorktree, cfg.PublishPush)
	if err := r.Run(cfg.HTTPAddr); err != nil {
		log.Fatalf("server: %v", err)
	}
}
