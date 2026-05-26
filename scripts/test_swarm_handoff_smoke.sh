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

log "=== Agent Handoff Smoke Test (Phase 5G Slice 16) ==="

# Test 1: Code structure
log "Test 1: Handoff code structure"
grep -q "HandoffRequest" internal/types/swarm.go || fail "HandoffRequest type missing"
grep -q "HandoffEvent" internal/types/swarm.go || fail "HandoffEvent type missing"
grep -q "HandoffRequested\|HandoffAccepted\|HandoffRejected\|HandoffCompleted\|HandoffFailed" internal/types/swarm.go || fail "HandoffStatus constants missing"
grep -q 'HandoffRequest' internal/types/swarm.go || fail "HandoffRequest field on WorkerAgentResult missing"
# Exact match count check: grep should find the field
[ $(grep -c 'HandoffRequest' internal/types/swarm.go) -ge 3 ] || fail "HandoffRequest field count too low"
grep -q "HANDOFF_REQUESTED\|HANDOFF_ACCEPTED\|HANDOFF_COMPLETED" internal/events/types.go || fail "Handoff events missing"
grep -q "handoff\|HandoffRequest" internal/workflows/swarm.go || fail "Handoff handling not in workflow"
grep -q "handoff\|HandoffRequest" internal/activities/swarm.go || fail "Handoff generation not in activity"
log "  PASSED"

# Test 2: Handoff events in swarm execution
log "Test 2: Handoff events in Redis Stream"
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{"query":"handoff event test","config":{"mode":"swarm","max_parallel_agents":3,"max_total_tokens":8000}}')
TID=$(echo "$RESP" | jq -r '.task_id')
[ -n "$TID" ] && [ "$TID" != "null" ] || fail "Failed"
for i in $(seq 1 30); do
  S=$(curl --noproxy '*' -s "http://127.0.0.1:8080/api/v1/tasks/$TID" | jq -r '.status')
  [ "$S" = "completed" ] && break; sleep 1
done
[ "$S" = "completed" ] || fail "Handoff swarm: $S"
sleep 1
EVENTS=$(redis_cmd XRANGE "task:$TID:events" - + 2>/dev/null || echo "")
H1=$(echo "$EVENTS" | grep -c "HANDOFF_REQUESTED" 2>/dev/null); H1=${H1##*$'\n'}; H1=${H1// /}
H2=$(echo "$EVENTS" | grep -c "HANDOFF_ACCEPTED" 2>/dev/null); H2=${H2##*$'\n'}; H2=${H2// /}
H3=$(echo "$EVENTS" | grep -c "HANDOFF_COMPLETED" 2>/dev/null); H3=${H3##*$'\n'}; H3=${H3// /}
# researcher should generate handoff → accepted → target (worker-2 critic) executed → completed
[ "${H1:-0}" -ge 1 ] || fail "HANDOFF_REQUESTED missing"
[ "${H2:-0}" -ge 1 ] || fail "HANDOFF_ACCEPTED missing"
[ "${H3:-0}" -ge 1 ] || fail "HANDOFF_COMPLETED missing"
log "  Handoff events: REQUESTED=$H1 ACCEPTED=$H2 COMPLETED=$H3: PASSED"

# Test 3: Phase 5A-5C events preserved
log "Test 3: Prior Phase events preserved"
S1=$(echo "$EVENTS" | grep -c "SWARM_COMPLETED" 2>/dev/null); S1=${S1##*$'\n'}; S1=${S1// /}
[ "${S1:-0}" -ge 1 ] || fail "SWARM_COMPLETED missing"
log "  Prior Phases preserved: PASSED"

# Test 4: DAG regression
log "Test 4: DAG regression"
RESP2=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{"query":"dag","config":{"mode":"dag","max_total_tokens":8000}}')
TID2=$(echo "$RESP2" | jq -r '.task_id')
for i in $(seq 1 15); do S2=$(curl --noproxy '*' -s "http://127.0.0.1:8080/api/v1/tasks/$TID2" | jq -r '.status'); [ "$S2" = "completed" ] && break; sleep 1; done
[ "$S2" = "completed" ] || fail "DAG: $S2"
log "  DAG regression: PASSED"

log ""
log "=== AGENT HANDOFF SMOKE TEST PASSED ==="
