-- name: ListTransactions :many
SELECT
  t.id,
  t.asset_id,
  a.code            AS asset_code,
  a.name            AS asset_name,
  t.txn_date,
  t.direction,
  t.amount_cents,
  t.fee_cents,
  t.notes,
  t.created_at
FROM transactions t
JOIN assets a ON a.id = t.asset_id
ORDER BY t.txn_date DESC, t.id DESC;
