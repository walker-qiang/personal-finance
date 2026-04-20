-- name: ListHoldings :many
SELECT
  asset_id,
  asset_code,
  asset_name,
  asset_type,
  bucket,
  channel,
  currency,
  risk_level,
  as_of,
  balance_cents,
  expected_yield_pct
FROM holdings
ORDER BY bucket, asset_type, asset_code;
