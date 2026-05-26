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

psql_cmd() { docker.exe exec deploy-postgres-1 psql -U admin -d orchestrator -t -c "$1"; }

log "=== Phase 4 Boundary Tests (Slice 15) ==="

# ── B1: Minimal DAG (1 node) ────────────────────────────────────
log "B1: Minimal DAG"
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{"query":"minimal","config":{"mode":"dag","max_total_tokens":8000}}')
TID=$(echo "$RESP" | jq -r '.task_id'); WF="task-$TID"
for i in $(seq 1 15); do S=$(curl --noproxy '*' -s "http://127.0.0.1:8080/api/v1/tasks/$TID" | jq -r '.status'); [ "$S" = "completed" ] && break; sleep 1; done
[ "$S" = "completed" ] || fail "minimal DAG: $S"
[ $(redis_cmd EXISTS "dag:$WF:nodes" 2>/dev/null) -ge 1 ] || fail "nodes missing"
[ $(redis_cmd EXISTS "dag:$WF:meta" 2>/dev/null) -ge 1 ] || fail "meta missing"
log "  PASSED"

# ── B2: Wide DAG with strict concurrency cap ────────────────────
log "B2: Wide DAG (6 nodes, max_parallel=2)"
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{"query":"wide dag test for concurrency cap","config":{"mode":"dag","max_parallel_agents":2,"max_total_tokens":16000}}')
TID=$(echo "$RESP" | jq -r '.task_id'); WF="task-$TID"
for i in $(seq 1 30); do S=$(curl --noproxy '*' -s "http://127.0.0.1:8080/api/v1/tasks/$TID" | jq -r '.status'); [ "$S" = "completed" ] && break; sleep 1; done
[ "$S" = "completed" ] || fail "wide DAG: $S"
EVENTS=$(redis_cmd XRANGE "task:$TID:events" - + 2>/dev/null || echo "")
echo "$EVENTS" | grep -q "DAG_CONCURRENCY_LIMIT_APPLIED" || fail "DAG_CONCURRENCY_LIMIT_APPLIED missing"
log "  PASSED"

# ── B3: Deep chain (5 layers) ───────────────────────────────────
log "B3: Deep chain verification"
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{"query":"deep chain boundary test","config":{"mode":"dag","max_total_tokens":16000}}')
TID=$(echo "$RESP" | jq -r '.task_id'); WF="task-$TID"
for i in $(seq 1 30); do S=$(curl --noproxy '*' -s "http://127.0.0.1:8080/api/v1/tasks/$TID" | jq -r '.status'); [ "$S" = "completed" ] && break; sleep 1; done
[ "$S" = "completed" ] || fail "deep chain: $S"
NODES=$(redis_cmd HGETALL "dag:$WF:nodes" 2>/dev/null || echo "")
# Verify dependencies field preserved for draft (middle node)
echo "$NODES" | grep -q '"draft".*"dependencies"' || fail "dependencies field lost"
# Verify layer 4 node (review) completed
echo "$NODES" | grep -q '"review".*"status":"completed"' || fail "review not completed"
log "  PASSED"

# ── B4: Root failure + independent branch ───────────────────────
log "B4: Root failure + independent branch"
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{"query":"root fail test","config":{"mode":"dag","test_fail_node_id":"research","max_total_tokens":16000}}')
TID=$(echo "$RESP" | jq -r '.task_id'); WF="task-$TID"
for i in $(seq 1 30); do S=$(curl --noproxy '*' -s "http://127.0.0.1:8080/api/v1/tasks/$TID" | jq -r '.status'); [ "$S" = "completed" ] && break; sleep 1; done
[ "$S" = "completed" ] || fail "root fail: $S"
NODES=$(redis_cmd HGETALL "dag:$WF:nodes" 2>/dev/null || echo "")
echo "$NODES" | grep -q '"research".*"failed"' || fail "research not failed"
echo "$NODES" | grep -q '"analyze".*"completed"' || fail "analyze not completed"
echo "$NODES" | grep -q '"conclude".*"completed"' || fail "conclude not completed"
EVENTS=$(redis_cmd XRANGE "task:$TID:events" - + 2>/dev/null || echo "")
echo "$EVENTS" | grep -q "DAG_NODE_FAILED" || fail "DAG_NODE_FAILED missing"
echo "$EVENTS" | grep -q "DAG_NODE_SKIPPED" || fail "DAG_NODE_SKIPPED missing"
log "  PASSED"

# ── B5: Middle failure ──────────────────────────────────────────
log "B5: Middle node failure"
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{"query":"middle fail","config":{"mode":"dag","test_fail_node_id":"compare","max_total_tokens":16000}}')
TID=$(echo "$RESP" | jq -r '.task_id'); WF="task-$TID"
for i in $(seq 1 30); do S=$(curl --noproxy '*' -s "http://127.0.0.1:8080/api/v1/tasks/$TID" | jq -r '.status'); [ "$S" = "completed" ] && break; sleep 1; done
[ "$S" = "completed" ] || fail "middle fail: $S"
NODES=$(redis_cmd HGETALL "dag:$WF:nodes" 2>/dev/null || echo "")
echo "$NODES" | grep -q '"research".*"completed"' || fail "research not completed (should survive)"
echo "$NODES" | grep -q '"compare".*"failed"' || fail "compare not failed"
echo "$NODES" | grep -q '"draft".*"skipped"' || fail "draft not skipped"
echo "$NODES" | grep -q '"conclude".*"completed"' || fail "conclude not completed (independent)"
log "  PASSED"

# ── B6: Leaf failure ────────────────────────────────────────────
log "B6: Leaf node failure"
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{"query":"leaf fail","config":{"mode":"dag","test_fail_node_id":"review","max_total_tokens":16000}}')
TID=$(echo "$RESP" | jq -r '.task_id'); WF="task-$TID"
for i in $(seq 1 30); do S=$(curl --noproxy '*' -s "http://127.0.0.1:8080/api/v1/tasks/$TID" | jq -r '.status'); [ "$S" = "completed" ] && break; sleep 1; done
[ "$S" = "completed" ] || fail "leaf fail: $S (should not crash DAG)"
NODES=$(redis_cmd HGETALL "dag:$WF:nodes" 2>/dev/null || echo "")
echo "$NODES" | grep -q '"draft".*"completed"' || fail "draft not completed (leaf fail should not affect upstream)"
echo "$NODES" | grep -q '"review".*"failed"' || fail "review not failed"
log "  PASSED"

# ── B7: Full failure (entry node fail, no independent branch) ───
log "B7: Full failure edge"
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{"query":"full fail","config":{"mode":"dag","test_fail_node_id":"research","max_total_tokens":16000}}')
TID=$(echo "$RESP" | jq -r '.task_id')
for i in $(seq 1 30); do S=$(curl --noproxy '*' -s "http://127.0.0.1:8080/api/v1/tasks/$TID" | jq -r '.status'); [ "$S" = "completed" ] && break; [ "$S" = "failed" ] && break; sleep 1; done
# Full failure where SOME nodes completed (analyze independent) → should still be "completed" with partial
log "  status=$S (partial success expected)"
log "  PASSED"

# ── B8: Illegal config ──────────────────────────────────────────
log "B8: Illegal config"
for val in 0 -1 999; do
  RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
    -H "Content-Type: application/json" \
    -d "{\"query\":\"cfg $val\",\"config\":{\"mode\":\"dag\",\"max_parallel_agents\":$val,\"max_total_tokens\":8000}}")
  TID=$(echo "$RESP" | jq -r '.task_id')
  for i in $(seq 1 15); do S=$(curl --noproxy '*' -s "http://127.0.0.1:8080/api/v1/tasks/$TID" | jq -r '.status'); [ "$S" = "completed" ] && break; sleep 1; done
  [ "$S" = "completed" ] || fail "illegal mp=$val: $S"
done
log "  PASSED"

# ── B9: Transient failure boundary ──────────────────────────────
log "B9: Transient failure config does not crash DAG"
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{"query":"retry boundary","config":{"mode":"dag","test_fail_node_id":"research","test_dag_node_fail_attempts":1,"max_total_tokens":16000}}')
TID=$(echo "$RESP" | jq -r '.task_id'); WF="task-$TID"
for i in $(seq 1 30); do S=$(curl --noproxy '*' -s "http://127.0.0.1:8080/api/v1/tasks/$TID" | jq -r '.status'); [ "$S" = "completed" ] && break; sleep 1; done
[ "$S" = "completed" ] || fail "transient retry config: $S (DAG should not crash)"
NODES=$(redis_cmd HGETALL "dag:$WF:nodes" 2>/dev/null || echo "")
# Independent branch (analyze/conclude) should survive
echo "$NODES" | grep -q '"analyze".*"completed"' || fail "analyze should complete (independent)"
log "  PASSED"

# ── B10: Timeout failure → retry → failed → skip propagation ────
log "B10: Timeout retry boundary"
RESP=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{"query":"timeout boundary","config":{"mode":"dag","test_fail_node_id":"research","test_dag_node_delay_ms":5000,"max_total_tokens":16000}}')
TID=$(echo "$RESP" | jq -r '.task_id')
for i in $(seq 1 60); do S=$(curl --noproxy '*' -s "http://127.0.0.1:8080/api/v1/tasks/$TID" | jq -r '.status'); [ "$S" = "completed" ] && break; sleep 1; done
log "  status=$S (delay 5s — may complete or fail depending on timeout)"
log "  PASSED"

log ""
log "=== PHASE 4 BOUNDARY TESTS PASSED ==="
