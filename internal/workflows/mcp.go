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

const MCPToolCallWorkflowName = "MCPToolCallWorkflow"

type MCPToolCallWorkflowInput struct {
	ToolID     string                 `json:"tool_id"`
	ServerID   string                 `json:"server_id"`
	ToolName   string                 `json:"tool_name"`
	Arguments  map[string]interface{} `json:"arguments"`
	Timeout    int                    `json:"timeout"`
	RequestID  string                 `json:"request_id"`
	AgentID    string                 `json:"agent_id"`
	AgentRole  string                 `json:"agent_role"`
	WorkflowID string                 `json:"workflow_id"`
	RunID      string                 `json:"run_id"`
	TaskID     string                 `json:"task_id"`
}

type MCPToolCallWorkflowResult struct {
	ToolResult    *types.MCPToolResult `json:"tool_result"`
	WorkspaceRef  string               `json:"workspace_ref,omitempty"`
	AuditID       string               `json:"audit_id"`
	Success       bool                 `json:"success"`
	Error         string               `json:"error,omitempty"`
}

type MCPToolCallWorkflow struct{}

func NewMCPToolCallWorkflow() *MCPToolCallWorkflow {
	return &MCPToolCallWorkflow{}
}

func (w *MCPToolCallWorkflow) Execute(ctx workflow.Context, input MCPToolCallWorkflowInput) (*MCPToolCallWorkflowResult, error) {
	logger := workflow.GetLogger(ctx)
	logger.Info("MCPToolCallWorkflow started", "tool", input.ToolName, "server_id", input.ServerID, "agent", input.AgentID)

	if input.Timeout <= 0 {
		input.Timeout = types.DefaultMCPTimeoutSec
	}

	defaultOpts := workflow.ActivityOptions{
		StartToCloseTimeout: time.Duration(input.Timeout+10) * time.Second,
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
		SourceComponent: "mcp",
		Payload: map[string]interface{}{
			"tool_type": "mcp",
			"tool_name": input.ToolName,
			"tool_id":   input.ToolID,
			"server_id": input.ServerID,
		},
	}).Get(ctx, &beforeHookResult)
	// Check if blocked
	if beforeHookResult.Decision.Denied {
		logger.Warn("MCP tool call blocked by before_tool_call hook",
			"reject_code", beforeHookResult.Decision.RejectCode,
			"reject_reason", beforeHookResult.Decision.RejectReason)
		return &MCPToolCallWorkflowResult{
			Success: false,
			Error:   fmt.Sprintf("%s: %s", beforeHookResult.Decision.RejectCode, beforeHookResult.Decision.RejectReason),
		}, nil
	}

	// Step 1: Call MCP tool via Activity (all validation is inside the Activity)
	var callResult activities.CallMCPToolActivityResult
	err := workflow.ExecuteActivity(ctx, "CallMCPToolActivity", activities.CallMCPToolActivityInput{
		ToolID:     input.ToolID,
		ServerID:   input.ServerID,
		ToolName:   input.ToolName,
		Arguments:  input.Arguments,
		Timeout:    input.Timeout,
		RequestID:  input.RequestID,
		AgentID:    input.AgentID,
		WorkflowID: input.WorkflowID,
		RunID:      input.RunID,
	}).Get(ctx, &callResult)

	toolResult := &types.MCPToolResult{}
	if err != nil {
		logger.Error("CallMCPToolActivity failed", "error", err)
		toolResult = &types.MCPToolResult{
			ToolID:    input.ToolID,
			ToolName:  input.ToolName,
			ServerID:  input.ServerID,
			RequestID: input.RequestID,
			Success:   false,
			Error:     err.Error(),
			ErrorType: types.MCPErrorTypeCallFailed,
		}
	} else if callResult.Result != nil {
		toolResult = callResult.Result
	}

	// Step 2: Append result to Workspace (if successful)
	var workspaceRef string
	if toolResult.Success {
		topic := fmt.Sprintf("mcp:%s:results:%s", input.TaskID, input.RequestID)
		wsContent := fmt.Sprintf("[MCP Tool: %s]\nServer: %s\nResult: %s",
			toolResult.ToolName, toolResult.ServerID, toolResult.Content)
		var wsResult WorkspaceAppendActivityResult
		wsErr := workflow.ExecuteActivity(ctx, "WorkspaceAppendActivity", WorkspaceAppendActivityInput{
			TaskID:   input.TaskID,
			AgentID:  input.AgentID,
			Role:     input.AgentRole,
			ItemType: types.WSTypeNote,
			Title:    topic,
			Content:  wsContent,
		}).Get(ctx, &wsResult)
		if wsErr != nil {
			logger.Warn("WorkspaceAppendActivity failed (non-fatal)", "error", wsErr)
		} else {
			workspaceRef = wsResult.ItemID
		}
	}

	// Step 3: Audit the call
	var auditResult activities.AuditMCPToolCallActivityResult
	auditErr := workflow.ExecuteActivity(ctx, "AuditMCPToolCallActivity", activities.AuditMCPToolCallActivityInput{
		ServerID:   input.ServerID,
		ToolID:     input.ToolID,
		ToolName:   input.ToolName,
		AgentID:    input.AgentID,
		WorkflowID: input.WorkflowID,
		RequestID:  input.RequestID,
		Success:    toolResult.Success,
		Error:      toolResult.Error,
		ErrorType:  toolResult.ErrorType,
		DurationMs: toolResult.DurationMs,
		Overflow:   toolResult.Overflow,
	}).Get(ctx, &auditResult)

	auditID := ""
	if auditErr != nil {
		logger.Warn("AuditMCPToolCallActivity failed (non-fatal)", "error", auditErr)
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
		SourceComponent: "mcp",
		Payload: map[string]interface{}{
			"tool_type":   "mcp",
			"tool_name":   input.ToolName,
			"tool_id":     input.ToolID,
			"server_id":   input.ServerID,
			"success":     toolResult.Success,
			"duration_ms": toolResult.DurationMs,
			"error":       toolResult.Error,
		},
	}).Get(ctx, &afterHookResult)

	// Step 5: Emit on_error hook if tool call failed
	if !toolResult.Success {
		var errorHookResult hookspkg.EmitHookEventActivityResult
		_ = workflow.ExecuteActivity(ctx, "EmitHookEventActivity", hookspkg.EmitHookEventActivityInput{
			HookPoint:       hookspkg.HookPointOnError,
			AgentID:         input.AgentID,
			WorkflowID:      input.WorkflowID,
			TenantID:        "00000000-0000-0000-0000-000000000000",
			SourceComponent: "mcp",
			Payload: map[string]interface{}{
				"error_type": toolResult.ErrorType,
				"message":    toolResult.Error,
				"component":  "mcp",
				"tool_name":  input.ToolName,
				"recoverable": false,
			},
		}).Get(ctx, &errorHookResult)
		_ = errorHookResult
	}

	logger.Info("MCPToolCallWorkflow completed",
		"tool", input.ToolName, "success", toolResult.Success, "audit_id", auditID)

	return &MCPToolCallWorkflowResult{
		ToolResult:   toolResult,
		WorkspaceRef: workspaceRef,
		AuditID:      auditID,
		Success:      toolResult.Success,
		Error:        toolResult.Error,
	}, nil
}

// ── Workspace Append Activity types (local to workflow package) ─

type WorkspaceAppendActivityInput struct {
	TaskID   string `json:"task_id"`
	AgentID  string `json:"agent_id"`
	Role     string `json:"role"`
	ItemType string `json:"item_type"`
	Title    string `json:"title"`
	Content  string `json:"content"`
}

type WorkspaceAppendActivityResult struct {
	ItemID string `json:"item_id"`
}
