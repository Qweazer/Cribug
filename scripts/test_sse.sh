#!/bin/bash
set -euo pipefail

GATEWAY_URL="${GATEWAY_URL:-http://127.0.0.1:8080}"

echo "=== SSE Test ==="

# Create a new task
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{
    "query":"sse smoke test task",
    "session_id":"sess-sse-test",
    "config":{
      "model":"gpt-4o-mini",
      "temperature":0.7,
      "max_total_tokens":8000,
      "max_completion_tokens":128
    }
  }')

echo "Task response: $RESP"
TASK_ID=$(echo "$RESP" | jq -r '.task_id')

if [ -z "$TASK_ID" ] || [ "$TASK_ID" = "null" ]; then
  echo "ERROR: Failed to create task"
  exit 1
fi

echo "Task ID: $TASK_ID"
SSE_LOG="/tmp/sse_${TASK_ID}.log"

# Start SSE in background - wait longer for all events
# Use stdbuf to disable buffering
stdbuf -oL curl --noproxy '*' -N --silent "$GATEWAY_URL/api/v1/stream/sse?task_id=$TASK_ID" > "$SSE_LOG" 2>&1 &
SSE_PID=$!

# Poll task until completed
COMPLETED=false
for i in $(seq 1 60); do
  STATUS=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID" | jq -r '.status')
  echo "Poll $i: $STATUS"

  if [ "$STATUS" = "completed" ] || [ "$STATUS" = "failed" ] || [ "$STATUS" = "budget_exceeded" ]; then
    COMPLETED=true
    # Wait for SSE to receive all events
    sleep 2
    kill $SSE_PID 2>/dev/null || true
    break
  fi
  sleep 1
done

if [ "$COMPLETED" != "true" ]; then
  kill $SSE_PID 2>/dev/null || true
fi

# Check SSE output
echo ""
echo "=== SSE Output ==="
cat "$SSE_LOG"

echo ""
echo "=== SSE Validation ==="

# Check for required events
REQUIRED_EVENTS=("TASK_CREATED" "WORKFLOW_STARTED" "SESSION_LOADED" "LLM_STARTED" "LLM_COMPLETED" "USAGE_RECORDED" "TASK_COMPLETED")

ALL_PASS=true
for event in "${REQUIRED_EVENTS[@]}"; do
  if grep -q "event: $event" "$SSE_LOG"; then
    echo "  [PASS] $event found"
  else
    echo "  [FAIL] $event NOT found"
    ALL_PASS=false
  fi
done

# Check task completion in SSE (should have TASK_COMPLETED or TASK_BUDGET_EXCEEDED)
if grep -q "TASK_COMPLETED" "$SSE_LOG"; then
  echo "  [PASS] TASK_COMPLETED event received"
elif grep -q "TASK_BUDGET_EXCEEDED" "$SSE_LOG"; then
  echo "  [PASS] TASK_BUDGET_EXCEEDED event received"
else
  echo "  [FAIL] No terminal event (TASK_COMPLETED/TASK_BUDGET_EXCEEDED) in SSE"
  ALL_PASS=false
fi

if [ "$ALL_PASS" = true ]; then
  echo ""
  echo "=== SSE TEST PASSED ==="
  rm -f "$SSE_LOG"
  exit 0
else
  echo ""
  echo "=== SSE TEST FAILED ==="
  rm -f "$SSE_LOG"
  exit 1
fi
