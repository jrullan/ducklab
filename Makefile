# ducklab
#
# `make api` regenerates the OpenAPI document and the TypeScript client from
# internal/engineapi's route table. `make api-check` fails when the committed
# artefacts are stale — that check is what actually prevents hand-editing a
# generated file, since a rule nobody enforces is a comment.

GO      ?= go
TARGETS  = linux/amd64 linux/arm64 darwin/arm64 windows/amd64

# The -dirty marker considers COMPILED sources only: .ducklab is harness state
# the engine rewrites on every Settings change, and a stamp that cries dirty
# over a roster pin dies of alarm fatigue. Why the marker exists at all: a
# binary built over a stale checkout wore HEAD's clean sha and served pre-fix
# code — an evening went to chasing that phantom regression.
SRCDIRTY = $$(git diff-index --quiet HEAD -- . ':(exclude).ducklab' 2>/dev/null || echo -dirty)
STAMPVER = $$(git describe --tags --always 2>/dev/null || echo unknown)$(SRCDIRTY)
STAMPSHA = $$(git rev-parse HEAD 2>/dev/null || echo unknown)$(SRCDIRTY)

.PHONY: all build test test-race vet api api-check e2e mcpb frontend desktop cross clean dev-install

all: vet test frontend

build:
	CGO_ENABLED=0 $(GO) build -ldflags "-X github.com/jrullan/ducklab/internal/build.Version=$(STAMPVER) -X github.com/jrullan/ducklab/internal/build.Branch=$$(git branch --show-current 2>/dev/null || echo unknown) -X github.com/jrullan/ducklab/internal/build.Commit=$(STAMPSHA)" -o bin/ducklab ./cmd/ducklab
	CGO_ENABLED=0 $(GO) build -ldflags "-X github.com/jrullan/ducklab/internal/build.Version=$(STAMPVER) -X github.com/jrullan/ducklab/internal/build.Branch=$$(git branch --show-current 2>/dev/null || echo unknown) -X github.com/jrullan/ducklab/internal/build.Commit=$(STAMPSHA)" -o bin/ducklab-engine ./cmd/ducklab-engine

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
	@# A fresh clone has no frontend/node_modules and `make` died at "tsc:
	@# not found" 23 seconds into the first stranger install. npm ci is
	@# deterministic (lockfile-exact), so running it when the directory is
	@# absent keeps the README's promise that `make` just works.
	@if [ ! -d frontend/node_modules ]; then 	  echo "  frontend/node_modules missing — running npm ci"; 	  cd frontend && npm ci; 	fi
	cd frontend && npm run build && npx vitest run

e2e:
	sh e2e/ac_test.sh
	sh e2e/desktop_test.sh
	sh e2e/mcpb_test.sh
	cd frontend && npx playwright test

# Build the Linux/amd64 MCPB release artifact. MCPB files are ZIP archives
# containing a manifest and an executable entry point; keep the staging tree
# outside the repository so a build never leaves generated files behind.
mcpb:
	@set -eu; \
	stage=$$(mktemp -d); \
	output=$$(pwd)/dist/ducklab-mcp.mcpb; \
	trap 'rm -rf "$$stage"' EXIT; \
	mkdir -p "$$stage" "$$(dirname "$$output")"; \
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -o "$$stage/ducklab-linux-amd64" ./cmd/ducklab; \
	chmod 755 "$$stage/ducklab-linux-amd64"; \
	version=$$(git describe --tags --always 2>/dev/null || echo dev); \
	dir_token='$${__dirname}'; \
	printf '%s\n' "{\"manifest_version\":\"0.2\",\"name\":\"ducklab\",\"version\":\"$$version\",\"description\":\"ducklab MCP server\",\"server\":{\"type\":\"stdio\",\"entry_point\":\"ducklab-linux-amd64\",\"mcp_config\":{\"command\":\"$$dir_token/ducklab-linux-amd64\",\"args\":[\"mcp\",\"serve\"]}}}" > "$$stage/manifest.json"; \
	( cd "$$stage" && zip -q -X "$$output" manifest.json ducklab-linux-amd64 ); \
	echo "  built dist/ducklab-mcp.mcpb"

desktop:
	cd frontend && npm run build
	rm -rf cmd/ducklab-desktop/frontend/dist
	cp -r frontend/dist cmd/ducklab-desktop/frontend/dist
	$(GO) build -ldflags "-X github.com/jrullan/ducklab/internal/build.Version=$(STAMPVER) -X github.com/jrullan/ducklab/internal/build.Branch=$$(git branch --show-current 2>/dev/null || echo unknown) -X github.com/jrullan/ducklab/internal/build.Commit=$(STAMPSHA)" -o bin/ducklab-desktop ./cmd/ducklab-desktop

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
	@# A running engine keeps serving the binary it started with. Installing a
	@# new one changes nothing until it restarts, and the symptom is a route
	@# that 405s or a fix that appears not to have worked.
	@#
	@# Compared against the *sources*, not the binary. Go stamps vcs.revision
	@# into every build, so the bytes change on every commit even when no Go
	@# file did — the first version of this check compared bytes and fired
	@# after frontend-only work, twice, which is how a warning stops being
	@# read.
	@pid=$$(pgrep -x ducklab-engine 2>/dev/null | head -1); \
	if [ -n "$$pid" ]; then \
	  age=$$(ps -o etimes= -p $$pid 2>/dev/null | tr -d ' '); \
	  if [ -n "$$age" ] && [ -n "$$(find cmd internal -name '*.go' -newermt "-$$age seconds" -print -quit 2>/dev/null)" ]; then \
	    echo "  warning: engine (pid $$pid) started before the newest Go change"; \
	    echo "           restart it, or your change is not in effect"; \
	  fi; \
	fi
	@echo "ducklab $$($(PREFIX)/bin/ducklab --version 2>/dev/null | head -1)"

.PHONY: install

# dev-install is the loop we live daily: rebuild everything, install it, and
# make the installed engine the one that answers. The restart refuses while
# runs are going — finish or abort them first — and the desktop picks up the
# fresh engine via its own Restart button, or on next launch.
dev-install: desktop install
	@$(PREFIX)/bin/ducklab engine restart || true
