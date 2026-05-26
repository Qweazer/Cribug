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

log "=== Phase 4 Real LLM E2E Smoke (Slice 15) ==="

[ "${RUN_REAL_LLM_SMOKE:-0}" = "1" ] || skip "RUN_REAL_LLM_SMOKE not set to 1"
[ -n "${OPENAI_API_KEY:-}${LLM_API_KEY:-}" ] || skip "No API key found"
log "RUN_REAL_LLM_SMOKE=1 model=$REAL_LLM_MODEL"

# ── Case 1: Simple Real LLM DAG ─────────────────────────────────
log "Case 1: Simple real LLM DAG"
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d "{\"query\":\"What is 3+4? Answer briefly.\",\"config\":{\"mode\":\"dag\",\"max_parallel_agents\":2,\"model\":\"$REAL_LLM_MODEL\",\"max_total_tokens\":8000,\"max_completion_tokens\":256,\"temperature\":0.0}}")
TID=$(echo "$RESP" | jq -r '.task_id'); WF="task-$TID"
for i in $(seq 1 60); do S=$(curl --noproxy '*' -s "http://127.0.0.1:8080/api/v1/tasks/$TID" | jq -r '.status'); [ "$S" = "completed" ] && break; sleep 1; done
[ "$S" = "completed" ] || fail "Case1: status=$S"
RESULT=$(curl --noproxy '*' -s "http://127.0.0.1:8080/api/v1/tasks/$TID" | jq -r '.result')
[ -n "$RESULT" ] || fail "Case1: result empty"
EVENTS=$(redis_cmd XRANGE "task:$TID:events" - + 2>/dev/null || echo "")
echo "$EVENTS" | grep -q "DAG_PLANNED" || fail "Case1: DAG_PLANNED missing"
echo "$EVENTS" | grep -q "DAG_NODE_COMPLETED" || fail "Case1: DAG_NODE_COMPLETED missing"
MODEL=$(curl --noproxy '*' -s "http://127.0.0.1:8080/api/v1/tasks/$TID" | jq -r '.model')
log "  Case1 PASSED (model=$MODEL)"

# ── Case 2: force_failed_after_llm isolation ────────────────────
log "Case 2: Real LLM + force_failed_after_llm isolation"
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d "{\"query\":\"Compare Docker vs local install for AI projects\",\"config\":{\"mode\":\"dag\",\"max_parallel_agents\":2,\"model\":\"$REAL_LLM_MODEL\",\"max_completion_tokens\":256,\"temperature\":0.0,\"test_fail_node_id\":\"research\",\"force_failed_after_llm\":true}}")
TID=$(echo "$RESP" | jq -r '.task_id'); WF="task-$TID"
for i in $(seq 1 90); do S=$(curl --noproxy '*' -s "http://127.0.0.1:8080/api/v1/tasks/$TID" | jq -r '.status'); [ "$S" = "completed" ] && break; sleep 1; done
[ "$S" = "completed" ] || fail "Case2: status=$S"
NODES=$(redis_cmd HGETALL "dag:$WF:nodes" 2>/dev/null || echo "")
echo "$NODES" | grep -q '"research".*"failed"' || fail "Case2: research not failed"
echo "$NODES" | grep -q '"analyze".*"completed"' || fail "Case2: analyze not completed"
echo "$NODES" | grep -q '"conclude".*"completed"' || fail "Case2: conclude not completed"
echo "$NODES" | grep -q '"compare".*"skipped"' || fail "Case2: compare not skipped"
EVENTS=$(redis_cmd XRANGE "task:$TID:events" - + 2>/dev/null || echo "")
echo "$EVENTS" | grep -q "DAG_NODE_FAILED" || fail "Case2: DAG_NODE_FAILED missing"
echo "$EVENTS" | grep -q "DAG_NODE_SKIPPED" || fail "Case2: DAG_NODE_SKIPPED missing"
log "  Case2 PASSED"

# ── Case 3: Real LLM + concurrency cap ──────────────────────────
log "Case 3: Real LLM + concurrency cap"
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d "{\"query\":\"Analyze pros and cons of Postgres vs Redis\",\"config\":{\"mode\":\"dag\",\"max_parallel_agents\":2,\"model\":\"$REAL_LLM_MODEL\",\"max_completion_tokens\":256,\"temperature\":0.0,\"test_dag_node_delay_ms\":100}}")
TID=$(echo "$RESP" | jq -r '.task_id'); WF="task-$TID"
for i in $(seq 1 90); do S=$(curl --noproxy '*' -s "http://127.0.0.1:8080/api/v1/tasks/$TID" | jq -r '.status'); [ "$S" = "completed" ] && break; sleep 1; done
[ "$S" = "completed" ] || fail "Case3: status=$S"
EVENTS=$(redis_cmd XRANGE "task:$TID:events" - + 2>/dev/null || echo "")
echo "$EVENTS" | grep -q "DAG_CONCURRENCY_LIMIT_APPLIED" || fail "Case3: DAG_CONCURRENCY_LIMIT_APPLIED missing"
RESULT=$(curl --noproxy '*' -s "http://127.0.0.1:8080/api/v1/tasks/$TID" | jq -r '.result')
[ -n "$RESULT" ] || fail "Case3: result empty"
log "  Case3 PASSED"

log ""
log "=== PHASE 4 REAL LLM E2E SMOKE PASSED ==="
