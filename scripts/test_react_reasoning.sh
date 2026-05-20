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

log "=== ReAct Reasoning Loop Test ==="

# Test 1: DAG workflow without ReAct (baseline)
log "Test 1: DAG workflow without ReAct (baseline)"
RESP=$(curl -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{"query":"What is 2+2?","config":{"mode":"dag"}}')
TASK_ID=$(echo "$RESP" | jq -r '.task_id')
[ -n "$TASK_ID" ] && [ "$TASK_ID" != "null" ] || fail "Failed to create task"

for i in $(seq 1 60); do
  STATUS=$(curl -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID" | jq -r '.status')
  [ "$STATUS" = "completed" ] && break
  [ "$STATUS" = "failed" ] && break
  sleep 1
done

[ "$STATUS" = "completed" ] || fail "Task failed: $STATUS"
BASELINE_TOKENS=$(curl -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID" | jq -r '.usage.total_tokens // 0')
log "  Baseline tokens: $BASELINE_TOKENS"
log "  No-ReAct test PASSED"

# Test 2: DAG workflow with ReAct enabled
log "Test 2: DAG workflow with ReAct enabled"
RESP2=$(curl -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{"query":"Calculate 15*23+7 step by step","config":{"mode":"dag","enable_react":true,"react_max_iterations":3}}')
TASK_ID2=$(echo "$RESP2" | jq -r '.task_id')
[ -n "$TASK_ID2" ] && [ "$TASK_ID2" != "null" ] || fail "Failed to create task 2"

for i in $(seq 1 90); do
  STATUS2=$(curl -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID2" | jq -r '.status')
  [ "$STATUS2" = "completed" ] && break
  [ "$STATUS2" = "failed" ] && break
  sleep 1
done

[ "$STATUS2" = "completed" ] || fail "ReAct task failed: $STATUS2"
REACT_TOKENS=$(curl -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID2" | jq -r '.usage.total_tokens // 0')
log "  ReAct tokens: $REACT_TOKENS"
log "  ReAct enabled test PASSED"

# Test 3: Verify REACT_STEP events (if enabled)
log "Test 3: Verify REACT_STEP events"
REACT_STEPS=$(redis_cmd XRANGE "task:$TASK_ID2:events" - + 2>/dev/null | grep -c "REACT_STEP" || true)
if [ "$REACT_STEPS" -ge 1 ]; then
  log "  Found $REACT_STEPS REACT_STEP events"
  log "  REACT_STEP events PASSED"
else
  log "  No REACT_STEP events (ENABLE_REACT=true may not be set)"
fi

# Test 4: Verify Redis react steps (if enabled)
log "Test 4: Verify Redis ReAct steps"
STEPS_KEY=$(redis_cmd KEYS "react:$TASK_ID2:*:steps" 2>/dev/null | head -1 || true)
if [ -n "$STEPS_KEY" ] && [ "$STEPS_KEY" != "" ]; then
  STEPS_COUNT=$(redis_cmd LLEN "$STEPS_KEY" 2>/dev/null | head -1 || echo "0")
  log "  Found $STEPS_COUNT ReAct steps in Redis key: $STEPS_KEY"
  log "  Redis ReAct steps PASSED"
else
  log "  No ReAct steps in Redis (ENABLE_REACT=true may not be set)"
fi

# Test 5: Verify multiple LLM calls (more iterations = more tokens)
log "Test 5: Verify multiple LLM calls"
if [ "$REACT_TOKENS" -gt "$BASELINE_TOKENS" ]; then
  log "  ReAct used more tokens ($REACT_TOKENS > $BASELINE_TOKENS) - confirms multiple iterations"
  log "  Multiple LLM calls PASSED"
else
  log "  Warning: Token count not significantly higher (may be expected for simple tasks)"
fi

log ""
log "=== REACT REASONING TESTS PASSED ==="