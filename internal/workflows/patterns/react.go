package patterns

import (
	"fmt"
	"strings"
	"time"

	"cribug/internal/activities"
	hookspkg "cribug/internal/hooks"
	"cribug/internal/types"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// ReactLoop executes a Workflow-level Reason-Act-Observe loop.
// Each Reason, Act, and Final Synthesis step is a separate ExecuteAgent Activity call.
// History is passed through Activity parameters to survive retries.
func ReactLoop(
	ctx workflow.Context,
	query string,
	baseContext string,
	sessionID string,
	taskID string,
	workflowID string,
	runID string,
	model string,
	temperature float64,
	maxCompletionTokens int,
	config types.ReactConfig,
) (*types.ReactLoopResult, error) {

	logger := workflow.GetLogger(ctx)
	logger.Info("ReactLoop started",
		"task_id", taskID,
		"workflow_id", workflowID,
		"max_iterations", config.MaxIterations)

	// Activity options with retry
	activityOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    1 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    10 * time.Second,
			MaximumAttempts:    3,
		},
	}
	actCtx := workflow.WithActivityOptions(ctx, activityOpts)

	// Usage recording activity options — idempotent upsert, retry ok
	usageOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    500 * time.Millisecond,
			BackoffCoefficient: 2.0,
			MaximumInterval:    5 * time.Second,
			MaximumAttempts:    2,
		},
	}
	usageCtx := workflow.WithActivityOptions(ctx, usageOpts)

	// Audit activity gets its own non-retry policy (audit failures don't block)
	auditOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 1, // No retry for audit
		},
	}
	auditCtx := workflow.WithActivityOptions(ctx, auditOpts)

	// Hook activity options — non-blocking emission
	hookOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 1, // No retry for hooks
		},
	}
	hookCtx := workflow.WithActivityOptions(ctx, hookOpts)

	// Initialize state
	var thoughts []string
	var actions []string
	var observations []string
	totalTokens := 0
	iteration := 0

	// Ensure safe defaults
	if config.MaxIterations <= 0 {
		config.MaxIterations = 3
	}
	if config.MaxIterations > 10 {
		config.MaxIterations = 10
	}
	if config.MinIterations <= 0 {
		config.MinIterations = 1
	}
	if config.ObservationWindow <= 0 {
		config.ObservationWindow = 3
	}
	if config.MaxObservations <= 0 {
		config.MaxObservations = 10
	}
	if config.MaxThoughts <= 0 {
		config.MaxThoughts = 10
	}
	if config.MaxActions <= 0 {
		config.MaxActions = 10
	}

	// System prompt for ReAct
	systemPrompt := `You are a ReAct agent. For each response:
1. REASON: Think step by step about what to do next
2. ACT: State your action or provide output
3. If you have the final answer, start with FINAL:`

	// Build initial history - this is passed through Activity parameters each call
	history := []types.LLMMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: query},
	}
	if baseContext != "" {
		history = append(history, types.LLMMessage{
			Role: "system", Content: "Context: " + baseContext,
		})
	}

	// Main Reason-Act-Observe loop
	for iteration < config.MaxIterations {
		iterNum := iteration + 1
		logger.Info("ReAct iteration", "iteration", iterNum, "max", config.MaxIterations)

		// Phase 1: REASON - Think about what to do next
		reasonQuery := fmt.Sprintf(
			"REASON (1-2 sentences) about the next action for: %s\nPrevious observations: %v\nPrevious thoughts: %v",
			query,
			getRecentObservations(observations, config.ObservationWindow),
			getRecentStrings(thoughts, config.ObservationWindow),
		)

		// Pass accumulated history as SessionMessages so it survives Activity retries
		reasonMessages := make([]types.LLMMessage, len(history))
		copy(reasonMessages, history)
		reasonMessages = append(reasonMessages, types.LLMMessage{
			Role: "user", Content: reasonQuery,
		})

		var reasonOutput *activities.AgentActivityOutput
		err := workflow.ExecuteActivity(actCtx, "AgentActivity", activities.AgentActivityInput{
			TaskID:                taskID,
			WorkflowID:            workflowID,
			RunID:                 runID,
			Query:                 reasonQuery,
			SessionID:             sessionID,
			Model:                 model,
			Temperature:           temperature,
			MaxCompletionTokens:   maxCompletionTokens,
			SessionMessages:       history,
			AllowedCompletionTokens: maxCompletionTokens,
		}).Get(actCtx, &reasonOutput)

		if err != nil {
			logger.Error("Reason step failed", "iteration", iterNum, "error", err)
			break
		}

		thought := reasonOutput.Answer
		thoughts = append(thoughts, thought)
		if len(thoughts) > config.MaxThoughts {
			thoughts = thoughts[len(thoughts)-config.MaxThoughts:]
		}
		totalTokens += reasonOutput.Usage.TotalTokens

			// Record usage for this Reason LLM call
			_ = workflow.ExecuteActivity(usageCtx, "RecordUsageActivity", activities.RecordUsageInput{
				TaskID:                taskID,
				WorkflowID:            workflowID,
				RunID:                 runID,
				Provider:              reasonOutput.Provider,
				Model:                 reasonOutput.Model,
				PromptTokens:          reasonOutput.Usage.PromptTokens,
				CompletionTokens:      reasonOutput.Usage.CompletionTokens,
				TotalTokens:           reasonOutput.Usage.TotalTokens,
				LatencyMS:             reasonOutput.LatencyMS,
				FinishReason:          reasonOutput.FinishReason,
				MaxCompletionTokens:   maxCompletionTokens,
				AgentRole:             fmt.Sprintf("react-reason-%d", iterNum),
			}).Get(usageCtx, nil)

			// Add thought to history
		history = append(history, types.LLMMessage{
			Role: "assistant", Content: "THOUGHT: " + thought,
		})

		// Check for early completion
		if isFinalAnswer(thought) {
			logger.Info("ReAct early stop - final answer found in reason step",
				"iteration", iterNum)
			break
		}

		// Phase 2: ACT - Execute the planned action
		actionQuery := fmt.Sprintf(
			"ACT on this plan: %s\nProvide your action result directly.",
			thought,
		)

		actionMessages := make([]types.LLMMessage, len(history))
		copy(actionMessages, history)
		actionMessages = append(actionMessages, types.LLMMessage{
			Role: "user", Content: actionQuery,
		})

		var actionOutput *activities.AgentActivityOutput
		err = workflow.ExecuteActivity(actCtx, "AgentActivity", activities.AgentActivityInput{
			TaskID:                taskID,
			WorkflowID:            workflowID,
			RunID:                 runID,
			Query:                 actionQuery,
			SessionID:             sessionID,
			Model:                 model,
			Temperature:           temperature,
			MaxCompletionTokens:   maxCompletionTokens,
			SessionMessages:       history,
			AllowedCompletionTokens: maxCompletionTokens,
		}).Get(actCtx, &actionOutput)

		if err != nil {
			logger.Error("Action step failed", "iteration", iterNum, "error", err)
			observations = append(observations, fmt.Sprintf("Error: %v", err))
			break
		}

		action := actionOutput.Answer
		actions = append(actions, action)
		if len(actions) > config.MaxActions {
			actions = actions[len(actions)-config.MaxActions:]
		}
		totalTokens += actionOutput.Usage.TotalTokens

			// Record usage for this Action LLM call
			_ = workflow.ExecuteActivity(usageCtx, "RecordUsageActivity", activities.RecordUsageInput{
				TaskID:                taskID,
				WorkflowID:            workflowID,
				RunID:                 runID,
				Provider:              actionOutput.Provider,
				Model:                 actionOutput.Model,
				PromptTokens:          actionOutput.Usage.PromptTokens,
				CompletionTokens:      actionOutput.Usage.CompletionTokens,
				TotalTokens:           actionOutput.Usage.TotalTokens,
				LatencyMS:             actionOutput.LatencyMS,
				FinishReason:          actionOutput.FinishReason,
				MaxCompletionTokens:   maxCompletionTokens,
				AgentRole:             fmt.Sprintf("react-action-%d", iterNum),
			}).Get(usageCtx, nil)

			// Phase 3: OBSERVE - Record the result
		observation := fmt.Sprintf("Action result: %s", action)
		observations = append(observations, observation)
		if len(observations) > config.MaxObservations {
			observations = observations[len(observations)-config.MaxObservations:]
		}

		// Add action and observation to history
		history = append(history,
			types.LLMMessage{Role: "assistant", Content: "ACTION: " + action},
			types.LLMMessage{Role: "user", Content: "OBSERVATION: " + observation},
		)

		// Phase 4: AUDIT - Write step to Postgres (fire-and-forget, non-blocking)
		createdAt := workflow.Now(ctx).UnixNano()
		_ = workflow.ExecuteActivity(auditCtx, "SaveReActStepAuditActivity", activities.SaveReActStepAuditInput{
			WorkflowID:  workflowID,
			RunID:       runID,
			NodeID:      taskID,
			StepIndex:   iterNum,
			Reasoning:   thought,
			Action:      action,
			Observation: observation,
			CreatedAtNs: createdAt,
		})
		// Fire-and-forget: audit failure is logged but does not block the main loop

		// Step 5: Emit on_agent_step hook (non-blocking)
		var hookResult hookspkg.EmitHookEventActivityResult
		_ = workflow.ExecuteActivity(hookCtx, "EmitHookEventActivity", hookspkg.EmitHookEventActivityInput{
			HookPoint:       hookspkg.HookPointOnAgentStep,
			AgentID:         "",
			WorkflowID:      workflowID,
			TenantID:        "00000000-0000-0000-0000-000000000000",
			SourceComponent: "react",
			Payload: map[string]interface{}{
				"step":        iterNum,
				"thought":     thought,
				"action":      action,
				"observation": observation,
			},
		}).Get(ctx, &hookResult)
		_ = hookResult

		iteration++

		// Check for final answer in action
		if isFinalAnswer(action) {
			logger.Info("ReAct early stop - final answer found in action step",
				"iteration", iterNum)
			break
		}
	}

	// Final Synthesis
	logger.Info("Synthesizing final result from ReAct loops",
		"iterations", iteration,
		"thoughts", len(thoughts),
		"actions", len(actions))

	synthesisQuery := fmt.Sprintf(
		"SYNTHESIZE the final answer for: %s\nThoughts: %v\nActions: %v\nObservations: %v\nProvide a clear, concise final answer.",
		query,
		thoughts,
		actions,
		observations,
	)

	var synthOutput *activities.AgentActivityOutput
	err := workflow.ExecuteActivity(actCtx, "AgentActivity", activities.AgentActivityInput{
		TaskID:                taskID,
		WorkflowID:            workflowID,
		RunID:                 runID,
		Query:                 synthesisQuery,
		SessionID:             sessionID,
		Model:                 model,
		Temperature:           temperature,
		MaxCompletionTokens:   maxCompletionTokens,
		SessionMessages:       history,
		AllowedCompletionTokens: maxCompletionTokens,
	}).Get(actCtx, &synthOutput)

	if err != nil {
		logger.Error("Final synthesis failed", "error", err)
		return nil, fmt.Errorf("final synthesis failed: %w", err)
	}

	totalTokens += synthOutput.Usage.TotalTokens
	finalResult := synthOutput.Answer

	// Record usage for the Final Synthesis LLM call
	_ = workflow.ExecuteActivity(usageCtx, "RecordUsageActivity", activities.RecordUsageInput{
		TaskID:                taskID,
		WorkflowID:            workflowID,
		RunID:                 runID,
		Provider:              synthOutput.Provider,
		Model:                 synthOutput.Model,
		PromptTokens:          synthOutput.Usage.PromptTokens,
		CompletionTokens:      synthOutput.Usage.CompletionTokens,
		TotalTokens:           synthOutput.Usage.TotalTokens,
		LatencyMS:             synthOutput.LatencyMS,
		FinishReason:          synthOutput.FinishReason,
		MaxCompletionTokens:   maxCompletionTokens,
		AgentRole:             "react-synthesizer",
	}).Get(usageCtx, nil)

	logger.Info("ReactLoop completed",
		"iterations", iteration,
		"total_tokens", totalTokens,
		"final_result_len", len(finalResult))

	return &types.ReactLoopResult{
		Thoughts:     thoughts,
		Actions:      actions,
		Observations: observations,
		FinalResult:  finalResult,
		TotalTokens:  totalTokens,
		Iterations:   iteration,
	}, nil
}

func getRecentObservations(observations []string, window int) []string {
	if window <= 0 || len(observations) <= window {
		return observations
	}
	return observations[len(observations)-window:]
}

func getRecentStrings(items []string, window int) []string {
	if window <= 0 || len(items) <= window {
		return items
	}
	return items[len(items)-window:]
}

func isFinalAnswer(text string) bool {
	return strings.Contains(strings.ToUpper(text), "FINAL:")
}
