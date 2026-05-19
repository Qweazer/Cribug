#!/bin/bash
set -uo pipefail  # Note: no -e so we can handle failures ourselves

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

log() { echo "[$(date +'%H:%M:%S')] === $* ==="; }
pass() { echo "[PASS] $*"; }
note_fail() { echo "[FAIL] $*" >&2; }  # Don't exit, just note

FAILED=0
RESULTS=()

run_test() {
  local script=$1
  local name=$(basename "$script" .sh)
  echo ""
  log "Running $name..."
  if bash "$script" 2>&1; then
    pass "$name passed"
    RESULTS+=("$name: PASS")
    return 0
  else
    note_fail "$name failed"
    RESULTS+=("$name: FAIL")
    FAILED=1
    return 1
  fi
}

log "Multi-Agent Lite Full Test Suite Starting"

# Run all multi-agent tests in order
# Slice 5.1: Skeleton (planner mock, researcher+critic+synthesizer LLM-backed)
run_test "$SCRIPT_DIR/test_multi_agent_skeleton.sh"
# Slice 5.2: LLM execution test (synthesizer)
run_test "$SCRIPT_DIR/test_multi_agent_llm_execution.sh"
# Slice 5.2: Budget protection test (synthesizer)
run_test "$SCRIPT_DIR/test_multi_agent_llm_budget.sh"
# Slice 5.2: Failure path test
run_test "$SCRIPT_DIR/test_multi_agent_failure.sh"

# Slice 5.3: Critic LLM execution test
run_test "$SCRIPT_DIR/test_multi_agent_critic_llm_execution.sh"
# Slice 5.3: Critic budget test
run_test "$SCRIPT_DIR/test_multi_agent_critic_budget.sh"

# Slice 5.4: Researcher LLM execution test
run_test "$SCRIPT_DIR/test_multi_agent_researcher_llm_execution.sh"
# Slice 5.4: Researcher budget test
run_test "$SCRIPT_DIR/test_multi_agent_researcher_budget.sh"

echo ""
echo "========================================"
echo "MULTI-AGENT LITE FULL TEST RESULTS"
echo "========================================"

for result in "${RESULTS[@]}"; do
  echo "$result"
done

echo ""
if [ "$FAILED" -eq 0 ]; then
  echo "ALL MULTI-AGENT LITE TESTS PASSED"
  exit 0
else
  echo "SOME TESTS FAILED"
  exit 1
fi