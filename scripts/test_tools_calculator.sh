#!/bin/bash
set -euo pipefail

# Calculator Tool Success Test
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

log "=== Calculator Tool Success Test ==="

# Test 1: "calc 1+2*3" should return 7
log "Test 1: calc 1+2*3"
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{
    "query":"calc 1+2*3",
    "config":{"enable_tools":true}
  }')

TASK_ID=$(echo "$RESP" | jq -r '.task_id')
[ -n "$TASK_ID" ] && [ "$TASK_ID" != "null" ] || fail "Failed to create task"

for i in $(seq 1 30); do
  STATUS=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID" | jq -r '.status')
  [ "$STATUS" = "completed" ] && break
  [ "$STATUS" = "failed" ] && fail "Task failed unexpectedly"
  sleep 1
done

[ "$STATUS" = "completed" ] || fail "Task did not complete (status=$STATUS)"

RESULT=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID" | jq -r '.result')
echo "$RESULT" | grep -q "7" || fail "Result should contain 7: $RESULT"
log "  Result: $RESULT"

# Verify events
TOOL_STARTED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + | grep -c "TOOL_STARTED" || true)
TOOL_COMPLETED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + | grep -c "TOOL_COMPLETED" || true)
TOOL_FAILED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + | grep -c "TOOL_FAILED" || true)
TASK_COMPLETED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + | grep -c "TASK_COMPLETED" || true)

[ "$TOOL_STARTED" -ge 1 ] || fail "Missing TOOL_STARTED event (count=$TOOL_STARTED)"
[ "$TOOL_COMPLETED" -ge 1 ] || fail "Missing TOOL_COMPLETED event (count=$TOOL_COMPLETED)"
[ "$TOOL_FAILED" = "0" ] || fail "Should not have TOOL_FAILED event (count=$TOOL_FAILED)"
[ "$TASK_COMPLETED" -ge 1 ] || fail "Missing TASK_COMPLETED event"
log "  Events: TOOL_STARTED=$TOOL_STARTED, TOOL_COMPLETED=$TOOL_COMPLETED, TASK_COMPLETED=$TASK_COMPLETED"

log "Test 1 PASSED"

# Test 2: "calculate: 10.5-2.1" should return 8.4
log "Test 2: calculate: 10.5-2.1"
RESP2=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{
    "query":"calculate: 10.5-2.1",
    "config":{"enable_tools":true}
  }')

TASK_ID2=$(echo "$RESP2" | jq -r '.task_id')
for i in $(seq 1 30); do
  STATUS2=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID2" | jq -r '.status')
  [ "$STATUS2" = "completed" ] && break
  sleep 1
done

RESULT2=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID2" | jq -r '.result')
echo "$RESULT2" | grep -q "8.4" || fail "Result should contain 8.4: $RESULT2"
log "  Result: $RESULT2"
log "Test 2 PASSED"

# Test 3: "(5+3)/2" should return 4
log "Test 3: calculate: (5+3)/2"
RESP3=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{
    "query":"calculate: (5+3)/2",
    "config":{"enable_tools":true}
  }')

TASK_ID3=$(echo "$RESP3" | jq -r '.task_id')
for i in $(seq 1 30); do
  STATUS3=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID3" | jq -r '.status')
  [ "$STATUS3" = "completed" ] && break
  sleep 1
done

RESULT3=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID3" | jq -r '.result')
echo "$RESULT3" | grep -q "4" || fail "Result should contain 4: $RESULT3"
log "  Result: $RESULT3"
log "Test 3 PASSED"

echo ""
echo "=== ALL CALCULATOR TOOL TESTS PASSED ==="