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
	var plannerOutput, researcherOutput string

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

	// 5. Execute researcher (mock)
	logger.Info("Executing researcher (mock)")
	err = workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
		TaskID: req.TaskID,
		Event:  events.NewAgentStartedEvent(req.TaskID, string(types.AgentRoleResearcher), 2),
	}).Get(ctx, nil)
	if err != nil {
		logger.Error("EmitEventActivity (AGENT_STARTED researcher) failed", "error", err)
	}

	var researcherAgentOutput *types.RunAgentActivityOutput
	err = workflow.ExecuteActivity(ctx, "RunAgentActivity", types.RunAgentActivityInput{
		TaskID: req.TaskID,
		Role:   types.AgentRoleResearcher,
		Query:  req.Query,
	}).Get(ctx, &researcherAgentOutput)
	if err != nil {
		logger.Error("RunAgentActivity (researcher) failed", "error", err)
		mw.emitFailure(ctx, req, err, "researcher")
		return nil, err
	}
	researcherOutput = researcherAgentOutput.Step.Output

	err = workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
		TaskID: req.TaskID,
		Event:  events.NewAgentCompletedEvent(req.TaskID, string(types.AgentRoleResearcher), "completed", researcherOutput),
	}).Get(ctx, nil)
	if err != nil {
		logger.Error("EmitEventActivity (AGENT_COMPLETED researcher) failed", "error", err)
	}
	logger.Info("Researcher completed", "output_length", len(researcherOutput))

	// 6. Execute critic (LLM-backed) with budget check
	logger.Info("Executing critic (LLM-backed)")

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

	// Estimate prompt tokens for critic
	samplePrompt := req.Query + " " + plannerOutput + " " + researcherOutput
	var estOutput *activities.EstimatePromptTokensOutput
	err = workflow.ExecuteActivity(ctx, "EstimatePromptTokensActivity", activities.EstimatePromptTokensInput{
		Model: req.Model,
		Text:  samplePrompt,
	}).Get(ctx, &estOutput)
	if err != nil {
		logger.Warn("EstimatePromptTokensActivity failed", "error", err)
	}

	// Check budget for critic
	var budgetOutput *activities.CheckBudgetOutput
	if estOutput != nil {
		err = workflow.ExecuteActivity(ctx, "CheckBudgetActivity", activities.CheckBudgetInput{
			EstimatedPromptTokens: estOutput.EstimatedPromptTokens,
			MaxTotalTokens:        req.MaxTotalTokens,
			MaxCompletionTokens:   req.MaxCompletionTokens,
		}).Get(ctx, &budgetOutput)

		if err != nil {
			logger.Warn("CheckBudgetActivity failed", "error", err)
		} else if !budgetOutput.Allowed {
			logger.Warn("Budget exceeded for critic", "reason", budgetOutput.Reason)
			workflow.ExecuteActivity(ctx, "SaveFailureActivity", activities.SaveFailureInput{
				TaskID:    req.TaskID,
				ErrorType: types.TaskStatusBudgetExceeded,
				ErrorMsg:  budgetOutput.Reason,
			}).Get(ctx, nil)

			workflow.ExecuteActivity(ctx, "RecordExecutionFailedActivity", activities.RecordExecutionFailedInput{
				TaskID:     req.TaskID,
				WorkflowID: req.WorkflowID,
				RunID:      req.RunID,
				ErrorType:  types.TaskStatusBudgetExceeded,
				ErrorMsg:   budgetOutput.Reason,
			}).Get(ctx, nil)

			workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
				TaskID: req.TaskID,
				Event:  events.NewTaskBudgetExceededEvent(req.TaskID, budgetOutput.Reason, estOutput.EstimatedPromptTokens, req.MaxTotalTokens),
			}).Get(ctx, nil)

			return &types.WorkflowTaskResult{
				TaskID: req.TaskID,
				Status: types.TaskStatusBudgetExceeded,
				Answer: "",
				Error:  budgetOutput.Reason,
			}, nil
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

	// Emit LLM_STARTED - only after budget check passes
	workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
		TaskID: req.TaskID,
		Event:  events.NewLLMStartedEvent(req.TaskID, req.Model),
	}).Get(ctx, nil)

	// Current answer for critic to review (could be researcher output or empty)
	currentAnswer := researcherOutput

	// Run critic LLM call
	var criticOutput *types.RunCriticAgentOutput
	err = workflow.ExecuteActivity(ctx, "RunCriticAgentActivity", types.RunCriticAgentInput{
		TaskID:              req.TaskID,
		WorkflowID:          req.WorkflowID,
		RunID:               req.RunID,
		Query:               req.Query,
		Model:               req.Model,
		Temperature:         req.Temperature,
		MaxCompletionTokens: budgetOutput.AllowedCompletionTokens,
		PlannerOutput:       plannerOutput,
		ResearcherOutput:    researcherOutput,
		CurrentAnswer:       currentAnswer,
	}).Get(ctx, &criticOutput)

	finishReason := "stop"
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
		MaxCompletionTokens:   budgetOutput.AllowedCompletionTokens,
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

	// Track accumulated tokens for budget check
	accumulatedTokens := criticOutput.TotalTokens

	// 7. Execute synthesizer (LLM-backed)
	logger.Info("Executing synthesizer (LLM-backed)")

	// Estimate prompt tokens for synthesizer (including critic output)
	synthSamplePrompt := req.Query + " " + plannerOutput + " " + researcherOutput + " " + criticOutput.LLMOutput
	var synthEstOutput *activities.EstimatePromptTokensOutput
	err = workflow.ExecuteActivity(ctx, "EstimatePromptTokensActivity", activities.EstimatePromptTokensInput{
		Model: req.Model,
		Text:  synthSamplePrompt,
	}).Get(ctx, &synthEstOutput)
	if err != nil {
		logger.Warn("EstimatePromptTokensActivity failed for synthesizer", "error", err)
	}

	// Check budget for synthesizer (considering critic usage)
	var synthBudgetOutput *activities.CheckBudgetOutput
	if synthEstOutput != nil {
		// Calculate remaining budget after critic
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
		ResearcherOutput:    researcherOutput,
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

	// 9. SaveResultActivity (with accumulated usage from critic + synthesizer)
	totalPromptTokens := criticOutput.PromptTokens + synthesizerOutput.PromptTokens
	totalCompletionTokens := criticOutput.CompletionTokens + synthesizerOutput.CompletionTokens
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