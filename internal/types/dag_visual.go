package types

// DAGNodeStatus represents the real-time status of a DAG node
type DAGNodeStatus struct {
	NodeID        string   `json:"node_id"`
	Status        string   `json:"status"` // pending, running, completed, failed
	Layer         int      `json:"layer"`
	Dependencies  []string `json:"dependencies"` // Always include, even if empty
	StartedAtNs   int64    `json:"started_at_ns,omitempty"`
	CompletedAtNs int64    `json:"completed_at_ns,omitempty"`
	Error         string   `json:"error,omitempty"`
}

// DAGVisualMeta contains aggregated metadata for a DAG workflow
type DAGVisualMeta struct {
	TaskID         string `json:"task_id"`
	WorkflowID     string `json:"workflow_id"`
	TotalNodes     int    `json:"total_nodes"`
	PendingNodes   int    `json:"pending_nodes"`
	RunningNodes   int    `json:"running_nodes"`
	CompletedNodes int    `json:"completed_nodes"`
	FailedNodes    int    `json:"failed_nodes"`
	UpdatedAtNs    int64  `json:"updated_at_ns"`
}

// DAGVisualSnapshot is a complete snapshot of DAG visualization state
type DAGVisualSnapshot struct {
	TaskID     string                 `json:"task_id"`
	WorkflowID string                 `json:"workflow_id"`
	Meta       DAGVisualMeta          `json:"meta"`
	Nodes      map[string]DAGNodeStatus `json:"nodes"`
}

// NodeStatus constants
const (
	NodeStatusPending   = "pending"
	NodeStatusRunning   = "running"
	NodeStatusCompleted = "completed"
	NodeStatusFailed    = "failed"
	NodeStatusSkipped   = "skipped"
)