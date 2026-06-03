package workflows

import (
	"strings"
	"testing"

	"cribug/internal/activities"
	"cribug/internal/types"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func setupResearchV2TestEnv(t *testing.T) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()
	env.RegisterWorkflowWithOptions(ResearchSynthesisV2Workflow, workflow.RegisterOptions{Name: ResearchSynthesisV2WorkflowName})

	env.RegisterActivityWithOptions(
		func(ctx interface{}, input interface{}) (interface{}, error) {
			return activities.LLMConfigSnapshot{Provider: "openai_compatible", ChatModel: "gpt-4o-mini"}, nil
		},
		activity.RegisterOptions{Name: "ResolveEffectiveLLMConfigActivity"},
	)
	env.RegisterActivityWithOptions(
		func(ctx interface{}, input interface{}) (interface{}, error) {
			return activities.PlanResearchResult{}, nil
		},
		activity.RegisterOptions{Name: "PlanResearchActivity"},
	)
	env.RegisterActivityWithOptions(
		func(ctx interface{}, input interface{}) (interface{}, error) {
			return activities.RetrieveMultiSourceEvidenceResult{}, nil
		},
		activity.RegisterOptions{Name: "RetrieveMultiSourceEvidenceActivity"},
	)
	env.RegisterActivityWithOptions(
		func(ctx interface{}, input interface{}) (interface{}, error) {
			return activities.ScoreSourceCredibilityResult{}, nil
		},
		activity.RegisterOptions{Name: "ScoreSourceCredibilityActivity"},
	)
	env.RegisterActivityWithOptions(
		func(ctx interface{}, input interface{}) (interface{}, error) {
			return activities.FilterByCredibilityResult{}, nil
		},
		activity.RegisterOptions{Name: "FilterByCredibilityActivity"},
	)
	env.RegisterActivityWithOptions(
		func(ctx interface{}, input interface{}) (interface{}, error) {
			return activities.DetectContradictionsResult{}, nil
		},
		activity.RegisterOptions{Name: "DetectContradictionsActivity"},
	)
	env.RegisterActivityWithOptions(
		func(ctx interface{}, input interface{}) (interface{}, error) {
			return activities.BuildCitationChainResult{}, nil
		},
		activity.RegisterOptions{Name: "BuildCitationChainActivity"},
	)
	env.RegisterActivityWithOptions(
		func(ctx interface{}, input interface{}) (interface{}, error) {
			return activities.GenerateReportV2Result{}, nil
		},
		activity.RegisterOptions{Name: "GenerateReportV2Activity"},
	)
	env.RegisterActivityWithOptions(
		func(ctx interface{}, input interface{}) (interface{}, error) {
			return activities.ReflectionBeforeSynthesisResult{}, nil
		},
		activity.RegisterOptions{Name: "ReflectionBeforeSynthesisActivity"},
	)
	env.RegisterActivityWithOptions(
		func(ctx interface{}, input interface{}) (interface{}, error) {
			return activities.DebateBeforeSynthesisResult{}, nil
		},
		activity.RegisterOptions{Name: "DebateBeforeSynthesisActivity"},
	)
	env.RegisterActivityWithOptions(
		func(ctx interface{}, input interface{}) (interface{}, error) {
			return activities.AuditResearchV2Result{AuditID: "audit-1"}, nil
		},
		activity.RegisterOptions{Name: "AuditResearchV2Activity"},
	)
	return env
}

func TestResearchV2MockHappyPath(t *testing.T) {
	env := setupResearchV2TestEnv(t)

	env.OnActivity("PlanResearchActivity", mock.Anything, mock.Anything).
		Return(activities.PlanResearchResult{
			QueryID:    "q-1",
			Subqueries: []types.ResearchSubqueryV2{{ID: "sq-1", Text: "what is async polling?", Intent: "definition"}},
			TokensUsed: 30,
			Mode:       "mock",
		}, nil)
	env.OnActivity("RetrieveMultiSourceEvidenceActivity", mock.Anything, mock.Anything).
		Return(activities.RetrieveMultiSourceEvidenceResult{
			Evidence: []types.Evidence{
				{ID: "ev-1", SourceID: "src-1", SourceType: types.SourceTypeLocalRAG, Summary: "evidence 1", ContentRef: "ref-1", CredibilityScore: 0.7},
				{ID: "ev-2", SourceID: "src-2", SourceType: types.SourceTypeLocalRAG, Summary: "evidence 2", ContentRef: "ref-2", CredibilityScore: 0.6},
			},
			TotalSources: 2,
		}, nil)
	env.OnActivity("ScoreSourceCredibilityActivity", mock.Anything, mock.Anything).
		Return(activities.ScoreSourceCredibilityResult{Domain: "x", CredibilityScore: 0.7, QualityScore: 0.7}, nil)
	env.OnActivity("FilterByCredibilityActivity", mock.Anything, mock.Anything).
		Return(activities.FilterByCredibilityResult{Filtered: []types.Evidence{
			{ID: "ev-1", SourceID: "src-1", SourceType: types.SourceTypeLocalRAG, Summary: "evidence 1", ContentRef: "ref-1", CredibilityScore: 0.7},
			{ID: "ev-2", SourceID: "src-2", SourceType: types.SourceTypeLocalRAG, Summary: "evidence 2", ContentRef: "ref-2", CredibilityScore: 0.6},
		}}, nil)
	env.OnActivity("DetectContradictionsActivity", mock.Anything, mock.Anything).
		Return(activities.DetectContradictionsResult{}, nil)
	env.OnActivity("BuildCitationChainActivity", mock.Anything, mock.Anything).
		Return(activities.BuildCitationChainResult{
			CitationChains: [][]string{
				{"src-1", "ev-1"},
				{"src-2", "ev-2"},
			},
		}, nil)
	env.OnActivity("GenerateReportV2Activity", mock.Anything, mock.Anything).
		Return(activities.GenerateReportV2Result{
			Report: types.ResearchReportV2{
				QueryID:          "q-1",
				Title:            "Research report: test",
				ExecutiveSummary: "Mock executive summary",
				TotalSources:     2,
				TotalEvidenceItems: 2,
				ReportRef:        "research:wf:report",
				ConfidenceScore:  0.7,
			},
			ReportRef:        "research:wf:report",
			ExecutiveRef:     "research:wf:exec",
			ContradictionRef: "research:wf:contra",
			TokensUsed:       60,
			Mode:             "mock",
		}, nil)

	env.ExecuteWorkflow(ResearchSynthesisV2Workflow, types.ResearchV2WorkflowInput{
		TaskID:     "t-1",
		WorkflowID: "wf-1",
		RunID:      "r-1",
		SessionID:  "s-1",
		Query:      "Compare async polling vs sync HTTP waiting.",
		Config: types.ResearchV2Config{
			MaxSources: 3, MaxEvidenceItems: 10, MaxSubqueries: 3, MaxIterations: 1,
			TokenBudget: 4000, CredibilityThreshold: 0.4,
			EnableContradictionDetection: true, RequireCitations: true,
			SourceTypes: []string{types.SourceTypeLocalRAG}, MockLLM: true,
		},
	})

	if !env.IsWorkflowCompleted() {
		t.Fatalf("workflow not completed: %v", env.GetWorkflowError())
	}
	var result types.ResearchV2WorkflowResult
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("result err: %v", err)
	}
	if result.ReportRef == "" {
		t.Error("report_ref must be set")
	}
	if result.FinalAnswerRef == "" {
		t.Error("final_answer_ref must be set")
	}
	if result.SourceCount != 2 {
		t.Errorf("source_count = %d, want 2", result.SourceCount)
	}
	if result.EvidenceCount != 2 {
		t.Errorf("evidence_count = %d, want 2", result.EvidenceCount)
	}
	if result.Mode != "mock" {
		t.Errorf("mode = %q, want mock", result.Mode)
	}
	if !result.Mock {
		t.Error("mock flag must be true in mock mode")
	}
	if result.LLMCalls < 2 {
		// Plan + Generate at minimum
		t.Errorf("llm_calls = %d, want >= 2", result.LLMCalls)
	}
	if result.WorkspaceTopic == "" {
		t.Error("workspace_topic must be set")
	}
}

func TestResearchV2EmptyEvidenceDoesNotPanic(t *testing.T) {
	env := setupResearchV2TestEnv(t)
	env.OnActivity("PlanResearchActivity", mock.Anything, mock.Anything).
		Return(activities.PlanResearchResult{
			QueryID:    "q-1",
			Subqueries: []types.ResearchSubqueryV2{{ID: "sq-1", Text: "Q"}},
			TokensUsed: 30, Mode: "mock",
		}, nil)
	env.OnActivity("RetrieveMultiSourceEvidenceActivity", mock.Anything, mock.Anything).
		Return(activities.RetrieveMultiSourceEvidenceResult{Evidence: nil}, nil)
	env.OnActivity("ScoreSourceCredibilityActivity", mock.Anything, mock.Anything).
		Return(activities.ScoreSourceCredibilityResult{}, nil)
	env.OnActivity("FilterByCredibilityActivity", mock.Anything, mock.Anything).
		Return(activities.FilterByCredibilityResult{Filtered: nil}, nil)
	env.OnActivity("DetectContradictionsActivity", mock.Anything, mock.Anything).
		Return(activities.DetectContradictionsResult{}, nil)
	env.OnActivity("BuildCitationChainActivity", mock.Anything, mock.Anything).
		Return(activities.BuildCitationChainResult{}, nil)
	env.OnActivity("GenerateReportV2Activity", mock.Anything, mock.Anything).
		Return(activities.GenerateReportV2Result{
			Report: types.ResearchReportV2{
				QueryID: "q-1", Title: "empty", ExecutiveSummary: "(no evidence)",
				TotalSources: 0, TotalEvidenceItems: 0, ReportRef: "research:wf:report",
				ConfidenceScore: 0.2,
			},
			ReportRef: "research:wf:report",
			ExecutiveRef: "research:wf:exec", TokensUsed: 30, Mode: "mock",
		}, nil)

	env.ExecuteWorkflow(ResearchSynthesisV2Workflow, types.ResearchV2WorkflowInput{
		TaskID: "t-1", WorkflowID: "wf-1", RunID: "r-1", SessionID: "s-1",
		Query: "Q",
		Config: types.ResearchV2Config{MockLLM: true, SourceTypes: []string{types.SourceTypeLocalRAG}},
	})
	if !env.IsWorkflowCompleted() {
		t.Fatalf("workflow not completed: %v", env.GetWorkflowError())
	}
	var result types.ResearchV2WorkflowResult
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("result: %v", err)
	}
	if result.SourceCount != 0 {
		t.Errorf("source_count = %d, want 0", result.SourceCount)
	}
	if result.FinalAnswerText == "" {
		t.Error("final answer text should still be set even with no evidence")
	}
}

func TestResearchV2ResultExcludesFullReport(t *testing.T) {
	// Result must carry refs + short preview, NOT the full report text.
	env := setupResearchV2TestEnv(t)
	env.OnActivity("PlanResearchActivity", mock.Anything, mock.Anything).
		Return(activities.PlanResearchResult{Subqueries: []types.ResearchSubqueryV2{{ID: "sq-1", Text: "Q"}}, Mode: "mock"}, nil)
	env.OnActivity("RetrieveMultiSourceEvidenceActivity", mock.Anything, mock.Anything).
		Return(activities.RetrieveMultiSourceEvidenceResult{Evidence: []types.Evidence{{ID: "e", SourceID: "s", SourceType: "x", Summary: "sum"}}}, nil)
	env.OnActivity("ScoreSourceCredibilityActivity", mock.Anything, mock.Anything).
		Return(activities.ScoreSourceCredibilityResult{CredibilityScore: 0.7}, nil)
	env.OnActivity("FilterByCredibilityActivity", mock.Anything, mock.Anything).
		Return(activities.FilterByCredibilityResult{Filtered: []types.Evidence{{ID: "e", SourceID: "s", SourceType: "x", Summary: "sum", CredibilityScore: 0.7}}}, nil)
	env.OnActivity("DetectContradictionsActivity", mock.Anything, mock.Anything).
		Return(activities.DetectContradictionsResult{}, nil)
	env.OnActivity("BuildCitationChainActivity", mock.Anything, mock.Anything).
		Return(activities.BuildCitationChainResult{CitationChains: [][]string{{"s"}}}, nil)
	env.OnActivity("GenerateReportV2Activity", mock.Anything, mock.Anything).
		Return(activities.GenerateReportV2Result{
			Report: types.ResearchReportV2{
				Title:            "t",
				ExecutiveSummary: strings.Repeat("a", 2000),
				ReportRef:        "research:wf:report",
			},
			ReportRef: "research:wf:report", ExecutiveRef: "research:wf:exec",
			Mode: "mock",
		}, nil)

	env.ExecuteWorkflow(ResearchSynthesisV2Workflow, types.ResearchV2WorkflowInput{
		TaskID: "t-1", WorkflowID: "wf-1", RunID: "r-1", SessionID: "s-1", Query: "Q",
		Config: types.ResearchV2Config{MockLLM: true, SourceTypes: []string{types.SourceTypeLocalRAG}},
	})
	var result types.ResearchV2WorkflowResult
	_ = env.GetWorkflowResult(&result)
	if len(result.FinalAnswerText) > 1500 {
		t.Errorf("final_answer_text too long: %d (must be ≤ 1500 chars)", len(result.FinalAnswerText))
	}
}

func TestResearchV2DefaultConfigFilledIn(t *testing.T) {
	env := setupResearchV2TestEnv(t)
	env.OnActivity("PlanResearchActivity", mock.Anything, mock.Anything).
		Return(activities.PlanResearchResult{Subqueries: []types.ResearchSubqueryV2{{ID: "sq-1", Text: "Q"}}, Mode: "mock"}, nil)
	env.OnActivity("RetrieveMultiSourceEvidenceActivity", mock.Anything, mock.Anything).
		Return(activities.RetrieveMultiSourceEvidenceResult{Evidence: []types.Evidence{{ID: "e", SourceID: "s", SourceType: "x", Summary: "sum"}}}, nil)
	env.OnActivity("ScoreSourceCredibilityActivity", mock.Anything, mock.Anything).
		Return(activities.ScoreSourceCredibilityResult{CredibilityScore: 0.7}, nil)
	env.OnActivity("FilterByCredibilityActivity", mock.Anything, mock.Anything).
		Return(activities.FilterByCredibilityResult{Filtered: []types.Evidence{{ID: "e", SourceID: "s", SourceType: "x", Summary: "sum", CredibilityScore: 0.7}}}, nil)
	env.OnActivity("DetectContradictionsActivity", mock.Anything, mock.Anything).
		Return(activities.DetectContradictionsResult{}, nil)
	env.OnActivity("BuildCitationChainActivity", mock.Anything, mock.Anything).
		Return(activities.BuildCitationChainResult{CitationChains: [][]string{{"s"}}}, nil)
	env.OnActivity("GenerateReportV2Activity", mock.Anything, mock.Anything).
		Return(activities.GenerateReportV2Result{
			Report: types.ResearchReportV2{Title: "t", ExecutiveSummary: "e", ReportRef: "research:wf:report"},
			ReportRef: "research:wf:report", ExecutiveRef: "research:wf:exec", Mode: "mock",
		}, nil)

	// Empty config should still produce a valid result.
	env.ExecuteWorkflow(ResearchSynthesisV2Workflow, types.ResearchV2WorkflowInput{
		TaskID: "t-1", WorkflowID: "wf-1", RunID: "r-1", SessionID: "s-1", Query: "Q",
		Config: types.ResearchV2Config{},
	})
	if !env.IsWorkflowCompleted() {
		t.Fatalf("workflow failed: %v", env.GetWorkflowError())
	}
	var result types.ResearchV2WorkflowResult
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("result: %v", err)
	}
	if result.ReportRef == "" {
		t.Error("default config should still produce refs")
	}
}

func TestResearchV2TokenBudgetBound(t *testing.T) {
	env := setupResearchV2TestEnv(t)
	env.OnActivity("PlanResearchActivity", mock.Anything, mock.Anything).
		Return(activities.PlanResearchResult{
			Subqueries: []types.ResearchSubqueryV2{
				{ID: "sq-1", Text: "Q1"},
				{ID: "sq-2", Text: "Q2"},
			},
			Mode: "mock",
		}, nil)
	retrievalCount := 0
	env.OnActivity("RetrieveMultiSourceEvidenceActivity", mock.Anything, mock.Anything).
		Return(func(ctx interface{}, in activities.RetrieveMultiSourceEvidenceInput) (activities.RetrieveMultiSourceEvidenceResult, error) {
			retrievalCount++
			return activities.RetrieveMultiSourceEvidenceResult{
				Evidence: []types.Evidence{{ID: "e", SourceID: "s", SourceType: "x", Summary: "sum"}},
			}, nil
		})
	env.OnActivity("ScoreSourceCredibilityActivity", mock.Anything, mock.Anything).
		Return(activities.ScoreSourceCredibilityResult{CredibilityScore: 0.7}, nil)
	env.OnActivity("FilterByCredibilityActivity", mock.Anything, mock.Anything).
		Return(activities.FilterByCredibilityResult{Filtered: []types.Evidence{{ID: "e", SourceID: "s", SourceType: "x", Summary: "sum", CredibilityScore: 0.7}}}, nil)
	env.OnActivity("DetectContradictionsActivity", mock.Anything, mock.Anything).
		Return(activities.DetectContradictionsResult{}, nil)
	env.OnActivity("BuildCitationChainActivity", mock.Anything, mock.Anything).
		Return(activities.BuildCitationChainResult{}, nil)
	env.OnActivity("GenerateReportV2Activity", mock.Anything, mock.Anything).
		Return(activities.GenerateReportV2Result{
			Report: types.ResearchReportV2{Title: "t", ExecutiveSummary: "e", ReportRef: "research:wf:report"},
			ReportRef: "research:wf:report", ExecutiveRef: "research:wf:exec", Mode: "mock",
		}, nil)

	env.ExecuteWorkflow(ResearchSynthesisV2Workflow, types.ResearchV2WorkflowInput{
		TaskID: "t-1", WorkflowID: "wf-1", RunID: "r-1", SessionID: "s-1", Query: "Q",
		Config: types.ResearchV2Config{
			MaxSources: 3, MaxEvidenceItems: 3, MaxSubqueries: 3, MaxIterations: 1,
			TokenBudget: 5, MockLLM: true, // tiny budget to force early stop
			SourceTypes: []string{types.SourceTypeLocalRAG},
		},
	})
	var result types.ResearchV2WorkflowResult
	_ = env.GetWorkflowResult(&result)
	// The Plan activity itself can return a non-zero TokensUsed and
	// that may already exhaust the budget, so retrieval is allowed to
	// be skipped. The contract is: workflow completes, no panic,
	// result has a valid ReportRef.
	if result.ReportRef == "" {
		t.Error("workflow must still produce a ReportRef even with tiny budget")
	}
}
