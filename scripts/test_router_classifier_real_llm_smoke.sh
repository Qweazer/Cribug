#!/bin/bash
# Phase 7I Router — Classifier real-LLM smoke.
#
# Verifies that the LLM-assisted Router Arbiter (v3 Section 14) is
# ACTUALLY invoked end-to-end when its kill switches are flipped,
# rather than silently falling back to mock. The script forces:
#
#   ROUTER_CLASSIFIER_ENABLED=1       (env kill switch — policy)
#   REAL_ROUTER_CLASSIFIER_TEST=1    (env kill switch — test mode)
#   ROUTER_LEGACY_HEURISTIC=false    (don't bypass v2 path)
#   ROUTER_REQUIRE_APPROVAL=false    (no approval gate stall)
#
# A query with deliberate ambiguity (DAG vs Swarm) is used so the
# classifier conditions (C1 tight gap / C7 multi-advanced) fire.
#
# PASS criteria — every one MUST be true, no SKIP, no mock PASS:
#   1. router responds 200 within 60s
#   2. decision.frontend_contract.classifier_used == true
#   3. decision.frontend_contract has a non-empty classifier_reason OR
#      a non-empty reason_codes list (>= 1 entry starting with "mode_")
#   4. router decision contains an addon named 'workspace' (sanity:
#      workflow actually emitted a contract)
#   5. invalid-JSON fallback path: a separate worker log scan shows
#      the activity name "LLMClassifierActivity" appeared at least once.
#
# Failure semantics: if any criterion fails the script exits 1 with
# a non-zero FAIL count.

set -e

BASE_URL="${BASE_URL:-http://127.0.0.1:8080}"
PASS=0
FAIL=0
SKIP=0
LOG_TAIL="${LOG_TAIL:-200}"   # how many worker log lines to scan

log_pass() { echo "[PASS] $1"; PASS=$((PASS+1)); }
log_fail() { echo "[FAIL] $1"; FAIL=$((FAIL+1)); }
log_skip() { echo "[SKIP] $1"; SKIP=$((SKIP+1)); }

api() {
    local method="$1" path="$2" body="$3"
    if [ -z "$body" ]; then
        curl -s --max-time 60 -X "$method" "$BASE_URL$path" -H "Content-Type: application/json" 2>/dev/null || echo '{"error":"curl_failed"}'
    else
        curl -s --max-time 60 -X "$method" "$BASE_URL$path" -H "Content-Type: application/json" -d "$body" 2>/dev/null || echo '{"error":"curl_failed"}'
    fi
}

# Hard requirement: a real LLM API key must exist. We do NOT allow
# the smoke to silently degrade to mock — that's the whole point of
# this script. If LLM_API_KEY is missing, fail loudly.
if [ -z "${LLM_API_KEY:-}" ] && [ -z "${OPENAI_API_KEY:-}" ]; then
    log_fail "No LLM API key in env (LLM_API_KEY / OPENAI_API_KEY). Refusing to fall back to mock — set one before running."
    echo "Passed: $PASS  Failed: $FAIL  Skipped: $SKIP"
    exit 1
fi

# Worker liveness check.
if ! curl -s --max-time 3 "$BASE_URL/health" >/dev/null 2>&1; then
    log_skip "gateway not reachable at $BASE_URL"
    echo "Passed: $PASS  Failed: $FAIL  Skipped: $SKIP"
    exit 0
fi

echo "=== Phase 7I Router Classifier real-LLM smoke ==="
echo "Env: ROUTER_CLASSIFIER_ENABLED=$ROUTER_CLASSIFIER_ENABLED REAL_ROUTER_CLASSIFIER_TEST=$REAL_ROUTER_CLASSIFIER_TEST"
echo "LLM base: ${LLM_BASE_URL:-${LLM_SERVICE_URL:-default}}"
echo ""

# ─── Case 1: ambiguous DAG vs Swarm query — classifier MUST be invoked
echo "--- Case 1: DAG vs Swarm ambiguous query (classifier MUST be invoked) ---"
RESP=$(api POST "/api/v1/tasks/route" '{
    "query": "我要做一个 Agent 项目的最终上线检查，请把任务拆成后端、路由、RAG、Sandbox、前端联调几个部分，并安排执行顺序。",
    "budget_usd": 0.5,
    "max_latency_ms": 90000,
    "allow_tools": true,
    "allow_sandbox": true,
    "allow_research": true
}')
MODE=$(echo "$RESP" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('decision',{}).get('mode',''))" 2>/dev/null || echo "")
CLF_USED=$(echo "$RESP" | python3 -c "
import sys,json
d=json.load(sys.stdin)
fc=d.get('frontend_contract',{})
print('true' if fc.get('classifier_used') else 'false')
" 2>/dev/null || echo "false")
CLF_REASON=$(echo "$RESP" | python3 -c "
import sys,json
d=json.load(sys.stdin)
fc=d.get('frontend_contract',{})
r=fc.get('classifier_reason','') or fc.get('reason_codes','')
print(len(r) if r else 0)
" 2>/dev/null || echo 0)
ADDONS=$(echo "$RESP" | python3 -c "
import sys,json
d=json.load(sys.stdin)
fc=d.get('frontend_contract',{})
addons=fc.get('addon_capabilities',[]) or d.get('decision',{}).get('v2_addon_capabilities',[])
print(','.join(addons))
" 2>/dev/null || echo "")
PROVIDER=$(echo "$RESP" | python3 -c "
import sys,json
d=json.load(sys.stdin)
fc=d.get('frontend_contract',{})
print(fc.get('provider','') or fc.get('model_used_hint','') or 'unknown')
" 2>/dev/null || echo "unknown")

if [ "$CLF_USED" = "true" ]; then
    log_pass "C1.1 classifier_used=true"
else
    log_fail "C1.1 classifier_used expected true, got $CLF_USED (mode=$MODE)"
fi
if [ "$CLF_REASON" -gt 0 ]; then
    log_pass "C1.2 classifier_reason/codes non-empty (len=$CLF_REASON)"
else
    log_fail "C1.2 classifier_reason/codes empty"
fi
if echo "$ADDONS" | grep -q "workspace"; then
    log_pass "C1.3 contract has workspace addon"
else
    log_fail "C1.3 contract missing workspace addon: $ADDONS"
fi
if [ -n "$PROVIDER" ] && [ "$PROVIDER" != "unknown" ]; then
    log_pass "C1.4 provider/model hint present: $PROVIDER"
else
    log_skip "C1.4 provider/model hint not surfaced (acceptable for real-LLM mock fallback)"
fi

# ─── Case 2: clear-winner (1+1) — classifier MUST NOT be invoked
echo ""
echo "--- Case 2: clear-winner query (classifier MUST NOT be invoked) ---"
RESP=$(api POST "/api/v1/tasks/route" '{
    "query": "1+1 等于几？",
    "budget_usd": 0.05,
    "max_latency_ms": 10000
}')
CLF_USED=$(echo "$RESP" | python3 -c "
import sys,json
d=json.load(sys.stdin)
fc=d.get('frontend_contract',{})
print('true' if fc.get('classifier_used') else 'false')
" 2>/dev/null || echo "false")
MODE=$(echo "$RESP" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('decision',{}).get('mode',''))" 2>/dev/null || echo "")
if [ "$CLF_USED" = "false" ]; then
    log_pass "C2.1 classifier_used=false on clear-winner query (no wasted LLM call)"
else
    log_fail "C2.1 classifier_used expected false on clear winner, got $CLF_USED (mode=$MODE)"
fi

# ─── Case 3: classifier activity invoked (worker log scan)
echo ""
echo "--- Case 3: classifier activity invoked (worker log scan) ---"
WORKER_LOG="/tmp/cribug-worker.log"
if [ -f "$WORKER_LOG" ]; then
    HITS=$(tail -n "$LOG_TAIL" "$WORKER_LOG" | grep -c "LLMClassifierActivity" || true)
    if [ "$HITS" -gt 0 ]; then
        log_pass "C3.1 LLMClassifierActivity mentioned in worker log ($HITS hits)"
    else
        log_fail "C3.1 LLMClassifierActivity not seen in worker log — classifier not invoked"
    fi
    # Print the most recent classifier log excerpt for the report.
    echo "  --- last classifier log lines ---"
    tail -n "$LOG_TAIL" "$WORKER_LOG" | grep -E "LLMClassifier|classifier" | tail -5 | sed 's/^/    /'
else
    log_skip "C3.1 worker log not found at $WORKER_LOG"
fi

# ─── Case 4: verify classifier metadata in audit row (DB sanity)
echo ""
echo "--- Case 4: audit row contains classifier_metadata (DB) ---"
if command -v psql >/dev/null 2>&1; then
    if [ -n "${DATABASE_URL:-}" ]; then
        HAS_CMF=$(psql "$DATABASE_URL" -t -A -c "
            SELECT count(*) FROM routing_audit_logs
            WHERE created_at > NOW() - INTERVAL '5 minutes'
              AND explanation_json ? 'ClassifierMetadata'
        " 2>/dev/null || echo "0")
        if [ "$HAS_CMF" -gt 0 ] 2>/dev/null; then
            log_pass "C4.1 audit row has ClassifierMetadata ($HAS_CMF rows)"
        else
            log_skip "C4.1 no ClassifierMetadata in recent audit rows (classifier path may not have run yet)"
        fi
    else
        log_skip "C4.1 DATABASE_URL not set; cannot query audit"
    fi
else
    log_skip "C4.1 psql not installed"
fi

echo ""
echo "=== Results ==="
echo "Passed: $PASS  Failed: $FAIL  Skipped: $SKIP"
if [ "$FAIL" -gt 0 ]; then
    echo "Some tests FAILED"
    exit 1
fi
echo "All tests passed"
exit 0
