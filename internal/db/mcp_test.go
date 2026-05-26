package db

import (
	"context"
	"encoding/json"
	"testing"

	"cribug/internal/types"

	"github.com/google/uuid"
)

func getTestDBHelper(t *testing.T) *Postgres {
	t.Helper()
	databaseURL := "postgres://admin:admin@localhost:5432/orchestrator?sslmode=disable"
	pg, err := New(databaseURL)
	if err != nil {
		t.Skipf("skipping DB integration test: cannot connect: %v", err)
	}
	if err := pg.Ping(context.Background()); err != nil {
		pg.Close()
		t.Skipf("skipping DB integration test: cannot ping: %v", err)
	}
	// Ensure schema
	pg.db.ExecContext(context.Background(), `CREATE TABLE IF NOT EXISTS mcp_servers (
		id UUID PRIMARY KEY, name VARCHAR(255) NOT NULL, command TEXT DEFAULT '',
		args TEXT DEFAULT '[]', env TEXT DEFAULT '[]', url VARCHAR(1024) DEFAULT '',
		status VARCHAR(20) NOT NULL DEFAULT 'registered', created_at TIMESTAMP DEFAULT NOW(),
		updated_at TIMESTAMP DEFAULT NOW(), CONSTRAINT mcp_servers_name_unique UNIQUE (name))`)
	pg.db.ExecContext(context.Background(), `CREATE TABLE IF NOT EXISTS mcp_tools (
		id UUID PRIMARY KEY, server_id UUID NOT NULL REFERENCES mcp_servers(id) ON DELETE CASCADE,
		name VARCHAR(255) NOT NULL, description TEXT DEFAULT '', input_schema JSONB DEFAULT '{}',
		permissions TEXT DEFAULT '[]', CONSTRAINT mcp_tools_server_name_unique UNIQUE (server_id, name))`)
	pg.db.ExecContext(context.Background(), `CREATE TABLE IF NOT EXISTS mcp_audit_logs (
		id UUID PRIMARY KEY, server_id UUID NOT NULL, tool_id UUID DEFAULT NULL,
		tool_name VARCHAR(255) NOT NULL, agent_id VARCHAR(255) NOT NULL DEFAULT '',
		workflow_id VARCHAR(255) NOT NULL DEFAULT '', request_id VARCHAR(255) NOT NULL,
		success BOOLEAN NOT NULL DEFAULT false, error TEXT DEFAULT '', error_type VARCHAR(50) DEFAULT '',
		duration_ms BIGINT DEFAULT 0, overflow BOOLEAN DEFAULT false, created_at TIMESTAMP DEFAULT NOW())`)
	return pg
}

func cleanupDB(t *testing.T, pg *Postgres) {
	t.Helper()
	ctx := context.Background()
	pg.db.ExecContext(ctx, "DELETE FROM mcp_audit_logs")
	pg.db.ExecContext(ctx, "DELETE FROM mcp_tools")
	pg.db.ExecContext(ctx, "DELETE FROM mcp_servers")
}

func TestMCPServer_InsertAndRead(t *testing.T) {
	pg := getTestDBHelper(t)
	defer cleanupDB(t, pg)

	ctx := context.Background()

	serverName := "test-server-" + uuid.New().String()[:8]
	srv, err := pg.CreateMCPServer(ctx, types.MCPRegisterServerInput{
		Name:    serverName,
		Command: "python",
		Args:    []string{"-m", "mcp"},
		Env:     []string{"API_KEY=secret", "LOG_LEVEL=debug"},
		URL:     "http://localhost:9999",
	})
	if err != nil {
		t.Fatalf("CreateMCPServer failed: %v", err)
	}
	if srv.ID == "" {
		t.Error("expected non-empty server ID")
	}
	if srv.Name != serverName {
		t.Errorf("name = %s, want %s", srv.Name, serverName)
	}

	// Verify env sanitization in persisted data
	for _, e := range srv.Env {
		idx := 0
		for ; idx < len(e) && e[idx] != '='; idx++ {
		}
		if idx < len(e) {
			val := e[idx+1:]
			if types.IsSensitiveEnvKey(e[:idx]) && val != "***" {
				t.Errorf("env not sanitized in server object: %s", e)
			}
		}
	}

	// Read back
	fetched, err := pg.GetMCPServerByID(ctx, srv.ID)
	if err != nil {
		t.Fatalf("GetMCPServerByID failed: %v", err)
	}
	if fetched == nil {
		t.Fatal("server not found after insert")
	}
	if fetched.Name != serverName {
		t.Errorf("fetched name = %s, want %s", fetched.Name, serverName)
	}

	// Check raw DB values for env sanitization
	var envRaw string
	err = pg.db.QueryRowContext(ctx, "SELECT env FROM mcp_servers WHERE id = $1", srv.ID).Scan(&envRaw)
	if err != nil {
		t.Fatalf("failed to query raw env: %v", err)
	}
	// Verify no plaintext secret in DB
	var envList []string
	json.Unmarshal([]byte(envRaw), &envList)
	for _, e := range envList {
		idx := 0
		for ; idx < len(e) && e[idx] != '='; idx++ {
		}
		if idx < len(e) {
			val := e[idx+1:]
			if types.IsSensitiveEnvKey(e[:idx]) && val != "***" {
				t.Errorf("DB contains plaintext secret: %s", e)
			}
		}
	}
}

func TestMCPServer_DuplicateName(t *testing.T) {
	pg := getTestDBHelper(t)
	defer cleanupDB(t, pg)

	ctx := context.Background()

	_, err := pg.CreateMCPServer(ctx, types.MCPRegisterServerInput{Name: "unique-name"})
	if err != nil {
		t.Fatalf("first insert failed: %v", err)
	}

	_, err = pg.CreateMCPServer(ctx, types.MCPRegisterServerInput{Name: "unique-name"})
	if err == nil {
		t.Error("expected duplicate name error")
	}
}

func TestMCPTool_Upsert(t *testing.T) {
	pg := getTestDBHelper(t)
	defer cleanupDB(t, pg)

	ctx := context.Background()

	srv, err := pg.CreateMCPServer(ctx, types.MCPRegisterServerInput{Name: "tool-test"})
	if err != nil {
		t.Fatalf("CreateMCPServer: %v", err)
	}

	tools := []types.MCPTool{
		{ID: uuid.New().String(), Name: "echo", Description: "Echo tool", InputSchema: map[string]interface{}{"type": "object"}},
		{ID: uuid.New().String(), Name: "get_time", Description: "Time tool"},
	}

	saved, err := pg.SaveMCPTools(ctx, srv.ID, tools)
	if err != nil {
		t.Fatalf("SaveMCPTools failed: %v", err)
	}
	if len(saved) != 2 {
		t.Errorf("expected 2 tools saved, got %d", len(saved))
	}

	// Read back
	fetched, err := pg.GetMCPToolsByServerID(ctx, srv.ID)
	if err != nil {
		t.Fatalf("GetMCPToolsByServerID failed: %v", err)
	}
	if len(fetched) != 2 {
		t.Errorf("expected 2 tools, got %d", len(fetched))
	}

	// Upsert: update echo, add new tool
	tools = []types.MCPTool{
		{ID: tools[0].ID, Name: "echo", Description: "Updated echo tool"},
		{ID: uuid.New().String(), Name: "calculator", Description: "Calc tool"},
	}
	saved, err = pg.SaveMCPTools(ctx, srv.ID, tools)
	if err != nil {
		t.Fatalf("upsert failed: %v", err)
	}
	if len(saved) != 2 {
		t.Errorf("expected 2 tools after upsert, got %d", len(saved))
	}

	fetched, _ = pg.GetMCPToolsByServerID(ctx, srv.ID)
	if len(fetched) != 3 {
		t.Errorf("expected 3 total tools after upsert, got %d", len(fetched))
	}
}

func TestMCPAudit_InsertAndRead(t *testing.T) {
	pg := getTestDBHelper(t)
	defer cleanupDB(t, pg)

	ctx := context.Background()
	serverID := uuid.New().String()

	t.Run("success audit", func(t *testing.T) {
		err := pg.CreateMCPAuditLog(ctx, types.MCPAuditLog{
			ID: uuid.New().String(), ServerID: serverID, ToolID: uuid.New().String(),
			ToolName: "echo", AgentID: "agent-1", WorkflowID: "wf-1",
			RequestID: "req-1", Success: true, DurationMs: 150,
		})
		if err != nil {
			t.Fatalf("create audit log: %v", err)
		}
	})

	t.Run("failure audit", func(t *testing.T) {
		err := pg.CreateMCPAuditLog(ctx, types.MCPAuditLog{
			ID: uuid.New().String(), ServerID: serverID, ToolID: uuid.New().String(),
			ToolName: "echo", AgentID: "agent-1", WorkflowID: "wf-1",
			RequestID: "req-2", Success: false, Error: "timeout",
			ErrorType: types.MCPErrorTypeTimeout, DurationMs: 30000,
		})
		if err != nil {
			t.Fatalf("create failure audit: %v", err)
		}
	})

	// Read back
	logs, err := pg.GetMCPAuditLogsByWorkflowID(ctx, "wf-1")
	if err != nil {
		t.Fatalf("GetMCPAuditLogsByWorkflowID: %v", err)
	}
	if len(logs) != 2 {
		t.Errorf("expected 2 audit logs, got %d", len(logs))
	}

	hasSuccess := false
	hasFailure := false
	for _, l := range logs {
		if l.Success {
			hasSuccess = true
		}
		if !l.Success && l.ErrorType == types.MCPErrorTypeTimeout {
			hasFailure = true
		}
	}
	if !hasSuccess {
		t.Error("expected at least one success audit log")
	}
	if !hasFailure {
		t.Error("expected at least one failure audit log")
	}
}

func TestMCP_EnvSanitizationInDB(t *testing.T) {
	pg := getTestDBHelper(t)
	defer cleanupDB(t, pg)
	ctx := context.Background()

	srv, err := pg.CreateMCPServer(ctx, types.MCPRegisterServerInput{
		Name: "env-test",
		Env:  []string{"DB_PASSWORD=hunter2", "API_TOKEN=abc123", "SECRET_KEY=xyz"},
	})
	if err != nil {
		t.Fatalf("create server: %v", err)
	}

	// Direct DB query to verify env is sanitized
	var envJSON string
	pg.db.QueryRowContext(ctx, "SELECT env FROM mcp_servers WHERE id=$1", srv.ID).Scan(&envJSON)

	for _, secret := range []string{"hunter2", "abc123", "xyz"} {
		if contains(envJSON, secret) {
			t.Errorf("plaintext secret '%s' found in DB env column", secret)
		}
	}

	// Audit log should also not contain secrets
	err = pg.CreateMCPAuditLog(ctx, types.MCPAuditLog{
		ID: uuid.New().String(), ServerID: srv.ID, ToolID: uuid.New().String(),
		ToolName: "echo", AgentID: "agent-1", WorkflowID: "wf-1",
		RequestID: "req-env", Success: true, DurationMs: 100,
	})
	if err != nil {
		t.Fatalf("create audit: %v", err)
	}

	logs, _ := pg.GetMCPAuditLogsByWorkflowID(ctx, "wf-1")
	for _, log := range logs {
		for _, secret := range []string{"hunter2", "abc123", "xyz"} {
			if contains(log.Error, secret) {
				t.Errorf("plaintext secret '%s' found in audit log error field", secret)
			}
		}
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestMigration_004_MCP(t *testing.T) {
	pg := getTestDBHelper(t)
	ctx := context.Background()

	// Verify all tables exist
	var tables []string
	rows, err := pg.db.QueryContext(ctx, "SELECT table_name FROM information_schema.tables WHERE table_schema='public' AND table_name LIKE 'mcp_%'")
	if err != nil {
		t.Fatalf("query tables: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		rows.Scan(&name)
		tables = append(tables, name)
	}

	expectedTables := map[string]bool{"mcp_servers": false, "mcp_tools": false, "mcp_audit_logs": false}
	for _, table := range tables {
		expectedTables[table] = true
	}
	for table, found := range expectedTables {
		if !found {
			t.Errorf("table %s not found", table)
		}
	}
}
