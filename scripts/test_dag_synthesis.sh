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

log "=== DAG Synthesis Smoke Test Starting ==="

log "1. Creating DAG synthesis task..."
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{
    "query": "analyze this research problem and synthesize a final answer",
    "session_id": "sess-dag-synthesis",
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

STATUS=$(wait_task $TASK_ID)
[ "$STATUS" = "completed" ] || fail "DAG task expected completed, got $STATUS"
log "  Task completed: $STATUS"

sleep 2

log "2. Checking result..."
RESULT=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID" | jq -r '.result')
echo "$RESULT" | grep -q "mock answer" || fail "Result does not contain LLM output: $RESULT"
log "  Result: $RESULT"

log "3. Checking Redis events for task_id=$TASK_ID..."
EVENTS=$(redis_cmd XRANGE "task:$TASK_ID:events" - +)
EVENT_COUNT=$(echo "$EVENTS" | wc -l)
log "  Total events: $EVENT_COUNT"

# Count required events
DAG_NODE_STARTED=$(echo "$EVENTS" | grep --line-buffered -c "DAG_NODE_STARTED" || true)
[ "$DAG_NODE_STARTED" -ge 2 ] || fail "Expected at least 2 DAG_NODE_STARTED (found $DAG_NODE_STARTED)"
log "  DAG_NODE_STARTED count: $DAG_NODE_STARTED"

DAG_NODE_COMPLETED=$(echo "$EVENTS" | grep --line-buffered -c "DAG_NODE_COMPLETED" || true)
[ "$DAG_NODE_COMPLETED" -ge 2 ] || fail "Expected at least 2 DAG_NODE_COMPLETED (found $DAG_NODE_COMPLETED)"
log "  DAG_NODE_COMPLETED count: $DAG_NODE_COMPLETED"

LLM_STARTED=$(echo "$EVENTS" | grep --line-buffered -c "LLM_STARTED" || true)
[ "$LLM_STARTED" -ge 1 ] || fail "Expected at least 1 LLM_STARTED (found $LLM_STARTED)"
log "  LLM_STARTED count: $LLM_STARTED"

LLM_COMPLETED=$(echo "$EVENTS" | grep --line-buffered -c "LLM_COMPLETED" || true)
[ "$LLM_COMPLETED" -ge 1 ] || fail "Expected at least 1 LLM_COMPLETED (found $LLM_COMPLETED)"
log "  LLM_COMPLETED count: $LLM_COMPLETED"

DAG_SYNTHESIZED=$(echo "$EVENTS" | grep --line-buffered -c "DAG_SYNTHESIZED" || true)
[ "$DAG_SYNTHESIZED" -ge 1 ] || fail "Expected at least 1 DAG_SYNTHESIZED (found $DAG_SYNTHESIZED)"
log "  DAG_SYNTHESIZED count: $DAG_SYNTHESIZED"

TASK_COMPLETED=$(echo "$EVENTS" | grep --line-buffered -c "TASK_COMPLETED" || true)
[ "$TASK_COMPLETED" -ge 1 ] || fail "Expected at least 1 TASK_COMPLETED (found $TASK_COMPLETED)"
log "  TASK_COMPLETED count: $TASK_COMPLETED"

for event in WORKFLOW_STARTED SESSION_LOADED TASK_CLASSIFIED DAG_PLANNED USAGE_RECORDED; do
  COUNT=$(echo "$EVENTS" | grep --line-buffered -c "$event" || true)
  [ "$COUNT" -ge 1 ] || fail "Missing required event: $event (count=$COUNT)"
  log "  $event count: $COUNT"
done

log "4. Checking llm_calls..."
LLM_COUNT=$(psql_cmd "SELECT count(*) FROM llm_calls WHERE task_id='$TASK_ID';" | tr -d ' ')
[ "$LLM_COUNT" -ge 1 ] || fail "Expected at least 1 llm_calls record (found $LLM_COUNT)"
log "  llm_calls count: $LLM_COUNT"

DRAFT_LLM=$(psql_cmd "SELECT count(*) FROM llm_calls WHERE task_id='$TASK_ID' AND node_id='draft_answer';" | tr -d ' ')
[ "$DRAFT_LLM" -ge 1 ] || fail "Expected at least 1 llm_calls for draft_answer (found $DRAFT_LLM)"
log "  llm_calls with node_id=draft_answer: $DRAFT_LLM"

ANALYZE_LLM=$(psql_cmd "SELECT count(*) FROM llm_calls WHERE task_id='$TASK_ID' AND node_id='analyze_input';" | tr -d ' ')
[ "$ANALYZE_LLM" = "0" ] || fail "Expected 0 llm_calls for analyze_input (found $ANALYZE_LLM)"
log "  llm_calls with node_id=analyze_input: $ANALYZE_LLM"

echo ""
echo "=== ALL DAG SYNTHESIS TESTS PASSED ==="
echo ""
echo "Summary:"
echo "  Task ID: $TASK_ID"
echo "  Status: $STATUS"
echo "  Result: $RESULT"
echo "  DAG_NODE_STARTED: $DAG_NODE_STARTED"
echo "  DAG_NODE_COMPLETED: $DAG_NODE_COMPLETED"
echo "  LLM_STARTED: $LLM_STARTED"
echo "  LLM_COMPLETED: $LLM_COMPLETED"
echo "  DAG_SYNTHESIZED: $DAG_SYNTHESIZED"
echo "  TASK_COMPLETED: $TASK_COMPLETED"
echo "  llm_calls: $LLM_COUNT"
echo "  llm_calls draft_answer: $DRAFT_LLM"
echo "  llm_calls analyze_input: $ANALYZE_LLM"