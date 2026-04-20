// Package csvexport contains the canonical CSV writers for the 4 finance
// tables (assets, snapshots, transactions, holdings).
//
// Why a shared package: both `internal/publish` (writes CSVs to the publish
// worktree) and `cmd/import` (round-trip verification) MUST produce byte-
// identical output for the round-trip property to hold. Duplicating the
// writers in two places is the kind of drift that silently breaks DR. By
// keeping a single source of truth, a change to the CSV format only needs
// to land here and the verify path catches mismatches against the previous
// publish.
//
// Format contract:
//   - LF line endings (encoding/csv default), UTF-8, no BOM
//   - Header row first, in the exact order documented at each writer
//   - Empty string = NULL
//   - Money: emitted as both *_yuan ("12345.67", 2 decimals) and *_cents
//     (canonical int). Importers should always read *_cents.
//   - created_at: ISO8601 UTC ("YYYY-MM-DDTHH:MM:SS.sssZ"), exported as-is
package csvexport

import (
	"context"
	"database/sql"
	"encoding/csv"
	"fmt"
	"os"
	"strconv"

	"github.com/walker-qiang/personal-finance/internal/db/store"
)

// Assets header (DO NOT REORDER; cmd/import depends on this).
var AssetsHeader = []string{
	"code", "name", "asset_type", "bucket", "channel", "currency",
	"risk_level", "holding_cost_pct", "expected_yield_pct", "notes", "created_at",
}

func DumpAssets(ctx context.Context, st *store.Store, path string) error {
	rows, err := st.ListAssets(ctx)
	if err != nil {
		return err
	}
	return WriteCSV(path, AssetsHeader, func(w *csv.Writer) error {
		for _, r := range rows {
			if err := w.Write([]string{
				r.Code, r.Name, r.AssetType, r.Bucket, r.Channel, r.Currency,
				NullStr(r.RiskLevel), NullFloat(r.HoldingCostPct), NullFloat(r.ExpectedYieldPct),
				r.Notes, r.CreatedAt,
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

var SnapshotsHeader = []string{
	"asset_code", "asset_name", "snapshot_date", "balance_yuan", "balance_cents",
	"expected_yield_pct", "actual_yield_pct", "notes", "created_at",
}

func DumpSnapshots(ctx context.Context, st *store.Store, path string) error {
	rows, err := st.ListSnapshots(ctx)
	if err != nil {
		return err
	}
	return WriteCSV(path, SnapshotsHeader, func(w *csv.Writer) error {
		for _, r := range rows {
			if err := w.Write([]string{
				r.AssetCode, r.AssetName, r.SnapshotDate,
				Yuan(r.BalanceCents), strconv.FormatInt(r.BalanceCents, 10),
				NullFloat(r.ExpectedYieldPct), NullFloat(r.ActualYieldPct),
				r.Notes, r.CreatedAt,
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

var TransactionsHeader = []string{
	"asset_code", "asset_name", "txn_date", "direction",
	"amount_yuan", "amount_cents", "fee_yuan", "fee_cents",
	"notes", "created_at",
}

func DumpTransactions(ctx context.Context, st *store.Store, path string) error {
	rows, err := st.ListTransactions(ctx)
	if err != nil {
		return err
	}
	return WriteCSV(path, TransactionsHeader, func(w *csv.Writer) error {
		for _, r := range rows {
			if err := w.Write([]string{
				r.AssetCode, r.AssetName, r.TxnDate, r.Direction,
				Yuan(r.AmountCents), strconv.FormatInt(r.AmountCents, 10),
				Yuan(r.FeeCents), strconv.FormatInt(r.FeeCents, 10),
				r.Notes, r.CreatedAt,
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

var HoldingsHeader = []string{
	"asset_code", "asset_name", "asset_type", "bucket", "channel", "currency",
	"risk_level", "as_of", "balance_yuan", "balance_cents", "expected_yield_pct",
}

func DumpHoldings(ctx context.Context, st *store.Store, path string) error {
	rows, err := st.ListHoldings(ctx)
	if err != nil {
		return err
	}
	return WriteCSV(path, HoldingsHeader, func(w *csv.Writer) error {
		for _, r := range rows {
			balYuan := ""
			balCents := ""
			if r.BalanceCents.Valid {
				balYuan = Yuan(r.BalanceCents.Int64)
				balCents = strconv.FormatInt(r.BalanceCents.Int64, 10)
			}
			if err := w.Write([]string{
				r.AssetCode, r.AssetName, r.AssetType, r.Bucket, r.Channel, r.Currency,
				NullStr(r.RiskLevel), NullStr(r.AsOf),
				balYuan, balCents, NullFloat(r.ExpectedYieldPct),
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// WriteCSV writes header + body to a temp file then atomically renames into
// place. Atomic rename means readers either see the previous file or the new
// one, never a half-written file (matters for the publish job's git add).
func WriteCSV(path string, header []string, body func(w *csv.Writer) error) error {
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	w := csv.NewWriter(f)
	if err := w.Write(header); err != nil {
		_ = f.Close()
		return err
	}
	if err := body(w); err != nil {
		_ = f.Close()
		return err
	}
	w.Flush()
	if err := w.Error(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Yuan formats cents as a 2-decimal yuan string. Negative values get a single
// leading minus. We intentionally don't use %.2f because float intermediate
// representation can drift on edge cases (e.g. 1234567 cents printing as
// 12345.669999... in some locales). Integer math is bulletproof.
func Yuan(cents int64) string {
	sign := ""
	if cents < 0 {
		sign = "-"
		cents = -cents
	}
	return fmt.Sprintf("%s%d.%02d", sign, cents/100, cents%100)
}

func NullStr(v sql.NullString) string {
	if !v.Valid {
		return ""
	}
	return v.String
}

func NullFloat(v sql.NullFloat64) string {
	if !v.Valid {
		return ""
	}
	return strconv.FormatFloat(v.Float64, 'f', -1, 64)
}
