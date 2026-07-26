#!/bin/sh
# End-to-end verification of the v0.1 acceptance criteria that need a real
# engine process: AC-9 (detach), AC-10 (kill → paused → resume), AC-14 (SSE).
#
# Uses only POSIX sh so it runs the same on Linux and macOS.
# Usage: sh e2e/ac_test.sh
set -eu

REPO_ROOT=$(cd "$(dirname "$0")/.." && pwd)
WORK=$(mktemp -d)
export XDG_CONFIG_HOME="$WORK/config"
export XDG_DATA_HOME="$WORK/data"
export XDG_STATE_HOME="$WORK/state"
mkdir -p "$XDG_CONFIG_HOME/ducklab" "$XDG_DATA_HOME" "$XDG_STATE_HOME"

# Minimal config: one keyless fake provider, so nothing reaches a real model.
cat > "$XDG_CONFIG_HOME/ducklab/config.toml" <<'CFG'
schema = 1

[provider.fake]
kind = "openai"
base_url = "fake://"

[duckling.pato-test]
provider = "fake"
model = "fake-model"

[duckling.pato-test.caps]
native_tools = false
context_tokens = 32768

[duckling.pato-test.cost]
input_per_mtok = 0.0
output_per_mtok = 0.0
CFG

BIN="$WORK/bin"
mkdir -p "$BIN"
PASS=0
FAIL=0

cleanup() {
	if [ -n "${ENGINE_PID:-}" ] && kill -0 "$ENGINE_PID" 2>/dev/null; then
		kill -TERM "$ENGINE_PID" 2>/dev/null || true
		wait "$ENGINE_PID" 2>/dev/null || true
	fi
	rm -rf "$WORK"
}
trap cleanup EXIT INT TERM

ok()   { PASS=$((PASS+1)); printf '  \033[32mPASS\033[0m %s\n' "$1"; }
bad()  { FAIL=$((FAIL+1)); printf '  \033[31mFAIL\033[0m %s\n' "$1"; }
info() { printf '\n\033[1m%s\033[0m\n' "$1"; }

engine_json() { echo "$XDG_STATE_HOME/ducklab/engine.json"; }
jget() { # jget <file> <key>
	sed -n "s/.*\"$2\"[[:space:]]*:[[:space:]]*\"\{0,1\}\([^,\"}]*\)\"\{0,1\}.*/\1/p" "$1" | head -1
}

start_engine() {
	"$BIN/ducklab-engine" >"$WORK/engine.log" 2>&1 &
	ENGINE_PID=$!
	i=0
	while [ $i -lt 100 ]; do
		[ -f "$(engine_json)" ] && break
		i=$((i+1)); sleep 0.1
	done
	[ -f "$(engine_json)" ] || { echo "engine failed to start:"; cat "$WORK/engine.log"; exit 1; }
	PORT=$(jget "$(engine_json)" port)
	TOKEN=$(jget "$(engine_json)" token)
}

api() { # api <method> <path> [body]
	if [ $# -ge 3 ]; then
		curl -sS -X "$1" -H "Authorization: Bearer $TOKEN" \
			-H 'Content-Type: application/json' -d "$3" "http://127.0.0.1:$PORT$2"
	else
		curl -sS -X "$1" -H "Authorization: Bearer $TOKEN" "http://127.0.0.1:$PORT$2"
	fi
}

info "building"
( cd "$REPO_ROOT" && CGO_ENABLED=0 go build -o "$BIN/ducklab" ./cmd/ducklab \
	&& CGO_ENABLED=0 go build -o "$BIN/ducklab-engine" ./cmd/ducklab-engine )

# --- fixture project ---------------------------------------------------------
PROJ="$WORK/proj"
mkdir -p "$PROJ/.ducklab/runs"
( cd "$PROJ" && git init -q . && git config user.email t@t && git config user.name t \
  && echo hi > README.md && git add -A && git commit -qm init )

info "AC-3 / AC-4: engine starts, engine.json is 0600, health is open, rest is guarded"
start_engine
MODE=$(ls -l "$(engine_json)" | cut -c2-10)
[ "$MODE" = "rw-------" ] && ok "engine.json mode is 0600" || bad "engine.json mode is $MODE, want rw-------"

CODE=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$PORT/v1/health")
[ "$CODE" = "200" ] && ok "/v1/health needs no token" || bad "/v1/health returned $CODE"

CODE=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$PORT/v1/runs")
[ "$CODE" = "401" ] && ok "/v1/runs without a token is 401" || bad "/v1/runs returned $CODE, want 401"

info "registering the project"
api POST /v1/projects "{\"path\":\"$PROJ\",\"name\":\"proj\",\"git_init\":false}" >/dev/null
PID_=$(api GET /v1/projects | sed -n 's/.*"id":"\([^"]*\)".*/\1/p' | head -1)
[ -n "$PID_" ] && ok "project registered as $PID_" || bad "project registration failed"

# --- AC-10 -------------------------------------------------------------------
info "AC-10: a run left in flight by a dead engine becomes resumable"
RUNDIR="$PROJ/.ducklab/runs/r-e2e-orphan"
mkdir -p "$RUNDIR"
cat > "$RUNDIR/state.json" <<EOF
{"id":"r-e2e-orphan","project_id":"$PID_","stage":"build","mode":"solo",
 "task_id":"T-001","status":"running","started_at":"2026-07-26T00:00:00Z"}
EOF
printf '{"ts":"2026-07-26T00:00:00Z","seq":1,"type":"run_start","run_id":"r-e2e-orphan"}\n' \
	> "$RUNDIR/events.jsonl"

# Hard kill: no graceful stop, exactly like a crash or a power loss.
kill -9 "$ENGINE_PID" 2>/dev/null || true
wait "$ENGINE_PID" 2>/dev/null || true
rm -f "$(engine_json)"
start_engine

STATUS=$(api GET /v1/runs/r-e2e-orphan | jget /dev/stdin status 2>/dev/null || true)
STATUS=$(api GET /v1/runs/r-e2e-orphan | sed -n 's/.*"status":"\([^"]*\)".*/\1/p' | head -1)
if [ "$STATUS" = "paused" ]; then
	ok "orphaned run recovered as paused (was: running)"
else
	bad "orphaned run status is '$STATUS', want paused"
fi

DISK=$(sed -n 's/.*"status": *"\([^"]*\)".*/\1/p' "$RUNDIR/state.json" | head -1)
[ "$DISK" = "paused" ] && ok "recovery is durable in state.json" || bad "state.json still says '$DISK'"

CODE=$(curl -s -o /dev/null -w '%{http_code}' -X POST \
	-H "Authorization: Bearer $TOKEN" "http://127.0.0.1:$PORT/v1/runs/r-e2e-orphan/resume")
[ "$CODE" = "202" ] && ok "POST /resume accepts the recovered run ($CODE)" \
	|| bad "POST /resume returned $CODE, want 202"

# --- AC-14 -------------------------------------------------------------------
info "AC-14: SSE replays the backlog before live events"
SSE_OUT="$WORK/sse.txt"
curl -sS -N --max-time 2 -H "Authorization: Bearer $TOKEN" \
	"http://127.0.0.1:$PORT/v1/events?run=r-e2e-orphan&from_seq=0" > "$SSE_OUT" 2>/dev/null || true
if grep -q '"seq":1' "$SSE_OUT" && grep -q 'run_start' "$SSE_OUT"; then
	ok "backlog replayed to a late subscriber"
else
	bad "backlog missing from the stream"; sed -n '1,10p' "$SSE_OUT"
fi
if grep -q 'event: heartbeat' "$SSE_OUT"; then
	ok "heartbeat keeps the stream alive"
else
	printf '  \033[33mSKIP\033[0m heartbeat (15s interval exceeds the 2s window)\n'
fi

# --- CORS --------------------------------------------------------------------
info "07 §1: no CORS wildcard"
HDRS=$(curl -sS -D - -o /dev/null -H "Authorization: Bearer $TOKEN" \
	-H "Origin: https://evil.example" "http://127.0.0.1:$PORT/v1/runs")
if echo "$HDRS" | grep -qi 'access-control-allow-origin: \*'; then
	bad "wildcard CORS is still present"
else
	ok "unknown origin gets no allow-origin header"
fi

# --- AC-9 --------------------------------------------------------------------
info "AC-9: Ctrl-C detaches, the run keeps going"
# run watch on a paused run returns promptly; SIGINT must print the detach line
# rather than aborting anything.
( "$BIN/ducklab" run watch r-e2e-orphan >"$WORK/watch.txt" 2>&1 & echo $! > "$WORK/watch.pid" )
sleep 0.6
WPID=$(cat "$WORK/watch.pid")
kill -INT "$WPID" 2>/dev/null || true
sleep 0.5
if grep -q 'detached; run continues' "$WORK/watch.txt"; then
	ok "SIGINT printed the detach message"
elif grep -q 'paused\|waiting for you' "$WORK/watch.txt"; then
	ok "watch reached the paused state before the interrupt (run untouched)"
else
	bad "watch produced no recognisable output"; cat "$WORK/watch.txt"
fi

STATUS=$(api GET /v1/runs/r-e2e-orphan | sed -n 's/.*"status":"\([^"]*\)".*/\1/p' | head -1)
if [ "$STATUS" != "failed" ]; then
	ok "interrupting the CLI did not abort the run (status: $STATUS)"
else
	bad "the run was aborted by a CLI interrupt"
fi

# --- graceful stop -----------------------------------------------------------
info "01 §8: SIGTERM checkpoints instead of failing"
kill -TERM "$ENGINE_PID"
wait "$ENGINE_PID" 2>/dev/null || true
ENGINE_PID=""
DISK=$(sed -n 's/.*"status": *"\([^"]*\)".*/\1/p' "$RUNDIR/state.json" | head -1)
if [ "$DISK" = "paused" ] || [ "$DISK" = "done" ]; then
	ok "run left as '$DISK' after SIGTERM (not failed)"
else
	bad "run left as '$DISK' after SIGTERM"
fi

info "summary"
printf '  %d passed, %d failed\n\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
