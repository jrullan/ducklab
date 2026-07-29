# ducklab
#
# `make api` regenerates the OpenAPI document and the TypeScript client from
# internal/engineapi's route table. `make api-check` fails when the committed
# artefacts are stale — that check is what actually prevents hand-editing a
# generated file, since a rule nobody enforces is a comment.

GO      ?= go
TARGETS  = linux/amd64 linux/arm64 darwin/arm64 windows/amd64

.PHONY: all build test test-race vet api api-check e2e frontend desktop cross clean

all: vet test frontend

build:
	CGO_ENABLED=0 $(GO) build -o bin/ducklab ./cmd/ducklab
	CGO_ENABLED=0 $(GO) build -o bin/ducklab-engine ./cmd/ducklab-engine

vet:
	$(GO) vet ./...

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

# No post-formatter: api-check compares byte-for-byte, and a formatter run
# only on `api` and not on `check` would make every fresh generation look
# stale. The generator emits its own final formatting.
api:
	$(GO) run ./cmd/apigen -out .

# Regenerates into a scratch tree and diffs. Fails on drift.
api-check:
	@tmp=$$(mktemp -d); \
	mkdir -p $$tmp/docs $$tmp/frontend/src/api; \
	$(GO) run ./cmd/apigen -out $$tmp >/dev/null; \
	fail=0; \
	for f in docs/openapi.json frontend/src/api/generated.ts; do \
	  if ! diff -q "$$f" "$$tmp/$$f" >/dev/null 2>&1; then \
	    echo "STALE: $$f — run 'make api'"; \
	    diff -u "$$f" "$$tmp/$$f" | head -40; \
	    fail=1; \
	  fi; \
	done; \
	rm -rf $$tmp; \
	if [ $$fail -eq 0 ]; then echo "generated API artefacts are up to date"; fi; \
	exit $$fail

frontend:
	cd frontend && npm run build && npx vitest run

e2e:
	sh e2e/ac_test.sh
	sh e2e/desktop_test.sh
	cd frontend && npx playwright test

desktop:
	cd frontend && npm run build
	rm -rf cmd/ducklab-desktop/frontend/dist
	cp -r frontend/dist cmd/ducklab-desktop/frontend/dist
	$(GO) build -o bin/ducklab-desktop ./cmd/ducklab-desktop

cross:
	@for t in $(TARGETS); do \
	  os=$${t%/*}; arch=$${t#*/}; \
	  CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build -o /dev/null ./cmd/ducklab ./cmd/ducklab-engine \
	    && echo "  OK   $$t" || echo "  FAIL $$t"; \
	done

clean:
	rm -rf bin fake-engine-bin frontend/dist frontend/test-results

PREFIX ?= $(HOME)/.local

# install puts the CLI and engine on PATH together.
#
# They must move as a pair: the engine rejects a client whose major version
# differs, so installing one and not the other turns every command into a
# version_skew error. Any binary already at the target is backed up rather than
# overwritten — the first person this bit had a superseded ducklab from an
# earlier iteration shadowing the new one, and silently deleting it would have
# destroyed the only copy.
install: build
	@mkdir -p $(PREFIX)/bin
	@# The desktop joins them when it has been built. It needs cgo and the
	@# frontend bundle, so `build` does not produce it, but an installed path
	@# is what an AppArmor profile matches on — see packaging/apparmor.
	@# Which means install ships whatever bin/ducklab-desktop happens to be.
	@# Say so when that is older than the frontend it claims to bundle: the
	@# alternative is a desktop that silently shows the previous build, and
	@# the next hour goes to debugging a UI change that was never in it.
	@if [ -x bin/ducklab-desktop ] && [ -n "$$(find frontend/src frontend/index.html -newer bin/ducklab-desktop 2>/dev/null)" ]; then \
	  echo "  warning: bin/ducklab-desktop predates frontend/src — run 'make desktop' to rebuild the bundle"; \
	fi
	@for b in ducklab ducklab-engine $$([ -x bin/ducklab-desktop ] && echo ducklab-desktop); do \
	  t=$(PREFIX)/bin/$$b; \
	  if [ -e $$t ] && ! cmp -s bin/$$b $$t; then \
	    mv $$t $$t.bak && echo "  backed up $$t -> $$t.bak"; \
	  fi; \
	  install -m 755 bin/$$b $$t && echo "  installed $$t"; \
	done
	@echo "ducklab $$($(PREFIX)/bin/ducklab --version 2>/dev/null | head -1)"

.PHONY: install
