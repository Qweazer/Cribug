# Phase 6E-2: Document Ingestion Pipeline — Design Spec

## 版本说明

在 6E-1 Embeddings + Qdrant Foundation 基础上，实现完整的文档摄入管线。不做 RAG query retrieval workflow，不接 ReAct/Swarm。

## 1. Architecture

```
DocumentIngestionWorkflow (deterministic)
  ├─ SaveDocumentMetadataActivity   → Postgres: INSERT documents
  ├─ ChunkDocumentActivity          → Go chunker service
  ├─ EmbedAndUpsertChunksActivity   → Python /embed → Qdrant upsert
  │   (vectors stay inside Activity, NOT in workflow history)
  └─ UpdateDocumentIndexStatusActivity → Postgres: UPDATE document + chunks
```

**Key: vectors never enter Workflow history. EmbedAndUpsertChunksActivity takes document_id, fetches pending chunks from DB, embeds them, upserts to Qdrant — all inside the Activity.**

## 2. Database Schema

### 2.1 documents
```sql
CREATE TABLE documents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000000',
    source_type VARCHAR(50) NOT NULL DEFAULT 'text',
    source_uri TEXT DEFAULT '',
    title VARCHAR(500) NOT NULL DEFAULT '',
    content TEXT NOT NULL,
    content_hash VARCHAR(64) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    chunk_count INTEGER DEFAULT 0,
    error TEXT DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);
CREATE UNIQUE INDEX idx_documents_content_hash ON documents(tenant_id, content_hash);
```

### 2.2 document_chunks
```sql
CREATE TABLE document_chunks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id UUID NOT NULL REFERENCES documents(id),
    tenant_id UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000000',
    chunk_index INTEGER NOT NULL,
    content TEXT NOT NULL,
    content_hash VARCHAR(64) NOT NULL,
    token_count INTEGER DEFAULT 0,
    embedding_model VARCHAR(100) DEFAULT '',
    embedding_dimension INTEGER DEFAULT 1536,
    qdrant_collection VARCHAR(100) DEFAULT 'task_embeddings',
    qdrant_point_id UUID,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    error TEXT DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);
CREATE INDEX idx_document_chunks_doc_id ON document_chunks(document_id);
```

## 3. Chunker

Package: `internal/rag/chunker/`

```go
type Config struct {
    ChunkSizeChars int // default 2000
    OverlapChars   int // default 200
    MaxChunks      int // default 500
    PreserveParagraphs bool // default false for simplicity in 6E-2
}

type ChunkResult struct {
    Chunks   []Chunk
}

type Chunk struct {
    Index       int
    Content     string
    ContentHash string // SHA256
    CharCount   int
    TokenEstimate int // char_count / 4
}
```

Behaviour: character-based sliding window with overlap. Empty chunks filtered. content_hash = SHA256(chunk content).

## 4. Document Repository

Package: `internal/db/documents.go`

Methods: CreateDocument, GetDocument, UpdateDocumentStatus, InsertChunks, ListChunksByDocument, UpdateChunkStatus, FindByContentHash

## 5. Activities

- SaveDocumentMetadataActivity
- ChunkDocumentActivity
- EmbedAndUpsertChunksActivity (combined: embedding + Qdrant, vectors never leave Activity)
- UpdateDocumentIndexStatusActivity

## 6. Workflow

```
DocumentIngestionWorkflow:
  SaveDocumentMetadata → ChunkDocument → EmbedAndUpsertChunks → UpdateIndexStatus
```

- Failure at any step → MarkDocumentFailed + on_error hook
- Idempotent: duplicate content_hash returns existing document_id
- No vectors in workflow history

## 7. Qdrant Payload

Per chunk: `{tenant_id, document_id, chunk_id, chunk_index, source_type, source_uri, title, content_hash}`
Chunk text stays in Postgres, NOT in Qdrant payload.

## 8. Tests
- Chunker correctness, overlap, empty, max_chunks
- DB repository CRUD
- Activities with mock embedding + mock Qdrant
- Workflow success + failure paths
- E2E smoke script

## 9. Non-Goals
- No RAG query retrieval (6E-3)
- No ReAct/Swarm integration (6E-4)
- No modifying go/ or python/llm-service/
