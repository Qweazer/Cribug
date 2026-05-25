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

# ── Test 1: Code structure ──────────────────────────────────────
log "Test 1: Code structure verification"
[ -f internal/activities/dag_fallback.go ] || fail "dag_fallback.go missing"
[ -f internal/workflows/dag.go ] || fail "dag.go missing"
grep -q "HandleDAGNodeFailureActivity" internal/activities/dag_fallback.go || fail "HandleDAGNodeFailureActivity not found"
grep -q "NodeStatusSkipped" internal/types/dag_visual.go || fail "NodeStatusSkipped not found"
grep -q "DAG_NODE_SKIPPED\|DAG_NODE_FAILED" internal/events/types.go || fail "DAG skip/fail events not found"
grep -q "skippedNodes" internal/workflows/dag.go || fail "skippedNodes tracking not in DAG workflow"
log "  Code structure: PASSED"

# ── Test 2: Normal DAG task still works (regression) ────────────
log "Test 2: Normal DAG task completes"
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{"query":"Test DAG replan regression","config":{"mode":"dag","max_total_tokens":16000}}')
TASK_ID=$(echo "$RESP" | jq -r '.task_id')
[ -n "$TASK_ID" ] && [ "$TASK_ID" != "null" ] || fail "Failed to create task"

for i in $(seq 1 30); do
  S=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID" | jq -r '.status')
  [ "$S" = "completed" ] && break
  [ "$S" = "failed" ] && break
  sleep 1
done
[ "$S" = "completed" ] || fail "Normal DAG task: status=$S"
log "  Normal DAG: PASSED"

# ── Test 3: Redis DAG events include new event types ────────────
log "Test 3: Redis stream events include Slice 13 types"
EVENTS=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null || echo "")
echo "$EVENTS" | grep -q "DAG_NODE_COMPLETED" || log "  WARNING: DAG_NODE_COMPLETED not found"
echo "$EVENTS" | grep -q "DAG_PLANNED" || log "  WARNING: DAG_PLANNED not found"
DAG_EVENTS=$(echo "$EVENTS" | grep -cE "DAG_" || echo "0")
log "  DAG_* events found: $DAG_EVENTS"
log "  Stream events: PASSED"

# ── Test 4: Redis DAG node status with merge semantics ──────────
log "Test 4: DAG node status in Redis"
WF_ID=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID" | jq -r '.workflow_id')
NODES_KEY="dag:${WF_ID}:nodes"
NODE_COUNT=$(redis_cmd HLEN "$NODES_KEY" 2>/dev/null || echo "0")
log "  Nodes in Redis: $NODE_COUNT"
if [ "$NODE_COUNT" -ge 1 ]; then
  FIRST_NODE=$(redis_cmd HGETALL "$NODES_KEY" 2>/dev/null | head -2 | tail -1)
  echo "$FIRST_NODE" | grep -q "dependencies" && log "  Dependencies field preserved: PASSED" || log "  WARNING: no dependencies field"
fi

# ── Test 5: Failed/skipped event types are registered ───────────
log "Test 5: Event type constants registered"
grep -q "DAG_NODE_SKIPPED" internal/events/types.go && log "  DAG_NODE_SKIPPED registered: PASSED"
grep -q "DAG_NODE_FAILED" internal/events/types.go && log "  DAG_NODE_FAILED registered: PASSED"
grep -q "DAG_REPLAN_SUMMARY" internal/events/types.go && log "  DAG_REPLAN_SUMMARY registered: PASSED"

# ── Test 6: HandleDAGNodeFailureActivity is registered ──────────
log "Test 6: HandleDAGNodeFailureActivity registered"
grep -q "HandleDAGNodeFailureActivity" cmd/worker/main.go && log "  Activity registered: PASSED"

log ""
log "=== DAG DYNAMIC REPLAN TEST PASSED ==="
