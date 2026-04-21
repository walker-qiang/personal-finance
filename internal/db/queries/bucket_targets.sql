-- name: ListBucketTargets :many
-- All target rows; we always return all 3 buckets even if some have no row
-- (handler layer fills missing buckets with target_pct=null in the response).
SELECT * FROM bucket_targets
ORDER BY
  CASE bucket
    WHEN 'cash'   THEN 1
    WHEN 'stable' THEN 2
    WHEN 'growth' THEN 3
  END;

-- name: GetBucketTarget :one
SELECT * FROM bucket_targets WHERE bucket = ?;

-- name: UpsertBucketTarget :one
-- Idempotent on `bucket`. Always bumps updated_at on overwrite so the audit
-- trail / CSV export shows the last edit time.
INSERT INTO bucket_targets (bucket, target_pct, notes)
VALUES (?, ?, ?)
ON CONFLICT (bucket) DO UPDATE SET
  target_pct = excluded.target_pct,
  notes      = excluded.notes,
  updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')
RETURNING bucket, target_pct, notes, updated_at;

-- name: DeleteBucketTarget :execrows
DELETE FROM bucket_targets WHERE bucket = ?;
