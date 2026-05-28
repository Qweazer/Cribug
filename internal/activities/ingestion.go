package activities

import (
	"context"
	"crypto/sha256"
	"fmt"

	"cribug/internal/db"
	"cribug/internal/embeddings"
	"cribug/internal/rag/chunker"
	"cribug/internal/vectordb"
)

// IngestionActivities handles document ingestion Temporal activities
// for the document processing pipeline (save metadata, chunk, embed, index).
type IngestionActivities struct {
	docRepo    *db.DocumentRepository
	chunkerCfg chunker.Config
	embSvc     *embeddings.Service
	vdbClient  *vectordb.Client
}

// NewIngestionActivities creates a new IngestionActivities.
func NewIngestionActivities(docRepo *db.DocumentRepository, embSvc *embeddings.Service, vdbClient *vectordb.Client) *IngestionActivities {
	return &IngestionActivities{
		docRepo:    docRepo,
		chunkerCfg: chunker.DefaultConfig(),
		embSvc:     embSvc,
		vdbClient:  vdbClient,
	}
}

// --- Input / Output Types ---

// SaveDocumentInput contains the metadata for a document to ingest.
type SaveDocumentInput struct {
	TenantID   string
	SourceType string
	SourceURI  string
	Title      string
	Content    string
}

// SaveDocumentOutput contains the result of saving document metadata.
type SaveDocumentOutput struct {
	DocumentID  string
	IsDuplicate bool // true if content_hash already existed
}

// ChunkDocumentInput identifies the document to chunk.
type ChunkDocumentInput struct {
	DocumentID string
}

// ChunkDocumentOutput contains the result of chunking.
type ChunkDocumentOutput struct {
	DocumentID string
	ChunkCount int
}

// EmbedAndUpsertInput identifies the document whose pending chunks should be embedded and upserted.
type EmbedAndUpsertInput struct {
	DocumentID string
	Collection string // Qdrant collection name, default "task_embeddings"
	Model      string // embedding model, default "text-embedding-3-small"
}

// EmbedAndUpsertOutput contains the result of embedding and upserting chunks.
type EmbedAndUpsertOutput struct {
	DocumentID   string
	IndexedCount int
}

// UpdateIndexStatusInput identifies the document to mark as indexed.
type UpdateIndexStatusInput struct {
	DocumentID string
}

// UpdateIndexStatusOutput contains the result of updating index status.
type UpdateIndexStatusOutput struct {
	DocumentID string
	Status     string
}

// --- Activities ---

// SaveDocumentMetadataActivity computes the content hash, checks for duplicates,
// and persists the document metadata with status "pending".
func (a *IngestionActivities) SaveDocumentMetadataActivity(ctx context.Context, input SaveDocumentInput) (SaveDocumentOutput, error) {
	hash := sha256Hex(input.Content)

	existing, err := a.docRepo.FindByContentHash(ctx, input.TenantID, hash)
	if err != nil {
		return SaveDocumentOutput{}, fmt.Errorf("check content hash: %w", err)
	}
	if existing != nil {
		return SaveDocumentOutput{DocumentID: existing.ID, IsDuplicate: true}, nil
	}

	doc := &db.Document{
		TenantID:    input.TenantID,
		SourceType:  input.SourceType,
		SourceURI:   input.SourceURI,
		Title:       input.Title,
		Content:     input.Content,
		ContentHash: hash,
		Status:      "pending",
	}
	if err := a.docRepo.CreateDocument(ctx, doc); err != nil {
		return SaveDocumentOutput{}, fmt.Errorf("create document: %w", err)
	}

	return SaveDocumentOutput{DocumentID: doc.ID, IsDuplicate: false}, nil
}

// ChunkDocumentActivity retrieves the document content, splits it into chunks via
// the chunker, persists the chunks, and updates the document status to "chunked".
func (a *IngestionActivities) ChunkDocumentActivity(ctx context.Context, input ChunkDocumentInput) (ChunkDocumentOutput, error) {
	doc, err := a.docRepo.GetDocument(ctx, input.DocumentID)
	if err != nil {
		return ChunkDocumentOutput{}, fmt.Errorf("get document: %w", err)
	}
	if doc == nil {
		return ChunkDocumentOutput{}, fmt.Errorf("document not found: %s", input.DocumentID)
	}

	chunks := chunker.ChunkText(doc.Content, a.chunkerCfg)
	if chunks == nil {
		// Empty or whitespace-only content — still mark as chunked with 0
		if err := a.docRepo.UpdateDocumentStatus(ctx, doc.ID, "chunked", "", 0); err != nil {
			return ChunkDocumentOutput{}, fmt.Errorf("update document status: %w", err)
		}
		return ChunkDocumentOutput{DocumentID: doc.ID, ChunkCount: 0}, nil
	}

	docChunks := make([]db.DocumentChunk, len(chunks))
	for i, c := range chunks {
		docChunks[i] = db.DocumentChunk{
			DocumentID:  doc.ID,
			TenantID:    doc.TenantID,
			ChunkIndex:  c.Index,
			Content:     c.Content,
			ContentHash: c.ContentHash,
			TokenCount:  c.TokenEstimate,
			Status:      "pending",
		}
	}

	if err := a.docRepo.InsertChunks(ctx, docChunks); err != nil {
		return ChunkDocumentOutput{}, fmt.Errorf("insert chunks: %w", err)
	}

	if err := a.docRepo.UpdateDocumentStatus(ctx, doc.ID, "chunked", "", len(chunks)); err != nil {
		return ChunkDocumentOutput{}, fmt.Errorf("update document status: %w", err)
	}

	return ChunkDocumentOutput{DocumentID: doc.ID, ChunkCount: len(chunks)}, nil
}

// EmbedAndUpsertChunksActivity reads pending chunks for a document, computes
// embeddings via the embedding service, upserts the vectors into Qdrant,
// and updates each chunk's status to "indexed".
//
// VECTORS NEVER LEAVE THIS ACTIVITY — vectors are computed and immediately upserted.
func (a *IngestionActivities) EmbedAndUpsertChunksActivity(ctx context.Context, input EmbedAndUpsertInput) (EmbedAndUpsertOutput, error) {
	collection := input.Collection
	if collection == "" {
		collection = "task_embeddings"
	}
	model := input.Model
	if model == "" {
		model = "text-embedding-3-small"
	}

	doc, err := a.docRepo.GetDocument(ctx, input.DocumentID)
	if err != nil {
		return EmbedAndUpsertOutput{}, fmt.Errorf("get document: %w", err)
	}
	if doc == nil {
		return EmbedAndUpsertOutput{}, fmt.Errorf("document not found: %s", input.DocumentID)
	}

	chunks, err := a.docRepo.ListPendingChunks(ctx, input.DocumentID)
	if err != nil {
		return EmbedAndUpsertOutput{}, fmt.Errorf("list pending chunks: %w", err)
	}
	if len(chunks) == 0 {
		return EmbedAndUpsertOutput{DocumentID: input.DocumentID, IndexedCount: 0}, nil
	}

	var points []vectordb.VectorPoint
	for _, chunk := range chunks {
		result, err := a.embSvc.EmbedText(ctx, chunk.Content, model)
		if err != nil {
			return EmbedAndUpsertOutput{}, fmt.Errorf("embed chunk %s: %w", chunk.ID, err)
		}
		if len(result.Vectors) == 0 {
			return EmbedAndUpsertOutput{}, fmt.Errorf("no vectors returned for chunk %s", chunk.ID)
		}

		points = append(points, vectordb.VectorPoint{
			ID:     chunk.ID,
			Vector: result.Vectors[0],
			Payload: map[string]interface{}{
				"tenant_id":    chunk.TenantID,
				"document_id":  chunk.DocumentID,
				"chunk_id":     chunk.ID,
				"chunk_index":  chunk.ChunkIndex,
				"source_type":  doc.SourceType,
				"source_uri":   doc.SourceURI,
				"title":        doc.Title,
				"content_hash": chunk.ContentHash,
			},
		})
	}

	// Upsert in batches of 20
	batchSize := 20
	for i := 0; i < len(points); i += batchSize {
		end := i + batchSize
		if end > len(points) {
			end = len(points)
		}
		if err := a.vdbClient.Upsert(ctx, collection, points[i:end]); err != nil {
			return EmbedAndUpsertOutput{}, fmt.Errorf("upsert batch at index %d: %w", i, err)
		}
	}

	// Mark each chunk as indexed
	for _, chunk := range chunks {
		pointID := chunk.ID
		if err := a.docRepo.UpdateChunkStatus(ctx, chunk.ID, "indexed", &pointID, ""); err != nil {
			return EmbedAndUpsertOutput{}, fmt.Errorf("update chunk status %s: %w", chunk.ID, err)
		}
	}

	return EmbedAndUpsertOutput{DocumentID: input.DocumentID, IndexedCount: len(chunks)}, nil
}

// UpdateDocumentIndexStatusActivity updates the document status to "indexed"
// after all chunks have been successfully embedded and upserted.
func (a *IngestionActivities) UpdateDocumentIndexStatusActivity(ctx context.Context, input UpdateIndexStatusInput) (UpdateIndexStatusOutput, error) {
	if err := a.docRepo.UpdateDocumentStatus(ctx, input.DocumentID, "indexed", "", 0); err != nil {
		return UpdateIndexStatusOutput{}, fmt.Errorf("update document status: %w", err)
	}
	return UpdateIndexStatusOutput{DocumentID: input.DocumentID, Status: "indexed"}, nil
}

// sha256Hex returns the lowercase hex-encoded SHA-256 hash of s.
func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", h)
}
