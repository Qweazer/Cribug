#!/bin/bash
# Phase 6B Sandbox Runtime - API Smoke Test
set -euo pipefail

BASE_URL="${1:-http://localhost:8080}"
PASS=0
FAIL=0

green() { echo -e "\033[32m[PASS]\033[0m $*"; }
red()   { echo -e "\033[31m[FAIL]\033[0m $*"; }
check() {
    local desc="$1" expected="$2" actual="$3"
    if echo "$actual" | grep -q "$expected"; then
        green "$desc"; PASS=$((PASS + 1))
    else
        red "$desc (expected: $expected, got: $actual)"; FAIL=$((FAIL + 1))
    fi
}

echo "=== Phase 6B Sandbox Runtime Smoke Test ==="
echo "BASE_URL: $BASE_URL"
echo ""

# ---- 1. Execute sandbox (mock) ----
echo "--- 1. Execute Sandbox ---"
EXEC=$(curl -s -X POST "${BASE_URL}/api/v1/sandbox/execute" \
    -H "Content-Type: application/json" \
    -d '{"code":"(module)","language":"wasi","agent_id":"test"}')
check "execute sandbox accepted" '"request_id"' "$EXEC"
check "language wasi" '"wasi"' "$EXEC"

# ---- 2. Empty code rejected ----
echo ""
echo "--- 2. Empty Code Rejected ---"
EMPTY=$(curl -s -X POST "${BASE_URL}/api/v1/sandbox/execute" \
    -H "Content-Type: application/json" \
    -d '{"code":"","language":"wasi"}')
check "empty code rejected" "code is required" "$EMPTY"

# ---- 3. Policy bounds ----
echo ""
echo "--- 3. Policy Bounds ---"
POL=$(curl -s -X POST "${BASE_URL}/api/v1/sandbox/execute" \
    -H "Content-Type: application/json" \
    -d '{"code":"print(1)","language":"python","policy":{"cpu_timeout_sec":120,"memory_mb":2048,"max_stdout_bytes":1024},"agent_id":"test"}')
check "policy accepted" '"request_id"' "$POL"

# ---- 4. Different languages ----
echo ""
echo "--- 4. Language Support ---"
for lang in wasi wasm python javascript; do
    LRESP=$(curl -s -X POST "${BASE_URL}/api/v1/sandbox/execute" \
        -H "Content-Type: application/json" \
        -d "{\"code\":\"test\",\"language\":\"$lang\",\"agent_id\":\"t\"}")
    check "language $lang accepted" '"request_id"' "$LRESP"
done

# ---- 5. Audit endpoint ----
echo ""
echo "--- 5. Audit Endpoint ---"
AUDIT=$(curl -s "${BASE_URL}/api/v1/sandbox/audit?workflow_id=nonexistent")
check "audit endpoint works" '"audit_logs"' "$AUDIT"

# ---- 6. Missing workflow_id ----
echo ""
echo "--- 6. Missing workflow_id ---"
MISSING=$(curl -s "${BASE_URL}/api/v1/sandbox/audit")
check "missing workflow_id rejected" "workflow_id" "$MISSING"

# ---- Summary ----
echo ""
echo "============================================="
echo "  PASS: $PASS  FAIL: $FAIL  TOTAL: $((PASS + FAIL))"
echo "============================================="
[ $FAIL -gt 0 ] && exit 1
exit 0
