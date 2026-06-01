package types

// RoutingMode is the canonical routing mode for Phase 7 Advanced Strategy Router.
type RoutingMode string

const (
	RouteDirectAnswer     RoutingMode = "direct_answer"
	RouteRAGAnswer        RoutingMode = "rag_answer"
	RouteReActTool        RoutingMode = "react_tool"
	RouteSandboxExecution RoutingMode = "sandbox_execution"
	RouteDAGWorkflow      RoutingMode = "dag_workflow"
	RouteSwarmWorkflow    RoutingMode = "swarm_workflow"
	RouteReflection       RoutingMode = "reflection"
	RouteTreeOfThoughts   RoutingMode = "tree_of_thoughts"
	RouteDebate           RoutingMode = "debate"
	RouteResearchV1       RoutingMode = "research_v1"
	RouteResearchV2       RoutingMode = "research_v2"
	RouteModeDisabled     RoutingMode = "mode_disabled"
	RouteNotImplemented   RoutingMode = "not_implemented"
)

// RouteAddon represents an optional capability attached to a primary routing mode.
type RouteAddon string

const (
	AddonRAG        RouteAddon = "rag"
	AddonSandbox    RouteAddon = "sandbox"
	AddonReflection RouteAddon = "reflection"
	AddonDebate     RouteAddon = "debate"
	AddonToT        RouteAddon = "tree_of_thoughts"
	AddonResearchV2 RouteAddon = "research_v2"
	AddonApproval   RouteAddon = "approval"
)

// RouterConfigSnapshot carries the router configuration at Workflow start time.
// It MUST be passed via RouteRequest; Workflows MUST NOT read config/env dynamically.
type RouterConfigSnapshot struct {
	ClassifierMode           string  `json:"classifier_mode"`
	ComplexityDAGThreshold   float64 `json:"complexity_dag_threshold"`
	ComplexitySwarmThreshold float64 `json:"complexity_swarm_threshold"`
	ComplexityToTThreshold   float64 `json:"complexity_tot_threshold"`
	ResearchV2Threshold      float64 `json:"research_v2_threshold"`
	ApprovalRiskThreshold    string  `json:"approval_risk_threshold"`
	MaxClassificationTokens  int     `json:"max_classification_tokens"`
	DecisionAuditEnabled     bool    `json:"decision_audit_enabled"`
	DefaultMode              string  `json:"default_mode"`
	Enabled                  bool    `json:"enabled"`

	// Feature flags for Slice 25-28 gating
	EnableReflection bool `json:"enable_reflection"`
	EnableToT        bool `json:"enable_tot"`
	EnableDebate     bool `json:"enable_debate"`
	EnableResearchV2 bool `json:"enable_research_v2"`
}

// RouteRequest is the input to AdvancedRoutingWorkflow.
type RouteRequest struct {
	SessionID        string                 `json:"session_id"`
	Query            string                 `json:"query"`
	UserIntent       string                 `json:"user_intent,omitempty"`
	ContextSummary   map[string]interface{} `json:"context_summary,omitempty"`
	AvailableTools   []string               `json:"available_tools,omitempty"`
	BudgetUSD        float64                `json:"budget_usd"`
	MaxLatencyMs     int                    `json:"max_latency_ms"`
	RequireCitations bool                   `json:"require_citations"`
	AllowTools       bool                   `json:"allow_tools"`
	AllowSandbox     bool                   `json:"allow_sandbox"`
	AllowResearch    bool                   `json:"allow_research"`
	RiskLevelHint    string                 `json:"risk_level_hint,omitempty"`
	RouterConfig     RouterConfigSnapshot   `json:"router_config"`
	PreviewOnly      bool                   `json:"preview_only"` // true=route preview (no dispatch); false=execute-routed
}

// RoutingDecision is the output of the routing evaluation.
// Workflow history MUST only store metadata + refs; long explanations go via PolicyTraceRef.
type RoutingDecision struct {
	PlannedMode        RoutingMode            `json:"planned_mode"`
	Mode               RoutingMode            `json:"mode"` // executed_mode
	FallbackReason     string                 `json:"fallback_reason,omitempty"`
	AddonCapabilities  []RouteAddon           `json:"addon_capabilities"`
	WorkflowType       string                 `json:"workflow_type"`
	Reason             string                 `json:"reason"`
	ComplexityScore    float64                `json:"complexity_score"`
	RiskLevel          string                 `json:"risk_level"`
	RequiresApproval   bool                   `json:"requires_approval"`
	RequiresRAG        bool                   `json:"requires_rag"`
	RequiresTools      bool                   `json:"requires_tools"`
	RequiresSandbox    bool                   `json:"requires_sandbox"`
	RequiresWorkspace  bool                   `json:"requires_workspace"`
	RequiresReflection bool                   `json:"requires_reflection"`
	RequiresDebate     bool                   `json:"requires_debate"`
	RequiresToT        bool                   `json:"requires_tot"`
	RequiresResearchV2 bool                   `json:"requires_research_v2"`
	ModelTier          string                 `json:"model_tier"`
	TokenBudget        int                    `json:"token_budget"`
	CostBudgetUSD      float64                `json:"cost_budget_usd"`
	Confidence         float64                `json:"confidence"`
	PolicyTraceRef     string                 `json:"policy_trace_ref,omitempty"`
	Metadata           map[string]interface{} `json:"metadata,omitempty"`
}

// RoutedExecutionResult is the unified result returned by AdvancedRoutingWorkflow.
type RoutedExecutionResult struct {
	SessionID         string                 `json:"session_id"`
	WorkflowID        string                 `json:"workflow_id"`
	RunID             string                 `json:"run_id"`
	Decision          RoutingDecision        `json:"decision"`
	FinalAnswerRef    string                 `json:"final_answer_ref,omitempty"`
	FinalAnswerText   string                 `json:"final_answer_text,omitempty"` // ≤2KB
	FeedbackSummary   string                 `json:"feedback_summary,omitempty"`
	FeedbackRef       string                 `json:"feedback_ref,omitempty"`
	Reason            string                 `json:"reason,omitempty"`
	TokensUsed        int                    `json:"tokens_used"`
	CostUSD           float64                `json:"cost_usd"`
	Status            string                 `json:"status"`
	PendingApprovalID string                 `json:"pending_approval_id,omitempty"`
	Metadata          map[string]interface{} `json:"metadata,omitempty"`
}

const (
	RoutedStatusOK               = "ok"
	RoutedStatusPreview          = "preview" // route preview only; no downstream dispatch
	RoutedStatusApprovalRequired = "approval_required"
	RoutedStatusRejected         = "rejected"
	RoutedStatusTimeout          = "timeout"
	RoutedStatusPartial          = "partial"
	RoutedStatusModeDisabled     = "mode_disabled"
	RoutedStatusError            = "error"
)

// isHighRiskMode returns true for modes that must fail-closed on approval evaluation failure.
func IsHighRiskMode(mode RoutingMode) bool {
	switch mode {
	case RouteSandboxExecution, RouteResearchV2, RouteDAGWorkflow, RouteSwarmWorkflow:
		return true
	default:
		return false
	}
}
