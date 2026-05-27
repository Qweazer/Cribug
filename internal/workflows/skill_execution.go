package workflows

import (
	"time"

	"cribug/internal/activities"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type SkillExecutionWorkflowInput struct {
	SkillName  string
	Parameters map[string]interface{}
	AgentID    string
	WorkflowID string
	RequestID  string
}

type SkillExecutionWorkflowOutput struct {
	RequestID       string
	Success         bool
	Output          interface{}
	Error           string
	AuditID         string
	WorkspaceItemID string
}

// SkillExecutionWorkflow executes a skill with audit and workspace logging.
// NO direct HTTP, DB, Redis, LLM, goroutine, time.Now, rand, uuid.New in body.
func SkillExecutionWorkflow(ctx workflow.Context, input SkillExecutionWorkflowInput) (SkillExecutionWorkflowOutput, error) {
	ao := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 1,
		},
	})

	// Step 1: Execute skill
	var execResult activities.ExecuteSkillOutput
	err := workflow.ExecuteActivity(ao, "ExecuteSkillActivity", activities.ExecuteSkillInput{
		SkillName:  input.SkillName,
		Parameters: input.Parameters,
		AgentID:    input.AgentID,
		WorkflowID: input.WorkflowID,
		RequestID:  input.RequestID,
	}).Get(ctx, &execResult)
	if err != nil {
		// Activity failed - still audit
		var auditResult activities.AuditSkillOutput
		auditErr := workflow.ExecuteActivity(ao, "AuditSkillExecutionActivity", activities.AuditSkillInput{
			SkillName:  input.SkillName,
			AgentID:    input.AgentID,
			WorkflowID: input.WorkflowID,
			RequestID:  input.RequestID,
			Success:    false,
			DurationMs: 0,
			Error:      err.Error(),
			ErrorType:  "execution_failed",
		}).Get(ctx, &auditResult)
		if auditErr != nil {
			workflow.GetLogger(ctx).Error("Audit failed", "error", auditErr)
		}
		return SkillExecutionWorkflowOutput{
			RequestID: input.RequestID,
			Success:   false,
			Error:     err.Error(),
		}, nil
	}

	// Step 2: Audit execution (ALWAYS, even on Python failure)
	var auditResult activities.AuditSkillOutput
	auditErr := workflow.ExecuteActivity(ao, "AuditSkillExecutionActivity", activities.AuditSkillInput{
		SkillName:  input.SkillName,
		AgentID:    input.AgentID,
		WorkflowID: input.WorkflowID,
		RequestID:  input.RequestID,
		Success:    execResult.Success,
		DurationMs: execResult.ExecutionTimeMs,
		Error:      execResult.Error,
		ErrorType:  execResult.ErrorType,
		Overflow:   execResult.Overflow,
		Provider:   execResult.Provider,
		Model:      execResult.Model,
		TokenUsage: execResult.TokenUsage,
	}).Get(ctx, &auditResult)
	if auditErr != nil {
		workflow.GetLogger(ctx).Error("Audit failed", "error", auditErr)
	}

	// Step 3: Workspace append (success only)
	workspaceItemID := ""
	if execResult.Success {
		var workspaceOutput activities.WorkspaceAppendOutput
		wsErr := workflow.ExecuteActivity(ao, "WorkspaceAppendSkillResultActivity", activities.WorkspaceAppendInput{
			WorkflowID: input.WorkflowID,
			AgentID:    input.AgentID,
			SkillName:  input.SkillName,
			RequestID:  input.RequestID,
			Result:     execResult.Output,
			Success:    execResult.Success,
		}).Get(ctx, &workspaceOutput)
		if wsErr != nil {
			workflow.GetLogger(ctx).Error("Workspace append failed", "error", wsErr)
		} else {
			workspaceItemID = workspaceOutput.ItemID
		}
	}

	return SkillExecutionWorkflowOutput{
		RequestID:       input.RequestID,
		Success:         execResult.Success,
		Output:          execResult.Output,
		Error:           execResult.Error,
		AuditID:         auditResult.AuditID,
		WorkspaceItemID: workspaceItemID,
	}, nil
}
