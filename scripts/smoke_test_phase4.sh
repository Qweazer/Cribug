#!/bin/bash
set -euo pipefail

log() { echo "[$(date +'%H:%M:%S')] $*"; }
fail() { echo "[FAIL] $*" >&2; exit 1; }

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

log "=== Phase 4 Smoke Test ==="

# Slice 10: DAG Visualization
log ""
log "--- Slice 10: DAG Visualization ---"
if [ -f "$SCRIPT_DIR/test_dag_visual.sh" ]; then
  bash "$SCRIPT_DIR/test_dag_visual.sh" || fail "test_dag_visual.sh failed"
  log "Slice 10 DAG visualization: PASSED"
else
  log "WARNING: test_dag_visual.sh not found, skipping"
fi

# Slice 10: ReAct Observability
log ""
log "--- Slice 10: ReAct Observability ---"
if [ -f "$SCRIPT_DIR/test_react_observability.sh" ]; then
  bash "$SCRIPT_DIR/test_react_observability.sh" || fail "test_react_observability.sh failed"
  log "Slice 10 ReAct observability: PASSED"
else
  log "WARNING: test_react_observability.sh not found, skipping"
fi

# Slice 10: ReAct Reasoning (old tests, still valid)
log ""
log "--- Slice 10: ReAct Reasoning (legacy) ---"
if [ -f "$SCRIPT_DIR/test_react_reasoning.sh" ]; then
  bash "$SCRIPT_DIR/test_react_reasoning.sh" || fail "test_react_reasoning.sh failed"
  log "Slice 10 ReAct reasoning: PASSED"
else
  log "WARNING: test_react_reasoning.sh not found, skipping"
fi

# Slice 11: Workflow-level ReAct
log ""
log "--- Slice 11: Workflow-level ReAct ---"
if [ -f "$SCRIPT_DIR/test_react_workflow_level.sh" ]; then
  bash "$SCRIPT_DIR/test_react_workflow_level.sh" || fail "test_react_workflow_level.sh failed"
  log "Slice 11 Workflow-level ReAct: PASSED"
else
  log "WARNING: test_react_workflow_level.sh not found, skipping"
fi

# Slice 11: Real LLM Smoke (always runs, skips internally if no key)
log ""
log "--- Slice 11: Real LLM Smoke ---"
if [ -f "$SCRIPT_DIR/test_react_real_llm_smoke.sh" ]; then
  bash "$SCRIPT_DIR/test_react_real_llm_smoke.sh" || log "Real LLM smoke skipped or failed (non-fatal)"
else
  log "WARNING: test_react_real_llm_smoke.sh not found, skipping"
fi

# Multi-agent lite full (regression)
log ""
log "--- Multi-Agent Lite Full (regression) ---"
if [ -f "$SCRIPT_DIR/test_multi_agent_lite_full.sh" ]; then
  bash "$SCRIPT_DIR/test_multi_agent_lite_full.sh" || fail "test_multi_agent_lite_full.sh failed"
  log "Multi-agent lite full: PASSED"
else
  log "WARNING: test_multi_agent_lite_full.sh not found, skipping"
fi

log ""
log "=== PHASE 4 SMOKE TEST COMPLETE ==="
