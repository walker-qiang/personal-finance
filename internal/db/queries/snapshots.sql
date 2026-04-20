-- name: ListSnapshots :many
-- All snapshots, joined with assets for code/name display columns. Used by
-- both the GET API and the publish job's snapshots-*.csv dump.
-- Filtered variants (asset_id / since / until) live in store.ListSnapshotsFiltered.
SELECT
  s.id,
  s.asset_id,
  a.code              AS asset_code,
  a.name              AS asset_name,
  s.snapshot_date,
  s.balance_cents,
  s.expected_yield_pct,
  s.actual_yield_pct,
  s.notes,
  s.created_at
FROM snapshots s
JOIN assets a ON a.id = s.asset_id
ORDER BY s.snapshot_date DESC, a.code;

-- name: GetSnapshotByID :one
SELECT
  s.id,
  s.asset_id,
  a.code              AS asset_code,
  a.name              AS asset_name,
  s.snapshot_date,
  s.balance_cents,
  s.expected_yield_pct,
  s.actual_yield_pct,
  s.notes,
  s.created_at
FROM snapshots s
JOIN assets a ON a.id = s.asset_id
WHERE s.id = ?;

-- name: UpsertSnapshot :one
-- Idempotent on (asset_id, snapshot_date). Returns row id so the caller can
-- 200 + read-back without a separate SELECT.
INSERT INTO snapshots (
  asset_id, snapshot_date, balance_cents, expected_yield_pct, actual_yield_pct, notes
) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (asset_id, snapshot_date) DO UPDATE SET
  balance_cents      = excluded.balance_cents,
  expected_yield_pct = excluded.expected_yield_pct,
  actual_yield_pct   = excluded.actual_yield_pct,
  notes              = excluded.notes
RETURNING id;

-- name: DeleteSnapshot :execrows
-- Hard delete (no soft-delete on snapshots, they're cheap to recreate).
DELETE FROM snapshots WHERE id = ?;
