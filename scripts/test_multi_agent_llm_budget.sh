#!/bin/bash
# Test multi-agent budget exceeded
# Tests: max_total_tokens=1 should prevent LLM call

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
echo "=== Multi-Agent LLM Budget Test ==="
echo ""

# Submit multi-agent task with tiny budget
log "Creating multi-agent budget task..."

task_id=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{
    "query": "multi agent budget test task",
    "session_id": "sess-multi-agent-budget",
    "config": {
      "mode": "multi_agent",
      "model": "gpt-4o-mini",
      "temperature": 0.7,
      "max_total_tokens": 1,
      "max_completion_tokens": 1
    }
  }' | jq -r '.task_id')

if [[ -z "$task_id" ]] || [[ "$task_id" == "null" ]]; then
  fail "Failed to create task"
fi

log "Task created: $task_id"

# Wait for task to reach terminal state
status=$(wait_for_task_terminal "$task_id" 90)
log "Task status: $status"

# Accept both budget_exceeded and failed with budget error
if [[ "$status" == "budget_exceeded" ]]; then
  pass "Task status is budget_exceeded"
elif [[ "$status" == "failed" ]]; then
  # Check error_type
  error_type=$(get_task_error_type "$task_id")
  if [[ "$error_type" == "budget_exceeded" ]]; then
    pass "Task status is failed with budget_exceeded error_type"
  else
    fail "Expected budget_exceeded error_type, got $error_type"
  fi
else
  fail "Expected budget_exceeded or failed, got $status"
fi

echo ""

# Verify no LLM events (budget should prevent LLM call)
log "Verifying budget protection (no LLM events)..."

# LLM_STARTED count = 0
count=$(count_task_event "$task_id" "LLM_STARTED")
if [[ "$count" != "0" ]]; then fail "Expected 0 LLM_STARTED (budget blocked), got $count"; fi
pass "LLM_STARTED count = 0 (budget blocked)"

# LLM_COMPLETED count = 0
count=$(count_task_event "$task_id" "LLM_COMPLETED")
if [[ "$count" != "0" ]]; then fail "Expected 0 LLM_COMPLETED (budget blocked), got $count"; fi
pass "LLM_COMPLETED count = 0 (budget blocked)"

# USAGE_RECORDED count = 0
count=$(count_task_event "$task_id" "USAGE_RECORDED")
if [[ "$count" != "0" ]]; then fail "Expected 0 USAGE_RECORDED (budget blocked), got $count"; fi
pass "USAGE_RECORDED count = 0 (budget blocked)"

# MULTI_AGENT_SYNTHESIZED count = 0
count=$(count_task_event "$task_id" "MULTI_AGENT_SYNTHESIZED")
if [[ "$count" != "0" ]]; then fail "Expected 0 MULTI_AGENT_SYNTHESIZED (budget blocked), got $count"; fi
pass "MULTI_AGENT_SYNTHESIZED count = 0 (budget blocked)"

# TASK_COMPLETED count = 0
count=$(count_task_event "$task_id" "TASK_COMPLETED")
if [[ "$count" != "0" ]]; then fail "Expected 0 TASK_COMPLETED (budget blocked), got $count"; fi
pass "TASK_COMPLETED count = 0 (budget blocked)"

# TASK_BUDGET_EXCEEDED count = 1
count=$(count_task_event "$task_id" "TASK_BUDGET_EXCEEDED")
if [[ "$count" != "1" ]]; then fail "Expected 1 TASK_BUDGET_EXCEEDED, got $count"; fi
pass "TASK_BUDGET_EXCEEDED count = 1"

# Check no llm_calls
llm_count=$(count_llm_calls "$task_id")
if [[ "$llm_count" != "0" ]]; then fail "Expected 0 llm_calls (budget blocked), got $llm_count"; fi
pass "llm_calls count = 0 (budget blocked)"

echo ""
log "All budget assertions passed for task_id=$task_id"
echo ""
echo "=== Multi-Agent LLM Budget Test PASSED ==="