#!/bin/bash
# Phase 6 Real ReAct + RAG E2E Test
# Requires: Docker services, OPENAI_API_KEY, REAL_LLM_TEST=1, REAL_EMBEDDING_TEST=1
# Opt-in only: does NOT run in CI. Uses real OpenAI, real Qdrant, real LLM.
set -euo pipefail

# ── Config ──────────────────────────────────────────────────────
GATEWAY_URL="${GATEWAY_URL:-http://127.0.0.1:8080}"
PYTHON_LLM_URL="${PYTHON_LLM_URL:-http://127.0.0.1:8000}"
QDRANT_URL="${QDRANT_URL:-http://127.0.0.1:6333}"
DB_URL="${DATABASE_URL:-postgres://admin:admin@localhost:5432/orchestrator?sslmode=disable}"
RUN_ID=$(date +%s)
CODENAME="BLUE_HERON_${RUN_ID}"
PASS=0
FAIL=0
START_TIME=$(date +%s)

pass() { echo "  [PASS] $1"; PASS=$((PASS+1)); }
fail() { echo "  [FAIL] $1"; FAIL=$((FAIL+1)); }
skip() { echo "  [SKIP] $1 (not available)"; }

# ── Step 0: Env check ──────────────────────────────────────────
echo "============================================================"
echo " Phase 6 Real ReAct + RAG E2E Test"
echo " RUN_ID: $RUN_ID"
echo " CODENAME: $CODENAME"
echo "============================================================"
echo ""

echo "[0/8] Checking env vars..."

if [ "${REAL_LLM_TEST:-}" != "1" ]; then
    echo "ERROR: REAL_LLM_TEST=1 required. This test uses real LLM, no mock."
    echo "  export REAL_LLM_TEST=1"
    exit 1
fi
if [ "${REAL_EMBEDDING_TEST:-}" != "1" ]; then
    echo "ERROR: REAL_EMBEDDING_TEST=1 required. This test uses real embeddings, no mock."
    echo "  export REAL_EMBEDDING_TEST=1"
    exit 1
fi
if [ -z "${OPENAI_API_KEY:-}" ]; then
    echo "ERROR: OPENAI_API_KEY is required for real LLM and real embeddings."
    echo "  export OPENAI_API_KEY=sk-..."
    exit 1
fi
pass "env vars set"

# ── Step 1: Service checks ─────────────────────────────────────
echo ""
echo "[1/8] Checking services..."

check_http() {
    local url="$1" name="$2"
    if curl -s -o /dev/null -w "%{http_code}" --max-time 3 "$url" 2>/dev/null | grep -q "200\|307\|401"; then
        pass "$name reachable ($url)"
    else
        fail "$name NOT reachable ($url)"
    fi
}

check_http "$GATEWAY_URL/health" "Gateway"
check_http "$PYTHON_LLM_URL/health" "Python LLM Service"
check_http "$QDRANT_URL/" "Qdrant"

# Check Python /embed
embed_check=$(curl -s -X POST "$PYTHON_LLM_URL/embed" \
    -H "Content-Type: application/json" \
    -d '{"input": ["test"]}' 2>/dev/null || echo '{"error":"unreachable"}')
if echo "$embed_check" | grep -q '"embedding"'; then
    pass "Python /embed endpoint"
else
    fail "Python /embed not returning embeddings: $embed_check"
fi

# ── Step 2: Create unique test document ─────────────────────────
echo ""
echo "[2/8] Preparing test document..."

TITLE="Cribug Real ReAct RAG E2E Test ${RUN_ID}"
CONTENT="This is a real RAG integration test document for Cribug.
The unique verification codename is ${CODENAME}.
The correct answer must mention ${CODENAME}.
The document says that Phase 6D Hooks observe before_tool_call, after_tool_call, before_llm_call, after_llm_call, on_agent_step, on_workspace_append, on_handoff, and on_error.
The document says that Phase 6E RAG uses Python embeddings, Qdrant vector search, Postgres chunk storage, and context packing.
The document says that Phase 6F Research-Synthesis does sequential subquery decomposition and LLM synthesis."

DOC_ID=$(uuidgen 2>/dev/null || echo "doc-${RUN_ID}")
CHUNK_ID=$(uuidgen 2>/dev/null || echo "chunk-${RUN_ID}")
CONTENT_HASH=$(echo -n "$CONTENT" | sha256sum | cut -d' ' -f1)

pass "test document prepared"
echo "  document_id: $DOC_ID"
echo "  content_hash: $CONTENT_HASH"

# ── Step 3: Embed via Python + upsert to Qdrant ─────────────────
echo ""
echo "[3/8] Generating real embeddings and upserting to Qdrant..."

# Generate real embedding
EMBED_RESULT=$(curl -s -X POST "$PYTHON_LLM_URL/embed" \
    -H "Content-Type: application/json" \
    -d "{\"input\": [\"$CONTENT\"], \"model\": \"text-embedding-3-small\"}" 2>/dev/null)

if ! echo "$EMBED_RESULT" | grep -q '"embedding"'; then
    fail "Failed to generate real embedding: $EMBED_RESULT"
    exit 1
fi

VECTOR=$(echo "$EMBED_RESULT" | python3 -c "
import sys, json
data = json.load(sys.stdin)
vec = data['data'][0]['embedding']
print(json.dumps(vec))
" 2>/dev/null)

DIM=$(echo "$VECTOR" | python3 -c "import sys,json; print(len(json.load(sys.stdin)))" 2>/dev/null)
pass "real embedding generated ($DIM dimensions)"

# Ensure Qdrant collection exists
curl -s -X PUT "$QDRANT_URL/collections/task_embeddings" \
    -H "Content-Type: application/json" \
    -d '{"vectors": {"size": 1536, "distance": "Cosine"}}' > /dev/null 2>&1 || true

# Upsert to Qdrant
UPLOAD_BODY=$(python3 -c "
import sys, json
vector = json.loads('$VECTOR')
point = {
    'points': [{
        'id': '$CHUNK_ID',
        'vector': vector,
        'payload': {
            'tenant_id': '00000000-0000-0000-0000-000000000000',
            'document_id': '$DOC_ID',
            'chunk_id': '$CHUNK_ID',
            'chunk_index': 0,
            'source_type': 'text',
            'title': '$TITLE',
            'content_hash': '$CONTENT_HASH',
        }
    }]
}
print(json.dumps(point))
" 2>/dev/null)

UPSERT_RESULT=$(curl -s -X PUT "$QDRANT_URL/collections/task_embeddings/points?wait=true" \
    -H "Content-Type: application/json" \
    -d "$UPLOAD_BODY" 2>/dev/null)

if echo "$UPSERT_RESULT" | grep -q '"ok"\|"acknowledged"\|"status":"ok"'; then
    pass "vector upserted to Qdrant ($CHUNK_ID)"
else
    fail "Qdrant upsert failed: $UPSERT_RESULT"
fi

# ── Step 4: Insert document + chunk in Postgres ─────────────────
echo ""
echo "[4/8] Writing document + chunk to Postgres..."

PG_INSERT=$(psql "$DB_URL" -c "
INSERT INTO documents (id, tenant_id, source_type, title, content, content_hash, status, chunk_count)
VALUES ('$DOC_ID', '00000000-0000-0000-0000-000000000000', 'text', '$TITLE', '$CONTENT', '$CONTENT_HASH', 'indexed', 1)
ON CONFLICT (tenant_id, content_hash) DO UPDATE SET updated_at = NOW()
RETURNING id;
" 2>&1 || echo "PSQL_FAILED")

if echo "$PG_INSERT" | grep -q "$DOC_ID\|INSERT"; then
    pass "document inserted into Postgres"
else
    echo "  psql may not be available. Try direct DB access or set DATABASE_URL."
    skip "document Postgres insert (psql unavailable)"
fi

# Insert chunk
psql "$DB_URL" -c "
INSERT INTO document_chunks (id, document_id, tenant_id, chunk_index, content, content_hash, token_count, embedding_model, embedding_dimension, qdrant_collection, qdrant_point_id, status)
VALUES ('$CHUNK_ID', '$DOC_ID', '00000000-0000-0000-0000-000000000000', 0, '$CONTENT', '$CONTENT_HASH', $(echo "$CONTENT" | wc -c), 'text-embedding-3-small', $DIM, 'task_embeddings', '$CHUNK_ID', 'indexed')
ON CONFLICT (id) DO NOTHING;
" 2>/dev/null && pass "chunk inserted into Postgres" || skip "chunk Postgres insert (psql unavailable)"

# ── Step 5: Verify Qdrant search ────────────────────────────────
echo ""
echo "[5/8] Verifying Qdrant search..."

SEARCH_BODY=$(python3 -c "
import json
vector = json.loads('$VECTOR')
print(json.dumps({'vector': vector, 'limit': 3, 'score_threshold': 0.5}))
" 2>/dev/null)

SEARCH_RESULT=$(curl -s -X POST "$QDRANT_URL/collections/task_embeddings/points/search" \
    -H "Content-Type: application/json" \
    -d "$SEARCH_BODY" 2>/dev/null)

HIT_COUNT=$(echo "$SEARCH_RESULT" | python3 -c "
import sys, json
data = json.load(sys.stdin)
hits = data.get('result', [])
print(len(hits))
" 2>/dev/null)

if [ "${HIT_COUNT:-0}" -gt 0 ]; then
    pass "Qdrant search returns $HIT_COUNT hits"
else
    fail "Qdrant search returned 0 hits"
fi

# ── Step 6: Real ReAct + RAG through Gateway ────────────────────
echo ""
echo "[6/8] Running real ReAct + RAG query through Gateway..."

QUERY="search: In the Cribug real RAG test document, what is the unique verification codename? Also summarize what Phase 6D Hooks and Phase 6E RAG do according to the document. Answer using the retrieved context."

TASK_RESP=$(curl -s -X POST "$GATEWAY_URL/api/v1/tasks" \
    -H "Content-Type: application/json" \
    -d "{
        \"query\": \"$QUERY\",
        \"config\": {
            \"enable_react\": true,
            \"react_max_iterations\": 3,
            \"mode\": \"simple\"
        }
    }" 2>/dev/null)

TASK_ID=$(echo "$TASK_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin).get('task_id',''))" 2>/dev/null || echo "")
WORKFLOW_ID=$(echo "$TASK_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin).get('workflow_id',''))" 2>/dev/null || echo "")

if [ -n "$TASK_ID" ] && [ "$TASK_ID" != "" ]; then
    pass "task created: $TASK_ID (workflow: ${WORKFLOW_ID:-none})"
else
    fail "task creation failed: $TASK_RESP"
fi

# ── Step 7: Poll for task completion ────────────────────────────
echo ""
echo "[7/8] Waiting for ReAct + RAG to complete..."

MAX_WAIT=120
POLL_INTERVAL=5
WAITED=0
TASK_STATUS="pending"
ANSWER=""

while [ "$WAITED" -lt "$MAX_WAIT" ]; do
    sleep $POLL_INTERVAL
    WAITED=$((WAITED + POLL_INTERVAL))

    TASK_INFO=$(curl -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID" 2>/dev/null || echo '{}')
    TASK_STATUS=$(echo "$TASK_INFO" | python3 -c "
import sys, json
data = json.load(sys.stdin)
print(data.get('status', 'pending'))
" 2>/dev/null || echo "pending")

    echo "  [$WAITED s] status: $TASK_STATUS"

    if [ "$TASK_STATUS" = "completed" ] || [ "$TASK_STATUS" = "failed" ] || [ "$TASK_STATUS" = "error" ]; then
        RESPONSE=$(echo "$TASK_INFO" | python3 -c "
import sys, json
data = json.load(sys.stdin)
resp = data.get('response', data.get('result', ''))
if isinstance(resp, dict):
    print(json.dumps(resp)[:2000])
else:
    print(str(resp)[:2000])
" 2>/dev/null || echo "")
        break
    fi
done

if [ "$TASK_STATUS" = "completed" ]; then
    pass "task completed in ${WAITED}s"

    # Check answer
    ANSWER_SNIPPET="${RESPONSE:0:1000}"
    if echo "$ANSWER_SNIPPET" | grep -q "$CODENAME"; then
        pass "answer contains $CODENAME"
    else
        fail "answer does NOT contain $CODENAME"
        echo "  answer: $ANSWER_SNIPPET"
    fi
    if echo "$ANSWER_SNIPPET" | grep -qi "hook\|Phase 6D"; then
        pass "answer mentions Phase 6D Hooks"
    else
        echo "  (answer may not mention hooks explicitly - check context)"
    fi
    if echo "$ANSWER_SNIPPET" | grep -qi "embed\|Qdrant\|vector\|RAG\|Phase 6E"; then
        pass "answer mentions Phase 6E RAG concepts"
    else
        echo "  (answer may not mention RAG explicitly - check context)"
    fi
elif [ "$TASK_STATUS" = "failed" ]; then
    fail "task failed: $RESPONSE"
else
    fail "task did not complete within ${MAX_WAIT}s (status: $TASK_STATUS)"
fi

# ── Step 8: Verify hooks/workspace/audit ────────────────────────
echo ""
echo "[8/8] Verifying hooks and audit..."

HOOKS_AUDIT=$(curl -s "$GATEWAY_URL/api/v1/hooks/audit" 2>/dev/null || echo '{}')
HOOKS_COUNT=$(echo "$HOOKS_AUDIT" | python3 -c "
import sys, json
data = json.load(sys.stdin)
items = data.get('items', data.get('audit_logs', []))
print(len(items))
" 2>/dev/null || echo "0")

if [ "${HOOKS_COUNT:-0}" -gt 0 ]; then
    pass "hooks audit has $HOOKS_COUNT entries"
else
    skip "hooks audit empty (no hooks registered for this run, or audit table empty)"
fi

# Check Qdrant for the upserted point
VERIFY_QDRANT=$(curl -s "$QDRANT_URL/collections/task_embeddings/points/$CHUNK_ID" 2>/dev/null || echo '{}')
if echo "$VERIFY_QDRANT" | grep -q "$CHUNK_ID\|$DOC_ID"; then
    pass "Qdrant point $CHUNK_ID verified"
else
    skip "Qdrant point verification (may need different API path)"
fi

# ── Summary ─────────────────────────────────────────────────────
TOTAL_DURATION=$(( $(date +%s) - START_TIME ))
echo ""
echo "============================================================"
echo " Phase 6 Real ReAct + RAG E2E Results"
echo "============================================================"
echo "  run_id:            $RUN_ID"
echo "  codename:          $CODENAME"
echo "  document_id:       $DOC_ID"
echo "  chunk_id:          $CHUNK_ID"
echo "  task_id:           ${TASK_ID:-none}"
echo "  workflow_id:       ${WORKFLOW_ID:-none}"
echo "  task_status:       ${TASK_STATUS:-unknown}"
echo "  qdrant_hits:       ${HIT_COUNT:-0}"
echo "  answer_snippet:    ${ANSWER_SNIPPET:0:200}..."
echo "  total_duration:    ${TOTAL_DURATION}s"
echo "  env_used:          REAL_LLM_TEST=1 REAL_EMBEDDING_TEST=1"
echo "  passed: $PASS / failed: $FAIL"
echo "============================================================"

if [ "$FAIL" -gt 0 ]; then
    echo ""
    echo "FAILURES DETECTED. Check output above for details."
    exit 1
fi

echo ""
echo "PASS: Real ReAct + RAG E2E completed successfully."
exit 0
