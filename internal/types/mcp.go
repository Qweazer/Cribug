package types

import (
	"encoding/json"
	"strings"
	"time"
)

// ── MCP Error Types ─────────────────────────────────────────────

const (
	MCPErrorTypeServerNotFound     = "mcp_server_not_found"
	MCPErrorTypeServerUnavailable  = "mcp_server_unavailable"
	MCPErrorTypeToolNotFound       = "mcp_tool_not_found"
	MCPErrorTypePermissionDenied   = "mcp_permission_denied"
	MCPErrorTypeSchemaValidation   = "mcp_schema_validation_error"
	MCPErrorTypeTimeout            = "mcp_timeout"
	MCPErrorTypeResultOverflow     = "mcp_result_overflow"
	MCPErrorTypeInvalidInput       = "mcp_invalid_input"
	MCPErrorTypeRegistrationFailed = "mcp_registration_failed"
	MCPErrorTypeDiscoveryFailed    = "mcp_discovery_failed"
	MCPErrorTypeCallFailed         = "mcp_call_failed"
	MCPErrorTypeAuditFailed        = "mcp_audit_failed"
)

// ── MCP Server ──────────────────────────────────────────────────

const (
	MCPServerStatusRegistered = "registered"
	MCPServerStatusRunning    = "running"
	MCPServerStatusStopped    = "stopped"
	MCPServerStatusError      = "error"
)

// SensitiveEnvKeys are environment variable name patterns that must be sanitized.
// Any env var whose upper-cased name contains one of these substrings is considered sensitive.
var SensitiveEnvKeys = []string{
	"KEY", "TOKEN", "SECRET", "PASSWORD", "PASSWD", "AUTH", "CREDENTIAL",
}

type MCPServer struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Command   string    `json:"command,omitempty"`
	Args      []string  `json:"args,omitempty"`
	Env       []string  `json:"env"`             // sanitized for persistence
	EnvRaw    []string  `json:"-"`               // original sensitive values, never persisted
	URL       string    `json:"url,omitempty"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (s *MCPServer) SanitizeEnv() {
	if len(s.EnvRaw) == 0 {
		return
	}
	s.Env = make([]string, len(s.EnvRaw))
	for i, raw := range s.EnvRaw {
		s.Env[i] = SanitizeEnvString(raw)
	}
}

// ── MCP Tool ────────────────────────────────────────────────────

type MCPTool struct {
	ID          string                 `json:"id"`
	ServerID    string                 `json:"server_id"`
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"input_schema"`
	Permissions []string               `json:"permissions,omitempty"`
}

// ── MCP Tool Call ───────────────────────────────────────────────

const (
	DefaultMCPTimeoutSec  = 30
	DefaultMCPResultLimit = 64 * 1024 // 64KB
	MaxMCPResultLimit     = 256 * 1024 // 256KB hard max
)

type MCPToolCallInput struct {
	ToolID    string                 `json:"tool_id"`
	ServerID  string                 `json:"server_id"`
	ToolName  string                 `json:"tool_name"`
	Arguments map[string]interface{} `json:"arguments"`
	Timeout   int                    `json:"timeout"`  // seconds, default 30
	RequestID string                 `json:"request_id"`
	AgentID   string                 `json:"agent_id"`
	WorkflowID string                `json:"workflow_id"`
	RunID     string                 `json:"run_id"`
}

func (in *MCPToolCallInput) EffectiveTimeout() int {
	if in.Timeout <= 0 || in.Timeout > 300 {
		return DefaultMCPTimeoutSec
	}
	return in.Timeout
}

type MCPToolResult struct {
	ToolID      string `json:"tool_id"`
	ToolName    string `json:"tool_name"`
	ServerID    string `json:"server_id"`
	RequestID   string `json:"request_id"`
	Content     string `json:"content"`
	Success     bool   `json:"success"`
	Error       string `json:"error,omitempty"`
	ErrorType   string `json:"error_type,omitempty"`
	DurationMs  int64  `json:"duration_ms"`
	Overflow    bool   `json:"overflow"`
	TruncatedAt int    `json:"truncated_at,omitempty"`
}

// ── MCP Audit Log ───────────────────────────────────────────────

type MCPAuditLog struct {
	ID         string    `json:"id"`
	ServerID   string    `json:"server_id"`
	ToolID     string    `json:"tool_id"`
	ToolName   string    `json:"tool_name"`
	AgentID    string    `json:"agent_id"`
	WorkflowID string    `json:"workflow_id"`
	RequestID  string    `json:"request_id"`
	Success    bool      `json:"success"`
	Error      string    `json:"error,omitempty"`
	ErrorType  string    `json:"error_type,omitempty"`
	DurationMs int64     `json:"duration_ms"`
	Overflow   bool      `json:"overflow"`
	CreatedAt  time.Time `json:"created_at"`
}

// ── MCP Registration Input ──────────────────────────────────────

type MCPRegisterServerInput struct {
	Name    string   `json:"name"`
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	Env     []string `json:"env,omitempty"`
	URL     string   `json:"url,omitempty"`
}

// ── MCP Error ───────────────────────────────────────────────────

type MCPError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Detail  string `json:"detail,omitempty"`
}

func (e *MCPError) Error() string {
	return e.Message
}

func NewMCPError(errType, message string) *MCPError {
	return &MCPError{Type: errType, Message: message}
}

func NewMCPErrorf(errType, format string, args ...interface{}) *MCPError {
	return &MCPError{Type: errType, Message: "MCP error: " + format}
}

// ── Discovery Input / Result ────────────────────────────────────

type MCPDiscoverToolsInput struct {
	ServerID string `json:"server_id"`
}

type MCPDiscoverToolsResult struct {
	ServerID string    `json:"server_id"`
	Tools    []MCPTool `json:"tools"`
}

// ── Env Sanitization ────────────────────────────────────────────

// SanitizeEnvString masks the value portion of a KEY=VALUE pair if the key is sensitive.
func SanitizeEnvString(raw string) string {
	idx := strings.Index(raw, "=")
	if idx < 0 {
		return raw
	}
	key := raw[:idx]
	if IsSensitiveEnvKey(key) {
		return key + "=***"
	}
	return raw
}

// IsSensitiveEnvKey checks if an environment variable name contains a sensitive pattern.
func IsSensitiveEnvKey(key string) bool {
	upper := strings.ToUpper(strings.TrimSpace(key))
	for _, pattern := range SensitiveEnvKeys {
		if strings.Contains(upper, pattern) {
			return true
		}
	}
	return false
}

// CleanAuditPayload ensures a JSON payload contains no plaintext secrets.
func CleanAuditPayload(payload []byte) []byte {
	var m map[string]interface{}
	if err := json.Unmarshal(payload, &m); err != nil {
		return payload
	}
	cleanMap(m)
	cleaned, err := json.Marshal(m)
	if err != nil {
		return payload
	}
	return cleaned
}

func cleanMap(m map[string]interface{}) {
	for k, v := range m {
		if IsSensitiveEnvKey(k) {
			m[k] = "***"
			continue
		}
		switch val := v.(type) {
		case map[string]interface{}:
			cleanMap(val)
		case []interface{}:
			for i, item := range val {
				if itemMap, ok := item.(map[string]interface{}); ok {
					cleanMap(itemMap)
					val[i] = itemMap
				}
			}
		case string:
			if IsSensitiveEnvKey(k) {
				m[k] = "***"
			}
		}
	}
}
