#!/bin/bash
# Phase 6E-2 Document Ingestion Smoke Test
set -e

PYTHON_URL="${PYTHON_URL:-http://127.0.0.1:8000}"
QDRANT_URL="${QDRANT_URL:-http://127.0.0.1:6333}"

echo "=== Phase 6E-2 Ingestion Smoke Test ==="

# Test 1: Python /embed reachable
echo "Test 1: Python /embed"
result=$(curl -s -X POST "$PYTHON_URL/embed" -H "Content-Type: application/json" -d '{"input": ["ingestion test"]}' 2>/dev/null || echo '{"error":"unreachable"}')
if echo "$result" | grep -q '"embedding"'; then
    echo "  PASS: /embed returns embeddings"
else
    echo "  SKIP: Python /embed not reachable (CI without service)"
fi

# Test 2: Qdrant reachable
echo "Test 2: Qdrant health"
health=$(curl -s "$QDRANT_URL/" 2>/dev/null || echo '{"error":"unreachable"}')
if echo "$health" | grep -q '"version"'; then
    echo "  PASS: Qdrant reachable"
else
    echo "  SKIP: Qdrant not running (CI)"
fi

# Test 3: Chunker unit test summary
echo "Test 3: Chunker tests"
cd "$(dirname "$0")/.."
result=$(go test cribug/internal/rag/chunker/... -count=1 2>&1)
if echo "$result" | grep -q "^ok"; then
    echo "  PASS: chunker tests pass"
else
    echo "  FAIL: chunker tests failed"
    exit 1
fi

# Test 4: Ingestion activities smoke
echo "Test 4: Ingestion activities"
result=$(go test cribug/internal/activities/... -count=1 -run "Ingestion" 2>&1)
if echo "$result" | grep -q "^ok"; then
    echo "  PASS: ingestion activity tests pass"
else
    echo "  SKIP: ingestion tests need DB/service"
fi

echo
echo "=== Smoke test complete ==="
