#!/bin/bash
set -euo pipefail

# Multi-Agent Tool Usage Aggregation Test
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

log "=== Multi-Agent Tool Usage Aggregation Test ==="

# Test: multi_agent mode with echo tool to verify TOOL_USAGE_SUMMARY events
log "Test: multi_agent mode with echo: aggregation test"
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{
    "query":"echo: aggregation test",
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

# Verify TOOL_USAGE_SUMMARY events for each agent
RESEARCHER_SUMMARY=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "TOOL_USAGE_SUMMARY.*researcher" || true)
CRITIC_SUMMARY=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "TOOL_USAGE_SUMMARY.*critic" || true)
SYNTHESIZER_SUMMARY=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "TOOL_USAGE_SUMMARY.*synthesizer" || true)

# Verify TOOL_COMPLETED events (3 agents x 1 tool = 3 events)
TOOL_COMPLETED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "TOOL_COMPLETED" || true)

# Verify MULTI_AGENT_SYNTHESIZED includes tool count
MULTI_AGENT=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep "MULTI_AGENT_SYNTHESIZED" -A2 | head -10 || true)

log "  Tool usage summary events: researcher=$RESEARCHER_SUMMARY, critic=$CRITIC_SUMMARY, synthesizer=$SYNTHESIZER_SUMMARY"
log "  Tool completed events: $TOOL_COMPLETED"
log "  Multi-agent synthesized: $MULTI_AGENT"

# Verify tool usage summary events exist for each agent
[ "$RESEARCHER_SUMMARY" -ge 1 ] || fail "Missing TOOL_USAGE_SUMMARY for researcher"
[ "$CRITIC_SUMMARY" -ge 1 ] || fail "Missing TOOL_USAGE_SUMMARY for critic"
[ "$SYNTHESIZER_SUMMARY" -ge 1 ] || fail "Missing TOOL_USAGE_SUMMARY for synthesizer"
[ "$TOOL_COMPLETED" -ge 3 ] || fail "Missing TOOL_COMPLETED events (expected >= 3)"

log "Test PASSED"

echo ""
echo "=== MULTI-AGENT TOOL USAGE AGGREGATION TEST PASSED ==="