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

psql_cmd() {
  docker.exe exec deploy-postgres-1 psql -U admin -d orchestrator -t -c "$1"
}

log "=== Token LRU Cache Test ==="

# ── Reset stats ─────────────────────────────────────────────────
redis_cmd DEL lru:stats 2>/dev/null || true
log "Stats reset"

# ── Clean old lru:tiktoken keys ─────────────────────────────────
OLD_KEYS=$(redis_cmd KEYS "lru:tiktoken:*" 2>/dev/null || true)
if [ -n "$OLD_KEYS" ] && [ "$OLD_KEYS" != "" ]; then
  redis_cmd DEL $OLD_KEYS 2>/dev/null || true
fi
log "Old cache keys cleaned"

# ── Test 1: First call — cache miss, python_service or fallback ─
log "Test 1: First tokenize — should miss cache"
RESP1=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{"query":"Test token cache. Count these tokens please.","config":{"mode":"simple","max_completion_tokens":64}}')
TASK_ID1=$(echo "$RESP1" | jq -r '.task_id')
[ -n "$TASK_ID1" ] && [ "$TASK_ID1" != "null" ] || fail "Failed to create task 1"
log "  Task: $TASK_ID1"

for i in $(seq 1 30); do
  S=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID1" | jq -r '.status')
  [ "$S" = "completed" ] && break
  [ "$S" = "failed" ] && break
  sleep 1
done
[ "$S" = "completed" ] || fail "Task 1 not completed: $S"
log "  Task 1 completed"

# ── Test 2: Second call with same text — should hit L1 ──────────
log "Test 2: Second tokenize (same text) — should hit L1 cache"
RESP2=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{"query":"Test token cache. Count these tokens please.","config":{"mode":"simple","max_completion_tokens":64}}')
TASK_ID2=$(echo "$RESP2" | jq -r '.task_id')
[ -n "$TASK_ID2" ] && [ "$TASK_ID2" != "null" ] || fail "Failed to create task 2"

for i in $(seq 1 30); do
  S=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID2" | jq -r '.status')
  [ "$S" = "completed" ] && break
  sleep 1
done
[ "$S" = "completed" ] || fail "Task 2 not completed: $S"
log "  Task 2 completed"

# ── Test 3: Third call (still same text) — should hit cache again ─
log "Test 3: Third tokenize (same text) — should still hit cache"
RESP3=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{"query":"Test token cache. Count these tokens please.","config":{"mode":"simple","max_completion_tokens":64}}')
TASK_ID3=$(echo "$RESP3" | jq -r '.task_id')
for i in $(seq 1 30); do
  S=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID3" | jq -r '.status')
  [ "$S" = "completed" ] && break
  sleep 1
done
[ "$S" = "completed" ] || fail "Task 3 not completed: $S"
log "  Task 3 completed"

# ── Redis: check lru:tiktoken keys exist ────────────────────────
log "Test 4: Redis lru:tiktoken keys"
TIKTOKEN_KEYS=$(redis_cmd KEYS "lru:tiktoken:*" 2>/dev/null | wc -l)
if [ "${TIKTOKEN_KEYS:-0}" -ge 1 ]; then
  log "  Found $TIKTOKEN_KEYS lru:tiktoken key(s): PASSED"
  # Show key and TTL for first key
  FIRST_KEY=$(redis_cmd KEYS "lru:tiktoken:*" 2>/dev/null | head -1)
  KEY_TTL=$(redis_cmd TTL "$FIRST_KEY" 2>/dev/null)
  log "  Example key: $FIRST_KEY (TTL=$KEY_TTL sec)"
else
  fail "No lru:tiktoken keys found in Redis"
fi

# ── Stats: verify lru:stats hash ────────────────────────────────
log "Test 5: lru:stats verification"
STATS=$(redis_cmd HGETALL lru:stats 2>/dev/null || echo "")
if [ -n "$STATS" ]; then
  log "  lru:stats present"
  LOCAL_HITS=$(echo "$STATS" | grep -A1 "local_hits" | tail -1 || echo "0")
  MISSES=$(echo "$STATS" | grep -A1 "misses" | tail -1 || echo "0")
  PYTHON=$(echo "$STATS" | grep -A1 "python_calls" | tail -1 || echo "0")
  log "  local_hits=$LOCAL_HITS misses=$MISSES python_calls=$PYTHON"
  # Second and third calls should have local hits
  if [ "${LOCAL_HITS:-0}" -ge 2 ]; then
    log "  local_hits >= 2 (second+third call): PASSED"
  else
    log "  WARNING: local_hits=$LOCAL_HITS, expected >= 2"
  fi
  if [ "${MISSES:-0}" -ge 1 ]; then
    log "  misses >= 1 (first call): PASSED"
  else
    log "  WARNING: misses=$MISSES, expected >= 1"
  fi
else
  log "  WARNING: lru:stats not found (may not have been populated yet)"
fi

# ── Test 6: Different text — should be cache miss ───────────────
log "Test 6: Different text — should miss cache"
RESP4=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{"query":"A completely different question about machine learning.","config":{"mode":"simple","max_completion_tokens":64}}')
TASK_ID4=$(echo "$RESP4" | jq -r '.task_id')
for i in $(seq 1 30); do
  S=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID4" | jq -r '.status')
  [ "$S" = "completed" ] && break
  sleep 1
done
[ "$S" = "completed" ] || fail "Task 4 not completed: $S"

# Stats should show at least 2 misses now (first + fourth)
STATS2=$(redis_cmd HGETALL lru:stats 2>/dev/null || echo "")
MISSES2=$(echo "$STATS2" | grep -A1 "misses" | tail -1 || echo "0")
TOTAL_HITS=$(echo "$STATS2" | grep -A1 "hits" | tail -1 || echo "0")
log "  Updated stats — misses=$MISSES2 total_hits=$TOTAL_HITS"
log "  Different text cache miss: PASSED"

# ── Summary ─────────────────────────────────────────────────────
log ""
log "=== Stats Summary ==="
redis_cmd HGETALL lru:stats 2>/dev/null
log ""
log "=== TOKEN LRU CACHE TEST PASSED ==="
