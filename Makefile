SHELL := /bin/bash
GO    := /opt/homebrew/bin/go
BIN   := $(CURDIR)/bin

GOOSE := $(BIN)/goose
SQLC  := $(BIN)/sqlc

DB_PATH         ?= $(CURDIR)/state/finance.db
MIGRATIONS_DIR  := $(CURDIR)/internal/db/migrations
PUBLISH_WORKTREE ?= $(HOME)/obsidian-wiki-publish-worktree

export FINANCE_DB_PATH=$(DB_PATH)
export PUBLISH_WORKTREE

.PHONY: tools migrate migrate-status migrate-down sqlc seed run build publish-dry import-from-exports import-from-exports-verify test fmt vet clean help

help:
	@echo "Targets:"
	@echo "  tools           install goose + sqlc into ./bin"
	@echo "  migrate         apply all up migrations to $(DB_PATH)"
	@echo "  migrate-status  show migration state"
	@echo "  migrate-down    roll back the last migration"
	@echo "  sqlc            regenerate sqlc code from internal/db/queries/*.sql"
	@echo "  seed            load seed/snapshot-20260301.json (idempotent upsert)"
	@echo "  run             run server on localhost:7001"
	@echo "  build           compile cmd/* to ./bin"
	@echo "  publish-dry     run publish job locally (no push, just commit)"
	@echo "  import-from-exports         DR: rebuild $(DB_PATH) from latest CSVs in PUBLISH_WORKTREE/finance/exports/"
	@echo "                              (refuses if DB non-empty; pass FORCE=1 to wipe)"
	@echo "  import-from-exports-verify  same as above + round-trip parity check"
	@echo "  test            run go test ./..."
	@echo "  fmt vet         go fmt / go vet"
	@echo "  clean           remove ./bin and state/finance.db (DESTRUCTIVE)"

$(BIN):
	mkdir -p $(BIN)

tools: $(BIN)
	GOBIN=$(BIN) $(GO) install github.com/pressly/goose/v3/cmd/goose@v3.22.1
	@echo
	@echo "Note: sqlc is intentionally NOT auto-installed in M1."
	@echo "      M1 uses hand-rolled queries in internal/db/store/. The sqlc.yaml"
	@echo "      and queries/*.sql are committed so M2+ can switch to generated"
	@echo "      code. Install sqlc manually with:"
	@echo "        GOBIN=\$$PWD/bin go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.27.0"
	@echo "      (warning: sqlc pulls pg_query_go which requires cgo; on macOS 15"
	@echo "       you may hit a strchrnul build error — pin a newer sqlc or use brew)."

migrate:
	$(GOOSE) -dir $(MIGRATIONS_DIR) sqlite3 $(DB_PATH) up

migrate-status:
	$(GOOSE) -dir $(MIGRATIONS_DIR) sqlite3 $(DB_PATH) status

migrate-down:
	$(GOOSE) -dir $(MIGRATIONS_DIR) sqlite3 $(DB_PATH) down

sqlc:
	$(SQLC) generate

seed:
	$(GO) run ./cmd/seed -db $(DB_PATH) -file $(CURDIR)/seed/snapshot-20260301.json

run:
	$(GO) run ./cmd/server

build: $(BIN)
	$(GO) build -o $(BIN)/finance-server ./cmd/server
	$(GO) build -o $(BIN)/finance-seed   ./cmd/seed

publish-dry:
	$(GO) run ./cmd/server -publish-once

# DR: rebuild finance.db from the latest CSVs published into the
# obsidian-wiki-publish-worktree. Refuses by default if DB has rows;
# pass FORCE=1 to wipe and re-import. Use DATE=YYYY-MM-DD to pin.
import-from-exports:
	$(GO) run ./cmd/import \
		-db   $(DB_PATH) \
		-from $(PUBLISH_WORKTREE)/finance/exports \
		$(if $(DATE),-date $(DATE),) \
		$(if $(FORCE),-force,)

import-from-exports-verify:
	$(GO) run ./cmd/import \
		-db   $(DB_PATH) \
		-from $(PUBLISH_WORKTREE)/finance/exports \
		$(if $(DATE),-date $(DATE),) \
		$(if $(FORCE),-force,) \
		-verify

test:
	$(GO) test ./...

fmt:
	$(GO) fmt ./...

vet:
	$(GO) vet ./...

clean:
	rm -rf $(BIN)
	rm -f $(DB_PATH) $(DB_PATH)-shm $(DB_PATH)-wal
