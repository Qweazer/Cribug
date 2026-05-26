#!/bin/bash
set -euo pipefail

GATEWAY_URL="${GATEWAY_URL:-http://127.0.0.1:8080}"
REDIS_HOST="${REDIS_HOST:-127.0.0.1}"
REDIS_PORT="${REDIS_PORT:-6379}"

log()   { echo "[$(date +'%H:%M:%S')] $*"; }
fail()  { echo "[FAIL] $*" >&2; exit 1; }

redis_cmd() {
  if command -v redis-cli &>/dev/null; then redis-cli -h "$REDIS_HOST" -p "$REDIS_PORT" "$@"
  else docker.exe exec deploy-redis-1 redis-cli "$@"; fi
}

log "=== Swarm Workflow Smoke Test (Phase 5A Slice 10) ==="

# ── Test 1: Code structure ──────────────────────────────────────
log "Test 1: Code structure"
[ -f internal/workflows/swarm.go ] || fail "swarm.go missing"
[ -f internal/activities/swarm.go ] || fail "swarm activities missing"
[ -f internal/types/swarm.go ] || fail "swarm types missing"
grep -q "NewSelector" internal/workflows/swarm.go || fail "NewSelector not used (required by Shannon spec)"
grep -q "NewTimer" internal/workflows/swarm.go || fail "NewTimer not used"
grep -q "WorkerAgentActivity" cmd/worker/main.go || fail "WorkerAgentActivity not registered"
grep -q "SwarmWorkflow" cmd/worker/main.go || fail "SwarmWorkflow not registered"
grep -q "WorkflowModeSwarm" internal/types/types.go || fail "WorkflowModeSwarm not defined"
grep -q "SwarmWorkflow" internal/api/handler.go || fail "swarm routing not in handler"
log "  PASSED"

# ── Test 2: SwarmWorkflow success path ──────────────────────────
log "Test 2: SwarmWorkflow success path"
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{"query":"Analyze the benefits of Docker for development workflows","config":{"mode":"swarm","max_parallel_agents":3,"max_total_tokens":8000,"max_completion_tokens":128}}')
TID=$(echo "$RESP" | jq -r '.task_id')
[ -n "$TID" ] && [ "$TID" != "null" ] || fail "Failed to create swarm task"
log "  Task: $TID"
for i in $(seq 1 60); do
  S=$(curl --noproxy '*' -s "http://127.0.0.1:8080/api/v1/tasks/$TID" | jq -r '.status')
  [ "$S" = "completed" ] && break; sleep 1
done
[ "$S" = "completed" ] || fail "SwarmWorkflow: status=$S"
RESULT=$(curl --noproxy '*' -s "http://127.0.0.1:8080/api/v1/tasks/$TID" | jq -r '.result')
[ -n "$RESULT" ] || fail "Result empty"
log "  SwarmWorkflow completed: PASSED"

# ── Test 3: SSE/Stream events ───────────────────────────────────
log "Test 3: Swarm events in Redis Stream"
sleep 1
EVENTS=$(redis_cmd XRANGE "task:$TID:events" - + 2>/dev/null || echo "")
SWARM_CNT=$(echo "$EVENTS" | grep -c "SWARM_STARTED" 2>/dev/null); SWARM_CNT=${SWARM_CNT##*$'\n'}; SWARM_CNT=${SWARM_CNT// /}; [ "${SWARM_CNT:-0}" -ge 1 ] || fail "SWARM_STARTED missing"
WCNT=$(echo "$EVENTS" | grep -c "WORKER_COMPLETED" 2>/dev/null); WCNT=${WCNT##*$'\n'}; WCNT=${WCNT// /}; [ "${WCNT:-0}" -ge 1 ] || fail "WORKER_COMPLETED missing"
SCNT=$(echo "$EVENTS" | grep -c "SWARM_COMPLETED" 2>/dev/null); SCNT=${SCNT##*$'\n'}; SCNT=${SCNT// /}; [ "${SCNT:-0}" -ge 1 ] || fail "SWARM_COMPLETED missing"
log "  Swarm events present: PASSED"

# ── Test 4: No workflow.Sleep polling ───────────────────────────
log "Test 4: No Sleep polling in Workflow"
grep -q "workflow.Sleep\|time.Sleep" internal/workflows/swarm.go && fail "workflow.Sleep found (forbidden)" || true
log "  No polling detected: PASSED"

# ── Test 5: No goroutine / time.Now() / DB in Workflow ──────────
log "Test 5: No forbidden ops in SwarmWorkflow"
grep -q 'go func\|go ' internal/workflows/swarm.go && fail "goroutine found" || true
grep -q 'time.Now()' internal/workflows/swarm.go && fail "time.Now() found" || true
log "  No forbidden ops: PASSED"

# ── Test 6: Regression — basic DAG still works ──────────────────
log "Test 6: DAG regression"
RESP2=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{"query":"dag regression","config":{"mode":"dag","max_total_tokens":8000}}')
TID2=$(echo "$RESP2" | jq -r '.task_id')
for i in $(seq 1 15); do S2=$(curl --noproxy '*' -s "http://127.0.0.1:8080/api/v1/tasks/$TID2" | jq -r '.status'); [ "$S2" = "completed" ] && break; sleep 1; done
[ "$S2" = "completed" ] || fail "DAG regression: $S2"
log "  DAG still works: PASSED"

log ""
log "=== SWARM WORKFLOW SMOKE TEST PASSED ==="
