-- name: ListSnapshots :many
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

-- name: UpsertSnapshot :exec
INSERT INTO snapshots (
  asset_id, snapshot_date, balance_cents, expected_yield_pct, actual_yield_pct, notes
) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (asset_id, snapshot_date) DO UPDATE SET
  balance_cents      = excluded.balance_cents,
  expected_yield_pct = excluded.expected_yield_pct,
  actual_yield_pct   = excluded.actual_yield_pct,
  notes              = excluded.notes;
