#!/bin/bash
set -euo pipefail

# Multi-Agent Stepwise Tool Usage Test
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

log "=== Multi-Agent Stepwise Tool Usage Test ==="

# Test 1: Verify TOOL_STEP_* events are emitted
log "Test 1: Verify TOOL_STEP_* events"
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{
    "query":"echo: stepwise test",
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

# Verify TOOL_STEP_STARTED events (3 agents x 1 tool = 3 events each, total 6 with COMPLETED)
STEP_STARTED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "TOOL_STEP_STARTED" || true)
STEP_COMPLETED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "TOOL_STEP_COMPLETED" || true)

log "  TOOL_STEP_STARTED events: $STEP_STARTED (expected: >= 3)"
log "  TOOL_STEP_COMPLETED events: $STEP_COMPLETED (expected: >= 3)"

[ "$STEP_STARTED" -ge 3 ] || fail "Missing TOOL_STEP_STARTED events (expected >= 3)"
[ "$STEP_COMPLETED" -ge 3 ] || fail "Missing TOOL_STEP_COMPLETED events (expected >= 3)"

# Verify step events contain agent_role and step_id
# Extract just the JSON payload lines (lines containing event_type or payload data)
STEP_EVENT=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep "agent_role" | head -1 || true)
echo "STEP_EVENT: $STEP_EVENT"
echo "$STEP_EVENT" | grep -q "agent_role" || fail "TOOL_STEP_STARTED missing agent_role"
echo "$STEP_EVENT" | grep -q "step_id" || fail "TOOL_STEP_STARTED missing step_id"
echo "$STEP_EVENT" | grep -q "tool_name" || fail "TOOL_STEP_STARTED missing tool_name"

# Test 2: Verify tool failure produces TOOL_STEP_FAILED
log "Test 2: Verify TOOL_STEP_FAILED on tool failure"
# Use calc 9/0 to trigger division by zero error
RESP2=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{
    "query":"calc 9/0",
    "config":{"mode":"multi_agent","enable_tools":true}
  }')

TASK_ID2=$(echo "$RESP2" | jq -r '.task_id')
[ -n "$TASK_ID2" ] && [ "$TASK_ID2" != "null" ] || fail "Failed to create task 2"

for i in $(seq 1 90); do
  STATUS2=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID2" | jq -r '.status')
  [ "$STATUS2" = "failed" ] && break
  [ "$STATUS2" = "completed" ] && break
  sleep 1
done

[ "$STATUS2" = "failed" ] || fail "Task 2 should have failed (status=$STATUS2)"

# Verify TOOL_STEP_FAILED event
STEP_FAILED=$(redis_cmd XRANGE "task:$TASK_ID2:events" - + 2>/dev/null | grep -c "TOOL_STEP_FAILED" || true)
log "  TOOL_STEP_FAILED events: $STEP_FAILED (expected: >= 1)"
[ "$STEP_FAILED" -ge 1 ] || fail "Missing TOOL_STEP_FAILED events on tool failure"

# Test 3: Verify step events contain latency_ms
log "Test 3: Verify step events contain latency_ms"
COMPLETED_EVENT=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep "latency_ms" | head -1 || true)
echo "COMPLETED_EVENT: $COMPLETED_EVENT"
echo "$COMPLETED_EVENT" | grep -q "latency_ms" || fail "TOOL_STEP_COMPLETED missing latency_ms"
log "  latency_ms present in TOOL_STEP_COMPLETED"

log ""
log "=== ALL STEPWISE TOOL USAGE TESTS PASSED ==="