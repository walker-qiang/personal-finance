// Package store wraps the SQL queries needed by the finance backend.
//
// Why hand-rolled and not sqlc-generated yet:
//   M1.5 has ~15 queries (reads + upserts + partial updates + soft/hard
//   deletes + filtered lists). sqlc.yaml + queries/*.sql are committed so the
//   toolchain is ready, but PATCH-style partial updates need dynamic SQL that
//   sqlc doesn't generate ergonomically, and the query surface still fits in
//   one file. The plan is: when M2+ web work demands transactional flows
//   spanning multiple statements (e.g. "insert txn AND update snapshot
//   atomically"), we'll switch to sqlc + manual tx helpers.
package store

import (
	"context"
	"database/sql"
	"strings"
)

type Store struct {
	db *sql.DB
}

func New(db *sql.DB) *Store { return &Store{db: db} }

// --- Asset ---

// Asset mirrors the assets table. Response shape for HTTP is in
// handler/dto.go (which converts sql.Null* to *T for nicer JSON).
type Asset struct {
	ID               int64
	Code             string
	Name             string
	AssetType        string
	Bucket           string
	Channel          string
	Currency         string
	RiskLevel        sql.NullString
	HoldingCostPct   sql.NullFloat64
	ExpectedYieldPct sql.NullFloat64
	Notes            string
	CreatedAt        string
}

// UpsertAsset inserts or overwrites the asset identified by code. Returns
// the row id (existing on update, new on insert).
//
// Note: this also revives soft-deleted (archived) rows by clearing
// archived_at. Re-creating an asset with the same code therefore "undeletes"
// it, which is what we want for personal use (a typo'd ARCHIVE → POST again).
func (s *Store) UpsertAsset(ctx context.Context, a Asset) (int64, error) {
	const q = `
INSERT INTO assets (
  code, name, asset_type, bucket, channel, currency,
  risk_level, holding_cost_pct, expected_yield_pct, notes
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (code) DO UPDATE SET
  name               = excluded.name,
  asset_type         = excluded.asset_type,
  bucket             = excluded.bucket,
  channel            = excluded.channel,
  currency           = excluded.currency,
  risk_level         = excluded.risk_level,
  holding_cost_pct   = excluded.holding_cost_pct,
  expected_yield_pct = excluded.expected_yield_pct,
  notes              = excluded.notes,
  archived_at        = NULL
RETURNING id`
	var id int64
	err := s.db.QueryRowContext(ctx, q,
		a.Code, a.Name, a.AssetType, a.Bucket, a.Channel, a.Currency,
		a.RiskLevel, a.HoldingCostPct, a.ExpectedYieldPct, a.Notes,
	).Scan(&id)
	return id, err
}

// AssetFilter narrows ListAssetsFiltered. Zero value = "all non-archived".
type AssetFilter struct {
	Bucket          string // "", "cash", "stable", "growth"
	AssetType       string // "" or one of the 8 enums
	IncludeArchived bool
}

func (s *Store) ListAssets(ctx context.Context) ([]Asset, error) {
	return s.ListAssetsFiltered(ctx, AssetFilter{})
}

func (s *Store) ListAssetsFiltered(ctx context.Context, f AssetFilter) ([]Asset, error) {
	where := []string{}
	args := []interface{}{}
	if !f.IncludeArchived {
		where = append(where, "archived_at IS NULL")
	}
	if f.Bucket != "" {
		where = append(where, "bucket = ?")
		args = append(args, f.Bucket)
	}
	if f.AssetType != "" {
		where = append(where, "asset_type = ?")
		args = append(args, f.AssetType)
	}
	q := `SELECT id, code, name, asset_type, bucket, channel, currency,
       risk_level, holding_cost_pct, expected_yield_pct, notes, created_at
FROM assets`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY bucket, asset_type, code"
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Asset
	for rows.Next() {
		var a Asset
		if err := rows.Scan(
			&a.ID, &a.Code, &a.Name, &a.AssetType, &a.Bucket, &a.Channel, &a.Currency,
			&a.RiskLevel, &a.HoldingCostPct, &a.ExpectedYieldPct, &a.Notes, &a.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) GetAssetIDByCode(ctx context.Context, code string) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM assets WHERE code = ?`, code).Scan(&id)
	return id, err
}

func (s *Store) GetAssetByID(ctx context.Context, id int64) (Asset, error) {
	const q = `
SELECT id, code, name, asset_type, bucket, channel, currency,
       risk_level, holding_cost_pct, expected_yield_pct, notes, created_at
FROM assets WHERE id = ? AND archived_at IS NULL`
	var a Asset
	err := s.db.QueryRowContext(ctx, q, id).Scan(
		&a.ID, &a.Code, &a.Name, &a.AssetType, &a.Bucket, &a.Channel, &a.Currency,
		&a.RiskLevel, &a.HoldingCostPct, &a.ExpectedYieldPct, &a.Notes, &a.CreatedAt,
	)
	return a, err
}

// AssetPatch holds partial-update fields. nil = leave alone.
type AssetPatch struct {
	Name             *string
	AssetType        *string
	Bucket           *string
	Channel          *string
	Currency         *string
	RiskLevel        *string // pass empty string to clear
	HoldingCostPct   *float64
	ClearHoldingCost bool // pass true to set NULL
	ExpectedYieldPct *float64
	ClearExpectedY   bool
	Notes            *string
}

// PatchAsset updates only the provided fields. Returns sql.ErrNoRows if id
// does not exist or is archived.
func (s *Store) PatchAsset(ctx context.Context, id int64, p AssetPatch) error {
	sets := []string{}
	args := []interface{}{}
	if p.Name != nil {
		sets = append(sets, "name = ?")
		args = append(args, *p.Name)
	}
	if p.AssetType != nil {
		sets = append(sets, "asset_type = ?")
		args = append(args, *p.AssetType)
	}
	if p.Bucket != nil {
		sets = append(sets, "bucket = ?")
		args = append(args, *p.Bucket)
	}
	if p.Channel != nil {
		sets = append(sets, "channel = ?")
		args = append(args, *p.Channel)
	}
	if p.Currency != nil {
		sets = append(sets, "currency = ?")
		args = append(args, *p.Currency)
	}
	if p.RiskLevel != nil {
		if *p.RiskLevel == "" {
			sets = append(sets, "risk_level = NULL")
		} else {
			sets = append(sets, "risk_level = ?")
			args = append(args, *p.RiskLevel)
		}
	}
	if p.ClearHoldingCost {
		sets = append(sets, "holding_cost_pct = NULL")
	} else if p.HoldingCostPct != nil {
		sets = append(sets, "holding_cost_pct = ?")
		args = append(args, *p.HoldingCostPct)
	}
	if p.ClearExpectedY {
		sets = append(sets, "expected_yield_pct = NULL")
	} else if p.ExpectedYieldPct != nil {
		sets = append(sets, "expected_yield_pct = ?")
		args = append(args, *p.ExpectedYieldPct)
	}
	if p.Notes != nil {
		sets = append(sets, "notes = ?")
		args = append(args, *p.Notes)
	}
	if len(sets) == 0 {
		// no-op patch is allowed; verify the row exists so the handler can
		// return 404 vs 200 correctly.
		var n int
		if err := s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM assets WHERE id = ? AND archived_at IS NULL`, id).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return sql.ErrNoRows
		}
		return nil
	}
	args = append(args, id)
	q := "UPDATE assets SET " + strings.Join(sets, ", ") + " WHERE id = ? AND archived_at IS NULL"
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// ArchiveAsset soft-deletes (sets archived_at = now). Idempotent: archiving
// an already-archived row is a no-op (returns nil, not ErrNoRows).
func (s *Store) ArchiveAsset(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE assets SET archived_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id = ? AND archived_at IS NULL`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// distinguish "not found" from "already archived"
		var exists int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM assets WHERE id = ?`, id).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return sql.ErrNoRows
		}
	}
	return nil
}

// --- Snapshot ---

type Snapshot struct {
	ID               int64
	AssetID          int64
	AssetCode        string
	AssetName        string
	SnapshotDate     string
	BalanceCents     int64
	ExpectedYieldPct sql.NullFloat64
	ActualYieldPct   sql.NullFloat64
	Notes            string
	CreatedAt        string
}

// UpsertSnapshot inserts a snapshot or overwrites the existing row for
// (asset_id, snapshot_date). Returns the row id. POST is idempotent on the
// natural key, so the HTTP layer always returns 200 OK regardless of whether
// the row was inserted or updated.
func (s *Store) UpsertSnapshot(ctx context.Context, sn Snapshot) (int64, error) {
	const q = `
INSERT INTO snapshots (asset_id, snapshot_date, balance_cents, expected_yield_pct, actual_yield_pct, notes)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (asset_id, snapshot_date) DO UPDATE SET
  balance_cents      = excluded.balance_cents,
  expected_yield_pct = excluded.expected_yield_pct,
  actual_yield_pct   = excluded.actual_yield_pct,
  notes              = excluded.notes
RETURNING id`
	var id int64
	err := s.db.QueryRowContext(ctx, q,
		sn.AssetID, sn.SnapshotDate, sn.BalanceCents, sn.ExpectedYieldPct, sn.ActualYieldPct, sn.Notes,
	).Scan(&id)
	return id, err
}

// SnapshotFilter narrows ListSnapshotsFiltered.
type SnapshotFilter struct {
	AssetID int64  // 0 = any
	Since   string // "YYYY-MM-DD" inclusive; "" = unbounded
	Until   string // "YYYY-MM-DD" inclusive; "" = unbounded
}

func (s *Store) ListSnapshots(ctx context.Context) ([]Snapshot, error) {
	return s.ListSnapshotsFiltered(ctx, SnapshotFilter{})
}

func (s *Store) ListSnapshotsFiltered(ctx context.Context, f SnapshotFilter) ([]Snapshot, error) {
	where := []string{}
	args := []interface{}{}
	if f.AssetID != 0 {
		where = append(where, "s.asset_id = ?")
		args = append(args, f.AssetID)
	}
	if f.Since != "" {
		where = append(where, "s.snapshot_date >= ?")
		args = append(args, f.Since)
	}
	if f.Until != "" {
		where = append(where, "s.snapshot_date <= ?")
		args = append(args, f.Until)
	}
	q := `SELECT s.id, s.asset_id, a.code, a.name, s.snapshot_date,
       s.balance_cents, s.expected_yield_pct, s.actual_yield_pct, s.notes, s.created_at
FROM snapshots s
JOIN assets a ON a.id = s.asset_id`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY s.snapshot_date DESC, a.code"
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Snapshot
	for rows.Next() {
		var sn Snapshot
		if err := rows.Scan(
			&sn.ID, &sn.AssetID, &sn.AssetCode, &sn.AssetName, &sn.SnapshotDate,
			&sn.BalanceCents, &sn.ExpectedYieldPct, &sn.ActualYieldPct, &sn.Notes, &sn.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, sn)
	}
	return out, rows.Err()
}

func (s *Store) GetSnapshotByID(ctx context.Context, id int64) (Snapshot, error) {
	const q = `
SELECT s.id, s.asset_id, a.code, a.name, s.snapshot_date,
       s.balance_cents, s.expected_yield_pct, s.actual_yield_pct, s.notes, s.created_at
FROM snapshots s
JOIN assets a ON a.id = s.asset_id
WHERE s.id = ?`
	var sn Snapshot
	err := s.db.QueryRowContext(ctx, q, id).Scan(
		&sn.ID, &sn.AssetID, &sn.AssetCode, &sn.AssetName, &sn.SnapshotDate,
		&sn.BalanceCents, &sn.ExpectedYieldPct, &sn.ActualYieldPct, &sn.Notes, &sn.CreatedAt,
	)
	return sn, err
}

// SnapshotPatch holds partial-update fields. snapshot_date and asset_id are
// intentionally excluded — the (asset_id, snapshot_date) pair is the natural
// key, so changing it is "delete + new POST", not a patch.
type SnapshotPatch struct {
	BalanceCents     *int64
	ExpectedYieldPct *float64
	ClearExpectedY   bool
	ActualYieldPct   *float64
	ClearActualY     bool
	Notes            *string
}

func (s *Store) PatchSnapshot(ctx context.Context, id int64, p SnapshotPatch) error {
	sets := []string{}
	args := []interface{}{}
	if p.BalanceCents != nil {
		sets = append(sets, "balance_cents = ?")
		args = append(args, *p.BalanceCents)
	}
	if p.ClearExpectedY {
		sets = append(sets, "expected_yield_pct = NULL")
	} else if p.ExpectedYieldPct != nil {
		sets = append(sets, "expected_yield_pct = ?")
		args = append(args, *p.ExpectedYieldPct)
	}
	if p.ClearActualY {
		sets = append(sets, "actual_yield_pct = NULL")
	} else if p.ActualYieldPct != nil {
		sets = append(sets, "actual_yield_pct = ?")
		args = append(args, *p.ActualYieldPct)
	}
	if p.Notes != nil {
		sets = append(sets, "notes = ?")
		args = append(args, *p.Notes)
	}
	if len(sets) == 0 {
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM snapshots WHERE id = ?`, id).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return sql.ErrNoRows
		}
		return nil
	}
	args = append(args, id)
	q := "UPDATE snapshots SET " + strings.Join(sets, ", ") + " WHERE id = ?"
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) DeleteSnapshot(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM snapshots WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// --- Transaction ---

type Transaction struct {
	ID          int64
	AssetID     int64
	AssetCode   string
	AssetName   string
	TxnDate     string
	Direction   string
	AmountCents int64
	FeeCents    int64
	Notes       string
	CreatedAt   string
}

// TransactionFilter narrows ListTransactionsFiltered.
type TransactionFilter struct {
	AssetID   int64
	Direction string // "" or one of buy/sell/dividend/fee/transfer-in/transfer-out/adjust
	Since     string
	Until     string
}

func (s *Store) ListTransactions(ctx context.Context) ([]Transaction, error) {
	return s.ListTransactionsFiltered(ctx, TransactionFilter{})
}

func (s *Store) ListTransactionsFiltered(ctx context.Context, f TransactionFilter) ([]Transaction, error) {
	where := []string{}
	args := []interface{}{}
	if f.AssetID != 0 {
		where = append(where, "t.asset_id = ?")
		args = append(args, f.AssetID)
	}
	if f.Direction != "" {
		where = append(where, "t.direction = ?")
		args = append(args, f.Direction)
	}
	if f.Since != "" {
		where = append(where, "t.txn_date >= ?")
		args = append(args, f.Since)
	}
	if f.Until != "" {
		where = append(where, "t.txn_date <= ?")
		args = append(args, f.Until)
	}
	q := `SELECT t.id, t.asset_id, a.code, a.name, t.txn_date,
       t.direction, t.amount_cents, t.fee_cents, t.notes, t.created_at
FROM transactions t
JOIN assets a ON a.id = t.asset_id`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY t.txn_date DESC, t.id DESC"
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Transaction
	for rows.Next() {
		var t Transaction
		if err := rows.Scan(
			&t.ID, &t.AssetID, &t.AssetCode, &t.AssetName, &t.TxnDate,
			&t.Direction, &t.AmountCents, &t.FeeCents, &t.Notes, &t.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) GetTransactionByID(ctx context.Context, id int64) (Transaction, error) {
	const q = `
SELECT t.id, t.asset_id, a.code, a.name, t.txn_date,
       t.direction, t.amount_cents, t.fee_cents, t.notes, t.created_at
FROM transactions t
JOIN assets a ON a.id = t.asset_id
WHERE t.id = ?`
	var t Transaction
	err := s.db.QueryRowContext(ctx, q, id).Scan(
		&t.ID, &t.AssetID, &t.AssetCode, &t.AssetName, &t.TxnDate,
		&t.Direction, &t.AmountCents, &t.FeeCents, &t.Notes, &t.CreatedAt,
	)
	return t, err
}

// InsertTransaction creates a new transaction row. Transactions have no
// natural unique key (an asset can buy twice the same day with the same
// amount), so duplicate-prevention is the caller's responsibility.
func (s *Store) InsertTransaction(ctx context.Context, t Transaction) (int64, error) {
	const q = `
INSERT INTO transactions (asset_id, txn_date, direction, amount_cents, fee_cents, notes)
VALUES (?, ?, ?, ?, ?, ?)`
	res, err := s.db.ExecContext(ctx, q, t.AssetID, t.TxnDate, t.Direction, t.AmountCents, t.FeeCents, t.Notes)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// TransactionPatch holds partial-update fields. asset_id is intentionally
// excluded — moving a txn between assets corrupts holdings history; do
// "delete + reinsert" instead.
type TransactionPatch struct {
	TxnDate     *string
	Direction   *string
	AmountCents *int64
	FeeCents    *int64
	Notes       *string
}

func (s *Store) PatchTransaction(ctx context.Context, id int64, p TransactionPatch) error {
	sets := []string{}
	args := []interface{}{}
	if p.TxnDate != nil {
		sets = append(sets, "txn_date = ?")
		args = append(args, *p.TxnDate)
	}
	if p.Direction != nil {
		sets = append(sets, "direction = ?")
		args = append(args, *p.Direction)
	}
	if p.AmountCents != nil {
		sets = append(sets, "amount_cents = ?")
		args = append(args, *p.AmountCents)
	}
	if p.FeeCents != nil {
		sets = append(sets, "fee_cents = ?")
		args = append(args, *p.FeeCents)
	}
	if p.Notes != nil {
		sets = append(sets, "notes = ?")
		args = append(args, *p.Notes)
	}
	if len(sets) == 0 {
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM transactions WHERE id = ?`, id).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return sql.ErrNoRows
		}
		return nil
	}
	args = append(args, id)
	q := "UPDATE transactions SET " + strings.Join(sets, ", ") + " WHERE id = ?"
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) DeleteTransaction(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM transactions WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// --- Holding (view) ---

type Holding struct {
	AssetID          int64
	AssetCode        string
	AssetName        string
	AssetType        string
	Bucket           string
	Channel          string
	Currency         string
	RiskLevel        sql.NullString
	AsOf             sql.NullString
	BalanceCents     sql.NullInt64
	ExpectedYieldPct sql.NullFloat64
}

func (s *Store) ListHoldings(ctx context.Context) ([]Holding, error) {
	const q = `
SELECT asset_id, asset_code, asset_name, asset_type, bucket, channel, currency,
       risk_level, as_of, balance_cents, expected_yield_pct
FROM holdings
ORDER BY bucket, asset_type, asset_code`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Holding
	for rows.Next() {
		var h Holding
		if err := rows.Scan(
			&h.AssetID, &h.AssetCode, &h.AssetName, &h.AssetType, &h.Bucket, &h.Channel, &h.Currency,
			&h.RiskLevel, &h.AsOf, &h.BalanceCents, &h.ExpectedYieldPct,
		); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
