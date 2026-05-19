#!/bin/bash
set -euo pipefail

# Source common test helpers
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/test_helpers.sh"

log() { echo "[$(date +'%H:%M:%S')] $*"; }
fail() { echo "[FAIL] $*" >&2; exit 1; }

log "=== DAG Synthesis Budget Smoke Test Starting ==="

log "1. Creating DAG synthesis budget task..."
# Use a long query to ensure budget check is triggered
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{
    "query": "analyze and synthesize aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "session_id": "sess-dag-synthesis-budget",
    "config": {
      "mode": "dag",
      "model": "gpt-4o-mini",
      "temperature": 0.7,
      "max_total_tokens": 1,
      "max_completion_tokens": 1
    }
  }')

TASK_ID=$(echo "$RESP" | jq -r '.task_id')
[ -n "$TASK_ID" ] && [ "$TASK_ID" != "null" ] || fail "Failed to create DAG task"

log "  Task created: $TASK_ID"

STATUS=$(wait_for_task_terminal "$TASK_ID" 30)
log "  Task status: $STATUS"

[[ "$STATUS" == "budget_exceeded" ]] || [[ "$STATUS" == "failed" ]] || fail "Expected budget_exceeded or failed, got $STATUS"

log "2. Checking error type..."
ERROR_TYPE=$(get_task_error_type "$TASK_ID")
log "  Error type: $ERROR_TYPE"

log "3. Checking Redis events for task_id=$TASK_ID..."

# Should NOT have LLM_STARTED for budget exceeded
LLM_STARTED=$(count_task_event "$TASK_ID" "LLM_STARTED")
log "  LLM_STARTED count: $LLM_STARTED"
[ "$LLM_STARTED" = "0" ] || fail "Should not have LLM_STARTED for budget exceeded (found $LLM_STARTED)"

# Should NOT have LLM_COMPLETED for budget exceeded
LLM_COMPLETED=$(count_task_event "$TASK_ID" "LLM_COMPLETED")
log "  LLM_COMPLETED count: $LLM_COMPLETED"
[ "$LLM_COMPLETED" = "0" ] || fail "Should not have LLM_COMPLETED for budget exceeded (found $LLM_COMPLETED)"

# Should NOT have DAG_SYNTHESIZED for budget exceeded
DAG_SYNTHESIZED=$(count_task_event "$TASK_ID" "DAG_SYNTHESIZED")
log "  DAG_SYNTHESIZED count: $DAG_SYNTHESIZED"
[ "$DAG_SYNTHESIZED" = "0" ] || fail "Should not have DAG_SYNTHESIZED for budget exceeded (found $DAG_SYNTHESIZED)"

# Should NOT have TASK_COMPLETED for budget exceeded
TASK_COMPLETED=$(count_task_event "$TASK_ID" "TASK_COMPLETED")
log "  TASK_COMPLETED count: $TASK_COMPLETED"
[ "$TASK_COMPLETED" = "0" ] || fail "Should not have TASK_COMPLETED for budget exceeded (found $TASK_COMPLETED)"

# Should have TASK_BUDGET_EXCEEDED
BUDGET=$(count_task_event "$TASK_ID" "TASK_BUDGET_EXCEEDED")
log "  TASK_BUDGET_EXCEEDED count: $BUDGET"
[ "$BUDGET" -ge 1 ] || fail "Expected at least 1 TASK_BUDGET_EXCEEDED (found $BUDGET)"

log "4. Checking llm_calls..."
LLM_COUNT=$(count_llm_calls "$TASK_ID")
log "  llm_calls count: $LLM_COUNT"
[ "$LLM_COUNT" = "0" ] || fail "Should not have llm_calls for budget exceeded (found $LLM_COUNT)"

echo ""
echo "=== ALL DAG SYNTHESIS BUDGET TESTS PASSED ==="
echo ""
echo "Summary:"
echo "  Task ID: $TASK_ID"
echo "  Status: $STATUS"
echo "  Error type: $ERROR_TYPE"
echo "  LLM_STARTED: $LLM_STARTED"
echo "  LLM_COMPLETED: $LLM_COMPLETED"
echo "  DAG_SYNTHESIZED: $DAG_SYNTHESIZED"
echo "  TASK_COMPLETED: $TASK_COMPLETED"
echo "  TASK_BUDGET_EXCEEDED: $BUDGET"
echo "  llm_calls: $LLM_COUNT"