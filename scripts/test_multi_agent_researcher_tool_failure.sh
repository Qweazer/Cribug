#!/bin/bash
set -euo pipefail

# Multi-Agent Researcher Tool Failure Test
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

log "=== Multi-Agent Researcher Tool Failure Test ==="

# Test: multi_agent mode with invalid calculator expression
log "Test: multi_agent mode with calc 1+abc (should fail)"
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{
    "query":"calc 1+abc",
    "config":{"mode":"multi_agent","enable_tools":true}
  }')

TASK_ID=$(echo "$RESP" | jq -r '.task_id')
[ -n "$TASK_ID" ] && [ "$TASK_ID" != "null" ] || fail "Failed to create task"

for i in $(seq 1 30); do
  STATUS=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID" | jq -r '.status')
  [ "$STATUS" = "failed" ] && break
  [ "$STATUS" = "completed" ] && fail "Should not complete on tool failure"
  sleep 1
done

[ "$STATUS" = "failed" ] || fail "Task did not fail (status=$STATUS)"

# Verify error_type is tool_error
ERROR_TYPE=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID" | jq -r '.error_type')
[ "$ERROR_TYPE" = "tool_error" ] || fail "Error type should be 'tool_error': $ERROR_TYPE"
log "  error_type: $ERROR_TYPE"

# Verify events: planner -> AGENT_STARTED(researcher) -> tool -> failed
# Note: researcher starts before tool, but should be marked as failed when tool fails
PLANNER_STARTED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "AGENT_STARTED.*planner" || true)
PLANNER_COMPLETED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "AGENT_COMPLETED.*planner" || true)
TOOL_STARTED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "TOOL_STARTED" || true)
TOOL_FAILED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "TOOL_FAILED" || true)
# Researcher should have started BEFORE tool execution, but be marked failed when tool fails
RESEARCHER_STARTED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "AGENT_STARTED.*researcher" || true)
RESEARCHER_COMPLETED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "AGENT_COMPLETED.*researcher.*failed" || true)
CRITIC_STARTED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "AGENT_STARTED.*critic" || true)
SYNTHESIZER_STARTED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "AGENT_STARTED.*synthesizer" || true)
TASK_FAILED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "TASK_FAILED" || true)
TASK_COMPLETED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "TASK_COMPLETED" || true)

[ "$PLANNER_STARTED" -ge 1 ] || fail "Missing planner started event"
[ "$PLANNER_COMPLETED" -ge 1 ] || fail "Missing planner completed event"
# Researcher starts before tool (tool is executed during researcher phase)
[ "$RESEARCHER_STARTED" -ge 1 ] || fail "Researcher should start (before tool execution)"
[ "$RESEARCHER_COMPLETED" -ge 1 ] || fail "Researcher should be marked failed after tool failure"
[ "$TOOL_STARTED" -ge 1 ] || fail "Missing TOOL_STARTED event"
[ "$TOOL_FAILED" -ge 1 ] || fail "Missing TOOL_FAILED event"
[ "$CRITIC_STARTED" = "0" ] || fail "Should NOT have critic started (workflow interrupted)"
[ "$SYNTHESIZER_STARTED" = "0" ] || fail "Should NOT have synthesizer started (workflow interrupted)"
[ "$TASK_FAILED" -ge 1 ] || fail "Missing TASK_FAILED event"
[ "$TASK_COMPLETED" = "0" ] || fail "Should NOT have TASK_COMPLETED event"

log "  Events: planner=$PLANNER_STARTED/$PLANNER_COMPLETED, researcher=$RESEARCHER_STARTED/$RESEARCHER_COMPLETED, tool=$TOOL_STARTED/$TOOL_FAILED, no critic/synthesizer, TASK_FAILED=$TASK_FAILED"

log "Test PASSED"

# Test 2: Division by zero
log "Test 2: multi_agent mode with calc 5/0 (should fail)"
RESP2=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{
    "query":"calc 5/0",
    "config":{"mode":"multi_agent","enable_tools":true}
  }')

TASK_ID2=$(echo "$RESP2" | jq -r '.task_id')
for i in $(seq 1 30); do
  STATUS2=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID2" | jq -r '.status')
  [ "$STATUS2" = "failed" ] && break
  sleep 1
done

[ "$STATUS2" = "failed" ] || fail "Task did not fail on division by zero"
ERROR2=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID2" | jq -r '.error')
echo "$ERROR2" | grep -qi "division\|zero" || fail "Error should mention division or zero: $ERROR2"
log "  error: $ERROR2"
log "Test 2 PASSED"

echo ""
echo "=== MULTI-AGENT RESEARCHER TOOL FAILURE TEST PASSED ==="