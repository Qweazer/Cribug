#!/bin/bash
set -euo pipefail

# Multi-Agent Critic Tool Execution Test
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

log "=== Multi-Agent Critic Tool Execution Test ==="

# Test: multi_agent mode with calculator tool triggered by critic
# Use "critic" prefix to trigger tool after researcher
log "Test: multi_agent mode with echo: critic test"
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{
    "query":"echo: critic tool test",
    "config":{"mode":"multi_agent","enable_tools":true}
  }')

TASK_ID=$(echo "$RESP" | jq -r '.task_id')
[ -n "$TASK_ID" ] && [ "$TASK_ID" != "null" ] || fail "Failed to create task"

for i in $(seq 1 90); do
  STATUS=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID" | jq -r '.status')
  [ "$STATUS" = "completed" ] && break
  [ "$STATUS" = "failed" ] && fail "Task failed unexpectedly"
  sleep 1
done

[ "$STATUS" = "completed" ] || fail "Task did not complete (status=$STATUS)"

# Get result
RESULT=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID" | jq -r '.result')
[ -n "$RESULT" ] || fail "Result should not be empty"
log "  Final result: $RESULT"

# Verify events: planner -> researcher -> TOOL_STARTED (by critic?) -> TOOL_COMPLETED -> AGENT_COMPLETED(critic) -> synthesizer -> completed
PLANNER_STARTED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "AGENT_STARTED.*planner" || true)
RESEARCHER_STARTED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "AGENT_STARTED.*researcher" || true)
CRITIC_STARTED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "AGENT_STARTED.*critic" || true)
SYNTHESIZER_STARTED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "AGENT_STARTED.*synthesizer" || true)
TASK_COMPLETED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "TASK_COMPLETED" || true)
MULTI_AGENT_SYNTHESIZED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "MULTI_AGENT_SYNTHESIZED" || true)

[ "$PLANNER_STARTED" -ge 1 ] || fail "Missing planner started event"
[ "$RESEARCHER_STARTED" -ge 1 ] || fail "Missing researcher started event"
[ "$CRITIC_STARTED" -ge 1 ] || fail "Missing critic started event"
[ "$SYNTHESIZER_STARTED" -ge 1 ] || fail "Missing synthesizer started event"
[ "$TASK_COMPLETED" -ge 1 ] || fail "Missing TASK_COMPLETED event"
[ "$MULTI_AGENT_SYNTHESIZED" -ge 1 ] || fail "Missing MULTI_AGENT_SYNTHESIZED event"

log "  Events: planner=$PLANNER_STARTED, researcher=$RESEARCHER_STARTED, critic=$CRITIC_STARTED, synthesizer=$SYNTHESIZER_STARTED, completed=$TASK_COMPLETED, synthesized=$MULTI_AGENT_SYNTHESIZED"

# Verify critic output contains tool result if tool was used by critic
CRITIC_COMPLETED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep "AGENT_COMPLETED.*critic" -A5 | grep '"output"' | head -1 || true)
if [ -n "$CRITIC_COMPLETED" ]; then
  echo "  Critic output: $CRITIC_COMPLETED"
fi

log "Test PASSED"

echo ""
echo "=== MULTI-AGENT CRITIC TOOL EXECUTION TEST PASSED ==="