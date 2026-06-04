#!/bin/bash
# Phase 7I Router — Final regression orchestrator.
#
# This is the single entry point that runs every smoke and unit test
# for the Phase 7I Router Strategy Upgrade. The order is intentional:
#   1. Static checks (go test, go build, go vet)
#   2. Mock smokes (no LLM dependency)
#   3. Real LLM smokes (only when API key is present)
#
# Real-LLM sections SKIP with a clear message when no API key is
# present. They DO NOT fake a PASS.
#
# Exit code: 0 if every section passed; 1 otherwise.

set -e

BASE_URL="${BASE_URL:-http://127.0.0.1:8080}"
SCRIPTS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$(dirname "${BASH_SOURCE[0]}")/.."

PASS=0
FAIL=0
SKIP=0
STAGE_RESULTS=()

log_pass() { echo "[PASS] $1"; PASS=$((PASS+1)); }
log_fail() { echo "[FAIL] $1"; FAIL=$((FAIL+1)); }
log_skip() { echo "[SKIP] $1"; SKIP=$((SKIP+1)); }

run_stage() {
    local name="$1"
    local cmd="$2"
    echo ""
    echo "========================================"
    echo "  Stage: $name"
    echo "  Cmd:   $cmd"
    echo "========================================"
    if eval "$cmd" >/tmp/cribug-stage-$$.log 2>&1; then
        log_pass "$name"
        STAGE_RESULTS+=("PASS  $name")
    else
        log_fail "$name"
        STAGE_RESULTS+=("FAIL  $name")
        tail -30 /tmp/cribug-stage-$$.log | sed 's/^/    /'
    fi
    rm -f /tmp/cribug-stage-$$.log
}

# ─── 1. Static checks ─────────────────────────────────────────────────
echo "=== Phase 7I Router Final Regression ==="
echo ""
run_stage "go test ./... -count=1" "go test ./... -count=1"
run_stage "go build ./..." "go build ./..."
run_stage "go vet ./..." "go vet ./..."

# ─── 2. Mock smokes (always run; no LLM needed) ───────────────────────
run_stage "test_debate_smoke" "$SCRIPTS_DIR/test_debate_smoke.sh"
run_stage "test_tot_smoke" "$SCRIPTS_DIR/test_tot_smoke.sh"
run_stage "test_research_v2_smoke" "$SCRIPTS_DIR/test_research_v2_smoke.sh"
run_stage "test_approval_async_smoke" "$SCRIPTS_DIR/test_approval_async_smoke.sh"
run_stage "test_router_strategy_smoke" "$SCRIPTS_DIR/test_router_strategy_smoke.sh"
run_stage "test_router_strategy_regression" "$SCRIPTS_DIR/test_router_strategy_regression.sh"

# ─── 3. Real-LLM smokes (skipped when no API key) ─────────────────────
HAS_KEY=0
if [ -n "${LLM_API_KEY:-}" ] || [ -n "${OPENAI_API_KEY:-}" ]; then
    HAS_KEY=1
fi

if [ "$HAS_KEY" = "1" ]; then
    export ROUTER_CLASSIFIER_ENABLED="${ROUTER_CLASSIFIER_ENABLED:-1}"
    export REAL_ROUTER_CLASSIFIER_TEST="${REAL_ROUTER_CLASSIFIER_TEST:-1}"
    export ROUTER_LEGACY_HEURISTIC="${ROUTER_LEGACY_HEURISTIC:-false}"
    export ROUTER_REQUIRE_APPROVAL="${ROUTER_REQUIRE_APPROVAL:-false}"
    run_stage "test_router_classifier_real_llm" \
        "$SCRIPTS_DIR/test_router_classifier_real_llm_smoke.sh"
    run_stage "test_router_total_matrix" \
        "$SCRIPTS_DIR/test_router_total_matrix_smoke.sh"
    run_stage "test_router_frontend_contract" \
        "$SCRIPTS_DIR/test_router_frontend_contract_smoke.sh"
else
    log_skip "test_router_classifier_real_llm (no API key in env)"
    log_skip "test_router_total_matrix (no API key in env)"
    log_skip "test_router_frontend_contract (no API key in env)"
    STAGE_RESULTS+=("SKIP  test_router_classifier_real_llm (no API key)")
    STAGE_RESULTS+=("SKIP  test_router_total_matrix (no API key)")
    STAGE_RESULTS+=("SKIP  test_router_frontend_contract (no API key)")
fi

# ─── Summary ───────────────────────────────────────────────────────────
echo ""
echo "========================================"
echo "  Final Regression Summary"
echo "========================================"
for r in "${STAGE_RESULTS[@]}"; do
    echo "  $r"
done
echo ""
echo "Passed: $PASS  Failed: $FAIL  Skipped: $SKIP"

if [ "$FAIL" -gt 0 ]; then
    echo "FINAL: regression FAILED"
    exit 1
fi
echo "FINAL: regression PASSED"
exit 0
