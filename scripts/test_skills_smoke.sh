#!/bin/bash
set -e

PYTHON_SERVICE_URL="${PYTHON_SERVICE_URL:-http://127.0.0.1:8000}"

echo "=== Phase 6C Skills System Smoke Test ==="
echo "Python Service: $PYTHON_SERVICE_URL"
echo

# Test 1: List skills
echo "Test 1: List skills"
skills=$(curl -s "$PYTHON_SERVICE_URL/tools")
if echo "$skills" | jq -e '.[]' 2>/dev/null | grep -q "echo_skill"; then
    echo "  PASS: echo_skill found"
else
    echo "  FAIL: echo_skill not found"
    exit 1
fi

# Test 2: Get echo_skill metadata
echo "Test 2: Get echo_skill metadata"
metadata=$(curl -s "$PYTHON_SERVICE_URL/tools/echo_skill")
if echo "$metadata" | jq -e '.execution_mode == "python_inline"' > /dev/null; then
    echo "  PASS: execution_mode is python_inline"
else
    echo "  FAIL: unexpected execution_mode"
    exit 1
fi

# Test 3: Execute echo_skill
echo "Test 3: Execute echo_skill"
result=$(curl -s -X POST "$PYTHON_SERVICE_URL/tools/echo_skill/execute" \
    -H "Content-Type: application/json" \
    -d '{"parameters": {"text": "hello"}}')
if echo "$result" | jq -e '.success == true and .output.text == "hello"' > /dev/null; then
    echo "  PASS: echo correct"
else
    echo "  FAIL: echo result wrong"
    exit 1
fi

# Test 4: Execute safe_math_skill (add)
echo "Test 4: Execute safe_math_skill (add)"
result=$(curl -s -X POST "$PYTHON_SERVICE_URL/tools/safe_math_skill/execute" \
    -H "Content-Type: application/json" \
    -d '{"parameters": {"operation": "add", "a": 2, "b": 3}}')
if echo "$result" | jq -e '.success == true and .output.result == 5' > /dev/null; then
    echo "  PASS: add worked"
else
    echo "  FAIL: add failed"
    exit 1
fi

# Test 5: division by zero
echo "Test 5: division by zero"
result=$(curl -s -X POST "$PYTHON_SERVICE_URL/tools/safe_math_skill/execute" \
    -H "Content-Type: application/json" \
    -d '{"parameters": {"operation": "divide", "a": 1, "b": 0}}')
if echo "$result" | jq -e '.success == false' > /dev/null; then
    echo "  PASS: division by zero handled"
else
    echo "  FAIL: division by zero not handled"
    exit 1
fi

# Test 6: llm_summary_skill config check
echo "Test 6: llm_summary_skill config check"
result=$(curl -s -X POST "$PYTHON_SERVICE_URL/tools/llm_summary_skill/execute" \
    -H "Content-Type: application/json" \
    -d '{"parameters": {"text": "test"}}')
if echo "$result" | jq -e '.success == false' > /dev/null; then
    error=$(echo "$result" | jq -r '.error')
    if echo "$error" | grep -qi "api\|config\|key"; then
        echo "  PASS: LLM skill fails without API key"
    else
        echo "  INFO: LLM skill failed: $error"
    fi
else
    echo "  PASS: LLM skill executed (API key present)"
fi

echo
echo "=== All smoke tests passed ==="
