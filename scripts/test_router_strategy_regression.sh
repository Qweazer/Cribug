#!/bin/bash
# Phase 7I Router Strategy Upgrade — regression smoke
# Runs the existing router / debate / tot / research_v2 / approval
# smoke tests to confirm Phase 7I v2 changes did not break the
# pre-existing baseline.
#
# If any one of the sub-smokes fails, this script fails with
# non-zero exit so CI / local runs can detect regressions.

set -e

SCRIPTS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PASS=0
FAIL=0

log_pass() { echo "[PASS] $1"; PASS=$((PASS+1)); }
log_fail() { echo "[FAIL] $1"; FAIL=$((FAIL+1)); }

# Helper: run a smoke and capture exit.
run_smoke() {
    local name="$1"
    local script="$2"
    if [ ! -x "$script" ]; then
        log_fail "$name: script not found or not executable: $script"
        return
    fi
    if "$script" >/tmp/cribug_smoke_$$.log 2>&1; then
        log_pass "$name"
    else
        log_fail "$name (see /tmp/cribug_smoke_$$.log)"
        tail -30 /tmp/cribug_smoke_$$.log | sed 's/^/    /'
    fi
}

echo "=== Phase 7I Router Strategy Upgrade — regression ==="
echo ""

# Existing router + workflow smokes. Adjust path if the script is
# somewhere else; the plan puts all smoke scripts in scripts/.
run_smoke "test_advanced_router_smoke" "$SCRIPTS_DIR/test_advanced_router_smoke.sh"
run_smoke "test_debate_smoke"          "$SCRIPTS_DIR/test_debate_smoke.sh"
run_smoke "test_tot_smoke"             "$SCRIPTS_DIR/test_tot_smoke.sh"
run_smoke "test_research_v2_smoke"     "$SCRIPTS_DIR/test_research_v2_smoke.sh"
run_smoke "test_approval_async_smoke"  "$SCRIPTS_DIR/test_approval_async_smoke.sh"

echo ""
echo "=== Results ==="
echo "Passed: $PASS"
echo "Failed: $FAIL"
if [ "$FAIL" -gt 0 ]; then
    echo "Some regression tests FAILED"
    exit 1
fi
echo "All regression smokes passed"
exit 0
