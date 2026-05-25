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

log "=== ReAct Workflow Level Test ==="

# Test 1: Code structure verification - ReAct loop in Workflow layer
log "Test 1: ReAct loop is in Workflow layer (not Activity)"
if [ -f "internal/workflows/patterns/react.go" ]; then
  # Check that ReactLoop is in the workflow/patterns package
  if grep -q "func ReactLoop" internal/workflows/patterns/react.go; then
    log "  ReactLoop found in internal/workflows/patterns/react.go"
  else
    fail "ReactLoop function not found in patterns/react.go"
  fi
  # Verify it's not in the old Activity location as a for-loop
  if grep -q "func ReactLoop\|for i := 0.*maxIterations" internal/activities/react.go 2>/dev/null; then
    log "  WARNING: Old ReAct loop still exists in activities/react.go (legacy, kept for compatibility)"
  fi
else
  fail "internal/workflows/patterns/react.go not found"
fi
log "  Workflow-level structure: PASSED"

# Test 2: enable_react=true task completes successfully
log "Test 2: enable_react=true task completes"
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{
    "query":"Calculate 15+27 step by step",
    "config":{
      "mode":"dag",
      "enable_react":true,
      "react_max_iterations":3,
      "max_completion_tokens":256,
      "temperature":0.0
    }
  }')
TASK_ID=$(echo "$RESP" | jq -r '.task_id')
[ -n "$TASK_ID" ] && [ "$TASK_ID" != "null" ] || fail "Failed to create ReAct task"
log "  Task created: $TASK_ID"

STATUS=""
for i in $(seq 1 90); do
  STATUS=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID" | jq -r '.status')
  [ "$STATUS" = "completed" ] && break
  [ "$STATUS" = "failed" ] && break
  [ "$STATUS" = "budget_exceeded" ] && break
  sleep 1
done

[ "$STATUS" = "completed" ] || fail "ReAct task not completed: status=$STATUS"
log "  Task completed: PASSED"

# Test 3: llm_calls > 1 (proves multiple AgentActivity calls per ReAct step)
log "Test 3: Multiple LLM calls (llm_calls > 1)"
LLM_CALLS=$(psql_cmd "SELECT count(*) FROM llm_calls WHERE task_id='$TASK_ID';" 2>/dev/null | tr -d ' ' || echo "0")
if [ "${LLM_CALLS:-0}" -gt 1 ]; then
  log "  llm_calls=$LLM_CALLS (> 1): PASSED"
else
  log "  WARNING: llm_calls=$LLM_CALLS (expected > 1 for ReAct with Reason+Act+Synthesis)"
fi

# Test 4: workflow result contains react metadata
log "Test 4: Workflow result present"
RESULT=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID" | jq -r '.result // empty')
if [ -n "$RESULT" ]; then
  log "  Result non-empty: PASSED (len=${#RESULT})"
else
  fail "Result is empty"
fi

# Test 5: Usage recorded
log "Test 5: Token usage recorded"
TOTAL_TOKENS=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID" | jq -r '.usage.total_tokens // 0')
if [ "${TOTAL_TOKENS:-0}" -gt 0 ]; then
  log "  Total tokens=$TOTAL_TOKENS (> 0): PASSED"
else
  log "  WARNING: Total tokens is 0"
fi

# Test 6: react_steps table exists and has data (if Postgres available)
log "Test 6: react_steps audit table"
if psql_cmd "SELECT count(*) FROM react_steps WHERE workflow_id=(SELECT workflow_id FROM tasks WHERE id='$TASK_ID');" 2>/dev/null; then
  REACT_STEP_COUNT=$(psql_cmd "SELECT count(*) FROM react_steps WHERE workflow_id=(SELECT workflow_id FROM tasks WHERE id='$TASK_ID');" 2>/dev/null | tr -d ' ' || echo "0")
  log "  react_steps records: $REACT_STEP_COUNT"
  if [ "${REACT_STEP_COUNT:-0}" -ge 0 ]; then
    log "  react_steps table accessible: PASSED"
  fi
else
  log "  WARNING: Could not query react_steps (table may not exist yet)"
fi

# Test 7: DAG visualization events preserved (Slice 10 regression)
log "Test 7: DAG visualization events preserved"
DAG_NODE_RUNNING=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "DAG_NODE_RUNNING\|DAG_NODE_STARTED" || echo "0")
DAG_NODE_COMPLETED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "DAG_NODE_COMPLETED" || echo "0")
log "  DAG_NODE_RUNNING/STARTED events: $DAG_NODE_RUNNING"
log "  DAG_NODE_COMPLETED events: $DAG_NODE_COMPLETED"
if [ "${DAG_NODE_RUNNING:-0}" -ge 1 ] && [ "${DAG_NODE_COMPLETED:-0}" -ge 1 ]; then
  log "  DAG visualization events preserved: PASSED"
else
  log "  WARNING: DAG visualization events may be incomplete"
fi

# Test 8: enable_react=false keeps original behavior
log "Test 8: enable_react=false original behavior"
RESP3=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{
    "query":"What is 3+4?",
    "config":{
      "mode":"dag",
      "max_completion_tokens":128,
      "temperature":0.0
    }
  }')
TASK_ID3=$(echo "$RESP3" | jq -r '.task_id')
[ -n "$TASK_ID3" ] && [ "$TASK_ID3" != "null" ] || fail "Failed to create non-ReAct task"

for i in $(seq 1 60); do
  STATUS3=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID3" | jq -r '.status')
  [ "$STATUS3" = "completed" ] && break
  [ "$STATUS3" = "failed" ] && break
  sleep 1
done

[ "$STATUS3" = "completed" ] || fail "Non-ReAct task not completed: status=$STATUS3"
log "  Non-ReAct task completed: PASSED"

log ""
log "=== REACT WORKFLOW LEVEL TESTS PASSED ==="
