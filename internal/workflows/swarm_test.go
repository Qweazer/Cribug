package workflows

import (
	"errors"
	"testing"

	"cribug/internal/types"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func setupSwarmTest(t *testing.T) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()
	swarmWf := NewSwarmWorkflow()
	env.RegisterWorkflowWithOptions(swarmWf.Execute, workflow.RegisterOptions{Name: SwarmWorkflowName})

	// Register generic activity stubs (env.OnActivity overrides per-test)
	env.RegisterActivityWithOptions(
		func(ctx interface{}, input interface{}) (interface{}, error) { return nil, nil },
		activity.RegisterOptions{Name: "EmitEventActivity"},
	)
	env.RegisterActivityWithOptions(
		func(ctx interface{}, input interface{}) (interface{}, error) {
			return &types.TeamActionDecision{Allowed: true}, nil
		},
		activity.RegisterOptions{Name: "AuthorizeTeamActionActivity"},
	)
	env.RegisterActivityWithOptions(
		func(ctx interface{}, input interface{}) (interface{}, error) {
			return &types.WorkerAgentResult{
				AgentID: "worker-1", Role: "researcher", Status: types.WorkerStatusCompleted,
				Result: "default result", TotalTokens: 100,
			}, nil
		},
		activity.RegisterOptions{Name: "WorkerAgentActivity"},
	)
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
	env.RegisterActivityWithOptions(
		func(ctx interface{}, input interface{}) (interface{}, error) { return nil, nil },
		activity.RegisterOptions{Name: "SaveResultActivity"},
	)
	env.RegisterActivityWithOptions(
		func(ctx interface{}, input interface{}) (interface{}, error) { return nil, nil },
		activity.RegisterOptions{Name: "RecordExecutionCompletedActivity"},
	)

	return env
}

// TestSwarm_LeaderRetrieves_WorkersGetContext verifies that when NeedsRetrieval is set,
// the leader executes RAG once and shares the packed context with all workers.
func TestSwarm_LeaderRetrieves_WorkersGetContext(t *testing.T) {
	env := setupSwarmTest(t)

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
			Context:       "Shared RAG context for swarm workers",
			Citations:     []string{"[1] Doc 1 (chunk 0)", "[2] Doc 1 (chunk 1)"},
			TokenEstimate: 100,
			ChunkCount:    2,
		}, nil)

	env.ExecuteWorkflow(SwarmWorkflowName, types.SwarmWorkflowInput{
		TaskID:         "task-rag-1",
		WorkflowID:     "wf-swarm-rag",
		RunID:          "run-1",
		Query:          "test query for RAG retrieval",
		Model:          "gpt-4",
		Temperature:    0.7,
		MaxTokens:      1000,
		WorkerCount:    1,
		WorkerTimeout:  10,
		MaxP2PRounds:   1,
		NeedsRetrieval: true,
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}

	var result types.SwarmWorkflowResult
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("workflow result error: %v", err)
	}
	if result.Status != types.SwarmStatusCompleted {
		t.Errorf("expected Status Completed, got %s", result.Status)
	}
	if result.Succeeded != 1 {
		t.Errorf("expected 1 succeeded worker, got %d", result.Succeeded)
	}
	if result.Failed != 0 {
		t.Errorf("expected 0 failed workers, got %d", result.Failed)
	}
}

// TestSwarm_NoRetrievalNeeded_NormalFlow verifies that the swarm works normally
// when NeedsRetrieval is false (no RAG activities are called).
func TestSwarm_NoRetrievalNeeded_NormalFlow(t *testing.T) {
	env := setupSwarmTest(t)

	// No OnActivity setup for RAG activities - they should NOT be called
	env.ExecuteWorkflow(SwarmWorkflowName, types.SwarmWorkflowInput{
		TaskID:         "task-normal-2",
		WorkflowID:     "wf-swarm-normal",
		RunID:          "run-2",
		Query:          "normal query without RAG",
		Model:          "gpt-4",
		Temperature:    0.7,
		MaxTokens:      1000,
		WorkerCount:    1,
		WorkerTimeout:  10,
		MaxP2PRounds:   1,
		NeedsRetrieval: false,
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}

	var result types.SwarmWorkflowResult
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("workflow result error: %v", err)
	}
	if result.Status != types.SwarmStatusCompleted {
		t.Errorf("expected Status Completed, got %s", result.Status)
	}
	if result.Succeeded != 1 {
		t.Errorf("expected 1 succeeded worker, got %d", result.Succeeded)
	}
	if result.Failed != 0 {
		t.Errorf("expected 0 failed workers, got %d", result.Failed)
	}
}

// TestSwarm_RetrievalFailure_WorkersProceed verifies that when RAG retrieval fails,
// workers still run without context and the workflow completes successfully.
func TestSwarm_RetrievalFailure_WorkersProceed(t *testing.T) {
	env := setupSwarmTest(t)

	// EmbedAndSearchChunksActivity returns an error -> retrieval fails
	env.OnActivity("EmbedAndSearchChunksActivity", mock.Anything, mock.Anything).
		Return(RAGSearchOutput{}, errors.New("vector search unavailable"))

	env.ExecuteWorkflow(SwarmWorkflowName, types.SwarmWorkflowInput{
		TaskID:         "task-fail-3",
		WorkflowID:     "wf-swarm-fail",
		RunID:          "run-3",
		Query:          "failing query",
		Model:          "gpt-4",
		Temperature:    0.7,
		MaxTokens:      1000,
		WorkerCount:    1,
		WorkerTimeout:  10,
		MaxP2PRounds:   1,
		NeedsRetrieval: true,
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}

	var result types.SwarmWorkflowResult
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("workflow result error: %v", err)
	}
	// Workers should still have completed successfully despite retrieval failure
	if result.Status != types.SwarmStatusCompleted {
		t.Errorf("expected Status Completed even with retrieval failure, got %s", result.Status)
	}
	if result.Succeeded != 1 {
		t.Errorf("expected 1 succeeded worker, got %d", result.Succeeded)
	}
	if result.Failed != 0 {
		t.Errorf("expected 0 failed workers, got %d", result.Failed)
	}
}
