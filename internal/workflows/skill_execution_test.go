package workflows

import (
	"errors"
	"testing"

	"cribug/internal/activities"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func setupSkillExecutionTest(t *testing.T) (*testsuite.TestWorkflowEnvironment, *activities.SkillActivities) {
	t.Helper()
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()
	env.RegisterWorkflowWithOptions(SkillExecutionWorkflow, workflow.RegisterOptions{Name: SkillExecutionWorkflowName})

	skill := &activities.SkillActivities{}

	// Register nil hook for EmitHookEventActivity so it returns zero-values instead of error
	env.RegisterActivityWithOptions(
		func(ctx interface{}, input interface{}) (interface{}, error) {
			return struct {
				Decision struct {
					Denied bool
				}
			}{}, nil
		},
		activity.RegisterOptions{Name: "EmitHookEventActivity"},
	)

	return env, skill
}

func registerSkillActivities(env *testsuite.TestWorkflowEnvironment, skill *activities.SkillActivities) {
	env.RegisterActivityWithOptions(skill.ExecuteSkillActivity, activity.RegisterOptions{Name: "ExecuteSkillActivity"})
	env.RegisterActivityWithOptions(skill.AuditSkillExecutionActivity, activity.RegisterOptions{Name: "AuditSkillExecutionActivity"})
	env.RegisterActivityWithOptions(skill.WorkspaceAppendSkillResultActivity, activity.RegisterOptions{Name: "WorkspaceAppendSkillResultActivity"})
}

func TestSkillExecutionWorkflow_Success(t *testing.T) {
	env, skill := setupSkillExecutionTest(t)
	registerSkillActivities(env, skill)

	env.OnActivity(skill.ExecuteSkillActivity, mock.Anything, mock.Anything).
		Return(activities.ExecuteSkillOutput{
			RequestID:       "req-1",
			Success:         true,
			Output:          map[string]interface{}{"result": "ok"},
			ExecutionTimeMs: 150,
			Provider:        "test-provider",
			Model:           "test-model",
			TokenUsage:      map[string]int{"prompt_tokens": 10, "completion_tokens": 20},
		}, nil)

	env.OnActivity(skill.AuditSkillExecutionActivity, mock.Anything, mock.Anything).
		Return(activities.AuditSkillOutput{AuditID: "audit-1"}, nil)

	env.OnActivity(skill.WorkspaceAppendSkillResultActivity, mock.Anything, mock.Anything).
		Return(activities.WorkspaceAppendOutput{ItemID: "ws-item-1"}, nil)

	env.ExecuteWorkflow(SkillExecutionWorkflowName, SkillExecutionWorkflowInput{
		SkillName:  "test_skill",
		Parameters: map[string]interface{}{"param1": "value1"},
		TaskID:     "task-1",
		AgentID:    "agent-1",
		WorkflowID: "wf-1",
		RequestID:  "req-1",
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}

	var result SkillExecutionWorkflowOutput
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("workflow result error: %v", err)
	}
	if !result.Success {
		t.Errorf("expected success, got error: %s", result.Error)
	}
	if result.AuditID != "audit-1" {
		t.Errorf("expected audit ID 'audit-1', got '%s'", result.AuditID)
	}
	if result.WorkspaceItemID != "ws-item-1" {
		t.Errorf("expected workspace item ID 'ws-item-1', got '%s'", result.WorkspaceItemID)
	}
	if result.RequestID != "req-1" {
		t.Errorf("expected request ID 'req-1', got '%s'", result.RequestID)
	}
}

func TestSkillExecutionWorkflow_ActivityError(t *testing.T) {
	env, skill := setupSkillExecutionTest(t)
	registerSkillActivities(env, skill)

	// ExecuteSkillActivity returns a top-level error
	env.OnActivity(skill.ExecuteSkillActivity, mock.Anything, mock.Anything).
		Return(activities.ExecuteSkillOutput{}, errors.New("connection refused"))

	// Audit must still happen on activity error
	env.OnActivity(skill.AuditSkillExecutionActivity, mock.Anything, mock.Anything).
		Return(activities.AuditSkillOutput{AuditID: "audit-fail"}, nil)

	env.ExecuteWorkflow(SkillExecutionWorkflowName, SkillExecutionWorkflowInput{
		SkillName:  "test_skill",
		Parameters: map[string]interface{}{},
		TaskID:     "task-1",
		AgentID:    "agent-1",
		WorkflowID: "wf-1",
		RequestID:  "req-fail",
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}

	var result SkillExecutionWorkflowOutput
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("workflow result error: %v", err)
	}
	if result.Success {
		t.Error("expected workflow to report failure on activity error")
	}
	if result.Error == "" {
		t.Error("expected non-empty error message on activity error")
	}
}

func TestSkillExecutionWorkflow_PythonExecutionFailure(t *testing.T) {
	env, skill := setupSkillExecutionTest(t)
	registerSkillActivities(env, skill)

	// Skill executed but returned failure (no top-level error)
	env.OnActivity(skill.ExecuteSkillActivity, mock.Anything, mock.Anything).
		Return(activities.ExecuteSkillOutput{
			RequestID:       "req-pfail",
			Success:         false,
			Output:          nil,
			Error:           "skill logic error: invalid input",
			ErrorType:       "validation_error",
			ExecutionTimeMs: 50,
		}, nil)

	// Audit must still happen
	env.OnActivity(skill.AuditSkillExecutionActivity, mock.Anything, mock.Anything).
		Return(activities.AuditSkillOutput{AuditID: "audit-pfail"}, nil)

	env.ExecuteWorkflow(SkillExecutionWorkflowName, SkillExecutionWorkflowInput{
		SkillName:  "test_skill",
		Parameters: map[string]interface{}{"bad": "param"},
		TaskID:     "task-1",
		AgentID:    "agent-1",
		WorkflowID: "wf-1",
		RequestID:  "req-pfail",
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}

	var result SkillExecutionWorkflowOutput
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("workflow result error: %v", err)
	}
	if result.Success {
		t.Error("expected workflow to report failure on skill execution failure")
	}
	if result.Error != "skill logic error: invalid input" {
		t.Errorf("expected error message to be propagated, got: %s", result.Error)
	}
	// Workspace should NOT be appended on failure
	if result.WorkspaceItemID != "" {
		t.Errorf("expected empty workspace item ID on failure, got '%s'", result.WorkspaceItemID)
	}
}

func TestSkillExecutionWorkflow_WorkspaceAppendFailure(t *testing.T) {
	env, skill := setupSkillExecutionTest(t)
	registerSkillActivities(env, skill)

	env.OnActivity(skill.ExecuteSkillActivity, mock.Anything, mock.Anything).
		Return(activities.ExecuteSkillOutput{
			RequestID:       "req-wsfail",
			Success:         true,
			Output:          "some result",
			ExecutionTimeMs: 100,
		}, nil)

	// Workspace append fails but audit must still succeed
	env.OnActivity(skill.WorkspaceAppendSkillResultActivity, mock.Anything, mock.Anything).
		Return(activities.WorkspaceAppendOutput{}, errors.New("redis stream write failed"))

	env.OnActivity(skill.AuditSkillExecutionActivity, mock.Anything, mock.Anything).
		Return(activities.AuditSkillOutput{AuditID: "audit-wsfail"}, nil)

	env.ExecuteWorkflow(SkillExecutionWorkflowName, SkillExecutionWorkflowInput{
		SkillName:  "test_skill",
		Parameters: map[string]interface{}{},
		TaskID:     "task-1",
		AgentID:    "agent-1",
		WorkflowID: "wf-1",
		RequestID:  "req-wsfail",
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}

	var result SkillExecutionWorkflowOutput
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("workflow result error: %v", err)
	}
	if !result.Success {
		t.Errorf("expected overall success even when workspace append fails")
	}
	if result.AuditID != "audit-wsfail" {
		t.Errorf("expected audit to be recorded even when workspace append fails, got audit_id=%q", result.AuditID)
	}
	if result.WorkspaceItemID != "" {
		t.Errorf("expected empty workspace item ID on append failure, got '%s'", result.WorkspaceItemID)
	}
}

func TestSkillExecutionWorkflow_AuditFailureDoesNotPanic(t *testing.T) {
	env, skill := setupSkillExecutionTest(t)
	registerSkillActivities(env, skill)

	env.OnActivity(skill.ExecuteSkillActivity, mock.Anything, mock.Anything).
		Return(activities.ExecuteSkillOutput{
			RequestID:       "req-auditfail",
			Success:         true,
			Output:          "ok",
			ExecutionTimeMs: 10,
		}, nil)

	env.OnActivity(skill.WorkspaceAppendSkillResultActivity, mock.Anything, mock.Anything).
		Return(activities.WorkspaceAppendOutput{ItemID: "ws-auditfail"}, nil)

	// Audit fails
	env.OnActivity(skill.AuditSkillExecutionActivity, mock.Anything, mock.Anything).
		Return(activities.AuditSkillOutput{}, errors.New("db write failed"))

	env.ExecuteWorkflow(SkillExecutionWorkflowName, SkillExecutionWorkflowInput{
		SkillName:  "test_skill",
		Parameters: map[string]interface{}{},
		TaskID:     "task-1",
		AgentID:    "agent-1",
		WorkflowID: "wf-1",
		RequestID:  "req-auditfail",
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}

	var result SkillExecutionWorkflowOutput
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("workflow result error: %v", err)
	}
	if !result.Success {
		t.Errorf("expected overall success even when audit fails")
	}
	if result.AuditID != "" {
		t.Errorf("expected empty audit ID on audit failure, got '%s'", result.AuditID)
	}
	if result.WorkspaceItemID != "ws-auditfail" {
		t.Errorf("expected workspace item to be appended even when audit fails")
	}
}

func TestSkillExecutionWorkflow_WithRAGContext(t *testing.T) {
	env, skill := setupSkillExecutionTest(t)
	registerSkillActivities(env, skill)

	rag := &activities.RAGRetrievalActivities{}
	env.RegisterActivityWithOptions(rag.EmbedAndSearchChunksActivity, activity.RegisterOptions{Name: "EmbedAndSearchChunksActivity"})
	env.RegisterActivityWithOptions(rag.FetchChunkContentActivity, activity.RegisterOptions{Name: "FetchChunkContentActivity"})
	env.RegisterActivityWithOptions(rag.PackContextActivity, activity.RegisterOptions{Name: "PackContextActivity"})

	env.OnActivity(rag.EmbedAndSearchChunksActivity, mock.Anything, mock.Anything).
		Return(activities.EmbedAndSearchChunksOutput{
			Hits: []activities.SearchHit{
				{ChunkID: "chunk-1", Score: 0.95},
			},
		}, nil)

	env.OnActivity(rag.FetchChunkContentActivity, mock.Anything, mock.Anything).
		Return(activities.FetchChunkContentOutput{
			Chunks: []activities.ChunkContent{
				{ChunkID: "chunk-1", DocumentID: "doc-1", Content: "context content", Title: "Doc 1", ChunkIndex: 0},
			},
		}, nil)

	env.OnActivity(rag.PackContextActivity, mock.Anything, mock.Anything).
		Return(activities.PackContextOutput{
			Context:       "packed context result",
			Citations:     []string{"[1] Source: Doc 1 (chunk 0)"},
			TokenEstimate: 10,
			ChunkCount:    1,
		}, nil)

	var capturedInput activities.ExecuteSkillInput
	env.OnActivity(skill.ExecuteSkillActivity, mock.Anything, mock.MatchedBy(func(input activities.ExecuteSkillInput) bool {
		capturedInput = input
		return true
	})).
		Return(activities.ExecuteSkillOutput{
			RequestID:       "req-ctx",
			Success:         true,
			Output:          map[string]interface{}{"result": "ok"},
			ExecutionTimeMs: 150,
		}, nil)

	env.OnActivity(skill.AuditSkillExecutionActivity, mock.Anything, mock.Anything).
		Return(activities.AuditSkillOutput{AuditID: "audit-ctx"}, nil)

	env.OnActivity(skill.WorkspaceAppendSkillResultActivity, mock.Anything, mock.Anything).
		Return(activities.WorkspaceAppendOutput{ItemID: "ws-ctx"}, nil)

	env.ExecuteWorkflow(SkillExecutionWorkflowName, SkillExecutionWorkflowInput{
		SkillName:  "test_skill",
		Parameters: map[string]interface{}{"retrieve_context": true, "param1": "value1"},
		TaskID:     "task-1",
		AgentID:    "agent-1",
		WorkflowID: "wf-1",
		RequestID:  "req-ctx",
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}

	var result SkillExecutionWorkflowOutput
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("workflow result error: %v", err)
	}
	if !result.Success {
		t.Errorf("expected success, got error: %s", result.Error)
	}

	// Verify context was injected and retrieve_context was removed
	if ctx, ok := capturedInput.Parameters["context"]; !ok {
		t.Error("expected context to be injected")
	} else if ctx != "packed context result" {
		t.Errorf("expected context 'packed context result', got '%v'", ctx)
	}
	if _, ok := capturedInput.Parameters["retrieve_context"]; ok {
		t.Error("expected retrieve_context to be removed from parameters")
	}
	if _, ok := capturedInput.Parameters["param1"]; !ok {
		t.Error("expected original param1 to be preserved")
	}
}

func TestSkillExecutionWorkflow_WithoutRAGContext(t *testing.T) {
	env, skill := setupSkillExecutionTest(t)
	registerSkillActivities(env, skill)

	var capturedInput activities.ExecuteSkillInput
	env.OnActivity(skill.ExecuteSkillActivity, mock.Anything, mock.MatchedBy(func(input activities.ExecuteSkillInput) bool {
		capturedInput = input
		return true
	})).
		Return(activities.ExecuteSkillOutput{
			RequestID:       "req-noctx",
			Success:         true,
			Output:          map[string]interface{}{"result": "ok"},
			ExecutionTimeMs: 150,
		}, nil)

	env.OnActivity(skill.AuditSkillExecutionActivity, mock.Anything, mock.Anything).
		Return(activities.AuditSkillOutput{AuditID: "audit-noctx"}, nil)

	env.OnActivity(skill.WorkspaceAppendSkillResultActivity, mock.Anything, mock.Anything).
		Return(activities.WorkspaceAppendOutput{ItemID: "ws-noctx"}, nil)

	env.ExecuteWorkflow(SkillExecutionWorkflowName, SkillExecutionWorkflowInput{
		SkillName:  "test_skill",
		Parameters: map[string]interface{}{"param1": "value1"},
		TaskID:     "task-1",
		AgentID:    "agent-1",
		WorkflowID: "wf-1",
		RequestID:  "req-noctx",
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}

	var result SkillExecutionWorkflowOutput
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("workflow result error: %v", err)
	}
	if !result.Success {
		t.Errorf("expected success, got error: %s", result.Error)
	}

	// Verify context was NOT injected
	if _, ok := capturedInput.Parameters["context"]; ok {
		t.Error("expected context to NOT be injected when retrieve_context is not set")
	}
}
