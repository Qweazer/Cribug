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

log "=== DAG Skeleton Smoke Test Starting ==="

# Create a DAG mode task with simple query
log "1. Creating DAG mode task..."
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{
    "query": "hello world",
    "session_id": "sess-dag-skeleton",
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

# Wait for events to be fully written to Redis (race condition fix)
sleep 2

# 2. Verify result contains DAG completion
log "2. Checking result..."
RESULT=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID" | jq -r '.result')
# 4.6+: result contains mock answer from LLM node synthesis
if echo "$RESULT" | grep -qE "mock answer"; then
  log "  Result: $RESULT (DAG mode - mock answer - OK)"
elif echo "$RESULT" | grep -qE "dag.*executed"; then
  log "  Result: $RESULT (DAG mode - OK)"
else
  fail "Result does not contain mock answer or DAG message: $RESULT"
fi

# 3. Verify Redis events
log "3. Checking Redis events..."
WORKFLOW_STARTED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + | grep -c "WORKFLOW_STARTED" || true)
[ "$WORKFLOW_STARTED" -ge 1 ] || fail "Missing WORKFLOW_STARTED event"

TASK_COMPLETED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + | grep -c "TASK_COMPLETED" || true)
[ "$TASK_COMPLETED" -ge 1 ] || fail "Missing TASK_COMPLETED event"

SESSION_LOADED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + | grep -c "SESSION_LOADED" || true)
[ "$SESSION_LOADED" -ge 1 ] || fail "Missing SESSION_LOADED event"

log "  WORKFLOW_STARTED count: $WORKFLOW_STARTED"
log "  SESSION_LOADED count: $SESSION_LOADED"
log "  TASK_COMPLETED count: $TASK_COMPLETED"

# 4. Verify DAG events exist (4.5+: DAG has LLM node)
log "4. Checking DAG events..."
DAG_PLANNED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + | grep -c "DAG_PLANNED" || true)
[ "$DAG_PLANNED" -ge 1 ] || fail "Missing DAG_PLANNED event"
log "  DAG_PLANNED count: $DAG_PLANNED"

DAG_NODE_STARTED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + | grep -c "DAG_NODE_STARTED" || true)
[ "$DAG_NODE_STARTED" -ge 2 ] || fail "Expected at least 2 DAG_NODE_STARTED events"
log "  DAG_NODE_STARTED count: $DAG_NODE_STARTED"

DAG_NODE_COMPLETED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + | grep -c "DAG_NODE_COMPLETED" || true)
[ "$DAG_NODE_COMPLETED" -ge 2 ] || fail "Expected at least 2 DAG_NODE_COMPLETED events"
log "  DAG_NODE_COMPLETED count: $DAG_NODE_COMPLETED"

# 5. Verify LLM events exist (4.5+: DAG has one LLM node)
log "5. Checking LLM events (slice 4.5+: DAG has one LLM node)..."
LLM_STARTED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + | grep -c "LLM_STARTED" || true)
[ "$LLM_STARTED" -ge 1 ] || fail "Expected at least 1 LLM_STARTED event"
log "  LLM_STARTED count: $LLM_STARTED"

LLM_COMPLETED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + | grep -c "LLM_COMPLETED" || true)
[ "$LLM_COMPLETED" -ge 1 ] || fail "Expected at least 1 LLM_COMPLETED event"
log "  LLM_COMPLETED count: $LLM_COMPLETED"

# 6. Verify llm_calls records (one LLM call for draft_answer node)
log "6. Checking llm_calls..."
LLM_COUNT=$(psql_cmd "SELECT count(*) FROM llm_calls WHERE task_id='$TASK_ID';" | tr -d ' ')
[ "$LLM_COUNT" -ge 1 ] || fail "Expected at least 1 llm_calls record"
log "  llm_calls count: $LLM_COUNT"

echo ""
echo "=== ALL DAG SKELETON TESTS PASSED ==="
echo ""
echo "Summary:"
echo "  Task ID: $TASK_ID"
echo "  Status: $STATUS"
echo "  Result: $RESULT"
echo "  DAG_PLANNED: $DAG_PLANNED"
echo "  DAG_NODE_STARTED: $DAG_NODE_STARTED"
echo "  DAG_NODE_COMPLETED: $DAG_NODE_COMPLETED"
echo "  LLM_STARTED: $LLM_STARTED"
echo "  LLM_COMPLETED: $LLM_COMPLETED"
echo "  llm_calls: $LLM_COUNT"
