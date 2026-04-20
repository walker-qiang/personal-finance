// Package store wraps the small set of SQL queries needed by M1.
//
// Why hand-rolled and not sqlc-generated yet:
//   M1 has 4 read queries + 2 upserts. sqlc.yaml + queries/*.sql are committed
//   so the toolchain is ready, but generating + checking in code for so few
//   queries is more ceremony than benefit. M2 (when transaction APIs land
//   and the query surface widens) will switch app code over to
//   `internal/db/sqlc/` and delete this package.
package store

import (
	"context"
	"database/sql"
)

type Store struct {
	db *sql.DB
}

func New(db *sql.DB) *Store { return &Store{db: db} }

// --- Asset ---

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

func (s *Store) UpsertAsset(ctx context.Context, a Asset) error {
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
  notes              = excluded.notes`
	_, err := s.db.ExecContext(ctx, q,
		a.Code, a.Name, a.AssetType, a.Bucket, a.Channel, a.Currency,
		a.RiskLevel, a.HoldingCostPct, a.ExpectedYieldPct, a.Notes,
	)
	return err
}

func (s *Store) ListAssets(ctx context.Context) ([]Asset, error) {
	const q = `
SELECT id, code, name, asset_type, bucket, channel, currency,
       risk_level, holding_cost_pct, expected_yield_pct, notes, created_at
FROM assets
WHERE archived_at IS NULL
ORDER BY bucket, asset_type, code`
	rows, err := s.db.QueryContext(ctx, q)
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

func (s *Store) UpsertSnapshot(ctx context.Context, sn Snapshot) error {
	const q = `
INSERT INTO snapshots (asset_id, snapshot_date, balance_cents, expected_yield_pct, actual_yield_pct, notes)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (asset_id, snapshot_date) DO UPDATE SET
  balance_cents      = excluded.balance_cents,
  expected_yield_pct = excluded.expected_yield_pct,
  actual_yield_pct   = excluded.actual_yield_pct,
  notes              = excluded.notes`
	_, err := s.db.ExecContext(ctx, q,
		sn.AssetID, sn.SnapshotDate, sn.BalanceCents, sn.ExpectedYieldPct, sn.ActualYieldPct, sn.Notes,
	)
	return err
}

func (s *Store) ListSnapshots(ctx context.Context) ([]Snapshot, error) {
	const q = `
SELECT s.id, s.asset_id, a.code, a.name, s.snapshot_date,
       s.balance_cents, s.expected_yield_pct, s.actual_yield_pct, s.notes, s.created_at
FROM snapshots s
JOIN assets a ON a.id = s.asset_id
ORDER BY s.snapshot_date DESC, a.code`
	rows, err := s.db.QueryContext(ctx, q)
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

func (s *Store) ListTransactions(ctx context.Context) ([]Transaction, error) {
	const q = `
SELECT t.id, t.asset_id, a.code, a.name, t.txn_date,
       t.direction, t.amount_cents, t.fee_cents, t.notes, t.created_at
FROM transactions t
JOIN assets a ON a.id = t.asset_id
ORDER BY t.txn_date DESC, t.id DESC`
	rows, err := s.db.QueryContext(ctx, q)
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
