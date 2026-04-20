// cmd/import is the disaster-recovery (DR) inverse of the publish job:
//   read obsidian-wiki/finance/exports/*.csv → rebuild state/finance.db
//
// Why this exists:
//   state/finance.db lives only on the host running personal-finance and is
//   gitignored. The publish job emits CSVs to the public/private wiki repo,
//   which IS backed up (git remote). If finance.db is lost (disk failure,
//   migration mistake, accidental rm), this command rebuilds it from the
//   most recent published snapshot.
//
// Round-trip property:
//   import → publish should produce CSVs byte-equivalent to the source CSVs
//   modulo timestamps in the commit message. This is the strongest guarantee
//   we have that the export format is lossless. Use -verify to assert it.
//
// Safety:
//   By default, refuses to write into a non-empty finance.db. Use -force to
//   wipe and re-import. If the DB file is missing, it is created and
//   migrated automatically (embedded *.sql via internal/db/migrations).
package main

import (
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/pressly/goose/v3"

	"github.com/walker-qiang/personal-finance/internal/config"
	"github.com/walker-qiang/personal-finance/internal/db"
	migrations "github.com/walker-qiang/personal-finance/internal/db/migrations"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	defaultFrom := filepath.Join(cfg.PublishWorktree, "finance", "exports")
	var (
		fromDir = flag.String("from", defaultFrom, "directory containing exported CSVs (assets/snapshots/transactions/holdings-YYYY-MM-DD.csv)")
		dbPath  = flag.String("db", cfg.DBPath, "destination finance.db path")
		date    = flag.String("date", "", "YYYY-MM-DD to import; empty = auto-pick most recent date present in -from")
		force   = flag.Bool("force", false, "wipe existing assets/snapshots/transactions before importing (DESTRUCTIVE)")
		verify  = flag.Bool("verify", false, "after import, dump fresh CSVs to a temp dir and diff against the source (round-trip parity check)")
	)
	flag.Parse()

	chosenDate, err := resolveDate(*fromDir, *date)
	if err != nil {
		log.Fatalf("resolve date: %v", err)
	}
	log.Printf("import: from=%s date=%s db=%s force=%v verify=%v",
		*fromDir, chosenDate, *dbPath, *force, *verify)

	conn, err := openOrInit(*dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer conn.Close()

	if err := guardNonEmpty(conn, *force); err != nil {
		log.Fatalf("%v", err)
	}

	ctx := context.Background()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		log.Fatalf("begin tx: %v", err)
	}
	defer func() {
		// rollback if something panics; commit path will have run already.
		_ = tx.Rollback()
	}()

	if *force {
		if err := truncateAll(ctx, tx); err != nil {
			log.Fatalf("truncate: %v", err)
		}
	}

	stats := importStats{}

	codeToID, n, err := importAssets(ctx, tx, filepath.Join(*fromDir, "assets-"+chosenDate+".csv"))
	if err != nil {
		log.Fatalf("import assets: %v", err)
	}
	stats.assets = n

	n, err = importSnapshots(ctx, tx, filepath.Join(*fromDir, "snapshots-"+chosenDate+".csv"), codeToID)
	if err != nil {
		log.Fatalf("import snapshots: %v", err)
	}
	stats.snapshots = n

	n, err = importTransactions(ctx, tx, filepath.Join(*fromDir, "transactions-"+chosenDate+".csv"), codeToID)
	if err != nil {
		log.Fatalf("import transactions: %v", err)
	}
	stats.transactions = n

	if err := tx.Commit(); err != nil {
		log.Fatalf("commit: %v", err)
	}

	fmt.Printf("import ok: assets=%d snapshots=%d transactions=%d (date=%s)\n",
		stats.assets, stats.snapshots, stats.transactions, chosenDate)

	if *verify {
		if err := roundTripVerify(ctx, conn, *fromDir, chosenDate); err != nil {
			log.Fatalf("verify: %v", err)
		}
		fmt.Println("verify ok: round-trip CSVs are byte-identical to source")
	}
}

type importStats struct {
	assets       int
	snapshots    int
	transactions int
}

// ---- date resolution ----

var datedRE = regexp.MustCompile(`^(?:assets|snapshots|transactions|holdings)-(\d{4}-\d{2}-\d{2})\.csv$`)

func resolveDate(dir, requested string) (string, error) {
	if requested != "" {
		// just check the assets file exists; snapshots/transactions are tolerated
		// to be missing (e.g. a fresh setup with assets only).
		if _, err := os.Stat(filepath.Join(dir, "assets-"+requested+".csv")); err != nil {
			return "", fmt.Errorf("assets-%s.csv not found in %s: %w", requested, dir, err)
		}
		return requested, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("read source dir: %w", err)
	}
	dates := map[string]struct{}{}
	for _, e := range entries {
		m := datedRE.FindStringSubmatch(e.Name())
		if m != nil {
			dates[m[1]] = struct{}{}
		}
	}
	if len(dates) == 0 {
		return "", fmt.Errorf("no exported CSVs found in %s (looking for {assets,snapshots,transactions,holdings}-YYYY-MM-DD.csv)", dir)
	}
	sorted := make([]string, 0, len(dates))
	for d := range dates {
		sorted = append(sorted, d)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(sorted)))
	return sorted[0], nil
}

// ---- DB lifecycle ----

func openOrInit(path string) (*sql.DB, error) {
	exists := fileExists(path)
	if !exists {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
	}
	conn, err := db.Open(path)
	if err != nil {
		return nil, err
	}
	if !exists {
		log.Printf("import: %s did not exist; creating + applying embedded migrations", path)
		goose.SetBaseFS(migrations.FS)
		if err := goose.SetDialect("sqlite3"); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("goose dialect: %w", err)
		}
		if err := goose.Up(conn, "."); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("goose up: %w", err)
		}
	}
	return conn, nil
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func guardNonEmpty(conn *sql.DB, force bool) error {
	var n int
	err := conn.QueryRow(`
		SELECT (SELECT COUNT(*) FROM assets) +
		       (SELECT COUNT(*) FROM snapshots) +
		       (SELECT COUNT(*) FROM transactions)`).Scan(&n)
	if err != nil {
		return fmt.Errorf("count rows: %w", err)
	}
	if n > 0 && !force {
		return fmt.Errorf("destination DB is non-empty (assets+snapshots+transactions = %d rows); refusing to import. Re-run with -force to wipe and replace, or back up first", n)
	}
	return nil
}

func truncateAll(ctx context.Context, tx *sql.Tx) error {
	// Order matters: snapshots/transactions FK -> assets, so wipe leaves first.
	stmts := []string{
		`DELETE FROM transactions`,
		`DELETE FROM snapshots`,
		`DELETE FROM assets`,
		// Reset autoincrement so re-imports give stable IDs (helps round-trip).
		// sqlite_sequence may not exist if no AUTOINCREMENT row was ever
		// inserted, which is fine — ignore the error.
		`DELETE FROM sqlite_sequence WHERE name IN ('assets','snapshots','transactions')`,
	}
	for _, s := range stmts {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			// Tolerate sqlite_sequence not existing
			if strings.Contains(err.Error(), "no such table: sqlite_sequence") {
				continue
			}
			return fmt.Errorf("%s: %w", s, err)
		}
	}
	return nil
}

// ---- CSV readers ----

// openCSV opens a CSV, validates the header matches `wantHeader` exactly
// (column count, names, order). Mismatch = stop and explain, because the
// publish-side schema is the source of truth and an unrecognized column
// shape almost always means importing the wrong format.
func openCSV(path string, wantHeader []string) (*csv.Reader, *os.File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	r := csv.NewReader(f)
	r.FieldsPerRecord = len(wantHeader)
	header, err := r.Read()
	if err != nil {
		_ = f.Close()
		return nil, nil, fmt.Errorf("read header: %w", err)
	}
	if !equalStrings(header, wantHeader) {
		_ = f.Close()
		return nil, nil, fmt.Errorf("header mismatch in %s\n  got:  %v\n  want: %v", path, header, wantHeader)
	}
	return r, f, nil
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---- importers ----

// assets header (must match publish.go dumpAssets):
//   code, name, asset_type, bucket, channel, currency, risk_level,
//   holding_cost_pct, expected_yield_pct, notes, created_at
func importAssets(ctx context.Context, tx *sql.Tx, path string) (map[string]int64, int, error) {
	r, f, err := openCSV(path, []string{
		"code", "name", "asset_type", "bucket", "channel", "currency",
		"risk_level", "holding_cost_pct", "expected_yield_pct", "notes", "created_at",
	})
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()

	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO assets (code, name, asset_type, bucket, channel, currency,
                    risk_level, holding_cost_pct, expected_yield_pct,
                    notes, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return nil, 0, err
	}
	defer stmt.Close()

	codeToID := map[string]int64{}
	count := 0
	for {
		row, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, count, fmt.Errorf("row %d: %w", count+1, err)
		}
		hcp, err := nullFloat(row[7])
		if err != nil {
			return nil, count, fmt.Errorf("row %d holding_cost_pct: %w", count+1, err)
		}
		eyp, err := nullFloat(row[8])
		if err != nil {
			return nil, count, fmt.Errorf("row %d expected_yield_pct: %w", count+1, err)
		}
		res, err := stmt.ExecContext(ctx,
			row[0], row[1], row[2], row[3], row[4], row[5],
			nullStr(row[6]), hcp, eyp,
			row[9], row[10],
		)
		if err != nil {
			return nil, count, fmt.Errorf("row %d code=%q: %w", count+1, row[0], err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return nil, count, err
		}
		codeToID[row[0]] = id
		count++
	}
	return codeToID, count, nil
}

// snapshots header:
//   asset_code, asset_name, snapshot_date, balance_yuan, balance_cents,
//   expected_yield_pct, actual_yield_pct, notes, created_at
//
// We use balance_cents (canonical) and ignore balance_yuan (helper).
func importSnapshots(ctx context.Context, tx *sql.Tx, path string, codeToID map[string]int64) (int, error) {
	r, f, err := openCSV(path, []string{
		"asset_code", "asset_name", "snapshot_date", "balance_yuan", "balance_cents",
		"expected_yield_pct", "actual_yield_pct", "notes", "created_at",
	})
	if err != nil {
		// snapshots file may legitimately not exist if user is importing a brand-new setup
		if errors.Is(err, os.ErrNotExist) {
			log.Printf("import: %s missing, skipping snapshots", path)
			return 0, nil
		}
		return 0, err
	}
	defer f.Close()

	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO snapshots (asset_id, snapshot_date, balance_cents,
                       expected_yield_pct, actual_yield_pct, notes, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	count := 0
	for {
		row, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return count, fmt.Errorf("row %d: %w", count+1, err)
		}
		assetID, ok := codeToID[row[0]]
		if !ok {
			return count, fmt.Errorf("row %d: asset_code %q not in assets CSV", count+1, row[0])
		}
		cents, err := strconv.ParseInt(row[4], 10, 64)
		if err != nil {
			return count, fmt.Errorf("row %d balance_cents %q: %w", count+1, row[4], err)
		}
		eyp, err := nullFloat(row[5])
		if err != nil {
			return count, fmt.Errorf("row %d expected_yield_pct: %w", count+1, err)
		}
		ayp, err := nullFloat(row[6])
		if err != nil {
			return count, fmt.Errorf("row %d actual_yield_pct: %w", count+1, err)
		}
		if _, err := stmt.ExecContext(ctx, assetID, row[2], cents, eyp, ayp, row[7], row[8]); err != nil {
			return count, fmt.Errorf("row %d: %w", count+1, err)
		}
		count++
	}
	return count, nil
}

// transactions header:
//   asset_code, asset_name, txn_date, direction, amount_yuan, amount_cents,
//   fee_yuan, fee_cents, notes, created_at
func importTransactions(ctx context.Context, tx *sql.Tx, path string, codeToID map[string]int64) (int, error) {
	r, f, err := openCSV(path, []string{
		"asset_code", "asset_name", "txn_date", "direction",
		"amount_yuan", "amount_cents", "fee_yuan", "fee_cents",
		"notes", "created_at",
	})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			log.Printf("import: %s missing, skipping transactions", path)
			return 0, nil
		}
		return 0, err
	}
	defer f.Close()

	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO transactions (asset_id, txn_date, direction,
                          amount_cents, fee_cents, notes, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	count := 0
	for {
		row, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return count, fmt.Errorf("row %d: %w", count+1, err)
		}
		assetID, ok := codeToID[row[0]]
		if !ok {
			return count, fmt.Errorf("row %d: asset_code %q not in assets CSV", count+1, row[0])
		}
		amt, err := strconv.ParseInt(row[5], 10, 64)
		if err != nil {
			return count, fmt.Errorf("row %d amount_cents %q: %w", count+1, row[5], err)
		}
		fee, err := strconv.ParseInt(row[7], 10, 64)
		if err != nil {
			return count, fmt.Errorf("row %d fee_cents %q: %w", count+1, row[7], err)
		}
		if _, err := stmt.ExecContext(ctx, assetID, row[2], row[3], amt, fee, row[8], row[9]); err != nil {
			return count, fmt.Errorf("row %d: %w", count+1, err)
		}
		count++
	}
	return count, nil
}

// ---- nullable helpers ----

func nullStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func nullFloat(s string) (interface{}, error) {
	if s == "" {
		return nil, nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil, err
	}
	return v, nil
}
