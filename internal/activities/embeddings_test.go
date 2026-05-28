package activities

import (
	"testing"

	"cribug/internal/embeddings"
)

func TestEmbedTextActivity_Compiles(t *testing.T) {
	svc := embeddings.NewService(embeddings.Config{})
	act := NewEmbeddingActivities(svc)
	if act == nil {
		t.Fatal("NewEmbeddingActivities returned nil")
	}
}

func TestEmbedBatchActivity_Compiles(t *testing.T) {
	svc := embeddings.NewService(embeddings.Config{})
	act := NewEmbeddingActivities(svc)
	if act == nil {
		t.Fatal("NewEmbeddingActivities returned nil")
	}
}

func TestEmbedTextActivity_Fields(t *testing.T) {
	input := EmbedTextInput{
		Text:  "hello world",
		Model: "text-embedding-3-small",
	}
	if input.Text != "hello world" {
		t.Errorf("Text = %q, want %q", input.Text, "hello world")
	}
	if input.Model != "text-embedding-3-small" {
		t.Errorf("Model = %q, want %q", input.Model, "text-embedding-3-small")
	}
}

func TestEmbedBatchActivity_Fields(t *testing.T) {
	input := EmbedBatchInput{
		Texts: []string{"first", "second"},
		Model: "text-embedding-3-small",
	}
	if len(input.Texts) != 2 {
		t.Errorf("len(Texts) = %d, want 2", len(input.Texts))
	}
	if input.Model != "text-embedding-3-small" {
		t.Errorf("Model = %q, want %q", input.Model, "text-embedding-3-small")
	}
}

func TestEmbeddingActivities_OutputTypesCompile(t *testing.T) {
	_ = EmbedTextOutput{Vector: []float64{0.1, 0.2}, Dim: 2, TokenCount: 10, Cached: false}
	_ = EmbedBatchOutput{Vectors: [][]float64{{0.1}}, Dim: 1, Cached: false}
}
