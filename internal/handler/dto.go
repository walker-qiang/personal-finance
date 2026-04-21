package handler

import (
	"database/sql"

	"github.com/walker-qiang/personal-finance/internal/db/store"
)

// All response DTOs use snake_case JSON keys and *T (not sql.NullT) for
// nullable fields so the JSON shape is human-friendly:
//   sql.NullString{Valid: true, String: "R3"} → "risk_level": "R3"
//   sql.NullString{Valid: false}              → "risk_level": null
//
// Money fields are exposed as *both* cents (canonical, lossless) and yuan
// (convenience; computed at render time, not stored). Web/CLI clients can pick
// whichever they prefer; we keep them in sync centrally so individual call
// sites can't drift.

type AssetResp struct {
	ID               int64    `json:"id"`
	Code             string   `json:"code"`
	Name             string   `json:"name"`
	AssetType        string   `json:"asset_type"`
	Bucket           string   `json:"bucket"`
	Channel          string   `json:"channel"`
	Currency         string   `json:"currency"`
	RiskLevel        *string  `json:"risk_level"`
	HoldingCostPct   *float64 `json:"holding_cost_pct"`
	ExpectedYieldPct *float64 `json:"expected_yield_pct"`
	Notes            string   `json:"notes"`
	CreatedAt        string   `json:"created_at"`
}

type SnapshotResp struct {
	ID               int64    `json:"id"`
	AssetID          int64    `json:"asset_id"`
	AssetCode        string   `json:"asset_code"`
	AssetName        string   `json:"asset_name"`
	SnapshotDate     string   `json:"snapshot_date"`
	BalanceCents     int64    `json:"balance_cents"`
	BalanceYuan      float64  `json:"balance_yuan"`
	ExpectedYieldPct *float64 `json:"expected_yield_pct"`
	ActualYieldPct   *float64 `json:"actual_yield_pct"`
	Notes            string   `json:"notes"`
	CreatedAt        string   `json:"created_at"`
}

type TransactionResp struct {
	ID          int64   `json:"id"`
	AssetID     int64   `json:"asset_id"`
	AssetCode   string  `json:"asset_code"`
	AssetName   string  `json:"asset_name"`
	TxnDate     string  `json:"txn_date"`
	Direction   string  `json:"direction"`
	AmountCents int64   `json:"amount_cents"`
	AmountYuan  float64 `json:"amount_yuan"`
	FeeCents    int64   `json:"fee_cents"`
	FeeYuan     float64 `json:"fee_yuan"`
	Notes       string  `json:"notes"`
	CreatedAt   string  `json:"created_at"`
}

type HoldingResp struct {
	AssetID          int64    `json:"asset_id"`
	AssetCode        string   `json:"asset_code"`
	AssetName        string   `json:"asset_name"`
	AssetType        string   `json:"asset_type"`
	Bucket           string   `json:"bucket"`
	Channel          string   `json:"channel"`
	Currency         string   `json:"currency"`
	RiskLevel        *string  `json:"risk_level"`
	AsOf             *string  `json:"as_of"`
	BalanceCents     *int64   `json:"balance_cents"`
	BalanceYuan      *float64 `json:"balance_yuan"`
	ExpectedYieldPct *float64 `json:"expected_yield_pct"`
}

func nullStrPtr(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	v := n.String
	return &v
}

func nullFloatPtr(n sql.NullFloat64) *float64 {
	if !n.Valid {
		return nil
	}
	v := n.Float64
	return &v
}

func nullIntPtr(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

func centsToYuan(c int64) float64 { return float64(c) / 100.0 }

func centsToYuanPtr(n sql.NullInt64) *float64 {
	if !n.Valid {
		return nil
	}
	v := centsToYuan(n.Int64)
	return &v
}

func toAssetResp(a store.Asset) AssetResp {
	return AssetResp{
		ID:               a.ID,
		Code:             a.Code,
		Name:             a.Name,
		AssetType:        a.AssetType,
		Bucket:           a.Bucket,
		Channel:          a.Channel,
		Currency:         a.Currency,
		RiskLevel:        nullStrPtr(a.RiskLevel),
		HoldingCostPct:   nullFloatPtr(a.HoldingCostPct),
		ExpectedYieldPct: nullFloatPtr(a.ExpectedYieldPct),
		Notes:            a.Notes,
		CreatedAt:        a.CreatedAt,
	}
}

func toAssetsResp(in []store.Asset) []AssetResp {
	out := make([]AssetResp, len(in))
	for i, a := range in {
		out[i] = toAssetResp(a)
	}
	return out
}

func toSnapshotResp(s store.Snapshot) SnapshotResp {
	return SnapshotResp{
		ID:               s.ID,
		AssetID:          s.AssetID,
		AssetCode:        s.AssetCode,
		AssetName:        s.AssetName,
		SnapshotDate:     s.SnapshotDate,
		BalanceCents:     s.BalanceCents,
		BalanceYuan:      centsToYuan(s.BalanceCents),
		ExpectedYieldPct: nullFloatPtr(s.ExpectedYieldPct),
		ActualYieldPct:   nullFloatPtr(s.ActualYieldPct),
		Notes:            s.Notes,
		CreatedAt:        s.CreatedAt,
	}
}

func toSnapshotsResp(in []store.Snapshot) []SnapshotResp {
	out := make([]SnapshotResp, len(in))
	for i, s := range in {
		out[i] = toSnapshotResp(s)
	}
	return out
}

func toTransactionResp(t store.Transaction) TransactionResp {
	return TransactionResp{
		ID:          t.ID,
		AssetID:     t.AssetID,
		AssetCode:   t.AssetCode,
		AssetName:   t.AssetName,
		TxnDate:     t.TxnDate,
		Direction:   t.Direction,
		AmountCents: t.AmountCents,
		AmountYuan:  centsToYuan(t.AmountCents),
		FeeCents:    t.FeeCents,
		FeeYuan:     centsToYuan(t.FeeCents),
		Notes:       t.Notes,
		CreatedAt:   t.CreatedAt,
	}
}

func toTransactionsResp(in []store.Transaction) []TransactionResp {
	out := make([]TransactionResp, len(in))
	for i, t := range in {
		out[i] = toTransactionResp(t)
	}
	return out
}

func toHoldingResp(h store.Holding) HoldingResp {
	return HoldingResp{
		AssetID:          h.AssetID,
		AssetCode:        h.AssetCode,
		AssetName:        h.AssetName,
		AssetType:        h.AssetType,
		Bucket:           h.Bucket,
		Channel:          h.Channel,
		Currency:         h.Currency,
		RiskLevel:        nullStrPtr(h.RiskLevel),
		AsOf:             nullStrPtr(h.AsOf),
		BalanceCents:     nullIntPtr(h.BalanceCents),
		BalanceYuan:      centsToYuanPtr(h.BalanceCents),
		ExpectedYieldPct: nullFloatPtr(h.ExpectedYieldPct),
	}
}

func toHoldingsResp(in []store.Holding) []HoldingResp {
	out := make([]HoldingResp, len(in))
	for i, h := range in {
		out[i] = toHoldingResp(h)
	}
	return out
}

// BucketTargetResp is the API shape for the per-bucket target allocation.
// All 4 fields are always present (no NULLs in the schema). The list endpoint
// always returns 3 entries (cash / stable / growth); rows that haven't been
// set yet come back with `target_pct: null` and `is_set: false` so the UI
// doesn't have to second-guess "absent vs zero".
type BucketTargetResp struct {
	Bucket    string   `json:"bucket"`
	TargetPct *float64 `json:"target_pct"`
	Notes     string   `json:"notes"`
	UpdatedAt *string  `json:"updated_at"`
	IsSet     bool     `json:"is_set"`
}

func toBucketTargetResp(b store.BucketTarget) BucketTargetResp {
	pct := b.TargetPct
	updated := b.UpdatedAt
	return BucketTargetResp{
		Bucket:    b.Bucket,
		TargetPct: &pct,
		Notes:     b.Notes,
		UpdatedAt: &updated,
		IsSet:     true,
	}
}
