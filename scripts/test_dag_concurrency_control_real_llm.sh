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

log "=== DAG Concurrency Real LLM Smoke (Slice 14 hybrid) ==="

[ "${RUN_REAL_LLM_SMOKE:-0}" = "1" ] || skip "RUN_REAL_LLM_SMOKE not set to 1"
[ -n "${OPENAI_API_KEY:-}${LLM_API_KEY:-}" ] || skip "No API key found"
log "RUN_REAL_LLM_SMOKE=1, API key present, model=$REAL_LLM_MODEL"

# ── Create DAG task with max_parallel_agents=2 + delay + fail hook ──
log "Creating concurrency DAG task..."
PAYLOAD=$(jq -n --arg model "$REAL_LLM_MODEL" '{
  query: "Analyze pros and cons of Postgres vs Redis for caching",
  config: {
    mode: "dag",
    max_parallel_agents: 2,
    model: $model,
    max_completion_tokens: 512,
    temperature: 0.0,
    test_dag_node_delay_ms: 100,
    test_fail_node_id: "research",
    force_failed_after_llm: true
  }
}')
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" -H "Content-Type: application/json" -d "$PAYLOAD")
TID=$(echo "$RESP" | jq -r '.task_id')
WFID=$(echo "$RESP" | jq -r '.workflow_id')
[ -n "$TID" ] && [ "$TID" != "null" ] || fail "Failed"
log "  Task: $TID"

for i in $(seq 1 90); do
  S=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TID" | jq -r '.status')
  [ "$S" = "completed" ] && break; [ "$S" = "failed" ] && break; sleep 1
done
[ "$S" = "completed" ] || fail "Not completed: $S"
log "  Task completed: PASSED"

# ── Verify nodes ───────────────────────────────────────────────
NODES=$(redis_cmd HGETALL "dag:$WFID:nodes" 2>/dev/null || echo "")
echo "$NODES" | grep -q '"research".*"failed"' || fail "research not failed"
echo "$NODES" | grep -q '"analyze".*"completed"' || fail "analyze not completed"
echo "$NODES" | grep -q '"conclude".*"completed"' || fail "conclude not completed"
log "  Node states: PASSED"

# ── Events ─────────────────────────────────────────────────────
EVENTS=$(redis_cmd XRANGE "task:$TID:events" - + 2>/dev/null || echo "")
echo "$EVENTS" | grep -q "DAG_CONCURRENCY_LIMIT_APPLIED" || fail "DAG_CONCURRENCY_LIMIT_APPLIED missing"
echo "$EVENTS" | grep -q "DAG_NODE_FAILED" || fail "DAG_NODE_FAILED missing"
echo "$EVENTS" | grep -q "DAG_NODE_SKIPPED" || fail "DAG_NODE_SKIPPED missing"
log "  Events: PASSED"

# ── Result ─────────────────────────────────────────────────────
RESULT=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TID" | jq -r '.result')
[ -n "$RESULT" ] || fail "Result empty"
log "  Result non-empty: PASSED"

# ── Model ──────────────────────────────────────────────────────
MODEL=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TID" | jq -r '.model')
log "  Model=$MODEL: PASSED"

log ""
log "=== DAG CONCURRENCY REAL LLM SMOKE PASSED ==="
