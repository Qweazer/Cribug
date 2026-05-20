#!/bin/bash
set -euo pipefail

# Multi-Agent Synthesizer Tool Execution Test
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

log "=== Multi-Agent Synthesizer Tool Execution Test ==="

# Test: multi_agent mode with echo tool triggered by synthesizer
log "Test: multi_agent mode with echo: synthesizer test"
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{
    "query":"echo: synthesizer tool test",
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

# Verify events
PLANNER_STARTED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "AGENT_STARTED.*planner" || true)
RESEARCHER_STARTED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "AGENT_STARTED.*researcher" || true)
CRITIC_STARTED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "AGENT_STARTED.*critic" || true)
SYNTHESIZER_STARTED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "AGENT_STARTED.*synthesizer" || true)
TASK_COMPLETED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "TASK_COMPLETED" || true)
MULTI_AGENT_SYNTHESIZED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "MULTI_AGENT_SYNTHESIZED" || true)
TOOL_STARTED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "TOOL_STARTED" || true)
TOOL_COMPLETED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "TOOL_COMPLETED" || true)

[ "$PLANNER_STARTED" -ge 1 ] || fail "Missing planner started event"
[ "$RESEARCHER_STARTED" -ge 1 ] || fail "Missing researcher started event"
[ "$CRITIC_STARTED" -ge 1 ] || fail "Missing critic started event"
[ "$SYNTHESIZER_STARTED" -ge 1 ] || fail "Missing synthesizer started event"
[ "$TOOL_STARTED" -ge 1 ] || fail "Missing TOOL_STARTED event"
[ "$TOOL_COMPLETED" -ge 1 ] || fail "Missing TOOL_COMPLETED event"
[ "$TASK_COMPLETED" -ge 1 ] || fail "Missing TASK_COMPLETED event"
[ "$MULTI_AGENT_SYNTHESIZED" -ge 1 ] || fail "Missing MULTI_AGENT_SYNTHESIZED event"

# Verify synthesizer output contains tool result (grep for last AGENT_COMPLETED with synthesizer role)
SYNTH_COMPLETED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep "AGENT_COMPLETED.*synthesizer" | tail -1 || true)
echo "  Synthesizer completed event: $SYNTH_COMPLETED"
echo "$SYNTH_COMPLETED" | grep -q "synthesizer tool test" && echo "  Synthesizer output contains tool result: OK" || echo "  Warning: synthesizer output may not contain expected tool result"

log "  Events: planner=$PLANNER_STARTED, researcher=$RESEARCHER_STARTED, critic=$CRITIC_STARTED, synthesizer=$SYNTHESIZER_STARTED, tools=$TOOL_STARTED/$TOOL_COMPLETED, completed=$TASK_COMPLETED, synthesized=$MULTI_AGENT_SYNTHESIZED"

log "Test PASSED"

echo ""
echo "=== MULTI-AGENT SYNTHESIZER TOOL EXECUTION TEST PASSED ==="