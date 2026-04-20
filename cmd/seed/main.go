// cmd/seed loads a snapshot fixture (seed/snapshot-YYYYMMDD.json) into
// finance.db. It is idempotent: re-running upserts assets and overwrites the
// snapshot row for that asset+date pair (decision: snapshot rows are immutable
// per (asset, date) but a re-import of the same fixture should produce the
// same DB state, not duplicates).
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"strings"

	"github.com/walker-qiang/personal-finance/internal/db"
	"github.com/walker-qiang/personal-finance/internal/db/store"
)

type SeedFile struct {
	SnapshotDate string      `json:"snapshot_date"` // YYYY-MM-DD; applies to all rows lacking their own date
	Notes        string      `json:"notes"`
	Assets       []SeedAsset `json:"assets"`
}

type SeedAsset struct {
	Code             string   `json:"code"`
	Name             string   `json:"name"`
	AssetType        string   `json:"asset_type"`
	Bucket           string   `json:"bucket"`
	Channel          string   `json:"channel"`
	Currency         string   `json:"currency"`
	RiskLevel        string   `json:"risk_level,omitempty"`
	HoldingCostPct   *float64 `json:"holding_cost_pct,omitempty"`
	ExpectedYieldPct *float64 `json:"expected_yield_pct,omitempty"`
	Notes            string   `json:"notes,omitempty"`
	BalanceYuan      float64  `json:"balance_yuan"`
	SnapshotDate     string   `json:"snapshot_date,omitempty"` // overrides file-level default
}

func main() {
	var dbPath, fixture string
	flag.StringVar(&dbPath, "db", os.Getenv("FINANCE_DB_PATH"), "path to finance.db")
	flag.StringVar(&fixture, "file", "seed/snapshot-20260301.json", "seed fixture (json)")
	flag.Parse()

	if dbPath == "" {
		log.Fatal("missing -db (and FINANCE_DB_PATH unset)")
	}

	raw, err := os.ReadFile(fixture)
	if err != nil {
		log.Fatalf("read seed: %v", err)
	}
	var sf SeedFile
	if err := json.Unmarshal(raw, &sf); err != nil {
		log.Fatalf("parse seed: %v", err)
	}
	if strings.TrimSpace(sf.SnapshotDate) == "" {
		log.Fatal("seed: top-level snapshot_date is required (YYYY-MM-DD)")
	}

	conn, err := db.Open(dbPath)
	if err != nil {
		log.Fatalf("db open: %v", err)
	}
	defer conn.Close()

	st := store.New(conn)
	ctx := context.Background()

	var assetCount, snapCount int
	for _, a := range sf.Assets {
		assetID, err := st.UpsertAsset(ctx, store.Asset{
			Code:             a.Code,
			Name:             a.Name,
			AssetType:        a.AssetType,
			Bucket:           a.Bucket,
			Channel:          a.Channel,
			Currency:         coalesce(a.Currency, "CNY"),
			RiskLevel:        nullStringIf(a.RiskLevel),
			HoldingCostPct:   nullFloatPtr(a.HoldingCostPct),
			ExpectedYieldPct: nullFloatPtr(a.ExpectedYieldPct),
			Notes:            a.Notes,
		})
		if err != nil {
			log.Fatalf("upsert asset %q: %v", a.Code, err)
		}
		assetCount++

		date := coalesce(a.SnapshotDate, sf.SnapshotDate)
		balanceCents := int64(math.Round(a.BalanceYuan * 100))
		if _, err := st.UpsertSnapshot(ctx, store.Snapshot{
			AssetID:          assetID,
			SnapshotDate:     date,
			BalanceCents:     balanceCents,
			ExpectedYieldPct: nullFloatPtr(a.ExpectedYieldPct),
		}); err != nil {
			log.Fatalf("upsert snapshot %q@%s: %v", a.Code, date, err)
		}
		snapCount++
	}

	fmt.Printf("seeded ok: assets=%d snapshots=%d (date=%s) db=%s\n", assetCount, snapCount, sf.SnapshotDate, dbPath)
}

func coalesce(s ...string) string {
	for _, v := range s {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func nullStringIf(s string) sql.NullString {
	if strings.TrimSpace(s) == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func nullFloatPtr(p *float64) sql.NullFloat64 {
	if p == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *p, Valid: true}
}
