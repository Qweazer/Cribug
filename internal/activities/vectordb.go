package activities

import (
	"context"
	"fmt"

	"cribug/internal/vectordb"
	"github.com/google/uuid"
)

// VectorDBActivities exposes vector database operations as Temporal activities.
type VectorDBActivities struct {
	client *vectordb.Client
}

// NewVectorDBActivities creates a new VectorDBActivities.
func NewVectorDBActivities(client *vectordb.Client) *VectorDBActivities {
	return &VectorDBActivities{client: client}
}

// UpsertVectorsInput is the input for UpsertVectorsActivity.
type UpsertVectorsInput struct {
	Collection string
	Vectors    [][]float64
	Payloads   []map[string]interface{}
	IDs        []string
}

// UpsertVectorsOutput is the output for UpsertVectorsActivity.
type UpsertVectorsOutput struct {
	Count int
}

// UpsertVectorsActivity upserts a batch of vectors into the given collection.
func (a *VectorDBActivities) UpsertVectorsActivity(ctx context.Context, input UpsertVectorsInput) (UpsertVectorsOutput, error) {
	points := make([]vectordb.VectorPoint, len(input.Vectors))
	for i := range input.Vectors {
		id := ""
		if i < len(input.IDs) && input.IDs[i] != "" {
			id = input.IDs[i]
		}
		if id == "" {
			id = uuid.New().String()
		}
		var payload map[string]interface{}
		if i < len(input.Payloads) {
			payload = input.Payloads[i]
		}
		points[i] = vectordb.VectorPoint{ID: id, Vector: input.Vectors[i], Payload: payload}
	}
	if err := a.client.Upsert(ctx, input.Collection, points); err != nil {
		return UpsertVectorsOutput{}, fmt.Errorf("upsert vectors: %w", err)
	}
	return UpsertVectorsOutput{Count: len(points)}, nil
}

// SearchVectorsInput is the input for SearchVectorsActivity.
type SearchVectorsInput struct {
	Collection string
	Vector     []float64
	TopK       int
	Threshold  float64
}

// SearchVectorsOutput is the output for SearchVectorsActivity.
type SearchVectorsOutput struct {
	Results []vectordb.SearchResult
}

// SearchVectorsActivity searches for similar vectors in the given collection.
func (a *VectorDBActivities) SearchVectorsActivity(ctx context.Context, input SearchVectorsInput) (SearchVectorsOutput, error) {
	results, err := a.client.Search(ctx, input.Collection, input.Vector, vectordb.SearchOptions{
		TopK:      input.TopK,
		Threshold: input.Threshold,
	})
	if err != nil {
		return SearchVectorsOutput{}, fmt.Errorf("search vectors: %w", err)
	}
	return SearchVectorsOutput{Results: results}, nil
}
