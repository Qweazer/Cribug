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

log "=== Swarm P2P Communication Smoke Test (Phase 5B Slice 11) ==="

# ── Test 1: Code structure ──────────────────────────────────────
log "Test 1: P2P code structure"
[ -f internal/types/swarm.go ] || fail "swarm.go missing"
grep -q "AgentMessage" internal/types/swarm.go || fail "AgentMessage type missing"
grep -q "P2PSummary" internal/types/swarm.go || fail "P2PSummary type missing"
grep -q "InboxMessages" internal/types/swarm.go || fail "InboxMessages missing in WorkerAgentInput"
grep -q "OutboundMessages" internal/types/swarm.go || fail "OutboundMessages missing in WorkerAgentResult"
grep -q "MsgTypeRequest\|MsgTypeResponse\|MsgTypeCritique\|MsgTypeFinal" internal/types/swarm.go || fail "MessageType constants missing"
grep -q "AGENT_MESSAGE_CREATED\|P2P_ROUND_STARTED" internal/events/types.go || fail "P2P events missing"
grep -q "max_p2p_rounds\|MaxP2PRounds" internal/workflows/swarm.go internal/api/handler.go || fail "max_p2p_rounds not enforced"
grep -q "inboxes\|roundTasks\|round.*range" internal/workflows/swarm.go || fail "P2P routing logic missing"
log "  PASSED"

# ── Test 2: P2P success path ────────────────────────────────────
log "Test 2: P2P multi-round execution"
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{"query":"Analyze the benefits of Docker for development","config":{"mode":"swarm","max_parallel_agents":3,"max_total_tokens":8000,"max_completion_tokens":128}}')
TID=$(echo "$RESP" | jq -r '.task_id')
[ -n "$TID" ] && [ "$TID" != "null" ] || fail "Failed to create task"
log "  Task: $TID"
for i in $(seq 1 60); do
  S=$(curl --noproxy '*' -s "http://127.0.0.1:8080/api/v1/tasks/$TID" | jq -r '.status')
  [ "$S" = "completed" ] && break; sleep 1
done
[ "$S" = "completed" ] || fail "P2P swarm: status=$S"
log "  P2P swarm completed: PASSED"

# ── Test 3: P2P events in Redis Stream ──────────────────────────
log "Test 3: P2P events"
sleep 1
EVENTS=$(redis_cmd XRANGE "task:$TID:events" - + 2>/dev/null || echo "")
[ $(echo "$EVENTS" | grep -c "\1" | tr -d " " || echo 0) -ge 1 ] || fail "P2P_ROUND_STARTED missing"
[ $(echo "$EVENTS" | grep -c "\1" | tr -d " " || echo 0) -ge 1 ] || fail "P2P_ROUND_COMPLETED missing"
[ $(echo "$EVENTS" | grep -c "\1" | tr -d " " || echo 0) -ge 1 ] || fail "AGENT_MESSAGE_CREATED missing"
[ $(echo "$EVENTS" | grep -c "\1" | tr -d " " || echo 0) -ge 1 ] || fail "AGENT_MESSAGE_ROUTED missing"
[ $(echo "$EVENTS" | grep -c "\1" | tr -d " " || echo 0) -ge 1 ] || fail "AGENT_MESSAGE_DELIVERED missing"
[ $(echo "$EVENTS" | grep -c "\1" | tr -d " " || echo 0) -ge 1 ] || fail "SWARM_COMPLETED missing (Phase 5A preserved)"
log "  P2P events present: PASSED"

# ── Test 4: Phase 5A events still work ──────────────────────────
log "Test 4: Phase 5A Swarm events preserved"
echo "$EVENTS" | grep -q "SWARM_STARTED" || fail "SWARM_STARTED missing"
echo "$EVENTS" | grep -q "WORKER_COMPLETED" || fail "WORKER_COMPLETED missing"
log "  Phase 5A events preserved: PASSED"

# ── Test 5: Forbidden patterns ──────────────────────────────────
log "Test 5: No forbidden patterns in SwarmWorkflow"
grep -q "workflow.Sleep" internal/workflows/swarm.go && fail "workflow.Sleep found" || true
grep -q 'go func' internal/workflows/swarm.go && fail "goroutine found" || true
log "  Clean: PASSED"

# ── Test 6: Regression — normal DAG still works ─────────────────
log "Test 6: DAG regression"
RESP2=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{"query":"dag regression","config":{"mode":"dag","max_total_tokens":8000}}')
TID2=$(echo "$RESP2" | jq -r '.task_id')
for i in $(seq 1 15); do S2=$(curl --noproxy '*' -s "http://127.0.0.1:8080/api/v1/tasks/$TID2" | jq -r '.status'); [ "$S2" = "completed" ] && break; sleep 1; done
[ "$S2" = "completed" ] || fail "DAG regression: $S2"
log "  DAG still works: PASSED"

log ""
log "=== SWARM P2P SMOKE TEST PASSED ==="
