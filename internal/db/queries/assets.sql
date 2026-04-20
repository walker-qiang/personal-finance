-- name: ListAssets :many
-- All non-archived assets, ordered for stable CSV publish.
-- Note: filtered variants (bucket / asset_type / include_archived) live in
-- internal/db/store/store.go (ListAssetsFiltered) because sqlc cannot generate
-- the optional WHERE conditions.
SELECT * FROM assets
WHERE archived_at IS NULL
ORDER BY bucket, asset_type, code;

-- name: GetAssetByCode :one
SELECT * FROM assets
WHERE code = ?;

-- name: GetAssetIDByCode :one
SELECT id FROM assets WHERE code = ?;

-- name: GetAssetByID :one
-- Excludes archived rows on purpose: the HTTP layer treats an archived asset
-- as "not found" for read paths so the UI doesn't have to filter.
SELECT * FROM assets
WHERE id = ? AND archived_at IS NULL;

-- name: UpsertAssetByCode :one
-- M1.5 contract:
--   - Idempotent on `code` (natural key)
--   - Re-creating an already-archived asset clears archived_at ("revive")
--   - Returns the row id (LastInsertId is unreliable for ON CONFLICT paths)
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
RETURNING id;

-- name: ArchiveAsset :execrows
-- Soft-delete. Returns RowsAffected so the caller can distinguish:
--   rows=0  +  asset exists      -> already archived (idempotent no-op)
--   rows=0  +  asset missing     -> 404
--   rows=1                       -> archived just now
-- The "exists?" check stays in store.go because sqlc :execrows can't combine
-- two statements.
UPDATE assets
SET archived_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')
WHERE id = ? AND archived_at IS NULL;

-- name: AssetExistsByID :one
-- Used by ArchiveAsset's "already-archived vs not-found" disambiguation.
SELECT COUNT(*) FROM assets WHERE id = ?;
