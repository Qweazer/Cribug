package types

type TaskClassification struct {
	TaskID         string  `json:"task_id"`
	Query          string  `json:"query"`
	Category       string  `json:"category"`        // "simple" | "analysis" | "creative"
	Complexity     float64 `json:"complexity"`       // 0.0-1.0
	RequiresTools  bool    `json:"requires_tools"`  // whether the task needs tools
	SuggestedMode  string  `json:"suggested_mode"` // "single" | "multi"
}

type DAGPlan struct {
	TaskID  string    `json:"task_id"`
	Nodes   []DAGNode `json:"nodes"`
	Edges   []DAGEdge `json:"edges"`
}

type DAGNode struct {
	ID        string   `json:"id"`
	Type      string   `json:"type"`       // "analysis" | "synthesis" | "research"
	Name      string   `json:"name"`
	Input     string   `json:"input"`
	DependsOn []string `json:"depends_on"`
}

type DAGEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type DAGNodeResult struct {
	TaskID     string `json:"task_id"`
	NodeID     string `json:"node_id"`
	NodeType   string `json:"node_type"`
	Status     string `json:"status"`      // "completed" | "failed"
	Output     string `json:"output"`
	Error      string `json:"error,omitempty"`
}