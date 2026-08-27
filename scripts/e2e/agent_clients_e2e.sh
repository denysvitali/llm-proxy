#!/usr/bin/env bash
# Exercise the real Claude Code and Codex CLIs through llm-proxy and OpenCode
# Zen's unauthenticated free tier. No provider credentials are used.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
WORK=${AGENT_E2E_WORK:-$(mktemp -d /tmp/llm-proxy-agents.XXXXXX)}
PORT=${AGENT_E2E_PORT:-18091}
BASE="http://127.0.0.1:$PORT"
PROXY_PID=""

cleanup() {
  [ -n "$PROXY_PID" ] && kill "$PROXY_PID" 2>/dev/null || true
  if [ "${KEEP_AGENT_E2E_WORK:-0}" != "1" ]; then
    rm -rf "$WORK"
  else
    echo "E2E artifacts kept in $WORK"
  fi
}
trap cleanup EXIT

for command in go curl jq claude codex; do
  command -v "$command" >/dev/null || {
    echo "required command not found: $command" >&2
    exit 1
  }
done

mkdir -p "$WORK/codex-home"
go -C "$ROOT" build -o "$WORK/llm-proxy" .
cat > "$WORK/config.yaml" <<EOF
server:
  listen: 127.0.0.1:$PORT
log_level: info
backends:
  - type: opencode
default_route:
  backend: opencode
EOF

"$WORK/llm-proxy" serve --config "$WORK/config.yaml" > "$WORK/proxy.log" 2>&1 &
PROXY_PID=$!
for _ in $(seq 1 100); do
  curl -fsS "$BASE/healthz" >/dev/null 2>&1 && break
  sleep 0.2
done
curl -fsS "$BASE/healthz" >/dev/null

# Select only a model explicitly advertised as free. A preferred model may be
# supplied for reproducibility, but it is rejected unless the live catalog
# still marks it free.
curl -fsS "$BASE/v1/models?backend=opencode" > "$WORK/models.json"
if [ -n "${OPENCODE_FREE_MODEL:-}" ]; then
  CANDIDATES=$(jq -r --arg id "opencode/$OPENCODE_FREE_MODEL" \
    '.data[] | select(.id == $id and (.id | endswith("-free"))) | .id' "$WORK/models.json")
else
  CANDIDATES=$(jq -r '.data[] | select(.id | endswith("-free")) | .id' "$WORK/models.json")
fi
if [ -z "$CANDIDATES" ]; then
  echo "OpenCode catalog contains no matching free model" >&2
  exit 1
fi

# Free preview models occasionally remain catalogued during a provider outage.
# Probe candidates in catalog order and use the first one currently serving.
MODEL=""
while IFS= read -r candidate; do
  body=$(jq -nc --arg model "$candidate" \
    '{model:$model,max_tokens:16,messages:[{role:"user",content:"Reply with exactly: E2E_OK"}]}')
  code=$(curl -sS --max-time 120 -o "$WORK/probe.json" -w '%{http_code}' \
    "$BASE/v1/chat/completions" -H "Content-Type: application/json" -d "$body" || true)
  if [ "$code" = "200" ] && grep -q "E2E_OK" "$WORK/probe.json"; then
    MODEL=$candidate
    break
  fi
  echo "Skipping unavailable free model $candidate (HTTP ${code:-000})"
done <<< "$CANDIDATES"
if [ -z "$MODEL" ]; then
  echo "No OpenCode free model completed the availability probe" >&2
  exit 1
fi
echo "Using OpenCode free model: $MODEL"

ANTHROPIC_BASE_URL="$BASE" \
ANTHROPIC_AUTH_TOKEN=local-proxy-token \
  claude --model "$MODEL" -p "Reply with exactly: E2E_OK" \
  | tee "$WORK/claude.out"
grep -q "E2E_OK" "$WORK/claude.out"

cat > "$WORK/codex-home/config.toml" <<EOF
model = "$MODEL"
model_provider = "llm_proxy"

[model_providers.llm_proxy]
name = "llm-proxy"
base_url = "$BASE/v1"
env_key = "CODEX_PROVIDER_KEY"
wire_api = "responses"
requires_openai_auth = false
EOF
CODEX_HOME="$WORK/codex-home" CODEX_PROVIDER_KEY=local-proxy-token \
  codex exec --skip-git-repo-check "Reply with exactly: E2E_OK" \
  | tee "$WORK/codex.out"
grep -q "E2E_OK" "$WORK/codex.out"

echo "Claude Code and Codex E2E passed with $MODEL"
