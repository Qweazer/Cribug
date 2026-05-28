package activities

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"cribug/internal/skillclient"

	"github.com/google/uuid"
	"go.temporal.io/sdk/testsuite"
)

// ── Helpers ────────────────────────────────────────────────────────────

// startSkillsServer starts an httptest.Server that responds to
// GET  /tools          → ListSkills
// GET  /tools/{name}   → GetSkillMetadata
// POST /tools/{name}/execute → ExecuteSkill
func startSkillsServer(t *testing.T, skills []string, meta *skillclient.SkillMetadata, execResp *skillclient.ExecuteResponse) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			if r.URL.Path == "/tools" {
				json.NewEncoder(w).Encode(skills)
				return
			}
			// /tools/{name}
			if meta != nil {
				json.NewEncoder(w).Encode(meta)
			} else {
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(map[string]string{"error": "skill not found"})
			}
		case http.MethodPost:
			if execResp != nil {
				// Check if the response signals a 404
				if execResp.ErrorType == "tool_not_found" {
					w.WriteHeader(http.StatusNotFound)
				}
				json.NewEncoder(w).Encode(execResp)
			} else {
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
			}
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
}

func newSkillActivitiesForTest(t *testing.T, serverURL string) *SkillActivities {
	t.Helper()
	client := skillclient.NewClient(serverURL)
	return &SkillActivities{
		client: client,
		db:     nil,
		redis:  nil,
	}
}

// ── Tests ──────────────────────────────────────────────────────────────

func TestListSkillsActivity(t *testing.T) {
	expected := []string{"skill-a", "skill-b", "skill-c"}
	srv := startSkillsServer(t, expected, nil, nil)
	defer srv.Close()

	sa := newSkillActivitiesForTest(t, srv.URL)
	suite := testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(sa.ListSkillsActivity)

	val, err := env.ExecuteActivity(sa.ListSkillsActivity, ListSkillsInput{})
	if err != nil {
		t.Fatalf("ListSkillsActivity failed: %v", err)
	}
	var out ListSkillsOutput
	val.Get(&out)

	if len(out.Skills) != len(expected) {
		t.Fatalf("got %d skills, want %d", len(out.Skills), len(expected))
	}
	for i, s := range out.Skills {
		if s != expected[i] {
			t.Errorf("skill[%d] = %q, want %q", i, s, expected[i])
		}
	}
}

func TestListSkillsActivity_Empty(t *testing.T) {
	srv := startSkillsServer(t, []string{}, nil, nil)
	defer srv.Close()

	sa := newSkillActivitiesForTest(t, srv.URL)
	suite := testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(sa.ListSkillsActivity)

	val, err := env.ExecuteActivity(sa.ListSkillsActivity, ListSkillsInput{})
	if err != nil {
		t.Fatalf("ListSkillsActivity failed: %v", err)
	}
	var out ListSkillsOutput
	val.Get(&out)
	if out.Skills == nil {
		t.Error("expected empty slice, not nil")
	}
	if len(out.Skills) != 0 {
		t.Errorf("expected 0 skills, got %d", len(out.Skills))
	}
}

func TestGetSkillActivity(t *testing.T) {
	meta := &skillclient.SkillMetadata{
		Name:            "test-skill",
		Version:         "1.0.0",
		Description:     "a test skill",
		Category:        "general",
		ExecutionMode:   "sandbox",
		RequiresSandbox: true,
		RequiresLLM:     false,
		RiskLevel:       "low",
		SideEffects:     false,
		Parameters:      map[string]interface{}{"key": "value"},
	}
	srv := startSkillsServer(t, nil, meta, nil)
	defer srv.Close()

	sa := newSkillActivitiesForTest(t, srv.URL)
	suite := testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(sa.GetSkillActivity)

	val, err := env.ExecuteActivity(sa.GetSkillActivity, GetSkillInput{SkillName: "test-skill"})
	if err != nil {
		t.Fatalf("GetSkillActivity failed: %v", err)
	}
	var out GetSkillOutput
	val.Get(&out)

	if out.Metadata == nil {
		t.Fatal("expected non-nil metadata")
	}
	if out.Metadata.Name != "test-skill" {
		t.Errorf("name = %q, want %q", out.Metadata.Name, "test-skill")
	}
}

func TestGetSkillActivity_NotFound(t *testing.T) {
	srv := startSkillsServer(t, nil, nil, nil)
	defer srv.Close()

	sa := newSkillActivitiesForTest(t, srv.URL)
	suite := testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(sa.GetSkillActivity)

	_, err := env.ExecuteActivity(sa.GetSkillActivity, GetSkillInput{SkillName: "nonexistent"})
	if err == nil {
		t.Fatal("expected error for nonexistent skill")
	}
}

func TestExecuteSkillActivity(t *testing.T) {
	execResp := &skillclient.ExecuteResponse{
		ToolName:        "test-skill",
		RequestID:       "req-abc",
		Success:         true,
		Output:          map[string]interface{}{"result": "hello"},
		Error:           "",
		ErrorType:       "",
		ExecutionTimeMs: 150,
		Overflow:        false,
		Metadata: map[string]interface{}{
			"provider": "openai",
			"model":    "gpt-4",
			"token_usage": map[string]interface{}{
				"prompt_tokens":     100.0,
				"completion_tokens": 50.0,
				"total_tokens":      150.0,
			},
		},
	}
	srv := startSkillsServer(t, nil, nil, execResp)
	defer srv.Close()

	sa := newSkillActivitiesForTest(t, srv.URL)
	suite := testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(sa.ExecuteSkillActivity)

	val, err := env.ExecuteActivity(sa.ExecuteSkillActivity, ExecuteSkillInput{
		SkillName:  "test-skill",
		Parameters: map[string]interface{}{"input": "data"},
		RequestID:  "req-abc",
	})
	if err != nil {
		t.Fatalf("ExecuteSkillActivity failed: %v", err)
	}
	var out ExecuteSkillOutput
	val.Get(&out)

	if !out.Success {
		t.Errorf("expected success")
	}
	if out.RequestID != "req-abc" {
		t.Errorf("request_id = %q, want %q", out.RequestID, "req-abc")
	}
	if out.Provider != "openai" {
		t.Errorf("provider = %q, want %q", out.Provider, "openai")
	}
	if out.Model != "gpt-4" {
		t.Errorf("model = %q, want %q", out.Model, "gpt-4")
	}
	if out.TokenUsage["prompt_tokens"] != 100 {
		t.Errorf("prompt_tokens = %d, want 100", out.TokenUsage["prompt_tokens"])
	}
	if out.TokenUsage["total_tokens"] != 150 {
		t.Errorf("total_tokens = %d, want 150", out.TokenUsage["total_tokens"])
	}
}

func TestExecuteSkillActivity_NotFound(t *testing.T) {
	execResp := &skillclient.ExecuteResponse{
		Success:   false,
		Error:     "skill not found",
		ErrorType: "tool_not_found",
	}
	srv := startSkillsServer(t, nil, nil, execResp)
	defer srv.Close()

	sa := newSkillActivitiesForTest(t, srv.URL)
	suite := testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(sa.ExecuteSkillActivity)

	val, err := env.ExecuteActivity(sa.ExecuteSkillActivity, ExecuteSkillInput{
		SkillName:  "nonexistent",
		Parameters: map[string]interface{}{},
		RequestID:  "req-notfound",
	})
	if err != nil {
		t.Fatalf("ExecuteSkillActivity should not error: %v", err)
	}
	var out ExecuteSkillOutput
	val.Get(&out)

	if out.Success {
		t.Error("expected failure")
	}
	if out.ErrorType != "tool_not_found" {
		t.Errorf("error_type = %q, want %q", out.ErrorType, "tool_not_found")
	}
	if out.Error == "" {
		t.Error("expected non-empty error message")
	}
}

func TestExecuteSkillActivity_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "internal error"})
	}))
	defer srv.Close()

	sa := newSkillActivitiesForTest(t, srv.URL)
	suite := testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(sa.ExecuteSkillActivity)

	val, err := env.ExecuteActivity(sa.ExecuteSkillActivity, ExecuteSkillInput{
		SkillName:  "failing-skill",
		Parameters: map[string]interface{}{},
		RequestID:  "req-httpfail",
	})
	if err != nil {
		t.Fatalf("ExecuteSkillActivity should not error on HTTP error: %v", err)
	}
	var out ExecuteSkillOutput
	val.Get(&out)

	if out.Success {
		t.Error("expected failure")
	}
	if out.ErrorType != "http_error" {
		t.Errorf("error_type = %q, want %q", out.ErrorType, "http_error")
	}
}

func TestExecuteSkillActivity_ClientError(t *testing.T) {
	// When the server is unreachable, client.ExecuteSkill returns a real error
	// which ExecuteSkillActivity wraps with error_type "http_error".
	sa := newSkillActivitiesForTest(t, "http://127.0.0.1:1")
	suite := testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(sa.ExecuteSkillActivity)

	val, err := env.ExecuteActivity(sa.ExecuteSkillActivity, ExecuteSkillInput{
		SkillName:  "test",
		Parameters: map[string]interface{}{},
		RequestID:  "req-clienterr",
	})
	if err != nil {
		t.Fatalf("ExecuteSkillActivity should not propagate client errors: %v", err)
	}
	var out ExecuteSkillOutput
	val.Get(&out)

	if out.Success {
		t.Error("expected failure for unreachable server")
	}
	if out.ErrorType != "http_error" {
		t.Errorf("error_type = %q, want %q", out.ErrorType, "http_error")
	}
	if out.Error == "" {
		t.Error("expected non-empty error message")
	}
}

func TestAuditSkillExecutionActivity_NilDB(t *testing.T) {
	// With a nil db, AuditSkillExecutionActivity generates a fallback audit ID.
	sa := &SkillActivities{client: nil, db: nil, redis: nil}
	suite := testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(sa.AuditSkillExecutionActivity)

	val, err := env.ExecuteActivity(sa.AuditSkillExecutionActivity, AuditSkillInput{
		SkillName:  "test-skill",
		AgentID:    "agent-1",
		WorkflowID: "wf-1",
		RequestID:  "req-1",
		Success:    true,
		DurationMs: 100,
		Provider:   "openai",
		Model:      "gpt-4",
		TokenUsage: map[string]int{"prompt_tokens": 50, "completion_tokens": 30},
	})
	if err != nil {
		t.Fatalf("AuditSkillExecutionActivity failed: %v", err)
	}
	var out AuditSkillOutput
	val.Get(&out)

	if out.AuditID == "" {
		t.Fatal("expected non-empty audit ID")
	}
}

func TestWorkspaceAppendSkillResultActivity_NilRedis(t *testing.T) {
	sa := &SkillActivities{client: nil, db: nil, redis: nil}
	suite := testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(sa.WorkspaceAppendSkillResultActivity)

	val, err := env.ExecuteActivity(sa.WorkspaceAppendSkillResultActivity, WorkspaceAppendInput{
		TaskID:     "task-1",
		WorkflowID: "wf-1",
		AgentID:    "agent-1",
		SkillName:  "test-skill",
		RequestID:  "req-1",
		Result:     map[string]string{"output": "done"},
		Success:    true,
	})
	if err != nil {
		t.Fatalf("WorkspaceAppendSkillResultActivity failed: %v", err)
	}
	var out WorkspaceAppendOutput
	val.Get(&out)

	if out.ItemID == "" {
		t.Fatal("expected non-empty item ID")
	}
	// Verify it's a valid UUID
	if _, err := uuid.Parse(out.ItemID); err != nil {
		t.Errorf("item ID is not a valid UUID: %v", err)
	}
}

func TestWorkspaceAppendSkillResultActivity_OversizedResult(t *testing.T) {
	sa := &SkillActivities{client: nil, db: nil, redis: nil}
	suite := testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(sa.WorkspaceAppendSkillResultActivity)

	// Create a result larger than 64KB
	bigData := make([]byte, 70000)
	for i := range bigData {
		bigData[i] = byte('A' + i%26)
	}

	val, err := env.ExecuteActivity(sa.WorkspaceAppendSkillResultActivity, WorkspaceAppendInput{
		TaskID:     "task-big",
		WorkflowID: "wf-1",
		AgentID:    "agent-1",
		SkillName:  "big-output-skill",
		RequestID:  "req-big",
		Result:     string(bigData),
		Success:    true,
	})
	if err != nil {
		t.Fatalf("WorkspaceAppendSkillResultActivity failed: %v", err)
	}
	var out WorkspaceAppendOutput
	val.Get(&out)

	if out.ItemID == "" {
		t.Fatal("expected non-empty item ID")
	}
}
