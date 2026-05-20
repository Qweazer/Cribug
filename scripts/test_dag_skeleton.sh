#!/bin/bash
set -euo pipefail

# Source common test helpers
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/test_helpers.sh"

log() { echo "[$(date +'%H:%M:%S')] $*"; }
fail() { echo "[FAIL] $*" >&2; exit 1; }

log "=== DAG Skeleton Smoke Test Starting ==="

# Create a DAG mode task with simple query
log "1. Creating DAG mode task..."
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{
    "query": "hello world",
    "session_id": "sess-dag-skeleton",
    "config": {
      "mode": "dag",
      "max_total_tokens": 16000
    }
  }')

TASK_ID=$(echo "$RESP" | jq -r '.task_id')
[ -n "$TASK_ID" ] && [ "$TASK_ID" != "null" ] || fail "Failed to create DAG task"

log "  Task created: $TASK_ID"

# Poll until completed
STATUS=$(wait_for_task_terminal "$TASK_ID")
[ "$STATUS" = "completed" ] || fail "DAG task expected completed, got $STATUS"
log "  Task completed: $STATUS"

# Wait for events to be fully written to Redis
sleep 2

# 2. Verify result contains DAG completion
log "2. Checking result..."
RESULT=$(get_task_result "$TASK_ID")
# 4.6+: result contains helpful response from LLM node synthesis
if echo "$RESULT" | grep -qE "helpful response"; then
  log "  Result: OK (DAG mode - helpful response)"
else
  fail "Result does not contain helpful response: $RESULT"
fi

# 3. Verify core events exist
log "3. Checking core events..."
for event in WORKFLOW_STARTED SESSION_LOADED TASK_COMPLETED; do
  count=$(count_task_event "$TASK_ID" "$event")
  [ "$count" -ge 1 ] || fail "Missing $event event (count=$count)"
  log "  $event count: $count"
done

# 4. Verify DAG events exist
log "4. Checking DAG events..."
for event in DAG_PLANNED; do
  count=$(count_task_event "$TASK_ID" "$event")
  [ "$count" -ge 1 ] || fail "Missing $event event (count=$count)"
  log "  $event count: $count"
done

# 5. Verify DAG node execution events (2 nodes)
log "5. Checking DAG node events..."
DAG_NODE_STARTED=$(count_task_event "$TASK_ID" "DAG_NODE_STARTED")
[ "$DAG_NODE_STARTED" -ge 2 ] || fail "Expected at least 2 DAG_NODE_STARTED (found $DAG_NODE_STARTED)"
log "  DAG_NODE_STARTED count: $DAG_NODE_STARTED"

DAG_NODE_COMPLETED=$(count_task_event "$TASK_ID" "DAG_NODE_COMPLETED")
[ "$DAG_NODE_COMPLETED" -ge 2 ] || fail "Expected at least 2 DAG_NODE_COMPLETED (found $DAG_NODE_COMPLETED)"
log "  DAG_NODE_COMPLETED count: $DAG_NODE_COMPLETED"

# 6. Verify LLM events (one LLM node in DAG)
log "6. Checking LLM events..."
LLM_STARTED=$(count_task_event "$TASK_ID" "LLM_STARTED")
[ "$LLM_STARTED" -ge 1 ] || fail "Expected at least 1 LLM_STARTED (found $LLM_STARTED)"
log "  LLM_STARTED count: $LLM_STARTED"

LLM_COMPLETED=$(count_task_event "$TASK_ID" "LLM_COMPLETED")
[ "$LLM_COMPLETED" -ge 1 ] || fail "Expected at least 1 LLM_COMPLETED (found $LLM_COMPLETED)"
log "  LLM_COMPLETED count: $LLM_COMPLETED"

# 7. Verify llm_calls records
log "7. Checking llm_calls..."
LLM_COUNT=$(count_llm_calls "$TASK_ID")
[ "$LLM_COUNT" -ge 1 ] || fail "Expected at least 1 llm_calls record"
log "  llm_calls count: $LLM_COUNT"

# 8. Verify DAG_SYNTHESIZED event (4.6+)
log "8. Checking DAG_SYNTHESIZED event..."
DAG_SYNTHESIZED=$(count_task_event "$TASK_ID" "DAG_SYNTHESIZED")
[ "$DAG_SYNTHESIZED" -ge 1 ] || fail "Expected at least 1 DAG_SYNTHESIZED (found $DAG_SYNTHESIZED)"
log "  DAG_SYNTHESIZED count: $DAG_SYNTHESIZED"

echo ""
echo "=== ALL DAG SKELETON TESTS PASSED ==="
echo ""
echo "Summary:"
echo "  Task ID: $TASK_ID"
echo "  Status: $STATUS"
echo "  DAG_NODE_STARTED: $DAG_NODE_STARTED"
echo "  DAG_NODE_COMPLETED: $DAG_NODE_COMPLETED"
echo "  LLM_STARTED: $LLM_STARTED"
echo "  LLM_COMPLETED: $LLM_COMPLETED"
echo "  DAG_SYNTHESIZED: $DAG_SYNTHESIZED"
echo "  llm_calls: $LLM_COUNT"