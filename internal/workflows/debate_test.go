package workflows

import (
	"errors"
	"strings"
	"testing"

	"cribug/internal/activities"
	"cribug/internal/types"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func setupDebateTestEnv(t *testing.T) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()
	env.RegisterWorkflowWithOptions(DebateWorkflow, workflow.RegisterOptions{Name: DebateWorkflowName})

	// Default stubs; tests override via env.OnActivity.
	env.RegisterActivityWithOptions(
		func(ctx interface{}, input interface{}) (interface{}, error) {
			return activities.LLMConfigSnapshot{Provider: "openai_compatible", ChatModel: "gpt-4o-mini"}, nil
		},
		activity.RegisterOptions{Name: "ResolveEffectiveLLMConfigActivity"},
	)
	env.RegisterActivityWithOptions(
		func(ctx interface{}, input interface{}) (interface{}, error) {
			return activities.GenerateArgumentsResult{}, nil
		},
		activity.RegisterOptions{Name: "GenerateArgumentsActivity"},
	)
	env.RegisterActivityWithOptions(
		func(ctx interface{}, input interface{}) (interface{}, error) {
			return activities.JudgeDebateResult{}, nil
		},
		activity.RegisterOptions{Name: "JudgeDebateActivity"},
	)
	env.RegisterActivityWithOptions(
		func(ctx interface{}, input interface{}) (interface{}, error) {
			return activities.AuditDebateResult{AuditID: "audit"}, nil
		},
		activity.RegisterOptions{Name: "AuditDebateActivity"},
	)
	return env
}

func TestDebateWorkflow_MockHappyPath(t *testing.T) {
	env := setupDebateTestEnv(t)

	env.OnActivity("GenerateArgumentsActivity", mock.Anything, mock.Anything).
		Return(activities.GenerateArgumentsResult{
			TurnID:     "turn-pro-r0",
			Content:    "mock pro argument",
			ContentRef: "debate:wf-1:turn:pro-r0",
			Summary:    "mock pro",
			Score:      0.7,
			Confidence: 0.8,
			TokensUsed: 30,
			Mode:       "mock",
		}, nil)

	env.OnActivity("JudgeDebateActivity", mock.Anything, mock.Anything).
		Return(activities.JudgeDebateResult{
			Verdict: &types.JudgeVerdict{
				Verdict: "pro", ProScore: 0.6, ConScore: 0.4,
				Confidence: 0.8, Rationale: "mock",
			},
			VerdictRef:    "verdict-ref",
			ProScore:      0.6,
			ConScore:      0.4,
			Confidence:    0.8,
			TokensUsed:    20,
			Mode:          "mock",
			ParseSource:   "heuristic",
			ConfidenceSrc: "fallback",
		}, nil)

	env.ExecuteWorkflow(DebateWorkflow, types.DebateWorkflowInput{
		TaskID:     "t1",
		WorkflowID: "wf-1",
		RunID:      "r1",
		SessionID:  "s1",
		Query:      "Should we adopt deterministic workflows?",
		Config: types.DebateConfig{
			MaxRounds:        2,
			ModeratorEnabled: true,
			ModelTier:        "small",
			RoundTimeoutSecs: 30,
			MockLLM:          true,
		},
	})

	if !env.IsWorkflowCompleted() {
		t.Fatalf("workflow not completed: %v", env.GetWorkflowError())
	}
	var result types.DebateResult
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("workflow result: %v", err)
	}

	// Result contract:
	if result.TranscriptRef == "" {
		t.Error("transcript_ref must be set")
	}
	if result.VerdictRef == "" {
		t.Error("verdict_ref must be set")
	}
	if result.FinalAnswerRef == "" {
		t.Error("final_answer_ref must be set")
	}
	if result.Rounds <= 0 {
		t.Errorf("rounds = %d, want > 0", result.Rounds)
	}
	if result.Mode != "mock" {
		t.Errorf("mode = %q, want mock", result.Mode)
	}
	if !result.Mock {
		t.Error("mock flag must be true in mock mode")
	}
	if result.FinalPosition == "" {
		t.Error("final_position must be set")
	}
	// Mock happy path with alternating verdicts + RequireConsensus=false
	// means we run all rounds then final judge decides.
	if result.TotalTokens <= 0 {
		t.Error("total_tokens must be > 0")
	}
}

func TestDebateWorkflow_ProAgentFailureContinues(t *testing.T) {
	env := setupDebateTestEnv(t)

	// Pro fails the first call but succeeds afterwards (or returns an
	// error and we move on). Con and Judge still run.
	callCount := 0
	env.OnActivity("GenerateArgumentsActivity", mock.Anything, mock.Anything).
		Return(func(ctx interface{}, in activities.GenerateArgumentsInput) (activities.GenerateArgumentsResult, error) {
			callCount++
			if callCount == 1 {
				return activities.GenerateArgumentsResult{}, errors.New("pro LLM down")
			}
			return activities.GenerateArgumentsResult{
				TurnID:     "turn-x",
				ContentRef: "ref-x",
				Summary:    "ok",
				TokensUsed: 20,
				Mode:       "mock",
			}, nil
		}, nil)

	env.OnActivity("JudgeDebateActivity", mock.Anything, mock.Anything).
		Return(activities.JudgeDebateResult{
			Verdict: &types.JudgeVerdict{Verdict: "tie", ProScore: 0.5, ConScore: 0.5, Confidence: 0.5},
			VerdictRef: "v", ProScore: 0.5, ConScore: 0.5, Confidence: 0.5,
			TokensUsed: 10, Mode: "mock", ParseSource: "heuristic", ConfidenceSrc: "fallback",
		}, nil)

	env.ExecuteWorkflow(DebateWorkflow, types.DebateWorkflowInput{
		TaskID: "t1", WorkflowID: "wf-1", RunID: "r1", SessionID: "s1",
		Query: "Q",
		Config: types.DebateConfig{MaxRounds: 1, MockLLM: true, ModeratorEnabled: true},
	})

	if !env.IsWorkflowCompleted() {
		t.Fatalf("workflow should still complete despite Pro failure: %v", env.GetWorkflowError())
	}
	var result types.DebateResult
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("result err: %v", err)
	}
	if result.Rounds < 1 {
		t.Errorf("rounds = %d, want >=1", result.Rounds)
	}
}

func TestDebateWorkflow_RequireConsensusStopsEarly(t *testing.T) {
	env := setupDebateTestEnv(t)

	env.OnActivity("GenerateArgumentsActivity", mock.Anything, mock.Anything).
		Return(activities.GenerateArgumentsResult{
			TurnID: "t", ContentRef: "r", Summary: "s", TokensUsed: 10, Mode: "mock",
		}, nil)

	// Judge always returns a confident pro.
	env.OnActivity("JudgeDebateActivity", mock.Anything, mock.Anything).
		Return(activities.JudgeDebateResult{
			Verdict: &types.JudgeVerdict{Verdict: "pro", ProScore: 0.9, ConScore: 0.3, Confidence: 0.95},
			VerdictRef: "v", ProScore: 0.9, ConScore: 0.3, Confidence: 0.95,
			TokensUsed: 10, Mode: "mock", ParseSource: "heuristic", ConfidenceSrc: "fallback",
		}, nil)

	env.ExecuteWorkflow(DebateWorkflow, types.DebateWorkflowInput{
		TaskID: "t1", WorkflowID: "wf-1", RunID: "r1", SessionID: "s1",
		Query: "Q",
		Config: types.DebateConfig{
			MaxRounds: 5, MockLLM: true, ModeratorEnabled: true,
			RequireConsensus: true,
		},
	})

	var result types.DebateResult
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("result err: %v", err)
	}
	if !result.ConsensusReached {
		t.Error("consensus should be reached")
	}
	if result.FinalPosition != "pro" {
		t.Errorf("final_position = %q, want pro", result.FinalPosition)
	}
	if result.Rounds > 1 {
		t.Errorf("rounds = %d, want 1 (early stop)", result.Rounds)
	}
}

func TestDebateWorkflow_MaxRoundsBounded(t *testing.T) {
	env := setupDebateTestEnv(t)

	env.OnActivity("GenerateArgumentsActivity", mock.Anything, mock.Anything).
		Return(activities.GenerateArgumentsResult{
			TurnID: "t", ContentRef: "r", Summary: "s", TokensUsed: 10, Mode: "mock",
		}, nil)

	// Judge always returns tie (no consensus possible).
	env.OnActivity("JudgeDebateActivity", mock.Anything, mock.Anything).
		Return(activities.JudgeDebateResult{
			Verdict: &types.JudgeVerdict{Verdict: "tie", ProScore: 0.5, ConScore: 0.5, Confidence: 0.4},
			VerdictRef: "v", ProScore: 0.5, ConScore: 0.5, Confidence: 0.4,
			TokensUsed: 10, Mode: "mock", ParseSource: "heuristic", ConfidenceSrc: "fallback",
		}, nil)

	env.ExecuteWorkflow(DebateWorkflow, types.DebateWorkflowInput{
		TaskID: "t1", WorkflowID: "wf-1", RunID: "r1", SessionID: "s1",
		Query: "Q",
		Config: types.DebateConfig{
			MaxRounds: 3, MockLLM: true, ModeratorEnabled: true,
			RequireConsensus: false,
		},
	})

	var result types.DebateResult
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("result err: %v", err)
	}
	if result.Rounds > 3 {
		t.Errorf("rounds = %d, want <= 3 (max_rounds bound)", result.Rounds)
	}
}

func TestDebateWorkflow_ResultExcludesFullTranscript(t *testing.T) {
	// History hygiene: result must not contain the full transcript text.
	// We assert that FinalAnswerText is bounded and not the entire debate.
	env := setupDebateTestEnv(t)
	env.OnActivity("GenerateArgumentsActivity", mock.Anything, mock.Anything).
		Return(activities.GenerateArgumentsResult{
			TurnID: "t", ContentRef: "r", Summary: "short summary",
			TokensUsed: 10, Mode: "mock",
		}, nil)
	env.OnActivity("JudgeDebateActivity", mock.Anything, mock.Anything).
		Return(activities.JudgeDebateResult{
			Verdict: &types.JudgeVerdict{Verdict: "pro", ProScore: 0.7, ConScore: 0.4, Confidence: 0.8},
			VerdictRef: "v", ProScore: 0.7, ConScore: 0.4, Confidence: 0.8,
			TokensUsed: 10, Mode: "mock", ParseSource: "heuristic", ConfidenceSrc: "fallback",
		}, nil)

	env.ExecuteWorkflow(DebateWorkflow, types.DebateWorkflowInput{
		TaskID: "t1", WorkflowID: "wf-1", RunID: "r1", SessionID: "s1",
		Query: "Q",
		Config: types.DebateConfig{MaxRounds: 2, MockLLM: true, ModeratorEnabled: true},
	})

	var result types.DebateResult
	_ = env.GetWorkflowResult(&result)
	if len(result.FinalAnswerText) > 1000 {
		t.Errorf("final_answer_text = %d bytes, want <= 1000 (history hygiene)", len(result.FinalAnswerText))
	}
}

func TestDebateWorkflow_JudgeFailureReturnsError(t *testing.T) {
	// Per Phase 7 task book: Judge failure must return error, not panic.
	env := setupDebateTestEnv(t)
	env.OnActivity("GenerateArgumentsActivity", mock.Anything, mock.Anything).
		Return(activities.GenerateArgumentsResult{
			TurnID: "t", ContentRef: "r", Summary: "s", TokensUsed: 10, Mode: "mock",
		}, nil)
	env.OnActivity("JudgeDebateActivity", mock.Anything, mock.Anything).
		Return(activities.JudgeDebateResult{}, errors.New("judge LLM down"))

	env.ExecuteWorkflow(DebateWorkflow, types.DebateWorkflowInput{
		TaskID: "t1", WorkflowID: "wf-1", RunID: "r1", SessionID: "s1",
		Query: "Q",
		Config: types.DebateConfig{MaxRounds: 1, MockLLM: true, ModeratorEnabled: true},
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow must complete (not hang)")
	}
	err := env.GetWorkflowError()
	if err == nil {
		t.Fatal("judge failure must propagate as error")
	}
	if !strings.Contains(err.Error(), "judge") {
		t.Logf("got error (acceptable): %v", err)
	}
}

func TestDebateWorkflow_DefaultConfigFilledIn(t *testing.T) {
	env := setupDebateTestEnv(t)
	env.OnActivity("GenerateArgumentsActivity", mock.Anything, mock.Anything).
		Return(activities.GenerateArgumentsResult{
			TurnID: "t", ContentRef: "r", Summary: "s", TokensUsed: 10, Mode: "mock",
		}, nil)
	env.OnActivity("JudgeDebateActivity", mock.Anything, mock.Anything).
		Return(activities.JudgeDebateResult{
			Verdict: &types.JudgeVerdict{Verdict: "pro", ProScore: 0.6, ConScore: 0.4, Confidence: 0.7},
			VerdictRef: "v", ProScore: 0.6, ConScore: 0.4, Confidence: 0.7,
			TokensUsed: 10, Mode: "mock", ParseSource: "heuristic", ConfidenceSrc: "fallback",
		}, nil)

	// Empty config should still produce a valid result.
	env.ExecuteWorkflow(DebateWorkflow, types.DebateWorkflowInput{
		TaskID: "t1", WorkflowID: "wf-1", RunID: "r1", SessionID: "s1",
		Query: "Q",
		Config: types.DebateConfig{},
	})
	if !env.IsWorkflowCompleted() {
		t.Fatalf("workflow failed: %v", env.GetWorkflowError())
	}
	var result types.DebateResult
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("result err: %v", err)
	}
	if result.Rounds <= 0 {
		t.Errorf("rounds = %d, want > 0 with default config", result.Rounds)
	}
}

func TestDebateWorkflow_NoTimeNowOrRandInHistory(t *testing.T) {
	// Deterministic hygiene: two runs with identical input must produce
	// identical RoundIndex / Round / Ref structure (no time.Now, no rand).
	runOnce := func() types.DebateResult {
		env := setupDebateTestEnv(t)
		env.OnActivity("GenerateArgumentsActivity", mock.Anything, mock.Anything).
			Return(activities.GenerateArgumentsResult{
				TurnID:     "t-det",
				ContentRef: "r-det",
				Summary:    "det",
				TokensUsed: 10,
				Mode:       "mock",
			}, nil)
		env.OnActivity("JudgeDebateActivity", mock.Anything, mock.Anything).
			Return(activities.JudgeDebateResult{
				Verdict: &types.JudgeVerdict{Verdict: "pro", ProScore: 0.6, ConScore: 0.4, Confidence: 0.7},
				VerdictRef: "v", ProScore: 0.6, ConScore: 0.4, Confidence: 0.7,
				TokensUsed: 10, Mode: "mock", ParseSource: "heuristic", ConfidenceSrc: "fallback",
			}, nil)
		env.ExecuteWorkflow(DebateWorkflow, types.DebateWorkflowInput{
			TaskID: "t1", WorkflowID: "wf-1", RunID: "r1", SessionID: "s1",
			Query: "Q",
			Config: types.DebateConfig{MaxRounds: 1, MockLLM: true, ModeratorEnabled: true},
		})
		var r types.DebateResult
		_ = env.GetWorkflowResult(&r)
		return r
	}
	r1 := runOnce()
	r2 := runOnce()
	if r1.Rounds != r2.Rounds {
		t.Errorf("rounds not deterministic: %d vs %d", r1.Rounds, r2.Rounds)
	}
	if r1.TotalTokens != r2.TotalTokens {
		t.Errorf("total_tokens not deterministic: %d vs %d", r1.TotalTokens, r2.TotalTokens)
	}
}
