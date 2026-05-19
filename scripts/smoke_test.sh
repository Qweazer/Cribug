#!/bin/bash
set -euo pipefail

# Configuration
GATEWAY_URL="${GATEWAY_URL:-http://127.0.0.1:8080}"
COMPOSE_FILE="${COMPOSE_FILE:-deploy/docker-compose.yaml}"
REDIS_HOST="${REDIS_HOST:-127.0.0.1}"
REDIS_PORT="${REDIS_PORT:-6379}"

# Helper functions
log() { echo "[$(date +'%H:%M:%S')] $*"; }
fail() { echo "[FAIL] $*" >&2; exit 1; }

# Check dependencies
check_cmd() {
  if ! command -v "$1" &>/dev/null; then
    fail "Required command not found: $1"
  fi
}

log "=== MVP Smoke Test Starting ==="

# Check dependencies
check_cmd curl
check_cmd jq

# Helper to run redis-cli
redis_cmd() {
  if command -v redis-cli &>/dev/null; then
    redis-cli -h "$REDIS_HOST" -p "$REDIS_PORT" "$@"
  else
    docker.exe exec deploy-redis-1 redis-cli "$@"
  fi
}

# Helper to run psql
psql_cmd() {
  docker.exe exec deploy-postgres-1 psql -U admin -d orchestrator -t -c "$1"
}

# 1. Health check
log "1. Checking Gateway health..."
HEALTH=$(curl --noproxy '*' -s "$GATEWAY_URL/health" | jq -r '.status')
[ "$HEALTH" = "healthy" ] || fail "Gateway health check failed: $HEALTH"

POSTGRES=$(curl --noproxy '*' -s "$GATEWAY_URL/health" | jq -r '.dependencies.postgres')
[ "$POSTGRES" = "connected" ] || fail "Postgres not connected: $POSTGRES"

REDIS=$(curl --noproxy '*' -s "$GATEWAY_URL/health" | jq -r '.dependencies.redis')
[ "$REDIS" = "connected" ] || fail "Redis not connected: $REDIS"

log "  Gateway health: OK (postgres=$POSTGRES, redis=$REDIS)"

# 2. Python LLM Service health
log "2. Checking Python LLM Service health..."
LLM_HEALTH=$(curl --noproxy '*' -s http://127.0.0.1:8000/health | jq -r '.status')
[ "$LLM_HEALTH" = "healthy" ] || fail "Python LLM Service not healthy: $LLM_HEALTH"
log "  Python LLM Service: OK"

# 3. Normal task test
log "3. Testing normal task..."
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{
    "query":"smoke normal task",
    "session_id":"sess-smoke",
    "config":{
      "model":"gpt-4o-mini",
      "temperature":0.7,
      "max_total_tokens":8000,
      "max_completion_tokens":128
    }
  }')

TASK_ID=$(echo "$RESP" | jq -r '.task_id')
[ -n "$TASK_ID" ] && [ "$TASK_ID" != "null" ] || fail "Failed to create normal task"

log "  Task created: $TASK_ID"

# Poll until completed
for i in $(seq 1 60); do
  RESULT=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID")
  STATUS=$(echo "$RESULT" | jq -r '.status')

  if [ "$STATUS" = "completed" ]; then
    log "  Task completed at poll $i"
    break
  elif [ "$STATUS" = "failed" ] || [ "$STATUS" = "budget_exceeded" ]; then
    fail "Task ended with unexpected status: $STATUS"
  fi
  sleep 1
done

[ "$STATUS" = "completed" ] || fail "Task did not complete in time (status=$STATUS)"

# Verify result
RESULT_VAL=$(echo "$RESULT" | jq -r '.result')
[ "$RESULT_VAL" = "mock answer: smoke normal task" ] || fail "Unexpected result: $RESULT_VAL"

# Verify usage
USAGE=$(echo "$RESULT" | jq -r '.usage')
[ "$USAGE" != "null" ] || fail "Usage is null"
PROMPT=$(echo "$RESULT" | jq -r '.usage.prompt_tokens')
[ "$PROMPT" -gt 0 ] 2>/dev/null || fail "Invalid prompt_tokens: $PROMPT"

log "  Result: $RESULT_VAL"
log "  Usage: $USAGE"

# 4. Verify Postgres tasks
log "4. Checking Postgres tasks..."
PG_STATUS=$(psql_cmd "SELECT status FROM tasks WHERE id='$TASK_ID';")
echo "$PG_STATUS" | grep -q "completed" || fail "Postgres task not completed"
USAGE_TOKENS=$(psql_cmd "SELECT usage_total_tokens FROM tasks WHERE id='$TASK_ID';")
USAGE_TOKENS=$(echo "$USAGE_TOKENS" | tr -d ' ')
[ "$USAGE_TOKENS" -gt 0 ] 2>/dev/null || fail "Postgres usage_total_tokens not set: $USAGE_TOKENS"
log "  Postgres tasks: OK (usage_total_tokens=$USAGE_TOKENS)"

# 5. Verify Postgres executions
log "5. Checking Postgres executions..."
PG_EXEC=$(psql_cmd "SELECT status FROM executions WHERE task_id='$TASK_ID';")
echo "$PG_EXEC" | grep -q "completed" || fail "Postgres execution not completed"
log "  Postgres executions: OK"

# 6. Verify Postgres llm_calls
log "6. Checking Postgres llm_calls..."
LLM_COUNT=$(psql_cmd "SELECT count(*) FROM llm_calls WHERE task_id='$TASK_ID';" | tr -d ' ')
[ "$LLM_COUNT" -ge 1 ] || fail "No llm_calls record found"
LLM_TOKENS=$(psql_cmd "SELECT total_tokens FROM llm_calls WHERE task_id='$TASK_ID';" | tr -d ' ')
[ "$LLM_TOKENS" -gt 0 ] || fail "llm_calls total_tokens not set"
log "  llm_calls: OK (count=$LLM_COUNT, tokens=$LLM_TOKENS)"

# 7. Verify Redis Stream
log "7. Checking Redis Stream..."
EVENTS=$(redis_cmd XRANGE "task:$TASK_ID:events" - + | grep -c "event_type")
[ "$EVENTS" -ge 7 ] || fail "Redis Stream missing events (found $EVENTS, expected >=7)"

for ev in TASK_CREATED WORKFLOW_STARTED SESSION_LOADED LLM_STARTED LLM_COMPLETED USAGE_RECORDED TASK_COMPLETED; do
  COUNT=$(redis_cmd XRANGE "task:$TASK_ID:events" - + | grep -c "$ev")
  [ "$COUNT" -ge 1 ] || fail "Redis Stream missing event: $ev"
done
log "  Redis Stream: OK (all required events present)"

# 8. Session Memory test
log "8. Testing Session Memory (second task in same session)..."
RESP2=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{
    "query":"smoke second task",
    "session_id":"sess-smoke",
    "config":{
      "model":"gpt-4o-mini",
      "temperature":0.7,
      "max_total_tokens":8000,
      "max_completion_tokens":128
    }
  }')

TASK_ID2=$(echo "$RESP2" | jq -r '.task_id')
[ -n "$TASK_ID2" ] && [ "$TASK_ID2" != "null" ] || fail "Failed to create second task"

for i in $(seq 1 60); do
  STATUS2=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID2" | jq -r '.status')
  if [ "$STATUS2" = "completed" ]; then
    break
  elif [ "$STATUS2" = "failed" ] || [ "$STATUS2" = "budget_exceeded" ]; then
    fail "Second task failed with status: $STATUS2"
  fi
  sleep 1
done

# Verify session messages in Redis
MSG_COUNT=$(redis_cmd LLEN "session:sess-smoke:messages")
[ "$MSG_COUNT" -ge 4 ] || fail "Session messages count too low: $MSG_COUNT (expected >=4)"

TTL=$(redis_cmd TTL "session:sess-smoke:messages")
[ "$TTL" -gt 0 ] || fail "Session TTL not set"
log "  Session messages: OK (count=$MSG_COUNT, TTL=$TTL)"

# Verify SESSION_LOADED for second task has message_count > 0
SESSION_EV=$(redis_cmd XRANGE "task:$TASK_ID2:events" - + | grep -A5 "SESSION_LOADED" | head -10)
echo "$SESSION_EV" | grep -q "message_count" || fail "SESSION_LOADED event missing message_count"
log "  Second task SESSION_LOADED: OK"

# 9. Budget Exceeded test
log "9. Testing Budget Exceeded..."
RESP3=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{
    "query":"this task should exceed budget very easily",
    "session_id":"sess-budget",
    "config":{
      "model":"gpt-4o-mini",
      "temperature":0.7,
      "max_total_tokens":1,
      "max_completion_tokens":128
    }
  }')
[ -z "$RESP3" ] && fail "RESP3 is empty"

TASK_ID3=$(echo "$RESP3" | jq -r '.task_id')
[ -z "$TASK_ID3" ] || [ "$TASK_ID3" = "null" ] && fail "TASK_ID3 is empty: $RESP3"
log "  Budget task created: $TASK_ID3"

for i in $(seq 1 30); do
  STATUS3=$(curl --noproxy '*' --max-time 10 -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID3" | jq -r '.status')
  log "  Poll $i: $STATUS3"
  if [ "$STATUS3" = "budget_exceeded" ]; then
    break
  elif [ "$STATUS3" = "completed" ]; then
    fail "Budget task should not complete"
  fi
  sleep 1
done

[ "$STATUS3" = "budget_exceeded" ] || fail "Budget exceeded task has wrong status: $STATUS3"

log "  Checking error_type..."
ERROR_TYPE=$(curl --noproxy '*' --max-time 10 -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID3" | jq -r '.error_type')
log "  error_type: $ERROR_TYPE"
[ "$ERROR_TYPE" = "budget_exceeded" ] || fail "Wrong error_type: $ERROR_TYPE"

# Verify no llm_calls
log "  Checking llm_calls..."
LLM_COUNT3=$(psql_cmd "SELECT count(*) FROM llm_calls WHERE task_id='$TASK_ID3';" | tr -d ' ')
log "  llm_calls count: $LLM_COUNT3"
[ "$LLM_COUNT3" = "0" ] || fail "Budget exceeded task should have no llm_calls (found $LLM_COUNT3)"

# Verify Redis Stream
log "  Checking Redis TASK_BUDGET_EXCEEDED..."
BUDGET_EVENTS=$(redis_cmd XRANGE "task:$TASK_ID3:events" - + | grep -c "TASK_BUDGET_EXCEEDED")
log "  TASK_BUDGET_EXCEEDED count: $BUDGET_EVENTS"
[ "$BUDGET_EVENTS" -ge 1 ] || fail "Redis Stream missing TASK_BUDGET_EXCEEDED"

# Should NOT contain LLM_STARTED or LLM_COMPLETED
log "  Checking no LLM_STARTED..."
LLM_STARTED_COUNT=$(redis_cmd XRANGE "task:$TASK_ID3:events" - + 2>/dev/null | grep -c "LLM_STARTED" || true)
LLM_STARTED_COUNT="${LLM_STARTED_COUNT:-0}"
log "  LLM_STARTED count: $LLM_STARTED_COUNT"
[ "$LLM_STARTED_COUNT" = "0" ] || fail "Budget exceeded task should not have LLM_STARTED"
log "  Budget Exceeded: OK (status=$STATUS3, error_type=$ERROR_TYPE, llm_calls=0)"

# 10. SSE Test (optional, if test_sse.sh exists)
if [ -x "$(dirname "$0")/test_sse.sh" ]; then
  log "10. Running SSE test..."
  bash "$(dirname "$0")/test_sse.sh" || fail "SSE test failed"
  log "  SSE: OK"
fi

echo ""
echo "=== ALL MVP SMOKE TESTS PASSED ==="
echo ""
echo "Summary:"
echo "  Normal task: $TASK_ID (completed, usage=$USAGE_TOKENS)"
echo "  Session task: $TASK_ID2 (completed, session msgs=$MSG_COUNT)"
echo "  Budget task: $TASK_ID3 (budget_exceeded, no LLM call)"
