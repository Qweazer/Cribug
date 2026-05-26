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

log "=== Swarm Workspace Smoke Test (Phase 5C Slice 12) ==="

# ── Test 1: Code structure ──────────────────────────────────────
log "Test 1: Workspace code structure"
grep -q "WorkspaceItem" internal/types/swarm.go || fail "WorkspaceItem missing"
grep -q "WorkspaceSummary" internal/types/swarm.go || fail "WorkspaceSummary missing"
grep -q "WSTypeObservation\|WSTypeCritique\|WSTypeFinal" internal/types/swarm.go || fail "WorkspaceItemType constants missing"
grep -q "workspaceItems\|WorkspaceItems\|WorkspaceAppends" internal/workflows/swarm.go || fail "workspace state not in workflow"
grep -q "WorkspaceAppends\|collectWorkspaceReads" internal/activities/swarm.go || fail "workspace not in activity"
grep -q "WORKSPACE_ITEM_CREATED\|WORKSPACE_SUMMARY_UPDATED" internal/events/types.go || fail "workspace events missing"
log "  PASSED"

# ── Test 2: Workspace + P2P integration ─────────────────────────
log "Test 2: Workspace items created in swarm"
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{"query":"Analyze the benefits of Docker for development workflows","config":{"mode":"swarm","max_parallel_agents":3,"max_total_tokens":8000,"max_completion_tokens":128}}')
TID=$(echo "$RESP" | jq -r '.task_id')
[ -n "$TID" ] && [ "$TID" != "null" ] || fail "Failed"
for i in $(seq 1 60); do
  S=$(curl --noproxy '*' -s "http://127.0.0.1:8080/api/v1/tasks/$TID" | jq -r '.status')
  [ "$S" = "completed" ] && break; sleep 1
done
[ "$S" = "completed" ] || fail "Workspace swarm: $S"
log "  Task completed: PASSED"

# ── Test 3: Workspace events ────────────────────────────────────
log "Test 3: Workspace events in Redis Stream"
sleep 1
EVENTS=$(redis_cmd XRANGE "task:$TID:events" - + 2>/dev/null || echo "")
C1=$(echo "$EVENTS" | grep -c "WORKSPACE_ITEM_CREATED" 2>/dev/null); C1=${C1##*$'\n'}; C1=${C1// /}
[ "${C1:-0}" -ge 1 ] || fail "WORKSPACE_ITEM_CREATED missing"
C2=$(echo "$EVENTS" | grep -c "WORKSPACE_SUMMARY_UPDATED" 2>/dev/null); C2=${C2##*$'\n'}; C2=${C2// /}
[ "${C2:-0}" -ge 1 ] || fail "WORKSPACE_SUMMARY_UPDATED missing"
log "  Workspace events present: PASSED"

# ── Test 4: Phase 5A+5B events preserved ────────────────────────
log "Test 4: Swarm + P2P events preserved"
S1=$(echo "$EVENTS" | grep -c "SWARM_COMPLETED" 2>/dev/null); S1=${S1##*$'\n'}; S1=${S1// /}
[ "${S1:-0}" -ge 1 ] || fail "SWARM_COMPLETED missing"
P1=$(echo "$EVENTS" | grep -c "P2P_ROUND_STARTED" 2>/dev/null); P1=${P1##*$'\n'}; P1=${P1// /}
[ "${P1:-0}" -ge 1 ] || fail "P2P_ROUND_STARTED missing"
log "  Phase 5A+5B events preserved: PASSED"

# ── Test 5: Forbidden patterns ──────────────────────────────────
log "Test 5: No forbidden patterns"
grep -q "workflow.Sleep" internal/workflows/swarm.go && fail "workflow.Sleep found" || true
log "  Clean: PASSED"

# ── Test 6: DAG regression ──────────────────────────────────────
log "Test 6: DAG regression"
RESP2=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{"query":"dag regression","config":{"mode":"dag","max_total_tokens":8000}}')
TID2=$(echo "$RESP2" | jq -r '.task_id')
for i in $(seq 1 15); do S2=$(curl --noproxy '*' -s "http://127.0.0.1:8080/api/v1/tasks/$TID2" | jq -r '.status'); [ "$S2" = "completed" ] && break; sleep 1; done
[ "$S2" = "completed" ] || fail "DAG regression: $S2"
log "  DAG still works: PASSED"

log ""
log "=== SWARM WORKSPACE SMOKE TEST PASSED ==="
