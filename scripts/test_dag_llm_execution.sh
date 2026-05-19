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

log "=== DAG LLM Execution Smoke Test Starting ==="

# Create a DAG mode task with LLM node
log "1. Creating DAG LLM task..."
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{
    "query": "analyze this research problem and draft a final answer with llm",
    "session_id": "sess-dag-llm",
    "config": {
      "mode": "dag",
      "model": "gpt-4o-mini",
      "temperature": 0.7,
      "max_total_tokens": 16000,
      "max_completion_tokens": 128
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

# 2. Verify result contains LLM execution info
log "2. Checking result..."
RESULT=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID" | jq -r '.result')
echo "$RESULT" | grep -q "dag llm executed" || fail "Result does not contain 'dag llm executed': $RESULT"
echo "$RESULT" | grep -q "llm_nodes=1" || fail "Result does not contain 'llm_nodes=1': $RESULT"
log "  Result: $RESULT"

# 3. Verify Redis events for this task_id
log "3. Checking Redis events for task_id=$TASK_ID..."

# Get all events for this task
EVENTS=$(redis_cmd XRANGE "task:$TASK_ID:events" - +)
EVENT_COUNT=$(echo "$EVENTS" | wc -l)
log "  Total events: $EVENT_COUNT"

# Count DAG_NODE_STARTED
DAG_NODE_STARTED=$(echo "$EVENTS" | grep --line-buffered -c "DAG_NODE_STARTED" || true)
log "  DAG_NODE_STARTED count: $DAG_NODE_STARTED"
[ "$DAG_NODE_STARTED" -ge 2 ] || fail "Expected at least 2 DAG_NODE_STARTED (found $DAG_NODE_STARTED)"

# Count DAG_NODE_COMPLETED
DAG_NODE_COMPLETED=$(echo "$EVENTS" | grep --line-buffered -c "DAG_NODE_COMPLETED" || true)
log "  DAG_NODE_COMPLETED count: $DAG_NODE_COMPLETED"
[ "$DAG_NODE_COMPLETED" -ge 2 ] || fail "Expected at least 2 DAG_NODE_COMPLETED (found $DAG_NODE_COMPLETED)"

# Count LLM_STARTED
LLM_STARTED=$(echo "$EVENTS" | grep --line-buffered -c "LLM_STARTED" || true)
log "  LLM_STARTED count: $LLM_STARTED"
[ "$LLM_STARTED" -ge 1 ] || fail "Expected at least 1 LLM_STARTED (found $LLM_STARTED)"

# Count LLM_COMPLETED
LLM_COMPLETED=$(echo "$EVENTS" | grep --line-buffered -c "LLM_COMPLETED" || true)
log "  LLM_COMPLETED count: $LLM_COMPLETED"
[ "$LLM_COMPLETED" -ge 1 ] || fail "Expected at least 1 LLM_COMPLETED (found $LLM_COMPLETED)"

# Verify required non-LLM events
for event in WORKFLOW_STARTED SESSION_LOADED TASK_CLASSIFIED DAG_PLANNED USAGE_RECORDED TASK_COMPLETED; do
  COUNT=$(echo "$EVENTS" | grep --line-buffered -c "$event" || true)
  [ "$COUNT" -ge 1 ] || fail "Missing required event: $event (count=$COUNT)"
  log "  $event count: $COUNT"
done

# 4. Verify llm_calls records
log "4. Checking llm_calls for task_id=$TASK_ID..."
LLM_COUNT=$(psql_cmd "SELECT count(*) FROM llm_calls WHERE task_id='$TASK_ID';" | tr -d ' ')
log "  llm_calls total count: $LLM_COUNT"
[ "$LLM_COUNT" -ge 1 ] || fail "Expected at least 1 llm_calls record (found $LLM_COUNT)"

# Check node_id='draft_answer' has 1 record
DRAFT_LLM=$(psql_cmd "SELECT count(*) FROM llm_calls WHERE task_id='$TASK_ID' AND node_id='draft_answer';" | tr -d ' ')
log "  llm_calls with node_id=draft_answer: $DRAFT_LLM"
[ "$DRAFT_LLM" -ge 1 ] || fail "Expected at least 1 llm_calls for draft_answer (found $DRAFT_LLM)"

# Check analyze_input has 0 records
ANALYZE_LLM=$(psql_cmd "SELECT count(*) FROM llm_calls WHERE task_id='$TASK_ID' AND node_id='analyze_input';" | tr -d ' ')
log "  llm_calls with node_id=analyze_input: $ANALYZE_LLM"
[ "$ANALYZE_LLM" = "0" ] || fail "Expected 0 llm_calls for analyze_input (found $ANALYZE_LLM)"

echo ""
echo "=== ALL DAG LLM EXECUTION TESTS PASSED ==="
echo ""
echo "Summary:"
echo "  Task ID: $TASK_ID"
echo "  Status: $STATUS"
echo "  Result: $RESULT"
echo "  DAG_NODE_STARTED: $DAG_NODE_STARTED"
echo "  DAG_NODE_COMPLETED: $DAG_NODE_COMPLETED"
echo "  LLM_STARTED: $LLM_STARTED"
echo "  LLM_COMPLETED: $LLM_COMPLETED"
echo "  llm_calls total: $LLM_COUNT"
echo "  llm_calls draft_answer: $DRAFT_LLM"
echo "  llm_calls analyze_input: $ANALYZE_LLM"