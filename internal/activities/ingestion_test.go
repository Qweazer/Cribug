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

// --- Compile and constructor tests ---

func TestNewIngestionActivities_Compiles(t *testing.T) {
	embSvc := embeddings.NewService(embeddings.Config{})
	vdbClient, err := vectordb.NewClient(vectordb.Config{Host: "localhost", Port: 6333})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	repo := db.NewDocumentRepository(nil)
	act := NewIngestionActivities(repo, embSvc, vdbClient)
	if act == nil {
		t.Fatal("NewIngestionActivities returned nil")
	}
}

func TestNewIngestionActivities_NoDB(t *testing.T) {
	// Verify that a DocumentRepository with nil *sql.DB does not cause a panic
	// at construction time.
	embSvc := embeddings.NewService(embeddings.Config{})
	vdbClient, err := vectordb.NewClient(vectordb.Config{Host: "localhost", Port: 6333})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	repo := db.NewDocumentRepository(nil)
	act := NewIngestionActivities(repo, embSvc, vdbClient)
	if act == nil {
		t.Fatal("NewIngestionActivities returned nil")
	}
}

// --- Input/Output type tests ---

func TestSaveDocumentTypes(t *testing.T) {
	input := SaveDocumentInput{
		TenantID:   "tenant-abc",
		SourceType: "web_page",
		SourceURI:  "https://example.com/doc",
		Title:      "Test Doc",
		Content:    "hello world",
	}
	if input.TenantID != "tenant-abc" {
		t.Errorf("TenantID = %q, want %q", input.TenantID, "tenant-abc")
	}
	if input.SourceType != "web_page" {
		t.Errorf("SourceType = %q, want %q", input.SourceType, "web_page")
	}
	if input.SourceURI != "https://example.com/doc" {
		t.Errorf("SourceURI = %q, want %q", input.SourceURI, "https://example.com/doc")
	}
	if input.Title != "Test Doc" {
		t.Errorf("Title = %q, want %q", input.Title, "Test Doc")
	}
	if input.Content != "hello world" {
		t.Errorf("Content = %q, want %q", input.Content, "hello world")
	}

	output := SaveDocumentOutput{
		DocumentID:  "doc-1",
		IsDuplicate: true,
	}
	if output.DocumentID != "doc-1" {
		t.Errorf("DocumentID = %q, want %q", output.DocumentID, "doc-1")
	}
	if !output.IsDuplicate {
		t.Error("IsDuplicate should be true")
	}
}

func TestChunkDocumentTypes(t *testing.T) {
	input := ChunkDocumentInput{DocumentID: "doc-1"}
	if input.DocumentID != "doc-1" {
		t.Errorf("DocumentID = %q, want %q", input.DocumentID, "doc-1")
	}

	output := ChunkDocumentOutput{DocumentID: "doc-1", ChunkCount: 5}
	if output.DocumentID != "doc-1" {
		t.Errorf("DocumentID = %q", output.DocumentID)
	}
	if output.ChunkCount != 5 {
		t.Errorf("ChunkCount = %d, want 5", output.ChunkCount)
	}
}

func TestEmbedAndUpsertTypes(t *testing.T) {
	input := EmbedAndUpsertInput{
		DocumentID: "doc-1",
		Collection: "my_collection",
		Model:      "text-embedding-3-small",
	}
	if input.DocumentID != "doc-1" {
		t.Errorf("DocumentID = %q", input.DocumentID)
	}
	if input.Collection != "my_collection" {
		t.Errorf("Collection = %q", input.Collection)
	}
	if input.Model != "text-embedding-3-small" {
		t.Errorf("Model = %q", input.Model)
	}

	output := EmbedAndUpsertOutput{DocumentID: "doc-1", IndexedCount: 3}
	if output.DocumentID != "doc-1" {
		t.Errorf("DocumentID = %q", output.DocumentID)
	}
	if output.IndexedCount != 3 {
		t.Errorf("IndexedCount = %d, want 3", output.IndexedCount)
	}
}

func TestUpdateIndexStatusTypes(t *testing.T) {
	input := UpdateIndexStatusInput{DocumentID: "doc-1"}
	if input.DocumentID != "doc-1" {
		t.Errorf("DocumentID = %q", input.DocumentID)
	}

	output := UpdateIndexStatusOutput{DocumentID: "doc-1", Status: "indexed"}
	if output.DocumentID != "doc-1" {
		t.Errorf("DocumentID = %q", output.DocumentID)
	}
	if output.Status != "indexed" {
		t.Errorf("Status = %q, want %q", output.Status, "indexed")
	}
}

// --- Activity smoke tests with nil docRepo ---

func TestSaveDocumentMetadataActivity_NilRepo(t *testing.T) {
	embSvc := embeddings.NewService(embeddings.Config{})
	vdbClient, err := vectordb.NewClient(vectordb.Config{Host: "localhost", Port: 6333})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	repo := db.NewDocumentRepository(nil)
	act := NewIngestionActivities(repo, embSvc, vdbClient)
	_, err = act.SaveDocumentMetadataActivity(context.Background(), SaveDocumentInput{
		TenantID:   "t1",
		SourceType: "web",
		SourceURI:  "https://example.com/doc",
		Title:      "Test",
		Content:    "hello",
	})
	if err == nil {
		t.Fatal("expected error from nil docRepo")
	}
	if !strings.Contains(err.Error(), "db not initialized") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestChunkDocumentActivity_NilRepo(t *testing.T) {
	embSvc := embeddings.NewService(embeddings.Config{})
	vdbClient, err := vectordb.NewClient(vectordb.Config{Host: "localhost", Port: 6333})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	repo := db.NewDocumentRepository(nil)
	act := NewIngestionActivities(repo, embSvc, vdbClient)
	_, err = act.ChunkDocumentActivity(context.Background(), ChunkDocumentInput{DocumentID: "doc-1"})
	if err == nil {
		t.Fatal("expected error from nil docRepo")
	}
	if !strings.Contains(err.Error(), "db not initialized") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestEmbedAndUpsertChunksActivity_NilRepo(t *testing.T) {
	// Create mock embedding server
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
		vectors := make([][]float64, len(req.Texts))
		for i := range req.Texts {
			vec := make([]float64, 1536)
			for j := range vec {
				vec[j] = float64(i*1000 + j)
			}
			vectors[i] = vec
		}
		resp := map[string]interface{}{
			"vectors":  vectors,
			"model":    req.Model,
			"provider": "test-provider",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer embedSrv.Close()

	// Create mock Qdrant server
	qdrantSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Accept any request (health, upsert, etc.)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"result": {"version": "mock"}}`))
	}))
	defer qdrantSrv.Close()

	// Parse Qdrant mock URL for vectordb client config
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

	repo := db.NewDocumentRepository(nil)
	act := NewIngestionActivities(repo, embSvc, vdbClient)
	_, err = act.EmbedAndUpsertChunksActivity(context.Background(), EmbedAndUpsertInput{
		DocumentID: "doc-1",
		Collection: "test_collection",
		Model:      "test-model",
	})
	if err == nil {
		t.Fatal("expected error from nil docRepo")
	}
	if !strings.Contains(err.Error(), "db not initialized") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestUpdateDocumentIndexStatusActivity_NilRepo(t *testing.T) {
	embSvc := embeddings.NewService(embeddings.Config{})
	vdbClient, err := vectordb.NewClient(vectordb.Config{Host: "localhost", Port: 6333})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	repo := db.NewDocumentRepository(nil)
	act := NewIngestionActivities(repo, embSvc, vdbClient)
	_, err = act.UpdateDocumentIndexStatusActivity(context.Background(), UpdateIndexStatusInput{DocumentID: "doc-1"})
	if err == nil {
		t.Fatal("expected error from nil docRepo")
	}
	if !strings.Contains(err.Error(), "db not initialized") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- Helper ---

// netSplitHostPort extracts host and port from a URL string like "http://127.0.0.1:1234".
func netSplitHostPort(rawURL string) (host, port string, err error) {
	// Strip scheme
	after := rawURL
	if idx := strings.Index(rawURL, "://"); idx >= 0 {
		after = rawURL[idx+3:]
	}
	// Split on ":"
	colonIdx := strings.LastIndex(after, ":")
	if colonIdx < 0 {
		return "", "", http.ErrNoLocation
	}
	host = after[:colonIdx]
	port = after[colonIdx+1:]
	return host, port, nil
}
