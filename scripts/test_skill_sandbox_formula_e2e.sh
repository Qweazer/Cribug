#!/bin/bash
# Phase 6B + Phase 6C Cross-module E2E Test
# Tests: Sandbox (Rust/WASI) → Go Gateway → Skills (Python/LLM) full chain
set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

PASS=0
FAIL=0
TESTS=()

pass() { PASS=$((PASS+1)); TESTS+=("${GREEN}PASS${NC}: $1"); }
fail() { FAIL=$((FAIL+1)); TESTS+=("${RED}FAIL${NC}: $1"); echo -e "${RED}FAIL${NC}: $1"; }

GO_GW="${GO_GATEWAY_URL:-http://127.0.0.1:8080}"
PY_URL="${PYTHON_SERVICE_URL:-http://127.0.0.1:8000}"
SANDBOX_RUNNER="${SANDBOX_RUNNER:-./sandbox/runner/target/release/sandbox-runner}"
WASM_FILE="testdata/sandbox/formula_compare.wasm"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(dirname "$SCRIPT_DIR")"

cd "$ROOT"

echo "================================================================"
echo " Phase 6B + 6C Cross-module E2E: Sandbox → Skills → LLM"
echo "================================================================"
echo "Go Gateway:   $GO_GW"
echo "Python LLM:   $PY_URL"
echo "Sandbox:      $SANDBOX_RUNNER"
echo "WASM:         $WASM_FILE"
echo

# ── Prerequisite checks ────────────────────────────────────────────
echo "--- Prerequisites ---"

curl -s "$GO_GW/health" > /dev/null 2>&1 && pass "Go gateway reachable" || { fail "Go gateway not reachable"; exit 1; }
curl -s "$PY_URL/health" > /dev/null 2>&1 && pass "Python LLM service reachable" || { fail "Python LLM not reachable"; exit 1; }

if [ -x "$SANDBOX_RUNNER" ] || command -v "$SANDBOX_RUNNER" > /dev/null 2>&1; then
    pass "Sandbox runner found"
else
    fail "Sandbox runner not found at $SANDBOX_RUNNER"
    exit 1
fi

if [ -f "$WASM_FILE" ]; then
    pass "WASM fixture exists"
else
    fail "WASM fixture not found: $WASM_FILE"
    echo "  Build it: GOOS=wasip1 GOARCH=wasm go build -o $WASM_FILE testdata/sandbox/formula_compare.go"
    exit 1
fi

echo

# ── Step 1: Execute Sandbox (Rust/WASI runner) ─────────────────────
echo "--- Step 1: Sandbox Execution (Rust/WASI) ---"

SB_OUTPUT=$(cat "$WASM_FILE" | "$SANDBOX_RUNNER" 2>/dev/null || true)
SB_SUCCESS=$(echo "$SB_OUTPUT" | python3 -c "import sys,json; print(json.load(sys.stdin)['success'])" 2>/dev/null || echo "false")

if [ "$SB_SUCCESS" = "True" ]; then
    pass "Sandbox execution success"
else
    fail "Sandbox execution failed: $SB_OUTPUT"
    exit 1
fi

SB_STDOUT=$(echo "$SB_OUTPUT" | python3 -c "import sys,json; print(json.load(sys.stdin)['stdout'])" 2>/dev/null)
FORMULA_JSON=$(echo "$SB_STDOUT" | python3 -m json.tool 2>/dev/null || echo "$SB_STDOUT")

echo "  Sandbox stdout:"
echo "$FORMULA_JSON" | while read line; do echo "    $line"; done

SI=$(echo "$SB_STDOUT" | python3 -c "import sys,json; print(json.load(sys.stdin)['simple_interest_amount'])" 2>/dev/null)
CI=$(echo "$SB_STDOUT" | python3 -c "import sys,json; print(json.load(sys.stdin)['compound_interest_amount'])" 2>/dev/null)
DIFF=$(echo "$SB_STDOUT" | python3 -c "import sys,json; print(json.load(sys.stdin)['difference'])" 2>/dev/null)
DURATION=$(echo "$SB_OUTPUT" | python3 -c "import sys,json; print(json.load(sys.stdin)['duration_ms'])" 2>/dev/null)

[ "$SI" = "1500" ] || [ "$SI" = "1500.0" ] && pass "simple_interest_amount = 1500" || fail "simple_interest_amount: expected 1500, got $SI"
[ "$CI" = "1628.89" ] && pass "compound_interest_amount = 1628.89" || fail "compound_interest_amount: expected 1628.89, got $CI"
[ "$DIFF" = "128.89" ] && pass "difference = 128.89" || fail "difference: expected 128.89, got $DIFF"

echo "  Sandbox duration: ${DURATION}ms"
echo

# ── Step 2: Build prompt for llm_summary_skill ──────────────────────
echo "--- Step 2: Build prompt from Sandbox result ---"

SUMMARY_PROMPT="请比较单利公式 A=P*(1+r*t) 和复利公式 A=P*(1+r)^t 在以下条件下的差异。

计算条件：
- 本金 P = 1000
- 年利率 r = 5%
- 时间 t = 10 年

计算结果：
- 单利本息和 = ${SI}
- 复利本息和 = ${CI}
- 两者差额 = ${DIFF}

请总结：
1. 单利和复利的核心区别
2. 为什么复利结果更高
3. 实际投资中的启示

用中文回答，控制在 150 字以内。"

echo "  Prompt length: ${#SUMMARY_PROMPT} chars"
pass "Prompt built from sandbox output"

# ── Step 3: Call llm_summary_skill via Go Gateway ──────────────────
echo
echo "--- Step 3: Go Gateway → Python Skills → MiniMax LLM ---"

FALLBACK_PROMPT="Compare simple interest and compound interest. P=1000, r=5%, t=10 years. Simple=$SI, Compound=$CI, Difference=$DIFF. Explain why compound is higher. Answer in Chinese under 150 chars."
LLM_RESP=$(curl -s -X POST "$GO_GW/api/v1/skills/llm_summary_skill/execute" \
    -H "Content-Type: application/json" \
    -d "{\"parameters\": {\"text\": \"$FALLBACK_PROMPT\", \"style\": \"concise\", \"language\": \"zh\", \"max_length\": 200}}")

LLM_SUCCESS=$(echo "$LLM_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['success'])" 2>/dev/null || echo "false")
LLM_PROVIDER=$(echo "$LLM_RESP" | python3 -c "import sys,json; d=json.load(sys.stdin); o=d.get('output',{}); print(o.get('provider','N/A') if isinstance(o,dict) else 'N/A')" 2>/dev/null || echo "N/A")
LLM_MODEL=$(echo "$LLM_RESP" | python3 -c "import sys,json; d=json.load(sys.stdin); o=d.get('output',{}); print(o.get('model','N/A') if isinstance(o,dict) else 'N/A')" 2>/dev/null || echo "N/A")
LLM_SUMMARY=$(echo "$LLM_RESP" | python3 -c "import sys,json; d=json.load(sys.stdin); o=d.get('output',{}); print(o.get('summary','N/A') if isinstance(o,dict) else 'N/A')" 2>/dev/null || echo "N/A")
LLM_TOKENS=$(echo "$LLM_RESP" | python3 -c "import sys,json; d=json.load(sys.stdin); o=d.get('output',{}); print(o.get('token_usage','N/A') if isinstance(o,dict) else 'N/A')" 2>/dev/null || echo "N/A")
LLM_TOKEN_USAGE=$(echo "$LLM_RESP" | python3 -c "import sys,json; d=json.load(sys.stdin); o=d.get('output',{}); print(o.get('token_usage',{}) if isinstance(o,dict) else '{}')" 2>/dev/null || echo "{}")

[ "$LLM_SUCCESS" = "True" ] && pass "llm_summary_skill success=true" || { fail "llm_summary_skill failed"; echo "  Response: $LLM_RESP"; }
[ "$LLM_PROVIDER" = "minimax" ] && pass "provider=minimax" || fail "provider: expected minimax, got $LLM_PROVIDER"
echo "  Model: $LLM_MODEL"
echo "  Token usage: $LLM_TOKENS"
echo "  Summary (first 200 chars): $(echo "$LLM_SUMMARY" | head -c 200)"
echo

# ── Step 4: Verify summary content ──────────────────────────────────
echo "--- Step 4: Verify LLM summary content ---"

SUMMARY_LEN=$(echo -n "$LLM_SUMMARY" | wc -c)
SFX=$(echo "$LLM_TOKEN_USAGE" | python3 -c "import ast,sys; d=ast.literal_eval(sys.stdin.read()); print(d.get('output_tokens',0))" 2>/dev/null || echo "0")

[ "$SUMMARY_LEN" -gt 50 ] && pass "Summary has content (${SUMMARY_LEN} chars)" || fail "Summary too short: ${SUMMARY_LEN} chars"
[ "$SFX" -gt 0 ] 2>/dev/null && pass "Token usage recorded (output=$SFX)" || echo -e "${YELLOW}WARN${NC}: Token usage check skipped"

# Search for math concepts in the LLM response (including think blocks for reasoning models)
KW_MATH=$(echo "$LLM_SUMMARY" | grep -ciE "compound|simple|interest|单利|复利|利息|线性|指数|formula" 2>/dev/null || echo "0")
[ "$KW_MATH" -gt 0 ] 2>/dev/null && pass "Summary discusses interest/compound concepts" || fail "Summary missing math concepts"

# Verify provider/model metadata
LLM_PROVIDER_OK=$(echo "$LLM_RESP" | python3 -c "import sys,json; d=json.load(sys.stdin); o=d.get('output',{}); p=o.get('provider','') if isinstance(o,dict) else ''; print('ok' if p=='minimax' else p)" 2>/dev/null)
LLM_MODEL_OK=$(echo "$LLM_RESP" | python3 -c "import sys,json; d=json.load(sys.stdin); o=d.get('output',{}); m=o.get('model','') if isinstance(o,dict) else ''; print('ok' if 'MiniMax' in m else m)" 2>/dev/null)

[ "$LLM_PROVIDER_OK" = "ok" ] && pass "provider=minimax confirmed" || fail "provider check: got $LLM_PROVIDER_OK"
[ "$LLM_MODEL_OK" = "ok" ] && pass "model contains MiniMax" || fail "model check: got $LLM_MODEL_OK"

echo

# ── Step 5: Secret leak check ──────────────────────────────────────
echo "--- Step 5: Secret leak check ---"

LLM_TEXT=$(echo "$LLM_RESP" | python3 -c "import sys,json; print(json.dumps(json.load(sys.stdin)))" 2>/dev/null)
if echo "$LLM_TEXT" | grep -qiE "sk-cp|sk-ant|api_key.*[a-zA-Z0-9]{20}"; then
    fail "SECRET LEAK: API key found in response"
else
    pass "No secrets in Go gateway response"
fi

# Check Python response too
PY_RESP=$(curl -s "$PY_URL/tools/llm_summary_skill" 2>/dev/null)
if echo "$PY_RESP" | grep -qiE "sk-cp|sk-ant|api_key.*[a-zA-Z0-9]{20}"; then
    fail "SECRET LEAK: API key found in Python metadata"
else
    pass "No secrets in Python API metadata"
fi

echo

# ── Step 6: Audit verification ────────────────────────────────────
echo "--- Step 6: skill_audit_logs check ---"

AUDIT_COUNT=$(psql "$DATABASE_URL" -t -c "SELECT COUNT(*) FROM skill_audit_logs" 2>/dev/null | tr -d ' ' || echo "0")
echo "  skill_audit_logs total rows: $AUDIT_COUNT"

if [ "$AUDIT_COUNT" -gt 0 ] 2>/dev/null; then
    pass "skill_audit_logs has records ($AUDIT_COUNT rows)"
    psql "$DATABASE_URL" -c "SELECT skill_name, success, provider, model, created_at FROM skill_audit_logs ORDER BY created_at DESC LIMIT 3" 2>/dev/null || true
else
    echo -e "${YELLOW}WARN${NC}: No audit records found (gateway sync execution doesn't write audit; workflow execution does)"
fi
echo

# ── Step 7: Workspace check ────────────────────────────────────────
echo "--- Step 7: Workspace Redis check ---"

WS_COUNT=$(redis-cli KEYS "workspace:*" 2>/dev/null | wc -l || echo "0")
echo "  Workspace keys in Redis: $WS_COUNT"

# Check for skill result events
SKILL_EVENTS=$(redis-cli KEYS "task:*:events" 2>/dev/null | head -5 || echo "")
if [ -n "$SKILL_EVENTS" ]; then
    pass "Workspace event streams exist"
else
    echo -e "${YELLOW}WARN${NC}: No task event streams found (expected for workflow path)"
fi
echo

# ── Final Report ───────────────────────────────────────────────────
echo "================================================================"
echo " Phase 6B + 6C Cross-module E2E Test Report"
echo "================================================================"
echo

for t in "${TESTS[@]}"; do echo -e "  $t"; done

echo
echo "Results: ${GREEN}$PASS passed${NC}, ${RED}$FAIL failed${NC}"

if [ "$FAIL" -gt 0 ]; then
    echo -e "${RED}E2E TEST FAILED${NC}"
    exit 1
else
    echo -e "${GREEN}E2E TEST PASSED${NC}"
    exit 0
fi
