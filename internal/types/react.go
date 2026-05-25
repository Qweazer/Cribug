package types

// ReactConfig controls the Workflow-level Reason-Act-Observe loop behavior
type ReactConfig struct {
	MaxIterations     int `json:"max_iterations"`
	MinIterations     int `json:"min_iterations"`
	ObservationWindow int `json:"observation_window"`
	MaxObservations   int `json:"max_observations"`
	MaxThoughts       int `json:"max_thoughts"`
	MaxActions        int `json:"max_actions"`
}

// DefaultReactConfig returns a ReactConfig with safe defaults
func DefaultReactConfig() ReactConfig {
	return ReactConfig{
		MaxIterations:     3,
		MinIterations:     1,
		ObservationWindow: 3,
		MaxObservations:   10,
		MaxThoughts:       10,
		MaxActions:        10,
	}
}

// ReactLoopResult contains the results of a Workflow-level ReAct execution
type ReactLoopResult struct {
	Thoughts     []string `json:"thoughts"`
	Actions      []string `json:"actions"`
	Observations []string `json:"observations"`
	FinalResult  string   `json:"final_result"`
	TotalTokens  int      `json:"total_tokens"`
	Iterations   int      `json:"iterations"`
}

// SaveReActStepInput is the input for SaveReActStepAudit Activity
type SaveReActStepInput struct {
	WorkflowID  string `json:"workflow_id"`
	RunID       string `json:"run_id"`
	NodeID      string `json:"node_id"`
	StepIndex   int    `json:"step_index"`
	Reasoning   string `json:"reasoning"`
	Action      string `json:"action"`
	Observation string `json:"observation"`
	CreatedAt   int64  `json:"created_at_ns"` // workflow.Now(ctx).UnixNano()
}
