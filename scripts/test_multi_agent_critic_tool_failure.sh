#!/bin/bash
set -euo pipefail

# Multi-Agent Critic Tool Failure Test
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

log "=== Multi-Agent Critic Tool Failure Test ==="

# Test: multi_agent mode with invalid calculator expression triggered by critic
log "Test: multi_agent mode with calc invalid (critic tool failure)"
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{
    "query":"calc 1+xyz",
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

# Verify events: planner -> researcher -> tool -> failed (no synthesizer)
PLANNER_STARTED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "AGENT_STARTED.*planner" || true)
RESEARCHER_STARTED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "AGENT_STARTED.*researcher" || true)
CRITIC_STARTED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "AGENT_STARTED.*critic" || true)
SYNTHESIZER_STARTED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "AGENT_STARTED.*synthesizer" || true)
TOOL_STARTED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "TOOL_STARTED" || true)
TOOL_FAILED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "TOOL_FAILED" || true)
TASK_FAILED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "TASK_FAILED" || true)
TASK_COMPLETED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "TASK_COMPLETED" || true)
MULTI_AGENT_SYNTHESIZED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "MULTI_AGENT_SYNTHESIZED" || true)

[ "$PLANNER_STARTED" -ge 1 ] || fail "Missing planner started event"
[ "$RESEARCHER_STARTED" -ge 1 ] || fail "Missing researcher started event"
[ "$TOOL_STARTED" -ge 1 ] || fail "Missing TOOL_STARTED event"
[ "$TOOL_FAILED" -ge 1 ] || fail "Missing TOOL_FAILED event"
[ "$TASK_FAILED" -ge 1 ] || fail "Missing TASK_FAILED event"
[ "$TASK_COMPLETED" = "0" ] || fail "Should NOT have TASK_COMPLETED event"
[ "$MULTI_AGENT_SYNTHESIZED" = "0" ] || fail "Should NOT have MULTI_AGENT_SYNTHESIZED event"
[ "$SYNTHESIZER_STARTED" = "0" ] || fail "Should NOT have synthesizer started (workflow interrupted)"

log "  Events: planner=$PLANNER_STARTED, researcher=$RESEARCHER_STARTED, tool=$TOOL_STARTED/$TOOL_FAILED, critic=$CRITIC_STARTED, synthesizer=$SYNTHESIZER_STARTED, TASK_FAILED=$TASK_FAILED"

log "Test PASSED"

echo ""
echo "=== MULTI-AGENT CRITIC TOOL FAILURE TEST PASSED ==="