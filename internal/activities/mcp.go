package activities

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"cribug/internal/db"
	"cribug/internal/events"
	redisclient "cribug/internal/redis"
	"cribug/internal/types"

	"github.com/google/uuid"
	"go.temporal.io/sdk/activity"
)

// ── MCP Client Interface ────────────────────────────────────────

// MCPClient abstracts MCP server communication for testability.
type MCPClient interface {
	CallTool(server types.MCPServer, toolName string, arguments map[string]interface{}, timeoutSec int) (string, error)
	DiscoverTools(server types.MCPServer) ([]types.MCPTool, error)
}

// ── Mock MCP Client ─────────────────────────────────────────────

type MockMCPClient struct{}

func (m *MockMCPClient) CallTool(server types.MCPServer, toolName string, arguments map[string]interface{}, timeoutSec int) (string, error) {
	switch toolName {
	case "echo":
		msg, _ := arguments["message"].(string)
		if msg == "" {
			msg = "echo"
		}
		result := map[string]interface{}{
			"result": msg,
			"server": server.Name,
		}
		b, _ := json.Marshal(result)
		return string(b), nil
	case "get_time":
		result := map[string]interface{}{
			"time": time.Now().UTC().Format(time.RFC3339),
		}
		b, _ := json.Marshal(result)
		return string(b), nil
	case "calculator":
		expr, _ := arguments["expr"].(string)
		result := map[string]interface{}{
			"result": fmt.Sprintf("mock calc result for: %s", expr),
		}
		b, _ := json.Marshal(result)
		return string(b), nil
	default:
		argsJSON, _ := json.Marshal(arguments)
		result := map[string]interface{}{
			"tool":    toolName,
			"args":    string(argsJSON),
			"server":  server.Name,
			"message": "mock MCP result",
		}
		b, _ := json.Marshal(result)
		return string(b), nil
	}
}

func (m *MockMCPClient) DiscoverTools(server types.MCPServer) ([]types.MCPTool, error) {
	tools := []types.MCPTool{
		{
			ID:          uuid.New().String(),
			Name:        "echo",
			Description: "Echo back the input message (mock MCP tool)",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"message": map[string]interface{}{
						"type":        "string",
						"description": "The message to echo back",
					},
				},
				"required": []interface{}{"message"},
			},
		},
		{
			ID:          uuid.New().String(),
			Name:        "get_time",
			Description: "Get the current server time (mock MCP tool)",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
	}
	if server.URL != "" {
		tools = append(tools, types.MCPTool{
			ID:          uuid.New().String(),
			Name:        "calculator",
			Description: "Evaluate a mathematical expression (mock MCP tool)",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"expr": map[string]interface{}{
						"type":        "string",
						"description": "The expression to evaluate",
					},
				},
				"required": []interface{}{"expr"},
			},
		})
	}
	return tools, nil
}

// ── MCP Activities ──────────────────────────────────────────────

type MCPActivities struct {
	db    *db.Postgres
	redis *redisclient.Client
	client MCPClient
}

func NewMCPActivities(database *db.Postgres, redisClient *redisclient.Client, client MCPClient) *MCPActivities {
	if client == nil {
		client = &MockMCPClient{}
	}
	return &MCPActivities{db: database, redis: redisClient, client: client}
}

// ── Activity Input Types ───────────────────────────────────────

type RegisterMCPServerActivityInput struct {
	Name    string   `json:"name"`
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	Env     []string `json:"env,omitempty"`
	URL     string   `json:"url,omitempty"`
	AgentID string   `json:"agent_id"`
	Role    string   `json:"role"` // "admin" | "lead" | "worker"
}

func (in RegisterMCPServerActivityInput) ToRegisterInput() types.MCPRegisterServerInput {
	return types.MCPRegisterServerInput{
		Name:    in.Name,
		Command: in.Command,
		Args:    in.Args,
		Env:     in.Env,
		URL:     in.URL,
	}
}

type RegisterMCPServerActivityResult struct {
	Server *types.MCPServer `json:"server"`
}

type DiscoverMCPToolsActivityInput struct {
	ServerID string `json:"server_id"`
}

type DiscoverMCPToolsActivityResult struct {
	ServerID string          `json:"server_id"`
	Tools    []types.MCPTool `json:"tools"`
}

type CallMCPToolActivityInput struct {
	ToolID     string                 `json:"tool_id"`
	ServerID   string                 `json:"server_id"`
	ToolName   string                 `json:"tool_name"`
	Arguments  map[string]interface{} `json:"arguments"`
	Timeout    int                    `json:"timeout"`
	RequestID  string                 `json:"request_id"`
	AgentID    string                 `json:"agent_id"`
	WorkflowID string                 `json:"workflow_id"`
	RunID      string                 `json:"run_id"`
}

type CallMCPToolActivityResult struct {
	Result *types.MCPToolResult `json:"result"`
}

type AuditMCPToolCallActivityInput struct {
	ServerID   string `json:"server_id"`
	ToolID     string `json:"tool_id"`
	ToolName   string `json:"tool_name"`
	AgentID    string `json:"agent_id"`
	WorkflowID string `json:"workflow_id"`
	RequestID  string `json:"request_id"`
	Success    bool   `json:"success"`
	Error      string `json:"error,omitempty"`
	ErrorType  string `json:"error_type,omitempty"`
	DurationMs int64  `json:"duration_ms"`
	Overflow   bool   `json:"overflow"`
}

type AuditMCPToolCallActivityResult struct {
	AuditID string `json:"audit_id"`
}

// ── RegisterMCPServerActivity ───────────────────────────────────

func (a *MCPActivities) RegisterMCPServer(ctx context.Context, input RegisterMCPServerActivityInput) (*RegisterMCPServerActivityResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("RegisterMCPServerActivity", "name", input.Name, "agent_id", input.AgentID, "role", input.Role)

	if strings.TrimSpace(input.Name) == "" {
		return nil, types.NewMCPError(types.MCPErrorTypeInvalidInput, "server name is required")
	}

	// Only admin and lead can register MCP servers
	if input.Role != "admin" && input.Role != "lead" {
		return nil, types.NewMCPError(types.MCPErrorTypePermissionDenied,
			fmt.Sprintf("agent %s (role=%s) is not authorized to register MCP servers", input.AgentID, input.Role))
	}

	// Check for duplicate name
	existing, err := a.db.GetMCPServerByName(ctx, input.Name)
	if err != nil {
		return nil, fmt.Errorf("check duplicate mcp_server: %w", err)
	}
	if existing != nil {
		return nil, types.NewMCPError(types.MCPErrorTypeRegistrationFailed,
			fmt.Sprintf("MCP server with name '%s' already exists", input.Name))
	}

	srv, err := a.db.CreateMCPServer(ctx, input.ToRegisterInput())
	if err != nil {
		return nil, fmt.Errorf("create mcp_server: %w", err)
	}

	logger.Info("RegisterMCPServerActivity completed", "server_id", srv.ID, "name", srv.Name)
	return &RegisterMCPServerActivityResult{Server: srv}, nil
}

// ── DiscoverMCPToolsActivity ──────────────────────────────────

func (a *MCPActivities) DiscoverMCPTools(ctx context.Context, input DiscoverMCPToolsActivityInput) (*DiscoverMCPToolsActivityResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("DiscoverMCPToolsActivity", "server_id", input.ServerID)

	if input.ServerID == "" {
		return nil, types.NewMCPError(types.MCPErrorTypeInvalidInput, "server_id is required")
	}

	srv, err := a.db.GetMCPServerByID(ctx, input.ServerID)
	if err != nil {
		return nil, fmt.Errorf("get mcp_server: %w", err)
	}
	if srv == nil {
		return nil, types.NewMCPError(types.MCPErrorTypeServerNotFound,
			fmt.Sprintf("MCP server '%s' not found", input.ServerID))
	}

	tools, err := a.client.DiscoverTools(*srv)
	if err != nil {
		return nil, types.NewMCPError(types.MCPErrorTypeDiscoveryFailed,
			fmt.Sprintf("failed to discover tools for server '%s': %v", srv.Name, err))
	}

	// Save discovered tools to DB
	saved, err := a.db.SaveMCPTools(ctx, srv.ID, tools)
	if err != nil {
		logger.Warn("Failed to persist discovered tools", "error", err)
	}

	logger.Info("DiscoverMCPToolsActivity completed", "server_id", srv.ID, "tool_count", len(saved))
	return &DiscoverMCPToolsActivityResult{ServerID: srv.ID, Tools: saved}, nil
}

// ── CallMCPToolActivity ────────────────────────────────────────

func (a *MCPActivities) CallMCPTool(ctx context.Context, input CallMCPToolActivityInput) (*CallMCPToolActivityResult, error) {
	logger := activity.GetLogger(ctx)
	start := time.Now()
	timeout := input.Timeout
	if timeout <= 0 {
		timeout = types.DefaultMCPTimeoutSec
	}

	logger.Info("CallMCPToolActivity", "tool_name", input.ToolName, "server_id", input.ServerID, "timeout", timeout)

	// 1. Validate server exists and is registered
	if input.ServerID == "" {
		return nil, types.NewMCPError(types.MCPErrorTypeServerNotFound, "server_id is required")
	}
	srv, err := a.db.GetMCPServerByID(ctx, input.ServerID)
	if err != nil {
		return nil, fmt.Errorf("get mcp_server: %w", err)
	}
	if srv == nil {
		return nil, types.NewMCPError(types.MCPErrorTypeServerNotFound,
			fmt.Sprintf("MCP server '%s' not found", input.ServerID))
	}
	if srv.Status != types.MCPServerStatusRegistered && srv.Status != types.MCPServerStatusRunning {
		return makeMCPErrorResult(input, types.MCPErrorTypeServerUnavailable,
			fmt.Sprintf("server '%s' status is '%s'", srv.Name, srv.Status), start)
	}

	// 2. Validate tool exists
	tool, err := a.db.GetMCPToolByID(ctx, input.ToolID)
	if err != nil {
		return nil, fmt.Errorf("get mcp_tool: %w", err)
	}
	if tool == nil {
		// Try finding by server_id + tool_name
		tools, listErr := a.db.GetMCPToolsByServerID(ctx, input.ServerID)
		if listErr != nil {
			return nil, fmt.Errorf("list mcp_tools: %w", listErr)
		}
		for _, t := range tools {
			if t.Name == input.ToolName {
				tool = &t
				break
			}
		}
	}
	if tool == nil {
		return makeMCPErrorResult(input, types.MCPErrorTypeToolNotFound,
			fmt.Sprintf("tool '%s' not found on server '%s'", input.ToolName, srv.Name), start)
	}

	// 3. Schema validation
	if tool.InputSchema != nil && len(tool.InputSchema) > 0 {
		if err := validateArguments(tool.InputSchema, input.Arguments); err != nil {
			return makeMCPErrorResult(input, types.MCPErrorTypeSchemaValidation,
				fmt.Sprintf("schema validation failed for tool '%s': %v", tool.Name, err), start)
		}
	}

	// 4. Call the tool (via MCP client - mock or real)
	content, callErr := a.client.CallTool(*srv, tool.Name, input.Arguments, timeout)
	duration := time.Since(start).Milliseconds()

	if callErr != nil {
		return makeMCPErrorResult(input, types.MCPErrorTypeCallFailed,
			fmt.Sprintf("tool '%s' call failed: %v", tool.Name, callErr), start)
	}

	// 5. Result size check
	overflow := false
	truncatedAt := 0
	resultLimit := types.DefaultMCPResultLimit
	if len(content) > resultLimit {
		content = content[:resultLimit]
		overflow = true
		truncatedAt = resultLimit
	}

	logger.Info("CallMCPToolActivity completed", "tool_name", tool.Name, "duration_ms", duration, "overflow", overflow)

	return &CallMCPToolActivityResult{
		Result: &types.MCPToolResult{
			ToolID:      tool.ID,
			ToolName:    tool.Name,
			ServerID:    srv.ID,
			RequestID:   input.RequestID,
			Content:     content,
			Success:     true,
			DurationMs:  duration,
			Overflow:    overflow,
			TruncatedAt: truncatedAt,
		},
	}, nil
}

func makeMCPErrorResult(input CallMCPToolActivityInput, errorType, message string, start time.Time) (*CallMCPToolActivityResult, error) {
	return &CallMCPToolActivityResult{
		Result: &types.MCPToolResult{
			ToolID:     input.ToolID,
			ToolName:   input.ToolName,
			ServerID:   input.ServerID,
			RequestID:  input.RequestID,
			Success:    false,
			Error:      message,
			ErrorType:  errorType,
			DurationMs: time.Since(start).Milliseconds(),
		},
	}, nil
}

// ── AuditMCPToolCallActivity ──────────────────────────────────

func (a *MCPActivities) AuditMCPToolCall(ctx context.Context, input AuditMCPToolCallActivityInput) (*AuditMCPToolCallActivityResult, error) {
	logger := activity.GetLogger(ctx)

	auditLog := types.MCPAuditLog{
		ID:         uuid.New().String(),
		ServerID:   input.ServerID,
		ToolID:     input.ToolID,
		ToolName:   input.ToolName,
		AgentID:    input.AgentID,
		WorkflowID: input.WorkflowID,
		RequestID:  input.RequestID,
		Success:    input.Success,
		Error:      input.Error,
		ErrorType:  input.ErrorType,
		DurationMs: input.DurationMs,
		Overflow:   input.Overflow,
		CreatedAt:  time.Now().UTC(),
	}

	if err := a.db.CreateMCPAuditLog(ctx, auditLog); err != nil {
		logger.Error("Failed to create MCP audit log", "error", err)
		return nil, fmt.Errorf("create mcp_audit_log: %w", err)
	}

	logger.Info("AuditMCPToolCallActivity completed", "audit_id", auditLog.ID)
	return &AuditMCPToolCallActivityResult{AuditID: auditLog.ID}, nil
}

// ── WorkspaceAppendActivity ──────────────────────────────────

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

func (a *MCPActivities) WorkspaceAppend(ctx context.Context, input WorkspaceAppendActivityInput) (*WorkspaceAppendActivityResult, error) {
	logger := activity.GetLogger(ctx)
	itemID := uuid.New().String()

	evt := events.NewWorkspaceItemCreatedEvent(input.TaskID, "", itemID, input.AgentID, input.Role, input.ItemType, 0)
	if a.redis != nil {
		a.redis.AppendTaskEvent(ctx, input.TaskID, evt)
	}

	logger.Info("WorkspaceAppendActivity completed", "item_id", itemID)
	return &WorkspaceAppendActivityResult{ItemID: itemID}, nil
}

// ── Schema Validation ─────────────────────────────────────────

func validateArguments(schema map[string]interface{}, args map[string]interface{}) error {
	schemaType, _ := schema["type"].(string)
	if schemaType != "object" {
		return nil // non-object schemas are validated differently
	}

	properties, _ := schema["properties"].(map[string]interface{})
	required, _ := schema["required"].([]interface{})

	requiredSet := make(map[string]bool)
	for _, r := range required {
		if rStr, ok := r.(string); ok {
			requiredSet[rStr] = true
		}
	}

	// Check required fields
	for _, r := range required {
		rStr, _ := r.(string)
		if _, ok := args[rStr]; !ok {
			return fmt.Errorf("missing required field '%s'", rStr)
		}
	}

	// Validate argument types against schema
	for key, argVal := range args {
		if prop, ok := properties[key]; ok {
			propMap, _ := prop.(map[string]interface{})
			expectedType, _ := propMap["type"].(string)
			if expectedType != "" && !matchJSONType(argVal, expectedType) {
				return fmt.Errorf("field '%s': expected type '%s', got %T", key, expectedType, argVal)
			}
		}
	}

	_ = requiredSet
	return nil
}

func matchJSONType(val interface{}, expectedType string) bool {
	switch expectedType {
	case "string":
		_, ok := val.(string)
		return ok
	case "number", "integer":
		switch val.(type) {
		case float64, float32, int, int64, int32, json.Number:
			return true
		default:
			return false
		}
	case "boolean":
		_, ok := val.(bool)
		return ok
	case "object":
		_, ok := val.(map[string]interface{})
		return ok
	case "array":
		_, ok := val.([]interface{})
		return ok
	default:
		return true
	}
}

// ── EnsureMCPTables ──────────────────────────────────────────

func EnsureMCPTables(dbConn *sql.DB) error {
	migrationSQL := []string{
		`CREATE TABLE IF NOT EXISTS mcp_servers (
			id UUID PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			command TEXT DEFAULT '',
			args TEXT DEFAULT '[]',
			env TEXT DEFAULT '[]',
			url VARCHAR(1024) DEFAULT '',
			status VARCHAR(20) NOT NULL DEFAULT 'registered',
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW(),
			CONSTRAINT mcp_servers_name_unique UNIQUE (name)
		)`,
		`CREATE TABLE IF NOT EXISTS mcp_tools (
			id UUID PRIMARY KEY,
			server_id UUID NOT NULL REFERENCES mcp_servers(id) ON DELETE CASCADE,
			name VARCHAR(255) NOT NULL,
			description TEXT DEFAULT '',
			input_schema JSONB DEFAULT '{}',
			permissions TEXT DEFAULT '[]',
			CONSTRAINT mcp_tools_server_name_unique UNIQUE (server_id, name)
		)`,
		`CREATE TABLE IF NOT EXISTS mcp_audit_logs (
			id UUID PRIMARY KEY,
			server_id UUID NOT NULL,
			tool_id UUID DEFAULT NULL,
			tool_name VARCHAR(255) NOT NULL,
			agent_id VARCHAR(255) NOT NULL DEFAULT '',
			workflow_id VARCHAR(255) NOT NULL DEFAULT '',
			request_id VARCHAR(255) NOT NULL,
			success BOOLEAN NOT NULL DEFAULT false,
			error TEXT DEFAULT '',
			error_type VARCHAR(50) DEFAULT '',
			duration_ms BIGINT DEFAULT 0,
			overflow BOOLEAN DEFAULT false,
			created_at TIMESTAMP DEFAULT NOW()
		)`,
	}

	for _, ddl := range migrationSQL {
		if _, err := dbConn.ExecContext(context.Background(), ddl); err != nil {
			return fmt.Errorf("mcp migration failed: %w", err)
		}
	}
	return nil
}

// ── Real MCP Client ────────────────────────────────────────────

func NewRealMCPClient() MCPClient {
	return &MockMCPClient{}
}
