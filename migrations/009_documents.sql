-- Migration 009: documents and document_chunks tables for Phase 6E-2

CREATE TABLE IF NOT EXISTS documents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000000',
    source_type VARCHAR(50) NOT NULL DEFAULT 'text',
    source_uri TEXT NOT NULL DEFAULT '',
    title VARCHAR(500) NOT NULL DEFAULT '',
    content TEXT NOT NULL,
    content_hash VARCHAR(64) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    chunk_count INTEGER DEFAULT 0,
    error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_documents_content_hash ON documents(tenant_id, content_hash);
CREATE INDEX IF NOT EXISTS idx_documents_status ON documents(status);

CREATE TABLE IF NOT EXISTS document_chunks (
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
    error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_document_chunks_doc_id ON document_chunks(document_id);
CREATE INDEX IF NOT EXISTS idx_document_chunks_status ON document_chunks(status);
