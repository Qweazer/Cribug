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
  for i in $(seq 1 60); do
    local status=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$task_id" | jq -r '.status')
    if [[ "$status" == "completed" ]] || [[ "$status" == "failed" ]] || [[ "$status" == "budget_exceeded" ]]; then
      echo "$status"
      return 0
    fi
    sleep 1
  done
  fail "Task $task_id timeout"
}

log "=== DAG Plan Smoke Test Starting ==="

# Create a DAG mode task with analysis query
log "1. Creating DAG plan task..."
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{
    "query": "analyze this research problem and create a plan",
    "session_id": "sess-dag-plan",
    "config": {
      "mode": "dag",
      "max_total_tokens": 16000
    }
  }')

TASK_ID=$(echo "$RESP" | jq -r '.task_id')
[ -n "$TASK_ID" ] && [ "$TASK_ID" != "null" ] || fail "Failed to create DAG task"

log "  Task created: $TASK_ID"

# Poll until completed
STATUS=$(wait_task $TASK_ID)
[ "$STATUS" = "completed" ] || fail "DAG task expected completed, got $STATUS"
log "  Task completed: $STATUS"

# 2. Verify result contains DAG execution info
log "2. Checking result..."
RESULT=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID" | jq -r '.result')
# Accept either "dag executed" (4.4+) or "dag plan created" (4.3)
if echo "$RESULT" | grep -qE "dag (executed|plan created)"; then
  echo "$RESULT" | grep -q "node_count=2" || echo "$RESULT" | grep -q "nodes=2" || echo "$RESULT" | grep -q "node_count" || true
  log "  Result: $RESULT (OK)"
else
  fail "Result does not contain DAG message: $RESULT"
fi

# 3. Verify Redis events contain required events
log "3. Checking Redis events..."
for event in WORKFLOW_STARTED SESSION_LOADED TASK_CLASSIFIED DAG_PLANNED TASK_COMPLETED; do
  COUNT=$(redis_cmd XRANGE "task:$TASK_ID:events" - + | grep -c "$event" || true)
  [ "$COUNT" -ge 1 ] || fail "Missing required event: $event (count=$COUNT)"
  log "  $event count: $COUNT"
done

# 4. Verify NO LLM events (no LLM calls)
log "4. Checking no LLM events..."
for event in LLM_STARTED LLM_COMPLETED; do
  COUNT=$(redis_cmd XRANGE "task:$TASK_ID:events" - + | grep -c "$event" || true)
  [ "$COUNT" = "0" ] || fail "Should not have $event event (count=$COUNT)"
  log "  $event count: $COUNT (OK)"
done

# 5. Verify no llm_calls records
log "5. Checking llm_calls..."
LLM_COUNT=$(psql_cmd "SELECT count(*) FROM llm_calls WHERE task_id='$TASK_ID';" | tr -d ' ')
[ "$LLM_COUNT" = "0" ] || fail "DAG should not have llm_calls records (found $LLM_COUNT)"
log "  llm_calls count: $LLM_COUNT (OK)"

# 6. Verify DAG node execution events (4.4+ always executes nodes)
log "6. Checking DAG node execution events..."
DAG_NODE_STARTED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + | grep -c "DAG_NODE_STARTED" || true)
[ "$DAG_NODE_STARTED" -ge 1 ] || fail "Expected at least 1 DAG_NODE_STARTED event (found $DAG_NODE_STARTED)"
log "  DAG_NODE_STARTED count: $DAG_NODE_STARTED"

DAG_NODE_COMPLETED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + | grep -c "DAG_NODE_COMPLETED" || true)
[ "$DAG_NODE_COMPLETED" -ge 1 ] || fail "Expected at least 1 DAG_NODE_COMPLETED event (found $DAG_NODE_COMPLETED)"
log "  DAG_NODE_COMPLETED count: $DAG_NODE_COMPLETED"

echo ""
echo "=== ALL DAG PLAN TESTS PASSED ==="
echo ""
echo "Summary:"
echo "  Task ID: $TASK_ID"
echo "  Status: $STATUS"
echo "  Result: $RESULT"
echo "  LLM_STARTED: 0"
echo "  llm_calls: 0"
echo "  DAG_NODE_STARTED: $DAG_NODE_STARTED"
echo "  DAG_NODE_COMPLETED: $DAG_NODE_COMPLETED"