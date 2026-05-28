#!/bin/bash
# Phase 6 Full E2E Smoke Test
# Covers: 6C Skills + 6D Hooks + 6E RAG + 6F Research-Synthesis
# All tests run WITHOUT live services (mock/fake by default)
set -e
cd "$(dirname "$0")/.."

PASS=0
FAIL=0
pass() { echo "  PASS: $1"; PASS=$((PASS+1)); }
fail() { echo "  FAIL: $1"; FAIL=$((FAIL+1)); }

echo "=== Phase 6 Full E2E Smoke ==="
echo

# ── Phase 6C: Skills ──
echo "--- Phase 6C: Skills ---"
echo "Test: echo_skill via Python API"
result=$(python_llm_service/.venv/bin/python -c "
import sys; sys.path.insert(0,'python_llm_service')
from llm_service.tools import get_registry
from llm_service.tools.builtin import EchoTool
get_registry().register(EchoTool)
import asyncio
r = asyncio.run(get_registry().get_tool('echo_skill').execute(session_context=None, observer=None, text='smoke'))
print('PASS' if r.success and r.output['text']=='smoke' else 'FAIL')
" 2>&1)
echo "$result" | grep -q "PASS" && pass "echo_skill" || fail "echo_skill"

echo "Test: safe_math_skill"
result=$(python_llm_service/.venv/bin/python -c "
import sys; sys.path.insert(0,'python_llm_service')
from llm_service.tools import get_registry
from llm_service.tools.builtin import SafeMathTool
get_registry().register(SafeMathTool)
import asyncio
r = asyncio.run(get_registry().get_tool('safe_math_skill').execute(session_context=None, observer=None, operation='add', a=2, b=3))
print('PASS' if r.success and r.output['result']==5.0 else 'FAIL')
" 2>&1)
echo "$result" | grep -q "PASS" && pass "safe_math_skill" || fail "safe_math_skill"

# ── Phase 6D: Hooks ──
echo "--- Phase 6D: Hooks ---"
go test ./internal/hooks/... -count=1 -run "TestHookDecision_WarningsCollected" 2>&1 | grep -q "^ok" && pass "hooks_decision" || fail "hooks_decision"
go test ./internal/hooks/... -count=1 -run "TestAuditLog_ContainsBlockingFields" 2>&1 | grep -q "^ok" && pass "hooks_audit_fields" || fail "hooks_audit_fields"

# ── Phase 6E-1: Embeddings ──
echo "--- Phase 6E-1: Embeddings + Qdrant Foundation ---"
go test ./internal/embeddings/... -count=1 -run "TestEmbedText" 2>&1 | grep -q "^ok" && pass "embed_service" || fail "embed_service"
go test ./internal/vectordb/... -count=1 -run "TestCircuitBreaker" 2>&1 | grep -q "^ok" && pass "vectordb_circuit" || fail "vectordb_circuit"

# ── Phase 6E-2: Ingestion ──
echo "--- Phase 6E-2: Document Ingestion ---"
go test ./internal/rag/chunker/... -count=1 2>&1 | grep -q "^ok" && pass "chunker" || fail "chunker"
go test ./internal/activities/... -count=1 -run "TestChunkDocumentActivity" 2>&1 | grep -q "^ok" && pass "chunk_activity" || fail "chunk_activity"

# ── Phase 6E-3: RAG Retrieval ──
echo "--- Phase 6E-3: RAG Retrieval ---"
go test ./internal/activities/... -count=1 -run "TestPackContextActivity" 2>&1 | grep -q "^ok" && pass "pack_context" || fail "pack_context"
go test ./internal/workflows/... -count=1 -run "TestRAGQueryWorkflow_Success" 2>&1 | grep -q "^ok" && pass "rag_query_wf" || fail "rag_query_wf"

# ── Phase 6E-4: Agent RAG ──
echo "--- Phase 6E-4: Agent RAG Integration ---"
go test ./internal/workflows/patterns/... -count=1 -run "TestReAct_RetrievalTrigger" 2>&1 | grep -q "^ok" && pass "react_rag_trigger" || fail "react_rag_trigger"
go test ./internal/workflows/... -count=1 -run "TestSwarm_LeaderRetrieves" 2>&1 | grep -q "^ok" && pass "swarm_rag" || fail "swarm_rag"
go test ./internal/workflows/... -count=1 -run "TestSkillExecutionWorkflow_WithRAGContext" 2>&1 | grep -q "^ok" && pass "skill_rag" || fail "skill_rag"

# ── Phase 6F: Research-Synthesis ──
echo "--- Phase 6F: Research-Synthesis ---"
go test ./internal/activities/... -count=1 -run "TestDecomposeQueryActivity" 2>&1 | grep -q "^ok" && pass "decompose_query" || fail "decompose_query"
go test ./internal/activities/... -count=1 -run "TestRetrieveEvidenceActivity" 2>&1 | grep -q "^ok" && pass "retrieve_evidence" || fail "retrieve_evidence"
go test ./internal/activities/... -count=1 -run "TestSynthesizeResultActivity" 2>&1 | grep -q "^ok" && pass "synthesize_result" || fail "synthesize_result"
go test ./internal/workflows/... -count=1 -run "TestResearchSynthesis_Success" 2>&1 | grep -q "^ok" && pass "research_synthesis_wf" || fail "research_synthesis_wf"

# ── Build Check ──
echo "--- Build ---"
go build ./cmd/worker/... 2>&1 && pass "worker_build" || fail "worker_build"
go build ./cmd/gateway/... 2>&1 && pass "gateway_build" || fail "gateway_build"

# ── Summary ──
echo
echo "=== Phase 6 E2E Smoke Results: $PASS passed, $FAIL failed ==="
[ "$FAIL" -eq 0 ] && echo "ALL PASS" && exit 0 || exit 1
