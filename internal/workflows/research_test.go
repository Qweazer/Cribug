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

func setupResearchSynthesisTest(t *testing.T) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()
	env.RegisterWorkflowWithOptions(ResearchSynthesisWorkflow, workflow.RegisterOptions{Name: ResearchSynthesisWorkflowName})

	// Register activity stubs (env.OnActivity overrides per-test)
	env.RegisterActivityWithOptions(
		func(ctx interface{}, input interface{}) (interface{}, error) {
			return DecomposeQueryOutput{}, nil
		},
		activity.RegisterOptions{Name: "DecomposeQueryActivity"},
	)
	env.RegisterActivityWithOptions(
		func(ctx interface{}, input interface{}) (interface{}, error) {
			return RetrieveEvidenceOutput{}, nil
		},
		activity.RegisterOptions{Name: "RetrieveEvidenceActivity"},
	)
	env.RegisterActivityWithOptions(
		func(ctx interface{}, input interface{}) (interface{}, error) {
			return SynthesizeResultOutput{}, nil
		},
		activity.RegisterOptions{Name: "SynthesizeResultActivity"},
	)

	return env
}

func TestResearchSynthesis_Success(t *testing.T) {
	env := setupResearchSynthesisTest(t)

	env.OnActivity("DecomposeQueryActivity", mock.Anything, mock.Anything).
		Return(DecomposeQueryOutput{
			QueryID: "query-1",
			Subqueries: []ResearchSubquery{
				{ID: "sq-1", Text: "subquery one"},
				{ID: "sq-2", Text: "subquery two"},
				{ID: "sq-3", Text: "subquery three"},
			},
		}, nil)
	env.OnActivity("RetrieveEvidenceActivity", mock.Anything, mock.Anything).
		Return(RetrieveEvidenceOutput{
			Summary:  "Evidence summary",
			ChunkIDs: []string{"chunk-1", "chunk-2"},
			Score:    0.95,
		}, nil)
	env.OnActivity("SynthesizeResultActivity", mock.Anything, mock.Anything).
		Return(SynthesizeResultOutput{
			Answer:     "Final synthesized answer",
			TokenCount: 500,
			SubqueryAnswers: []SubqueryAnswer{
				{SubqueryID: "sq-1", Answer: "Answer 1", Evidence: "Evidence 1"},
				{SubqueryID: "sq-2", Answer: "Answer 2", Evidence: "Evidence 2"},
				{SubqueryID: "sq-3", Answer: "Answer 3", Evidence: "Evidence 3"},
			},
		}, nil)

	env.ExecuteWorkflow(ResearchSynthesisWorkflowName, ResearchSynthesisWorkflowInput{
		Query:      "test research question",
		AgentID:    "agent-1",
		WorkflowID: "wf-1",
		Collection: "docs",
		TopK:       5,
		Model:      "gpt-4",
		MockLLM:    false,
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}

	var result ResearchSynthesisWorkflowOutput
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("workflow result error: %v", err)
	}
	if result.QueryID != "query-1" {
		t.Errorf("expected QueryID 'query-1', got '%s'", result.QueryID)
	}
	if result.Answer != "Final synthesized answer" {
		t.Errorf("expected Answer 'Final synthesized answer', got '%s'", result.Answer)
	}
	if len(result.Evidence) != 3 {
		t.Errorf("expected 3 evidence refs, got %d", len(result.Evidence))
	}
	if len(result.SubqueryAnswers) != 3 {
		t.Errorf("expected 3 subquery answers, got %d", len(result.SubqueryAnswers))
	}
	if result.TokenCount != 500 {
		t.Errorf("expected TokenCount 500, got %d", result.TokenCount)
	}
	if result.Partial {
		t.Error("expected Partial to be false")
	}
	if result.Error != "" {
		t.Errorf("expected no error, got '%s'", result.Error)
	}
}

func TestResearchSynthesis_DecomposeFails(t *testing.T) {
	env := setupResearchSynthesisTest(t)

	env.OnActivity("DecomposeQueryActivity", mock.Anything, mock.Anything).
		Return(DecomposeQueryOutput{}, errors.New("decomposition failed"))

	env.ExecuteWorkflow(ResearchSynthesisWorkflowName, ResearchSynthesisWorkflowInput{
		Query:      "failing query",
		AgentID:    "agent-1",
		WorkflowID: "wf-1",
		Collection: "docs",
		TopK:       5,
		Model:      "gpt-4",
		MockLLM:    false,
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}

	var result ResearchSynthesisWorkflowOutput
	err := env.GetWorkflowResult(&result)
	if err == nil {
		t.Fatal("expected workflow error on decomposition failure")
	}
	if !strings.Contains(err.Error(), "decomposition failed") {
		t.Errorf("expected error containing 'decomposition failed', got '%v'", err)
	}
}

func TestResearchSynthesis_EvidenceRetrievalPartialFailure(t *testing.T) {
	env := setupResearchSynthesisTest(t)

	env.OnActivity("DecomposeQueryActivity", mock.Anything, mock.Anything).
		Return(DecomposeQueryOutput{
			QueryID: "query-1",
			Subqueries: []ResearchSubquery{
				{ID: "sq-1", Text: "subquery one"},
				{ID: "sq-2", Text: "subquery two"},
				{ID: "sq-3", Text: "subquery three"},
			},
		}, nil)

	// First and third retrievals succeed; second fails
	env.OnActivity("RetrieveEvidenceActivity", mock.Anything, mock.Anything).
		Return(RetrieveEvidenceOutput{
			Summary:  "Evidence summary 1",
			ChunkIDs: []string{"chunk-1"},
			Score:    0.90,
		}, nil).
		Once()
	env.OnActivity("RetrieveEvidenceActivity", mock.Anything, mock.Anything).
		Return(RetrieveEvidenceOutput{}, errors.New("retrieval failed for sq-2")).
		Once()
	env.OnActivity("RetrieveEvidenceActivity", mock.Anything, mock.Anything).
		Return(RetrieveEvidenceOutput{
			Summary:  "Evidence summary 3",
			ChunkIDs: []string{"chunk-3"},
			Score:    0.85,
		}, nil).
		Once()

	env.OnActivity("SynthesizeResultActivity", mock.Anything, mock.Anything).
		Return(SynthesizeResultOutput{
			Answer:     "Synthesized with partial evidence",
			TokenCount: 400,
			SubqueryAnswers: []SubqueryAnswer{
				{SubqueryID: "sq-1", Answer: "Answer 1"},
				{SubqueryID: "sq-3", Answer: "Answer 3"},
			},
		}, nil)

	env.ExecuteWorkflow(ResearchSynthesisWorkflowName, ResearchSynthesisWorkflowInput{
		Query:      "test research question",
		AgentID:    "agent-1",
		WorkflowID: "wf-1",
		Collection: "docs",
		TopK:       5,
		Model:      "gpt-4",
		MockLLM:    false,
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}

	var result ResearchSynthesisWorkflowOutput
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("workflow result error: %v", err)
	}
	if result.QueryID != "query-1" {
		t.Errorf("expected QueryID 'query-1', got '%s'", result.QueryID)
	}
	if result.Answer != "Synthesized with partial evidence" {
		t.Errorf("expected Answer 'Synthesized with partial evidence', got '%s'", result.Answer)
	}
	if !result.Partial {
		t.Error("expected Partial to be true when a retrieval fails")
	}
	if len(result.Evidence) != 2 {
		t.Errorf("expected 2 evidence refs (1 failed retrieval), got %d", len(result.Evidence))
	}
	if result.Error != "" {
		t.Errorf("expected no error, got '%s'", result.Error)
	}
	// Verify both successful subquery answers are present
	if len(result.SubqueryAnswers) != 2 {
		t.Errorf("expected 2 subquery answers, got %d", len(result.SubqueryAnswers))
	}
}

func TestResearchSynthesis_NoSubqueries(t *testing.T) {
	env := setupResearchSynthesisTest(t)

	env.OnActivity("DecomposeQueryActivity", mock.Anything, mock.Anything).
		Return(DecomposeQueryOutput{
			QueryID:    "query-empty",
			Subqueries: nil,
		}, nil)
	// No other activities should be called

	env.ExecuteWorkflow(ResearchSynthesisWorkflowName, ResearchSynthesisWorkflowInput{
		Query:      "simple query",
		AgentID:    "agent-1",
		WorkflowID: "wf-1",
		Collection: "docs",
		TopK:       5,
		Model:      "gpt-4",
		MockLLM:    false,
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}

	var result ResearchSynthesisWorkflowOutput
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("workflow result error: %v", err)
	}
	if result.QueryID != "query-empty" {
		t.Errorf("expected QueryID 'query-empty', got '%s'", result.QueryID)
	}
	if result.Answer != "Query could not be decomposed into subqueries." {
		t.Errorf("expected early exit message, got '%s'", result.Answer)
	}
	if len(result.Evidence) != 0 {
		t.Errorf("expected no evidence refs, got %d", len(result.Evidence))
	}
	if len(result.SubqueryAnswers) != 0 {
		t.Errorf("expected no subquery answers, got %d", len(result.SubqueryAnswers))
	}
	if result.Partial {
		t.Error("expected Partial to be false")
	}
	if result.TokenCount != 0 {
		t.Errorf("expected TokenCount 0, got %d", result.TokenCount)
	}
}
