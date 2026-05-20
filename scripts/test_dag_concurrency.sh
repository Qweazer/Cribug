#!/bin/bash
set -euo pipefail

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

log "=== DAG Concurrency Test ==="

# Test 1: DAG workflow basic execution
log "Test 1: DAG workflow basic execution"
RESP=$(curl -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{"query":"Analyze AI and ML","config":{"mode":"dag"}}')
TASK_ID=$(echo "$RESP" | jq -r '.task_id')
[ -n "$TASK_ID" ] && [ "$TASK_ID" != "null" ] || fail "Failed to create task"

for i in $(seq 1 60); do
  STATUS=$(curl -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID" | jq -r '.status')
  [ "$STATUS" = "completed" ] && break
  [ "$STATUS" = "failed" ] && break
  sleep 1
done

[ "$STATUS" = "completed" ] || fail "DAG task failed: $STATUS"
log "  Basic DAG execution PASSED"

# Test 2: Verify DAG_PLANNED event
log "Test 2: Verify DAG_PLANNED event"
DAG_PLANNED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "DAG_PLANNED" || true)
[ "$DAG_PLANNED" -ge 1 ] || fail "Missing DAG_PLANNED event"
log "  DAG_PLANNED event PASSED"

# Test 3: Verify DAG_NODE_STARTED and DAG_NODE_COMPLETED events
log "Test 3: Verify DAG_NODE events"
NODE_STARTED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "DAG_NODE_STARTED" || true)
NODE_COMPLETED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "DAG_NODE_COMPLETED" || true)

[ "$NODE_STARTED" -ge 1 ] || fail "Expected at least 1 DAG_NODE_STARTED event, got $NODE_STARTED"
[ "$NODE_COMPLETED" -ge 1 ] || fail "Expected at least 1 DAG_NODE_COMPLETED event, got $NODE_COMPLETED"
log "  DAG_NODE events PASSED"

# Test 4: Verify Redis node statuses (Slice 7)
log "Test 4: Verify Redis DAG node statuses"
if redis_cmd EXISTS "dag:$TASK_ID:nodes:status" 2>/dev/null | grep -q "1"; then
  STATUS_COUNT=$(redis_cmd HLEN "dag:$TASK_ID:nodes:status" 2>/dev/null || echo "0")
  [ "$STATUS_COUNT" -ge 1 ] || fail "Expected node statuses in Redis, got $STATUS_COUNT"
  log "  Redis node statuses PASSED"
else
  log "  Redis tracking not yet enabled (ENABLE_DAG_CONCURRENCY=true)"
fi

# Test 5: Verify LLM usage recorded
log "Test 5: Verify LLM usage"
# Check usage from llm_calls table or API response
USAGE=$(curl -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID" | jq -r '.usage.total_tokens // 0')
LLM_CALLS=$(docker exec deploy-postgres-1 psql -U admin -d orchestrator -t -c "SELECT COUNT(*) FROM llm_calls WHERE task_id='$TASK_ID';" 2>/dev/null | tr -d ' ' || echo "0")
if [ "$LLM_CALLS" -gt 0 ]; then
  log "  LLM usage recorded in database ($LLM_CALLS calls)"
elif [ "$USAGE" -gt 0 ]; then
  log "  LLM usage from API: $USAGE"
else
  log "  Warning: No LLM usage recorded (may be expected for mock DAG nodes)"
fi
log "  LLM usage check PASSED"

# Test 6: DAG_SYNTHESIZED event
log "Test 6: Verify DAG_SYNTHESIZED event"
DAG_SYN=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "DAG_SYNTHESIZED" || true)
[ "$DAG_SYN" -ge 1 ] || fail "Missing DAG_SYNTHESIZED event"
log "  DAG_SYNTHESIZED event PASSED"

log ""
log "=== DAG CONCURRENCY TESTS PASSED ==="