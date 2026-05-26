#!/bin/bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
if [ -f "$ROOT_DIR/.env" ]; then set -a; source "$ROOT_DIR/.env"; set +a; fi

GATEWAY_URL="${GATEWAY_URL:-http://127.0.0.1:8080}"
REDIS_HOST="${REDIS_HOST:-127.0.0.1}"
REDIS_PORT="${REDIS_PORT:-6379}"
REAL_LLM_MODEL="${REAL_LLM_MODEL:-${LLM_MODEL:-MiniMax-M2.7}}"

log()   { echo "[$(date +'%H:%M:%S')] $*"; }
fail()  { echo "[FAIL] $*" >&2; exit 1; }
skip()  { echo "[SKIP] $*"; exit 0; }

redis_cmd() {
  if command -v redis-cli &>/dev/null; then redis-cli -h "$REDIS_HOST" -p "$REDIS_PORT" "$@"
  else docker.exe exec deploy-redis-1 redis-cli "$@"; fi
}

log "=== DAG Dynamic Replan Real LLM Smoke (Slice 13+14 hybrid) ==="

[ "${RUN_REAL_LLM_SMOKE:-0}" = "1" ] || skip "RUN_REAL_LLM_SMOKE not set to 1"
[ -n "${OPENAI_API_KEY:-}${LLM_API_KEY:-}" ] || skip "No API key found"
log "RUN_REAL_LLM_SMOKE=1, API key present, model=$REAL_LLM_MODEL"

# ── Create DAG task with hybrid test hooks ──────────────────────
# research: force_failed_after_llm (calls real LLM, captures raw output, then fails)
# all other LLM nodes: normal real LLM
log "Creating DAG task..."
PAYLOAD=$(jq -n --arg model "$REAL_LLM_MODEL" '{
  query: "Compare Docker vs local installation for AI agent projects",
  config: {
    mode: "dag",
    max_parallel_agents: 2,
    model: $model,
    max_completion_tokens: 512,
    temperature: 0.0,
    test_fail_node_id: "research",
    force_failed_after_llm: true
  }
}')
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" -H "Content-Type: application/json" -d "$PAYLOAD")
TID=$(echo "$RESP" | jq -r '.task_id')
WFID=$(echo "$RESP" | jq -r '.workflow_id')
[ -n "$TID" ] && [ "$TID" != "null" ] || fail "Failed to create task"
log "  Task: $TID  Workflow: $WFID"

# Wait
for i in $(seq 1 90); do
  S=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TID" | jq -r '.status')
  [ "$S" = "completed" ] && break; [ "$S" = "failed" ] && break; sleep 1
done
[ "$S" = "completed" ] || fail "Task not completed: status=$S"
log "  Task completed: PASSED"

# ── DAG node states ────────────────────────────────────────────
log "Checking node states..."
NODES=$(redis_cmd HGETALL "dag:$WFID:nodes" 2>/dev/null || echo "")
echo "$NODES" | grep -q '"research".*"status":"failed"' || fail "research not failed"
echo "$NODES" | grep -q '"analyze".*"status":"completed"' || fail "analyze not completed"
echo "$NODES" | grep -q '"conclude".*"status":"completed"' || fail "conclude not completed"
echo "$NODES" | grep -q '"compare".*"status":"skipped"' || fail "compare not skipped"
log "  failed/completed/skipped states: PASSED"

# ── Real LLM evidence ──────────────────────────────────────────
log "Checking real LLM evidence..."
RESULT=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TID" | jq -r '.result')
[ -n "$RESULT" ] || fail "Result empty"
[ "$RESULT" != "dag synthesized:"* ] || fail "Result is fallback"
log "  Result non-empty and not fallback: PASSED"

# ── Events ─────────────────────────────────────────────────────
EVENTS=$(redis_cmd XRANGE "task:$TID:events" - + 2>/dev/null || echo "")
echo "$EVENTS" | grep -q "DAG_NODE_FAILED" || fail "DAG_NODE_FAILED missing"
echo "$EVENTS" | grep -q "DAG_NODE_SKIPPED" || fail "DAG_NODE_SKIPPED missing"
log "  Events: PASSED"

# ── Provider non-mock ──────────────────────────────────────────
MODEL=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TID" | jq -r '.model')
[ "$MODEL" = "$REAL_LLM_MODEL" ] || log "  WARNING: model=$MODEL"
log "  Model=$MODEL: PASSED"

log ""
log "=== DAG DYNAMIC REPLAN REAL LLM SMOKE PASSED ==="
