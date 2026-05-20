#!/bin/bash
set -euo pipefail

# Tool Failure Test - Calculator Parse Error
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

log "=== Tool Failure Test ==="

# Test: "calc 1+abc" should fail with parse error
log "Test: calc 1+abc (should fail)"
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{
    "query":"calc 1+abc",
    "config":{"enable_tools":true}
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

ERROR_TYPE=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID" | jq -r '.error_type')
[ "$ERROR_TYPE" = "tool_error" ] || fail "Error type should be 'tool_error': $ERROR_TYPE"
log "  error_type: $ERROR_TYPE"

ERROR=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID" | jq -r '.error')
log "  error: $ERROR"

# Verify events
TOOL_STARTED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + | grep -c "TOOL_STARTED" || true)
TOOL_FAILED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + | grep -c "TOOL_FAILED" || true)
TASK_FAILED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + | grep -c "TASK_FAILED" || true)
TASK_COMPLETED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + | grep -c "TASK_COMPLETED" || true)

[ "$TOOL_STARTED" -ge 1 ] || fail "Missing TOOL_STARTED event"
[ "$TOOL_FAILED" -ge 1 ] || fail "Missing TOOL_FAILED event"
[ "$TASK_FAILED" -ge 1 ] || fail "Missing TASK_FAILED event"
[ "$TASK_COMPLETED" = "0" ] || fail "Should NOT have TASK_COMPLETED event"
log "  Events: TOOL_STARTED=$TOOL_STARTED, TOOL_FAILED=$TOOL_FAILED, TASK_FAILED=$TASK_FAILED, TASK_COMPLETED=$TASK_COMPLETED"

log "Test PASSED"

# Test 2: Division by zero
log "Test 2: calc 5/0 (should fail with division by zero)"
RESP2=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{
    "query":"calc 5/0",
    "config":{"enable_tools":true}
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
echo "=== ALL TOOL FAILURE TESTS PASSED ==="