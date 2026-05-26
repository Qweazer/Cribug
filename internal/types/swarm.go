package types

// ── Swarm Workflow Input / Output ──────────────────────────────

type SwarmWorkflowInput struct {
	TaskID        string  `json:"task_id"`
	WorkflowID    string  `json:"workflow_id"`
	RunID         string  `json:"run_id"`
	Query         string  `json:"query"`
	Model         string  `json:"model"`
	Temperature   float64 `json:"temperature"`
	MaxTokens     int     `json:"max_tokens"`
	WorkerCount   int     `json:"worker_count"`
	WorkerTimeout int     `json:"worker_timeout"`
	MaxP2PRounds  int     `json:"max_p2p_rounds"` // default 2, prevents infinite loops
}

type SwarmWorkflowResult struct {
	TaskID         string              `json:"task_id"`
	WorkflowID     string              `json:"workflow_id"`
	Status         string              `json:"status"`
	TotalWorkers   int                 `json:"total_workers"`
	Succeeded      int                 `json:"succeeded"`
	Failed         int                 `json:"failed"`
	Timeout        int                 `json:"timeout"`
	TotalTokens    int                 `json:"total_tokens"`
	TotalLatencyMs int64               `json:"total_latency_ms"`
	FinalAnswer    string              `json:"final_answer"`
	WorkerResults  []WorkerAgentResult `json:"worker_results"`
	Error          string              `json:"error,omitempty"`
	P2PSummary     *P2PSummary         `json:"p2p_summary,omitempty"` // Phase 5B
}

// ── Worker Agent Input / Output ────────────────────────────────

type WorkerAgentInput struct {
	TaskID        string         `json:"task_id"`
	WorkflowID    string         `json:"workflow_id"`
	RunID         string         `json:"run_id"`
	AgentID       string         `json:"agent_id"`
	Role          string         `json:"role"`
	Task          string         `json:"task"`
	Model         string         `json:"model"`
	Temperature   float64        `json:"temperature"`
	MaxTokens     int            `json:"max_tokens"`
	InboxMessages []AgentMessage `json:"inbox_messages,omitempty"` // Phase 5B
	Round         int            `json:"round,omitempty"`          // Phase 5B
}

type WorkerAgentResult struct {
	AgentID           string         `json:"agent_id"`
	Role              string         `json:"role"`
	Status            string         `json:"status"`
	Result            string         `json:"result"`
	Error             string         `json:"error,omitempty"`
	LatencyMs         int64          `json:"latency_ms"`
	PromptTokens      int            `json:"prompt_tokens"`
	CompletionTokens  int            `json:"completion_tokens"`
	TotalTokens       int            `json:"total_tokens"`
	OutboundMessages  []AgentMessage `json:"outbound_messages,omitempty"`  // Phase 5B
	InboxCount        int            `json:"inbox_count,omitempty"`        // Phase 5B
	OutboxCount       int            `json:"outbox_count,omitempty"`       // Phase 5B
}

// ── Agent P2P Message (Phase 5B) ───────────────────────────────

type AgentMessage struct {
	MessageID       string `json:"message_id"`
	WorkflowID      string `json:"workflow_id"`
	TaskID          string `json:"task_id"`
	FromAgentID     string `json:"from_agent_id"`
	ToAgentID       string `json:"to_agent_id"`
	FromRole        string `json:"from_role"`
	ToRole          string `json:"to_role"`
	MessageType     string `json:"message_type"`
	Content         string `json:"content"`
	Status          string `json:"status"`
	Round           int    `json:"round"`
	ParentMessageID string `json:"parent_message_id,omitempty"`
}

// MessageType constants
const (
	MsgTypeRequest     = "request"
	MsgTypeResponse    = "response"
	MsgTypeCritique    = "critique"
	MsgTypeObservation = "observation"
	MsgTypeFinal       = "final"
	MsgTypeError       = "error"
)

// MessageStatus constants
const (
	MsgStatusCreated   = "created"
	MsgStatusRouted    = "routed"
	MsgStatusDelivered = "delivered"
	MsgStatusFailed    = "failed"
	MsgStatusDropped   = "dropped"
)

// P2PSummary aggregates all P2P message statistics (Phase 5B)
type P2PSummary struct {
	TotalMessages    int              `json:"total_messages"`
	RoutedMessages   int              `json:"routed_messages"`
	DeliveredMessages int             `json:"delivered_messages"`
	FailedMessages   int              `json:"failed_messages"`
	DroppedMessages  int              `json:"dropped_messages"`
	P2PRounds        int              `json:"p2p_rounds"`
	Messages         []AgentMessage   `json:"messages"`
}

// ── Status Constants ────────────────────────────────────────────

const (
	SwarmStatusCompleted      = "completed"
	SwarmStatusPartialSuccess = "partial_success"
	SwarmStatusFailed         = "failed"
	WorkerStatusCompleted     = "completed"
	WorkerStatusFailed        = "failed"
	WorkerStatusTimeout       = "timeout"
)
