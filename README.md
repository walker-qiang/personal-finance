# personal-finance

> Owner backend of `obsidian-wiki/finance/exports/`. Single-user, localhost-only, snapshot-driven asset tracker.
>
> **Lifetime**: 5–10 years. **Repo posture**: permanent private (decision #33 in `obsidian-wiki/_system/SYSTEM-DESIGN.md`).
>
> Status (2026-04-20): **M1** — bootstrap, first migration, publish job framework. Manual-trigger publish, no auto-dump after every write.

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

## 4. Endpoints (M1)

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/healthz` | liveness |
| `GET` | `/api/finance/assets` | list assets |
| `GET` | `/api/finance/snapshots?asset_id=&from=&to=` | list snapshots |
| `POST` | `/api/finance/publish` | run publish job synchronously, return `{commit, files, pushed}` |

M2 will add: POST endpoints to mutate snapshots/transactions, transaction list, holdings view, web UI.

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

- `state/finance.db` lost? → run `make import-from-exports` (M2) to rebuild from `obsidian-wiki/finance/exports/*.csv`.
- This repo lost? → re-clone from origin; this repo has no SoT data, only code.

---

## 9. Reference

- `obsidian-wiki/_system/SYSTEM-DESIGN.md` §6 / §14 / §15 / §20 (P2-23) / §23 decisions #10 / #16 / #30–#34
- `obsidian-wiki/_system/standards/publish-workflow.md` §2.3
- `obsidian-wiki/finance/README.md` (publish-side contract)
- `obsidian-wiki/raw/legacy/career/理财/资产配置计划.md` (seed data source)
