package activities

import (
	"testing"

	"cribug/internal/vectordb"
)

func TestUpsertVectorsActivity_Compiles(t *testing.T) {
	t.Log("VectorDBActivities type compiles - integration tests need Qdrant")
	// Verify the type is constructable (nil client is fine for unit testing)
	act := NewVectorDBActivities(nil)
	if act == nil {
		t.Fatal("NewVectorDBActivities returned nil")
	}
}

func TestSearchVectorsActivity_Compiles(t *testing.T) {
	client, err := vectordb.NewClient(vectordb.Config{
		Host: "localhost",
		Port: 6333,
	})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	act := NewVectorDBActivities(client)
	if act == nil {
		t.Fatal("NewVectorDBActivities returned nil")
	}
}

func TestUpsertVectorsInput_Fields(t *testing.T) {
	input := UpsertVectorsInput{
		Collection: "test_collection",
		Vectors:    [][]float64{{0.1, 0.2}, {0.3, 0.4}},
		Payloads: []map[string]interface{}{
			{"key": "val1"},
			{"key": "val2"},
		},
		IDs: []string{"id-1", "id-2"},
	}
	if input.Collection != "test_collection" {
		t.Errorf("Collection = %q, want %q", input.Collection, "test_collection")
	}
	if len(input.Vectors) != 2 {
		t.Errorf("len(Vectors) = %d, want 2", len(input.Vectors))
	}
}

func TestSearchVectorsInput_Fields(t *testing.T) {
	input := SearchVectorsInput{
		Collection: "test_collection",
		Vector:     []float64{0.1, 0.2, 0.3},
		TopK:       5,
		Threshold:  0.75,
	}
	if input.Collection != "test_collection" {
		t.Errorf("Collection = %q, want %q", input.Collection, "test_collection")
	}
	if input.TopK != 5 {
		t.Errorf("TopK = %d, want 5", input.TopK)
	}
	if input.Threshold != 0.75 {
		t.Errorf("Threshold = %f, want 0.75", input.Threshold)
	}
}
