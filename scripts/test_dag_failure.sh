#!/bin/bash
set -euo pipefail

# Source common test helpers
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/test_helpers.sh"

log() { echo "[$(date +'%H:%M:%S')] $*"; }
fail() { echo "[FAIL] $*" >&2; exit 1; }

log "=== DAG Failure Path Test Starting ==="

# Create a DAG mode task with special query that triggers failure
# The query contains "__force_dag_node_failure__" which should trigger the debug hook
log "1. Creating DAG failure task..."
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{
    "query": "test __force_dag_node_failure__",
    "session_id": "sess-dag-failure",
    "config": {
      "mode": "dag",
      "model": "gpt-4o-mini",
      "max_total_tokens": 16000,
      "max_completion_tokens": 128
    }
  }')

TASK_ID=$(echo "$RESP" | jq -r '.task_id')
[ -n "$TASK_ID" ] && [ "$TASK_ID" != "null" ] || fail "Failed to create DAG task"

log "  Task created: $TASK_ID"

# Poll until terminal
STATUS=$(wait_for_task_terminal "$TASK_ID" 60)
log "  Task status: $STATUS"

sleep 2

# Check that task failed
log "2. Checking status..."
[[ "$STATUS" == "failed" ]] || fail "Expected failed status, got $STATUS"

# Check error type
ERROR_TYPE=$(get_task_error_type "$TASK_ID")
log "  Error type: $ERROR_TYPE"

log "3. Checking Redis events for task_id=$TASK_ID..."

# Should have DAG_NODE_STARTED (at least one node started before failure)
DAG_NODE_STARTED=$(count_task_event "$TASK_ID" "DAG_NODE_STARTED")
log "  DAG_NODE_STARTED count: $DAG_NODE_STARTED"
[ "$DAG_NODE_STARTED" -ge 1 ] || fail "Expected at least 1 DAG_NODE_STARTED (found $DAG_NODE_STARTED)"

# Should NOT have successful DAG_SYNTHESIZED
DAG_SYNTHESIZED=$(count_task_event "$TASK_ID" "DAG_SYNTHESIZED")
log "  DAG_SYNTHESIZED count: $DAG_SYNTHESIZED"
[ "$DAG_SYNTHESIZED" = "0" ] || fail "Should not have DAG_SYNTHESIZED for failed task (found $DAG_SYNTHESIZED)"

# Should NOT have TASK_COMPLETED for failed task
TASK_COMPLETED=$(count_task_event "$TASK_ID" "TASK_COMPLETED")
log "  TASK_COMPLETED count: $TASK_COMPLETED"
[ "$TASK_COMPLETED" = "0" ] || fail "Should not have TASK_COMPLETED for failed task (found $TASK_COMPLETED)"

# Should have TASK_FAILED or equivalent failure event
TASK_FAILED=$(count_task_event "$TASK_ID" "TASK_FAILED")
log "  TASK_FAILED count: $TASK_FAILED"
[ "$TASK_FAILED" -ge 1 ] || fail "Expected at least 1 TASK_FAILED (found $TASK_FAILED)"

# Check that the node that failed has status=failed in DAG_NODE_COMPLETED
log "4. Checking failed node status..."
EVENTS=$(get_task_events "$TASK_ID")
FAILED_NODE=$(echo "$EVENTS" | grep -E '"event_type":"DAG_NODE_COMPLETED"' | grep '"status":"failed"' || true)
if [ -n "$FAILED_NODE" ]; then
  log "  Found failed node in DAG_NODE_COMPLETED"
else
  log "  Note: No failed DAG_NODE_COMPLETED found - checking if failure occurred before node completion"
fi

# Should NOT have llm_calls for failed task
LLM_COUNT=$(count_llm_calls "$TASK_ID")
log "  llm_calls count: $LLM_COUNT"
[ "$LLM_COUNT" = "0" ] || fail "Should not have llm_calls for failed task (found $LLM_COUNT)"

echo ""
echo "=== ALL DAG FAILURE PATH TESTS PASSED ==="
echo ""
echo "Summary:"
echo "  Task ID: $TASK_ID"
echo "  Status: $STATUS"
echo "  Error type: $ERROR_TYPE"
echo "  DAG_NODE_STARTED: $DAG_NODE_STARTED"
echo "  DAG_SYNTHESIZED: $DAG_SYNTHESIZED"
echo "  TASK_COMPLETED: $TASK_COMPLETED"
echo "  TASK_FAILED: $TASK_FAILED"
echo "  llm_calls: $LLM_COUNT"