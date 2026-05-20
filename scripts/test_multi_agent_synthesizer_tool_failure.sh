#!/bin/bash
set -euo pipefail

# Multi-Agent Synthesizer Tool Failure Test
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

log "=== Multi-Agent Synthesizer Tool Failure Test ==="

# Test: synthesizer tool failure - first agent that should fail is researcher
# Note: calc 9/0 fails at researcher tool detection since all agents detect the same query pattern
log "Test: multi_agent mode with calc 9/0 (tool failure)"
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{
    "query":"calc 9/0",
    "config":{"mode":"multi_agent","enable_tools":true}
  }')

TASK_ID=$(echo "$RESP" | jq -r '.task_id')
[ -n "$TASK_ID" ] && [ "$TASK_ID" != "null" ] || fail "Failed to create task"

for i in $(seq 1 60); do
  STATUS=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID" | jq -r '.status')
  [ "$STATUS" = "failed" ] && break
  [ "$STATUS" = "completed" ] && break
  sleep 1
done

[ "$STATUS" = "failed" ] || fail "Task did not fail (status=$STATUS)"

# Verify error_type is tool_error
ERROR_TYPE=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID" | jq -r '.error_type')
[ "$ERROR_TYPE" = "tool_error" ] || fail "Error type should be 'tool_error': $ERROR_TYPE"
ERROR=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID" | jq -r '.error')
echo "$ERROR" | grep -qi "division\|zero" || fail "Error should mention division or zero: $ERROR"
log "  error_type: $ERROR_TYPE, error: $ERROR"

# Verify events: planner -> AGENT_STARTED(researcher) -> tool -> failed
PLANNER_STARTED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "AGENT_STARTED.*planner" || true)
PLANNER_COMPLETED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "AGENT_COMPLETED.*planner" || true)
RESEARCHER_STARTED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "AGENT_STARTED.*researcher" || true)
RESEARCHER_COMPLETED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "AGENT_COMPLETED.*researcher.*failed" || true)
TOOL_STARTED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "TOOL_STARTED" || true)
TOOL_FAILED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "TOOL_FAILED" || true)
TASK_FAILED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "TASK_FAILED" || true)
TASK_COMPLETED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "TASK_COMPLETED" || true)

[ "$PLANNER_STARTED" -ge 1 ] || fail "Missing planner started event"
[ "$PLANNER_COMPLETED" -ge 1 ] || fail "Missing planner completed event"
[ "$RESEARCHER_STARTED" -ge 1 ] || fail "Researcher should start (tool detected during researcher phase)"
[ "$RESEARCHER_COMPLETED" -ge 1 ] || fail "Researcher should be marked failed after tool failure"
[ "$TOOL_STARTED" -ge 1 ] || fail "Missing TOOL_STARTED event"
[ "$TOOL_FAILED" -ge 1 ] || fail "Missing TOOL_FAILED event"
[ "$TASK_FAILED" -ge 1 ] || fail "Missing TASK_FAILED event"
[ "$TASK_COMPLETED" = "0" ] || fail "Should NOT have TASK_COMPLETED event"

log "  Events: planner=$PLANNER_STARTED/$PLANNER_COMPLETED, researcher=$RESEARCHER_STARTED/$RESEARCHER_COMPLETED, tool=$TOOL_STARTED/$TOOL_FAILED, TASK_FAILED=$TASK_FAILED"

log "Test PASSED"

echo ""
echo "=== MULTI-AGENT SYNTHESIZER TOOL TEST PASSED ==="