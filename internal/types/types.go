package types

import (
	"database/sql"
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
	ErrorTypeUnknown        = "unknown_error"
)

type TaskConfig struct {
	MaxTotalTokens      *int     `json:"max_total_tokens"`
	MaxCompletionTokens *int     `json:"max_completion_tokens"`
	Model               *string  `json:"model"`
	Temperature         *float64 `json:"temperature"`
	Mode                *string  `json:"mode,omitempty"`
	EnableTools         *bool    `json:"enable_tools,omitempty"`
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