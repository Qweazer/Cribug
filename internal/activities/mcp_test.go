package activities

import (
	"context"
	"database/sql"
	"testing"

	"cribug/internal/db"
	"cribug/internal/types"

	_ "github.com/jackc/pgx/v5/stdlib"
	"go.temporal.io/sdk/testsuite"
)

// newMCPTestEnv creates a Temporal test activity environment with all MCP activities registered.
func newMCPTestEnv(t *testing.T, mcp *MCPActivities) *testsuite.TestActivityEnvironment {
	t.Helper()
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestActivityEnvironment()
	env.RegisterActivity(mcp.RegisterMCPServer)
	env.RegisterActivity(mcp.DiscoverMCPTools)
	env.RegisterActivity(mcp.CallMCPTool)
	env.RegisterActivity(mcp.AuditMCPToolCall)
	env.RegisterActivity(mcp.WorkspaceAppend)
	return env
}

// getTestDB returns a test database connection or skips the test if unavailable.
func getTestDB(t *testing.T) *db.Postgres {
	t.Helper()
	databaseURL := "postgres://admin:admin@localhost:5432/orchestrator?sslmode=disable"
	pg, err := db.New(databaseURL)
	if err != nil {
		t.Skipf("skipping test: cannot connect to database: %v", err)
	}
	if err := pg.Ping(context.Background()); err != nil {
		pg.Close()
		t.Skipf("skipping test: database not reachable: %v", err)
	}
	EnsureMCPTables(pg.Stdlib())
	return pg
}

func cleanupMCPData(t *testing.T, pg *db.Postgres) {
	t.Helper()
	ctx := context.Background()
	pg.Stdlib().ExecContext(ctx, "DELETE FROM mcp_audit_logs")
	pg.Stdlib().ExecContext(ctx, "DELETE FROM mcp_tools")
	pg.Stdlib().ExecContext(ctx, "DELETE FROM mcp_servers")
}

// ── Unit Tests (no DB, no Temporal) ─────────────────────────────

func TestValidateArguments_Required(t *testing.T) {
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"message": map[string]interface{}{"type": "string"},
		},
		"required": []interface{}{"message"},
	}
	if err := validateArguments(schema, map[string]interface{}{}); err == nil {
		t.Error("expected validation error for missing required field")
	}
}

func TestValidateArguments_TypeCheck(t *testing.T) {
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"count": map[string]interface{}{"type": "number"},
		},
	}
	if err := validateArguments(schema, map[string]interface{}{"count": "not-a-number"}); err == nil {
		t.Error("expected type validation error")
	}
}

func TestValidateArguments_Success(t *testing.T) {
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"message": map[string]interface{}{"type": "string"},
		},
		"required": []interface{}{"message"},
	}
	if err := validateArguments(schema, map[string]interface{}{"message": "hello"}); err != nil {
		t.Errorf("unexpected validation error: %v", err)
	}
}

func TestValidateArguments_NonObjectSchema(t *testing.T) {
	schema := map[string]interface{}{"type": "string"}
	if err := validateArguments(schema, map[string]interface{}{}); err != nil {
		t.Errorf("non-object schemas should pass: %v", err)
	}
}

func TestValidateArguments_ArrayType(t *testing.T) {
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"items": map[string]interface{}{"type": "array"},
		},
	}
	if err := validateArguments(schema, map[string]interface{}{"items": []interface{}{1, 2}}); err != nil {
		t.Errorf("array type should validate: %v", err)
	}
}

func TestValidateArguments_BooleanType(t *testing.T) {
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"enabled": map[string]interface{}{"type": "boolean"},
		},
	}
	if err := validateArguments(schema, map[string]interface{}{"enabled": true}); err != nil {
		t.Errorf("boolean type should validate: %v", err)
	}
	if err := validateArguments(schema, map[string]interface{}{"enabled": "not-bool"}); err == nil {
		t.Error("expected type error for boolean field")
	}
}

func TestMockMCPClient_CallTool(t *testing.T) {
	client := &MockMCPClient{}
	srv := types.MCPServer{ID: "s1", Name: "test-server"}

	for _, toolName := range []string{"echo", "get_time", "calculator", "unknown_tool"} {
		t.Run(toolName, func(t *testing.T) {
			result, err := client.CallTool(srv, toolName, map[string]interface{}{"message": "test"}, 30)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if result == "" {
				t.Error("expected non-empty result")
			}
		})
	}
}

func TestMockMCPClient_DiscoverTools(t *testing.T) {
	client := &MockMCPClient{}
	srv := types.MCPServer{ID: "s1", Name: "test-server", URL: "http://localhost:9999"}

	tools, err := client.DiscoverTools(srv)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) < 2 {
		t.Errorf("expected at least 2 tools, got %d", len(tools))
	}
	for _, tool := range tools {
		if tool.Name == "" || tool.ID == "" {
			t.Error("tool should have name and ID")
		}
	}
}

// ── Activity Tests with Temporal Test Environment (require DB) ──

func TestRegisterMCPServerActivity(t *testing.T) {
	pg := getTestDB(t)
	defer cleanupMCPData(t, pg)

	mcp := NewMCPActivities(pg, nil, nil)
	env := newMCPTestEnv(t, mcp)

	t.Run("admin can register", func(t *testing.T) {
		val, err := env.ExecuteActivity(mcp.RegisterMCPServer, RegisterMCPServerActivityInput{
			Name:    "admin-server",
			Command: "python",
			Args:    []string{"-m", "mcp_server"},
			Env:     []string{"API_KEY=secret123", "LOG_LEVEL=debug"},
			URL:     "http://localhost:9999",
			AgentID: "admin-1",
			Role:    "admin",
		})
		if err != nil {
			t.Fatalf("execute activity failed: %v (type=%T)", err, err)
		}
		var result RegisterMCPServerActivityResult
		if err := val.Get(&result); err != nil {
			t.Fatalf("admin registration failed: %v (type=%T)", err, err)
		}
		if result.Server == nil {
			t.Fatal("expected server in result")
		}
		for _, e := range result.Server.Env {
			idx := 0
			for ; idx < len(e) && e[idx] != '='; idx++ {
			}
			if idx < len(e) {
				val := e[idx+1:]
				if val != "***" && val != "" && types.IsSensitiveEnvKey(e[:idx]) {
					t.Errorf("env still contains sensitive value: %s", e)
				}
			}
		}
	})

	t.Run("worker cannot register", func(t *testing.T) {
		_, err := env.ExecuteActivity(mcp.RegisterMCPServer, RegisterMCPServerActivityInput{
			Name: "worker-server", AgentID: "worker-1", Role: "worker",
		})
		if err == nil {
			t.Error("expected error for worker registration")
		}
	})

	t.Run("duplicate name rejected", func(t *testing.T) {
		fut, _ := env.ExecuteActivity(mcp.RegisterMCPServer, RegisterMCPServerActivityInput{
			Name: "dup-server", AgentID: "admin-1", Role: "admin",
		})
		fut.Get(nil)

		_, err := env.ExecuteActivity(mcp.RegisterMCPServer, RegisterMCPServerActivityInput{
			Name: "dup-server", AgentID: "admin-1", Role: "admin",
		})
		if err == nil {
			t.Error("expected duplicate name error")
		}
	})

	t.Run("empty name rejected", func(t *testing.T) {
		_, err := env.ExecuteActivity(mcp.RegisterMCPServer, RegisterMCPServerActivityInput{
			Name: "", AgentID: "admin-1", Role: "admin",
		})
		if err == nil {
			t.Error("expected empty name error")
		}
	})
}

func TestDiscoverMCPToolsActivity(t *testing.T) {
	pg := getTestDB(t)
	defer cleanupMCPData(t, pg)

	mcp := NewMCPActivities(pg, nil, nil)
	env := newMCPTestEnv(t, mcp)

	regVal, err := env.ExecuteActivity(mcp.RegisterMCPServer, RegisterMCPServerActivityInput{
		Name: "discover-server", AgentID: "admin-1", Role: "admin",
	})
	if err != nil {
		t.Fatalf("registration failed: %v", err)
	}
	var regResult RegisterMCPServerActivityResult
	regVal.Get(&regResult)

	t.Run("discover tools", func(t *testing.T) {
		discVal, err := env.ExecuteActivity(mcp.DiscoverMCPTools, DiscoverMCPToolsActivityInput{
			ServerID: regResult.Server.ID,
		})
		if err != nil {
			t.Fatalf("activity execution failed: %v", err)
		}
		var discResult DiscoverMCPToolsActivityResult
		if err := discVal.Get(&discResult); err != nil {
			t.Fatalf("discovery failed: %v", err)
		}
		if len(discResult.Tools) == 0 {
			t.Error("expected non-empty tool list")
		}
	})

	t.Run("nonexistent server", func(t *testing.T) {
		_, err := env.ExecuteActivity(mcp.DiscoverMCPTools, DiscoverMCPToolsActivityInput{
			ServerID: "00000000-0000-0000-0000-000000000000",
		})
		if err == nil {
			t.Error("expected error for nonexistent server")
		}
	})
}

func TestCallMCPToolActivity(t *testing.T) {
	pg := getTestDB(t)
	defer cleanupMCPData(t, pg)

	mcp := NewMCPActivities(pg, nil, nil)
	env := newMCPTestEnv(t, mcp)

	regVal, _ := env.ExecuteActivity(mcp.RegisterMCPServer, RegisterMCPServerActivityInput{
		Name: "call-server", AgentID: "admin-1", Role: "admin",
	})
	var regResult RegisterMCPServerActivityResult
	regVal.Get(&regResult)

	discVal, _ := env.ExecuteActivity(mcp.DiscoverMCPTools, DiscoverMCPToolsActivityInput{
		ServerID: regResult.Server.ID,
	})
	var discResult DiscoverMCPToolsActivityResult
	discVal.Get(&discResult)

	var echoTool *types.MCPTool
	for i, t := range discResult.Tools {
		if t.Name == "echo" {
			echoTool = &discResult.Tools[i]
			break
		}
	}
	if echoTool == nil {
		t.Fatal("echo tool not found after discovery")
	}

	t.Run("successful call", func(t *testing.T) {
		callVal, err := env.ExecuteActivity(mcp.CallMCPTool, CallMCPToolActivityInput{
			ToolID:     echoTool.ID,
			ServerID:   regResult.Server.ID,
			ToolName:   "echo",
			Arguments:  map[string]interface{}{"message": "hello world"},
			Timeout:    30,
			RequestID:  "req-001",
			AgentID:    "agent-1",
			WorkflowID: "wf-001",
		})
		if err != nil {
			t.Fatalf("activity execution failed: %v", err)
		}
		var callResult CallMCPToolActivityResult
		if err := callVal.Get(&callResult); err != nil {
			t.Fatalf("tool call failed: %v", err)
		}
		if !callResult.Result.Success {
			t.Errorf("expected success, got error: %s", callResult.Result.Error)
		}
	})

	t.Run("nonexistent server", func(t *testing.T) {
		_, err := env.ExecuteActivity(mcp.CallMCPTool, CallMCPToolActivityInput{
			ServerID: "00000000-0000-0000-0000-000000000000", ToolName: "echo", RequestID: "req-002",
		})
		if err == nil {
			t.Error("expected error for nonexistent server")
		}
	})

	t.Run("schema validation - missing required", func(t *testing.T) {
		callVal, err := env.ExecuteActivity(mcp.CallMCPTool, CallMCPToolActivityInput{
			ToolID:     echoTool.ID,
			ServerID:   regResult.Server.ID,
			ToolName:   "echo",
			Arguments:  map[string]interface{}{},
			Timeout:    30,
			RequestID:  "req-003",
			AgentID:    "agent-1",
			WorkflowID: "wf-001",
		})
		if err != nil {
			return // Error from activity itself is fine
		}
		var callResult CallMCPToolActivityResult
		callVal.Get(&callResult)
		if callResult.Result != nil && callResult.Result.Success {
			t.Error("expected schema validation failure")
		}
	})
}

func TestAuditMCPToolCallActivity(t *testing.T) {
	pg := getTestDB(t)
	defer cleanupMCPData(t, pg)

	mcp := NewMCPActivities(pg, nil, nil)
	env := newMCPTestEnv(t, mcp)

	t.Run("success audit", func(t *testing.T) {
		val, err := env.ExecuteActivity(mcp.AuditMCPToolCall, AuditMCPToolCallActivityInput{
			ServerID: "a1b2c3d4-e5f6-7890-abcd-ef1234567890", ToolID: "b2c3d4e5-f6a7-8901-bcde-f12345678901", ToolName: "echo",
			AgentID: "agent-1", WorkflowID: "wf-001", RequestID: "req-001",
			Success: true, DurationMs: 150,
		})
		if err != nil {
			t.Fatalf("activity execution failed: %v", err)
		}
		var result AuditMCPToolCallActivityResult
		if err := val.Get(&result); err != nil {
			t.Fatalf("success audit failed: %v", err)
		}
		if result.AuditID == "" {
			t.Error("expected non-empty audit ID")
		}
	})

	t.Run("failure audit", func(t *testing.T) {
		val, err := env.ExecuteActivity(mcp.AuditMCPToolCall, AuditMCPToolCallActivityInput{
			ServerID: "a1b2c3d4-e5f6-7890-abcd-ef1234567890", ToolID: "b2c3d4e5-f6a7-8901-bcde-f12345678901", ToolName: "echo",
			AgentID: "agent-1", WorkflowID: "wf-001", RequestID: "req-002",
			Success: false, Error: "timeout", ErrorType: types.MCPErrorTypeTimeout,
			DurationMs: 30000,
		})
		if err != nil {
			t.Fatalf("activity execution failed: %v", err)
		}
		var result AuditMCPToolCallActivityResult
		if err := val.Get(&result); err != nil {
			t.Fatalf("failure audit should not error: %v", err)
		}
		if result.AuditID == "" {
			t.Error("expected non-empty audit ID for failure")
		}
	})
}

func init() {
	if db, err := sql.Open("pgx", "postgres://admin:admin@localhost:5432/orchestrator?sslmode=disable"); err == nil {
		EnsureMCPTables(db)
		db.Close()
	}
}
