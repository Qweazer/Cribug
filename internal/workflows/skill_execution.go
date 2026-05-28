package workflows

import (
	"fmt"
	"time"

	"cribug/internal/activities"
	hookspkg "cribug/internal/hooks"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const SkillExecutionWorkflowName = "SkillExecutionWorkflow"

type SkillExecutionWorkflowInput struct {
	SkillName  string
	Parameters map[string]interface{}
	TaskID     string
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

	// Step 0: Emit before_tool_call hook
	var beforeHookResult hookspkg.EmitHookEventActivityResult
	_ = workflow.ExecuteActivity(ao, "EmitHookEventActivity", hookspkg.EmitHookEventActivityInput{
		HookPoint:       hookspkg.HookPointBeforeToolCall,
		AgentID:         input.AgentID,
		WorkflowID:      input.WorkflowID,
		TenantID:        "00000000-0000-0000-0000-000000000000",
		SourceComponent: "skill",
		Payload: map[string]interface{}{
			"tool_type":  "skill",
			"tool_name":  input.SkillName,
			"skill_name": input.SkillName,
			"parameters": input.Parameters,
		},
	}).Get(ctx, &beforeHookResult)
	if beforeHookResult.Decision.Denied {
		workflow.GetLogger(ctx).Warn("Skill execution blocked by before_tool_call hook",
			"reject_code", beforeHookResult.Decision.RejectCode,
			"reject_reason", beforeHookResult.Decision.RejectReason)
		return SkillExecutionWorkflowOutput{
			RequestID: input.RequestID,
			Success:   false,
			Error:     fmt.Sprintf("%s: %s", beforeHookResult.Decision.RejectCode, beforeHookResult.Decision.RejectReason),
		}, nil
	}

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
			TaskID:     input.TaskID,
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

	// Step 4: Emit after_tool_call hook (non-blocking)
	var afterHookResult hookspkg.EmitHookEventActivityResult
	_ = workflow.ExecuteActivity(ao, "EmitHookEventActivity", hookspkg.EmitHookEventActivityInput{
		HookPoint:       hookspkg.HookPointAfterToolCall,
		AgentID:         input.AgentID,
		WorkflowID:      input.WorkflowID,
		TenantID:        "00000000-0000-0000-0000-000000000000",
		SourceComponent: "skill",
		Payload: map[string]interface{}{
			"tool_type":       "skill",
			"tool_name":       input.SkillName,
			"skill_name":      input.SkillName,
			"success":         execResult.Success,
			"duration_ms":     execResult.ExecutionTimeMs,
			"workspace_topic": workspaceItemID,
			"provider":        execResult.Provider,
			"model":           execResult.Model,
		},
	}).Get(ctx, &afterHookResult)
	_ = afterHookResult

	// Step 5: Emit on_error hook if execution failed
	if !execResult.Success {
		var errorHookResult hookspkg.EmitHookEventActivityResult
		_ = workflow.ExecuteActivity(ao, "EmitHookEventActivity", hookspkg.EmitHookEventActivityInput{
			HookPoint:       hookspkg.HookPointOnError,
			AgentID:         input.AgentID,
			WorkflowID:      input.WorkflowID,
			TenantID:        "00000000-0000-0000-0000-000000000000",
			SourceComponent: "skill",
			Payload: map[string]interface{}{
				"error_type":  execResult.ErrorType,
				"message":     execResult.Error,
				"component":   "skill",
				"skill_name":  input.SkillName,
				"recoverable": false,
			},
		}).Get(ctx, &errorHookResult)
		_ = errorHookResult
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
