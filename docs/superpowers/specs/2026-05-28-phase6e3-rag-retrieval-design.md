# Phase 6E-3: RAG Query Retrieval — Design Spec

## 版本说明

在 6E-1 (embedding + Qdrant) 和 6E-2 (document ingestion) 基础上，实现 RAG 检索管线。不做 ReAct/Swarm 集成（6E-4）。

## 1. Architecture

```
RAGQueryWorkflow (deterministic, no vectors in history)
  ├─ EmbedQueryActivity       → Python /embed (query vector)
  ├─ SearchChunksActivity     → Qdrant search (vector → IDs + scores)
  ├─ FetchChunkContentActivity → Postgres: get chunk text by IDs
  └─ PackContextActivity      → Format LLM context with citations
```

**Query vector stays inside EmbedQueryActivity. Only chunk IDs + scores return.**

## 2. Activities (all new)

### EmbedQueryActivity
- Input: query_text, model
- Output: vector ([]float64) — stays in Activity, NOT returned to workflow
- Actually: combine with Search into single EmbedAndSearchChunksActivity to keep vector contained

### EmbedAndSearchChunksActivity (combined)
- Input: query_text, model, collection, top_k, threshold
- Internal: Python /embed → Qdrant search
- Output: []SearchHit { chunk_id, qdrant_point_id, score, payload }
- Vector never leaves this Activity

### FetchChunkContentActivity
- Input: []chunk_id
- Output: []ChunkContent { chunk_id, document_id, content, content_hash, title, chunk_index, score }

### PackContextActivity
- Input: []ChunkContent, query_text, max_tokens
- Output: packed_context_string, citations[], token_count

## 3. Workflow

```
RAGQueryWorkflow:
  EmbedAndSearch → FetchChunkContent → PackContext
```

- Failure → error returned
- Empty results → empty context, not error
- Hook emission: on_error on failure

## 4. Go files

```
internal/activities/
  rag_retrieval.go         — EmbedAndSearchChunksActivity, FetchChunkContentActivity, PackContextActivity
  rag_retrieval_test.go

internal/workflows/
  rag_query.go             — RAGQueryWorkflow
  rag_query_test.go
```

## 5. Tests
- Activities with mock embedding + mock Qdrant + nil DB
- Workflow success / empty results / failure
- PackContext formatting correctness
- E2E smoke script

## 6. Non-Goals
- No ReAct/Swarm integration
- No API endpoints (can add if time)
- No modifying go/ or python/llm-service/
