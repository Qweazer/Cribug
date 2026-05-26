package types

// SwarmWorkflowInput is the input for SwarmWorkflow
type SwarmWorkflowInput struct {
	TaskID        string   `json:"task_id"`
	WorkflowID    string   `json:"workflow_id"`
	RunID         string   `json:"run_id"`
	Query         string   `json:"query"`
	Model         string   `json:"model"`
	Temperature   float64  `json:"temperature"`
	MaxTokens     int      `json:"max_tokens"`
	WorkerCount   int      `json:"worker_count"`   // number of worker agents to spawn
	WorkerTimeout int      `json:"worker_timeout"` // seconds per worker, default 60
}

// SwarmWorkflowResult is the aggregated result of a SwarmWorkflow
type SwarmWorkflowResult struct {
	TaskID        string              `json:"task_id"`
	WorkflowID    string              `json:"workflow_id"`
	Status        string              `json:"status"` // completed / partial_success / failed
	TotalWorkers  int                 `json:"total_workers"`
	Succeeded     int                 `json:"succeeded"`
	Failed        int                 `json:"failed"`
	Timeout       int                 `json:"timeout"`
	TotalTokens   int                 `json:"total_tokens"`
	TotalLatencyMs int64              `json:"total_latency_ms"`
	FinalAnswer   string              `json:"final_answer"`
	WorkerResults []WorkerAgentResult `json:"worker_results"`
	Error         string              `json:"error,omitempty"`
}

// WorkerAgentInput is the input for WorkerAgentActivity
type WorkerAgentInput struct {
	TaskID     string `json:"task_id"`
	WorkflowID string `json:"workflow_id"`
	RunID      string `json:"run_id"`
	AgentID    string `json:"agent_id"`
	Role       string `json:"role"`
	Task       string `json:"task"`
	Model      string `json:"model"`
	Temperature float64 `json:"temperature"`
	MaxTokens  int    `json:"max_tokens"`
}

// WorkerAgentResult is the output from WorkerAgentActivity
type WorkerAgentResult struct {
	AgentID    string `json:"agent_id"`
	Role       string `json:"role"`
	Status     string `json:"status"` // completed / failed / timeout
	Result     string `json:"result"`
	Error      string `json:"error,omitempty"`
	LatencyMs  int64  `json:"latency_ms"`
	PromptTokens    int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens     int `json:"total_tokens"`
}

// WorkerStatus constants
const (
	SwarmStatusCompleted      = "completed"
	SwarmStatusPartialSuccess = "partial_success"
	SwarmStatusFailed         = "failed"
	WorkerStatusCompleted     = "completed"
	WorkerStatusFailed        = "failed"
	WorkerStatusTimeout       = "timeout"
)
