#!/bin/bash
# Test multi-agent failure path
# Tests: forced failure with __force_multi_agent_failure__

set -euo pipefail

# Source test helpers
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/test_helpers.sh"

GATEWAY_URL="${GATEWAY_URL:-http://127.0.0.1:8080}"
REDIS_HOST="${REDIS_HOST:-127.0.0.1}"
REDIS_PORT="${REDIS_PORT:-6379}"

log() { echo "[$(date +'%H:%M:%S')] $*" ; }
pass() { echo "  [PASS] $*" ; }
fail() { echo "[FAIL] $*" >&2; exit 1; }

echo ""
echo "=== Multi-Agent Failure Test ==="
echo ""

# Submit multi-agent task with forced failure trigger
log "Creating forced failure task..."

task_id=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{
    "query": "test __force_multi_agent_failure__ trigger",
    "session_id": "sess-multi-agent-failure",
    "config": {
      "mode": "multi_agent",
      "model": "gpt-4o-mini",
      "max_total_tokens": 16000
    }
  }' | jq -r '.task_id')

if [[ -z "$task_id" ]] || [[ "$task_id" == "null" ]]; then
  fail "Failed to create task"
fi

log "Task created: $task_id"

# Wait for task to reach terminal state
status=$(wait_for_task_terminal "$task_id" 90)
log "Task status: $status"

if [[ "$status" != "failed" ]]; then
  fail "Expected task failed, got $status"
fi

pass "Task failed as expected"
echo ""

# Verify failure events
log "Verifying failure events..."

# TASK_FAILED count = 1
count=$(count_task_event "$task_id" "TASK_FAILED")
if [[ "$count" != "1" ]]; then fail "Expected 1 TASK_FAILED, got $count"; fi
pass "TASK_FAILED count = 1"

# MULTI_AGENT_SYNTHESIZED count = 0
count=$(count_task_event "$task_id" "MULTI_AGENT_SYNTHESIZED")
if [[ "$count" != "0" ]]; then fail "Expected 0 MULTI_AGENT_SYNTHESIZED (failure), got $count"; fi
pass "MULTI_AGENT_SYNTHESIZED count = 0 (failure blocked)"

# TASK_COMPLETED count = 0
count=$(count_task_event "$task_id" "TASK_COMPLETED")
if [[ "$count" != "0" ]]; then fail "Expected 0 TASK_COMPLETED (failure), got $count"; fi
pass "TASK_COMPLETED count = 0 (failure blocked)"

# No LLM events since failure happens before LLM
# LLM_STARTED count = 0
count=$(count_task_event "$task_id" "LLM_STARTED")
if [[ "$count" != "0" ]]; then fail "Expected 0 LLM_STARTED (failure before LLM), got $count"; fi
pass "LLM_STARTED count = 0 (failure before LLM)"

# No llm_calls
llm_count=$(count_llm_calls "$task_id")
if [[ "$llm_count" != "0" ]]; then fail "Expected 0 llm_calls (failure before LLM), got $llm_count"; fi
pass "llm_calls count = 0 (failure before LLM)"

echo ""
log "All failure assertions passed for task_id=$task_id"
echo ""
echo "=== Multi-Agent Failure Test PASSED ==="