package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"cribug/internal/types"

	"github.com/google/uuid"
)

// ── MCP Server Operations ───────────────────────────────────────

func (p *Postgres) CreateMCPServer(ctx context.Context, input types.MCPRegisterServerInput) (*types.MCPServer, error) {
	srv := &types.MCPServer{
		ID:        uuid.New().String(),
		Name:      input.Name,
		Command:   input.Command,
		Args:      input.Args,
		EnvRaw:    input.Env,
		URL:       input.URL,
		Status:    types.MCPServerStatusRegistered,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	srv.SanitizeEnv()

	argsJSON, _ := json.Marshal(srv.Args)
	envJSON, _ := json.Marshal(srv.Env)

	query := `INSERT INTO mcp_servers (id, name, command, args, env, url, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`
	_, err := p.db.ExecContext(ctx, query,
		srv.ID, srv.Name, srv.Command, string(argsJSON), string(envJSON),
		srv.URL, srv.Status, srv.CreatedAt, srv.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert mcp_server: %w", err)
	}
	return srv, nil
}

func (p *Postgres) GetMCPServerByID(ctx context.Context, id string) (*types.MCPServer, error) {
	query := `SELECT id, name, command, args, env, url, status, created_at, updated_at
		FROM mcp_servers WHERE id = $1`
	return p.scanMCPServer(p.db.QueryRowContext(ctx, query, id))
}

func (p *Postgres) GetMCPServerByName(ctx context.Context, name string) (*types.MCPServer, error) {
	query := `SELECT id, name, command, args, env, url, status, created_at, updated_at
		FROM mcp_servers WHERE name = $1`
	return p.scanMCPServer(p.db.QueryRowContext(ctx, query, name))
}

func (p *Postgres) ListMCPServers(ctx context.Context) ([]types.MCPServer, error) {
	query := `SELECT id, name, command, args, env, url, status, created_at, updated_at
		FROM mcp_servers ORDER BY created_at DESC`
	rows, err := p.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list mcp_servers: %w", err)
	}
	defer rows.Close()

	var servers []types.MCPServer
	for rows.Next() {
		srv, err := p.scanMCPServerFromRows(rows)
		if err != nil {
			return nil, err
		}
		servers = append(servers, *srv)
	}
	return servers, rows.Err()
}

func (p *Postgres) UpdateMCPServerStatus(ctx context.Context, id, status string) error {
	query := `UPDATE mcp_servers SET status = $2, updated_at = NOW() WHERE id = $1`
	_, err := p.db.ExecContext(ctx, query, id, status)
	if err != nil {
		return fmt.Errorf("update mcp_server status: %w", err)
	}
	return nil
}

func (p *Postgres) scanMCPServer(row *sql.Row) (*types.MCPServer, error) {
	srv, err := p.scanMCPServerFromRow(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return srv, err
}

func (p *Postgres) scanMCPServerFromRow(scanner interface{ Scan(...interface{}) error }) (*types.MCPServer, error) {
	var srv types.MCPServer
	var argsRaw, envRaw string
	err := scanner.Scan(
		&srv.ID, &srv.Name, &srv.Command, &argsRaw, &envRaw,
		&srv.URL, &srv.Status, &srv.CreatedAt, &srv.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if argsRaw != "" {
		json.Unmarshal([]byte(argsRaw), &srv.Args)
	}
	if envRaw != "" {
		json.Unmarshal([]byte(envRaw), &srv.Env)
	}
	return &srv, nil
}

func (p *Postgres) scanMCPServerFromRows(rows *sql.Rows) (*types.MCPServer, error) {
	var srv types.MCPServer
	var argsRaw, envRaw string
	err := rows.Scan(
		&srv.ID, &srv.Name, &srv.Command, &argsRaw, &envRaw,
		&srv.URL, &srv.Status, &srv.CreatedAt, &srv.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if argsRaw != "" {
		json.Unmarshal([]byte(argsRaw), &srv.Args)
	}
	if envRaw != "" {
		json.Unmarshal([]byte(envRaw), &srv.Env)
	}
	return &srv, nil
}

// ── MCP Tool Operations ─────────────────────────────────────────

func (p *Postgres) SaveMCPTools(ctx context.Context, serverID string, tools []types.MCPTool) ([]types.MCPTool, error) {
	saved := make([]types.MCPTool, 0, len(tools))
	for _, t := range tools {
		if t.ID == "" {
			t.ID = uuid.New().String()
		}
		t.ServerID = serverID
		schemaJSON, _ := json.Marshal(t.InputSchema)
		permsJSON, _ := json.Marshal(t.Permissions)

		query := `INSERT INTO mcp_tools (id, server_id, name, description, input_schema, permissions)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (server_id, name) DO UPDATE SET
				description = EXCLUDED.description,
				input_schema = EXCLUDED.input_schema,
				permissions = EXCLUDED.permissions`
		_, err := p.db.ExecContext(ctx, query,
			t.ID, serverID, t.Name, t.Description, string(schemaJSON), string(permsJSON))
		if err != nil {
			return saved, fmt.Errorf("insert mcp_tool %s: %w", t.Name, err)
		}
		saved = append(saved, t)
	}
	return saved, nil
}

func (p *Postgres) GetMCPToolsByServerID(ctx context.Context, serverID string) ([]types.MCPTool, error) {
	query := `SELECT id, server_id, name, description, input_schema, permissions
		FROM mcp_tools WHERE server_id = $1 ORDER BY name`
	rows, err := p.db.QueryContext(ctx, query, serverID)
	if err != nil {
		return nil, fmt.Errorf("list mcp_tools: %w", err)
	}
	defer rows.Close()

	var tools []types.MCPTool
	for rows.Next() {
		var t types.MCPTool
		var schemaRaw, permsRaw string
		if err := rows.Scan(&t.ID, &t.ServerID, &t.Name, &t.Description, &schemaRaw, &permsRaw); err != nil {
			return nil, fmt.Errorf("scan mcp_tool: %w", err)
		}
		if schemaRaw != "" {
			json.Unmarshal([]byte(schemaRaw), &t.InputSchema)
		}
		if permsRaw != "" {
			json.Unmarshal([]byte(permsRaw), &t.Permissions)
		}
		tools = append(tools, t)
	}
	return tools, rows.Err()
}

func (p *Postgres) GetMCPToolByID(ctx context.Context, id string) (*types.MCPTool, error) {
	query := `SELECT id, server_id, name, description, input_schema, permissions
		FROM mcp_tools WHERE id = $1`
	var t types.MCPTool
	var schemaRaw, permsRaw string
	err := p.db.QueryRowContext(ctx, query, id).Scan(
		&t.ID, &t.ServerID, &t.Name, &t.Description, &schemaRaw, &permsRaw,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get mcp_tool: %w", err)
	}
	if schemaRaw != "" {
		json.Unmarshal([]byte(schemaRaw), &t.InputSchema)
	}
	if permsRaw != "" {
		json.Unmarshal([]byte(permsRaw), &t.Permissions)
	}
	return &t, nil
}

// ── MCP Audit Operations ────────────────────────────────────────

func (p *Postgres) CreateMCPAuditLog(ctx context.Context, audit types.MCPAuditLog) error {
	if audit.ID == "" {
		audit.ID = uuid.New().String()
	}
	if audit.CreatedAt.IsZero() {
		audit.CreatedAt = time.Now().UTC()
	}
	query := `INSERT INTO mcp_audit_logs
		(id, server_id, tool_id, tool_name, agent_id, workflow_id, request_id,
		 success, error, error_type, duration_ms, overflow, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`
	_, err := p.db.ExecContext(ctx, query,
		audit.ID, audit.ServerID, audit.ToolID, audit.ToolName,
		audit.AgentID, audit.WorkflowID, audit.RequestID,
		audit.Success, audit.Error, audit.ErrorType,
		audit.DurationMs, audit.Overflow, audit.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert mcp_audit_log: %w", err)
	}
	return nil
}

func (p *Postgres) GetMCPAuditLogsByWorkflowID(ctx context.Context, workflowID string) ([]types.MCPAuditLog, error) {
	query := `SELECT id, server_id, tool_id, tool_name, agent_id, workflow_id, request_id,
		success, error, error_type, duration_ms, overflow, created_at
		FROM mcp_audit_logs WHERE workflow_id = $1 ORDER BY created_at DESC`
	rows, err := p.db.QueryContext(ctx, query, workflowID)
	if err != nil {
		return nil, fmt.Errorf("list mcp_audit_logs: %w", err)
	}
	defer rows.Close()

	var logs []types.MCPAuditLog
	for rows.Next() {
		var l types.MCPAuditLog
		if err := rows.Scan(
			&l.ID, &l.ServerID, &l.ToolID, &l.ToolName, &l.AgentID,
			&l.WorkflowID, &l.RequestID, &l.Success, &l.Error,
			&l.ErrorType, &l.DurationMs, &l.Overflow, &l.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan mcp_audit_log: %w", err)
		}
		logs = append(logs, l)
	}
	return logs, rows.Err()
}
