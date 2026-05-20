#!/bin/bash
set -euo pipefail

# Source common test helpers
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/test_helpers.sh"

log() { echo "[$(date +'%H:%M:%S')] $*"; }
fail() { echo "[FAIL] $*" >&2; exit 1; }

log "=== DAG Synthesis Smoke Test Starting ==="

log "1. Creating DAG synthesis task..."
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{
    "query": "analyze this research problem and synthesize a final answer",
    "session_id": "sess-dag-synthesis",
    "config": {
      "mode": "dag",
      "model": "gpt-4o-mini",
      "temperature": 0.7,
      "max_total_tokens": 16000,
      "max_completion_tokens": 128
    }
  }')

TASK_ID=$(echo "$RESP" | jq -r '.task_id')
[ -n "$TASK_ID" ] && [ "$TASK_ID" != "null" ] || fail "Failed to create DAG task"

log "  Task created: $TASK_ID"

STATUS=$(wait_for_task_terminal "$TASK_ID")
[ "$STATUS" = "completed" ] || fail "DAG task expected completed, got $STATUS"
log "  Task completed: $STATUS"

sleep 2

log "2. Checking result..."
RESULT=$(get_task_result "$TASK_ID")
echo "$RESULT" | grep -q "helpful response" || fail "Result does not contain helpful response: $RESULT"
log "  Result: OK (helpful response present)"

log "3. Checking Redis events for task_id=$TASK_ID..."

# Count DAG node events (should be 2)
DAG_NODE_STARTED=$(count_task_event "$TASK_ID" "DAG_NODE_STARTED")
[ "$DAG_NODE_STARTED" -ge 2 ] || fail "Expected at least 2 DAG_NODE_STARTED (found $DAG_NODE_STARTED)"
log "  DAG_NODE_STARTED count: $DAG_NODE_STARTED"

DAG_NODE_COMPLETED=$(count_task_event "$TASK_ID" "DAG_NODE_COMPLETED")
[ "$DAG_NODE_COMPLETED" -ge 2 ] || fail "Expected at least 2 DAG_NODE_COMPLETED (found $DAG_NODE_COMPLETED)"
log "  DAG_NODE_COMPLETED count: $DAG_NODE_COMPLETED"

LLM_STARTED=$(count_task_event "$TASK_ID" "LLM_STARTED")
[ "$LLM_STARTED" -ge 1 ] || fail "Expected at least 1 LLM_STARTED (found $LLM_STARTED)"
log "  LLM_STARTED count: $LLM_STARTED"

LLM_COMPLETED=$(count_task_event "$TASK_ID" "LLM_COMPLETED")
[ "$LLM_COMPLETED" -ge 1 ] || fail "Expected at least 1 LLM_COMPLETED (found $LLM_COMPLETED)"
log "  LLM_COMPLETED count: $LLM_COMPLETED"

DAG_SYNTHESIZED=$(count_task_event "$TASK_ID" "DAG_SYNTHESIZED")
[ "$DAG_SYNTHESIZED" -ge 1 ] || fail "Expected at least 1 DAG_SYNTHESIZED (found $DAG_SYNTHESIZED)"
log "  DAG_SYNTHESIZED count: $DAG_SYNTHESIZED"

TASK_COMPLETED=$(count_task_event "$TASK_ID" "TASK_COMPLETED")
[ "$TASK_COMPLETED" -ge 1 ] || fail "Expected at least 1 TASK_COMPLETED (found $TASK_COMPLETED)"
log "  TASK_COMPLETED count: $TASK_COMPLETED"

for event in WORKFLOW_STARTED SESSION_LOADED TASK_CLASSIFIED DAG_PLANNED USAGE_RECORDED; do
  COUNT=$(count_task_event "$TASK_ID" "$event")
  [ "$COUNT" -ge 1 ] || fail "Missing required event: $event (count=$COUNT)"
  log "  $event count: $COUNT"
done

# 4. Check event ordering (key ordering checks)
log "4. Checking event ordering..."
check_event_ordering "$TASK_ID" "TASK_CREATED" "WORKFLOW_STARTED" || fail "Event ordering: TASK_CREATED should come before WORKFLOW_STARTED"
check_event_ordering "$TASK_ID" "WORKFLOW_STARTED" "SESSION_LOADED" || fail "Event ordering: WORKFLOW_STARTED should come before SESSION_LOADED"
check_event_ordering "$TASK_ID" "DAG_PLANNED" "DAG_NODE_STARTED" || fail "Event ordering: DAG_PLANNED should come before DAG_NODE_STARTED"
check_event_ordering "$TASK_ID" "DAG_NODE_COMPLETED" "DAG_SYNTHESIZED" || fail "Event ordering: DAG_NODE_COMPLETED should come before DAG_SYNTHESIZED"
check_event_ordering "$TASK_ID" "DAG_SYNTHESIZED" "TASK_COMPLETED" || fail "Event ordering: DAG_SYNTHESIZED should come before TASK_COMPLETED"
log "  Event ordering: OK"

log "5. Checking llm_calls..."
LLM_COUNT=$(count_llm_calls "$TASK_ID")
[ "$LLM_COUNT" -ge 1 ] || fail "Expected at least 1 llm_calls record (found $LLM_COUNT)"
log "  llm_calls count: $LLM_COUNT"

DRAFT_LLM=$(count_llm_calls_by_node "$TASK_ID" "draft_answer")
[ "$DRAFT_LLM" -ge 1 ] || fail "Expected at least 1 llm_calls for draft_answer (found $DRAFT_LLM)"
log "  llm_calls with node_id=draft_answer: $DRAFT_LLM"

ANALYZE_LLM=$(count_llm_calls_by_node "$TASK_ID" "analyze_input")
[ "$ANALYZE_LLM" = "0" ] || fail "Expected 0 llm_calls for analyze_input (found $ANALYZE_LLM)"
log "  llm_calls with node_id=analyze_input: $ANALYZE_LLM"

echo ""
echo "=== ALL DAG SYNTHESIS TESTS PASSED ==="
echo ""
echo "Summary:"
echo "  Task ID: $TASK_ID"
echo "  Status: $STATUS"
echo "  DAG_NODE_STARTED: $DAG_NODE_STARTED"
echo "  DAG_NODE_COMPLETED: $DAG_NODE_COMPLETED"
echo "  LLM_STARTED: $LLM_STARTED"
echo "  LLM_COMPLETED: $LLM_COMPLETED"
echo "  DAG_SYNTHESIZED: $DAG_SYNTHESIZED"
echo "  TASK_COMPLETED: $TASK_COMPLETED"
echo "  llm_calls: $LLM_COUNT"
echo "  llm_calls draft_answer: $DRAFT_LLM"
echo "  llm_calls analyze_input: $ANALYZE_LLM"