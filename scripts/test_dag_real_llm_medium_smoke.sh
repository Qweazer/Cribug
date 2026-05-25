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

# ── Real LLM config from env ────────────────────────────────────
REAL_LLM_PROVIDER="${REAL_LLM_PROVIDER:-${LLM_PROVIDER:-minimax}}"
REAL_LLM_MODE="${REAL_LLM_MODE:-${LLM_MODE:-openai_compatible}}"
REAL_LLM_MODEL="${REAL_LLM_MODEL:-${LLM_MODEL:-MiniMax-M2.7}}"

log()   { echo "[$(date +'%H:%M:%S')] $*"; }
fail()  { echo "[FAIL] $*" >&2; exit 1; }
skip()  { echo "[SKIP] $*"; exit 0; }

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

log "=== DAG Medium Real LLM Smoke Test ==="
log "  model: $REAL_LLM_MODEL"

# ── Pre-flight: REAL_LLM_TEST ───────────────────────────────────
if [ "${REAL_LLM_TEST:-0}" != "1" ]; then
  skip "REAL_LLM_TEST is not set to 1"
fi

# ── Pre-flight: API key ─────────────────────────────────────────
if [ -n "${LLM_API_KEY:-}" ] && [ -z "${OPENAI_API_KEY:-}" ]; then
  export OPENAI_API_KEY="$LLM_API_KEY"
fi
if [ -z "${OPENAI_API_KEY:-}" ] && [ -z "${LLM_API_KEY:-}" ]; then
  skip "No API key found"
fi
log "API key present."

# ── Pre-flight: LLM service health ──────────────────────────────
LLM_HEALTH=$(curl --noproxy '*' -s "$LLM_SERVICE_URL/health" 2>/dev/null || echo '{}')
LLM_STATUS=$(echo "$LLM_HEALTH" | jq -r '.status // "unreachable"')
if [ "$LLM_STATUS" != "healthy" ]; then
  fail "LLM service not healthy at $LLM_SERVICE_URL (status=$LLM_STATUS)"
fi
if echo "$LLM_HEALTH" | jq -e '.mode == "mock"' >/dev/null 2>&1; then
  fail "LLM service is in mock mode"
fi
log "LLM service: $LLM_STATUS (mode=$(echo "$LLM_HEALTH" | jq -r '.mode // "?"'))"

# ── Create DAG task (no ReAct) ──────────────────────────────────
QUERY="比较在本地 AI Agent 项目中，把 Postgres、Redis、Temporal 放在 Docker 中运行，和直接在本机独立安装运行的区别。请从开发便利性、环境隔离、数据持久化、性能、调试维护、团队协作这 6 个角度分析，并给出推荐方案。"

log "Creating DAG task (enable_react=false)..."
PAYLOAD=$(jq -n \
  --arg model "$REAL_LLM_MODEL" \
  --arg query "$QUERY" \
  '{
    query: $query,
    config: {
      mode: "dag",
      enable_react: false,
      model: $model,
      max_total_tokens: 12000,
      max_completion_tokens: 1200,
      temperature: 0.0
    }
  }')
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d "$PAYLOAD")
TASK_ID=$(echo "$RESP" | jq -r '.task_id')
WF_ID=$(echo "$RESP" | jq -r '.workflow_id // "?"')
[ -n "$TASK_ID" ] && [ "$TASK_ID" != "null" ] || fail "Failed to create task"
log "  Task: $TASK_ID  Workflow: $WF_ID"

# ── Wait for completion ─────────────────────────────────────────
log "Waiting for task completion..."
STATUS=""
for i in $(seq 1 180); do
  STATUS=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID" | jq -r '.status')
  [ "$STATUS" = "completed" ] && break
  [ "$STATUS" = "failed" ] && break
  [ "$STATUS" = "budget_exceeded" ] && break
  sleep 1
done
[ "$STATUS" = "completed" ] || fail "Task not completed: status=$STATUS"
log "  Task completed: PASSED"

# ── Run ID ──────────────────────────────────────────────────────
RUN_ID=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID" | jq -r '.run_id // "?"')
log "  run_id: $RUN_ID"

# ── Result non-empty ────────────────────────────────────────────
RESULT=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID" | jq -r '.result // empty')
[ -n "$RESULT" ] || fail "Result is empty"
log "  Result non-empty: PASSED (len=${#RESULT})"

# ── Semantic assertions ─────────────────────────────────────────
MISSING=""
for term in Docker 隔离 性能 维护 推荐; do
  if ! echo "$RESULT" | grep -qi "$term"; then
    MISSING="$MISSING $term"
  fi
done
FOUND=$((5 - $(echo "$MISSING" | wc -w)))
if [ "$FOUND" -lt 3 ]; then
  fail "Only $FOUND/5 keywords found in result. Missing:$MISSING"
fi
log "  Keywords found: $FOUND/5 (Docker,隔离,性能,维护,推荐): PASSED"

# ── Defensive: not query echo ───────────────────────────────────
if [ "$RESULT" = "$QUERY" ]; then
  fail "Result is the original query — LLM answer not propagated"
fi

# ── Defensive: not fallback template ────────────────────────────
if echo "$RESULT" | grep -qi "dag synthesized"; then
  fail "Result is the 'dag synthesized' fallback — LLM answer lost"
fi
log "  Defensive assertions: PASSED"

# ── llm_calls ───────────────────────────────────────────────────
LLM_CALLS=$(psql_cmd "SELECT count(*) FROM llm_calls WHERE task_id='$TASK_ID';" 2>/dev/null | tr -d ' ' || echo "0")
[ "${LLM_CALLS:-0}" -ge 2 ] || fail "llm_calls=$LLM_CALLS, expected >= 2"
log "  llm_calls=$LLM_CALLS (>= 2): PASSED"

# ── total_tokens ────────────────────────────────────────────────
TOTAL_TOKENS=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID" | jq -r '.usage.total_tokens // 0')
[ "${TOTAL_TOKENS:-0}" -gt 0 ] || fail "total_tokens=0"
log "  total_tokens=$TOTAL_TOKENS (> 0): PASSED"

# ── DAG_NODE_COMPLETED ──────────────────────────────────────────
DAG_DONE=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "DAG_NODE_COMPLETED" || echo "0")
[ "${DAG_DONE:-0}" -ge 1 ] || fail "DAG_NODE_COMPLETED=$DAG_DONE, expected >= 1"
log "  DAG_NODE_COMPLETED=$DAG_DONE (>= 1): PASSED"

# ── provider / model ────────────────────────────────────────────
CALL_PROVIDER=$(psql_cmd "SELECT provider FROM llm_calls WHERE task_id='$TASK_ID' LIMIT 1;" 2>/dev/null | tr -d ' ' || echo "")
CALL_MODEL=$(psql_cmd "SELECT model FROM llm_calls WHERE task_id='$TASK_ID' LIMIT 1;" 2>/dev/null | tr -d ' ' || echo "")
if echo "$CALL_PROVIDER" | grep -qi "mock"; then
  fail "provider=$CALL_PROVIDER is mock"
fi
if [ -n "$CALL_MODEL" ] && ! echo "$CALL_MODEL" | grep -qi "$REAL_LLM_MODEL"; then
  log "  WARNING: model=$CALL_MODEL does not contain $REAL_LLM_MODEL"
else
  log "  model=$CALL_MODEL: PASSED"
fi
log "  provider=$CALL_PROVIDER (non-mock): PASSED"

# ── llm_calls summary ───────────────────────────────────────────
log ""
log "  Recent llm_calls:"
psql_cmd "SELECT provider, model, prompt_tokens, completion_tokens, total_tokens, created_at FROM llm_calls WHERE task_id='$TASK_ID' ORDER BY created_at DESC;" 2>/dev/null

log ""
log "=== DAG MEDIUM REAL LLM SMOKE TEST PASSED ==="
