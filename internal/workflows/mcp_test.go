package workflows

import (
	"errors"
	"testing"

	"cribug/internal/activities"
	"cribug/internal/types"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func setupMCPWorkflowTest(t *testing.T) (*testsuite.TestWorkflowEnvironment, *MCPToolCallWorkflow, *activities.MCPActivities) {
	t.Helper()
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()
	wf := NewMCPToolCallWorkflow()
	env.RegisterWorkflowWithOptions(wf.Execute, workflow.RegisterOptions{Name: MCPToolCallWorkflowName})

	mcp := &activities.MCPActivities{}
	env.RegisterActivityWithOptions(mcp.CallMCPTool, activity.RegisterOptions{Name: "CallMCPToolActivity"})
	env.RegisterActivityWithOptions(mcp.AuditMCPToolCall, activity.RegisterOptions{Name: "AuditMCPToolCallActivity"})
	env.RegisterActivityWithOptions(mcp.WorkspaceAppend, activity.RegisterOptions{Name: "WorkspaceAppendActivity"})

	return env, wf, mcp
}

func TestMCPToolCallWorkflow_Success(t *testing.T) {
	env, _, mcp := setupMCPWorkflowTest(t)
	serverID := uuid.New().String()
	toolID := uuid.New().String()

	env.OnActivity(mcp.CallMCPTool, mock.Anything, mock.Anything).
		Return(&activities.CallMCPToolActivityResult{
			Result: &types.MCPToolResult{
				ToolID: toolID, ToolName: "echo", ServerID: serverID,
				RequestID: "req-1", Content: "echo: hello", Success: true, DurationMs: 10,
			},
		}, nil)

	env.OnActivity(mcp.WorkspaceAppend, mock.Anything, mock.Anything).
		Return(&activities.WorkspaceAppendActivityResult{ItemID: "ws-item-1"}, nil)

	env.OnActivity(mcp.AuditMCPToolCall, mock.Anything, mock.Anything).
		Return(&activities.AuditMCPToolCallActivityResult{AuditID: "audit-1"}, nil)

	env.ExecuteWorkflow(MCPToolCallWorkflowName, MCPToolCallWorkflowInput{
		ToolID: toolID, ServerID: serverID, ToolName: "echo",
		Arguments:  map[string]interface{}{"message": "hello"},
		Timeout:    30, RequestID: "req-1",
		AgentID: "agent-1", AgentRole: "worker",
		WorkflowID: "wf-1", RunID: "run-1", TaskID: "task-1",
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
	var result MCPToolCallWorkflowResult
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("workflow result error: %v", err)
	}
	if !result.Success {
		t.Errorf("expected success, got error: %s", result.Error)
	}
	if result.AuditID != "audit-1" {
		t.Errorf("expected audit ID 'audit-1', got '%s'", result.AuditID)
	}
	if result.WorkspaceRef != "ws-item-1" {
		t.Errorf("expected workspace ref 'ws-item-1', got '%s'", result.WorkspaceRef)
	}
}

func TestMCPToolCallWorkflow_CallFailureStillAudited(t *testing.T) {
	env, _, mcp := setupMCPWorkflowTest(t)
	serverID := uuid.New().String()
	toolID := uuid.New().String()

	env.OnActivity(mcp.CallMCPTool, mock.Anything, mock.Anything).
		Return(&activities.CallMCPToolActivityResult{
			Result: &types.MCPToolResult{
				ToolID: toolID, ToolName: "echo", ServerID: serverID,
				RequestID: "req-fail", Success: false,
				Error:     "schema validation failed: missing required field 'message'",
				ErrorType: types.MCPErrorTypeSchemaValidation,
				DurationMs: 1,
			},
		}, nil)

	env.OnActivity(mcp.AuditMCPToolCall, mock.Anything, mock.Anything).
		Return(&activities.AuditMCPToolCallActivityResult{AuditID: "audit-fail"}, nil)

	env.ExecuteWorkflow(MCPToolCallWorkflowName, MCPToolCallWorkflowInput{
		ToolID: toolID, ServerID: serverID, ToolName: "echo",
		Arguments:  map[string]interface{}{},
		Timeout:    30, RequestID: "req-fail",
		AgentID: "agent-1", AgentRole: "worker",
		WorkflowID: "wf-1", RunID: "run-1", TaskID: "task-1",
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
	var result MCPToolCallWorkflowResult
	env.GetWorkflowResult(&result)
	if result.AuditID != "audit-fail" {
		t.Errorf("audit NOT recorded on failure: got audit_id=%q", result.AuditID)
	}
	if result.Success {
		t.Error("expected workflow to report failure")
	}
}

func TestMCPToolCallWorkflow_ResultOverflow(t *testing.T) {
	env, _, mcp := setupMCPWorkflowTest(t)
	serverID := uuid.New().String()
	toolID := uuid.New().String()

	env.OnActivity(mcp.CallMCPTool, mock.Anything, mock.Anything).
		Return(&activities.CallMCPToolActivityResult{
			Result: &types.MCPToolResult{
				ToolID: toolID, ToolName: "get_data", ServerID: serverID,
				RequestID: "req-overflow", Content: "truncated...", Success: true,
				DurationMs: 100, Overflow: true, TruncatedAt: 65536,
			},
		}, nil)

	env.OnActivity(mcp.WorkspaceAppend, mock.Anything, mock.Anything).
		Return(&activities.WorkspaceAppendActivityResult{ItemID: "ws-overflow"}, nil)

	env.OnActivity(mcp.AuditMCPToolCall, mock.Anything, mock.Anything).
		Return(&activities.AuditMCPToolCallActivityResult{AuditID: "audit-overflow"}, nil)

	env.ExecuteWorkflow(MCPToolCallWorkflowName, MCPToolCallWorkflowInput{
		ToolID: toolID, ServerID: serverID, ToolName: "get_data",
		Arguments:  map[string]interface{}{},
		Timeout:    30, RequestID: "req-overflow",
		AgentID: "agent-1", AgentRole: "worker",
		WorkflowID: "wf-1", RunID: "run-1", TaskID: "task-1",
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
	var result MCPToolCallWorkflowResult
	env.GetWorkflowResult(&result)
	if result.ToolResult == nil || !result.ToolResult.Overflow {
		t.Error("expected overflow flag in tool result")
	}
}

func TestMCPToolCallWorkflow_ServerNotFound(t *testing.T) {
	env, _, mcp := setupMCPWorkflowTest(t)
	serverID := uuid.New().String()
	toolID := uuid.New().String()

	env.OnActivity(mcp.CallMCPTool, mock.Anything, mock.Anything).
		Return(&activities.CallMCPToolActivityResult{
			Result: &types.MCPToolResult{
				ToolID: toolID, ToolName: "echo", ServerID: serverID,
				RequestID: "req-snf", Success: false,
				Error: "MCP server not found", ErrorType: types.MCPErrorTypeServerNotFound,
				DurationMs: 1,
			},
		}, nil)

	env.OnActivity(mcp.AuditMCPToolCall, mock.Anything, mock.Anything).
		Return(&activities.AuditMCPToolCallActivityResult{AuditID: "audit-snf"}, nil)

	env.ExecuteWorkflow(MCPToolCallWorkflowName, MCPToolCallWorkflowInput{
		ToolID: toolID, ServerID: serverID, ToolName: "echo",
		Arguments:  map[string]interface{}{"message": "x"},
		Timeout:    30, RequestID: "req-snf",
		AgentID: "agent-1", AgentRole: "worker",
		WorkflowID: "wf-1", RunID: "run-1", TaskID: "task-1",
	})

	var result MCPToolCallWorkflowResult
	env.GetWorkflowResult(&result)
	if result.AuditID != "audit-snf" {
		t.Errorf("server-not-found: audit NOT recorded, got audit_id=%q", result.AuditID)
	}
}

func TestMCPToolCallWorkflow_PermissionDenied(t *testing.T) {
	env, _, mcp := setupMCPWorkflowTest(t)
	serverID := uuid.New().String()
	toolID := uuid.New().String()

	env.OnActivity(mcp.CallMCPTool, mock.Anything, mock.Anything).
		Return(&activities.CallMCPToolActivityResult{
			Result: &types.MCPToolResult{
				ToolID: toolID, ToolName: "restricted_tool", ServerID: serverID,
				RequestID: "req-pd", Success: false,
				Error: "agent not authorized", ErrorType: types.MCPErrorTypePermissionDenied,
				DurationMs: 1,
			},
		}, nil)

	env.OnActivity(mcp.AuditMCPToolCall, mock.Anything, mock.Anything).
		Return(&activities.AuditMCPToolCallActivityResult{AuditID: "audit-pd"}, nil)

	env.ExecuteWorkflow(MCPToolCallWorkflowName, MCPToolCallWorkflowInput{
		ToolID: toolID, ServerID: serverID, ToolName: "restricted_tool",
		Arguments:  map[string]interface{}{},
		Timeout:    30, RequestID: "req-pd",
		AgentID: "agent-1", AgentRole: "worker",
		WorkflowID: "wf-1", RunID: "run-1", TaskID: "task-1",
	})

	var result MCPToolCallWorkflowResult
	env.GetWorkflowResult(&result)
	if result.AuditID != "audit-pd" {
		t.Errorf("permission-denied: audit NOT recorded, got audit_id=%q", result.AuditID)
	}
}

func TestMCPToolCallWorkflow_WorkspaceAppendFailureHandled(t *testing.T) {
	env, _, mcp := setupMCPWorkflowTest(t)
	serverID := uuid.New().String()
	toolID := uuid.New().String()

	env.OnActivity(mcp.CallMCPTool, mock.Anything, mock.Anything).
		Return(&activities.CallMCPToolActivityResult{
			Result: &types.MCPToolResult{
				ToolID: toolID, ToolName: "echo", ServerID: serverID,
				RequestID: "req-wsf", Content: "result", Success: true, DurationMs: 5,
			},
		}, nil)

	env.OnActivity(mcp.WorkspaceAppend, mock.Anything, mock.Anything).
		Return(nil, errors.New("workspace write failed"))

	env.OnActivity(mcp.AuditMCPToolCall, mock.Anything, mock.Anything).
		Return(&activities.AuditMCPToolCallActivityResult{AuditID: "audit-wsf"}, nil)

	env.ExecuteWorkflow(MCPToolCallWorkflowName, MCPToolCallWorkflowInput{
		ToolID: toolID, ServerID: serverID, ToolName: "echo",
		Arguments:  map[string]interface{}{"message": "x"},
		Timeout:    30, RequestID: "req-wsf",
		AgentID: "agent-1", AgentRole: "worker",
		WorkflowID: "wf-1", RunID: "run-1", TaskID: "task-1",
	})

	var result MCPToolCallWorkflowResult
	env.GetWorkflowResult(&result)
	if result.AuditID != "audit-wsf" {
		t.Errorf("workspace-append-failure: audit NOT recorded, got audit_id=%q", result.AuditID)
	}
	if result.WorkspaceRef != "" {
		t.Errorf("workspace ref should be empty on failure, got %q", result.WorkspaceRef)
	}
}
