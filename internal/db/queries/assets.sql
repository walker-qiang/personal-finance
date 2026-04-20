-- name: ListAssets :many
SELECT * FROM assets
WHERE archived_at IS NULL
ORDER BY bucket, asset_type, code;

-- name: GetAssetByCode :one
SELECT * FROM assets
WHERE code = ?;

-- name: UpsertAssetByCode :exec
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
  notes              = excluded.notes;

-- name: GetAssetIDByCode :one
SELECT id FROM assets WHERE code = ?;
