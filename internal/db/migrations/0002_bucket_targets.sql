-- +goose Up
-- +goose StatementBegin

-- 0002 bucket_targets: per-bucket target allocation %.
--
-- Why a separate table instead of a JSON file:
--   * The web UI needs CRUD on these values (set / clear / read).
--   * They live in the same audit trail as everything else (CSV export →
--     git history shows when target weights were changed).
--   * `bucket` is the natural key (3 rows max: cash / stable / growth).
--
-- Why we DON'T enforce sum(target_pct) ≤ 100 at the DB level:
--   * SQLite has no AFTER-row trigger that can re-check a global invariant
--     without contortions; the UI / handler enforce 0..100 per row, and
--     surface the sum as informational ("⚠ targets sum to 105%").
--   * Single-user scenario; the user knows when they're experimenting with
--     overlapping ranges.
--
-- Empty / missing bucket = "no target set", not "target = 0%". Drift card
-- in SummaryPage skips buckets without a target row.

CREATE TABLE bucket_targets (
  bucket      TEXT    PRIMARY KEY CHECK (bucket IN ('cash', 'stable', 'growth')),
  target_pct  REAL    NOT NULL CHECK (target_pct >= 0 AND target_pct <= 100),
  notes       TEXT    NOT NULL DEFAULT '',
  updated_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS bucket_targets;
-- +goose StatementEnd
