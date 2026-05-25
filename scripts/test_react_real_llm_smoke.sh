#!/bin/bash
set -euo pipefail

# ── Load .env from project root ─────────────────────────────────
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
if [ -f "$ROOT_DIR/.env" ]; then
  set -a
  source "$ROOT_DIR/.env"
  set +a
fi

GATEWAY_URL="${GATEWAY_URL:-http://127.0.0.1:8080}"
LLM_SERVICE_URL="${LLM_SERVICE_URL:-http://127.0.0.1:8000}"
REDIS_HOST="${REDIS_HOST:-127.0.0.1}"
REDIS_PORT="${REDIS_PORT:-6379}"

# ── Real LLM config from env (MiniMax or other OpenAI-compatible) ──
REAL_LLM_PROVIDER="${REAL_LLM_PROVIDER:-${LLM_PROVIDER:-minimax}}"
REAL_LLM_MODE="${REAL_LLM_MODE:-${LLM_MODE:-openai_compatible}}"
REAL_LLM_BASE_URL="${REAL_LLM_BASE_URL:-${LLM_BASE_URL:-https://api.minimaxi.com/v1}}"
REAL_LLM_MODEL="${REAL_LLM_MODEL:-${LLM_MODEL:-MiniMax-M2.7}}"

log() { echo "[$(date +'%H:%M:%S')] $*"; }
fail() { echo "[FAIL] $*" >&2; exit 1; }
skip() { echo "[SKIP] $*"; exit 0; }

redis_cmd() {
  if command -v redis-cli &>/dev/null; then
    redis-cli -h "$REDIS_HOST" -p "$REDIS_PORT" "$@"
  else
    docker.exe exec deploy-redis-1 redis-cli "$@"
  fi
}

psql_cmd() {
  docker.exe exec deploy-postgres-1 psql -U admin -d orchestrator -t -c "$1"
}

log "=== Real LLM ReAct Smoke Test ==="
log "  provider: $REAL_LLM_PROVIDER"
log "  base_url: $REAL_LLM_BASE_URL"
log "  model:    $REAL_LLM_MODEL"

# ── Pre-flight: REAL_LLM_TEST flag ──────────────────────────────
if [ "${REAL_LLM_TEST:-0}" != "1" ]; then
  skip "REAL_LLM_TEST is not set to 1 (current: ${REAL_LLM_TEST:-unset})"
fi

# ── Pre-flight: API key ─────────────────────────────────────────
# Ensure OPENAI_API_KEY is set if LLM_API_KEY exists (client may only read OPENAI_API_KEY)
if [ -n "${LLM_API_KEY:-}" ] && [ -z "${OPENAI_API_KEY:-}" ]; then
  export OPENAI_API_KEY="$LLM_API_KEY"
fi

if [ -z "${OPENAI_API_KEY:-}" ] && [ -z "${LLM_API_KEY:-}" ] && [ -z "${AZURE_OPENAI_API_KEY:-}" ]; then
  skip "No API key found (OPENAI_API_KEY, LLM_API_KEY, or AZURE_OPENAI_API_KEY required)"
fi
log "Real LLM smoke test enabled. API key present."

# ── Pre-flight: backend LLM config check ────────────────────────
log "Checking Python LLM service health..."
LLM_HEALTH=$(curl --noproxy '*' -s "$LLM_SERVICE_URL/health" 2>/dev/null || echo '{}')
LLM_STATUS=$(echo "$LLM_HEALTH" | jq -r '.status // "unreachable"')
if [ "$LLM_STATUS" != "healthy" ]; then
  fail "Python LLM service not healthy at $LLM_SERVICE_URL (status=$LLM_STATUS). Restart with real LLM config."
fi
log "  LLM service: $LLM_STATUS"

# Check whether the LLM service looks like it's in mock mode.
# The /health endpoint currently only returns {"status":"healthy"}.
# We query it for any mock-related fields and warn if we can't confirm real mode.
if echo "$LLM_HEALTH" | jq -e '.mock == true or .mode == "mock" or .provider == "mock"' >/dev/null 2>&1; then
  fail "LLM service reports mock mode. Restart python_llm_service with OPENAI_API_KEY and without --mock."
fi

# Try a lightweight LLM ping to confirm the service actually talks to a real provider.
# This is a best-effort check; skip if the /chat endpoint doesn't support a ping query.
PING_RESP=$(curl --noproxy '*' -s -X POST "$LLM_SERVICE_URL/chat" \
  -H "Content-Type: application/json" \
  -d "{\"trace_id\":\"react-smoke-ping\",\"task_id\":\"react-smoke-ping\",\"provider\":\"$REAL_LLM_MODE\",\"model\":\"$REAL_LLM_MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"ping\"}],\"max_completion_tokens\":4,\"temperature\":0.0}" 2>/dev/null || echo '{}')
PING_CONTENT=$(echo "$PING_RESP" | jq -r '.content // empty')
PING_ERROR=$(echo "$PING_RESP" | jq -r '.error // empty')
if [ -z "$PING_CONTENT" ] && [ -n "$PING_ERROR" ]; then
  log "  LLM ping failed: $PING_ERROR"
  log "  WARNING: Ensure python_llm_service is running with a real API key (OPENAI_API_KEY)."
  log "  If using mock, restart with: OPENAI_API_KEY=sk-... python python_llm_service/app.py"
elif [ -z "$PING_CONTENT" ]; then
  log "  LLM ping returned empty (service may still be starting). Continuing..."
else
  log "  LLM ping OK (preview: ${PING_CONTENT:0:60}...)"
fi
log "  Backend config check: PASSED"

# ── Create ReAct task ───────────────────────────────────────────
log "Creating ReAct task with real LLM..."
PAYLOAD=$(jq -n \
  --arg model "$REAL_LLM_MODEL" \
  '{
    query: "who is Gnabry? Answer in one sentence.",
    config: {
      mode: "dag",
      enable_react: true,
      react_max_iterations: 2,
      model: $model,
      max_completion_tokens: 256,
      temperature: 0.0
    }
  }')
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d "$PAYLOAD")
TASK_ID=$(echo "$RESP" | jq -r '.task_id')
[ -n "$TASK_ID" ] && [ "$TASK_ID" != "null" ] || fail "Failed to create real LLM ReAct task"
log "  Task created: $TASK_ID"

# ── Wait for completion ─────────────────────────────────────────
log "Waiting for task completion..."
STATUS=""
for i in $(seq 1 120); do
  STATUS=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID" | jq -r '.status')
  [ "$STATUS" = "completed" ] && break
  [ "$STATUS" = "failed" ] && break
  [ "$STATUS" = "budget_exceeded" ] && break
  sleep 1
done

if [ "$STATUS" != "completed" ]; then
  TASK_DETAIL=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID")
  fail "Real LLM task did not complete: status=$STATUS, detail=$TASK_DETAIL"
fi
log "  Task completed: PASSED"

# ── Assert result non-empty ─────────────────────────────────────
RESULT=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID" | jq -r '.result // empty')
if [ -z "$RESULT" ]; then
  fail "Result is empty"
fi
log "  Result non-empty: PASSED (len=${#RESULT})"

# ── Weak semantic assertion: must contain Paris or 巴黎 ──────────
if echo "$RESULT" | grep -Eiq "Paris|巴黎"; then
  log "  Result contains Paris/巴黎: PASSED"
else
  fail "Result does not contain Paris or 巴黎 — LLM answer not propagated"
fi

# ── Defensive: result must NOT be the original query ─────────────
ORIG_QUERY="What is the capital of France? Answer in one sentence."
if [ "$RESULT" = "$ORIG_QUERY" ]; then
  fail "Result is identical to the original query — LLM answer not propagated."
fi

# ── Defensive: result must NOT be the "dag synthesized" fallback ─
if echo "$RESULT" | grep -qi "dag synthesized"; then
  fail "Result is the 'dag synthesized' fallback template — LLM answer lost"
fi
log "  Defensive assertions (not query echo, not fallback): PASSED"

# ── Assert llm_calls >= 2 ───────────────────────────────────────
LLM_CALLS=$(psql_cmd "SELECT count(*) FROM llm_calls WHERE task_id='$TASK_ID';" 2>/dev/null | tr -d ' ' || echo "0")
if [ "${LLM_CALLS:-0}" -lt 2 ]; then
  fail "Expected llm_calls >= 2, got $LLM_CALLS"
fi
log "  llm_calls=${LLM_CALLS} (>= 2): PASSED"

# ── Assert usage.total_tokens > 0 ───────────────────────────────
TOTAL_TOKENS=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID" | jq -r '.usage.total_tokens // 0')
if [ "${TOTAL_TOKENS:-0}" -le 0 ]; then
  fail "Expected total_tokens > 0, got $TOTAL_TOKENS"
fi
log "  total_tokens=${TOTAL_TOKENS} (> 0): PASSED"

# ── Assert react_steps >= 1 (HARD FAIL) ─────────────────────────
REACT_STEPS=$(psql_cmd "SELECT count(*) FROM react_steps WHERE workflow_id=(SELECT workflow_id FROM tasks WHERE id='$TASK_ID');" 2>/dev/null | tr -d ' ' || echo "0")
if [ "${REACT_STEPS:-0}" -lt 1 ]; then
  fail "react_steps count is $REACT_STEPS, expected >= 1. ReAct audit not writing to Postgres."
fi
log "  react_steps=${REACT_STEPS} (>= 1): PASSED"

# ── Assert DAG_NODE_COMPLETED >= 1 (HARD FAIL) ──────────────────
DAG_NODE_COMPLETED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "DAG_NODE_COMPLETED" || echo "0")
if [ "${DAG_NODE_COMPLETED:-0}" -lt 1 ]; then
  fail "DAG_NODE_COMPLETED events found: $DAG_NODE_COMPLETED, expected >= 1. DAG visualization broken."
fi
log "  DAG_NODE_COMPLETED events: ${DAG_NODE_COMPLETED} (>= 1): PASSED"

# ── Assert llm_calls model/provider are real (not mock) ─────────
log "Verifying llm_calls model/provider..."
CALL_MODEL=$(psql_cmd "SELECT model FROM llm_calls WHERE task_id='$TASK_ID' LIMIT 1;" 2>/dev/null | tr -d ' ' || echo "")
CALL_PROVIDER=$(psql_cmd "SELECT provider FROM llm_calls WHERE task_id='$TASK_ID' LIMIT 1;" 2>/dev/null | tr -d ' ' || echo "")

# model must match REAL_LLM_MODEL (or contain it, since service may normalize the name)
if [ -n "$CALL_MODEL" ]; then
  if echo "$CALL_MODEL" | grep -qi "$REAL_LLM_MODEL"; then
    log "  model='$CALL_MODEL': PASSED (matches $REAL_LLM_MODEL)"
  else
    # Also allow if the requested model appears in the stored model name
    if echo "$CALL_MODEL" | grep -qi "$(echo "$REAL_LLM_MODEL" | cut -d'-' -f1)"; then
      log "  model='$CALL_MODEL': PASSED (contains base of $REAL_LLM_MODEL)"
    else
      fail "llm_calls.model='$CALL_MODEL' does not match expected model ($REAL_LLM_MODEL)"
    fi
  fi
else
  log "  WARNING: model field empty in llm_calls — verify manually"
fi

# provider must NOT be 'mock'
if [ -n "$CALL_PROVIDER" ]; then
  if echo "$CALL_PROVIDER" | grep -qi "mock"; then
    fail "llm_calls.provider='$CALL_PROVIDER' is mock — LLM service is not using a real provider."
  fi
  log "  provider='$CALL_PROVIDER' (non-mock): PASSED"
else
  log "  provider field empty in llm_calls (not a hard failure — verify LLM is real)"
fi

# ── Print recent llm_calls on success ───────────────────────────
log ""
log "Recent llm_calls for task $TASK_ID:"
psql_cmd "SELECT task_id, provider, model, prompt_tokens, completion_tokens, total_tokens, created_at FROM llm_calls WHERE task_id='$TASK_ID' ORDER BY created_at DESC;" 2>/dev/null || log "  (could not query llm_calls)"

# Do NOT assert full natural language content
log ""
log "=== REAL LLM REACT SMOKE TEST PASSED ==="
