package types

// DebateConfig controls Debate Mode (Phase 7E Slice 27).
//
// Constraints (per Phase 7 task book):
//   - Debate cannot run unlimited rounds; MaxRounds must be set.
//   - DebateConfig.MockLLM is an EXPLICIT field (no implicit fallback).
//   - Pro/Con/Judge large texts MUST go via WorkspaceRef; Workflow history stores metadata only.
type DebateConfig struct {
	NumDebaters       int      `json:"num_debaters"`        // default 2 (pro + con)
	MaxRounds         int      `json:"max_rounds"`          // default 3
	Perspectives      []string `json:"perspectives"`        // default ["pro", "con"]
	RequireConsensus  bool     `json:"require_consensus"`   // stop early when judge picks a side
	ModeratorEnabled  bool     `json:"moderator_enabled"`   // if true, Judge scores each round
	VotingEnabled     bool     `json:"voting_enabled"`      // optional majority vote
	ModelTier         string   `json:"model_tier"`          // "small" | "medium"
	RoundTimeoutSecs  int      `json:"round_timeout_secs"`  // per-round timeout, default 60
	MockLLM           bool     `json:"mock_llm"`            // explicit field; default true for CI
}

// DebatePosition represents one Agent's argument for a turn.
// Workflow history stores only metadata + ref, never full argument text.
type DebatePosition struct {
	TurnID     string  `json:"turn_id"`
	AgentID    string  `json:"agent_id"`    // "pro" | "con"
	Position   string  `json:"position"`    // "pro" | "con"
	ContentRef string  `json:"content_ref"` // Workspace ref for full argument
	Summary    string  `json:"summary"`     // short summary, ≤500 chars
	Score      float64 `json:"score"`       // agent's self-score
	Confidence float64 `json:"confidence"`  // agent's self-confidence
	TokensUsed int     `json:"tokens_used"`
	Mode       string  `json:"mode,omitempty"` // "real" | "mock"
}

// DebateRound captures one round of debate (Pro + Con + optional Judge).
// Workflow history stores only metadata + refs.
type DebateRound struct {
	RoundIndex  int     `json:"round_index"`
	ProTurnRef  string  `json:"pro_turn_ref"`
	ConTurnRef  string  `json:"con_turn_ref"`
	ProScore    float64 `json:"pro_score"`
	ConScore    float64 `json:"con_score"`
	JudgeScore  float64 `json:"judge_score"`
	JudgeVerdict string `json:"judge_verdict,omitempty"` // "pro" | "con" | "tie"
	TokensUsed  int     `json:"tokens_used"`
}

// DebateResult is the workflow result.
// Workflow history stores only refs + metadata; full transcript is in Workspace.
type DebateResult struct {
	FinalPosition     string  `json:"final_position"`     // "pro" | "con" | "tie"
	FinalAnswerRef    string  `json:"final_answer_ref"`   // final answer / recommendation Workspace ref
	TranscriptRef     string  `json:"transcript_ref"`     // full transcript Workspace ref
	VerdictRef        string  `json:"verdict_ref"`        // judge reasoning Workspace ref
	FinalAnswerText   string  `json:"final_answer_text"`  // short summary (≤2KB)
	ConsensusReached  bool    `json:"consensus_reached"`
	TotalTokens       int     `json:"total_tokens"`
	Rounds            int     `json:"rounds"`             // actual rounds executed
	WinningTurnRef    string  `json:"winning_turn_ref"`
	WorkspaceTopic    string  `json:"workspace_topic"`
	// LLM metadata for real-only assertions
	Provider     string `json:"provider,omitempty"`
	ModelUsed    string `json:"model_used,omitempty"`
	Mode         string `json:"mode,omitempty"`
	// Mock is a real JSON boolean (NOT omitempty) so the API result
	// body always carries an explicit true/false. Phase 7E.6 polish.
	Mock         bool `json:"mock"`
	FallbackUsed bool `json:"fallback_used,omitempty"`
	// LLMCalls is the number of real LLM round-trips the debate made
	// (Pro+Con+Judge per round + 1 final Judge). Phase 7E.6 polish.
	LLMCalls int `json:"llm_calls"`
	// Judge parse telemetry
	JudgeParseSource string `json:"judge_parse_source,omitempty"` // "json" | "regex" | "heuristic"
	ConfidenceSource string `json:"confidence_source,omitempty"` // "json" | "regex" | "heuristic" | "fallback"
}

// DebateWorkflowInput is the input to DebateWorkflow.
type DebateWorkflowInput struct {
	TaskID     string                 `json:"task_id"`
	WorkflowID string                 `json:"workflow_id"`
	RunID      string                 `json:"run_id"`
	SessionID  string                 `json:"session_id"`
	Query      string                 `json:"query"`
	Context    map[string]interface{} `json:"context,omitempty"`
	Config     DebateConfig           `json:"config"`
}

// JudgeVerdict is the structured Judge output.
type JudgeVerdict struct {
	Verdict       string   `json:"verdict"`        // "pro" | "con" | "tie"
	ProScore      float64  `json:"pro_score"`
	ConScore      float64  `json:"con_score"`
	Confidence    float64  `json:"confidence"`
	Rationale     string   `json:"rationale"`      // short summary
	KeyProPoints  []string `json:"key_pro_points"`
	KeyConPoints  []string `json:"key_con_points"`
	Unresolved    []string `json:"unresolved_issues"`
	Recommendation string  `json:"recommendation"`
}

const (
	DebatePositionPro = "pro"
	DebatePositionCon = "con"

	DebateVerdictPro = "pro"
	DebateVerdictCon = "con"
	DebateVerdictTie = "tie"
)
