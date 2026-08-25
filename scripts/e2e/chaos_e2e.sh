#!/usr/bin/env bash
# Chaos E2E: run llm-proxy against a deliberately misbehaving mock upstream
# and assert the retry/surfacing contract holds over real HTTP.
#
#   scripts/e2e/chaos_e2e.sh          # fast scenarios only (~1 min)
#   RUN_SLOW=1 scripts/e2e/chaos_e2e.sh  # adds the connect-phase exhaustion
#                                        # scenario, which waits out the full
#                                        # exponential backoff (~3.5 min)
#
# Requires: go, python3, curl.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
WORK=${CHAOS_WORK:-$(mktemp -d /tmp/llm-proxy-chaos.XXXXXX)}
MOCK_PORT=19911
PROXY_PORT=18090
BASE="http://127.0.0.1:$PROXY_PORT"
MOCK_PID=""
PROXY_PID=""

cleanup() {
  [ -n "$MOCK_PID" ] && kill "$MOCK_PID" 2>/dev/null || true
  [ -n "$PROXY_PID" ] && kill "$PROXY_PID" 2>/dev/null || true
  if [ "${FAIL:-0}" = "0" ]; then
    rm -rf "$WORK"
  else
    echo "logs kept in $WORK"
  fi
}
trap cleanup EXIT

PASS=0; FAIL=0
ok()   { echo "PASS: $1"; PASS=$((PASS+1)); }
bad()  { echo "FAIL: $1"; tail -5 "$WORK/proxy.log" || true; FAIL=$((FAIL+1)); }

start_mock() { # start_mock [flags...]
  if [ -n "$MOCK_PID" ]; then kill "$MOCK_PID" 2>/dev/null || true; fi
  sleep 0.3
  python3 "$ROOT/scripts/e2e/mock_upstream.py" "$MOCK_PORT" "$@" \
    >"$WORK/mock.log" 2>&1 &
  MOCK_PID=$!
  local ready=0
  for _ in $(seq 1 50); do
    if curl -fsS "http://127.0.0.1:$MOCK_PORT/v1/models" >/dev/null 2>&1; then
      ready=1; break
    fi
    sleep 0.2
  done
  if [ "$ready" != "1" ]; then
    echo "mock never became ready"; cat "$WORK/mock.log" || true
    exit 1
  fi
}

# --- build and configure the proxy once -------------------------------------
echo "== building llm-proxy =="
go -C "$ROOT" build -o "$WORK/llm-proxy" .
"$WORK/llm-proxy" keys create-user e2e --store "$WORK/keys.json" >/dev/null
KEY=$("$WORK/llm-proxy" keys create e2e --name chaos --store "$WORK/keys.json" | head -1)

cat > "$WORK/config.yaml" <<EOF
server:
  listen: 127.0.0.1:$PROXY_PORT
auth:
  file: $WORK/keys.json
log_level: warn
backends:
  - type: venice
    api_key_env: MOCK_KEY
    base_url: http://127.0.0.1:$MOCK_PORT/v1
default_route:
  backend: venice
EOF

MOCK_KEY=unused "$WORK/llm-proxy" serve --config "$WORK/config.yaml" >"$WORK/proxy.log" 2>&1 &
PROXY_PID=$!
for _ in $(seq 1 50); do
  curl -fsS "$BASE/healthz" >/dev/null 2>&1 && break
  sleep 0.2
done

# --- scenario 1: transient failures are retried invisibly --------------------
echo "== scenario 1: three 429s then success (connect-phase recovery) =="
start_mock --fail-first 3 --fail-status 429 --retry-after 1

BODY=$(curl -fsS --max-time 120 "$BASE/v1/chat/completions" \
  -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -d '{"model":"x","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}')
if echo "$BODY" | grep -q E2E_OK; then ok "429 storm recovered invisibly"; else bad "429 storm recovered invisibly"; fi

# --- scenario 2: mid-stream cut is surfaced, not faked -----------------------
echo "== scenario 2: upstream cuts the stream after the first event =="
start_mock --cut-stream-after 1

SSE=$(curl -sS --max-time 90 "$BASE/v1/chat/completions" \
  -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -d '{"model":"x","stream":true,"max_tokens":16,"messages":[{"role":"user","content":"hi"}]}' || true)
printf '%s\n' "$SSE" >"$WORK/sse.txt"
HAS_ERROR=false; HAS_DONE=false
grep -q '"error"' "$WORK/sse.txt" && HAS_ERROR=true
grep -q '\[DONE\]' "$WORK/sse.txt" && HAS_DONE=true
if [ "$HAS_ERROR" = "true" ] && [ "$HAS_DONE" = "false" ]; then
  ok "cut stream surfaced an in-band error, no fake [DONE]"
else
  bad "cut stream surfaced an in-band error, no fake [DONE] (error=$HAS_ERROR done=$HAS_DONE)"
fi

# --- scenario 3: HTTP 200 with an error body is not a success ---------------
echo "== scenario 3: quota error smuggled behind HTTP 200 =="
start_mock --error-200

CODE=$(curl -sS -o "$WORK/e200.json" -w '%{http_code}' --max-time 90 "$BASE/v1/chat/completions" \
  -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -d '{"model":"x","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}' || echo 000)
if [ "$CODE" = "502" ] && grep -q '"error"' "$WORK/e200.json"; then
  ok "200-with-error-body answered as clean 502"
else
  bad "200-with-error-body answered as clean 502 (got HTTP $CODE)"
fi

# --- scenario 4: soak under concurrency --------------------------------------
echo "== scenario 4: healthy soak, mixed endpoints, concurrent streams =="
start_mock

GOROUTINES_BEFORE=$(curl -fsS -H "Authorization: Bearer $KEY" "$BASE/metrics" | awk '/^go_goroutines/{print int($2)}')
if python3 "$ROOT/scripts/e2e/load_test.py" --url "$BASE" --key "$KEY" \
    --model x --requests 96 --concurrency 12 --stream-ratio 0.6; then
  ok "soak: every request came back 200"
else
  bad "soak: every request came back 200"
fi
GOROUTINES_AFTER=$(curl -fsS -H "Authorization: Bearer $KEY" "$BASE/metrics" | awk '/^go_goroutines/{print int($2)}')
LEAK=$(( GOROUTINES_AFTER - GOROUTINES_BEFORE ))
if [ "$LEAK" -lt 5 ]; then
  ok "goroutines stable after soak (+$LEAK)"
else
  bad "goroutines stable after soak (+$LEAK)"
fi

# --- scenario 5 (slow): exhausted budget surfaces the status -----------------
if [ "${RUN_SLOW:-0}" = "1" ]; then
  echo "== scenario 5: permanent 503 exhausts the always-retry budget =="
  start_mock --fail-first 9999 --fail-status 503
  CODE=$(curl -sS -o "$WORK/exh.json" -w '%{http_code}' --max-time 400 "$BASE/v1/chat/completions" \
    -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
    -d '{"model":"x","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}' || echo 000)
  if [ "$CODE" = "503" ] && grep -q '"error"' "$WORK/exh.json"; then
    ok "exhausted retries relayed as 503 in client dialect"
  else
    bad "exhausted retries relayed as 503 in client dialect (got HTTP $CODE)"
  fi
fi

echo
echo "chaos e2e: $PASS passed, $FAIL failed (workdir $WORK)"
[ "$FAIL" = "0" ]
