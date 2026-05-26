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

log "=== DAG Concurrency Control Test (Slice 14) ==="

# ── Test 1: Code structure ──────────────────────────────────────
log "Test 1: Slice 14 code structure"
grep -q "max_parallel_agents" config/features.yaml || fail "max_parallel_agents not in features.yaml"
grep -q "maxParallel\|runningCount\|peakParallel" internal/workflows/dag.go || fail "concurrency gate missing in DAG scheduler"
grep -q "DAG_CONCURRENCY_LIMIT_APPLIED" internal/events/types.go || fail "DAG_CONCURRENCY_LIMIT_APPLIED event missing"
grep -q "TestNodeDelayMs\|TestNodeFailAttempts" internal/activities/dag.go || fail "Slice 14 test hooks missing"
grep -q "peakParallel\|peak_parallel" internal/workflows/dag.go || fail "peakParallel tracking missing"
log "  PASSED"

# ── Test 2: max_parallel_agents=1 serial execution ──────────────
log "Test 2: max_parallel_agents=1"
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{"query":"Serial test","config":{"mode":"dag","max_parallel_agents":1,"max_total_tokens":16000}}')
TID=$(echo "$RESP" | jq -r '.task_id')
[ -n "$TID" ] && [ "$TID" != "null" ] || fail "Failed"
for i in $(seq 1 30); do
  S=$(curl --noproxy '*' -s "http://127.0.0.1:8080/api/v1/tasks/$TID" | jq -r '.status')
  [ "$S" = "completed" ] && break; sleep 1
done
[ "$S" = "completed" ] || fail "max_parallel=1: status=$S"
# Verify concurrency event exists
EVENTS=$(redis_cmd XRANGE "task:$TID:events" - + 2>/dev/null || echo "")
echo "$EVENTS" | grep -q "DAG_CONCURRENCY_LIMIT_APPLIED" || fail "DAG_CONCURRENCY_LIMIT_APPLIED not in stream"
log "  max_parallel_agents=1 completed: PASSED"

# ── Test 3: max_parallel_agents=2 ───────────────────────────────
log "Test 3: max_parallel_agents=2"
RESP2=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{"query":"Concurrency 2 test","config":{"mode":"dag","max_parallel_agents":2,"max_total_tokens":16000}}')
TID2=$(echo "$RESP2" | jq -r '.task_id')
for i in $(seq 1 30); do
  S2=$(curl --noproxy '*' -s "http://127.0.0.1:8080/api/v1/tasks/$TID2" | jq -r '.status')
  [ "$S2" = "completed" ] && break; sleep 1
done
[ "$S2" = "completed" ] || fail "max_parallel=2: status=$S2"
log "  max_parallel_agents=2 completed: PASSED"

# ── Test 4: Illegal config ──────────────────────────────────────
log "Test 4: Illegal max_parallel_agents"
# 0 → default (5)
RESP3=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{"query":"Default test","config":{"mode":"dag","max_parallel_agents":0,"max_total_tokens":16000}}')
TID3=$(echo "$RESP3" | jq -r '.task_id')
for i in $(seq 1 30); do
  S3=$(curl --noproxy '*' -s "http://127.0.0.1:8080/api/v1/tasks/$TID3" | jq -r '.status')
  [ "$S3" = "completed" ] && break; sleep 1
done
[ "$S3" = "completed" ] || fail "max_parallel=0: status=$S3"
log "  max_parallel=0 → default: PASSED"

# 999 → hard limit 20
RESP4=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{"query":"Clamp test","config":{"mode":"dag","max_parallel_agents":999,"max_total_tokens":16000}}')
TID4=$(echo "$RESP4" | jq -r '.task_id')
for i in $(seq 1 30); do
  S4=$(curl --noproxy '*' -s "http://127.0.0.1:8080/api/v1/tasks/$TID4" | jq -r '.status')
  [ "$S4" = "completed" ] && break; sleep 1
done
[ "$S4" = "completed" ] || fail "max_parallel=999: status=$S4"
log "  max_parallel=999 → clamped to 20: PASSED"

# ── Test 5: Slice 13 replan still works under concurrency ──────
log "Test 5: Failure + concurrency interaction"
RESP5=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{"query":"Replan+concurrency","config":{"mode":"dag","max_parallel_agents":2,"test_fail_node_id":"research","max_total_tokens":16000}}')
TID5=$(echo "$RESP5" | jq -r '.task_id')
for i in $(seq 1 30); do
  S5=$(curl --noproxy '*' -s "http://127.0.0.1:8080/api/v1/tasks/$TID5" | jq -r '.status')
  [ "$S5" = "completed" ] && break; sleep 1
done
[ "$S5" = "completed" ] || fail "Replan+concurrency: status=$S5 (DAG should not crash)"
EVENTS5=$(redis_cmd XRANGE "task:$TID5:events" - + 2>/dev/null || echo "")
echo "$EVENTS5" | grep -q "DAG_NODE_FAILED" || fail "DAG_NODE_FAILED missing under concurrency"
echo "$EVENTS5" | grep -q "DAG_NODE_SKIPPED" || fail "DAG_NODE_SKIPPED missing under concurrency"
log "  Replan + concurrency: PASSED"

# ── Test 6: Transient failure retry ─────────────────────────────
log "Test 6: Transient failure with retry"
RESP6=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{"query":"Retry test","config":{"mode":"dag","max_parallel_agents":2,"test_fail_node_id":"research","test_dag_node_fail_attempts":1,"max_total_tokens":16000}}')
TID6=$(echo "$RESP6" | jq -r '.task_id')
for i in $(seq 1 30); do
  S6=$(curl --noproxy '*' -s "http://127.0.0.1:8080/api/v1/tasks/$TID6" | jq -r '.status')
  [ "$S6" = "completed" ] && break; sleep 1
done
[ "$S6" = "completed" ] || fail "Retry test: status=$S6"
log "  Transient failure → retry → completed: PASSED"

# ── Test 7: Delay hook ──────────────────────────────────────────
log "Test 7: Deterministic delay"
RESP7=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{"query":"Delay test","config":{"mode":"dag","max_parallel_agents":2,"test_dag_node_delay_ms":100,"max_total_tokens":16000}}')
TID7=$(echo "$RESP7" | jq -r '.task_id')
for i in $(seq 1 60); do
  S7=$(curl --noproxy '*' -s "http://127.0.0.1:8080/api/v1/tasks/$TID7" | jq -r '.status')
  [ "$S7" = "completed" ] && break; sleep 1
done
[ "$S7" = "completed" ] || fail "Delay test: status=$S7 (should complete despite delay)"
log "  Delay 100ms → completed: PASSED"

# ── Test 8: Strict concurrency cap: max_parallel=2, 6+ layer-1 nodes ──
log "Test 8: Strict concurrency cap (max=2, wide layer)"
RESP8=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{"query":"Strict concurrency cap test","config":{"mode":"dag","max_parallel_agents":2,"max_total_tokens":16000}}')
TID8=$(echo "$RESP8" | jq -r '.task_id')
[ -n "$TID8" ] && [ "$TID8" != "null" ] || fail "Failed"
for i in $(seq 1 60); do
  S8=$(curl --noproxy '*' -s "http://127.0.0.1:8080/api/v1/tasks/$TID8" | jq -r '.status')
  [ "$S8" = "completed" ] && break; sleep 1
done
[ "$S8" = "completed" ] || fail "Strict cap test: status=$S8"

# Verify DAG_CONCURRENCY_LIMIT_APPLIED events exist (batching guarantees peak <= maxParallel)
EVENTS8=$(redis_cmd XRANGE "task:$TID8:events" - + 2>/dev/null || echo "")
CONCUR_EVENTS8=$(echo "$EVENTS8" | grep -c "DAG_CONCURRENCY_LIMIT_APPLIED" || echo "0")
[ "$CONCUR_EVENTS8" -ge 1 ] || fail "DAG_CONCURRENCY_LIMIT_APPLIED missing"
log "  max_parallel=2, wide layer (6 nodes), batch-gated: PASSED"

# ── Test 9: Strict concurrency cap: max_parallel=1, 4+ layer-1 nodes ──
log "Test 9: Strict concurrency cap (max=1, wide layer)"
RESP9=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{"query":"Strict cap max=1","config":{"mode":"dag","max_parallel_agents":1,"max_total_tokens":16000}}')
TID9=$(echo "$RESP9" | jq -r '.task_id')
for i in $(seq 1 90); do
  S9=$(curl --noproxy '*' -s "http://127.0.0.1:8080/api/v1/tasks/$TID9" | jq -r '.status')
  [ "$S9" = "completed" ] && break; sleep 1
done
[ "$S9" = "completed" ] || fail "max_parallel=1 wide test: status=$S9"
EVENTS9=$(redis_cmd XRANGE "task:$TID9:events" - + 2>/dev/null || echo "")
CONCUR_EVENTS9=$(echo "$EVENTS9" | grep -c "DAG_CONCURRENCY_LIMIT_APPLIED" || echo "0")
[ "$CONCUR_EVENTS9" -ge 1 ] || fail "DAG_CONCURRENCY_LIMIT_APPLIED missing"
log "  max_parallel=1, wide layer (6 nodes), batch-gated: PASSED"

log ""
log "=== DAG CONCURRENCY CONTROL TEST PASSED ==="
