package patterns

import (
	"errors"
	"testing"

	"cribug/internal/activities"
	hookspkg "cribug/internal/hooks"
	"cribug/internal/types"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
)

// setupReActTest creates a test workflow environment with all activities registered as stubs.
// Each test overrides specific activities via env.OnActivity.
func setupReActTest(t *testing.T) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	// Register AgentActivity stub (overridden per test)
	env.RegisterActivityWithOptions(
		func(ctx interface{}, input interface{}) (interface{}, error) {
			panic("unexpected AgentActivity call - mock not configured")
		},
		activity.RegisterOptions{Name: "AgentActivity"},
	)
	// RecordUsageActivity — fire-and-forget, always succeeds
	env.RegisterActivityWithOptions(
		func(ctx interface{}, input interface{}) (interface{}, error) {
			return nil, nil
		},
		activity.RegisterOptions{Name: "RecordUsageActivity"},
	)
	// SaveReActStepAuditActivity — fire-and-forget, always succeeds
	env.RegisterActivityWithOptions(
		func(ctx interface{}, input interface{}) (interface{}, error) {
			return nil, nil
		},
		activity.RegisterOptions{Name: "SaveReActStepAuditActivity"},
	)
	// EmitHookEventActivity — non-blocking, always succeeds
	env.RegisterActivityWithOptions(
		func(ctx interface{}, input interface{}) (interface{}, error) {
			return &hookspkg.EmitHookEventActivityResult{}, nil
		},
		activity.RegisterOptions{Name: "EmitHookEventActivity"},
	)
	// RAG activities — overridden per test when RAG is triggered
	env.RegisterActivityWithOptions(
		func(ctx interface{}, input interface{}) (interface{}, error) {
			panic("unexpected EmbedAndSearchChunksActivity call - mock not configured")
		},
		activity.RegisterOptions{Name: "EmbedAndSearchChunksActivity"},
	)
	env.RegisterActivityWithOptions(
		func(ctx interface{}, input interface{}) (interface{}, error) {
			panic("unexpected FetchChunkContentActivity call - mock not configured")
		},
		activity.RegisterOptions{Name: "FetchChunkContentActivity"},
	)
	env.RegisterActivityWithOptions(
		func(ctx interface{}, input interface{}) (interface{}, error) {
			panic("unexpected PackContextActivity call - mock not configured")
		},
		activity.RegisterOptions{Name: "PackContextActivity"},
	)

	return env
}

// sharedAgentActivityOutput returns a minimal AgentActivityOutput for mocking.
func sharedAgentActivityOutput(answer string) *activities.AgentActivityOutput {
	return &activities.AgentActivityOutput{
		Answer:       answer,
		Usage:        types.Usage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30},
		Model:        "gpt-4o-mini",
		Provider:     "openai",
		LatencyMS:    100,
		FinishReason: "stop",
	}
}

// setDefaultAgentMock configures the minimum AgentActivity mocks needed for a single
// ReAct iteration (Reason, Action, Synthesis) with the given thought and action answers.
func setDefaultAgentMock(env *testsuite.TestWorkflowEnvironment, thoughtAnswer, actionAnswer string) {
	// Reason step
	env.OnActivity("AgentActivity", mock.Anything, mock.Anything).
		Return(sharedAgentActivityOutput(thoughtAnswer), nil).Once()
	// Action step
	env.OnActivity("AgentActivity", mock.Anything, mock.Anything).
		Return(sharedAgentActivityOutput(actionAnswer), nil).Once()
	// Synthesis step
	env.OnActivity("AgentActivity", mock.Anything, mock.Anything).
		Return(sharedAgentActivityOutput("Final synthesized answer."), nil).Once()
}

// ─── Tests ───────────────────────────────────────────────────────────────────────

func configOneIteration() types.ReactConfig {
	return types.ReactConfig{
		MaxIterations:     1,
		MinIterations:     1,
		ObservationWindow: 3,
		MaxObservations:   10,
		MaxThoughts:       10,
		MaxActions:        10,
	}
}

func TestReAct_NoRetrievalTrigger_NoRAGCall(t *testing.T) {
	env := setupReActTest(t)

	// Thought does NOT contain any trigger keyword and does NOT contain FINAL
	// so the loop runs 1 iteration then proceeds to synthesis.
	setDefaultAgentMock(env,
		"I will analyze the problem step by step and consider the options.",
		"I have completed the analysis. FINAL: Answer is 42.",
	)

	env.ExecuteWorkflow(ReactLoop,
		"what is the answer?",
		"",
		"session-1",
		"task-1",
		"wf-1",
		"run-1",
		"gpt-4o-mini",
		0.7,
		1024,
		configOneIteration(),
	)

	require.True(t, env.IsWorkflowCompleted())
	var result types.ReactLoopResult
	err := env.GetWorkflowResult(&result)
	require.NoError(t, err)
	require.Equal(t, 1, result.Iterations)
	require.NotEmpty(t, result.FinalResult)

	// Verify RAG activities were NEVER called
	env.AssertNotCalled(t, "EmbedAndSearchChunksActivity", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "FetchChunkContentActivity", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "PackContextActivity", mock.Anything, mock.Anything)
}

func TestReAct_RetrievalTrigger_CallsRAG(t *testing.T) {
	env := setupReActTest(t)

	// Thought contains the "search:" trigger keyword but not "FINAL:"
	setDefaultAgentMock(env,
		"I need to search: for relevant documents about this topic before proceeding.",
		"Based on the search results, I can answer. FINAL: The answer is found.",
	)

	// Mock RAG pipeline: search returns hits
	mockHits := []activities.SearchHit{
		{ChunkID: "chunk-1", QdrantPointID: "pt-1", Score: 0.95, Payload: map[string]interface{}{"chunk_id": "chunk-1"}},
		{ChunkID: "chunk-2", QdrantPointID: "pt-2", Score: 0.87, Payload: map[string]interface{}{"chunk_id": "chunk-2"}},
	}
	env.OnActivity("EmbedAndSearchChunksActivity", mock.Anything, mock.Anything).
		Return(activities.EmbedAndSearchChunksOutput{Hits: mockHits}, nil).Once()

	// FetchChunkContent returns chunk contents
	mockChunks := []activities.ChunkContent{
		{ChunkID: "chunk-1", DocumentID: "doc-1", Content: "Relevant content one.", Title: "Doc 1", ChunkIndex: 0, Score: 0.95},
		{ChunkID: "chunk-2", DocumentID: "doc-1", Content: "Relevant content two.", Title: "Doc 1", ChunkIndex: 1, Score: 0.87},
	}
	env.OnActivity("FetchChunkContentActivity", mock.Anything, mock.Anything).
		Return(activities.FetchChunkContentOutput{Chunks: mockChunks}, nil).Once()

	// PackContext returns formatted context
	env.OnActivity("PackContextActivity", mock.Anything, mock.Anything).
		Return(activities.PackContextOutput{
			Context:       "Relevant context for query:\n---\n[1] Source: Doc 1 (chunk 0)\nRelevant content one.\n---\n[2] Source: Doc 1 (chunk 1)\nRelevant content two.\n---",
			Citations:     []string{"[1] Source: Doc 1 (chunk 0)", "[2] Source: Doc 1 (chunk 1)"},
			TokenEstimate: 80,
			ChunkCount:    2,
		}, nil).Once()

	env.ExecuteWorkflow(ReactLoop,
		"find me information about X",
		"",
		"session-1",
		"task-2",
		"wf-2",
		"run-2",
		"gpt-4o-mini",
		0.7,
		1024,
		configOneIteration(),
	)

	require.True(t, env.IsWorkflowCompleted())
	var result types.ReactLoopResult
	err := env.GetWorkflowResult(&result)
	require.NoError(t, err)

	// Verify RAG activities WERE called
	env.AssertExpectations(t)

	// Verify step contains retrieval context
	require.Len(t, result.Steps, 1)
	step := result.Steps[0]
	require.NotNil(t, step.RetrievalContext)
	require.Equal(t, 2, step.RetrievalContext.HitCount)
	require.Greater(t, step.RetrievalContext.ContextSize, 0)
	require.Equal(t, "task_embeddings", step.RetrievalContext.Collection)
	require.Len(t, step.RetrievalContext.Citations, 2)
}

func TestReAct_RetrievalEmptyResults_Continues(t *testing.T) {
	env := setupReActTest(t)

	// Thought contains "rag:" trigger keyword but not "FINAL:"
	setDefaultAgentMock(env,
		"Let me rag: look up relevant documents to inform my answer.",
		"I can answer without search results. FINAL: Answer is here.",
	)

	// EmbedAndSearch returns empty hits
	env.OnActivity("EmbedAndSearchChunksActivity", mock.Anything, mock.Anything).
		Return(activities.EmbedAndSearchChunksOutput{Hits: nil}, nil).Once()

	// FetchChunkContentActivity and PackContextActivity should NOT be called
	// when there are no search hits.

	env.ExecuteWorkflow(ReactLoop,
		"answer my question",
		"",
		"session-1",
		"task-3",
		"wf-3",
		"run-3",
		"gpt-4o-mini",
		0.7,
		1024,
		configOneIteration(),
	)

	require.True(t, env.IsWorkflowCompleted())
	var result types.ReactLoopResult
	err := env.GetWorkflowResult(&result)
	require.NoError(t, err)
	require.Equal(t, 1, result.Iterations)
	require.NotEmpty(t, result.FinalResult)

	// EmbedAndSearch WAS called (trigger matched)
	env.AssertCalled(t, "EmbedAndSearchChunksActivity", mock.Anything, mock.Anything)
	// But fetch and pack should NOT be called when empty results
	env.AssertNotCalled(t, "FetchChunkContentActivity", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "PackContextActivity", mock.Anything, mock.Anything)

	// No retrieval context in step
	if len(result.Steps) > 0 {
		require.Nil(t, result.Steps[0].RetrievalContext)
	}
}

// ─── Additional error-path tests ────────────────────────────────────────────────

func TestReAct_TriggerKeyword_AnyFormat(t *testing.T) {
	tests := []struct {
		name    string
		thought string
	}{
		{"search prefix", "I should search: for this topic"},
		{"retrieve prefix", "I need to retrieve: the relevant data"},
		{"lookup prefix", "Let me lookup: the answer"},
		{"rag prefix", "I will rag: to find more context"},
		{"uppercase SEARCH", "SEARCH: for things"},
		{"mixed case Retrieve", "I must Retrieve: the docs"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := setupReActTest(t)

			setDefaultAgentMock(env,
				tt.thought,
				"FINAL: Done.",
			)

			// RAG returns empty so tests complete without full pipeline
			env.OnActivity("EmbedAndSearchChunksActivity", mock.Anything, mock.Anything).
				Return(activities.EmbedAndSearchChunksOutput{Hits: nil}, nil).Once()

			env.ExecuteWorkflow(ReactLoop,
				"test query",
				"",
				"session-1",
				"task-trigger",
				"wf-trigger",
				"run-trigger",
				"gpt-4o-mini",
				0.7,
				1024,
				configOneIteration(),
			)

			require.True(t, env.IsWorkflowCompleted())
			// EmbedAndSearchChunksActivity should have been called because
			// the thought containing the trigger keyword matched.
			env.AssertCalled(t, "EmbedAndSearchChunksActivity", mock.Anything, mock.Anything)
		})
	}
}

func TestReAct_NoTriggerKeyword_NoRAG(t *testing.T) {
	thoughts := []string{
		"I will think step by step.",
		"Let me consider the options.",
		"I need to look up the answer (but without colon).",
		"search is not a trigger without colon",
		"retrieve without colon is not a trigger",
	}

	for _, thought := range thoughts {
		t.Run(thought, func(t *testing.T) {
			env := setupReActTest(t)

			// No FINAL so the loop runs
			setDefaultAgentMock(env,
				thought,
				"I have the answer. FINAL: Answer found.",
			)

			env.ExecuteWorkflow(ReactLoop,
				"test query",
				"",
				"session-1",
				"task-notrigger",
				"wf-notrigger",
				"run-notrigger",
				"gpt-4o-mini",
				0.7,
				1024,
				configOneIteration(),
			)

			require.True(t, env.IsWorkflowCompleted())
			env.AssertNotCalled(t, "EmbedAndSearchChunksActivity", mock.Anything, mock.Anything)
		})
	}
}

func TestReAct_RetrievalSearchFailure_Continues(t *testing.T) {
	env := setupReActTest(t)

	// Thought triggers RAG
	setDefaultAgentMock(env,
		"I should search: for the answer but the service might be down.",
		"FINALS: The answer is clear without search.",
	)

	// EmbedAndSearch returns an error (simulate service failure)
	// Note: retry is configured with MaxAttempts=3, so the mock must
	// expect up to 3 calls; we use .Times(3) to cover retries.
	env.OnActivity("EmbedAndSearchChunksActivity", mock.Anything, mock.Anything).
		Return(activities.EmbedAndSearchChunksOutput{}, errors.New("service unavailable")).Times(3)

	env.ExecuteWorkflow(ReactLoop,
		"test query",
		"",
		"session-1",
		"task-fail",
		"wf-fail",
		"run-fail",
		"gpt-4o-mini",
		0.7,
		1024,
		configOneIteration(),
	)

	require.True(t, env.IsWorkflowCompleted())
	var result types.ReactLoopResult
	err := env.GetWorkflowResult(&result)
	require.NoError(t, err)
	require.NotEmpty(t, result.FinalResult)

	// Activity was called (trigger matched) but error was handled gracefully
	env.AssertCalled(t, "EmbedAndSearchChunksActivity", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "FetchChunkContentActivity", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "PackContextActivity", mock.Anything, mock.Anything)
}

// Additional test: verify hasRetrievalTrigger directly
func TestHasRetrievalTrigger(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"search: foo", true},
		{"SEARCH: foo", true},
		{"retrieve: foo", true},
		{"Retrieve: foo", true},
		{"lookup: foo", true},
		{"rag: foo", true},
		{"RAG: foo", true},
		{"no trigger", false},
		{"search without colon", false},
		{"", false},
		{"search: retrieve: lookup: rag:", true},
		{"FINAL: answer", false},
	}
	for _, tt := range tests {
		got := hasRetrievalTrigger(tt.input)
		if got != tt.want {
			t.Errorf("hasRetrievalTrigger(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}
