package workflows

import (
	"fmt"
	"time"

	"cribug/internal/activities"
	hookspkg "cribug/internal/hooks"
	"cribug/internal/types"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const SandboxWorkflowName = "SandboxWorkflow"

type SandboxWorkflowInput struct {
	Code       string               `json:"code"`
	Language   string               `json:"language"`
	Stdin      string               `json:"stdin,omitempty"`
	Policy     *types.SandboxPolicy `json:"policy,omitempty"`
	RequestID  string               `json:"request_id"`
	AgentID    string               `json:"agent_id"`
	AgentRole  string               `json:"agent_role"`
	WorkflowID string               `json:"workflow_id"`
	RunID      string               `json:"run_id"`
	TaskID     string               `json:"task_id"`
}

type SandboxWorkflowResult struct {
	Result       *types.SandboxExecutionResult `json:"result"`
	WorkspaceRef string                        `json:"workspace_ref,omitempty"`
	AuditID      string                        `json:"audit_id"`
	Success      bool                          `json:"success"`
	Error        string                        `json:"error,omitempty"`
}

type SandboxWorkflow struct{}

func NewSandboxWorkflow() *SandboxWorkflow { return &SandboxWorkflow{} }

func (w *SandboxWorkflow) Execute(ctx workflow.Context, input SandboxWorkflowInput) (*SandboxWorkflowResult, error) {
	logger := workflow.GetLogger(ctx)
	logger.Info("SandboxWorkflow started", "language", input.Language, "request_id", input.RequestID)

	timeout := types.DefaultSandboxWallTimeoutSec + 10
	if input.Policy != nil && input.Policy.WallTimeoutSec > 0 {
		timeout = input.Policy.WallTimeoutSec + 10
	}

	defaultOpts := workflow.ActivityOptions{
		StartToCloseTimeout: time.Duration(timeout) * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    500 * time.Millisecond,
			BackoffCoefficient: 2.0,
			MaximumInterval:    5 * time.Second,
			MaximumAttempts:    1,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, defaultOpts)

	// Step 0: Emit before_tool_call hook
	var beforeHookResult hookspkg.EmitHookEventActivityResult
	_ = workflow.ExecuteActivity(ctx, "EmitHookEventActivity", hookspkg.EmitHookEventActivityInput{
		HookPoint:       hookspkg.HookPointBeforeToolCall,
		AgentID:         input.AgentID,
		WorkflowID:      input.WorkflowID,
		TenantID:        "00000000-0000-0000-0000-000000000000",
		SourceComponent: "sandbox",
		Payload: map[string]interface{}{
			"tool_type": "sandbox",
			"tool_name": "sandbox_execute",
			"language":  input.Language,
			"code_len":  len(input.Code),
		},
	}).Get(ctx, &beforeHookResult)
	if beforeHookResult.Decision.Denied {
		logger.Warn("Sandbox execution blocked by before_tool_call hook",
			"reject_code", beforeHookResult.Decision.RejectCode,
			"reject_reason", beforeHookResult.Decision.RejectReason)
		return &SandboxWorkflowResult{
			Success: false,
			Error:   fmt.Sprintf("%s: %s", beforeHookResult.Decision.RejectCode, beforeHookResult.Decision.RejectReason),
		}, nil
	}

	// Step 1: Execute sandbox code
	var execResult activities.ExecuteSandboxActivityResult
	err := workflow.ExecuteActivity(ctx, "ExecuteSandboxActivity", activities.ExecuteSandboxActivityInput{
		Code:       input.Code,
		Language:   input.Language,
		Stdin:      input.Stdin,
		Policy:     input.Policy,
		RequestID:  input.RequestID,
		AgentID:    input.AgentID,
		WorkflowID: input.WorkflowID,
		RunID:      input.RunID,
		TaskID:     input.TaskID,
	}).Get(ctx, &execResult)

	sandboxResult := &types.SandboxExecutionResult{}
	if err != nil {
		logger.Error("ExecuteSandboxActivity failed", "error", err)
		sandboxResult = &types.SandboxExecutionResult{
			RequestID: input.RequestID, Success: false,
			Error: err.Error(), ErrorType: types.SandboxErrorTypeExecutionFailed,
		}
	} else if execResult.Result != nil {
		sandboxResult = execResult.Result
	}

	// Step 2: Append to Workspace
	var workspaceRef string
	topic := fmt.Sprintf("sandbox:%s:results:%s", input.TaskID, input.RequestID)
	wsContent := fmt.Sprintf("[Sandbox: %s]\nstdout: %s\nstderr: %s\nexit_code: %d",
		input.Language, sandboxResult.Stdout, sandboxResult.Stderr, sandboxResult.ExitCode)
	var wsResult activities.SandboxWorkspaceAppendResult
	wsErr := workflow.ExecuteActivity(ctx, "SandboxWorkspaceAppendActivity", activities.SandboxWorkspaceAppendInput{
		TaskID:  input.TaskID,
		AgentID: input.AgentID,
		Role:    input.AgentRole,
		Title:   topic,
		Content: wsContent,
	}).Get(ctx, &wsResult)
	if wsErr != nil {
		logger.Warn("SandboxWorkspaceAppend failed (non-fatal)", "error", wsErr)
	} else {
		workspaceRef = wsResult.ItemID
	}

	// Step 3: Audit
	var auditResult activities.AuditSandboxActivityResult
	auditErr := workflow.ExecuteActivity(ctx, "AuditSandboxActivity", activities.AuditSandboxActivityInput{
		RequestID:  input.RequestID,
		AgentID:    input.AgentID,
		WorkflowID: input.WorkflowID,
		Language:   input.Language,
		CodeLen:    len(input.Code),
		Success:    sandboxResult.Success,
		ExitCode:   sandboxResult.ExitCode,
		Error:      sandboxResult.Error,
		ErrorType:  sandboxResult.ErrorType,
		DurationMs: sandboxResult.DurationMs,
	}).Get(ctx, &auditResult)

	auditID := ""
	if auditErr != nil {
		logger.Warn("AuditSandboxActivity failed (non-fatal)", "error", auditErr)
	} else {
		auditID = auditResult.AuditID
	}

	// Step 4: Emit after_tool_call hook (non-blocking)
	var afterHookResult hookspkg.EmitHookEventActivityResult
	_ = workflow.ExecuteActivity(ctx, "EmitHookEventActivity", hookspkg.EmitHookEventActivityInput{
		HookPoint:       hookspkg.HookPointAfterToolCall,
		AgentID:         input.AgentID,
		WorkflowID:      input.WorkflowID,
		TenantID:        "00000000-0000-0000-0000-000000000000",
		SourceComponent: "sandbox",
		Payload: map[string]interface{}{
			"tool_type":    "sandbox",
			"tool_name":    "sandbox_execute",
			"success":      sandboxResult.Success,
			"exit_code":    sandboxResult.ExitCode,
			"duration_ms":  sandboxResult.DurationMs,
			"stdout_size":  len(sandboxResult.Stdout),
			"stderr_size":  len(sandboxResult.Stderr),
			"error":        sandboxResult.Error,
		},
	}).Get(ctx, &afterHookResult)

	// Step 5: Emit on_error hook if execution failed
	if !sandboxResult.Success {
		var errorHookResult hookspkg.EmitHookEventActivityResult
		_ = workflow.ExecuteActivity(ctx, "EmitHookEventActivity", hookspkg.EmitHookEventActivityInput{
			HookPoint:       hookspkg.HookPointOnError,
			AgentID:         input.AgentID,
			WorkflowID:      input.WorkflowID,
			TenantID:        "00000000-0000-0000-0000-000000000000",
			SourceComponent: "sandbox",
			Payload: map[string]interface{}{
				"error_type":  sandboxResult.ErrorType,
				"message":     sandboxResult.Error,
				"component":   "sandbox",
				"exit_code":   sandboxResult.ExitCode,
				"recoverable": false,
			},
		}).Get(ctx, &errorHookResult)
		_ = errorHookResult
	}

	logger.Info("SandboxWorkflow completed",
		"success", sandboxResult.Success, "exit_code", sandboxResult.ExitCode)

	return &SandboxWorkflowResult{
		Result:       sandboxResult,
		WorkspaceRef: workspaceRef,
		AuditID:      auditID,
		Success:      sandboxResult.Success,
		Error:        sandboxResult.Error,
	}, nil
}
