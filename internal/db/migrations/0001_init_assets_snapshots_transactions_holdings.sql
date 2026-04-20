-- +goose Up
-- +goose StatementBegin

-- 0001 init: assets / snapshots / transactions + holdings VIEW
--
-- Design notes (long-form rationale lives in obsidian-wiki/finance/migrations/0001_*.md):
--   * Money stored as INTEGER cents (avoid float drift).
--   * `code` is a stable kebab-case slug, used as the natural key in CSV exports.
--   * 7+1 asset_type taxonomy: see README §6.
--   * `bucket` is orthogonal to asset_type (cash/stable/growth = 活钱/稳钱/长钱).
--   * SQLite CHECK constraints used for taxonomy enforcement; relax via 0002 if it ever blocks.

CREATE TABLE assets (
  id                  INTEGER PRIMARY KEY AUTOINCREMENT,
  code                TEXT    NOT NULL UNIQUE,
  name                TEXT    NOT NULL,
  asset_type          TEXT    NOT NULL CHECK (asset_type IN (
    'cash-cny',
    'wealth-mgmt-product',
    'etf-fund',
    'cn-stock',
    'hk-stock',
    'us-stock',
    'social-insurance',
    'real-estate'
  )),
  bucket              TEXT    NOT NULL CHECK (bucket IN ('cash', 'stable', 'growth')),
  channel             TEXT    NOT NULL DEFAULT '',
  currency            TEXT    NOT NULL DEFAULT 'CNY' CHECK (length(currency) = 3),
  risk_level          TEXT             CHECK (risk_level IS NULL OR risk_level IN ('R1','R2','R3','R4','R5')),
  holding_cost_pct    REAL,
  expected_yield_pct  REAL,
  notes               TEXT    NOT NULL DEFAULT '',
  created_at          TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  archived_at         TEXT
);

CREATE INDEX idx_assets_bucket   ON assets(bucket);
CREATE INDEX idx_assets_type     ON assets(asset_type);

CREATE TABLE snapshots (
  id                  INTEGER PRIMARY KEY AUTOINCREMENT,
  asset_id            INTEGER NOT NULL REFERENCES assets(id) ON DELETE RESTRICT,
  snapshot_date       TEXT    NOT NULL CHECK (snapshot_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
  balance_cents       INTEGER NOT NULL CHECK (balance_cents >= 0),
  expected_yield_pct  REAL,
  actual_yield_pct    REAL,
  notes               TEXT    NOT NULL DEFAULT '',
  created_at          TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  UNIQUE (asset_id, snapshot_date)
);

CREATE INDEX idx_snapshots_date  ON snapshots(snapshot_date);
CREATE INDEX idx_snapshots_asset ON snapshots(asset_id, snapshot_date);

CREATE TABLE transactions (
  id                  INTEGER PRIMARY KEY AUTOINCREMENT,
  asset_id            INTEGER NOT NULL REFERENCES assets(id) ON DELETE RESTRICT,
  txn_date            TEXT    NOT NULL CHECK (txn_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
  direction           TEXT    NOT NULL CHECK (direction IN (
    'buy', 'sell', 'dividend', 'fee', 'transfer-in', 'transfer-out', 'adjust'
  )),
  amount_cents        INTEGER NOT NULL CHECK (amount_cents >= 0),
  fee_cents           INTEGER NOT NULL DEFAULT 0 CHECK (fee_cents >= 0),
  notes               TEXT    NOT NULL DEFAULT '',
  created_at          TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE INDEX idx_txn_asset_date ON transactions(asset_id, txn_date);
CREATE INDEX idx_txn_date       ON transactions(txn_date);

-- holdings: derived view = latest snapshot per asset (with metadata).
-- M1: snapshot-driven only. M2 may layer transactions on top to compute drift.
CREATE VIEW holdings AS
SELECT
  a.id              AS asset_id,
  a.code            AS asset_code,
  a.name            AS asset_name,
  a.asset_type      AS asset_type,
  a.bucket          AS bucket,
  a.channel         AS channel,
  a.currency        AS currency,
  a.risk_level      AS risk_level,
  s.snapshot_date   AS as_of,
  s.balance_cents   AS balance_cents,
  s.expected_yield_pct AS expected_yield_pct
FROM assets a
LEFT JOIN snapshots s
  ON s.asset_id = a.id
 AND s.snapshot_date = (
   SELECT MAX(snapshot_date) FROM snapshots WHERE asset_id = a.id
 )
WHERE a.archived_at IS NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP VIEW  IF EXISTS holdings;
DROP TABLE IF EXISTS transactions;
DROP TABLE IF EXISTS snapshots;
DROP TABLE IF EXISTS assets;
-- +goose StatementEnd
