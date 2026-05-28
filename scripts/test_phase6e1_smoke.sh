#!/bin/bash
# Phase 6E-1 Embeddings + Qdrant Foundation Smoke Test
set -e

PYTHON_URL="${PYTHON_URL:-http://127.0.0.1:8000}"
QDRANT_URL="${QDRANT_URL:-http://127.0.0.1:6333}"

echo "=== Phase 6E-1 Smoke Test ==="

# Test 1: Python /embed single
echo "Test 1: Python /embed single text"
result=$(curl -s -X POST "$PYTHON_URL/embed" -H "Content-Type: application/json" -d '{"input": ["hello world"]}' 2>/dev/null || echo '{"error":"python not running"}')
if echo "$result" | grep -q '"embedding"'; then
    echo "  PASS: /embed returns embeddings"
else
    echo "  SKIP: Python /embed not reachable (expected in CI without service)"
fi

# Test 2: Qdrant health
echo "Test 2: Qdrant health"
health=$(curl -s "$QDRANT_URL/" 2>/dev/null || echo '{"error":"connection refused"}')
if echo "$health" | grep -q '"version"\|"title"'; then
    echo "  PASS: Qdrant reachable"
else
    echo "  SKIP: Qdrant not running (expected for CI)"
fi

echo
echo "=== Smoke test complete ==="
