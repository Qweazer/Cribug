#!/bin/bash
set -euo pipefail

# Echo Tool Success Test
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

log "=== Echo Tool Success Test ==="

# Test 1: "echo: hello tools"
log "Test 1: echo: hello tools"
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{
    "query":"echo: hello tools",
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
echo "$RESULT" | grep -q "hello tools" || fail "Result should contain 'hello tools': $RESULT"
log "  Result: $RESULT"

# Verify events
TOOL_STARTED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + | grep -c "TOOL_STARTED" || true)
TOOL_COMPLETED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + | grep -c "TOOL_COMPLETED" || true)
TOOL_FAILED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + | grep -c "TOOL_FAILED" || true)
TASK_COMPLETED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + | grep -c "TASK_COMPLETED" || true)

[ "$TOOL_STARTED" -ge 1 ] || fail "Missing TOOL_STARTED event"
[ "$TOOL_COMPLETED" -ge 1 ] || fail "Missing TOOL_COMPLETED event"
[ "$TOOL_FAILED" = "0" ] || fail "Should not have TOOL_FAILED event"
[ "$TASK_COMPLETED" -ge 1 ] || fail "Missing TASK_COMPLETED event"
log "  Events: OK"
log "Test 1 PASSED"

# Test 2: "回显: 测试中文"
log "Test 2: 回显: 测试中文"
RESP2=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{
    "query":"回显: 测试中文",
    "config":{"enable_tools":true}
  }')

TASK_ID2=$(echo "$RESP2" | jq -r '.task_id')
for i in $(seq 1 30); do
  STATUS2=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID2" | jq -r '.status')
  [ "$STATUS2" = "completed" ] && break
  sleep 1
done

RESULT2=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID2" | jq -r '.result')
echo "$RESULT2" | grep -q "测试中文" || fail "Result should contain '测试中文': $RESULT2"
log "  Result: $RESULT2"
log "Test 2 PASSED"

echo ""
echo "=== ALL ECHO TOOL TESTS PASSED ==="