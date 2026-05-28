package workflows

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func setupRAGQueryTest(t *testing.T) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()
	env.RegisterWorkflowWithOptions(RAGQueryWorkflow, workflow.RegisterOptions{Name: RAGQueryWorkflowName})

	// Register all activities as stubs (env.OnActivity overrides per-test)
	env.RegisterActivityWithOptions(
		func(ctx interface{}, input interface{}) (interface{}, error) {
			return RAGSearchOutput{}, nil
		},
		activity.RegisterOptions{Name: "EmbedAndSearchChunksActivity"},
	)
	env.RegisterActivityWithOptions(
		func(ctx interface{}, input interface{}) (interface{}, error) {
			return RAGFetchOutput{}, nil
		},
		activity.RegisterOptions{Name: "FetchChunkContentActivity"},
	)
	env.RegisterActivityWithOptions(
		func(ctx interface{}, input interface{}) (interface{}, error) {
			return RAGPackOutput{}, nil
		},
		activity.RegisterOptions{Name: "PackContextActivity"},
	)

	return env
}

func TestRAGQueryWorkflow_Success(t *testing.T) {
	env := setupRAGQueryTest(t)

	mockHits := []RAGSearchHit{
		{ChunkID: "chunk-1", Score: 0.95},
		{ChunkID: "chunk-2", Score: 0.87},
	}

	mockChunks := []RAGChunkContent{
		{ChunkID: "chunk-1", DocumentID: "doc-1", Content: "Content one", Title: "Doc 1", ChunkIndex: 0, Score: 0.95},
		{ChunkID: "chunk-2", DocumentID: "doc-1", Content: "Content two", Title: "Doc 1", ChunkIndex: 1, Score: 0.87},
	}

	env.OnActivity("EmbedAndSearchChunksActivity", mock.Anything, mock.Anything).
		Return(RAGSearchOutput{Hits: mockHits}, nil)
	env.OnActivity("FetchChunkContentActivity", mock.Anything, mock.Anything).
		Return(RAGFetchOutput{Chunks: mockChunks}, nil)
	env.OnActivity("PackContextActivity", mock.Anything, mock.Anything).
		Return(RAGPackOutput{
			Context:       "Packed context",
			Citations:     []string{"doc-1"},
			TokenEstimate: 150,
			ChunkCount:    2,
		}, nil)

	env.ExecuteWorkflow(RAGQueryWorkflowName, RAGQueryWorkflowInput{
		QueryText:  "test query",
		Collection: "test-collection",
		Model:      "text-embedding-ada-002",
		TopK:       5,
		Threshold:  0.7,
		MaxTokens:  1000,
		AgentID:    "agent-1",
		WorkflowID: "wf-1",
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}

	var result RAGQueryWorkflowOutput
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("workflow result error: %v", err)
	}
	if result.Context != "Packed context" {
		t.Errorf("expected Context 'Packed context', got '%s'", result.Context)
	}
	if len(result.Citations) != 1 || result.Citations[0] != "doc-1" {
		t.Errorf("expected Citations ['doc-1'], got %v", result.Citations)
	}
	if result.HitCount != 2 {
		t.Errorf("expected HitCount 2, got %d", result.HitCount)
	}
	if result.TokenEstimate != 150 {
		t.Errorf("expected TokenEstimate 150, got %d", result.TokenEstimate)
	}
}

func TestRAGQueryWorkflow_EmptyResults(t *testing.T) {
	env := setupRAGQueryTest(t)

	// Search returns zero hits; workflow should return empty output early
	env.OnActivity("EmbedAndSearchChunksActivity", mock.Anything, mock.Anything).
		Return(RAGSearchOutput{Hits: nil}, nil)
	// No other activities should be called

	env.ExecuteWorkflow(RAGQueryWorkflowName, RAGQueryWorkflowInput{
		QueryText:  "empty query",
		Collection: "test-collection",
		Model:      "text-embedding-ada-002",
		TopK:       5,
		Threshold:  0.7,
		MaxTokens:  1000,
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}

	var result RAGQueryWorkflowOutput
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("workflow result error: %v", err)
	}
	if result.HitCount != 0 {
		t.Errorf("expected HitCount 0, got %d", result.HitCount)
	}
	if result.Context != "" {
		t.Errorf("expected empty Context, got '%s'", result.Context)
	}
	if result.Citations != nil {
		t.Errorf("expected nil Citations, got %v", result.Citations)
	}
	if result.TokenEstimate != 0 {
		t.Errorf("expected TokenEstimate 0, got %d", result.TokenEstimate)
	}
}

func TestRAGQueryWorkflow_SearchFailure(t *testing.T) {
	env := setupRAGQueryTest(t)

	env.OnActivity("EmbedAndSearchChunksActivity", mock.Anything, mock.Anything).
		Return(RAGSearchOutput{}, errors.New("search failed"))

	env.ExecuteWorkflow(RAGQueryWorkflowName, RAGQueryWorkflowInput{
		QueryText:  "failing query",
		Collection: "test-collection",
		Model:      "text-embedding-ada-002",
		TopK:       5,
		Threshold:  0.7,
		MaxTokens:  1000,
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}

	var result RAGQueryWorkflowOutput
	err := env.GetWorkflowResult(&result)
	if err == nil {
		t.Fatal("expected workflow error on search failure")
	}
	if !strings.Contains(err.Error(), "search failed") {
		t.Errorf("expected error containing 'search failed', got '%v'", err)
	}
}
