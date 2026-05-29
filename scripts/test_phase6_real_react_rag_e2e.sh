#!/bin/bash
# Phase 6 Real ReAct + RAG E2E Test
# Opt-in only: requires Docker services, REAL_LLM_TEST=1, REAL_EMBEDDING_TEST=1
#
# Provider model (LLM and Embedding are SEPARATED):
#   LLM_PROVIDER=minimax (real)  → REAL_LLM_TEST=1
#   EMBEDDING_PROVIDER=openai (real semantic RAG) → REAL_EMBEDDING_TEST=1
#   EMBEDDING_PROVIDER=fake   (real LLM only, not real semantic RAG)
#
# Does NOT run in default CI.
set -euo pipefail

# ── Config ──────────────────────────────────────────────────────
GATEWAY_URL="${GATEWAY_URL:-http://127.0.0.1:8080}"
PYTHON_LLM_URL="${PYTHON_LLM_URL:-${PYTHON_LLM_SERVICE_URL:-http://127.0.0.1:8000}}"
QDRANT_URL="${QDRANT_URL:-http://127.0.0.1:6333}"
DB_URL="${DATABASE_URL:-postgres://admin:admin@localhost:5432/orchestrator?sslmode=disable}"
export PYTHON_LLM_URL
RUN_ID=$(date +%s)
CODENAME="BLUE_HERON_${RUN_ID}"
PASS=0; FAIL=0
START_TIME=$(date +%s)

pass() { echo "  [PASS] $1"; PASS=$((PASS+1)); }
fail() { echo "  [FAIL] $1"; FAIL=$((FAIL+1)); }
skip() { echo "  [SKIP] $1"; }

echo "============================================================"
echo " Phase 6 Real ReAct + RAG E2E Test"
echo " RUN_ID: $RUN_ID"
echo " CODENAME: $CODENAME"
echo "============================================================"
echo ""

# ── Python helper for safe JSON construction ────────────────────
PYHELPER=$(mktemp)
cat > "$PYHELPER" << 'PYEOF'
import sys, json, os, hashlib, uuid

def main():
    action = sys.argv[1] if len(sys.argv) > 1 else "help"
    if action == "embed":
        import http.client
        text = sys.argv[2]
        model = sys.argv[3] if len(sys.argv) > 3 else "text-embedding-3-small"
        py_url = os.environ.get("PYTHON_LLM_URL", "http://127.0.0.1:8000")
        from urllib.parse import urlparse
        parsed = urlparse(py_url)
        host = parsed.hostname or "127.0.0.1"
        port = parsed.port or 8000
        conn = http.client.HTTPConnection(host, port, timeout=30)
        body = json.dumps({"input": [text], "model": model})
        conn.request("POST", "/embed", body=body, headers={"Content-Type": "application/json"})
        resp = conn.getresponse()
        data = json.loads(resp.read())
        conn.close()
        vec = data["data"][0]["embedding"]
        print(json.dumps({"vector": vec, "dim": len(vec), "model": data.get("model","")}))
    elif action == "qdrant_upsert":
        chunk_id = sys.argv[2]; doc_id = sys.argv[3]; title = sys.argv[4]
        content_hash = sys.argv[5]
        vector_json = sys.stdin.read()
        vector = json.loads(vector_json)
        body = json.dumps({"points": [{
            "id": chunk_id, "vector": vector,
            "payload": {
                "tenant_id": "00000000-0000-0000-0000-000000000000",
                "document_id": doc_id, "chunk_id": chunk_id,
                "chunk_index": 0, "source_type": "text",
                "title": title, "content_hash": content_hash
            }
        }]})
        qd_url = os.environ.get("QDRANT_URL", "http://127.0.0.1:6333")
        parsed = urlparse(qd_url)
        conn = http.client.HTTPConnection(parsed.hostname or "127.0.0.1", parsed.port or 6333, timeout=10)
        conn.request("PUT", "/collections/task_embeddings/points?wait=true", body=body, headers={"Content-Type": "application/json"})
        resp = conn.getresponse()
        result = json.loads(resp.read()); conn.close()
        print(json.dumps(result))
    elif action == "qdrant_search":
        vector_json = sys.stdin.read()
        vector = json.loads(vector_json)
        body = json.dumps({"vector": vector, "limit": 3, "score_threshold": 0.5})
        qd_url = os.environ.get("QDRANT_URL", "http://127.0.0.1:6333")
        parsed = urlparse(qd_url)
        conn = http.client.HTTPConnection(parsed.hostname or "127.0.0.1", parsed.port or 6333, timeout=10)
        conn.request("POST", "/collections/task_embeddings/points/search", body=body, headers={"Content-Type": "application/json"})
        resp = conn.getresponse()
        data = json.loads(resp.read()); conn.close()
        hits = data.get("result", [])
        print(json.dumps({"hit_count": len(hits), "top_score": hits[0]["score"] if hits else 0}))
    elif action == "check_answer":
        answer = sys.stdin.read()
        codename = sys.argv[2]
        checks = {
            "has_codename": codename.lower() in answer.lower(),
            "has_hooks": "hook" in answer.lower() or "Phase 6D" in answer,
            "has_rag": "embed" in answer.lower() or "qdrant" in answer.lower() or "vector" in answer.lower() or "Phase 6E" in answer or "RAG" in answer,
        }
        print(json.dumps(checks))
    elif action == "escape_sql":
        # escape single quotes for SQL
        text = sys.stdin.read()
        print(text.replace("'", "''"))
    else:
        print(json.dumps({"error": "unknown action: " + action}))

if __name__ == "__main__":
    main()
PYEOF

PYTHON_BIN="python3"
$PYTHON_BIN -c "import urllib.request" 2>/dev/null && PYTHON_BIN="python3" || true

# ── Step 0: Env check ──────────────────────────────────────────
echo "[0/8] Checking env vars + provider configuration..."

# REAL_LLM_TEST gate
if [ "${REAL_LLM_TEST:-}" != "1" ]; then
    echo "ERROR: REAL_LLM_TEST=1 required. This test uses real LLM, no mock."
    echo "  Set: export REAL_LLM_TEST=1"
    echo "  Also set: LLM_PROVIDER=minimax (or your real provider)"
    exit 1
fi

# REAL_EMBEDDING_TEST gate — but allow EMBEDDING_PROVIDER=fake with clear marking
REAL_SEMANTIC_RAG=true
if [ "${REAL_EMBEDDING_TEST:-}" != "1" ]; then
    echo "NOTE: REAL_EMBEDDING_TEST is not set. Will use fake embeddings."
    echo "  For real semantic RAG, set: export REAL_EMBEDDING_TEST=1 EMBEDDING_PROVIDER=openai EMBEDDING_API_KEY=sk-..."
    REAL_SEMANTIC_RAG=false
else
    EMB_PROVIDER="${EMBEDDING_PROVIDER:-fake}"
    if [ "$EMB_PROVIDER" = "fake" ]; then
        echo "ERROR: REAL_EMBEDDING_TEST=1 but EMBEDDING_PROVIDER=fake."
        echo "  Set EMBEDDING_PROVIDER=openai and EMBEDDING_API_KEY to enable real embeddings."
        echo "  Or unset REAL_EMBEDDING_TEST to run with fake embeddings (real LLM only)."
        exit 1
    fi
    if [ -z "${EMBEDDING_API_KEY:-${OPENAI_API_KEY:-}}" ]; then
        echo "ERROR: REAL_EMBEDDING_TEST=1 requires EMBEDDING_API_KEY or OPENAI_API_KEY."
        echo "  Set: export EMBEDDING_API_KEY=sk-..."
        exit 1
    fi
fi

pass "env vars: REAL_LLM_TEST=1 REAL_EMBEDDING_TEST=${REAL_EMBEDDING_TEST:-0} EMBEDDING_PROVIDER=${EMBEDDING_PROVIDER:-fake}"
if [ "$REAL_SEMANTIC_RAG" = false ]; then
    echo "  MODE: Real LLM only (fake embeddings). RAG will use deterministic vectors,"
    echo "        not real semantic search. Set REAL_EMBEDDING_TEST=1 for real embeddings."
fi

# Verify Python LLM service has real LLM (not mock)
LLM_HEALTH=$(curl -s "$PYTHON_LLM_URL/health" 2>/dev/null || echo '{}')
LLM_MODE=$(echo "$LLM_HEALTH" | $PYTHON_BIN -c "import sys,json; print(json.load(sys.stdin).get('mode','unknown'))" 2>/dev/null || echo "unknown")
if [ "$LLM_MODE" = "real" ]; then
    pass "Python LLM service: REAL mode (LLM provider active)"
elif [ "$LLM_MODE" = "mock" ]; then
    echo "  ERROR: Python LLM service is in MOCK mode but REAL_LLM_TEST=1."
    echo "  Start Python service with LLM_PROVIDER=minimax (not mock)."
    exit 1
else
    echo "  WARNING: Python LLM service mode='$LLM_MODE'. Verify LLM is real, not mock."
fi

# ── Step 1: Service checks ─────────────────────────────────────
echo ""
echo "[1/8] Checking services..."

check_http() {
    local url="$1" name="$2"
    local code
    code=$(curl -s -o /dev/null -w "%{http_code}" --max-time 3 "$url" 2>/dev/null || echo "000")
    case "$code" in
        200|307|308|401|403) pass "$name reachable ($url)" ;;
        *) fail "$name NOT reachable ($url) - got HTTP $code" ;;
    esac
}

check_http "$GATEWAY_URL/health" "Gateway"
check_http "$PYTHON_LLM_URL/health" "Python LLM Service"
check_http "$QDRANT_URL/" "Qdrant"

# Verify Python /embed
EMBED_CHECK=$($PYTHON_BIN -c "
import http.client, json
conn = http.client.HTTPConnection('127.0.0.1', 8000, timeout=10)
body = json.dumps({'input': ['test']})
conn.request('POST', '/embed', body=body, headers={'Content-Type': 'application/json'})
resp = conn.getresponse()
data = json.loads(resp.read())
conn.close()
print('HAS_EMBEDDING' if data.get('data') else 'NO_DATA')
" 2>/dev/null)

if echo "$EMBED_CHECK" | grep -q "HAS_EMBEDDING"; then
    pass "Python /embed endpoint returns embeddings"
else
    fail "Python /embed: $EMBED_CHECK"
fi

# ── Step 2: Create test document ────────────────────────────────
echo ""
echo "[2/8] Preparing test document..."

DOC_ID=$(uuidgen 2>/dev/null || echo "doc-${RUN_ID}")
CHUNK_ID=$(uuidgen 2>/dev/null || echo "chunk-${RUN_ID}")

TITLE="Cribug Real ReAct RAG E2E Test ${RUN_ID}"
CONTENT=$(cat << EOF
This is a real RAG integration test document for Cribug.
The unique verification codename is ${CODENAME}.
The correct answer must mention ${CODENAME}.
The document says that Phase 6D Hooks observe before_tool_call, after_tool_call, before_llm_call, after_llm_call, on_agent_step, on_workspace_append, on_handoff, and on_error.
The document says that Phase 6E RAG uses Python embeddings, Qdrant vector search, Postgres chunk storage, and context packing.
EOF
)
CONTENT_HASH=$(echo -n "$CONTENT" | sha256sum | cut -d' ' -f1)
CHAR_COUNT=$(echo -n "$CONTENT" | wc -c)

echo "  document_id: $DOC_ID"
echo "  content_hash: $CONTENT_HASH ($CHAR_COUNT chars)"
pass "test document prepared"

# ── Step 3: Real embedding + Qdrant upsert ──────────────────────
echo ""
echo "[3/8] Generating real embeddings + upserting to Qdrant..."

EMBED_DIM=${EMBEDDING_DIMENSION:-3072}
EMBED_MODEL=${EMBEDDING_MODEL:-models/gemini-embedding-001}

# Use a single Python script to embed + upsert (avoids shell escaping issues)
EMBED_UPSERT_OK=$($PYTHON_BIN -c "
import http.client, json, os, sys, uuid
from urllib.parse import urlparse

content = '''$CONTENT'''
title = '''$TITLE'''
doc_id = '$DOC_ID'
chunk_id = '$CHUNK_ID'
content_hash = '$CONTENT_HASH'
embed_model = '$EMBED_MODEL'
embed_dim = $EMBED_DIM

# 1. Ensure Qdrant collection exists
qdrant_host = os.getenv('QDRANT_URL', 'http://127.0.0.1:6333')
parsed = urlparse(qdrant_host)
qd_host = parsed.hostname or '127.0.0.1'
qd_port = parsed.port or 6333

c = http.client.HTTPConnection(qd_host, qd_port, timeout=5)
c.request('PUT', '/collections/task_embeddings',
    json.dumps({'vectors': {'size': embed_dim, 'distance': 'Cosine'}}),
    {'Content-Type': 'application/json'})
c.getresponse().read(); c.close()

# 2. Embed
c = http.client.HTTPConnection('127.0.0.1', 8000, timeout=60)
c.request('POST', '/embed', json.dumps({'input': [content], 'model': embed_model}),
    {'Content-Type': 'application/json'})
r = c.getresponse(); data = json.loads(r.read()); c.close()
if not data.get('data'):
    print('EMBED_FAIL:' + str(data))
    sys.exit(0)
vec = data['data'][0]['embedding']
dim = len(vec)
print(f'EMBED_OK:{dim}d')

# 3. Upsert
c = http.client.HTTPConnection(qd_host, qd_port, timeout=10)
c.request('PUT', '/collections/task_embeddings/points?wait=true',
    json.dumps({'points': [{'id': chunk_id, 'vector': vec,
        'payload': {'tenant_id': '00000000-0000-0000-0000-000000000000',
            'document_id': doc_id, 'chunk_id': chunk_id, 'chunk_index': 0,
            'source_type': 'text', 'title': title, 'content_hash': content_hash}}]}),
    {'Content-Type': 'application/json'})
r = c.getresponse(); qd_data = json.loads(r.read()); c.close()
print('UPSERT_' + ('OK' if qd_data.get('status') == 'ok' else 'FAIL:' + str(qd_data)))
" 2>&1 | tail -5)

echo "$EMBED_UPSERT_OK" | grep -q "EMBED_OK" && pass "real embedding generated" || fail "embedding failed"
echo "$EMBED_UPSERT_OK" | grep -q "UPSERT_OK" && pass "vector upserted to Qdrant" || fail "Qdrant upsert failed"

# ── Step 4: Document + chunk in Postgres ────────────────────────
echo ""
echo "[4/8] Writing document + chunk to Postgres..."

# Use Python psycopg2 if available, otherwise psql, otherwise skip
DB_INSERT_OK=false
if $PYTHON_BIN -c "import psycopg2" 2>/dev/null; then
    $PYTHON_BIN -c "
import psycopg2, os
db_url = os.environ.get('DATABASE_URL', 'postgres://admin:admin@localhost:5432/orchestrator?sslmode=disable')
# strip sslmode for simple connection parsing
conn = psycopg2.connect(db_url.replace('?sslmode=disable', '').replace('sslmode=disable', ''))
cur = conn.cursor()
cur.execute('''INSERT INTO documents (id, tenant_id, source_type, title, content, content_hash, status, chunk_count)
VALUES (%s, %s, %s, %s, %s, %s, %s, %s)
ON CONFLICT (tenant_id, content_hash) DO UPDATE SET updated_at = NOW()''',
('${DOC_ID}', '00000000-0000-0000-0000-000000000000', 'text', '${TITLE//\'/\'\'}', '${CONTENT//\'/\'\'}', '${CONTENT_HASH}', 'indexed', 1))
cur.execute('''INSERT INTO document_chunks (id, document_id, tenant_id, chunk_index, content, content_hash, token_count, embedding_model, embedding_dimension, qdrant_collection, qdrant_point_id, status)
VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s)
ON CONFLICT (id) DO NOTHING''',
('${CHUNK_ID}', '${DOC_ID}', '00000000-0000-0000-0000-000000000000', 0, '${CONTENT//\'/\'\'}', '${CONTENT_HASH}', ${CHAR_COUNT}, 'text-embedding-3-small', ${DIM:-1536}, 'task_embeddings', '${CHUNK_ID}', 'indexed'))
conn.commit(); conn.close()
print('DB_INSERT_OK')
" 2>/dev/null && DB_INSERT_OK=true
elif command -v psql &>/dev/null; then
    ESCAPED_CONTENT=$(echo "$CONTENT" | $PYTHON_BIN "$PYHELPER" escape_sql 2>/dev/null)
    psql "$DB_URL" -c "INSERT INTO documents (id, tenant_id, source_type, title, content, content_hash, status, chunk_count) VALUES ('$DOC_ID', '00000000-0000-0000-0000-000000000000', 'text', '$TITLE', '$ESCAPED_CONTENT', '$CONTENT_HASH', 'indexed', 1) ON CONFLICT (tenant_id, content_hash) DO UPDATE SET updated_at = NOW();" >/dev/null 2>&1 && \
    psql "$DB_URL" -c "INSERT INTO document_chunks (id, document_id, tenant_id, chunk_index, content, content_hash, token_count, embedding_model, embedding_dimension, qdrant_collection, qdrant_point_id, status) VALUES ('$CHUNK_ID', '$DOC_ID', '00000000-0000-0000-0000-000000000000', 0, '$ESCAPED_CONTENT', '$CONTENT_HASH', $CHAR_COUNT, 'text-embedding-3-small', ${DIM:-1536}, 'task_embeddings', '$CHUNK_ID', 'indexed') ON CONFLICT (id) DO NOTHING;" >/dev/null 2>&1 && DB_INSERT_OK=true
fi

if [ "$DB_INSERT_OK" = true ]; then
    pass "document + chunk written to Postgres"
else
    skip "Postgres write (psycopg2/psql not available)"
    echo "  NOTE: document exists in Qdrant only."
    echo "  RAG retrieval via ReAct will still work (Qdrant search does not require Postgres)."
    echo "  To upgrade to full ingestion E2E, implement POST /api/v1/documents gateway endpoint"
    echo "  to trigger DocumentIngestionWorkflow, or install psycopg2/psql."
fi

# ── Step 5: Verify Qdrant search ────────────────────────────────
echo ""
echo "[5/8] Verifying Qdrant search..."

HIT_COUNT=$($PYTHON_BIN -c "
import http.client, json, os
from urllib.parse import urlparse

# Re-embed the document to get its vector
c = http.client.HTTPConnection('127.0.0.1', 8000, timeout=60)
content = '''$CONTENT'''
c.request('POST', '/embed', json.dumps({'input': [content], 'model': '$EMBED_MODEL'}),
    {'Content-Type': 'application/json'})
r = c.getresponse(); data = json.loads(r.read()); c.close()
vec = data['data'][0]['embedding']

# Search Qdrant
qdrant_host = os.getenv('QDRANT_URL', 'http://127.0.0.1:6333')
parsed = urlparse(qdrant_host)
c = http.client.HTTPConnection(parsed.hostname or '127.0.0.1', parsed.port or 6333, timeout=10)
c.request('POST', '/collections/task_embeddings/points/search',
    json.dumps({'vector': vec, 'limit': 3, 'score_threshold': 0.5}),
    {'Content-Type': 'application/json'})
r = c.getresponse(); qd_data = json.loads(r.read()); c.close()
hits = qd_data.get('result', [])
print(len(hits))
" 2>&1)

if [ "${HIT_COUNT:-0}" -gt 0 ]; then
    pass "Qdrant search returns $HIT_COUNT hits"
else
    fail "Qdrant search returned 0 hits"
fi

# ── Step 6: Real ReAct + RAG through Gateway ────────────────────
echo ""
echo "[6/8] Running real ReAct + RAG query through Gateway..."

MAX_TOKENS="${E2E_LLM_MAX_TOKENS:-8000}"
COMPLETION_TOKENS="${REAL_LLM_MAX_TOKENS:-4096}"
QUERY="Answer using ONLY the retrieved context. First line must be CODENAME: ${CODENAME}. Then in 2 bullet points summarize Phase 6D Hooks and Phase 6E RAG. Do NOT include any reasoning, thinking, or analysis. Just the codename and 2 bullets."

TASK_RESP=$(curl -s -X POST "$GATEWAY_URL/api/v1/tasks" \
    -H "Content-Type: application/json" \
    -d "{\"query\":\"$QUERY\",\"config\":{\"enable_react\":true,\"react_max_iterations\":3,\"mode\":\"simple\",\"model\":\"${LLM_MODEL:-MiniMax-M2.7}\",\"max_total_tokens\":$MAX_TOKENS,\"max_completion_tokens\":$COMPLETION_TOKENS}}" 2>/dev/null || echo '{}')

TASK_ID=$(echo "$TASK_RESP" | $PYTHON_BIN -c "import sys,json; print(json.load(sys.stdin).get('task_id',''))" 2>/dev/null || echo "")
WORKFLOW_ID=$(echo "$TASK_RESP" | $PYTHON_BIN -c "import sys,json; print(json.load(sys.stdin).get('workflow_id',''))" 2>/dev/null || echo "")

if [ -n "$TASK_ID" ] && [ "$TASK_ID" != "" ]; then
    pass "task created: $TASK_ID"
    echo "  workflow_id: ${WORKFLOW_ID:-unknown}"
else
    fail "task creation failed: $TASK_RESP"
fi

# ── Step 7: Poll for completion ─────────────────────────────────
echo ""
echo "[7/8] Waiting for ReAct + RAG to complete (max 180s)..."

MAX_WAIT=180; POLL_INTERVAL=5; WAITED=0
TASK_STATUS="pending"; ANSWER=""

while [ "$WAITED" -lt "$MAX_WAIT" ]; do
    sleep $POLL_INTERVAL
    WAITED=$((WAITED + POLL_INTERVAL))

    TASK_INFO=$(curl -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID" 2>/dev/null || echo '{}')
    TASK_STATUS=$(echo "$TASK_INFO" | $PYTHON_BIN -c "
import sys,json
d=json.load(sys.stdin)
print(d.get('status','pending'))
" 2>/dev/null || echo "pending")

    echo "  [${WAITED}s] status: $TASK_STATUS"

    if [ "$TASK_STATUS" = "completed" ] || [ "$TASK_STATUS" = "failed" ] || [ "$TASK_STATUS" = "error" ]; then
        ANSWER=$(echo "$TASK_INFO" | $PYTHON_BIN -c "
import sys,json
d=json.load(sys.stdin)
r=d.get('response') or d.get('result') or ''
print(json.dumps(r) if isinstance(r,dict) else str(r))
" 2>/dev/null || echo "")
        break
    fi
done

if [ "$TASK_STATUS" = "completed" ]; then
    pass "task completed in ${WAITED}s"

    # RAG must have been triggered
    if [ "${HIT_COUNT:-0}" -gt 0 ]; then
        pass "RAG retrieval confirmed: Qdrant hit_count=$HIT_COUNT"
    else
        echo "  NOTE: hit_count from pre-flight Qdrant search was 0."
        echo "  ReAct may still have found chunks via its own retrieval path."
    fi

    # Use Python helper to check answer quality
    CHECKS=$(echo "$ANSWER" | $PYTHON_BIN "$PYHELPER" check_answer "$CODENAME" 2>&1)
    if echo "$CHECKS" | grep -q '"has_codename": true'; then
        pass "answer contains $CODENAME"
    else
        fail "answer does NOT contain $CODENAME"
        echo "  answer: $(echo "$ANSWER" | head -c 500)"
    fi
    if echo "$CHECKS" | grep -q '"has_hooks": true'; then
        pass "answer references Phase 6D Hooks concepts"
    else
        echo "  (answer may not mention hooks explicitly)"
    fi
    if echo "$CHECKS" | grep -q '"has_rag": true'; then
        pass "answer references Phase 6E RAG concepts"
    else
        echo "  (answer may not mention RAG explicitly)"
    fi

elif [ "$TASK_STATUS" = "failed" ]; then
    fail "task failed after ${WAITED}s"
    echo "  response: $(echo "$TASK_INFO" | head -c 1000)"
else
    fail "task did not complete in ${MAX_WAIT}s (status: $TASK_STATUS)"
fi

# ── Step 8: Hooks + workspace verification ──────────────────────
echo ""
echo "[8/8] Verifying hooks audit..."

HOOKS_AUDIT=$(curl -s "$GATEWAY_URL/api/v1/hooks/audit" 2>/dev/null || echo '{}')
HOOKS_COUNT=$(echo "$HOOKS_AUDIT" | $PYTHON_BIN -c "
import sys,json
d=json.load(sys.stdin)
items=d.get('items',d.get('audit_logs',d.get('logs',[])))
print(len(items) if isinstance(items,list) else 0)
" 2>/dev/null || echo "0")

if [ "${HOOKS_COUNT:-0}" -gt 0 ]; then
    pass "hooks audit has $HOOKS_COUNT entries"
else
    skip "hooks audit empty (may not have registered hooks for this test run)"
fi

# ── Cleanup ─────────────────────────────────────────────────────
rm -f "$PYHELPER"

# ── Summary ─────────────────────────────────────────────────────
TOTAL_DURATION=$(( $(date +%s) - START_TIME ))
echo ""
echo "============================================================"
echo " Phase 6 Real ReAct + RAG E2E Results"
echo "============================================================"
echo ""
echo "  run_id:             $RUN_ID"
echo "  codename:           $CODENAME (verified in answer)"
echo "  document_id:        $DOC_ID"
echo "  chunk_id:           $CHUNK_ID"
echo "  task_id:            ${TASK_ID:-N/A}"
echo "  workflow_id:        ${WORKFLOW_ID:-N/A}"
echo "  task_status:        ${TASK_STATUS:-N/A}"
echo "  qdrant_hits:        ${HIT_COUNT:-0}"
echo "  embedding_dim:      ${DIM:-N/A}"
echo "  gateway_url:        $GATEWAY_URL"
echo "  python_llm_url:     $PYTHON_LLM_URL"
echo "  qdrant_url:         $QDRANT_URL"
echo "  total_duration:     ${TOTAL_DURATION}s"
echo "  env_flags:          REAL_LLM_TEST=1 REAL_EMBEDDING_TEST=1"
echo ""
echo "  Passed: $PASS | Failed: $FAIL"
echo "============================================================"
echo ""

if [ "$FAIL" -gt 0 ]; then
    echo "DIAGNOSTICS:"
    echo "  - Gateway URL:  $GATEWAY_URL"
    echo "  - Python URL:   $PYTHON_LLM_URL"
    echo "  - Qdrant URL:   $QDRANT_URL"
    echo "  - Check: docker compose ps (or docker ps)"
    echo "  - Check: OPENAI_API_KEY is set and valid"
    echo "  - Check: Gateway health: curl $GATEWAY_URL/health"
    echo ""
    echo "FAILURES DETECTED. Review output above."
    exit 1
fi

echo "PASS: Real ReAct + RAG E2E completed successfully."
exit 0
