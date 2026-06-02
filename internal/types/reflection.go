package types

// ReflectionRequest is the input to ReflectionWorkflow.
type ReflectionRequest struct {
	TaskID       string                 `json:"task_id"`
	WorkflowID   string                 `json:"workflow_id"`
	RunID        string                 `json:"run_id"`
	SessionID    string                 `json:"session_id"`
	Query        string                 `json:"query"`
	Context      map[string]interface{} `json:"context,omitempty"`
	Config       ReflectionConfig       `json:"config"`
	RouterConfig RouterConfigSnapshot   `json:"router_config"`
}

// ReflectionConfig controls reflection behaviour.
type ReflectionConfig struct {
	MaxIterations       int      `json:"max_iterations"`      // default 2
	MinScoreThreshold   float64  `json:"min_score_threshold"` // default 0.75
	EvaluationCriteria  []string `json:"evaluation_criteria"` // e.g. ["clarity","accuracy","completeness"]
	MockLLM             bool     `json:"mock_llm"`            // default true for CI
	Model               string   `json:"model"`               // empty = resolved via ConfigResolver; "gpt-4o-mini" only as dev fallback
	Temperature         float64  `json:"temperature"`
	MaxCompletionTokens int      `json:"max_completion_tokens"`
}

// ReflectionIteration records one generate-evaluate-revise round.
// Workflow history stores only metadata (summary + ref + score), never full draft/critique text.
type ReflectionIteration struct {
	Iteration       int     `json:"iteration"`
	DraftRef        string  `json:"draft_ref,omitempty"`
	DraftSummary    string  `json:"draft_summary,omitempty"` // ≤500 chars
	Score           float64 `json:"score"`
	CritiqueRef     string  `json:"critique_ref,omitempty"`
	CritiqueSummary string  `json:"critique_summary,omitempty"` // ≤500 chars
	RevisedRef      string  `json:"revised_ref,omitempty"`
	RevisedSummary  string  `json:"revised_summary,omitempty"` // ≤500 chars
	TokensUsed      int     `json:"tokens_used"`
}

// ReflectionResult is the output of ReflectionWorkflow.
type ReflectionResult struct {
	TaskID             string                `json:"task_id"`
	WorkflowID         string                `json:"workflow_id"`
	FinalAnswerRef     string                `json:"final_answer_ref,omitempty"`
	FinalAnswerSummary string                `json:"final_answer_summary,omitempty"` // ≤500 chars
	FinalScore         float64               `json:"final_score"`
	TotalIterations    int                   `json:"total_iterations"`
	TotalTokens        int                   `json:"total_tokens"`
	Status             string                `json:"status"`                  // "ok" | "max_iterations_reached" | "error"
	Iterations         []ReflectionIteration `json:"iterations"`              // metadata only, no full text
	AuditSummary       string                `json:"audit_summary,omitempty"` // no hidden CoT
	// LLM metadata for real-only E2E assertions (no API keys)
	Provider      string `json:"provider,omitempty"`
	ModelUsed     string `json:"model_used,omitempty"`
	Mode          string `json:"mode,omitempty"`          // "real" | "mock"
	FallbackUsed  bool   `json:"fallback_used,omitempty"`
}

const (
	ReflectionStatusOK               = "ok"
	ReflectionStatusMaxIterations    = "max_iterations_reached"
	ReflectionStatusEvaluationFailed = "evaluation_failed"
	ReflectionStatusError            = "error"
)
