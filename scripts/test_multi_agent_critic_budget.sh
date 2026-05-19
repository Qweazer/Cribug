#!/bin/bash
# Test multi-agent Critic Budget exceeded
# Tests: budget check blocks critic LLM call (Slice 5.3)

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
echo "=== Multi-Agent Critic Budget Test (Slice 5.3) ==="
echo ""

# Submit multi-agent task with very small budget that critic will exceed
log "Creating multi-agent task with small budget for critic budget test..."

task_id=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{
    "query": "critic budget test query",
    "session_id": "sess-critic-budget-test",
    "config": {
      "mode": "multi_agent",
      "model": "gpt-4o-mini",
      "temperature": 0.7,
      "max_total_tokens": 10,
      "max_completion_tokens": 5
    }
  }' | jq -r '.task_id')

if [[ -z "$task_id" ]] || [[ "$task_id" == "null" ]]; then
  fail "Failed to create task"
fi

log "Task created: $task_id"

# Wait for task to reach terminal state
status=$(wait_for_task_terminal "$task_id" 90)
log "Task status: $status"

if [[ "$status" != "budget_exceeded" ]]; then
  fail "Expected task budget_exceeded, got $status"
fi

pass "Task budget_exceeded as expected"
echo ""

# Verify failure events
log "Verifying budget exceeded events..."

# TASK_BUDGET_EXCEEDED count = 1
count=$(count_task_event "$task_id" "TASK_BUDGET_EXCEEDED")
if [[ "$count" != "1" ]]; then fail "Expected 1 TASK_BUDGET_EXCEEDED, got $count"; fi
pass "TASK_BUDGET_EXCEEDED count = 1"

# No LLM_STARTED events (budget blocked before critic LLM call)
count=$(count_task_event "$task_id" "LLM_STARTED")
if [[ "$count" != "0" ]]; then fail "Expected 0 LLM_STARTED (budget blocked), got $count"; fi
pass "LLM_STARTED count = 0 (budget blocked before critic)"

# No LLM_COMPLETED events
count=$(count_task_event "$task_id" "LLM_COMPLETED")
if [[ "$count" != "0" ]]; then fail "Expected 0 LLM_COMPLETED (budget blocked), got $count"; fi
pass "LLM_COMPLETED count = 0"

# No MULTI_AGENT_SYNTHESIZED
count=$(count_task_event "$task_id" "MULTI_AGENT_SYNTHESIZED")
if [[ "$count" != "0" ]]; then fail "Expected 0 MULTI_AGENT_SYNTHESIZED (budget blocked), got $count"; fi
pass "MULTI_AGENT_SYNTHESIZED count = 0 (budget blocked)"

# No TASK_COMPLETED
count=$(count_task_event "$task_id" "TASK_COMPLETED")
if [[ "$count" != "0" ]]; then fail "Expected 0 TASK_COMPLETED (budget blocked), got $count"; fi
pass "TASK_COMPLETED count = 0"

# Verify planner and researcher AGENT events still happened
count=$(count_task_event "$task_id" "AGENT_STARTED")
if [[ "$count" -lt 2 ]]; then fail "Expected >=2 AGENT_STARTED (planner+researcher at least), got $count"; fi
pass "AGENT_STARTED count = $count (planner+researcher executed, critic blocked)"

# Check no llm_calls in Postgres (budget exceeded before any LLM call)
llm_count=$(count_llm_calls "$task_id")
if [[ "$llm_count" != "0" ]]; then fail "Expected 0 llm_calls (budget exceeded), got $llm_count"; fi
pass "llm_calls count = 0 (budget exceeded before LLM calls)"

# Verify error_type in Postgres
error_type=$(get_task_error_type "$task_id")
if [[ "$error_type" != "budget_exceeded" ]]; then
  fail "Expected error_type=budget_exceeded, got $error_type"
fi
pass "tasks.error_type = budget_exceeded"

echo ""
log "All budget assertions passed for task_id=$task_id"
echo ""
echo "=== Multi-Agent Critic Budget Test PASSED ==="