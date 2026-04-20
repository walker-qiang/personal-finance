# personal-finance

> Owner backend of `obsidian-wiki/finance/exports/`. Single-user, localhost-only, snapshot-driven asset tracker.
>
> **Lifetime**: 5–10 years. **Repo posture**: permanent private (decision #33 in `obsidian-wiki/_system/SYSTEM-DESIGN.md`).
>
> Status (2026-04-20): **M1.5** — full mutation API (POST/PATCH/DELETE for assets, snapshots, transactions) on top of the M1 publish framework. Still manual-trigger publish (no auto-dump after every write).

---

## 0. What lives here

| In this repo | In `~/obsidian-wiki/` |
|---|---|
| Backend code (Go / Gin / SQLite / sqlc / goose) | nothing executable |
| `state/finance.db` (gitignored, source of runtime truth) | nothing |
| Schema migrations (`internal/db/migrations/*.sql`) | human-readable mirror at `finance/migrations/*.md` |
| Seed fixtures (`seed/*.json`) | nothing |
| — | `finance/exports/*.csv` published by this backend (SoT for facts layer) |
| — | `finance/reports/*.md` written by humans (promote from `_draft/reports/`) |

**Hard rules** (契约 13 / 15):
- This backend **only** writes `obsidian-wiki/finance/exports/` via the machine publish worktree at `~/obsidian-wiki-publish-worktree/`.
- pre-commit hook `[3]` in the worktree enforces the path whitelist; even a bug in this code can't write elsewhere.
- `state/finance.db` lives only on the host that runs this backend. Disaster recovery = re-import from `obsidian-wiki/finance/exports/*.csv`.

---

## 1. Stack

- Go 1.22+ (tested on 1.25.6)
- Gin (HTTP)
- SQLite via `modernc.org/sqlite` (pure Go, no cgo)
- `sqlc` for typed queries
- `goose` for migrations
- No gRPC, no Docker (decision #34 — keep "splittable" but don't pre-build)

---

## 2. Layout

```text
personal-finance/
├── README.md
├── go.mod / go.sum
├── Makefile
├── sqlc.yaml
├── cmd/
│   ├── server/        # Gin HTTP server (localhost:7001)
│   ├── migrate/       # goose CLI wrapper (up / down / status / version)
│   └── seed/          # load seed/*.json into finance.db (idempotent)
├── internal/
│   ├── config/        # paths + env (FINANCE_DB_PATH, PUBLISH_WORKTREE, etc.)
│   ├── db/
│   │   ├── migrations/    # goose .sql migrations (NNNN_*.sql)
│   │   ├── queries/       # sqlc input .sql
│   │   └── sqlc/          # sqlc-generated Go (regen via `make sqlc`)
│   ├── handler/       # gin handlers
│   ├── service/       # business logic (assets, snapshots, transactions)
│   └── publish/       # publish job: dump tables → CSV → worktree git commit
├── seed/
│   └── snapshot-20260301.json     # real M1 seed from raw/legacy/.../资产配置计划.md
└── state/             # gitignored: finance.db lives here
    └── .gitkeep
```

---

## 3. Quickstart (M1)

Prereqs: Go 1.22+, git, the publish worktree at `~/obsidian-wiki-publish-worktree/` already created (see `obsidian-wiki/_system/standards/publish-workflow.md` §2.1).

```bash
cd ~/personal-finance

# 1. install dev tools (goose + sqlc) into ./bin (no global install)
make tools

# 2. apply migrations (creates state/finance.db)
make migrate

# 3. (optional) regenerate sqlc code after editing internal/db/queries/*.sql
make sqlc

# 4. load real M1 seed (idempotent: re-running just upserts)
make seed

# 5. run server
make run     # listens on localhost:7001

# 6. trigger publish job (in another terminal)
curl -X POST http://localhost:7001/api/finance/publish | jq
# → commits to ~/obsidian-wiki-publish-worktree/ on `publish-main`
# → does NOT push (set PUBLISH_PUSH=1 to enable push to origin/main)
```

---

## 4. Endpoints (M1.5)

All paths are under `http://localhost:7001`. JSON in / JSON out.

### Reads

| Method | Path | Query params | Purpose |
|---|---|---|---|
| `GET` | `/healthz` | — | liveness |
| `GET` | `/api/finance/assets` | `bucket`, `asset_type`, `include_archived=1` | list assets (default = non-archived only) |
| `GET` | `/api/finance/assets/:id` | — | one asset |
| `GET` | `/api/finance/snapshots` | `asset_id`, `since=YYYY-MM-DD`, `until=YYYY-MM-DD` | list snapshots |
| `GET` | `/api/finance/snapshots/:id` | — | one snapshot |
| `GET` | `/api/finance/transactions` | `asset_id`, `direction`, `since`, `until` | list transactions |
| `GET` | `/api/finance/transactions/:id` | — | one transaction |
| `GET` | `/api/finance/holdings` | — | latest snapshot per asset (= holdings VIEW) |

### Mutations

| Method | Path | Body shape | Idempotency |
|---|---|---|---|
| `POST` | `/api/finance/assets` | `{code,name,asset_type,bucket,channel?,currency?,risk_level?,holding_cost_pct?,expected_yield_pct?,notes?}` | UPSERT by `code` (revives archived rows) |
| `PATCH` | `/api/finance/assets/:id` | any subset of the above + `clear_holding_cost_pct`, `clear_expected_yield_pct` | partial update |
| `DELETE` | `/api/finance/assets/:id` | — | soft delete (sets `archived_at`) |
| `POST` | `/api/finance/snapshots` | `{asset_id\|asset_code, snapshot_date, balance_yuan\|balance_cents, expected_yield_pct?, actual_yield_pct?, notes?}` | UPSERT by `(asset_id, snapshot_date)` |
| `PATCH` | `/api/finance/snapshots/:id` | subset of above + `clear_*_yield_pct` | partial update; `(asset_id, snapshot_date)` is immutable — delete + repost to move |
| `DELETE` | `/api/finance/snapshots/:id` | — | hard delete |
| `POST` | `/api/finance/transactions` | `{asset_id\|asset_code, txn_date, direction, amount_yuan\|amount_cents, fee_yuan?\|fee_cents?, notes?}` | always inserts (no natural key) → `201 Created` |
| `PATCH` | `/api/finance/transactions/:id` | subset (any field except `asset_id`) | partial update |
| `DELETE` | `/api/finance/transactions/:id` | — | hard delete |

### Publish

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/api/finance/publish` | run publish job synchronously, return `{ok,commit_sha,files_written,pushed,message}` |

### Money input

Mutation endpoints that accept money take **either** `*_cents` (lossless `int64`, preferred for programmatic clients) **or** `*_yuan` (`float64`, convenient for curl). Sending both → `400 ErrMoneyConflict`. Sending neither (when required) → `400 ErrMoneyMissing`. Internally stored as cents; responses include both.

### Status codes

- `200 OK` — successful read / update / upsert / soft-delete / hard-delete
- `201 Created` — `POST /api/finance/transactions` only (true insert)
- `400 Bad Request` — validation failed (enum out of range, bad date, negative cents, missing required field, both yuan+cents)
- `404 Not Found` — `:id` does not exist
- `500 Internal Server Error` — DB error or publish-job failure

M2 follow-ups: ~~`make import-from-exports` (DR rebuild)~~ done in M1.6, ~~switch app to `sqlc` generated code~~ done in M1.6 (hybrid: static SQL via sqlc, dynamic SQL hand-rolled — see `internal/db/store/store.go` package comment for the split rationale). Next: kick off `personal-web` (P2-24) consuming this API.

---

## 5. Money & precision

All monetary amounts are stored as **`INTEGER` cents** (`balance_cents BIGINT`) to avoid floating-point drift. Display layer divides by 100. CSV exports use yuan with 2 decimals for human readability + a `_cents` column for round-trip safety.

---

## 6. Asset taxonomy (decision #31)

7 `asset_type` values cover all current data; `wealth-mgmt-product` added as 8th to accommodate 招行理财 / 腾讯理财 / 微众理财 series (the raw markdown's 稳钱 bucket items that aren't true ETFs).

| asset_type | Examples |
|---|---|
| `cash-cny` | 招商银行朝朝宝, 微众活期+, 腾讯活期+ |
| `wealth-mgmt-product` | 招行理财·多月宝, 中邮邮鸿宝, 微众稳健理财 |
| `etf-fund` | 招行基金/雪球基金/腾讯基金 (open-end / FOF / 债基 / 混合) |
| `cn-stock` | 同花顺 A 股账户 |
| `hk-stock` | (none yet) |
| `us-stock` | (none yet) |
| `social-insurance` | (placeholder for future) |
| `real-estate` | (placeholder for future) |

`bucket` is a separate orthogonal field: `cash` / `stable` / `growth` (= 活钱 / 稳钱 / 长钱).

---

## 7. Publish job behavior

See `internal/publish/`:

1. `cd $PUBLISH_WORKTREE && git fetch origin main && git reset --hard origin/main`
2. assert `git status -s` is empty (else abort + log)
3. dump 4 tables → `finance/exports/{assets,snapshots,transactions,holdings}-YYYY-MM-DD.csv`
4. `git add finance/exports/` (whitelist enforced by hook)
5. `git commit -m "[auto-publish] finance/exports: <ts> by personal-finance"`
6. if `PUBLISH_PUSH=1`: `git push`; else stop after commit
7. on failure at any step: `git restore --staged . && git restore .` + remove unstaged new files; log to `~/Library/Logs/personal/personal-finance.log`

Hook bypass (e.g. for emergency change of whitelist scope) requires `ALLOW_PUBLISH_OUT_OF_WHITELIST=1` and is intentionally not exposed via env-var defaults.

---

## 8. Disaster recovery

`state/finance.db` lives only on this machine and is gitignored; the published CSVs in `obsidian-wiki/finance/exports/` are the only off-machine copy. The `import-from-exports` command is the inverse of publish.

```bash
# fresh DB (file missing): auto-runs migrations + imports the latest dated CSVs
make import-from-exports

# DB exists but has data: refused by default — pass FORCE=1 to wipe + replace
make import-from-exports FORCE=1

# pin a specific date (default = newest YYYY-MM-DD found in the source dir)
make import-from-exports DATE=2026-04-15

# round-trip parity check: import, then re-dump CSVs to a temp dir and SHA-256
# compare to the source files. Any drift = the export format silently changed
# and DR would have been lossy.
make import-from-exports-verify FORCE=1
```

Round-trip property: `import → publish → byte-identical CSVs`. The single source of truth for the CSV format is `internal/csvexport/`, used by both publish and the verifier — so you can't change one without breaking the other (they share code, not schema).

Lossy edge case (documented, not fixed): if you `DELETE /api/finance/assets/:id` (soft-archive) **without** first deleting that asset's snapshots, the snapshots CSV will contain rows whose `asset_code` is no longer in the assets CSV, and `import-from-exports` will fail. Workflow: delete snapshots first, then archive — or accept that DR will lose archived assets' historical snapshots. (Future fix: include `archived_at` column in assets CSV and dump archived assets too — bumps publish format to v2.)

Repo loss: this repo has no SoT data, only code → re-clone from origin (when one exists) or rebuild from this README.

---

## 9. Reference

- `obsidian-wiki/_system/SYSTEM-DESIGN.md` §6 / §14 / §15 / §20 (P2-23) / §23 decisions #10 / #16 / #30–#34
- `obsidian-wiki/_system/standards/publish-workflow.md` §2.3
- `obsidian-wiki/finance/README.md` (publish-side contract)
- `obsidian-wiki/raw/legacy/career/理财/资产配置计划.md` (seed data source)
