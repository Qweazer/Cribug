package activities

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"cribug/internal/db"
	"cribug/internal/embeddings"
	"cribug/internal/vectordb"
)

// ---------------------------------------------------------------------------
// Constructor and compile tests
// ---------------------------------------------------------------------------

func TestNewRAGRetrievalActivities_Compiles(t *testing.T) {
	embSvc := embeddings.NewService(embeddings.Config{})
	vdbClient, err := vectordb.NewClient(vectordb.Config{Host: "localhost", Port: 6333})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	repo := db.NewDocumentRepository(nil)
	act := NewRAGRetrievalActivities(embSvc, vdbClient, repo)
	if act == nil {
		t.Fatal("NewRAGRetrievalActivities returned nil")
	}
}

func TestRAGRetrievalActivities_Types(t *testing.T) {
	// EmbedAndSearchChunksInput
	input := EmbedAndSearchChunksInput{
		QueryText:  "test query",
		Collection: "my_collection",
		Model:      "text-embedding-3-small",
		TopK:       5,
		Threshold:  0.75,
	}
	if input.QueryText != "test query" {
		t.Errorf("QueryText = %q", input.QueryText)
	}
	if input.Collection != "my_collection" {
		t.Errorf("Collection = %q", input.Collection)
	}
	if input.Model != "text-embedding-3-small" {
		t.Errorf("Model = %q", input.Model)
	}
	if input.TopK != 5 {
		t.Errorf("TopK = %d", input.TopK)
	}
	if input.Threshold != 0.75 {
		t.Errorf("Threshold = %f", input.Threshold)
	}

	// SearchHit
	hit := SearchHit{
		ChunkID:       "chunk-1",
		QdrantPointID: "point-1",
		Score:         0.95,
		Payload: map[string]interface{}{
			"document_id": "doc-1",
			"title":       "Test Doc",
		},
	}
	if hit.ChunkID != "chunk-1" {
		t.Errorf("ChunkID = %q", hit.ChunkID)
	}
	if hit.QdrantPointID != "point-1" {
		t.Errorf("QdrantPointID = %q", hit.QdrantPointID)
	}
	if hit.Score != 0.95 {
		t.Errorf("Score = %f", hit.Score)
	}
	if hit.Payload["document_id"] != "doc-1" {
		t.Errorf("Payload[document_id] = %q", hit.Payload["document_id"])
	}

	// EmbedAndSearchChunksOutput
	output := EmbedAndSearchChunksOutput{
		Hits: []SearchHit{hit},
	}
	if len(output.Hits) != 1 {
		t.Errorf("Hits length = %d", len(output.Hits))
	}

	// FetchChunkContentInput
	fetchInput := FetchChunkContentInput{
		ChunkIDs: []string{"chunk-1", "chunk-2"},
	}
	if len(fetchInput.ChunkIDs) != 2 {
		t.Errorf("ChunkIDs length = %d", len(fetchInput.ChunkIDs))
	}

	// ChunkContent
	chunkContent := ChunkContent{
		ChunkID:     "chunk-1",
		DocumentID:  "doc-1",
		Content:     "chunk content text",
		ContentHash: "abc123",
		Title:       "Test Document",
		ChunkIndex:  0,
		Score:       0.95,
	}
	if chunkContent.ChunkID != "chunk-1" {
		t.Errorf("ChunkID = %q", chunkContent.ChunkID)
	}
	if chunkContent.DocumentID != "doc-1" {
		t.Errorf("DocumentID = %q", chunkContent.DocumentID)
	}
	if chunkContent.Content != "chunk content text" {
		t.Errorf("Content = %q", chunkContent.Content)
	}
	if chunkContent.ContentHash != "abc123" {
		t.Errorf("ContentHash = %q", chunkContent.ContentHash)
	}
	if chunkContent.Title != "Test Document" {
		t.Errorf("Title = %q", chunkContent.Title)
	}
	if chunkContent.ChunkIndex != 0 {
		t.Errorf("ChunkIndex = %d", chunkContent.ChunkIndex)
	}
	if chunkContent.Score != 0.95 {
		t.Errorf("Score = %f", chunkContent.Score)
	}

	// FetchChunkContentOutput
	fetchOutput := FetchChunkContentOutput{
		Chunks: []ChunkContent{chunkContent},
	}
	if len(fetchOutput.Chunks) != 1 {
		t.Errorf("Chunks length = %d", len(fetchOutput.Chunks))
	}

	// PackContextInput
	packInput := PackContextInput{
		Query:     "test query",
		Chunks:    []ChunkContent{chunkContent},
		MaxTokens: 4000,
	}
	if packInput.MaxTokens != 4000 {
		t.Errorf("MaxTokens = %d", packInput.MaxTokens)
	}

	// PackContextOutput
	packOutput := PackContextOutput{
		Context:       "context string",
		Citations:     []string{"[1] Source: Doc (chunk 0)"},
		TokenEstimate: 50,
		ChunkCount:    1,
	}
	if packOutput.Context != "context string" {
		t.Errorf("Context = %q", packOutput.Context)
	}
	if len(packOutput.Citations) != 1 {
		t.Errorf("Citations length = %d", len(packOutput.Citations))
	}
	if packOutput.TokenEstimate != 50 {
		t.Errorf("TokenEstimate = %d", packOutput.TokenEstimate)
	}
	if packOutput.ChunkCount != 1 {
		t.Errorf("ChunkCount = %d", packOutput.ChunkCount)
	}
}

// ---------------------------------------------------------------------------
// EmbedAndSearchChunksActivity with mock servers
// ---------------------------------------------------------------------------

func TestEmbedAndSearchChunksActivity_MockServers(t *testing.T) {
	// Mock embedding server
	embedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embed" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		var req struct {
			Texts []string `json:"texts"`
			Model string   `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		vectors := make([][]float64, 1)
		vec := make([]float64, 1536)
		for j := range vec {
			vec[j] = float64(j)
		}
		vectors[0] = vec
		resp := map[string]interface{}{
			"vectors":  vectors,
			"model":    req.Model,
			"provider": "test-provider",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer embedSrv.Close()

	// Mock Qdrant search server
	qdrantSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		result := []map[string]interface{}{
			{
				"id":    "qdrant-point-1",
				"score": 0.95,
				"payload": map[string]interface{}{
					"chunk_id":    "chunk-1",
					"document_id": "doc-1",
					"title":       "Test Document 1",
					"chunk_index": float64(0),
				},
			},
			{
				"id":    "qdrant-point-2",
				"score": 0.88,
				"payload": map[string]interface{}{
					"chunk_id":    "chunk-2",
					"document_id": "doc-1",
					"title":       "Test Document 1",
					"chunk_index": float64(1),
				},
			},
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"result": result,
		})
	}))
	defer qdrantSrv.Close()

	qHost, qPortStr, err := netSplitHostPort(qdrantSrv.URL)
	if err != nil {
		t.Fatalf("parse qdrant URL: %v", err)
	}
	qPort, err := strconv.Atoi(qPortStr)
	if err != nil {
		t.Fatalf("parse qdrant port: %v", err)
	}

	embSvc := embeddings.NewService(embeddings.Config{
		BaseURL:     embedSrv.URL,
		ExpectedDim: 1536,
	})
	vdbClient, err := vectordb.NewClient(vectordb.Config{
		Host:        qHost,
		Port:        qPort,
		ExpectedDim: 1536,
	})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	// docRepo is nil — EmbedAndSearchChunksActivity does not use it.
	act := NewRAGRetrievalActivities(embSvc, vdbClient, nil)

	output, err := act.EmbedAndSearchChunksActivity(context.Background(), EmbedAndSearchChunksInput{
		QueryText:  "test query",
		Collection: "test_collection",
		Model:      "test-model",
		TopK:       5,
		Threshold:  0.7,
	})
	if err != nil {
		t.Fatalf("EmbedAndSearchChunksActivity failed: %v", err)
	}
	if len(output.Hits) != 2 {
		t.Fatalf("expected 2 hits, got %d", len(output.Hits))
	}

	// Verify first hit
	if output.Hits[0].ChunkID != "chunk-1" {
		t.Errorf("Hit[0] ChunkID = %q, want %q", output.Hits[0].ChunkID, "chunk-1")
	}
	if output.Hits[0].QdrantPointID != "qdrant-point-1" {
		t.Errorf("Hit[0] QdrantPointID = %q, want %q", output.Hits[0].QdrantPointID, "qdrant-point-1")
	}
	if output.Hits[0].Score != 0.95 {
		t.Errorf("Hit[0] Score = %f, want 0.95", output.Hits[0].Score)
	}
	if output.Hits[0].Payload == nil {
		t.Fatal("Hit[0] Payload is nil")
	}
	if title, ok := output.Hits[0].Payload["title"].(string); !ok || title != "Test Document 1" {
		t.Errorf("Hit[0] Payload[title] = %q, want %q", output.Hits[0].Payload["title"], "Test Document 1")
	}

	// Verify second hit
	if output.Hits[1].ChunkID != "chunk-2" {
		t.Errorf("Hit[1] ChunkID = %q, want %q", output.Hits[1].ChunkID, "chunk-2")
	}
	if output.Hits[1].Score != 0.88 {
		t.Errorf("Hit[1] Score = %f, want 0.88", output.Hits[1].Score)
	}
}

// ---------------------------------------------------------------------------
// FetchChunkContentActivity with nil docRepo (will error on DB call)
// ---------------------------------------------------------------------------

func TestFetchChunkContentActivity_NilRepo(t *testing.T) {
	repo := db.NewDocumentRepository(nil)
	act := NewRAGRetrievalActivities(nil, nil, repo)
	_, err := act.FetchChunkContentActivity(context.Background(), FetchChunkContentInput{
		ChunkIDs: []string{"chunk-1", "chunk-2"},
	})
	if err == nil {
		t.Fatal("expected error from nil docRepo")
	}
	if !strings.Contains(err.Error(), "db not initialized") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// PackContextActivity tests
// ---------------------------------------------------------------------------

func TestPackContextActivity(t *testing.T) {
	act := NewRAGRetrievalActivities(nil, nil, nil)
	chunks := []ChunkContent{
		{
			ChunkID:    "chunk-1",
			DocumentID: "doc-1",
			Content:    "This is the first chunk content.",
			Title:      "Test Document",
			ChunkIndex: 0,
		},
		{
			ChunkID:    "chunk-2",
			DocumentID: "doc-1",
			Content:    "This is the second chunk content.",
			Title:      "Test Document",
			ChunkIndex: 1,
		},
	}

	output, err := act.PackContextActivity(context.Background(), PackContextInput{
		Query:     "test query",
		Chunks:    chunks,
		MaxTokens: 4000,
	})
	if err != nil {
		t.Fatalf("PackContextActivity failed: %v", err)
	}

	// Verify header
	expectedPrefix := "Relevant context for query:\n---\n"
	if !strings.HasPrefix(output.Context, expectedPrefix) {
		t.Errorf("context does not start with expected prefix\nwant: %q\ngot:  %q", expectedPrefix, output.Context[:len(expectedPrefix)])
	}

	// Verify first chunk reference
	if !strings.Contains(output.Context, "[1] Source: Test Document (chunk 0)") {
		t.Errorf("context missing first chunk reference\ngot: %s", output.Context)
	}

	// Verify first chunk content
	if !strings.Contains(output.Context, "This is the first chunk content.") {
		t.Errorf("context missing first chunk content")
	}

	// Verify second chunk reference
	if !strings.Contains(output.Context, "[2] Source: Test Document (chunk 1)") {
		t.Errorf("context missing second chunk reference")
	}

	// Verify second chunk content
	if !strings.Contains(output.Context, "This is the second chunk content.") {
		t.Errorf("context missing second chunk content")
	}

	// Verify sections are separated by ---
	if !strings.Contains(output.Context, "---\n[2]") {
		t.Errorf("chunks not separated by ---")
	}

	// Verify trailing ---
	if !strings.HasSuffix(output.Context, "---") {
		t.Errorf("context should end with ---")
	}

	// Verify citations
	if len(output.Citations) != 2 {
		t.Errorf("expected 2 citations, got %d", len(output.Citations))
	}
	if len(output.Citations) >= 1 && output.Citations[0] != "[1] Source: Test Document (chunk 0)" {
		t.Errorf("citation[0] = %q", output.Citations[0])
	}

	// Verify ChunkCount
	if output.ChunkCount != 2 {
		t.Errorf("expected ChunkCount=2, got %d", output.ChunkCount)
	}

	// Verify TokenEstimate is positive
	if output.TokenEstimate <= 0 {
		t.Errorf("expected positive TokenEstimate, got %d", output.TokenEstimate)
	}
}

func TestPackContextActivity_EmptyChunks(t *testing.T) {
	act := NewRAGRetrievalActivities(nil, nil, nil)
	output, err := act.PackContextActivity(context.Background(), PackContextInput{
		Query:     "test query",
		Chunks:    []ChunkContent{},
		MaxTokens: 4000,
	})
	if err != nil {
		t.Fatalf("PackContextActivity failed: %v", err)
	}

	if output.Context != "" {
		t.Errorf("expected empty context, got: %s", output.Context)
	}
	if len(output.Citations) != 0 {
		t.Errorf("expected 0 citations, got %d", len(output.Citations))
	}
	if output.ChunkCount != 0 {
		t.Errorf("expected ChunkCount=0, got %d", output.ChunkCount)
	}
	if output.TokenEstimate != 0 {
		t.Errorf("expected TokenEstimate=0, got %d", output.TokenEstimate)
	}
}
