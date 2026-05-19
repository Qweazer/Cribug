package workflows

import (
	"fmt"
	"strings"

	"cribug/internal/activities"
	"cribug/internal/events"
	"cribug/internal/types"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const MultiAgentWorkflowName = "MultiAgentWorkflow"

type MultiAgentWorkflow struct{}

func NewMultiAgentWorkflow() *MultiAgentWorkflow {
	return &MultiAgentWorkflow{}
}

func (mw *MultiAgentWorkflow) Execute(ctx workflow.Context, req types.WorkflowTaskRequest) (*types.WorkflowTaskResult, error) {
	logger := workflow.GetLogger(ctx)
	logger.Info("MultiAgentWorkflow started", "task_id", req.TaskID, "workflow_id", req.WorkflowID)

	opts := workflow.ActivityOptions{
		StartToCloseTimeout: 90 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    1 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    10 * time.Second,
			MaximumAttempts:    3,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, opts)

	// 1. Emit WORKFLOW_STARTED
	err := workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
		TaskID: req.TaskID,
		Event:  events.NewWorkflowStartedEvent(req.TaskID, req.WorkflowID),
	}).Get(ctx, nil)
	if err != nil {
		logger.Error("EmitEventActivity (WORKFLOW_STARTED) failed", "error", err)
	}

	// 2. LoadSessionActivity
	var sessionOutput *activities.LoadSessionOutput
	err = workflow.ExecuteActivity(ctx, "LoadSessionActivity", activities.LoadSessionInput{
		TaskID:    req.TaskID,
		SessionID: req.SessionID,
	}).Get(ctx, &sessionOutput)
	if err != nil {
		logger.Error("LoadSessionActivity failed", "error", err)
	}

	// 3. Emit SESSION_LOADED
	workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
		TaskID: req.TaskID,
		Event:  events.NewSessionLoadedEvent(req.TaskID, sessionOutput.MessageCount),
	}).Get(ctx, nil)

	// Track outputs from each agent
	var plannerOutput string

	// 4. Execute planner (mock)
	logger.Info("Executing planner (mock)")
	err = workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
		TaskID: req.TaskID,
		Event:  events.NewAgentStartedEvent(req.TaskID, string(types.AgentRolePlanner), 1),
	}).Get(ctx, nil)
	if err != nil {
		logger.Error("EmitEventActivity (AGENT_STARTED planner) failed", "error", err)
	}

	var plannerAgentOutput *types.RunAgentActivityOutput
	err = workflow.ExecuteActivity(ctx, "RunAgentActivity", types.RunAgentActivityInput{
		TaskID: req.TaskID,
		Role:   types.AgentRolePlanner,
		Query:  req.Query,
	}).Get(ctx, &plannerAgentOutput)
	if err != nil {
		logger.Error("RunAgentActivity (planner) failed", "error", err)
		mw.emitFailure(ctx, req, err, "planner")
		return nil, err
	}
	plannerOutput = plannerAgentOutput.Step.Output

	err = workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
		TaskID: req.TaskID,
		Event:  events.NewAgentCompletedEvent(req.TaskID, string(types.AgentRolePlanner), "completed", plannerOutput),
	}).Get(ctx, nil)
	if err != nil {
		logger.Error("EmitEventActivity (AGENT_COMPLETED planner) failed", "error", err)
	}
	logger.Info("Planner completed", "output_length", len(plannerOutput))

	// 5. Execute researcher (LLM-backed) with budget check
	logger.Info("Executing researcher (LLM-backed)")

	// Check for forced failure trigger
	if req.Query != "" && containsForceFailure(req.Query) {
		logger.Warn("Forced failure triggered")
		workflow.ExecuteActivity(ctx, "SaveFailureActivity", activities.SaveFailureInput{
			TaskID:    req.TaskID,
			ErrorType: types.ErrorTypeWorkflow,
			ErrorMsg:  "forced multi-agent failure",
		}).Get(ctx, nil)

		workflow.ExecuteActivity(ctx, "RecordExecutionFailedActivity", activities.RecordExecutionFailedInput{
			TaskID:     req.TaskID,
			WorkflowID: req.WorkflowID,
			RunID:      req.RunID,
			ErrorType:  types.ErrorTypeWorkflow,
			ErrorMsg:   "forced multi-agent failure",
		}).Get(ctx, nil)

		workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
			TaskID: req.TaskID,
			Event:  events.NewTaskFailedEvent(req.TaskID, req.WorkflowID, "forced multi-agent failure"),
		}).Get(ctx, nil)

		return nil, fmt.Errorf("forced failure")
	}

	// Estimate prompt tokens for researcher
	samplePrompt := req.Query + " " + plannerOutput
	var researcherEstOutput *activities.EstimatePromptTokensOutput
	err = workflow.ExecuteActivity(ctx, "EstimatePromptTokensActivity", activities.EstimatePromptTokensInput{
		Model: req.Model,
		Text:  samplePrompt,
	}).Get(ctx, &researcherEstOutput)
	if err != nil {
		logger.Warn("EstimatePromptTokensActivity failed", "error", err)
	}

	// Check budget for researcher
	var researcherBudgetOutput *activities.CheckBudgetOutput
	if researcherEstOutput != nil {
		err = workflow.ExecuteActivity(ctx, "CheckBudgetActivity", activities.CheckBudgetInput{
			EstimatedPromptTokens: researcherEstOutput.EstimatedPromptTokens,
			MaxTotalTokens:        req.MaxTotalTokens,
			MaxCompletionTokens:   req.MaxCompletionTokens,
		}).Get(ctx, &researcherBudgetOutput)

		if err != nil {
			logger.Warn("CheckBudgetActivity failed", "error", err)
		} else if !researcherBudgetOutput.Allowed {
			logger.Warn("Budget exceeded for researcher", "reason", researcherBudgetOutput.Reason)
			workflow.ExecuteActivity(ctx, "SaveFailureActivity", activities.SaveFailureInput{
				TaskID:    req.TaskID,
				ErrorType: types.TaskStatusBudgetExceeded,
				ErrorMsg:  researcherBudgetOutput.Reason,
			}).Get(ctx, nil)

			workflow.ExecuteActivity(ctx, "RecordExecutionFailedActivity", activities.RecordExecutionFailedInput{
				TaskID:     req.TaskID,
				WorkflowID: req.WorkflowID,
				RunID:      req.RunID,
				ErrorType:  types.TaskStatusBudgetExceeded,
				ErrorMsg:   researcherBudgetOutput.Reason,
			}).Get(ctx, nil)

			workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
				TaskID: req.TaskID,
				Event:  events.NewTaskBudgetExceededEvent(req.TaskID, researcherBudgetOutput.Reason, researcherEstOutput.EstimatedPromptTokens, req.MaxTotalTokens),
			}).Get(ctx, nil)

			return &types.WorkflowTaskResult{
				TaskID: req.TaskID,
				Status: types.TaskStatusBudgetExceeded,
				Answer: "",
				Error:  researcherBudgetOutput.Reason,
			}, nil
		}
	}

	// Emit AGENT_STARTED for researcher
	err = workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
		TaskID: req.TaskID,
		Event:  events.NewAgentStartedEvent(req.TaskID, string(types.AgentRoleResearcher), 2),
	}).Get(ctx, nil)
	if err != nil {
		logger.Error("EmitEventActivity (AGENT_STARTED researcher) failed", "error", err)
	}

	// Emit LLM_STARTED - only after budget check passes
	workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
		TaskID: req.TaskID,
		Event:  events.NewLLMStartedEvent(req.TaskID, req.Model),
	}).Get(ctx, nil)

	// Run researcher LLM call
	var researcherOutput *types.RunResearcherAgentOutput
	err = workflow.ExecuteActivity(ctx, "RunResearcherAgentActivity", types.RunResearcherAgentInput{
		TaskID:              req.TaskID,
		WorkflowID:          req.WorkflowID,
		RunID:               req.RunID,
		Query:               req.Query,
		Model:               req.Model,
		Temperature:         req.Temperature,
		MaxCompletionTokens: researcherBudgetOutput.AllowedCompletionTokens,
		PlannerOutput:       plannerOutput,
	}).Get(ctx, &researcherOutput)

	finishReason := "stop"
	if err != nil {
		finishReason = "error"
		logger.Error("RunResearcherAgentActivity failed", "error", err)
		mw.emitFailure(ctx, req, err, "researcher")
		return nil, err
	}

	// Emit LLM_COMPLETED for researcher
	workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
		TaskID: req.TaskID,
		Event:  events.NewLLMCompletedEvent(req.TaskID, req.Model, finishReason, researcherOutput.LatencyMS),
	}).Get(ctx, nil)

	// Record researcher usage to llm_calls
	workflow.ExecuteActivity(ctx, "RecordUsageActivity", activities.RecordUsageInput{
		TaskID:                req.TaskID,
		WorkflowID:           req.WorkflowID,
		RunID:                 req.RunID,
		Provider:              "openai_compatible",
		Model:                 req.Model,
		EstimatedPromptTokens: 0,
		MaxCompletionTokens:   researcherBudgetOutput.AllowedCompletionTokens,
		PromptTokens:          researcherOutput.PromptTokens,
		CompletionTokens:      researcherOutput.CompletionTokens,
		TotalTokens:           researcherOutput.TotalTokens,
		LatencyMS:             researcherOutput.LatencyMS,
		FinishReason:          finishReason,
		AgentRole:             string(types.AgentRoleResearcher),
	}).Get(ctx, nil)

	// Emit USAGE_RECORDED for researcher
	workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
		TaskID: req.TaskID,
		Event:  events.NewUsageRecordedEvent(req.TaskID, researcherOutput.TotalTokens),
	}).Get(ctx, nil)

	// Emit AGENT_COMPLETED for researcher
	err = workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
		TaskID: req.TaskID,
		Event:  events.NewAgentCompletedEvent(req.TaskID, string(types.AgentRoleResearcher), "completed", researcherOutput.LLMOutput),
	}).Get(ctx, nil)
	if err != nil {
		logger.Error("EmitEventActivity (AGENT_COMPLETED researcher) failed", "error", err)
	}

	logger.Info("Researcher completed", "tokens", researcherOutput.TotalTokens)

	// Track accumulated tokens for budget check
	accumulatedTokens := researcherOutput.TotalTokens

	// 6. Execute critic (LLM-backed) with budget check considering researcher usage
	logger.Info("Executing critic (LLM-backed)")

	// Estimate prompt tokens for critic (including researcher output)
	samplePrompt = req.Query + " " + plannerOutput + " " + researcherOutput.LLMOutput
	var criticEstOutput *activities.EstimatePromptTokensOutput
	err = workflow.ExecuteActivity(ctx, "EstimatePromptTokensActivity", activities.EstimatePromptTokensInput{
		Model: req.Model,
		Text:  samplePrompt,
	}).Get(ctx, &criticEstOutput)
	if err != nil {
		logger.Warn("EstimatePromptTokensActivity failed for critic", "error", err)
	}

	// Check budget for critic (considering researcher usage)
	var criticBudgetOutput *activities.CheckBudgetOutput
	if criticEstOutput != nil {
		// Calculate remaining budget after researcher
		remainingBudget := req.MaxTotalTokens - accumulatedTokens
		criticAllowed := min(criticEstOutput.EstimatedPromptTokens, remainingBudget)
		if criticAllowed > req.MaxCompletionTokens {
			criticAllowed = req.MaxCompletionTokens
		}

		if criticEstOutput.EstimatedPromptTokens > remainingBudget {
			logger.Warn("Budget exceeded for critic", "remaining", remainingBudget, "estimated", criticEstOutput.EstimatedPromptTokens)
			workflow.ExecuteActivity(ctx, "SaveFailureActivity", activities.SaveFailureInput{
				TaskID:    req.TaskID,
				ErrorType: types.TaskStatusBudgetExceeded,
				ErrorMsg:  "budget exceeded after researcher",
			}).Get(ctx, nil)

			workflow.ExecuteActivity(ctx, "RecordExecutionFailedActivity", activities.RecordExecutionFailedInput{
				TaskID:     req.TaskID,
				WorkflowID: req.WorkflowID,
				RunID:      req.RunID,
				ErrorType:  types.TaskStatusBudgetExceeded,
				ErrorMsg:   "budget exceeded after researcher",
			}).Get(ctx, nil)

			workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
				TaskID: req.TaskID,
				Event:  events.NewTaskBudgetExceededEvent(req.TaskID, "budget exceeded after researcher", criticEstOutput.EstimatedPromptTokens, req.MaxTotalTokens),
			}).Get(ctx, nil)

			return &types.WorkflowTaskResult{
				TaskID: req.TaskID,
				Status: types.TaskStatusBudgetExceeded,
				Answer: "",
				Error:  "budget exceeded after researcher",
			}, nil
		}

		criticBudgetOutput = &activities.CheckBudgetOutput{
			Allowed:                true,
			AllowedCompletionTokens: criticAllowed,
		}
	}

	// Emit AGENT_STARTED for critic
	err = workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
		TaskID: req.TaskID,
		Event:  events.NewAgentStartedEvent(req.TaskID, string(types.AgentRoleCritic), 3),
	}).Get(ctx, nil)
	if err != nil {
		logger.Error("EmitEventActivity (AGENT_STARTED critic) failed", "error", err)
	}

	// Emit LLM_STARTED for critic
	workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
		TaskID: req.TaskID,
		Event:  events.NewLLMStartedEvent(req.TaskID, req.Model),
	}).Get(ctx, nil)

	// Run critic LLM call
	var criticOutput *types.RunCriticAgentOutput
	err = workflow.ExecuteActivity(ctx, "RunCriticAgentActivity", types.RunCriticAgentInput{
		TaskID:              req.TaskID,
		WorkflowID:          req.WorkflowID,
		RunID:               req.RunID,
		Query:               req.Query,
		Model:               req.Model,
		Temperature:         req.Temperature,
		MaxCompletionTokens: criticBudgetOutput.AllowedCompletionTokens,
		PlannerOutput:       plannerOutput,
		ResearcherOutput:    researcherOutput.LLMOutput,
		CurrentAnswer:       researcherOutput.LLMOutput,
	}).Get(ctx, &criticOutput)

	if err != nil {
		finishReason = "error"
		logger.Error("RunCriticAgentActivity failed", "error", err)
		mw.emitFailure(ctx, req, err, "critic")
		return nil, err
	}

	// Emit LLM_COMPLETED for critic
	workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
		TaskID: req.TaskID,
		Event:  events.NewLLMCompletedEvent(req.TaskID, req.Model, finishReason, criticOutput.LatencyMS),
	}).Get(ctx, nil)

	// Record critic usage to llm_calls
	workflow.ExecuteActivity(ctx, "RecordUsageActivity", activities.RecordUsageInput{
		TaskID:                req.TaskID,
		WorkflowID:           req.WorkflowID,
		RunID:                 req.RunID,
		Provider:              "openai_compatible",
		Model:                 req.Model,
		EstimatedPromptTokens: 0,
		MaxCompletionTokens:   criticBudgetOutput.AllowedCompletionTokens,
		PromptTokens:          criticOutput.PromptTokens,
		CompletionTokens:      criticOutput.CompletionTokens,
		TotalTokens:           criticOutput.TotalTokens,
		LatencyMS:             criticOutput.LatencyMS,
		FinishReason:          finishReason,
		AgentRole:             string(types.AgentRoleCritic),
	}).Get(ctx, nil)

	// Emit USAGE_RECORDED for critic
	workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
		TaskID: req.TaskID,
		Event:  events.NewUsageRecordedEvent(req.TaskID, criticOutput.TotalTokens),
	}).Get(ctx, nil)

	// Emit AGENT_COMPLETED for critic
	err = workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
		TaskID: req.TaskID,
		Event:  events.NewAgentCompletedEvent(req.TaskID, string(types.AgentRoleCritic), "completed", criticOutput.LLMOutput),
	}).Get(ctx, nil)
	if err != nil {
		logger.Error("EmitEventActivity (AGENT_COMPLETED critic) failed", "error", err)
	}

	// Emit CRITIC_REVIEWED
	workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
		TaskID: req.TaskID,
		Event:  events.NewCriticReviewedEvent(req.TaskID, criticOutput.LLMOutput),
	}).Get(ctx, nil)

	logger.Info("Critic completed", "tokens", criticOutput.TotalTokens)

	// Update accumulated tokens
	accumulatedTokens += criticOutput.TotalTokens

	// 7. Execute synthesizer (LLM-backed)
	logger.Info("Executing synthesizer (LLM-backed)")

	// Estimate prompt tokens for synthesizer (including researcher + critic output)
	synthSamplePrompt := req.Query + " " + plannerOutput + " " + researcherOutput.LLMOutput + " " + criticOutput.LLMOutput
	var synthEstOutput *activities.EstimatePromptTokensOutput
	err = workflow.ExecuteActivity(ctx, "EstimatePromptTokensActivity", activities.EstimatePromptTokensInput{
		Model: req.Model,
		Text:  synthSamplePrompt,
	}).Get(ctx, &synthEstOutput)
	if err != nil {
		logger.Warn("EstimatePromptTokensActivity failed for synthesizer", "error", err)
	}

	// Check budget for synthesizer (considering researcher + critic usage)
	var synthBudgetOutput *activities.CheckBudgetOutput
	if synthEstOutput != nil {
		// Calculate remaining budget after researcher + critic
		remainingBudget := req.MaxTotalTokens - accumulatedTokens
		synthAllowed := min(synthEstOutput.EstimatedPromptTokens, remainingBudget)
		if synthAllowed > req.MaxCompletionTokens {
			synthAllowed = req.MaxCompletionTokens
		}

		if synthEstOutput.EstimatedPromptTokens > remainingBudget {
			logger.Warn("Budget exceeded for synthesizer", "remaining", remainingBudget, "estimated", synthEstOutput.EstimatedPromptTokens)
			workflow.ExecuteActivity(ctx, "SaveFailureActivity", activities.SaveFailureInput{
				TaskID:    req.TaskID,
				ErrorType: types.TaskStatusBudgetExceeded,
				ErrorMsg:  "budget exceeded after critic",
			}).Get(ctx, nil)

			workflow.ExecuteActivity(ctx, "RecordExecutionFailedActivity", activities.RecordExecutionFailedInput{
				TaskID:     req.TaskID,
				WorkflowID: req.WorkflowID,
				RunID:      req.RunID,
				ErrorType:  types.TaskStatusBudgetExceeded,
				ErrorMsg:   "budget exceeded after critic",
			}).Get(ctx, nil)

			workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
				TaskID: req.TaskID,
				Event:  events.NewTaskBudgetExceededEvent(req.TaskID, "budget exceeded after critic", synthEstOutput.EstimatedPromptTokens, req.MaxTotalTokens),
			}).Get(ctx, nil)

			return &types.WorkflowTaskResult{
				TaskID: req.TaskID,
				Status: types.TaskStatusBudgetExceeded,
				Answer: "",
				Error:  "budget exceeded after critic",
			}, nil
		}

		synthBudgetOutput = &activities.CheckBudgetOutput{
			Allowed:                true,
			AllowedCompletionTokens: synthAllowed,
		}
	}

	// Emit AGENT_STARTED for synthesizer
	err = workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
		TaskID: req.TaskID,
		Event:  events.NewAgentStartedEvent(req.TaskID, string(types.AgentRoleSynthesizer), 4),
	}).Get(ctx, nil)
	if err != nil {
		logger.Error("EmitEventActivity (AGENT_STARTED synthesizer) failed", "error", err)
	}

	// Emit LLM_STARTED for synthesizer
	workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
		TaskID: req.TaskID,
		Event:  events.NewLLMStartedEvent(req.TaskID, req.Model),
	}).Get(ctx, nil)

	// Run synthesizer LLM call
	var synthesizerOutput *types.RunSynthesizerAgentOutput
	err = workflow.ExecuteActivity(ctx, "RunSynthesizerAgentActivity", types.RunSynthesizerAgentInput{
		TaskID:              req.TaskID,
		WorkflowID:          req.WorkflowID,
		RunID:               req.RunID,
		Query:               req.Query,
		Model:               req.Model,
		Temperature:         req.Temperature,
		MaxCompletionTokens: synthBudgetOutput.AllowedCompletionTokens,
		PlannerOutput:       plannerOutput,
		ResearcherOutput:    researcherOutput.LLMOutput,
		CriticOutput:        criticOutput.LLMOutput,
	}).Get(ctx, &synthesizerOutput)

	if err != nil {
		finishReason = "error"
		workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
			TaskID: req.TaskID,
			Event:  events.NewLLMCompletedEvent(req.TaskID, req.Model, finishReason, 0),
		}).Get(ctx, nil)

		logger.Error("RunSynthesizerAgentActivity failed", "error", err)
		mw.emitFailure(ctx, req, err, "synthesizer")
		return nil, err
	}

	workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
		TaskID: req.TaskID,
		Event:  events.NewLLMCompletedEvent(req.TaskID, req.Model, finishReason, synthesizerOutput.LatencyMS),
	}).Get(ctx, nil)

	// Record synthesizer usage to llm_calls
	workflow.ExecuteActivity(ctx, "RecordUsageActivity", activities.RecordUsageInput{
		TaskID:                req.TaskID,
		WorkflowID:           req.WorkflowID,
		RunID:                 req.RunID,
		Provider:              "openai_compatible",
		Model:                 req.Model,
		EstimatedPromptTokens: 0,
		MaxCompletionTokens:   synthBudgetOutput.AllowedCompletionTokens,
		PromptTokens:          synthesizerOutput.PromptTokens,
		CompletionTokens:      synthesizerOutput.CompletionTokens,
		TotalTokens:           synthesizerOutput.TotalTokens,
		LatencyMS:             synthesizerOutput.LatencyMS,
		FinishReason:          finishReason,
		AgentRole:             string(types.AgentRoleSynthesizer),
	}).Get(ctx, nil)

	// Emit USAGE_RECORDED for synthesizer
	workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
		TaskID: req.TaskID,
		Event:  events.NewUsageRecordedEvent(req.TaskID, synthesizerOutput.TotalTokens),
	}).Get(ctx, nil)

	// Emit AGENT_COMPLETED for synthesizer
	err = workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
		TaskID: req.TaskID,
		Event:  events.NewAgentCompletedEvent(req.TaskID, string(types.AgentRoleSynthesizer), "completed", synthesizerOutput.LLMOutput),
	}).Get(ctx, nil)
	if err != nil {
		logger.Error("EmitEventActivity (AGENT_COMPLETED synthesizer) failed", "error", err)
	}

	logger.Info("Synthesizer completed", "tokens", synthesizerOutput.TotalTokens)

	// Update accumulated tokens
	accumulatedTokens += synthesizerOutput.TotalTokens

	// 8. Emit MULTI_AGENT_SYNTHESIZED
	err = workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
		TaskID: req.TaskID,
		Event:  events.NewMultiAgentSynthesizedEvent(req.TaskID, 4, 4, accumulatedTokens),
	}).Get(ctx, nil)
	if err != nil {
		logger.Error("EmitEventActivity (MULTI_AGENT_SYNTHESIZED) failed", "error", err)
	}

	// 9. SaveResultActivity (with accumulated usage from researcher + critic + synthesizer)
	totalPromptTokens := researcherOutput.PromptTokens + criticOutput.PromptTokens + synthesizerOutput.PromptTokens
	totalCompletionTokens := researcherOutput.CompletionTokens + criticOutput.CompletionTokens + synthesizerOutput.CompletionTokens
	totalUsageTokens := accumulatedTokens

	err = workflow.ExecuteActivity(ctx, "SaveResultActivity", activities.SaveResultInput{
		TaskID:           req.TaskID,
		Result:           synthesizerOutput.LLMOutput,
		PromptTokens:     &totalPromptTokens,
		CompletionTokens: &totalCompletionTokens,
		TotalTokens:      &totalUsageTokens,
	}).Get(ctx, nil)
	if err != nil {
		logger.Error("SaveResultActivity failed", "error", err)
		mw.emitFailure(ctx, req, err, "save_result")
		return nil, err
	}

	// 10. RecordExecutionCompletedActivity
	err = workflow.ExecuteActivity(ctx, "RecordExecutionCompletedActivity", activities.RecordExecutionInput{
		TaskID:     req.TaskID,
		WorkflowID: req.WorkflowID,
		RunID:      req.RunID,
	}).Get(ctx, nil)
	if err != nil {
		logger.Error("RecordExecutionCompletedActivity failed", "error", err)
	}

	// 11. Emit TASK_COMPLETED
	workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
		TaskID: req.TaskID,
		Event:  events.NewTaskCompletedEvent(req.TaskID, req.WorkflowID),
	}).Get(ctx, nil)

	logger.Info("MultiAgentWorkflow completed", "task_id", req.TaskID, "completed_agents", 4, "total_tokens", accumulatedTokens)
	return &types.WorkflowTaskResult{
		TaskID: req.TaskID,
		Status: types.TaskStatusCompleted,
		Answer: synthesizerOutput.LLMOutput,
	}, nil
}

// emitFailure handles failure path for multi-agent workflow
func (mw *MultiAgentWorkflow) emitFailure(ctx workflow.Context, req types.WorkflowTaskRequest, err error, failedAt string) {
	logger := workflow.GetLogger(ctx)

	// Save failure
	workflow.ExecuteActivity(ctx, "SaveFailureActivity", activities.SaveFailureInput{
		TaskID:    req.TaskID,
		ErrorType: types.ErrorTypeWorkflow,
		ErrorMsg:  err.Error(),
	}).Get(ctx, nil)

	workflow.ExecuteActivity(ctx, "RecordExecutionFailedActivity", activities.RecordExecutionFailedInput{
		TaskID:     req.TaskID,
		WorkflowID: req.WorkflowID,
		RunID:      req.RunID,
		ErrorType:  types.ErrorTypeWorkflow,
		ErrorMsg:   err.Error(),
	}).Get(ctx, nil)

	workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
		TaskID: req.TaskID,
		Event:  events.NewTaskFailedEvent(req.TaskID, req.WorkflowID, err.Error()),
	}).Get(ctx, nil)

	logger.Error("MultiAgentWorkflow failed", "task_id", req.TaskID, "failed_at", failedAt, "error", err)
}

// containsForceFailure checks if query contains forced failure trigger
func containsForceFailure(query string) bool {
	return strings.Contains(query, "__force_multi_agent_failure__")
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}