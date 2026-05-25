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
	UseLLM   bool     `json:"use_llm"`   // whether this node should call LLM

	// ReAct support
	ReactConfig *ReactLoopConfig `json:"react_config,omitempty"`
}

type DAGEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type DAGNodeResult struct {
	TaskID        string `json:"task_id"`
	NodeID        string `json:"node_id"`
	NodeType      string `json:"node_type"`
	Status        string `json:"status"`       // "completed" | "failed" | "skipped"
	Output        string `json:"output"`
	Error         string `json:"error,omitempty"`
	SkippedReason string `json:"skipped_reason,omitempty"`
}

type DAGSynthesisResult struct {
	TaskID               string `json:"task_id"`
	FinalAnswer          string `json:"final_answer"`
	NodeCount            int    `json:"node_count"`
	CompletedNodes       int    `json:"completed_nodes"`
	FailedNodes          int    `json:"failed_nodes"`
	SkippedNodes         int    `json:"skipped_nodes"`
	PartialSuccess       bool   `json:"partial_success"`
	LLMNodes            int    `json:"llm_nodes"`
	TotalPromptTokens    int    `json:"total_prompt_tokens"`
	TotalCompletionTokens int   `json:"total_completion_tokens"`
	TotalTokens         int    `json:"total_tokens"`
}

// HandleDAGNodeFailureInput is the input for HandleDAGNodeFailureActivity
type HandleDAGNodeFailureInput struct {
	WorkflowID    string `json:"workflow_id"`
	TaskID        string `json:"task_id"`
	FailedNodeID  string `json:"failed_node_id"`
	Error         string `json:"error"`
	FailedAtNs    int64  `json:"failed_at_ns"`
	AllNodeIDs    []string `json:"all_node_ids"`
}

// HandleDAGNodeFailureOutput contains the result of failure handling
type HandleDAGNodeFailureOutput struct {
	FailedNodeID  string   `json:"failed_node_id"`
	SkippedNodes  []string `json:"skipped_nodes"`
	AffectedNodes []string `json:"affected_nodes"`
	ReplanApplied bool     `json:"replan_applied"`
}

// ReactLoopConfig holds configuration for ReAct reasoning loop
type ReactLoopConfig struct {
	EnableReAct       bool `json:"enable_react"`
	MaxIterations    int  `json:"max_iterations"`     // default 3, max 10
	EarlyStopOnAnswer bool `json:"early_stop_on_answer"` // default true
}

// ReactStep represents a single step in ReAct reasoning
type ReactStep struct {
	Iteration   int    `json:"iteration"`
	Thought     string `json:"thought"`
	Action      string `json:"action"`
	Observation string `json:"observation"`
	Timestamp   string `json:"timestamp"`
}

// ReactResult holds the result of a ReAct reasoning loop
type ReactResult struct {
	Steps          []ReactStep `json:"steps"`
	FinalAnswer    string       `json:"final_answer"`
	IterationsUsed int         `json:"iterations_used"`
}