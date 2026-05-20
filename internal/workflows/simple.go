package workflows

import (
	"cribug/internal/activities"
	"cribug/internal/events"
	"cribug/internal/types"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type SimpleWorkflow struct{}

func NewSimpleWorkflow() *SimpleWorkflow {
	return &SimpleWorkflow{}
}

func (sw *SimpleWorkflow) Execute(ctx workflow.Context, req types.WorkflowTaskRequest) (*types.WorkflowTaskResult, error) {
	logger := workflow.GetLogger(ctx)
	logger.Info("SimpleWorkflow started", "task_id", req.TaskID, "workflow_id", req.WorkflowID)

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

	// 4. Check enable_tools from task config (default false)
	enableTools := false
	if req.Config != nil && req.Config.EnableTools != nil {
		enableTools = *req.Config.EnableTools
	}

	// 4b. Detect tool intent if tools are enabled
	var toolDecision types.ToolDecision
	if enableTools {
		toolDecision = types.DetectToolIntent(req.Query)
	}

	// 5. Handle tool execution if detected
	if toolDecision.Matched {
		logger.Info("Tool detected", "tool_name", toolDecision.ToolName, "task_id", req.TaskID)

		// Emit TOOL_STARTED
		workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
			TaskID: req.TaskID,
			Event:  events.NewToolStartedEvent(req.TaskID, toolDecision.ToolName, toolDecision.Arguments),
		}).Get(ctx, nil)

		var toolResult *types.ToolResult
		err = workflow.ExecuteActivity(ctx, "ExecuteToolActivity", activities.ToolExecuteInput{
			TaskID:    req.TaskID,
			ToolName:  toolDecision.ToolName,
			Arguments: toolDecision.Arguments,
		}).Get(ctx, &toolResult)
		if err != nil {
			logger.Error("ExecuteToolActivity failed", "error", err)
		}

		// Check if tool execution failed
		if toolResult != nil && toolResult.Error != "" {
			logger.Warn("Tool execution failed", "tool_name", toolDecision.ToolName, "error", toolResult.Error)

			// Emit TOOL_FAILED
			workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
				TaskID: req.TaskID,
				Event:  events.NewToolFailedEvent(req.TaskID, toolDecision.ToolName, toolResult.Error),
			}).Get(ctx, nil)

			workflow.ExecuteActivity(ctx, "SaveFailureActivity", activities.SaveFailureInput{
				TaskID:    req.TaskID,
				ErrorType: types.ErrorTypeTool,
				ErrorMsg:  toolResult.Error,
			}).Get(ctx, nil)

			workflow.ExecuteActivity(ctx, "RecordExecutionFailedActivity", activities.RecordExecutionFailedInput{
				TaskID:     req.TaskID,
				WorkflowID: req.WorkflowID,
				RunID:      req.RunID,
				ErrorType:  types.ErrorTypeTool,
				ErrorMsg:   toolResult.Error,
			}).Get(ctx, nil)

			workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
				TaskID: req.TaskID,
				Event:  events.NewTaskFailedEvent(req.TaskID, req.WorkflowID, toolResult.Error),
			}).Get(ctx, nil)

			return &types.WorkflowTaskResult{
				TaskID:  req.TaskID,
				Status:  types.TaskStatusFailed,
				Answer:  "",
				Error:   toolResult.Error,
			}, nil
		}

		// Tool succeeded - emit TOOL_COMPLETED and merge result
		if toolResult != nil {
			// Emit TOOL_COMPLETED
			workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
				TaskID: req.TaskID,
				Event:  events.NewToolCompletedEvent(req.TaskID, toolResult.ToolName, toolResult.Output, toolResult.LatencyMs),
			}).Get(ctx, nil)

			finalResult := "Tool " + toolResult.ToolName + " result: " + toolResult.Output

			workflow.ExecuteActivity(ctx, "SaveResultActivity", activities.SaveResultInput{
				TaskID: req.TaskID,
				Result: finalResult,
			}).Get(ctx, nil)

			workflow.ExecuteActivity(ctx, "RecordExecutionCompletedActivity", activities.RecordExecutionInput{
				TaskID:     req.TaskID,
				WorkflowID: req.WorkflowID,
				RunID:      req.RunID,
			}).Get(ctx, nil)

			workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
				TaskID: req.TaskID,
				Event:  events.NewTaskCompletedEvent(req.TaskID, req.WorkflowID),
			}).Get(ctx, nil)

			return &types.WorkflowTaskResult{
				TaskID: req.TaskID,
				Status: types.TaskStatusCompleted,
				Answer: finalResult,
			}, nil
		}
	}

	// 6. EstimatePromptTokensActivity (if no tool matched or tool execution returned no result)
	// Build text to estimate: session messages + current query
	estInput := activities.EstimatePromptTokensInput{Model: req.Model}
	if sessionOutput != nil {
		for _, msg := range sessionOutput.Messages {
			estInput.Text += msg.Content + "\n"
		}
	}
	estInput.Text += req.Query

	var estOutput *activities.EstimatePromptTokensOutput
	err = workflow.ExecuteActivity(ctx, "EstimatePromptTokensActivity", estInput).Get(ctx, &estOutput)
	if err != nil {
		logger.Error("EstimatePromptTokensActivity failed", "error", err)
	}

	// 7. CheckBudgetActivity
	var budgetOutput *activities.CheckBudgetOutput
	err = workflow.ExecuteActivity(ctx, "CheckBudgetActivity", activities.CheckBudgetInput{
		EstimatedPromptTokens: estOutput.EstimatedPromptTokens,
		MaxTotalTokens:        req.MaxTotalTokens,
		MaxCompletionTokens:   req.MaxCompletionTokens,
	}).Get(ctx, &budgetOutput)
	if err != nil {
		logger.Error("CheckBudgetActivity failed", "error", err)
	}

	// 8. If budget not allowed, return budget_exceeded
	if !budgetOutput.Allowed {
		logger.Warn("Budget exceeded", "reason", budgetOutput.Reason)

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

	// 9. Emit LLM_STARTED
	err = workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
		TaskID: req.TaskID,
		Event:  events.NewLLMStartedEvent(req.TaskID, req.Model),
	}).Get(ctx, nil)
	if err != nil {
		logger.Error("EmitEventActivity (LLM_STARTED) failed", "error", err)
	}

	// 10. AgentActivity
	var agentOutput *activities.AgentActivityOutput
	err = workflow.ExecuteActivity(ctx, "AgentActivity", activities.AgentActivityInput{
		TaskID:                  req.TaskID,
		WorkflowID:              req.WorkflowID,
		RunID:                   req.RunID,
		Query:                   req.Query,
		SessionID:               req.SessionID,
		Model:                   req.Model,
		Temperature:             req.Temperature,
		MaxCompletionTokens:     req.MaxCompletionTokens,
		SessionMessages:         sessionOutput.Messages,
		AllowedCompletionTokens: budgetOutput.AllowedCompletionTokens,
	}).Get(ctx, &agentOutput)
	if err != nil {
		logger.Error("AgentActivity failed", "error", err)

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

		return nil, err
	}

	// 11. Emit LLM_COMPLETED
	err = workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
		TaskID: req.TaskID,
		Event:  events.NewLLMCompletedEvent(req.TaskID, agentOutput.Model, agentOutput.FinishReason, agentOutput.LatencyMS),
	}).Get(ctx, nil)
	if err != nil {
		logger.Error("EmitEventActivity (LLM_COMPLETED) failed", "error", err)
	}

	// 12. RecordUsageActivity
	err = workflow.ExecuteActivity(ctx, "RecordUsageActivity", activities.RecordUsageInput{
		TaskID:                req.TaskID,
		WorkflowID:           req.WorkflowID,
		RunID:                 req.RunID,
		Provider:              agentOutput.Provider,
		Model:                 agentOutput.Model,
		EstimatedPromptTokens: estOutput.EstimatedPromptTokens,
		MaxCompletionTokens:   budgetOutput.AllowedCompletionTokens,
		PromptTokens:          agentOutput.Usage.PromptTokens,
		CompletionTokens:      agentOutput.Usage.CompletionTokens,
		TotalTokens:           agentOutput.Usage.TotalTokens,
		LatencyMS:             agentOutput.LatencyMS,
		FinishReason:          agentOutput.FinishReason,
	}).Get(ctx, nil)
	if err != nil {
		logger.Error("RecordUsageActivity failed", "error", err)
	}

	// 13. Emit USAGE_RECORDED
	workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
		TaskID: req.TaskID,
		Event:  events.NewUsageRecordedEvent(req.TaskID, agentOutput.Usage.TotalTokens),
	}).Get(ctx, nil)

	// 14. Check post-LLM budget
	if agentOutput.Usage.TotalTokens > req.MaxTotalTokens {
		logger.Warn("Post-LLM budget exceeded", "total_tokens", agentOutput.Usage.TotalTokens, "max_total", req.MaxTotalTokens)

		workflow.ExecuteActivity(ctx, "SaveFailureActivity", activities.SaveFailureInput{
			TaskID:    req.TaskID,
			ErrorType: types.TaskStatusBudgetExceeded,
			ErrorMsg:  "actual_total_tokens exceeds max_total_tokens",
		}).Get(ctx, nil)

		workflow.ExecuteActivity(ctx, "RecordExecutionFailedActivity", activities.RecordExecutionFailedInput{
			TaskID:     req.TaskID,
			WorkflowID: req.WorkflowID,
			RunID:      req.RunID,
			ErrorType:  types.TaskStatusBudgetExceeded,
			ErrorMsg:   "actual_total_tokens exceeds max_total_tokens",
		}).Get(ctx, nil)

		workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
			TaskID: req.TaskID,
			Event:  events.NewTaskBudgetExceededEvent(req.TaskID, "actual_total_tokens exceeds max_total_tokens", agentOutput.Usage.TotalTokens, req.MaxTotalTokens),
		}).Get(ctx, nil)

		return &types.WorkflowTaskResult{
			TaskID: req.TaskID,
			Status: types.TaskStatusBudgetExceeded,
			Answer: "",
			Error:  "actual_total_tokens exceeds max_total_tokens",
		}, nil
	}

	// 15. SaveSessionActivity
	if req.SessionID != "" {
		workflow.ExecuteActivity(ctx, "SaveSessionActivity", activities.SaveSessionInput{
			TaskID:           req.TaskID,
			SessionID:        req.SessionID,
			UserMessage:      req.Query,
			AssistantMessage: agentOutput.Answer,
		}).Get(ctx, nil)
	}

	// 16. SaveResultActivity with usage
	promptTokens := agentOutput.Usage.PromptTokens
	completionTokens := agentOutput.Usage.CompletionTokens
	totalTokens := agentOutput.Usage.TotalTokens
	err = workflow.ExecuteActivity(ctx, "SaveResultActivity", activities.SaveResultInput{
		TaskID:           req.TaskID,
		Result:           agentOutput.Answer,
		PromptTokens:     &promptTokens,
		CompletionTokens: &completionTokens,
		TotalTokens:      &totalTokens,
	}).Get(ctx, nil)
	if err != nil {
		logger.Error("SaveResultActivity failed", "error", err)

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

		return nil, err
	}

	// 17. RecordExecutionCompletedActivity
	err = workflow.ExecuteActivity(ctx, "RecordExecutionCompletedActivity", activities.RecordExecutionInput{
		TaskID:     req.TaskID,
		WorkflowID: req.WorkflowID,
		RunID:      req.RunID,
	}).Get(ctx, nil)
	if err != nil {
		logger.Error("RecordExecutionCompletedActivity failed", "error", err)
	}

	// 18. Emit TASK_COMPLETED
	workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
		TaskID: req.TaskID,
		Event:  events.NewTaskCompletedEvent(req.TaskID, req.WorkflowID),
	}).Get(ctx, nil)

	logger.Info("SimpleWorkflow completed", "task_id", req.TaskID, "answer", agentOutput.Answer)
	return &types.WorkflowTaskResult{
		TaskID: req.TaskID,
		Status: types.TaskStatusCompleted,
		Answer: agentOutput.Answer,
	}, nil
}