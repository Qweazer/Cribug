package workflows

import (
	"fmt"
	"time"

	"cribug/internal/activities"
	"cribug/internal/types"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const ReflectionWorkflowName = "ReflectionWorkflow"

// ReflectionWorkflow implements the Phase 7C generate-evaluate-revise loop.
// Model/provider resolved via Activity from active LLM config — no hardcoded model.
func ReflectionWorkflow(ctx workflow.Context, input types.ReflectionRequest) (*types.ReflectionResult, error) {
	workflowID := workflow.GetInfo(ctx).WorkflowExecution.ID
	runID := workflow.GetInfo(ctx).WorkflowExecution.RunID
	logger := workflow.GetLogger(ctx)

	cfg := input.Config
	if cfg.MaxIterations <= 0 {
		cfg.MaxIterations = 2
	}
	if cfg.MinScoreThreshold <= 0 {
		cfg.MinScoreThreshold = 0.75
	}
	if cfg.Temperature <= 0 {
		cfg.Temperature = 0.7
	}
	if cfg.MaxCompletionTokens <= 0 {
		cfg.MaxCompletionTokens = 1024
	}
	if len(cfg.EvaluationCriteria) == 0 {
		cfg.EvaluationCriteria = []string{"clarity", "accuracy", "completeness"}
	}

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 90 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    1 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    10 * time.Second,
			MaximumAttempts:    2,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	// Step 0: Resolve effective LLM config via Activity (no file/env access in workflow)
	var llmCfg activities.LLMConfigSnapshot
	err := workflow.ExecuteActivity(ctx, "ResolveEffectiveLLMConfigActivity",
		activities.ResolveEffectiveLLMConfigInput{},
	).Get(ctx, &llmCfg)
	if err != nil || llmCfg.ChatModel == "" {
		logger.Warn("ResolveEffectiveLLMConfig failed, using safe default", "error", err)
		llmCfg = activities.LLMConfigSnapshot{
			Provider:  "openai_compatible",
			ChatModel: "gpt-4o-mini",
			BaseURL:   "http://127.0.0.1:8000",
		}
	}
	effectiveModel := llmCfg.ChatModel

	// Step 1: Generate initial draft
	var draftResult activities.GenerateInitialDraftResult
	err = workflow.ExecuteActivity(ctx, "GenerateInitialDraftActivity",
		activities.GenerateInitialDraftInput{
			TaskID:              input.TaskID,
			WorkflowID:          workflowID,
			RunID:               runID,
			Query:               input.Query,
			Context:             input.Context,
			MockLLM:             cfg.MockLLM,
			Model:               effectiveModel,
			Temperature:         cfg.Temperature,
			MaxCompletionTokens: cfg.MaxCompletionTokens,
		},
	).Get(ctx, &draftResult)
	if err != nil {
		return &types.ReflectionResult{
			TaskID: input.TaskID, WorkflowID: workflowID,
			Status:             types.ReflectionStatusError,
			FinalAnswerSummary: fmt.Sprintf("generate draft failed: %v", err),
			Provider:           llmCfg.Provider,
			ModelUsed:          effectiveModel,
		}, nil
	}

	_ = workflow.ExecuteActivity(ctx, "EmitReflectionEventActivity",
		activities.EmitReflectionEventInput{WorkflowID: workflowID, EventType: "REFLECTION_STARTED"},
	).Get(ctx, nil)

	currentDraft := draftResult.DraftText
	currentDraftRef := draftResult.DraftRef
	currentDraftSummary := draftResult.DraftSummary
	totalTokens := draftResult.TokensUsed
	usedMode := draftResult.Mode
	hadFallback := draftResult.FallbackUsed
	iterations := make([]types.ReflectionIteration, 0)

	for i := 0; i < cfg.MaxIterations; i++ {
		var evalResult activities.EvaluateDraftResult
		err = workflow.ExecuteActivity(ctx, "EvaluateDraftActivity",
			activities.EvaluateDraftInput{
				TaskID: input.TaskID, WorkflowID: workflowID, RunID: runID,
				Query: input.Query, DraftText: currentDraft,
				Criteria: cfg.EvaluationCriteria, MockLLM: cfg.MockLLM,
				Model: effectiveModel,
			},
		).Get(ctx, &evalResult)
		if err != nil {
			logger.Warn("EvaluateDraft failed", "error", err)
			break
		}
		totalTokens += evalResult.TokensUsed
		if evalResult.Mode != "" {
			usedMode = evalResult.Mode
		}
		if evalResult.FallbackUsed {
			hadFallback = true
		}

		iter := types.ReflectionIteration{
			Iteration: i + 1, DraftRef: currentDraftRef, DraftSummary: currentDraftSummary,
			Score: evalResult.Score, CritiqueRef: evalResult.CritiqueRef,
			CritiqueSummary: evalResult.CritiqueSummary, TokensUsed: evalResult.TokensUsed,
		}

		_ = workflow.ExecuteActivity(ctx, "EmitReflectionEventActivity",
			activities.EmitReflectionEventInput{
				WorkflowID: workflowID, EventType: "REFLECTION_ITERATION",
				Iteration: i + 1, Score: evalResult.Score,
			},
		).Get(ctx, nil)

		if evalResult.Score >= cfg.MinScoreThreshold {
			iterations = append(iterations, iter)
			break
		}

		var reviseResult activities.ReviseDraftResult
		err = workflow.ExecuteActivity(ctx, "ReviseDraftActivity",
			activities.ReviseDraftInput{
				TaskID: input.TaskID, WorkflowID: workflowID, RunID: runID,
				Query: input.Query, DraftText: currentDraft, CritiqueText: evalResult.CritiqueText,
				MockLLM: cfg.MockLLM, Model: effectiveModel,
				Temperature: cfg.Temperature, MaxCompletionTokens: cfg.MaxCompletionTokens,
			},
		).Get(ctx, &reviseResult)
		if err != nil {
			logger.Warn("ReviseDraft failed", "error", err)
			iterations = append(iterations, iter)
			break
		}
		totalTokens += reviseResult.TokensUsed
		if reviseResult.Mode != "" {
			usedMode = reviseResult.Mode
		}
		if reviseResult.FallbackUsed {
			hadFallback = true
		}

		iter.RevisedRef = reviseResult.RevisedRef
		iter.RevisedSummary = reviseResult.RevisedSummary
		iterations = append(iterations, iter)

		currentDraft = reviseResult.RevisedText
		currentDraftRef = reviseResult.RevisedRef
		currentDraftSummary = reviseResult.RevisedSummary
	}

	finalScore := 0.0
	status := types.ReflectionStatusOK
	if len(iterations) > 0 {
		finalScore = iterations[len(iterations)-1].Score
		if finalScore < cfg.MinScoreThreshold {
			status = types.ReflectionStatusMaxIterations
		}
	}
	if usedMode == "" {
		usedMode = "unknown"
	}

	_ = workflow.ExecuteActivity(ctx, "AuditReflectionActivity",
		activities.AuditReflectionInput{
			WorkflowID: workflowID, Query: input.Query, FinalScore: finalScore,
			Iterations: len(iterations), TotalTokens: totalTokens, Status: status,
		},
	).Get(ctx, nil)

	_ = workflow.ExecuteActivity(ctx, "EmitReflectionEventActivity",
		activities.EmitReflectionEventInput{
			WorkflowID: workflowID, EventType: "REFLECTION_COMPLETED", Score: finalScore,
		},
	).Get(ctx, nil)

	auditSummary := fmt.Sprintf("reflection: query=%q iterations=%d final_score=%.2f tokens=%d status=%s provider=%s model=%s mode=%s",
		truncateStr(input.Query, 100), len(iterations), finalScore, totalTokens, status,
		llmCfg.Provider, effectiveModel, usedMode)

	return &types.ReflectionResult{
		TaskID: input.TaskID, WorkflowID: workflowID,
		FinalAnswerRef: currentDraftRef, FinalAnswerSummary: truncateStr(currentDraftSummary, 500),
		FinalScore: finalScore, TotalIterations: len(iterations), TotalTokens: totalTokens,
		Status: status, Iterations: iterations, AuditSummary: auditSummary,
		Provider: llmCfg.Provider, ModelUsed: effectiveModel,
		Mode: usedMode, FallbackUsed: hadFallback,
	}, nil
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen { return s }
	return s[:maxLen]
}
