#!/bin/bash
set -euo pipefail

# Multi-Agent Researcher Tool Execution Test
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

log "=== Multi-Agent Researcher Tool Execution Test ==="

# Test: multi_agent mode with calculator tool
log "Test: multi_agent mode with calc 1+2*3"
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{
    "query":"calc 1+2*3",
    "config":{"mode":"multi_agent","enable_tools":true}
  }')

TASK_ID=$(echo "$RESP" | jq -r '.task_id')
[ -n "$TASK_ID" ] && [ "$TASK_ID" != "null" ] || fail "Failed to create task"

for i in $(seq 1 60); do
  STATUS=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID" | jq -r '.status')
  [ "$STATUS" = "completed" ] && break
  [ "$STATUS" = "failed" ] && fail "Task failed unexpectedly"
  sleep 1
done

[ "$STATUS" = "completed" ] || fail "Task did not complete (status=$STATUS)"

# Get result (synthesizer output, not the raw tool result)
RESULT=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID" | jq -r '.result')

# Verify result is not empty
[ -n "$RESULT" ] || fail "Result should not be empty"
log "  Final result: $RESULT"

# Verify events: planner -> tool -> researcher -> critic -> synthesizer -> completed
PLANNER_STARTED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "AGENT_STARTED.*planner" || true)
TOOL_STARTED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "TOOL_STARTED" || true)
TOOL_COMPLETED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "TOOL_COMPLETED" || true)
RESEARCHER_STARTED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "AGENT_STARTED.*researcher" || true)
RESEARCHER_COMPLETED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "AGENT_COMPLETED.*researcher" || true)
SYNTHESIZER_STARTED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "AGENT_STARTED.*synthesizer" || true)
TASK_COMPLETED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "TASK_COMPLETED" || true)

[ "$PLANNER_STARTED" -ge 1 ] || fail "Missing planner started event"
[ "$TOOL_STARTED" -ge 1 ] || fail "Missing TOOL_STARTED event"
[ "$TOOL_COMPLETED" -ge 1 ] || fail "Missing TOOL_COMPLETED event"
[ "$RESEARCHER_STARTED" -ge 1 ] || fail "Missing researcher started event"
[ "$RESEARCHER_COMPLETED" -ge 1 ] || fail "Missing researcher completed event"
[ "$SYNTHESIZER_STARTED" -ge 1 ] || fail "Missing synthesizer started event"
[ "$TASK_COMPLETED" -ge 1 ] || fail "Missing TASK_COMPLETED event"

log "  Events: planner=$PLANNER_STARTED, tool=$TOOL_STARTED/$TOOL_COMPLETED, researcher=$RESEARCHER_STARTED/$RESEARCHER_COMPLETED, synthesizer=$SYNTHESIZER_STARTED, completed=$TASK_COMPLETED"

# Verify researcher output contains tool result
RESEARCHER_OUTPUT=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep "AGENT_COMPLETED.*researcher" -A5 | grep '"output"' | head -1)
echo "$RESEARCHER_OUTPUT" | grep -q "tool\|calculator\|7" || fail "Researcher output should contain tool result: $RESEARCHER_OUTPUT"
log "  Researcher output contains tool result: OK"

log "Test PASSED"

echo ""
echo "=== MULTI-AGENT RESEARCHER TOOL EXECUTION TEST PASSED ==="