#!/bin/bash
set -euo pipefail

# Source common test helpers
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/test_helpers.sh"

log() { echo "[$(date +'%H:%M:%S')] $*"; }
fail() { echo "[FAIL] $*" >&2; exit 1; }

log "=== DAG LLM Execution Smoke Test Starting ==="

# Create a DAG mode task with LLM node
log "1. Creating DAG LLM task..."
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{
    "query": "analyze this research problem and draft a final answer with llm",
    "session_id": "sess-dag-llm",
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

# Poll until completed
STATUS=$(wait_for_task_terminal "$TASK_ID")
[ "$STATUS" = "completed" ] || fail "DAG task expected completed, got $STATUS"
log "  Task completed: $STATUS"

# Wait for events to be fully written to Redis
sleep 2

# 2. Verify result contains helpful response
log "2. Checking result..."
RESULT=$(get_task_result "$TASK_ID")
echo "$RESULT" | grep -q "helpful response" || fail "Result does not contain helpful response: $RESULT"
log "  Result: OK (helpful response present)"

# 3. Verify Redis events for this task_id
log "3. Checking Redis events for task_id=$TASK_ID..."

# Count DAG node events (should be 2)
DAG_NODE_STARTED=$(count_task_event "$TASK_ID" "DAG_NODE_STARTED")
log "  DAG_NODE_STARTED count: $DAG_NODE_STARTED"
[ "$DAG_NODE_STARTED" -ge 2 ] || fail "Expected at least 2 DAG_NODE_STARTED (found $DAG_NODE_STARTED)"

DAG_NODE_COMPLETED=$(count_task_event "$TASK_ID" "DAG_NODE_COMPLETED")
log "  DAG_NODE_COMPLETED count: $DAG_NODE_COMPLETED"
[ "$DAG_NODE_COMPLETED" -ge 2 ] || fail "Expected at least 2 DAG_NODE_COMPLETED (found $DAG_NODE_COMPLETED)"

# Count LLM events (should be 1)
LLM_STARTED=$(count_task_event "$TASK_ID" "LLM_STARTED")
log "  LLM_STARTED count: $LLM_STARTED"
[ "$LLM_STARTED" -ge 1 ] || fail "Expected at least 1 LLM_STARTED (found $LLM_STARTED)"

LLM_COMPLETED=$(count_task_event "$TASK_ID" "LLM_COMPLETED")
log "  LLM_COMPLETED count: $LLM_COMPLETED"
[ "$LLM_COMPLETED" -ge 1 ] || fail "Expected at least 1 LLM_COMPLETED (found $LLM_COMPLETED)"

# Verify required events exist
log "4. Checking required events..."
for event in WORKFLOW_STARTED SESSION_LOADED TASK_CLASSIFIED DAG_PLANNED USAGE_RECORDED TASK_COMPLETED DAG_SYNTHESIZED; do
  count=$(count_task_event "$TASK_ID" "$event")
  [ "$count" -ge 1 ] || fail "Missing required event: $event (count=$count)"
  log "  $event count: $count"
done

# 5. Verify llm_calls records
log "5. Checking llm_calls for task_id=$TASK_ID..."
LLM_COUNT=$(count_llm_calls "$TASK_ID")
log "  llm_calls total count: $LLM_COUNT"
[ "$LLM_COUNT" -ge 1 ] || fail "Expected at least 1 llm_calls record (found $LLM_COUNT)"

# Check node_id='draft_answer' has 1 record
DRAFT_LLM=$(count_llm_calls_by_node "$TASK_ID" "draft_answer")
log "  llm_calls with node_id=draft_answer: $DRAFT_LLM"
[ "$DRAFT_LLM" -ge 1 ] || fail "Expected at least 1 llm_calls for draft_answer (found $DRAFT_LLM)"

# Check analyze_input has 0 records
ANALYZE_LLM=$(count_llm_calls_by_node "$TASK_ID" "analyze_input")
log "  llm_calls with node_id=analyze_input: $ANALYZE_LLM"
[ "$ANALYZE_LLM" = "0" ] || fail "Expected 0 llm_calls for analyze_input (found $ANALYZE_LLM)"

echo ""
echo "=== ALL DAG LLM EXECUTION TESTS PASSED ==="
echo ""
echo "Summary:"
echo "  Task ID: $TASK_ID"
echo "  Status: $STATUS"
echo "  DAG_NODE_STARTED: $DAG_NODE_STARTED"
echo "  DAG_NODE_COMPLETED: $DAG_NODE_COMPLETED"
echo "  LLM_STARTED: $LLM_STARTED"
echo "  LLM_COMPLETED: $LLM_COMPLETED"
echo "  DAG_SYNTHESIZED: $(count_task_event "$TASK_ID" "DAG_SYNTHESIZED")"
echo "  llm_calls total: $LLM_COUNT"
echo "  llm_calls draft_answer: $DRAFT_LLM"
echo "  llm_calls analyze_input: $ANALYZE_LLM"