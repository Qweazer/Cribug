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

log "DAG Lite Full Test Suite Starting"

# Run all DAG tests in order
run_test "$SCRIPT_DIR/test_dag_skeleton.sh"
run_test "$SCRIPT_DIR/test_dag_plan.sh"
run_test "$SCRIPT_DIR/test_dag_execution.sh"
run_test "$SCRIPT_DIR/test_dag_llm_execution.sh"
run_test "$SCRIPT_DIR/test_dag_llm_budget.sh"
run_test "$SCRIPT_DIR/test_dag_synthesis.sh"
run_test "$SCRIPT_DIR/test_dag_synthesis_budget.sh"
run_test "$SCRIPT_DIR/test_dag_failure.sh"

echo ""
echo "========================================"
echo "DAG LITE FULL TEST RESULTS"
echo "========================================"

for result in "${RESULTS[@]}"; do
  echo "$result"
done

echo ""
if [ "$FAILED" -eq 0 ]; then
  echo "ALL DAG LITE TESTS PASSED"
  exit 0
else
  echo "SOME TESTS FAILED"
  exit 1
fi