-- name: ListTransactions :many
-- All transactions, joined with assets for code/name display columns.
-- Filtered variants (asset_id / direction / since / until) live in
-- store.ListTransactionsFiltered.
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

-- name: GetTransactionByID :one
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
WHERE t.id = ?;

-- name: InsertTransaction :one
-- Append-only. Transactions have no natural unique key (an asset can buy
-- twice the same day at the same price), so duplicate-prevention is the
-- caller's responsibility.
-- Returns the new row id via RETURNING for ergonomic post-insert read-back.
INSERT INTO transactions (
  asset_id, txn_date, direction, amount_cents, fee_cents, notes
) VALUES (?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: DeleteTransaction :execrows
DELETE FROM transactions WHERE id = ?;
