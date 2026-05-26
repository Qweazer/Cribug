#!/bin/bash
set -euo pipefail

GATEWAY_URL="${GATEWAY_URL:-http://127.0.0.1:8080}"
REDIS_HOST="${REDIS_HOST:-127.0.0.1}"
REDIS_PORT="${REDIS_PORT:-6379}"

log()   { echo "[$(date +'%H:%M:%S')] $*"; }
fail()  { echo "[FAIL] $*" >&2; exit 1; }

redis_cmd() {
  if command -v redis-cli &>/dev/null; then
    redis-cli -h "$REDIS_HOST" -p "$REDIS_PORT" "$@"
  else
    docker.exe exec deploy-redis-1 redis-cli "$@"
  fi
}

log "=== DAG Dynamic Replan Test (Slice 13) ==="

# ═══════════════════════════════════════════════════════════════
# Part A: Smoke / Structure / Regression (keep all existing)
# ═══════════════════════════════════════════════════════════════

log "--- Part A: Structure & Regression ---"

log "A1: Code structure"
[ -f internal/activities/dag_fallback.go ] || fail "dag_fallback.go missing"
grep -q "HandleDAGNodeFailureActivity" internal/activities/dag_fallback.go || fail "HandleDAGNodeFailureActivity not found"
grep -q "NodeStatusSkipped" internal/types/dag_visual.go || fail "NodeStatusSkipped not found"
grep -q "DAG_NODE_SKIPPED" internal/events/types.go || fail "DAG_NODE_SKIPPED event not found"
grep -q "DAG_NODE_FAILED" internal/events/types.go || fail "DAG_NODE_FAILED event not found"
grep -q "DAG_REPLAN_SUMMARY" internal/events/types.go || fail "DAG_REPLAN_SUMMARY event not found"
grep -q "skippedNodes" internal/workflows/dag.go || fail "skippedNodes tracking not in DAG workflow"
grep -q "HandleDAGNodeFailureActivity" cmd/worker/main.go || fail "Activity not registered"
log "  PASSED"

log "A2: Normal DAG task still completes"
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{"query":"Slice 13 regression","config":{"mode":"dag","max_total_tokens":16000}}')
TID=$(echo "$RESP" | jq -r '.task_id')
[ -n "$TID" ] && [ "$TID" != "null" ] || fail "Failed to create task"
for i in $(seq 1 30); do
  S=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TID" | jq -r '.status')
  [ "$S" = "completed" ] && break; sleep 1
done
[ "$S" = "completed" ] || fail "Normal DAG: status=$S"
log "  PASSED"

# ═══════════════════════════════════════════════════════════════
# Part B: Real Failure Path Tests
# ═══════════════════════════════════════════════════════════════

log ""
log "--- Part B: Failure Injection Tests ---"

# ── B1: Trigger research node failure ───────────────────────────
log "B1: Creating DAG task with research node failure..."
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{"query":"Test DAG failure propagation","config":{"mode":"dag","test_fail_node_id":"research","max_total_tokens":16000}}')
FTID=$(echo "$RESP" | jq -r '.task_id')
[ -n "$FTID" ] && [ "$FTID" != "null" ] || fail "Failed to create failure test task"
WFID="task-$FTID"
log "  Task: $FTID"

for i in $(seq 1 30); do
  FS=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$FTID" | jq -r '.status')
  [ "$FS" = "completed" ] && break
  [ "$FS" = "failed" ] && fail "DAG crashed on node failure: should have completed with partial_success"
  sleep 1
done
[ "$FS" = "completed" ] || fail "Failure task not completed: status=$FS (DAG may have crashed instead of replanning)"
log "  Task completed (did not crash): PASSED"

# ── B2: Redis node states ──────────────────────────────────────
log "B2: Redis DAG node states"
NODES_KEY="dag:$WFID:nodes"
NODE_DATA=$(redis_cmd HGETALL "$NODES_KEY" 2>/dev/null || echo "")

# Verify research = failed
echo "$NODE_DATA" | grep -q '"research".*"status":"failed"' || fail "research node not failed"
echo "$NODE_DATA" | grep -q '"research".*"error":"' || fail "research missing error field"
log "  research=failed with error: PASSED"

# Verify analyze = completed (X = independent branch root)
echo "$NODE_DATA" | grep -q '"analyze".*"status":"completed"' || fail "analyze (X) not completed"
log "  analyze(X)=completed: PASSED"

# Verify conclude = completed (Y depends on X only, X succeeded → Y should succeed)
echo "$NODE_DATA" | grep -q '"conclude".*"status":"completed"' || fail "conclude (Y) not completed — independent branch X→Y broken"
log "  conclude(Y)=completed (X→Y independent branch): PASSED"

# Verify compare = skipped (depends on failed research)
echo "$NODE_DATA" | grep -q '"compare".*"status":"skipped"' || fail "compare not skipped"
log "  compare=skipped: PASSED"

# Verify validate = skipped
echo "$NODE_DATA" | grep -q '"validate".*"status":"skipped"' || fail "validate not skipped"
log "  validate=skipped: PASSED"

# Verify draft = skipped (multi-level: depends on skipped compare/validate)
echo "$NODE_DATA" | grep -q '"draft".*"status":"skipped"' || fail "draft not skipped (multi-level propagation failed)"
log "  draft=skipped (multi-level): PASSED"

# Verify review = skipped (depends on skipped draft)
echo "$NODE_DATA" | grep -q '"review".*"status":"skipped"' || fail "review not skipped"
log "  review=skipped: PASSED"

# Verify skipped nodes have reason
for n in compare validate draft review; do
  echo "$NODE_DATA" | grep -q '"'$n'".*"error"' || fail "$n missing error/skip_reason"
done
log "  All skipped nodes have skip reason: PASSED"

# Verify completed node preserves dependencies field
echo "$NODE_DATA" | grep -q '"analyze".*"dependencies"' || fail "analyze missing dependencies (merge lost field)"
log "  Dependencies preserved after merge: PASSED"

# ── B3: Workflow result summary ─────────────────────────────────
log "B3: Workflow result"
RESULT=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$FTID" | jq -r '.result')
[ -n "$RESULT" ] || fail "Result empty"

# Result must indicate partial success (not full success, not crash)
echo "$RESULT" | grep -qi "partial\|failed\|skipped" || fail "Result does not mention failure/skip: ${RESULT:0:120}"
log "  Result indicates partial success: PASSED"

# Result must NOT be just the original query
[ "$RESULT" != "Test DAG failure propagation" ] || fail "Result is original query"
log "  Result not query echo: PASSED"

# ── B4: Redis Stream events ─────────────────────────────────────
log "B4: Redis Stream events"
EVENTS=$(redis_cmd XRANGE "task:$FTID:events" - + 2>/dev/null || echo "")

# Count event types
DAG_FAILED=$(echo "$EVENTS" | grep -c "DAG_NODE_FAILED" || echo "0")
DAG_SKIPPED=$(echo "$EVENTS" | grep -c "DAG_NODE_SKIPPED" || echo "0")
DAG_REPLAN=$(echo "$EVENTS" | grep -c "DAG_REPLAN_SUMMARY" || echo "0")
DAG_COMPLETED=$(echo "$EVENTS" | grep -c "DAG_NODE_COMPLETED" || echo "0")
DAG_STARTED=$(echo "$EVENTS" | grep -c "DAG_NODE_STARTED" || echo "0")

[ "$DAG_FAILED" -ge 1 ] || fail "DAG_NODE_FAILED not found in stream"
log "  DAG_NODE_FAILED: $DAG_FAILED (>=1): PASSED"

[ "$DAG_SKIPPED" -ge 1 ] || fail "DAG_NODE_SKIPPED not found in stream"
log "  DAG_NODE_SKIPPED: $DAG_SKIPPED (>=1): PASSED"

[ "$DAG_REPLAN" -ge 1 ] || fail "DAG_REPLAN_SUMMARY not found in stream"
log "  DAG_REPLAN_SUMMARY: $DAG_REPLAN (>=1): PASSED"

[ "$DAG_COMPLETED" -ge 1 ] || fail "DAG_NODE_COMPLETED not preserved"
log "  DAG_NODE_COMPLETED preserved: $DAG_COMPLETED (>=1): PASSED"

[ "$DAG_STARTED" -ge 1 ] || fail "DAG_NODE_STARTED not preserved"
log "  DAG_NODE_STARTED preserved: $DAG_STARTED (>=1): PASSED"

# Verify Slice 10 events still present
echo "$EVENTS" | grep -q "DAG_PLANNED" || fail "DAG_PLANNED missing"
echo "$EVENTS" | grep -q "DAG_SYNTHESIZED" || fail "DAG_SYNTHESIZED missing"
log "  Slice 10 events preserved: PASSED"

# ═══════════════════════════════════════════════════════════════
log ""
log "=== DAG DYNAMIC REPLAN TEST PASSED ==="
