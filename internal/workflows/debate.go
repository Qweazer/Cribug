package workflows

import (
	"fmt"
	"time"

	"cribug/internal/activities"
	"cribug/internal/types"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const DebateWorkflowName = "DebateWorkflow"

// DebateWorkflow implements a structured Pro/Con/Judge debate loop.
//
// Constraints (per Phase 7 task book):
//   - Workflow is deterministic: no time.Now()/rand/goroutines/direct IO.
//   - All LLM calls, Workspace writes and DB IOs go through Activities.
//   - Pro/Con arguments and Judge verdicts are stored via Workspace refs;
//     Workflow history stores only short summaries + refs + scores.
//   - Debate rounds are bounded by Config.MaxRounds.
//   - Judge cannot be hard-coded to a winner: it must read the transcript
//     and produce a parse-source-tagged verdict.
func DebateWorkflow(ctx workflow.Context, input types.DebateWorkflowInput) (*types.DebateResult, error) {
	workflowID := workflow.GetInfo(ctx).WorkflowExecution.ID
	logger := workflow.GetLogger(ctx)

	cfg := input.Config
	if cfg.MaxRounds <= 0 {
		cfg.MaxRounds = 3
	}
	if cfg.MaxRounds > 10 {
		// Safety cap: debate must be bounded.
		cfg.MaxRounds = 10
	}
	if cfg.RoundTimeoutSecs <= 0 {
		cfg.RoundTimeoutSecs = 60
	}
	if cfg.NumDebaters <= 0 {
		cfg.NumDebaters = 2
	}
	if len(cfg.Perspectives) == 0 {
		cfg.Perspectives = []string{types.DebatePositionPro, types.DebatePositionCon}
	}

	workspaceTopic := fmt.Sprintf("debate:%s:transcript", workflowID[:min8(len(workflowID))])
	if len(workflowID) < 8 {
		workspaceTopic = fmt.Sprintf("debate:%s:transcript", workflowID)
	}

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: time.Duration(cfg.RoundTimeoutSecs) * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    1 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    10 * time.Second,
			MaximumAttempts:    2,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	// Resolve effective LLM config (so mock vs real follows the project profile).
	var llmCfg activities.LLMConfigSnapshot
	err := workflow.ExecuteActivity(ctx, "ResolveEffectiveLLMConfigActivity",
		activities.ResolveEffectiveLLMConfigInput{},
	).Get(ctx, &llmCfg)
	if err != nil || llmCfg.ChatModel == "" {
		logger.Warn("ResolveEffectiveLLMConfig failed, using safe default", "error", err)
		llmCfg = activities.LLMConfigSnapshot{
			Provider: "openai_compatible", ChatModel: "gpt-4o-mini",
			BaseURL: "http://127.0.0.1:8000",
		}
	}
	effectiveModel := llmCfg.ChatModel

	// Mock vs real is decided at the Activity level, but the Workflow still
	// records the effective provider/model/mode in the result.
	usedMode := "unknown"
	if cfg.MockLLM {
		usedMode = "mock"
	}
	totalTokens := 0
	llmCalls := 0
	var rounds []types.DebateRound
	var lastProRef, lastConRef string
	var lastProSummary, lastConSummary string
	var finalVerdictRef string
	var finalPosition = types.DebateVerdictTie
	var consensusReached bool
	var parseSource, confSource string
	winningTurnRef := ""

	// If a profile requires real LLM, force MockLLM=false here so the
	// Activities actually call the LLM.
	mockLLM := cfg.MockLLM
	if llmCfg.RequireReal {
		mockLLM = false
	}

	for round := 0; round < cfg.MaxRounds; round++ {
		// ── Step 1: Pro Agent generates argument ──
		var proResult activities.GenerateArgumentsResult
		errPro := workflow.ExecuteActivity(ctx, "GenerateArgumentsActivity",
			activities.GenerateArgumentsInput{
				Position:        types.DebatePositionPro,
				Query:           input.Query,
				RoundIndex:      round,
				PreviousTurnRef: lastConRef,
				PreviousSummary: lastConSummary,
				Model:           effectiveModel,
				MockLLM:         mockLLM,
				TaskID:          input.TaskID,
				WorkflowID:      input.WorkflowID,
				RunID:           input.RunID,
			},
		).Get(ctx, &proResult)
		if errPro != nil {
			// Pro failure: record error, continue with a placeholder.
			logger.Warn("Pro agent failed", "round", round, "error", errPro)
			proResult = activities.GenerateArgumentsResult{
				TurnID:     fmt.Sprintf("turn-pro-r%d", round),
				ContentRef: fmt.Sprintf("debate:%s:turn:pro-r%d:error", workflowID, round),
				Summary:    fmt.Sprintf("pro_error_round_%d", round),
				Score:      0,
				Confidence: 0,
				TokensUsed: 0,
				Mode:       "error",
			}
		} else {
			totalTokens += proResult.TokensUsed
			llmCalls++
			if proResult.Mode != "" {
				usedMode = proResult.Mode
			}
		}
		lastProRef = proResult.ContentRef
		lastProSummary = proResult.Summary

		// ── Step 2: Con Agent generates argument ──
		var conResult activities.GenerateArgumentsResult
		errCon := workflow.ExecuteActivity(ctx, "GenerateArgumentsActivity",
			activities.GenerateArgumentsInput{
				Position:        types.DebatePositionCon,
				Query:           input.Query,
				RoundIndex:      round,
				PreviousTurnRef: proResult.ContentRef,
				PreviousSummary: proResult.Summary,
				Model:           effectiveModel,
				MockLLM:         mockLLM,
				TaskID:          input.TaskID,
				WorkflowID:      input.WorkflowID,
				RunID:           input.RunID,
			},
		).Get(ctx, &conResult)
		if errCon != nil {
			logger.Warn("Con agent failed", "round", round, "error", errCon)
			conResult = activities.GenerateArgumentsResult{
				TurnID:     fmt.Sprintf("turn-con-r%d", round),
				ContentRef: fmt.Sprintf("debate:%s:turn:con-r%d:error", workflowID, round),
				Summary:    fmt.Sprintf("con_error_round_%d", round),
				Score:      0,
				Confidence: 0,
				TokensUsed: 0,
				Mode:       "error",
			}
		} else {
			totalTokens += conResult.TokensUsed
			llmCalls++
			if conResult.Mode != "" {
				usedMode = conResult.Mode
			}
		}
		lastConRef = conResult.ContentRef
		lastConSummary = conResult.Summary

		// ── Step 3: Judge scoring (if Moderator enabled) ──
		var judgeVerdict *types.JudgeVerdict
		if cfg.ModeratorEnabled {
			var judgeResult activities.JudgeDebateResult
			errJudge := workflow.ExecuteActivity(ctx, "JudgeDebateActivity",
				activities.JudgeDebateInput{
					Query:         input.Query,
					TranscriptRef: workspaceTopic,
					RoundIndex:    round,
					Model:         effectiveModel,
					MockLLM:       mockLLM,
					TaskID:        input.TaskID,
					WorkflowID:    input.WorkflowID,
					RunID:         input.RunID,
				},
			).Get(ctx, &judgeResult)
			if errJudge == nil {
				totalTokens += judgeResult.TokensUsed
				llmCalls++
				if judgeResult.Mode != "" {
					usedMode = judgeResult.Mode
				}
				judgeVerdict = judgeResult.Verdict
				finalVerdictRef = judgeResult.VerdictRef
				parseSource = judgeResult.ParseSource
				confSource = judgeResult.ConfidenceSrc
				if judgeVerdict != nil {
					if judgeVerdict.Verdict == types.DebateVerdictPro {
						winningTurnRef = proResult.ContentRef
					} else if judgeVerdict.Verdict == types.DebateVerdictCon {
						winningTurnRef = conResult.ContentRef
					}
				}
			} else {
				logger.Warn("Judge failed", "round", round, "error", errJudge)
				// Judge failure is non-fatal: the loop continues; final
				// judge below will retry.
			}
		}

		rounds = append(rounds, types.DebateRound{
			RoundIndex:   round,
			ProTurnRef:   proResult.ContentRef,
			ConTurnRef:   conResult.ContentRef,
			ProScore:     proResult.Score,
			ConScore:     conResult.Score,
			JudgeScore:   scoreFromVerdict(judgeVerdict),
			JudgeVerdict: verdictString(judgeVerdict),
			TokensUsed:   proResult.TokensUsed + conResult.TokensUsed,
		})

		// ── Step 4: Check early consensus (if RequireConsensus) ──
		if cfg.RequireConsensus && judgeVerdict != nil && judgeVerdict.Verdict != types.DebateVerdictTie {
			finalPosition = judgeVerdict.Verdict
			consensusReached = true
			break
		}
	}

	// ── Step 5: Final judge if no consensus yet OR Moderator disabled during loop ──
	if !consensusReached {
		var finalJudge activities.JudgeDebateResult
		errFinal := workflow.ExecuteActivity(ctx, "JudgeDebateActivity",
			activities.JudgeDebateInput{
				Query:         input.Query,
				TranscriptRef: workspaceTopic,
				RoundIndex:    len(rounds),
				Model:         effectiveModel,
				MockLLM:       mockLLM,
				TaskID:        input.TaskID,
				WorkflowID:    input.WorkflowID,
				RunID:         input.RunID,
			},
		).Get(ctx, &finalJudge)
		if errFinal != nil {
			// Judge failure: return explicit error per Phase 7 task book.
			return &types.DebateResult{
				TranscriptRef: workspaceTopic,
				Rounds:        len(rounds),
				TotalTokens:   totalTokens,
				LLMCalls:      llmCalls,
				Mode:          usedMode,
				Mock:          usedMode == "mock",
				Provider:      llmCfg.Provider,
				ModelUsed:     effectiveModel,
			}, fmt.Errorf("final judge failed: %w", errFinal)
		}
		totalTokens += finalJudge.TokensUsed
		llmCalls++
		if finalJudge.Mode != "" {
			usedMode = finalJudge.Mode
		}
		if finalJudge.Verdict != nil {
			finalPosition = finalJudge.Verdict.Verdict
			finalVerdictRef = finalJudge.VerdictRef
			parseSource = finalJudge.ParseSource
			confSource = finalJudge.ConfidenceSrc
			if finalPosition == types.DebateVerdictPro {
				winningTurnRef = lastProRef
			} else if finalPosition == types.DebateVerdictCon {
				winningTurnRef = lastConRef
			}
		}
		consensusReached = finalPosition != types.DebateVerdictTie
	}

	if usedMode == "" || usedMode == "unknown" {
		usedMode = "mock"
	}

	// ── Step 6: Audit (non-fatal) ──
	_ = workflow.ExecuteActivity(ctx, "AuditDebateActivity",
		activities.AuditDebateInput{
			WorkflowID:     input.WorkflowID,
			Query:          input.Query,
			FinalPosition:  finalPosition,
			Consensus:      consensusReached,
			TotalRounds:    len(rounds),
			TotalTokens:    totalTokens,
			VerdictRef:     finalVerdictRef,
			TranscriptRef:  workspaceTopic,
			WinningTurnRef: winningTurnRef,
		},
	).Get(ctx, nil)

	finalAnswerText := buildFinalAnswerText(finalPosition, lastProSummary, lastConSummary)
	finalAnswerRef := fmt.Sprintf("debate:%s:final:%s", workflowID, finalPosition)

	return &types.DebateResult{
		FinalPosition:    finalPosition,
		FinalAnswerRef:   finalAnswerRef,
		FinalAnswerText:  truncate(finalAnswerText, 500),
		TranscriptRef:    workspaceTopic,
		VerdictRef:       finalVerdictRef,
		ConsensusReached: consensusReached,
		TotalTokens:      totalTokens,
		Rounds:           len(rounds),
		WinningTurnRef:   winningTurnRef,
		WorkspaceTopic:   workspaceTopic,
		Provider:         llmCfg.Provider,
		ModelUsed:        effectiveModel,
		Mode:             usedMode,
		Mock:             usedMode == "mock",
		LLMCalls:         llmCalls,
		JudgeParseSource: parseSource,
		ConfidenceSource: confSource,
	}, nil
}

func buildFinalAnswerText(position, proSummary, conSummary string) string {
	switch position {
	case types.DebateVerdictPro:
		return "Verdict: PRO. The Pro side presented stronger arguments. Pro summary: " + proSummary
	case types.DebateVerdictCon:
		return "Verdict: CON. The Con side presented stronger arguments. Con summary: " + conSummary
	default:
		return "Verdict: TIE. Both sides presented comparable arguments. Pro: " + proSummary + " | Con: " + conSummary
	}
}

func scoreFromVerdict(v *types.JudgeVerdict) float64 {
	if v == nil {
		return 0
	}
	if v.ProScore >= v.ConScore {
		return v.ProScore
	}
	return v.ConScore
}

func verdictString(v *types.JudgeVerdict) string {
	if v == nil {
		return ""
	}
	return v.Verdict
}

func min8(n int) int {
	if n < 8 {
		return n
	}
	return 8
}
