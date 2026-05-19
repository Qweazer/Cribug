#!/bin/bash
# Test multi-agent LLM execution
# Tests: synthesizer calls LLM and records usage

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
echo "=== Multi-Agent LLM Execution Test ==="
echo ""

# Submit multi-agent task with LLM-backed synthesizer
log "Creating multi-agent LLM task..."

task_id=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{
    "query": "multi agent llm synthesis smoke task",
    "session_id": "sess-multi-agent-llm",
    "config": {
      "mode": "multi_agent",
      "model": "gpt-4o-mini",
      "temperature": 0.7,
      "max_total_tokens": 16000,
      "max_completion_tokens": 128
    }
  }' | jq -r '.task_id')

if [[ -z "$task_id" ]] || [[ "$task_id" == "null" ]]; then
  fail "Failed to create task"
fi

log "Task created: $task_id"

# Wait for task to complete
status=$(wait_for_task_terminal "$task_id" 90)
log "Task status: $status"

if [[ "$status" != "completed" ]]; then
  fail "Expected task completed, got $status"
fi

# Check result contains LLM mock output
result=$(get_task_result "$task_id")
log "Result: $result"

if [[ -z "$result" ]] || [[ "$result" == "null" ]]; then
  fail "Expected non-empty result"
fi

pass "Task completed with LLM result"
echo ""

# Verify Redis events for this task_id
log "Verifying Redis events for task_id=$task_id"

# WORKFLOW_STARTED count = 1
count=$(count_task_event "$task_id" "WORKFLOW_STARTED")
if [[ "$count" != "1" ]]; then fail "Expected 1 WORKFLOW_STARTED, got $count"; fi
pass "WORKFLOW_STARTED count = 1"

# SESSION_LOADED count = 1
count=$(count_task_event "$task_id" "SESSION_LOADED")
if [[ "$count" != "1" ]]; then fail "Expected 1 SESSION_LOADED, got $count"; fi
pass "SESSION_LOADED count = 1"

# AGENT_STARTED count = 4
count=$(count_task_event "$task_id" "AGENT_STARTED")
if [[ "$count" != "4" ]]; then fail "Expected 4 AGENT_STARTED, got $count"; fi
pass "AGENT_STARTED count = 4"

# AGENT_COMPLETED count = 4
count=$(count_task_event "$task_id" "AGENT_COMPLETED")
if [[ "$count" != "4" ]]; then fail "Expected 4 AGENT_COMPLETED, got $count"; fi
pass "AGENT_COMPLETED count = 4"

# LLM_STARTED count = 2 (critic + synthesizer)
count=$(count_task_event "$task_id" "LLM_STARTED")
if [[ "$count" != "2" ]]; then fail "Expected 2 LLM_STARTED (critic+synthesizer), got $count"; fi
pass "LLM_STARTED count = 2 (critic + synthesizer)"

# LLM_COMPLETED count = 2
count=$(count_task_event "$task_id" "LLM_COMPLETED")
if [[ "$count" != "2" ]]; then fail "Expected 2 LLM_COMPLETED (critic+synthesizer), got $count"; fi
pass "LLM_COMPLETED count = 2"

# USAGE_RECORDED count = 2 (critic + synthesizer)
count=$(count_task_event "$task_id" "USAGE_RECORDED")
if [[ "$count" != "2" ]]; then fail "Expected 2 USAGE_RECORDED (critic+synthesizer), got $count"; fi
pass "USAGE_RECORDED count = 2"

# MULTI_AGENT_SYNTHESIZED count = 1
count=$(count_task_event "$task_id" "MULTI_AGENT_SYNTHESIZED")
if [[ "$count" != "1" ]]; then fail "Expected 1 MULTI_AGENT_SYNTHESIZED, got $count"; fi
pass "MULTI_AGENT_SYNTHESIZED count = 1"

# TASK_COMPLETED count = 1
count=$(count_task_event "$task_id" "TASK_COMPLETED")
if [[ "$count" != "1" ]]; then fail "Expected 1 TASK_COMPLETED, got $count"; fi
pass "TASK_COMPLETED count = 1"

# TASK_FAILED count = 0
count=$(count_task_event "$task_id" "TASK_FAILED")
if [[ "$count" != "0" ]]; then fail "Expected 0 TASK_FAILED, got $count"; fi
pass "TASK_FAILED count = 0"

# Check Postgres llm_calls count = 2 (critic + synthesizer)
llm_count=$(count_llm_calls "$task_id")
if [[ "$llm_count" != "2" ]]; then fail "Expected 2 llm_calls (critic+synthesizer), got $llm_count"; fi
pass "llm_calls count = 2 (critic + synthesizer)"

echo ""
log "All assertions passed for task_id=$task_id"
echo ""
echo "=== Multi-Agent LLM Execution Test PASSED ==="