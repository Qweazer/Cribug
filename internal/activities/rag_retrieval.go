package activities

import (
	"context"
	"fmt"
	"strings"

	"cribug/internal/db"
	"cribug/internal/embeddings"
	"cribug/internal/vectordb"
)

// RAGRetrievalActivities provides Temporal activities for RAG-based retrieval:
// embedding + vector search, chunk content fetching, and context packing.
type RAGRetrievalActivities struct {
	embSvc    *embeddings.Service
	vdbClient *vectordb.Client
	docRepo   *db.DocumentRepository
}

// NewRAGRetrievalActivities creates a new RAGRetrievalActivities.
func NewRAGRetrievalActivities(embSvc *embeddings.Service, vdbClient *vectordb.Client, docRepo *db.DocumentRepository) *RAGRetrievalActivities {
	return &RAGRetrievalActivities{
		embSvc:    embSvc,
		vdbClient: vdbClient,
		docRepo:   docRepo,
	}
}

// ---------------------------------------------------------------------------
// Activity 1: EmbedAndSearchChunksActivity
// ---------------------------------------------------------------------------

// EmbedAndSearchChunksInput contains the query text and search parameters.
type EmbedAndSearchChunksInput struct {
	QueryText  string
	Collection string  // Qdrant collection, default "task_embeddings"
	Model      string  // embedding model
	TopK       int     // default 5
	Threshold  float64 // default 0.7
}

// SearchHit represents a single vector search hit with metadata.
// No vector data is exposed — only IDs, scores, and payload.
type SearchHit struct {
	ChunkID       string
	QdrantPointID string
	Score         float64
	Payload       map[string]interface{} // from Qdrant
}

// EmbedAndSearchChunksOutput contains the search hits.
type EmbedAndSearchChunksOutput struct {
	Hits []SearchHit
}

// EmbedAndSearchChunksActivity embeds the query text, searches the vector DB,
// and returns search hits. The embedding vector is computed and consumed
// entirely within this activity and never returned to the workflow.
func (a *RAGRetrievalActivities) EmbedAndSearchChunksActivity(ctx context.Context, input EmbedAndSearchChunksInput) (EmbedAndSearchChunksOutput, error) {
	collection := input.Collection
	if collection == "" {
		collection = "task_embeddings"
	}
	topK := input.TopK
	if topK <= 0 {
		topK = 5
	}
	threshold := input.Threshold
	if threshold <= 0 {
		threshold = 0.7
	}

	// Embed the query text — vector stays inside this activity.
	result, err := a.embSvc.EmbedText(ctx, input.QueryText, input.Model)
	if err != nil {
		return EmbedAndSearchChunksOutput{}, fmt.Errorf("embed query: %w", err)
	}
	if len(result.Vectors) == 0 {
		return EmbedAndSearchChunksOutput{}, fmt.Errorf("embedding returned zero vectors")
	}

	// Search Qdrant with the computed vector.
	searchResults, err := a.vdbClient.Search(ctx, collection, result.Vectors[0], vectordb.SearchOptions{
		TopK:      topK,
		Threshold: threshold,
	})
	if err != nil {
		return EmbedAndSearchChunksOutput{}, fmt.Errorf("vector search: %w", err)
	}

	// Map results to SearchHit — no vectors exposed.
	hits := make([]SearchHit, len(searchResults))
	for i, sr := range searchResults {
		chunkID := extractChunkID(sr.Payload)
		if chunkID == "" {
			chunkID = sr.ID
		}
		hits[i] = SearchHit{
			ChunkID:       chunkID,
			QdrantPointID: sr.ID,
			Score:         sr.Score,
			Payload:       sr.Payload,
		}
	}

	return EmbedAndSearchChunksOutput{Hits: hits}, nil
}

// extractChunkID pulls the "chunk_id" from a Qdrant payload, returning empty
// if the key is absent or not a string.
func extractChunkID(payload map[string]interface{}) string {
	if payload == nil {
		return ""
	}
	if id, ok := payload["chunk_id"]; ok {
		if s, ok := id.(string); ok {
			return s
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Activity 2: FetchChunkContentActivity
// ---------------------------------------------------------------------------

// FetchChunkContentInput identifies the chunks whose content should be fetched.
type FetchChunkContentInput struct {
	ChunkIDs []string
}

// ChunkContent holds the full content and metadata for a single chunk.
type ChunkContent struct {
	ChunkID     string
	DocumentID  string
	Content     string
	ContentHash string
	Title       string
	ChunkIndex  int
	Score       float64
}

// FetchChunkContentOutput contains the fetched chunk contents.
type FetchChunkContentOutput struct {
	Chunks []ChunkContent
}

// FetchChunkContentActivity retrieves chunk content and document titles
// for the given chunk IDs from the document repository.
func (a *RAGRetrievalActivities) FetchChunkContentActivity(ctx context.Context, input FetchChunkContentInput) (FetchChunkContentOutput, error) {
	var chunks []ChunkContent
	for _, chunkID := range input.ChunkIDs {
		docChunk, err := a.docRepo.GetChunkByID(ctx, chunkID)
		if err != nil {
			return FetchChunkContentOutput{}, fmt.Errorf("get chunk %s: %w", chunkID, err)
		}
		if docChunk == nil {
			continue
		}

		// Fetch the document for its title.
		doc, err := a.docRepo.GetDocument(ctx, docChunk.DocumentID)
		if err != nil {
			return FetchChunkContentOutput{}, fmt.Errorf("get document for chunk %s: %w", chunkID, err)
		}
		title := ""
		if doc != nil {
			title = doc.Title
		}

		chunks = append(chunks, ChunkContent{
			ChunkID:     docChunk.ID,
			DocumentID:  docChunk.DocumentID,
			Content:     docChunk.Content,
			ContentHash: docChunk.ContentHash,
			Title:       title,
			ChunkIndex:  docChunk.ChunkIndex,
			Score:       0,
		})
	}
	return FetchChunkContentOutput{Chunks: chunks}, nil
}

// ---------------------------------------------------------------------------
// Activity 3: PackContextActivity
// ---------------------------------------------------------------------------

// PackContextInput contains the query and chunks to format into a context string.
type PackContextInput struct {
	Query     string
	Chunks    []ChunkContent
	MaxTokens int // default 4000
}

// PackContextOutput contains the formatted context and metadata.
type PackContextOutput struct {
	Context       string   // formatted context for LLM
	Citations     []string // source references
	TokenEstimate int
	ChunkCount    int
}

// PackContextActivity formats chunks into a structured context string suitable
// for inclusion in an LLM prompt, along with citation references.
//
// Format:
//
//	Relevant context for query:
//	---
//	[1] Source: {title} (chunk {index})
//	{chunk_content}
//	---
//	[2] Source: {title} (chunk {index})
//	{chunk_content}
//	---
func (a *RAGRetrievalActivities) PackContextActivity(ctx context.Context, input PackContextInput) (PackContextOutput, error) {
	if len(input.Chunks) == 0 {
		return PackContextOutput{}, nil
	}

	var builder strings.Builder
	citations := make([]string, 0, len(input.Chunks))

	builder.WriteString("Relevant context for query:\n---\n")

	for i, chunk := range input.Chunks {
		if i > 0 {
			builder.WriteString("---\n")
		}
		title := chunk.Title
		if title == "" {
			title = "Untitled"
		}
		line := fmt.Sprintf("[%d] Source: %s (chunk %d)\n%s\n", i+1, title, chunk.ChunkIndex, chunk.Content)
		builder.WriteString(line)
		citations = append(citations, fmt.Sprintf("[%d] Source: %s (chunk %d)", i+1, title, chunk.ChunkIndex))
	}

	// Close the last section.
	builder.WriteString("---")

	context := builder.String()

	// Rough token estimate: ~4 characters per token for English text.
	tokenEstimate := len(context) / 4
	if tokenEstimate < 0 {
		tokenEstimate = 0
	}

	return PackContextOutput{
		Context:       context,
		Citations:     citations,
		TokenEstimate: tokenEstimate,
		ChunkCount:    len(input.Chunks),
	}, nil
}
