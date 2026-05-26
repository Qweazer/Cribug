package workflows

import (
	"errors"
	"testing"

	"cribug/internal/activities"
	"cribug/internal/types"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func setupSandboxTest(t *testing.T) (*testsuite.TestWorkflowEnvironment, *activities.SandboxActivities) {
	t.Helper()
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()
	wf := NewSandboxWorkflow()
	env.RegisterWorkflowWithOptions(wf.Execute, workflow.RegisterOptions{Name: SandboxWorkflowName})

	sand := &activities.SandboxActivities{}
	env.RegisterActivityWithOptions(sand.ExecuteSandbox, activity.RegisterOptions{Name: "ExecuteSandboxActivity"})
	env.RegisterActivityWithOptions(sand.AuditSandbox, activity.RegisterOptions{Name: "AuditSandboxActivity"})
	env.RegisterActivityWithOptions(sand.WorkspaceAppendForSandbox, activity.RegisterOptions{Name: "SandboxWorkspaceAppendActivity"})

	return env, sand
}

func TestSandboxWorkflow_Success(t *testing.T) {
	env, sand := setupSandboxTest(t)

	env.OnActivity(sand.ExecuteSandbox, mock.Anything, mock.Anything).
		Return(&activities.ExecuteSandboxActivityResult{
			Result: &types.SandboxExecutionResult{
				RequestID: "req-1", Stdout: "hello", Stderr: "",
				ExitCode: 0, Success: true, DurationMs: 10,
			},
		}, nil)

	env.OnActivity(sand.WorkspaceAppendForSandbox, mock.Anything, mock.Anything).
		Return(&activities.SandboxWorkspaceAppendResult{ItemID: "ws-1"}, nil)

	env.OnActivity(sand.AuditSandbox, mock.Anything, mock.Anything).
		Return(&activities.AuditSandboxActivityResult{AuditID: "audit-1"}, nil)

	env.ExecuteWorkflow(SandboxWorkflowName, SandboxWorkflowInput{
		Code: "(module)", Language: "wasi", RequestID: "req-1",
		AgentID: "a1", AgentRole: "lead",
		WorkflowID: "wf-1", RunID: "run-1", TaskID: "task-1",
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}

	var result SandboxWorkflowResult
	env.GetWorkflowResult(&result)
	if !result.Success {
		t.Errorf("expected success, got: %s", result.Error)
	}
	if result.AuditID != "audit-1" {
		t.Error("expected audit")
	}
}

func TestSandboxWorkflow_FailureStillAudited(t *testing.T) {
	env, sand := setupSandboxTest(t)

	env.OnActivity(sand.ExecuteSandbox, mock.Anything, mock.Anything).
		Return(&activities.ExecuteSandboxActivityResult{
			Result: &types.SandboxExecutionResult{
				RequestID: "req-fail", Stdout: "", Stderr: "error",
				ExitCode: 1, Success: false, Error: "timeout",
				ErrorType: types.SandboxErrorTypeTimeout, DurationMs: 30000,
			},
		}, nil)

	env.OnActivity(sand.WorkspaceAppendForSandbox, mock.Anything, mock.Anything).
		Return(&activities.SandboxWorkspaceAppendResult{ItemID: "ws-fail"}, nil)

	env.OnActivity(sand.AuditSandbox, mock.Anything, mock.Anything).
		Return(&activities.AuditSandboxActivityResult{AuditID: "audit-fail"}, nil)

	env.ExecuteWorkflow(SandboxWorkflowName, SandboxWorkflowInput{
		Code: "bad code", Language: "python", RequestID: "req-fail",
		AgentID: "a1", AgentRole: "lead",
		WorkflowID: "wf-1", RunID: "run-1", TaskID: "task-1",
	})

	var result SandboxWorkflowResult
	env.GetWorkflowResult(&result)
	if result.AuditID != "audit-fail" {
		t.Errorf("failure audit NOT recorded: got audit_id=%q", result.AuditID)
	}
	if result.Success {
		t.Error("expected failure")
	}
}

func TestSandboxWorkflow_NonZeroExitCode(t *testing.T) {
	env, sand := setupSandboxTest(t)

	env.OnActivity(sand.ExecuteSandbox, mock.Anything, mock.Anything).
		Return(&activities.ExecuteSandboxActivityResult{
			Result: &types.SandboxExecutionResult{
				RequestID: "req-exit", Stdout: "", Stderr: "panic",
				ExitCode: 1, Success: false, DurationMs: 5,
			},
		}, nil)

	env.OnActivity(sand.WorkspaceAppendForSandbox, mock.Anything, mock.Anything).
		Return(&activities.SandboxWorkspaceAppendResult{}, errors.New("ws error"))

	env.OnActivity(sand.AuditSandbox, mock.Anything, mock.Anything).
		Return(&activities.AuditSandboxActivityResult{AuditID: "audit-exit"}, nil)

	env.ExecuteWorkflow(SandboxWorkflowName, SandboxWorkflowInput{
		Code: "exit(1)", Language: "python", RequestID: "req-exit",
		AgentID: "a1", AgentRole: "lead",
		WorkflowID: "wf-1", RunID: "run-1", TaskID: "task-1",
	})

	var result SandboxWorkflowResult
	env.GetWorkflowResult(&result)
	if result.AuditID != "audit-exit" {
		t.Errorf("non-zero exit: audit NOT recorded")
	}
	if result.Result == nil || result.Result.ExitCode != 1 {
		t.Error("expected exit code 1")
	}
}

func TestSandboxWorkflow_ResultSizeLimit(t *testing.T) {
	env, sand := setupSandboxTest(t)

	bigOutput := make([]byte, types.DefaultSandboxResultMaxBytes+100)

	env.OnActivity(sand.ExecuteSandbox, mock.Anything, mock.Anything).
		Return(&activities.ExecuteSandboxActivityResult{
			Result: &types.SandboxExecutionResult{
				RequestID: "req-big", Stdout: string(bigOutput), Stderr: "",
				ExitCode: 0, Success: true, StdoutOverflow: true, DurationMs: 100,
			},
		}, nil)

	env.OnActivity(sand.WorkspaceAppendForSandbox, mock.Anything, mock.Anything).
		Return(&activities.SandboxWorkspaceAppendResult{ItemID: "ws-big"}, nil)

	env.OnActivity(sand.AuditSandbox, mock.Anything, mock.Anything).
		Return(&activities.AuditSandboxActivityResult{AuditID: "audit-big"}, nil)

	env.ExecuteWorkflow(SandboxWorkflowName, SandboxWorkflowInput{
		Code: "generate_large_output()", Language: "python", RequestID: "req-big",
		AgentID: "a1", AgentRole: "lead",
		WorkflowID: "wf-1", RunID: "run-1", TaskID: "task-1",
	})

	var result SandboxWorkflowResult
	env.GetWorkflowResult(&result)
	if result.Result == nil || !result.Result.StdoutOverflow {
		t.Error("expected stdout overflow flag")
	}
}
