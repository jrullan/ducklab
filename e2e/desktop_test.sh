#!/bin/sh
# AC-28 / AC-29 against the REAL engine, not the fake.
#
# AC-28: a run started from the CLI is visible to a desktop client, and vice
#        versa — neither client holds state (I11).
# AC-29: closing the desktop app does not stop a run; reattaching sees the
#        full backlog with no gap and no duplicate.
set -eu

ROOT=$(cd "$(dirname "$0")/.." && pwd)
WORK=$(mktemp -d)
export XDG_CONFIG_HOME="$WORK/config" XDG_DATA_HOME="$WORK/data" XDG_STATE_HOME="$WORK/state"
mkdir -p "$XDG_CONFIG_HOME/ducklab"
PASS=0; FAIL=0
ok()  { PASS=$((PASS+1)); printf '  \033[32mPASS\033[0m %s\n' "$1"; }
bad() { FAIL=$((FAIL+1)); printf '  \033[31mFAIL\033[0m %s\n' "$1"; }
info(){ printf '\n\033[1m%s\033[0m\n' "$1"; }

cleanup() { [ -n "${EPID:-}" ] && kill "$EPID" 2>/dev/null; rm -rf "$WORK"; }
trap cleanup EXIT INT TERM

cat > "$XDG_CONFIG_HOME/ducklab/config.toml" <<'CFG'
schema = 1
[provider.fake]
kind = "openai"
base_url = "fake://"
[duckling.pato-uno]
provider = "fake"
model = "fake-a"
[duckling.pato-uno.caps]
native_tools = false
[duckling.pato-dos]
provider = "fake"
model = "fake-b"
[duckling.pato-dos.caps]
native_tools = false
CFG

info "building"
( cd "$ROOT" && CGO_ENABLED=0 go build -o "$WORK/ducklab" ./cmd/ducklab \
  && CGO_ENABLED=0 go build -o "$WORK/ducklab-engine" ./cmd/ducklab-engine )

PROJ="$WORK/proj"
cp -r "$ROOT/fixtures/fixture-go-red" "$PROJ"
( cd "$PROJ" && git init -q . && git config user.email t@t && git config user.name t \
  && git add -A && git commit -qm init )

"$WORK/ducklab-engine" > "$WORK/engine.log" 2>&1 &
EPID=$!
i=0; while [ $i -lt 100 ]; do [ -f "$XDG_STATE_HOME/ducklab/engine.json" ] && break; i=$((i+1)); sleep 0.1; done
PORT=$(sed -n 's/.*"port": *\([0-9]*\).*/\1/p' "$XDG_STATE_HOME/ducklab/engine.json")
TOKEN=$(sed -n 's/.*"token": *"\([^"]*\)".*/\1/p' "$XDG_STATE_HOME/ducklab/engine.json")
api() { curl -sS -H "Authorization: Bearer $TOKEN" "$@"; }

PID_=$(api -X POST -H 'Content-Type: application/json' \
  -d "{\"path\":\"$PROJ\",\"name\":\"x\"}" "http://127.0.0.1:$PORT/v1/projects" \
  | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')

info "AC-28: a CLI-started run is visible to a second client"
# Start from the CLI, detached, exactly as the desktop would observe it.
( cd "$PROJ" && "$WORK/ducklab" run T-001 --mode pair --no-wait >/dev/null 2>&1 ) || true
sleep 1
RID=$(api "http://127.0.0.1:$PORT/v1/runs" | sed -n 's/.*"id":"\(r-[^"]*\)".*/\1/p' | head -1)
if [ -n "$RID" ]; then ok "a second client sees the CLI's run ($RID)"; else bad "the CLI's run was invisible"; fi

# A desktop client would authenticate its stream with ?token=, since
# EventSource cannot set headers.
CODE=$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 \
  "http://127.0.0.1:$PORT/v1/events?run=$RID&token=$TOKEN" || true)
[ "$CODE" = "200" ] && ok "SSE authenticates with a query token (EventSource path)" \
  || bad "SSE with ?token= returned $CODE"

CODE=$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 \
  "http://127.0.0.1:$PORT/v1/events?run=$RID&token=wrong" || true)
[ "$CODE" = "401" ] && ok "a wrong query token is still refused" || bad "wrong token returned $CODE"

info "AC-28: CORS preflight is answered for a browser client"
CODE=$(curl -s -o /dev/null -w '%{http_code}' -X OPTIONS \
  -H "Origin: wails://wails" -H "Access-Control-Request-Headers: authorization,x-ducklab-client" \
  "http://127.0.0.1:$PORT/v1/runs")
[ "$CODE" = "204" ] && ok "preflight from the app origin is answered" || bad "preflight returned $CODE"

HDRS=$(curl -sS -D - -o /dev/null -X OPTIONS -H "Origin: wails://wails" \
  -H "Access-Control-Request-Headers: authorization" "http://127.0.0.1:$PORT/v1/runs")
echo "$HDRS" | grep -qi "x-ducklab-client" \
  && ok "the version header the engine requires is allowed" \
  || bad "X-Ducklab-Client is not in Access-Control-Allow-Headers"

CODE=$(curl -s -o /dev/null -w '%{http_code}' -X OPTIONS -H "Origin: https://evil.example" \
  "http://127.0.0.1:$PORT/v1/runs")
[ "$CODE" = "403" ] && ok "an unknown origin's preflight is refused" || bad "unknown origin returned $CODE"

info "AC-29: a client detaching does not stop the run"
sleep 3
STATUS=$(api "http://127.0.0.1:$PORT/v1/runs/$RID" | sed -n 's/.*"status":"\([a-z]*\)".*/\1/p' | head -1)
if [ "$STATUS" != "failed" ]; then
  ok "the run survived every client disconnecting (status: $STATUS)"
else
  bad "the run died when clients detached"
fi

info "AC-29: reattaching replays the backlog with no gap or duplicate"
curl -sS -N --max-time 3 "http://127.0.0.1:$PORT/v1/events?run=$RID&token=$TOKEN&from_seq=0" \
  > "$WORK/stream.txt" 2>/dev/null || true
STREAM_SEQS=$(grep '^id: ' "$WORK/stream.txt" | sed 's/.*://' | sort -n)
DISK_SEQS=$(sed -n 's/.*"seq":\([0-9]*\).*/\1/p' "$PROJ/.ducklab/runs/$RID/events.jsonl" | sort -n)
if [ "$STREAM_SEQS" = "$DISK_SEQS" ]; then
  ok "the replayed stream matches events.jsonl exactly ($(echo "$STREAM_SEQS" | wc -l) events)"
else
  bad "stream and disk differ"
  printf '   stream: %s\n   disk:   %s\n' "$(echo "$STREAM_SEQS" | tr '\n' ' ')" "$(echo "$DISK_SEQS" | tr '\n' ' ')"
fi
DUPES=$(echo "$STREAM_SEQS" | uniq -d)
[ -z "$DUPES" ] && ok "no duplicate seq in the replay" || bad "duplicate seqs: $DUPES"

info "summary"
printf '  %d passed, %d failed\n\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
