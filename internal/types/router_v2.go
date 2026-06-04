package types

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
)

// ─── Capability (v2 Phase 7I) ──────────────────────────────────────────

// Capability is a typed addon / mode-suggested capability. The Router
// does NOT execute these — Workflows / Activities do. The Router only
// signals "this task needs X" via AddonCapabilities and the workflow
// internally decides whether to actually invoke the capability.
type Capability string

const (
	CapRAG         Capability = "rag"
	CapSandbox     Capability = "sandbox"
	CapSkills      Capability = "skills"
	CapMCPTools    Capability = "mcp_tools"
	CapCitations   Capability = "citations"
	CapWebSearch   Capability = "web_search"
	CapWorkspace   Capability = "workspace"
	CapAudit       Capability = "audit"
	CapApproval    Capability = "approval"
	CapReflection  Capability = "reflection"
	CapDebate      Capability = "debate"
)

// CapabilityNeeds is a normalized view of what the task requires.
type CapabilityNeeds struct {
	NeedsTools      bool
	NeedsRAG        bool
	NeedsSandbox    bool
	NeedsResearch   bool
	NeedsCitations  bool
	NeedsWebSearch  bool
	NeedsSkills     bool
	NeedsMCPTools   bool
	NeedsReflection bool
	NeedsDebate     bool
}

// ─── RouterDecisionSignals (v2 Phase 7I) ───────────────────────────────

// RouterDecisionSignals captures every signal the Router consults to make
// a decision. Mirrors the policy.yaml structure so the audit trail can
// reproduce the decision from signals alone.
type RouterDecisionSignals struct {
	// Text signals
	QueryLength      int
	QueryCharCount   int
	HasNumberedList  bool
	HasMultiSentence bool
	UserIntent       string

	// Complexity sub-signals
	ComplexityAnalyze     float64
	ComplexityResearch    float64
	ComplexityExecution   float64
	ComplexityMultiAgent  float64
	ComplexityDebate      float64
	ComplexityExploration float64
	ComplexityOverall     float64
	// ComplexitySemantic is the union of 23 Chinese complex-intent
	// categories. Phase 7I Fix-1 path: when present (>0.4) it
	// lifts ComplexityOverall so the v2 router stops defaulting to
	// direct_answer on short Chinese queries.
	ComplexitySemantic    float64

	// Capability requirements (from DetectTaskCapabilitiesActivity)
	RequiresTools      bool
	RequiresRAG        bool
	RequiresResearch   bool
	RequiresSandbox    bool
	RequiresCitations  bool
	RequiresWebSearch  bool

	// Capability signals (Phase 7I additions)
	RagNeedScore           float64
	EvidenceNeedScore      float64
	ToolNeedScore          float64
	SkillNeedScore         float64
	SandboxNeedScore       float64
	ToolExecutionNeedScore float64
	WebSearchNeedScore     float64
	WorkspaceArtifactNeed  bool
	AuditNeed              bool
	ApprovalNeedScore      float64

	// User constraints (from RouteRequest)
	AllowTools     bool
	AllowSandbox   bool
	AllowResearch  bool
	AllowWebSearch bool
	BudgetUSD      float64
	MaxLatencyMs   int

	// Risk signals
	RiskKeywords []string
	RiskLevel     string

	// Cost / latency fit
	BudgetFitScore  float64
	LatencyFitScore float64

	// Policy / classifier telemetry
	PolicyVersion  string
	ClassifierUsed bool
}

// ─── RejectedModeReason (v1+v2) ────────────────────────────────────────

// RejectedModeReason is the typed reason a mode was rejected.
type RejectedModeReason string

const (
	RejectDisabledByConfig  RejectedModeReason = "disabled_by_config"
	RejectBlockedByAllow     RejectedModeReason = "blocked_by_user_allow_flag"
	RejectCostOverBudget     RejectedModeReason = "cost_exceeds_budget"
	RejectLatencyOverMax     RejectedModeReason = "latency_exceeds_max"
	RejectNoCapability       RejectedModeReason = "missing_capability"
	RejectNoSignalMatch      RejectedModeReason = "no_signal_match"
	RejectRiskTooLow         RejectedModeReason = "risk_below_threshold"
	RejectAddonConflict      RejectedModeReason = "addon_conflict"
	RejectFallbackFrom       RejectedModeReason = "fallback_from_higher_priority"
	RejectUserFlagDisabled   RejectedModeReason = "user_flag_disabled"
	RejectSandboxForbidden   RejectedModeReason = "sandbox_forbidden_combo"
	RejectBudgetZero         RejectedModeReason = "budget_zero"
)

// ModeCandidate is a single scored mode option in the policy scoring path.
type ModeCandidate struct {
	Mode           RoutingMode
	Score          float64
	CostEstimate   float64
	LatencyTier    string
	SignalsHit     []string
	Rejected       bool
	RejectReason   RejectedModeReason
	SuggestedAddons []Capability
}

// RejectedMode is the API-friendly rejected-mode entry.
type RejectedMode struct {
	Mode   RoutingMode      `json:"mode"`
	Reason RejectedModeReason `json:"reason"`
}

// ─── BudgetLatencySignals (v1+v2) ─────────────────────────────────────

// BudgetLatencySignals captures the budget/latency envelope.
type BudgetLatencySignals struct {
	BudgetUSD            float64
	EstimatedCostUSD     float64
	BudgetHeadroom       float64
	MaxLatencyMs         int
	EstimatedLatencyMs   int
	LatencyHeadroomMs    int
	LatencyTight         bool
}

// ─── RouterDecisionExplanation (v2 Phase 7I) ──────────────────────────

// RouterDecisionExplanation is the structured explanation of a routing
// decision. Used both for the audit trail and for the frontend
// contract.
type RouterDecisionExplanation struct {
	SelectedMode            RoutingMode
	SelectedReason          string

	AddonCapabilities       []Capability
	RequiredCapabilities    []Capability
	DisabledCapabilities    []Capability
	WorkspaceArtifactsExpected []string
	EstimatedCostUSD        float64
	EstimatedLatencyMs      int
	AsyncRequired           bool
	AuditRequired           bool

	ScoreBreakdown          map[RoutingMode]float64
	Candidates              []ModeCandidate
	RejectedModes           []RejectedMode
	Signals                 RouterDecisionSignals
	PolicyVersion           string
	// ClassifierMetadata records the LLM-assisted Router Arbiter's
	// participation in this decision. Nil when the classifier did not
	// run (the common case — classifier is OFF by default).
	ClassifierMetadata      *ClassifierAuditFields `json:"classifier_metadata,omitempty"`
}

// FrontendExplanation is the human-readable explanation for the frontend.
type FrontendExplanation struct {
	SelectedModeHuman string   `json:"selected_mode_human"`
	Why               string   `json:"why"`
	CapabilitySummary []string `json:"capability_summary"`
	CostEstimate      string   `json:"cost_estimate"`
	ApprovalNeeded    bool     `json:"approval_needed"`
}

// FrontendRouterContract is the API-facing router decision payload
// returned by /api/v1/tasks/route and the async execute-routed submit.
type FrontendRouterContract struct {
	SelectedMode            RoutingMode        `json:"selected_mode"`
	PlannedMode             RoutingMode        `json:"planned_mode"`
	ExecutedMode            RoutingMode        `json:"executed_mode"`
	FallbackReason          string             `json:"fallback_reason"`
	AddonCapabilities      []Capability       `json:"addon_capabilities"`
	RequiredCapabilities   []Capability       `json:"required_capabilities"`
	DisabledCapabilities   []Capability       `json:"disabled_capabilities"`
	ApprovalRequired       bool               `json:"approval_required"`
	ApprovalReason         string             `json:"approval_reason"`
	AsyncRequired          bool               `json:"async_required"`
	WorkspaceArtifactsExpected []string        `json:"workspace_artifacts_expected"`
	AuditRequired          bool               `json:"audit_required"`
	EstimatedCostUSD       float64            `json:"estimated_cost_usd"`
	EstimatedLatencyMs     int                `json:"estimated_latency_ms"`
	RiskLevel              string             `json:"risk_level"`
	ModelTier              string             `json:"model_tier"`
	ModelUsedHint          string             `json:"model_used_hint"`
	Provider               string             `json:"provider"`
	ReasonCodes            []string           `json:"reason_codes"`
	Candidates             []ModeCandidate    `json:"candidates"`
	RejectedModes          []RejectedMode     `json:"rejected_modes"`
	ScoreBreakdown         map[string]float64 `json:"score_breakdown"`
	PolicyVersion          string             `json:"policy_version"`
	ClassifierUsed         bool               `json:"classifier_used"`
	FrontendExplanation    *FrontendExplanation `json:"frontend_explanation,omitempty"`
}

// ─── driver.Valuer for JSONB columns ──────────────────────────────────
//
// RouterDecisionSignals and RouterDecisionExplanation implement
// driver.Valuer so they can be written directly to JSONB columns
// (migration 016) via the pgx driver. Marshalling happens in Value()
// so callers pass *Signals / *Explanation directly to db.Exec / tx.

func (s *RouterDecisionSignals) Value() (driver.Value, error) {
	if s == nil {
		return nil, nil
	}
	b, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("marshal RouterDecisionSignals: %w", err)
	}
	return b, nil
}

func (e *RouterDecisionExplanation) Value() (driver.Value, error) {
	if e == nil {
		return nil, nil
	}
	b, err := json.Marshal(e)
	if err != nil {
		return nil, fmt.Errorf("marshal RouterDecisionExplanation: %w", err)
	}
	return b, nil
}

// ─── v3 Section 14 — LLM-assisted Router Arbiter types ──────────────

// LLMClassifierInput is what EvaluateRoutingPolicy feeds to the LLM
// classifier when the heuristic is ambiguous. It is serialised into
// the prompt and contains the query, top-N candidates with their
// scores, and a compact signals summary (so the LLM does not have to
// re-derive what the heuristic already computed).
type LLMClassifierInput struct {
	SessionID        string                          `json:"session_id"`
	Query            string                          `json:"query"`
	UserIntent       string                          `json:"user_intent,omitempty"`
	TopNCandidates   []ModeCandidate                 `json:"top_n_candidates"`
	SignalsSummary   string                          `json:"signals_summary"` // pre-formatted text
	PolicyVersion    string                          `json:"policy_version"`
	BudgetUSD        float64                         `json:"budget_usd"`
	MaxLatencyMs     int                             `json:"max_latency_ms"`
	TriggerReason    string                          `json:"trigger_reason"` // C1..C8
}

// LLMClassifierOutput is the parsed LLM response. The Activity fills
// Fallback=true on any failure mode (timeout, invalid JSON, missing
// fields, score out of range). Caller MUST honour FailOpenToHeuristic
// when Fallback is true.
type LLMClassifierOutput struct {
	Scores       map[string]float64 `json:"scores"`
	Reasoning    string             `json:"reasoning"`
	Confidence   float64            `json:"confidence"`
	SelectedMode string             `json:"selected_mode"`
	Fallback     bool               `json:"fallback"`
	ModelUsed    string             `json:"model_used"`
	TokensUsed   int                `json:"tokens_used"`
	LatencyMs    int                `json:"latency_ms"`
	ErrorReason  string             `json:"error_reason,omitempty"`
}

// ClassifierAuditFields is the subset of classifier-internal data that
// gets persisted to the audit trail and exposed via the frontend
// contract. It is a separate struct so callers can read it without
// pulling the full LLM response.
type ClassifierAuditFields struct {
	Used           bool                `json:"used"`
	TriggerReason  string              `json:"trigger_reason,omitempty"`
	ModelUsed      string              `json:"model_used,omitempty"`
	TokensUsed     int                 `json:"tokens_used,omitempty"`
	LatencyMs      int                 `json:"latency_ms,omitempty"`
	Confidence     float64             `json:"confidence,omitempty"`
	Fallback       bool                `json:"fallback"`
	ErrorReason    string              `json:"error_reason,omitempty"`
	RawScores      map[string]float64  `json:"raw_scores,omitempty"`
	Reasoning      string              `json:"reasoning,omitempty"`
	PolicyVersion  string              `json:"policy_version,omitempty"`
}
