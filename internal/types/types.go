package types

import (
	"database/sql"
	"strings"
	"time"
)

const (
	TaskStatusPending    = "pending"
	TaskStatusRunning    = "running"
	TaskStatusCompleted  = "completed"
	TaskStatusFailed     = "failed"
	TaskStatusBudgetExceeded = "budget_exceeded"
	TaskStatusCancelled = "cancelled"
)

const (
	ErrorTypeValidation     = "validation_error"
	ErrorTypeDB             = "db_error"
	ErrorTypeRedis          = "redis_error"
	ErrorTypeWorkflowStart  = "workflow_start_error"
	ErrorTypeWorkflow       = "workflow_error"
	ErrorTypeTool           = "tool_error"
	ErrorTypeUnknown        = "unknown_error"
)

type TaskConfig struct {
	MaxTotalTokens      *int     `json:"max_total_tokens"`
	MaxCompletionTokens *int     `json:"max_completion_tokens"`
	Model               *string  `json:"model"`
	Temperature         *float64 `json:"temperature"`
	Mode                *string  `json:"mode,omitempty"`
	EnableTools         *bool    `json:"enable_tools,omitempty"`
	MaxParallelAgents   *int     `json:"max_parallel_agents,omitempty"`
	EnableReAct         *bool    `json:"enable_react,omitempty"`
	ReActMaxIterations  *int     `json:"react_max_iterations,omitempty"`
	TestFailNodeID      *string  `json:"test_fail_node_id,omitempty"` // Slice 13 test hook: returns deterministic error for this node
}

const (
	WorkflowModeSimple     = "simple"
	WorkflowModeDAG       = "dag"
	WorkflowModeMultiAgent = "multi_agent"
)

type CreateTaskRequest struct {
	Query     string     `json:"query"`
	SessionID string     `json:"session_id"`
	Config    *TaskConfig `json:"config,omitempty"`
}

type CreateTaskResponse struct {
	TaskID     string `json:"task_id"`
	WorkflowID string `json:"workflow_id"`
	RunID      *string `json:"run_id"`
	Status     string `json:"status"`
	StreamURL  string `json:"stream_url"`
}

type TaskUsage struct {
	PromptTokens     *int `json:"prompt_tokens"`
	CompletionTokens *int `json:"completion_tokens"`
	TotalTokens     *int `json:"total_tokens"`
}

type TaskDetailResponse struct {
	TaskID             string     `json:"task_id"`
	WorkflowID         string     `json:"workflow_id"`
	RunID              *string    `json:"run_id"`
	SessionID          *string    `json:"session_id"`
	Status             string     `json:"status"`
	Result             *string    `json:"result"`
	Error              *string    `json:"error"`
	ErrorType          *string    `json:"error_type"`
	Usage              *TaskUsage `json:"usage"`
	Model              string     `json:"model"`
	MaxTotalTokens     int        `json:"max_total_tokens"`
	MaxCompletionTokens int       `json:"max_completion_tokens"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

type HealthResponse struct {
	Status       string                  `json:"status"`
	Dependencies map[string]string        `json:"dependencies"`
}

type ErrorResponse struct {
	Error     string `json:"error"`
	ErrorType string `json:"error_type"`
}

type Task struct {
	ID                string         `db:"id"`
	SessionID         sql.NullString `db:"session_id"`
	Query             string         `db:"query"`
	Status            string         `db:"status"`
	Result            sql.NullString `db:"result"`
	ErrorType         sql.NullString `db:"error_type"`
	Error             sql.NullString `db:"error"`
	MaxTotalTokens    int            `db:"max_total_tokens"`
	MaxCompletionTokens int          `db:"max_completion_tokens"`
	Model             string         `db:"model"`
	WorkflowID        string         `db:"workflow_id"`
	RunID             sql.NullString `db:"run_id"`
	UsagePromptTokens sql.NullInt64   `db:"usage_prompt_tokens"`
	UsageCompletionTokens sql.NullInt64 `db:"usage_completion_tokens"`
	UsageTotalTokens  sql.NullInt64   `db:"usage_total_tokens"`
	CreatedAt         time.Time      `db:"created_at"`
	UpdatedAt         time.Time      `db:"updated_at"`
}

func NormalizeConfig(cfg *TaskConfig) (maxTotalTokens, maxCompletionTokens int, model string, temperature float64) {
	maxTotalTokens = 8000
	maxCompletionTokens = 1024
	model = "gpt-4o-mini"
	temperature = 0.7

	if cfg == nil {
		return
	}

	if cfg.MaxTotalTokens != nil {
		maxTotalTokens = *cfg.MaxTotalTokens
	}
	if cfg.MaxCompletionTokens != nil {
		maxCompletionTokens = *cfg.MaxCompletionTokens
	}
	if cfg.Model != nil {
		model = *cfg.Model
	}
	if cfg.Temperature != nil {
		temperature = *cfg.Temperature
	}

	return
}

type WorkflowTaskRequest struct {
	TaskID              string
	Query               string
	SessionID           string
	Model               string
	Temperature         float64
	MaxTotalTokens      int
	MaxCompletionTokens int
	WorkflowID          string
	RunID               string
	Config              *TaskConfig

	// DAG Concurrency Config
	MaxParallelAgents int `json:"max_parallel_agents,omitempty"`

	// ReAct Config
	EnableReAct        bool `json:"enable_react,omitempty"`
	ReActMaxIterations int  `json:"react_max_iterations,omitempty"`

	// Test hook for Slice 13 DAG dynamic replan testing
	TestFailNodeID string `json:"test_fail_node_id,omitempty"`

	// ReactConfig is computed from EnableReAct + ReActMaxIterations
	ReactConfig *ReactLoopConfig `json:"-"`
}

type WorkflowTaskResult struct {
	TaskID  string
	Status  string
	Answer  string
	Error   string
}

// LLM types for AgentActivity

type LLMMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type LLMRequest struct {
	TraceID             string         `json:"trace_id"`
	TaskID              string         `json:"task_id"`
	SessionID           *string        `json:"session_id,omitempty"`
	Provider            string         `json:"provider"`
	Model               string         `json:"model"`
	Messages            []LLMMessage   `json:"messages"`
	Temperature         float64        `json:"temperature"`
	MaxCompletionTokens int            `json:"max_completion_tokens"`
	ResponseFormat      *string        `json:"response_format,omitempty"`
	Role                string         `json:"role,omitempty"` // "planner" | "researcher" | "critic" | "synthesizer"
	Metadata            map[string]any `json:"metadata,omitempty"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type LLMResponse struct {
	Content            string  `json:"content"`
	Usage              Usage   `json:"usage"`
	Model              string  `json:"model"`
	Provider           string  `json:"provider"`
	FinishReason       string  `json:"finish_reason"`
	ProviderResponseID *string `json:"provider_response_id,omitempty"`
	LatencyMS          int64   `json:"latency_ms"`
	Error              *string `json:"error,omitempty"`
}

// Tool types

type ToolCall struct {
	ToolName   string                 `json:"tool_name"`
	Arguments  map[string]interface{} `json:"arguments"`
}

type ToolResult struct {
	ToolName  string `json:"tool_name"`
	Output    string `json:"output"`
	Error     string `json:"error,omitempty"`
	LatencyMs int    `json:"latency_ms"`
}

type ToolInput struct {
	TaskID    string                 `json:"task_id"`
	ToolName  string                 `json:"tool_name"`
	Arguments map[string]interface{} `json:"arguments"`
}

type ToolDecision struct {
	Matched   bool   `json:"matched"`
	ToolName  string `json:"tool_name,omitempty"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
}

// DetectToolIntent applies deterministic rules to determine if query matches a known tool pattern.
// Returns matched=false if no tool pattern matches.
func DetectToolIntent(query string) ToolDecision {
	// Calculator patterns: "calculate:" or "计算:" or "calc " prefix
	lowerQuery := query
	if len(lowerQuery) > 256 {
		lowerQuery = lowerQuery[:256]
	}

	// Check for calculator patterns
	if strings.HasPrefix(lowerQuery, "calculate:") {
		expr := strings.TrimSpace(query[len("calculate:"):])
		if expr != "" {
			return ToolDecision{
				Matched:   true,
				ToolName:  "calculator",
				Arguments: map[string]interface{}{"expr": expr},
			}
		}
	}
	if strings.Contains(lowerQuery, "计算:") {
		idx := strings.Index(lowerQuery, "计算:")
		expr := strings.TrimSpace(query[idx+len("计算:"):])
		if expr != "" {
			return ToolDecision{
				Matched:   true,
				ToolName:  "calculator",
				Arguments: map[string]interface{}{"expr": expr},
			}
		}
	}
	if strings.HasPrefix(lowerQuery, "calc ") {
		expr := strings.TrimSpace(query[len("calc "):])
		if expr != "" {
			return ToolDecision{
				Matched:   true,
				ToolName:  "calculator",
				Arguments: map[string]interface{}{"expr": expr},
			}
		}
	}

	// Echo patterns: "echo:" or "回显:" prefix
	if strings.HasPrefix(lowerQuery, "echo:") {
		msg := strings.TrimSpace(query[len("echo:"):])
		if msg != "" {
			return ToolDecision{
				Matched:   true,
				ToolName:  "echo",
				Arguments: map[string]interface{}{"message": msg},
			}
		}
	}
	if strings.Contains(lowerQuery, "回显:") {
		idx := strings.Index(lowerQuery, "回显:")
		msg := strings.TrimSpace(query[idx+len("回显:"):])
		if msg != "" {
			return ToolDecision{
				Matched:   true,
				ToolName:  "echo",
				Arguments: map[string]interface{}{"message": msg},
			}
		}
	}

	return ToolDecision{Matched: false}
}