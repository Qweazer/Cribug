package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Document represents a document record in the documents table.
type Document struct {
	ID          string
	TenantID    string
	SourceType  string
	SourceURI   string
	Title       string
	Content     string
	ContentHash string
	Status      string
	ChunkCount  int
	Error       string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// DocumentChunk represents a chunk record in the document_chunks table.
type DocumentChunk struct {
	ID                 string
	DocumentID         string
	TenantID           string
	ChunkIndex         int
	Content            string
	ContentHash        string
	TokenCount         int
	EmbeddingModel     string
	EmbeddingDimension int
	QdrantCollection   string
	QdrantPointID      *string
	Status             string
	Error              string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// DocumentRepository provides CRUD operations for documents and chunks.
type DocumentRepository struct {
	db *sql.DB
}

// NewDocumentRepository creates a new DocumentRepository.
func NewDocumentRepository(db *sql.DB) *DocumentRepository {
	return &DocumentRepository{db: db}
}

// CreateDocument inserts a new document. If doc.ID is empty, a UUID is generated.
func (r *DocumentRepository) CreateDocument(ctx context.Context, doc *Document) error {
	if r.db == nil {
		return fmt.Errorf("document repository: db not initialized")
	}
	if doc.ID == "" {
		doc.ID = uuid.New().String()
	}
	if doc.Status == "" {
		doc.Status = "pending"
	}
	if doc.CreatedAt.IsZero() {
		doc.CreatedAt = time.Now().UTC()
	}
	if doc.UpdatedAt.IsZero() {
		doc.UpdatedAt = time.Now().UTC()
	}

	query := `INSERT INTO documents
		(id, tenant_id, source_type, source_uri, title, content, content_hash,
		 status, chunk_count, error, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`
	_, err := r.db.ExecContext(ctx, query,
		doc.ID, doc.TenantID, doc.SourceType, doc.SourceURI,
		doc.Title, doc.Content, doc.ContentHash, doc.Status,
		doc.ChunkCount, doc.Error, doc.CreatedAt, doc.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert document: %w", err)
	}
	return nil
}

// GetDocument retrieves a document by ID. Returns nil if not found.
func (r *DocumentRepository) GetDocument(ctx context.Context, id string) (*Document, error) {
	if r.db == nil {
		return nil, fmt.Errorf("document repository: db not initialized")
	}
	query := `SELECT id, tenant_id, source_type, source_uri, title, content, content_hash,
		status, chunk_count, error, created_at, updated_at
		FROM documents WHERE id = $1`
	var doc Document
	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&doc.ID, &doc.TenantID, &doc.SourceType, &doc.SourceURI,
		&doc.Title, &doc.Content, &doc.ContentHash, &doc.Status,
		&doc.ChunkCount, &doc.Error, &doc.CreatedAt, &doc.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get document: %w", err)
	}
	return &doc, nil
}

// GetChunkByID retrieves a single chunk by its ID. Returns nil if not found.
func (r *DocumentRepository) GetChunkByID(ctx context.Context, id string) (*DocumentChunk, error) {
	if r.db == nil {
		return nil, fmt.Errorf("document repository: db not initialized")
	}
	query := `SELECT id, document_id, tenant_id, chunk_index, content, content_hash,
		token_count, embedding_model, embedding_dimension,
		qdrant_collection, qdrant_point_id, status, error, created_at, updated_at
		FROM document_chunks WHERE id = $1`
	var c DocumentChunk
	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&c.ID, &c.DocumentID, &c.TenantID, &c.ChunkIndex,
		&c.Content, &c.ContentHash, &c.TokenCount,
		&c.EmbeddingModel, &c.EmbeddingDimension,
		&c.QdrantCollection, &c.QdrantPointID,
		&c.Status, &c.Error, &c.CreatedAt, &c.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get chunk: %w", err)
	}
	return &c, nil
}

// FindByContentHash looks up a document by tenant_id and content_hash.
// Returns nil if not found.
func (r *DocumentRepository) FindByContentHash(ctx context.Context, tenantID, hash string) (*Document, error) {
	if r.db == nil {
		return nil, fmt.Errorf("document repository: db not initialized")
	}
	query := `SELECT id, tenant_id, source_type, source_uri, title, content, content_hash,
		status, chunk_count, error, created_at, updated_at
		FROM documents WHERE tenant_id = $1 AND content_hash = $2`
	var doc Document
	err := r.db.QueryRowContext(ctx, query, tenantID, hash).Scan(
		&doc.ID, &doc.TenantID, &doc.SourceType, &doc.SourceURI,
		&doc.Title, &doc.Content, &doc.ContentHash, &doc.Status,
		&doc.ChunkCount, &doc.Error, &doc.CreatedAt, &doc.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find document by content hash: %w", err)
	}
	return &doc, nil
}

// UpdateDocumentStatus updates a document's status, error, chunk_count, and updated_at.
func (r *DocumentRepository) UpdateDocumentStatus(ctx context.Context, id, status, error string, chunkCount int) error {
	if r.db == nil {
		return fmt.Errorf("document repository: db not initialized")
	}
	query := `UPDATE documents SET status = $2, error = $3, chunk_count = $4, updated_at = NOW() WHERE id = $1`
	_, err := r.db.ExecContext(ctx, query, id, status, error, chunkCount)
	if err != nil {
		return fmt.Errorf("update document status: %w", err)
	}
	return nil
}

// InsertChunks inserts chunks one at a time in a loop.
func (r *DocumentRepository) InsertChunks(ctx context.Context, chunks []DocumentChunk) error {
	if r.db == nil {
		return fmt.Errorf("document repository: db not initialized")
	}
	for i := range chunks {
		if chunks[i].ID == "" {
			chunks[i].ID = uuid.New().String()
		}
		if chunks[i].Status == "" {
			chunks[i].Status = "pending"
		}
		if chunks[i].CreatedAt.IsZero() {
			chunks[i].CreatedAt = time.Now().UTC()
		}
		if chunks[i].UpdatedAt.IsZero() {
			chunks[i].UpdatedAt = time.Now().UTC()
		}

		query := `INSERT INTO document_chunks
			(id, document_id, tenant_id, chunk_index, content, content_hash,
			 token_count, embedding_model, embedding_dimension,
			 qdrant_collection, qdrant_point_id, status, error, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`
		_, err := r.db.ExecContext(ctx, query,
			chunks[i].ID, chunks[i].DocumentID, chunks[i].TenantID, chunks[i].ChunkIndex,
			chunks[i].Content, chunks[i].ContentHash, chunks[i].TokenCount,
			chunks[i].EmbeddingModel, chunks[i].EmbeddingDimension,
			chunks[i].QdrantCollection, chunks[i].QdrantPointID,
			chunks[i].Status, chunks[i].Error, chunks[i].CreatedAt, chunks[i].UpdatedAt)
		if err != nil {
			return fmt.Errorf("insert chunk %d: %w", i, err)
		}
	}
	return nil
}

// ListChunksByDocument returns all chunks for a document ordered by chunk_index.
func (r *DocumentRepository) ListChunksByDocument(ctx context.Context, documentID string) ([]DocumentChunk, error) {
	if r.db == nil {
		return nil, fmt.Errorf("document repository: db not initialized")
	}
	query := `SELECT id, document_id, tenant_id, chunk_index, content, content_hash,
		token_count, embedding_model, embedding_dimension,
		qdrant_collection, qdrant_point_id, status, error, created_at, updated_at
		FROM document_chunks WHERE document_id = $1 ORDER BY chunk_index`
	rows, err := r.db.QueryContext(ctx, query, documentID)
	if err != nil {
		return nil, fmt.Errorf("list chunks: %w", err)
	}
	defer rows.Close()

	var chunks []DocumentChunk
	for rows.Next() {
		var c DocumentChunk
		if err := rows.Scan(
			&c.ID, &c.DocumentID, &c.TenantID, &c.ChunkIndex,
			&c.Content, &c.ContentHash, &c.TokenCount,
			&c.EmbeddingModel, &c.EmbeddingDimension,
			&c.QdrantCollection, &c.QdrantPointID,
			&c.Status, &c.Error, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan chunk: %w", err)
		}
		chunks = append(chunks, c)
	}
	if chunks == nil {
		chunks = []DocumentChunk{}
	}
	return chunks, rows.Err()
}

// UpdateChunkStatus updates a chunk's status, qdrant_point_id, error, and updated_at.
func (r *DocumentRepository) UpdateChunkStatus(ctx context.Context, chunkID, status string, pointID *string, error string) error {
	if r.db == nil {
		return fmt.Errorf("document repository: db not initialized")
	}
	query := `UPDATE document_chunks SET status = $2, qdrant_point_id = $3, error = $4, updated_at = NOW() WHERE id = $1`
	_, err := r.db.ExecContext(ctx, query, chunkID, status, pointID, error)
	if err != nil {
		return fmt.Errorf("update chunk status: %w", err)
	}
	return nil
}

// ListPendingChunks returns chunks with status 'pending' for a document ordered by chunk_index.
func (r *DocumentRepository) ListPendingChunks(ctx context.Context, documentID string) ([]DocumentChunk, error) {
	if r.db == nil {
		return nil, fmt.Errorf("document repository: db not initialized")
	}
	query := `SELECT id, document_id, tenant_id, chunk_index, content, content_hash,
		token_count, embedding_model, embedding_dimension,
		qdrant_collection, qdrant_point_id, status, error, created_at, updated_at
		FROM document_chunks WHERE document_id = $1 AND status = 'pending' ORDER BY chunk_index`
	rows, err := r.db.QueryContext(ctx, query, documentID)
	if err != nil {
		return nil, fmt.Errorf("list pending chunks: %w", err)
	}
	defer rows.Close()

	var chunks []DocumentChunk
	for rows.Next() {
		var c DocumentChunk
		if err := rows.Scan(
			&c.ID, &c.DocumentID, &c.TenantID, &c.ChunkIndex,
			&c.Content, &c.ContentHash, &c.TokenCount,
			&c.EmbeddingModel, &c.EmbeddingDimension,
			&c.QdrantCollection, &c.QdrantPointID,
			&c.Status, &c.Error, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan chunk: %w", err)
		}
		chunks = append(chunks, c)
	}
	if chunks == nil {
		chunks = []DocumentChunk{}
	}
	return chunks, rows.Err()
}
