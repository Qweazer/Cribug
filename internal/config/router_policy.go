package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// RouterPolicyV2 is the v2 multi-signal policy for Phase 7I.
// All fields are explicit so the Router can score every mode against
// the same set of signals.
type RouterPolicyV2 struct {
	Version string `yaml:"version"`

	Thresholds         PolicyThresholds         `yaml:"thresholds"`
	Risk               PolicyRiskThresholds     `yaml:"risk"`
	Budget             PolicyBudget             `yaml:"budget"`
	Latency            PolicyLatency            `yaml:"latency"`
	Keywords           PolicyKeywords           `yaml:"keywords"`
	SignalWeights      map[string]PolicyModeW   `yaml:"signal_weights"`
	CapabilityRequirements map[string]string      `yaml:"capability_requirements"`
	DefaultAddons      map[string][]string      `yaml:"default_addons"`
	ForcedAddons       []PolicyForcedAddon      `yaml:"forced_addons"`
	ForbiddenCombos    []PolicyForbiddenCombo   `yaml:"forbidden_combinations"`
	FallbackOrder      map[string][]string      `yaml:"fallback_order"`
	DisabledBehavior   map[string]PolicyDisabled `yaml:"disabled_behavior"`
	ModeFeatureFlags   map[string]bool          `yaml:"mode_feature_flags"`
	ApprovalHints      PolicyApprovalHints     `yaml:"approval_hints"`
	Classifier         PolicyClassifier         `yaml:"classifier"`
	Telemetry          PolicyTelemetry          `yaml:"telemetry"`
	Legacy             PolicyLegacy             `yaml:"legacy"`
}

type PolicyThresholds struct {
	DirectAnswerMax    float64 `yaml:"direct_answer_max"`
	RagMin             float64 `yaml:"rag_min"`
	ReactMin           float64 `yaml:"react_min"`
	DagMin             float64 `yaml:"dag_min"`
	ReflectionMin      float64 `yaml:"reflection_min"`
	TreeOfThoughtsMin  float64 `yaml:"tree_of_thoughts_min"`
	DebateMin          float64 `yaml:"debate_min"`
	DebateMax          float64 `yaml:"debate_max"`
	ResearchV2Min      float64 `yaml:"research_v2_min"`
	SwarmMin           float64 `yaml:"swarm_min"`
	SandboxMinRisk     string  `yaml:"sandbox_min_risk"`
}

type PolicyRiskThresholds struct {
	LowMax          float64 `yaml:"low_max"`
	MediumMax       float64 `yaml:"medium_max"`
	HighMax         float64 `yaml:"high_max"`
	CriticalMin     float64 `yaml:"critical_min"`
	RequireApprovalMin string `yaml:"require_approval_min"`
}

type PolicyBudget struct {
	CheapMaxUsd         float64            `yaml:"cheap_max_usd"`
	MediumMaxUsd        float64            `yaml:"medium_max_usd"`
	ExpensiveMinUsd     float64            `yaml:"expensive_min_usd"`
	EstimatedCostUsd    map[string]float64 `yaml:"estimated_cost_usd"`
}

type PolicyLatency struct {
	FastMaxMs          float64            `yaml:"fast_max_ms"`
	MediumMaxMs        float64            `yaml:"medium_max_ms"`
	SlowMinMs          float64            `yaml:"slow_min_ms"`
	EstimatedLatencyMs map[string]float64 `yaml:"estimated_latency_ms"`
}

type PolicyKeyword struct {
	WeightPerMatch float64  `yaml:"weight_per_match"`
	Keywords       []string  `yaml:"keywords"`
}

type PolicyKeywords struct {
	Debate         PolicyKeyword `yaml:"debate"`
	TreeOfThoughts PolicyKeyword `yaml:"tree_of_thoughts"`
	ResearchV2     PolicyKeyword `yaml:"research_v2"`
	SandboxExec    PolicyKeyword `yaml:"sandbox_execution"`
	Swarm          PolicyKeyword `yaml:"swarm_workflow"`
	Reflection     PolicyKeyword `yaml:"reflection"`
	Dag            PolicyKeyword `yaml:"dag_workflow"`
	Skills         PolicyKeyword `yaml:"skills"`
	McpTools       PolicyKeyword `yaml:"mcp_tools"`
}

type PolicyModeW struct {
	Base               float64 `yaml:"base"`
	Analyze            float64 `yaml:"analyze"`
	Research           float64 `yaml:"research"`
	Execution          float64 `yaml:"execution"`
	MultiAgent         float64 `yaml:"multi_agent"`
	Debate             float64 `yaml:"debate"`
	Exploration        float64 `yaml:"exploration"`
	ToolNeed           float64 `yaml:"tool_need"`
	RagNeed            float64 `yaml:"rag_need"`
	SandboxNeed        float64 `yaml:"sandbox_need"`
	NeedsCitations     float64 `yaml:"needs_citation"`
	ComplexityOverall  float64 `yaml:"complexity_overall"`
	RiskPenalty        float64 `yaml:"risk_penalty"`
}

type PolicyForcedAddon struct {
	When string  `yaml:"when"`
	Add  []string `yaml:"add"`
}

type PolicyForbiddenCombo struct {
	Modes      []string `yaml:"modes"`
	When       string   `yaml:"when"`
	Reason     string   `yaml:"reason"`
}

type PolicyDisabled struct {
	WhenDisabled    string `yaml:"when_disabled"`
	WhenUserBlocks  string `yaml:"when_user_blocks"`
}

type PolicyApprovalHints struct {
	AutoApprovalModes  []string                       `yaml:"auto_approval_modes"`
	SkipApprovalWhen   []PolicySkipApprovalCondition  `yaml:"skip_approval_when"`
}

type PolicySkipApprovalCondition struct {
	Mode           string  `yaml:"mode"`
	MaxComplexity  float64 `yaml:"max_complexity"`
}

type PolicyClassifier struct {
	Enabled          bool                            `yaml:"enabled"`
	EnabledField     string                          `yaml:"enabled_field"`
	RealTestOnly     bool                            `yaml:"real_test_only"`
	ModelTier        string                          `yaml:"model_tier"`
	MaxTokens        int                             `yaml:"max_tokens"`
	SkipWhen         []string                        `yaml:"skip_when"`
	InvokeOnlyWhen   []string                        `yaml:"invoke_only_when"`
	PromptTemplate   string                          `yaml:"prompt_template"`
	EnvOverride      string                          `yaml:"env_override"`

	// ── v3 Phase 7I (Section 14) — LLM-assisted Router Arbiter ──
	// These fields control the secondary-arbiter LLM classifier that
	// re-ranks heuristic candidates when the routing decision is
	// ambiguous. The classifier is OFF by default and fully
	// independent from the Approval Gate kill switch.

	// MinScoreGapToSkip is the heuristic top-1 vs top-2 score gap above
	// which the LLM classifier is NOT invoked (clear winner).
	MinScoreGapToSkip float64 `yaml:"min_score_gap_to_skip"`
	// MaxCostUsdPerCall caps the LLM call budget per invocation.
	MaxCostUsdPerCall float64 `yaml:"max_cost_usd_per_call"`
	// TimeoutMs is the LLM call deadline; on timeout we fall back.
	TimeoutMs int `yaml:"timeout_ms"`
	// InvokeOnHighRisk triggers the LLM classifier when sandbox /
	// swarm / research_v2 are candidates with score >= 0.3.
	InvokeOnHighRisk bool `yaml:"invoke_on_high_risk"`
	// InvokeOnConflict triggers when capability signals conflict
	// (e.g. requires_research=true but budget_usd < 0.01).
	InvokeOnConflict bool `yaml:"invoke_on_conflict"`
	// InvokeOnNoClearWinner triggers when no mode has a clear lead.
	InvokeOnNoClearWinner bool `yaml:"invoke_on_no_clear_winner"`
	// InvokeOnDisabledFallback triggers when the planned mode is
	// disabled and the fallback chain has multiple candidates.
	InvokeOnDisabledFallback bool `yaml:"invoke_on_disabled_fallback"`
	// InvokeOnMultiAdvanced triggers when 3+ advanced modes are
	// candidates simultaneously.
	InvokeOnMultiAdvanced bool `yaml:"invoke_on_multi_advanced"`
	// InvokeOnLegacyDivergence is OFF by default — only enable for
	// A/B testing against the legacy if/else chain.
	InvokeOnLegacyDivergence bool `yaml:"invoke_on_legacy_divergence"`
	// OutputSchemaVersion is the JSON schema version the LLM must
	// emit; used to validate responses.
	OutputSchemaVersion string `yaml:"output_schema_version"`
	// FailOpenToHeuristic is true → any classifier failure (timeout /
	// invalid JSON / LLM error) falls back to the heuristic
	// candidates without failing the workflow.
	FailOpenToHeuristic bool `yaml:"fail_open_to_heuristic"`
	// RequiredFields are the keys the classifier output must contain.
	RequiredFields []string `yaml:"required_fields"`
	// Audit configures which classifier-internal fields are persisted.
	Audit PolicyClassifierAudit `yaml:"audit"`
}

// PolicyClassifierAudit controls which classifier-internal fields are
// written to the audit trail / decision explanation.
type PolicyClassifierAudit struct {
	RecordRawScores     bool `yaml:"record_raw_scores"`
	RecordTokensUsed    bool `yaml:"record_tokens_used"`
	RecordLatencyMs     bool `yaml:"record_latency_ms"`
	RecordTriggerReason bool `yaml:"record_trigger_reason"`
}

type PolicyTelemetry struct {
	LogRoutingDecisions bool     `yaml:"log_routing_decisions"`
	RecordSignals       bool     `yaml:"record_signals"`
	RecordExplanation   bool     `yaml:"record_explanation"`
	AuditPolicyVersion  bool     `yaml:"audit_policy_version"`
	EmitEvent           string   `yaml:"emit_event"`
	ForbiddenLog        []string `yaml:"forbidden_log,omitempty"`
}

type PolicyLegacy struct {
	Enabled     bool   `yaml:"enabled"`
	EnvOverride string `yaml:"env_override"`
}

// LoadRouterPolicyV2 reads config/router_policy.yaml. If the file
// is missing or malformed, returns DefaultRouterPolicyV2() so the
// Router still works in degraded mode.
func LoadRouterPolicyV2(path string) (*RouterPolicyV2, error) {
	if path == "" {
		return DefaultRouterPolicyV2(), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return DefaultRouterPolicyV2(), fmt.Errorf("read router policy: %w", err)
	}
	var p RouterPolicyV2
	if err := yaml.Unmarshal(data, &p); err != nil {
		return DefaultRouterPolicyV2(), fmt.Errorf("parse router policy: %w", err)
	}
	if p.Version == "" {
		p.Version = "1.0"
	}
	return &p, nil
}

// DefaultRouterPolicyV2 returns safe defaults. Used when policy file
// is missing or malformed. All thresholds and weights are explicit so
// scoring is deterministic.
func DefaultRouterPolicyV2() *RouterPolicyV2 {
	return &RouterPolicyV2{
		Version: "1.0",
		Thresholds: PolicyThresholds{
			DirectAnswerMax: 0.15, RagMin: 0.10, ReactMin: 0.20,
			DagMin: 0.35, ReflectionMin: 0.30, TreeOfThoughtsMin: 0.55,
			DebateMin: 0.35, DebateMax: 0.60, ResearchV2Min: 0.50,
			SwarmMin: 0.65, SandboxMinRisk: "high",
		},
		Risk: PolicyRiskThresholds{
			LowMax: 0.30, MediumMax: 0.60, HighMax: 0.85,
			CriticalMin: 0.85, RequireApprovalMin: "high",
		},
		Budget: PolicyBudget{
			CheapMaxUsd: 0.05, MediumMaxUsd: 0.25, ExpensiveMinUsd: 0.50,
			EstimatedCostUsd: map[string]float64{
				"direct_answer": 0.005, "rag_answer": 0.02, "react_tool": 0.03,
				"dag_workflow": 0.08, "reflection": 0.06, "tree_of_thoughts": 0.18,
				"debate": 0.12, "research_v2": 0.20, "swarm_workflow": 0.30,
				"sandbox_execution": 0.05,
			},
		},
		Latency: PolicyLatency{
			FastMaxMs: 5000, MediumMaxMs: 30000, SlowMinMs: 60000,
			EstimatedLatencyMs: map[string]float64{
				"direct_answer": 2000, "rag_answer": 5000, "react_tool": 8000,
				"dag_workflow": 20000, "reflection": 30000, "tree_of_thoughts": 90000,
				"debate": 60000, "research_v2": 120000, "swarm_workflow": 180000,
				"sandbox_execution": 30000,
			},
		},
		Keywords: PolicyKeywords{
			Debate:         PolicyKeyword{WeightPerMatch: 0.20, Keywords: []string{"比较", "对比", "vs ", "versus", "debate", "pros and cons", "trade-off", "which is better", "compare"}},
			TreeOfThoughts: PolicyKeyword{WeightPerMatch: 0.25, Keywords: []string{"explore multiple", "multi-path", "branch", "best path", "search tree", "explore paths", "find best approach", "analyze paths", "multiple perspectives"}},
			ResearchV2:     PolicyKeyword{WeightPerMatch: 0.25, Keywords: []string{"研究", "分析", "报告", "evidence", "citation", "sources", "literature", "review", "investigate", "with citations"}},
			SandboxExec:    PolicyKeyword{WeightPerMatch: 0.30, Keywords: []string{"执行", "运行", "沙箱", "代码", "编译", "测试", "execute", "run ", "sandbox", "python code", "compile"}},
			Swarm:          PolicyKeyword{WeightPerMatch: 0.30, Keywords: []string{"多agent", "多角色", "团队", "分工", "swarm", "multi-agent"}},
			Reflection:     PolicyKeyword{WeightPerMatch: 0.15, Keywords: []string{"分析", "评估", "改进", "analyze", "evaluate", "improve", "refine"}},
			Dag:            PolicyKeyword{WeightPerMatch: 0.15, Keywords: []string{"步骤", "step", "sequence", "workflow", "pipeline"}},
			Skills:         PolicyKeyword{WeightPerMatch: 0.10, Keywords: []string{"summarize", "calculate", "translate", "skill"}},
			McpTools:       PolicyKeyword{WeightPerMatch: 0.20, Keywords: []string{"mcp", "tool call", "external tool", "bash", "search"}},
		},
		SignalWeights: map[string]PolicyModeW{
			"direct_answer":    {Base: 0.15},
			"react_tool":       {Base: 0.10, ToolNeed: 0.30, ComplexityOverall: 0.30},
			"rag_answer":       {Base: 0.10, RagNeed: 0.40, ComplexityOverall: 0.20},
			"dag_workflow":     {Base: 0.05, ToolNeed: 0.20, ComplexityOverall: 0.30},
			"reflection":       {Base: 0.10, Analyze: 0.30, ComplexityOverall: 0.30},
			"tree_of_thoughts": {Base: 0.05, Exploration: 0.35, ComplexityOverall: 0.40},
			"debate":           {Base: 0.10, Debate: 0.40, ComplexityOverall: 0.20},
			"research_v2":      {Base: 0.10, Research: 0.35, NeedsCitations: 0.25, ComplexityOverall: 0.20},
			"swarm_workflow":   {Base: 0.05, MultiAgent: 0.40, ComplexityOverall: 0.30},
			"sandbox_execution": {Base: 0.05, Execution: 0.50, SandboxNeed: 0.40, RiskPenalty: 0.30},
		},
		CapabilityRequirements: map[string]string{
			"react_tool":        "requires_tools",
			"rag_answer":        "requires_rag",
			"dag_workflow":      "requires_tools",
			"sandbox_execution": "requires_sandbox",
			"research_v2":       "requires_research",
		},
		DefaultAddons: map[string][]string{
			"direct_answer":    {"workspace", "audit"},
			"rag_answer":       {"rag", "citations", "workspace", "audit"},
			"react_tool":       {"mcp_tools", "workspace", "audit"},
			"dag_workflow":     {"mcp_tools", "workspace", "audit"},
			"reflection":       {"workspace", "audit"},
			"tree_of_thoughts": {"workspace", "audit"},
			"debate":           {"workspace", "audit"},
			"research_v2":      {"rag", "citations", "workspace", "audit"},
			"swarm_workflow":   {"workspace", "audit"},
			"sandbox_execution": {"sandbox", "approval", "workspace", "audit"},
		},
		FallbackOrder: map[string][]string{
			"sandbox_execution":  {},
			"research_v2":       {"dag_workflow", "reflection"},
			"tree_of_thoughts":  {"debate", "reflection"},
			"debate":            {"reflection", "tree_of_thoughts"},
			"swarm_workflow":    {"dag_workflow", "reflection"},
			"dag_workflow":      {"react_tool", "reflection"},
			"reflection":        {"tree_of_thoughts", "react_tool", "direct_answer"},
			"react_tool":        {"direct_answer"},
			"rag_answer":        {"direct_answer"},
			"direct_answer":     {},
		},
		DisabledBehavior: map[string]PolicyDisabled{
			"sandbox_execution":  {WhenDisabled: "mode_disabled", WhenUserBlocks: "direct_answer"},
			"research_v2":       {WhenDisabled: "research_v1", WhenUserBlocks: "direct_answer"},
			"tree_of_thoughts":  {WhenDisabled: "reflection", WhenUserBlocks: "direct_answer"},
			"debate":            {WhenDisabled: "reflection", WhenUserBlocks: "direct_answer"},
			"swarm_workflow":    {WhenDisabled: "dag_workflow", WhenUserBlocks: "direct_answer"},
			"reflection":        {WhenDisabled: "tree_of_thoughts", WhenUserBlocks: "direct_answer"},
			"dag_workflow":      {WhenDisabled: "react_tool", WhenUserBlocks: "direct_answer"},
			"react_tool":        {WhenDisabled: "direct_answer", WhenUserBlocks: "direct_answer"},
			"rag_answer":        {WhenDisabled: "direct_answer", WhenUserBlocks: "direct_answer"},
			"direct_answer":     {WhenDisabled: "direct_answer", WhenUserBlocks: "direct_answer"},
		},
		ModeFeatureFlags: map[string]bool{
			"reflection": true, "tree_of_thoughts": true, "debate": true,
			"research_v2": true, "sandbox_execution": true,
		},
		ApprovalHints: PolicyApprovalHints{
			AutoApprovalModes: []string{"sandbox_execution"},
			SkipApprovalWhen:  []PolicySkipApprovalCondition{},
		},
		Classifier: PolicyClassifier{
			Enabled:        false,
			EnabledField:   "enable_router_classifier",
			RealTestOnly:   true,
			ModelTier:      "small",
			MaxTokens:      400,
			EnvOverride:    "ROUTER_CLASSIFIER_ENABLED",
			// v3 defaults
			MinScoreGapToSkip:        0.10,
			MaxCostUsdPerCall:        0.005,
			TimeoutMs:                3000,
			InvokeOnHighRisk:         true,
			InvokeOnConflict:         true,
			InvokeOnNoClearWinner:    true,
			InvokeOnDisabledFallback: true,
			InvokeOnMultiAdvanced:    true,
			InvokeOnLegacyDivergence: false,
			OutputSchemaVersion:      "1.0",
			FailOpenToHeuristic:      true,
			RequiredFields:           []string{"scores", "reasoning", "confidence"},
			Audit: PolicyClassifierAudit{
				RecordRawScores:     true,
				RecordTokensUsed:    true,
				RecordLatencyMs:     true,
				RecordTriggerReason: true,
			},
		},
		Telemetry: PolicyTelemetry{
			LogRoutingDecisions: true, RecordSignals: true,
			RecordExplanation: true, AuditPolicyVersion: true,
			EmitEvent: "router_decision_emitted",
		},
		Legacy: PolicyLegacy{Enabled: false, EnvOverride: "ROUTER_LEGACY_HEURISTIC"},
	}
}

// ValidatePolicy does sanity checks on the policy.
func (p *RouterPolicyV2) ValidatePolicy() error {
	if p == nil {
		return fmt.Errorf("nil policy")
	}
	if p.Thresholds.DirectAnswerMax < 0 || p.Thresholds.DirectAnswerMax > 1 {
		return fmt.Errorf("invalid direct_answer_max: %f", p.Thresholds.DirectAnswerMax)
	}
	if p.Thresholds.SwarmMin < p.Thresholds.TreeOfThoughtsMin {
		return fmt.Errorf("swarm_min must be >= tree_of_thoughts_min")
	}
	for mode, w := range p.SignalWeights {
		if w.Base < 0 || w.Base > 1 {
			return fmt.Errorf("invalid base weight for %s: %f", mode, w.Base)
		}
	}
	if p.Classifier.Enabled && p.Classifier.EnvOverride == "" {
		return fmt.Errorf("classifier.enabled=true requires env_override")
	}
	return nil
}

// LegacyHeuristicEnabled checks the legacy kill switch.
func LegacyHeuristicEnabled() bool {
	v := os.Getenv("ROUTER_LEGACY_HEURISTIC")
	if v == "" {
		return false
	}
	b, _ := strconv.ParseBool(v)
	return b
}

// ClassifierEnabled checks the classifier kill switch.
// Independent from RequireApproval.
func ClassifierEnabled() bool {
	// policy.classifier.enabled has highest priority.
	if _, ok := os.LookupEnv("ROUTER_CLASSIFIER_ENABLED"); ok {
		v := strings.ToLower(os.Getenv("ROUTER_CLASSIFIER_ENABLED"))
		return v == "1" || v == "true" || v == "yes"
	}
	return false
}
