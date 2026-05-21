package types

// AgentMetrics represents aggregated metrics for a single agent role
type AgentMetrics struct {
	AgentRole      string `json:"agent_role"`
	CallCount      int    `json:"call_count"`
	SuccessCount   int    `json:"success_count"`
	FailureCount   int    `json:"failure_count"`
	TotalLatencyMs int64  `json:"total_latency_ms"`
	AvgLatencyMs   int64  `json:"avg_latency_ms"`
	TotalTokens    int    `json:"total_tokens"`
	ToolCallCount  int    `json:"tool_call_count"`
}

// MetricsSummary is the task-level aggregation of all agent metrics
type MetricsSummary struct {
	TaskID         string         `json:"task_id"`
	WorkflowID     string         `json:"workflow_id"`
	AgentMetrics   []AgentMetrics `json:"agent_metrics"`
	TotalCallCount int            `json:"total_call_count"`
	TotalTokens    int            `json:"total_tokens"`
}

// MetricsAgentRole constants (used for metrics aggregation)
const (
	MetricsAgentRoleResearcher  = "researcher"
	MetricsAgentRoleCritic      = "critic"
	MetricsAgentRoleSynthesizer = "synthesizer"
)