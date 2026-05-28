package db

import (
	"context"
	"testing"
)

func TestDocumentRepository_Exists(t *testing.T) {
	repo := NewDocumentRepository(nil)
	if repo == nil {
		t.Fatal("NewDocumentRepository returned nil")
	}
}

func TestDocumentRepository_CreateDocumentNilDB(t *testing.T) {
	repo := NewDocumentRepository(nil)
	err := repo.CreateDocument(context.Background(), &Document{
		TenantID:   "tenant-1",
		SourceType: "file",
		SourceURI:  "/tmp/test.md",
		Title:      "Test Document",
		Content:    "Hello, world!",
	})
	if err == nil {
		t.Error("expected error when db is nil")
	}
}

func TestDocumentRepository_GetDocumentNilDB(t *testing.T) {
	repo := NewDocumentRepository(nil)
	doc, err := repo.GetDocument(context.Background(), "test-id")
	if err == nil {
		t.Error("expected error when db is nil")
	}
	if doc != nil {
		t.Error("expected nil document when db is nil")
	}
}

func TestDocumentRepository_UpdateDocumentStatusNilDB(t *testing.T) {
	repo := NewDocumentRepository(nil)
	err := repo.UpdateDocumentStatus(context.Background(), "test-id", "completed", "", 5)
	if err == nil {
		t.Error("expected error when db is nil")
	}
}

func TestDocumentRepository_InsertChunksNilDB(t *testing.T) {
	repo := NewDocumentRepository(nil)
	err := repo.InsertChunks(context.Background(), []DocumentChunk{
		{DocumentID: "doc-1", ChunkIndex: 0, Content: "chunk 1"},
	})
	if err == nil {
		t.Error("expected error when db is nil")
	}
}

func TestDocumentRepository_ListChunksByDocumentNilDB(t *testing.T) {
	repo := NewDocumentRepository(nil)
	chunks, err := repo.ListChunksByDocument(context.Background(), "doc-1")
	if err == nil {
		t.Error("expected error when db is nil")
	}
	if chunks != nil {
		t.Error("expected nil chunks when db is nil")
	}
}

func TestDocumentRepository_UpdateChunkStatusNilDB(t *testing.T) {
	repo := NewDocumentRepository(nil)
	pointID := "qdrant-point-1"
	err := repo.UpdateChunkStatus(context.Background(), "chunk-1", "embedded", &pointID, "")
	if err == nil {
		t.Error("expected error when db is nil")
	}
}

func TestDocumentRepository_ListPendingChunksNilDB(t *testing.T) {
	repo := NewDocumentRepository(nil)
	chunks, err := repo.ListPendingChunks(context.Background(), "doc-1")
	if err == nil {
		t.Error("expected error when db is nil")
	}
	if chunks != nil {
		t.Error("expected nil chunks when db is nil")
	}
}

func TestDocumentRepository_FindByContentHashNilDB(t *testing.T) {
	repo := NewDocumentRepository(nil)
	doc, err := repo.FindByContentHash(context.Background(), "tenant-1", "hash123")
	if err == nil {
		t.Error("expected error when db is nil")
	}
	if doc != nil {
		t.Error("expected nil document when db is nil")
	}
}
