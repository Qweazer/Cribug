#!/bin/bash
set -euo pipefail

GATEWAY_URL="${GATEWAY_URL:-http://127.0.0.1:8080}"
REDIS_HOST="${REDIS_HOST:-127.0.0.1}"
REDIS_PORT="${REDIS_PORT:-6379}"

log() { echo "[$(date +'%H:%M:%S')] $*"; }
fail() { echo "[FAIL] $*" >&2; exit 1; }

# Helper to run redis-cli
redis_cmd() {
  if command -v redis-cli &>/dev/null; then
    redis-cli -h "$REDIS_HOST" -p "$REDIS_PORT" "$@"
  else
    docker.exe exec deploy-redis-1 redis-cli "$@"
  fi
}

# Helper to run psql
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

log "=== DAG LLM Budget Smoke Test Starting ==="

# Create a DAG mode task with very low budget
log "1. Creating DAG budget task..."
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{
    "query": "analyze this with llm",
    "session_id": "sess-dag-budget",
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

# Poll until terminal
STATUS=$(wait_task $TASK_ID)
log "  Task status: $STATUS"

# 2. Verify status is budget_exceeded or failed with budget error
log "2. Checking status..."
[[ "$STATUS" == "budget_exceeded" ]] || [[ "$STATUS" == "failed" ]] || fail "Expected budget_exceeded or failed, got $STATUS"

# 3. Get error type
ERROR_TYPE=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID" | jq -r '.error_type')
log "  Error type: $ERROR_TYPE"

# 4. Verify Redis events for this task_id
log "4. Checking Redis events for task_id=$TASK_ID..."
EVENTS=$(redis_cmd XRANGE "task:$TASK_ID:events" - +)

# Should NOT have LLM_STARTED for budget exceeded
LLM_STARTED=$(echo "$EVENTS" | grep -c "LLM_STARTED" || true)
log "  LLM_STARTED count: $LLM_STARTED"
[ "$LLM_STARTED" = "0" ] || fail "Should not have LLM_STARTED for budget exceeded (found $LLM_STARTED)"

# Should NOT have LLM_COMPLETED for budget exceeded
LLM_COMPLETED=$(echo "$EVENTS" | grep -c "LLM_COMPLETED" || true)
log "  LLM_COMPLETED count: $LLM_COMPLETED"
[ "$LLM_COMPLETED" = "0" ] || fail "Should not have LLM_COMPLETED for budget exceeded (found $LLM_COMPLETED)"

# Should have TASK_BUDGET_EXCEEDED or similar budget event
BUDGET_EVENTS=$(echo "$EVENTS" | grep -cE "TASK_BUDGET_EXCEEDED|BUDGET_EXCEEDED|budget" || true)
log "  Budget-related events: $BUDGET_EVENTS"
[ "$BUDGET_EVENTS" -ge 1 ] || fail "Expected budget-related event (found $BUDGET_EVENTS)"

# 5. Verify no llm_calls records
log "5. Checking llm_calls for task_id=$TASK_ID..."
LLM_COUNT=$(psql_cmd "SELECT count(*) FROM llm_calls WHERE task_id='$TASK_ID';" | tr -d ' ')
log "  llm_calls count: $LLM_COUNT"
[ "$LLM_COUNT" = "0" ] || fail "Should not have llm_calls for budget exceeded (found $LLM_COUNT)"

echo ""
echo "=== ALL DAG LLM BUDGET TESTS PASSED ==="
echo ""
echo "Summary:"
echo "  Task ID: $TASK_ID"
echo "  Status: $STATUS"
echo "  Error type: $ERROR_TYPE"
echo "  LLM_STARTED: $LLM_STARTED"
echo "  LLM_COMPLETED: $LLM_COMPLETED"
echo "  llm_calls: $LLM_COUNT"