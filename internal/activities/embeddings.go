package activities

import (
	"context"
	"fmt"

	"cribug/internal/embeddings"
)

// EmbeddingActivities exposes embedding operations as Temporal activities.
type EmbeddingActivities struct {
	service *embeddings.Service
}

// NewEmbeddingActivities creates a new EmbeddingActivities.
func NewEmbeddingActivities(service *embeddings.Service) *EmbeddingActivities {
	return &EmbeddingActivities{service: service}
}

// EmbedTextInput is the input for EmbedTextActivity.
type EmbedTextInput struct {
	Text  string
	Model string
}

// EmbedTextOutput is the output for EmbedTextActivity.
type EmbedTextOutput struct {
	Vector     []float64
	Dim        int
	TokenCount int
	Cached     bool
}

// EmbedTextActivity embeds a single text string.
func (a *EmbeddingActivities) EmbedTextActivity(ctx context.Context, input EmbedTextInput) (EmbedTextOutput, error) {
	result, err := a.service.EmbedText(ctx, input.Text, input.Model)
	if err != nil {
		return EmbedTextOutput{}, fmt.Errorf("embed text: %w", err)
	}
	if len(result.Vectors) == 0 {
		return EmbedTextOutput{}, fmt.Errorf("no vectors returned")
	}
	return EmbedTextOutput{
		Vector: result.Vectors[0],
		Dim:    len(result.Vectors[0]),
		Cached: result.Cached,
	}, nil
}

// EmbedBatchInput is the input for EmbedBatchActivity.
type EmbedBatchInput struct {
	Texts []string
	Model string
}

// EmbedBatchOutput is the output for EmbedBatchActivity.
type EmbedBatchOutput struct {
	Vectors [][]float64
	Dim     int
	Cached  bool
}

// EmbedBatchActivity embeds multiple text strings in a single request.
func (a *EmbeddingActivities) EmbedBatchActivity(ctx context.Context, input EmbedBatchInput) (EmbedBatchOutput, error) {
	result, err := a.service.EmbedBatch(ctx, input.Texts, input.Model)
	if err != nil {
		return EmbedBatchOutput{}, fmt.Errorf("embed batch: %w", err)
	}
	if len(result.Vectors) == 0 {
		return EmbedBatchOutput{}, fmt.Errorf("no vectors returned")
	}
	return EmbedBatchOutput{
		Vectors: result.Vectors,
		Dim:     len(result.Vectors[0]),
		Cached:  result.Cached,
	}, nil
}
