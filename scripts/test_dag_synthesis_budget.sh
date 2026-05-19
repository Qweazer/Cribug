#!/bin/bash
set -euo pipefail

GATEWAY_URL="${GATEWAY_URL:-http://127.0.0.1:8080}"
REDIS_HOST="${REDIS_HOST:-127.0.0.1}"
REDIS_PORT="${REDIS_PORT:-6379}"

log() { echo "[$(date +'%H:%M:%S')] $*"; }
fail() { echo "[FAIL] $*" >&2; exit 1; }

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

wait_task() {
  local task_id=$1
  for i in $(seq 1 30); do
    local status=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$task_id" | jq -r '.status')
    if [[ "$status" == "completed" ]] || [[ "$status" == "failed" ]] || [[ "$status" == "budget_exceeded" ]]; then
      echo "$status"
      return 0
    fi
    sleep 1
  done
  fail "Task $task_id timeout"
}

log "=== DAG Synthesis Budget Smoke Test Starting ==="

log "1. Creating DAG synthesis budget task..."
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{
    "query": "analyze and synthesize",
    "session_id": "sess-dag-synthesis-budget",
    "config": {
      "mode": "dag",
      "model": "gpt-4o-mini",
      "temperature": 0.7,
      "max_total_tokens": 1,
      "max_completion_tokens": 1
    }
  }')

TASK_ID=$(echo "$RESP" | jq -r '.task_id')
[ -n "$TASK_ID" ] && [ "$TASK_ID" != "null" ] || fail "Failed to create DAG task"

log "  Task created: $TASK_ID"

STATUS=$(wait_task $TASK_ID)
log "  Task status: $STATUS"

[[ "$STATUS" == "budget_exceeded" ]] || [[ "$STATUS" == "failed" ]] || fail "Expected budget_exceeded or failed, got $STATUS"

log "2. Checking error type..."
ERROR_TYPE=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID" | jq -r '.error_type')
log "  Error type: $ERROR_TYPE"

log "3. Checking Redis events for task_id=$TASK_ID..."
EVENTS=$(redis_cmd XRANGE "task:$TASK_ID:events" - +)

LLM_STARTED=$(echo "$EVENTS" | grep -c "LLM_STARTED" || true)
log "  LLM_STARTED count: $LLM_STARTED"
[ "$LLM_STARTED" = "0" ] || fail "Should not have LLM_STARTED for budget exceeded (found $LLM_STARTED)"

LLM_COMPLETED=$(echo "$EVENTS" | grep -c "LLM_COMPLETED" || true)
log "  LLM_COMPLETED count: $LLM_COMPLETED"
[ "$LLM_COMPLETED" = "0" ] || fail "Should not have LLM_COMPLETED for budget exceeded (found $LLM_COMPLETED)"

DAG_SYNTHESIZED=$(echo "$EVENTS" | grep -c "DAG_SYNTHESIZED" || true)
log "  DAG_SYNTHESIZED count: $DAG_SYNTHESIZED"
[ "$DAG_SYNTHESIZED" = "0" ] || fail "Should not have DAG_SYNTHESIZED for budget exceeded (found $DAG_SYNTHESIZED)"

TASK_BUDGET=$(echo "$EVENTS" | grep -cE "TASK_BUDGET_EXCEEDED|BUDGET_EXCEEDED" || true)
log "  Budget-related events: $TASK_BUDGET"
[ "$TASK_BUDGET" -ge 1 ] || fail "Expected budget-related event (found $TASK_BUDGET)"

log "4. Checking llm_calls..."
LLM_COUNT=$(psql_cmd "SELECT count(*) FROM llm_calls WHERE task_id='$TASK_ID';" | tr -d ' ')
log "  llm_calls count: $LLM_COUNT"
[ "$LLM_COUNT" = "0" ] || fail "Should not have llm_calls for budget exceeded (found $LLM_COUNT)"

echo ""
echo "=== ALL DAG SYNTHESIS BUDGET TESTS PASSED ==="
echo ""
echo "Summary:"
echo "  Task ID: $TASK_ID"
echo "  Status: $STATUS"
echo "  Error type: $ERROR_TYPE"
echo "  LLM_STARTED: $LLM_STARTED"
echo "  LLM_COMPLETED: $LLM_COMPLETED"
echo "  DAG_SYNTHESIZED: $DAG_SYNTHESIZED"
echo "  llm_calls: $LLM_COUNT"