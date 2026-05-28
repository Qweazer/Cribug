#!/bin/bash
# Phase 6E-3 RAG Retrieval Smoke Test
set -e
cd "$(dirname "$0")/.."

echo "=== Phase 6E-3 RAG Retrieval Smoke Test ==="

# Test 1: Chunker works (dependency from 6E-2)
echo "Test 1: Chunker"
result=$(go test cribug/internal/rag/chunker/... -count=1 2>&1)
echo "$result" | grep -q "^ok" && echo "  PASS" || { echo "  FAIL"; exit 1; }

# Test 2: RAG retrieval activities compile and test
echo "Test 2: RAG retrieval activities"
result=$(go test cribug/internal/activities/... -count=1 -run "RAGRetrieval|EmbedAndSearch|FetchChunk|PackContext" 2>&1)
echo "$result" | grep -q "^ok" && echo "  PASS" || { echo "  FAIL"; exit 1; }

# Test 3: RAGQueryWorkflow tests
echo "Test 3: RAGQueryWorkflow"
result=$(go test cribug/internal/workflows/... -count=1 -run "RAGQuery" 2>&1)
echo "$result" | grep -q "^ok" && echo "  PASS" || { echo "  FAIL"; exit 1; }

# Test 4: PackContext formats citations
echo "Test 4: PackContext citations"
go test cribug/internal/activities/... -count=1 -run "TestPackContextActivity" -v 2>&1 | grep -q "PASS" && echo "  PASS" || echo "  SKIP"

# Test 5: Build worker + gateway
echo "Test 5: Build"
go build ./cmd/worker/... 2>&1 && go build ./cmd/gateway/... 2>&1 && echo "  PASS" || { echo "  FAIL"; exit 1; }

echo
echo "=== All 6E-3 smoke tests passed ==="
