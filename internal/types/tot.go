package types

// TreeOfThoughtsConfig controls Tree-of-Thoughts bounded search.
type TreeOfThoughtsConfig struct {
	MaxDepth          int     `json:"max_depth"`           // default 5
	BranchingFactor   int     `json:"branching_factor"`    // default 3
	MaxTotalNodes     int     `json:"max_total_nodes"`     // default 50
	TokenBudget       int     `json:"token_budget"`        // default 5000
	PruningThreshold  float64 `json:"pruning_threshold"`   // default 0.3
	EvaluationMethod  string  `json:"evaluation_method"`   // "scoring" | "voting" | "llm"
	BacktrackEnabled  bool    `json:"backtrack_enabled"`   // default false (not supported in Slice 26)
	ModelTier         string  `json:"model_tier"`          // "small" | "medium"
	MockLLM           bool    `json:"mock_llm"`            // default true for CI
}

// ThoughtNode is a single node in the ToT search tree.
// Workflow history stores only summary + ref + score, never full thought text.
type ThoughtNode struct {
	ID          string   `json:"id"`
	ParentID    string   `json:"parent_id"`
	Depth       int      `json:"depth"`
	Score       float64  `json:"score"`
	Status      string   `json:"status"`       // "active" | "pruned" | "terminal"
	ThoughtRef  string   `json:"thought_ref"`  // Workspace ref for full thought content
	Summary     string   `json:"summary"`      // short summary; goes into history
	Children    []string `json:"children"`     // child node IDs
	TokensUsed  int      `json:"tokens_used"`
	IsTerminal  bool     `json:"is_terminal"`
	Explanation string   `json:"explanation"`  // score explanation, short
}

// ToTResult is the output of TreeOfThoughtsWorkflow.
// Workflow history stores only refs + metadata, never full thought content.
type ToTResult struct {
	BestPath           []string `json:"best_path"`            // best path node IDs
	SolutionRef        string   `json:"solution_ref"`         // final answer Workspace ref
	SolutionSummary    string   `json:"solution_summary"`     // short summary of final answer
	TotalThoughts      int      `json:"total_thoughts"`       // total nodes
	TreeDepth          int      `json:"tree_depth"`           // actual depth reached
	TotalTokens        int      `json:"total_tokens"`         // total tokens used
	ExplorationTreeRef string   `json:"exploration_tree_ref"` // tree metadata Workspace ref
	Confidence         float64  `json:"confidence"`
	PrunedCount        int      `json:"pruned_count"`
	// LLM metadata for real-only assertions
	Provider     string `json:"provider,omitempty"`
	ModelUsed    string `json:"model_used,omitempty"`
	Mode         string `json:"mode,omitempty"` // "real" | "mock"
	Mock         bool   `json:"mock,omitempty"`
	FallbackUsed bool   `json:"fallback_used,omitempty"`
}

// ToTWorkflowInput is the input to TreeOfThoughtsWorkflow.
type ToTWorkflowInput struct {
	TaskID    string                 `json:"task_id"`
	WorkflowID string                `json:"workflow_id"`
	RunID      string                `json:"run_id"`
	SessionID  string                `json:"session_id"`
	Query      string                `json:"query"`
	Context    map[string]interface{} `json:"context,omitempty"`
	Config     TreeOfThoughtsConfig  `json:"config"`
}

const (
	ThoughtStatusActive   = "active"
	ThoughtStatusPruned   = "pruned"
	ThoughtStatusTerminal = "terminal"
)
