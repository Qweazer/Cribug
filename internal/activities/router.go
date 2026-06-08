package activities

import (
	"log"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"cribug/internal/db"
	"cribug/internal/types"
)

// RouterActivities holds dependencies needed by Router Activities.
type RouterActivities struct {
	db             *sql.DB
	llmServiceURL  string
	llmModel       string
	llmMaxTokens   int
	llmTemperature float64
}

// NewRouterActivities creates a new RouterActivities instance.
// The LLM-related fields are optional; when llmServiceURL is empty the
// classifier falls back to a deterministic mock (still preserving
// fail_open_to_heuristic behaviour for invalid JSON / errors).
func NewRouterActivities(db *sql.DB) *RouterActivities {
	ra := &RouterActivities{db: db}
	routerActivitiesSingleton = ra
	return ra
}

// NewRouterActivitiesWithLLM creates a RouterActivities wired to a real
// LLM service. classifier smoke scripts use this so the
// LLMClassifierActivity actually invokes the provider instead of the
// mock fallback.
func NewRouterActivitiesWithLLM(db *sql.DB, llmServiceURL, llmModel string, llmMaxTokens int, llmTemperature float64) *RouterActivities {
	ra := &RouterActivities{
		db:             db,
		llmServiceURL:  llmServiceURL,
		llmModel:       llmModel,
		llmMaxTokens:   llmMaxTokens,
		llmTemperature: llmTemperature,
	}
	routerActivitiesSingleton = ra
	return ra
}

// ─── ClassifyTaskComplexity ────────────────────────────────────────────

type ClassifyTaskComplexityInput struct {
	Query      string `json:"query"`
	UserIntent string `json:"user_intent,omitempty"`
}

type ClassifyTaskComplexityResult struct {
	ComplexityScore float64 `json:"complexity_score"`
	RiskLevel       string  `json:"risk_level"`
	Summary         string  `json:"summary"`
	ReasoningRef    string  `json:"reasoning_ref"` // placeholder; real impl writes to Workspace via Activity
	TokensUsed      int     `json:"tokens_used"`
}

func (ra *RouterActivities) ClassifyTaskComplexity(ctx context.Context, input ClassifyTaskComplexityInput) (*ClassifyTaskComplexityResult, error) {
	score := classifyHeuristic(input.Query, input.UserIntent)
	risk := riskFromScore(score)
	// Tag the summary with keyword-flags so the policy evaluator
	// (which only sees the summary, not the raw query) can make
	// mode decisions like "route to debate" without re-running the
	// heuristic. This is a minimal, non-restructuring addition.
	flags := classifierKeywordFlags(input.Query)
	summary := fmt.Sprintf("complexity=%.2f risk=%s", score, risk)
	if flags != "" {
		summary = summary + " " + flags
	}
	return &ClassifyTaskComplexityResult{
		ComplexityScore: score,
		RiskLevel:       risk,
		Summary:         summary,
		TokensUsed:      0, // heuristic uses 0 tokens
	}, nil
}

// classifierKeywordFlags returns a space-separated list of mode-tag
// keywords detected in the query, e.g. "kw:debate". The flags are
// propagated via the complexity summary so downstream policy logic
// can route by keyword without re-parsing the query.
func classifierKeywordFlags(query string) string {
	lower := strings.ToLower(query)
	tags := []string{}
	for _, kw := range []string{"比较", "对比", "vs ", "versus", "debate", "pros and cons", "trade-off",
		"postgresql", "mongodb", "which is better", "compare"} {
		if strings.Contains(lower, strings.ToLower(kw)) {
			tags = append(tags, "kw:debate")
			break
		}
	}
	return strings.Join(tags, " ")
}

func classifyHeuristic(query, intent string) float64 {
	lower := strings.ToLower(query)
	score := 0.0

	// Longer queries signal more complexity
	if len(query) < 30 {
		score += 0.05
	} else if len(query) > 120 {
		score += 0.20
	} else if len(query) > 80 {
		score += 0.10
	}

	// Keywords indicating higher complexity (Chinese + English)
	complexKeywords := []string{"分析", "对比", "方案", "比较", "规划", "设计", "架构", "优化",
		"analyze", "compare", "design", "architecture", "optimize", "evaluate", "refactor",
		"pros and cons", "trade-off", "calculate", "compute", "tool"}
	for _, kw := range complexKeywords {
		if strings.Contains(lower, kw) {
			score += 0.15
			break
		}
	}

	// Research / reporting keywords
	researchKeywords := []string{"研究", "报告", "引用", "多源", "证据", "调研",
		"research", "report", "cite", "multi-source", "evidence", "citation", "multiple sources"}
	for _, kw := range researchKeywords {
		if strings.Contains(lower, kw) {
			score += 0.20
			break
		}
	}

	// Sandbox / execution keywords
	execKeywords := []string{"执行", "运行", "沙箱", "代码", "编译", "测试",
		"execute", "run ", "sandbox", "python code", "script", "compile", "wasi"}
	for _, kw := range execKeywords {
		if strings.Contains(lower, kw) {
			score += 0.20
			break
		}
	}

	// Multi-agent / swarm keywords
	swarmKeywords := []string{"多agent", "多角色", "团队", "分工", "协作", "swarm",
		"multi-agent", "team of agents", "collaborat", "multiple agents", "microservice"}
	for _, kw := range swarmKeywords {
		if strings.Contains(lower, kw) {
			score += 0.20
			break
		}
	}

	// Debate / comparison keywords
	totKeywords := []string{"explore multiple", "multi-path", "branch", "best path", "search tree",
		"explore paths", "find best approach", "analyze paths", "multiple perspectives"}
	for _, kw := range totKeywords {
		if strings.Contains(lower, kw) {
			score += 0.30
			break
		}
	}

	debateKeywords := []string{"比较", "对比", "vs ", "versus", "debate", "pros and cons", "trade-off",
		"postgresql", "mongodb", "which is better", "compare"}
	for _, kw := range debateKeywords {
		if strings.Contains(lower, kw) {
			score += 0.15
			break
		}
	}

	// Multi-step detection: multiple sentences or numbered lists
	periodCount := strings.Count(query, ".") + strings.Count(query, "。")
	if periodCount >= 2 {
		score += 0.15
	}
	if strings.Count(query, "\n") >= 2 {
		score += 0.05
	}
	// Numbered lists (1. 2. 3. or 1) 2) 3))
	if strings.Contains(query, "first") || strings.Contains(query, "then") || strings.Contains(query, "finally") {
		score += 0.10
	}

	// User intent override
	if intent == "deep_research" {
		score += 0.30
	}

	if score > 1.0 {
		score = 1.0
	}
	return score
}

func riskFromScore(score float64) string {
	switch {
	case score >= 0.85:
		return "critical"
	case score >= 0.60:
		return "high"
	case score >= 0.30:
		return "medium"
	default:
		return "low"
	}
}

// ─── DetectTaskCapabilities ────────────────────────────────────────────

type DetectTaskCapabilitiesInput struct {
	Query          string   `json:"query"`
	AvailableTools []string `json:"available_tools"`
	AllowTools     bool     `json:"allow_tools"`
	AllowSandbox   bool     `json:"allow_sandbox"`
	AllowResearch  bool     `json:"allow_research"`
}

type DetectTaskCapabilitiesResult struct {
	RequiresTools    bool     `json:"requires_tools"`
	RequiresSandbox  bool     `json:"requires_sandbox"`
	RequiresRAG      bool     `json:"requires_rag"`
	RequiresResearch bool     `json:"requires_research"`
	DetectedTools    []string `json:"detected_tools"`
	Summary          string   `json:"summary"`
	ReasoningRef     string   `json:"reasoning_ref"`
}

func (ra *RouterActivities) DetectTaskCapabilities(ctx context.Context, input DetectTaskCapabilitiesInput) (*DetectTaskCapabilitiesResult, error) {
	lower := strings.ToLower(input.Query)
	result := &DetectTaskCapabilitiesResult{}
	// DEBUG
	if strings.Contains(lower, "research") || strings.Contains(lower, "investigate") {
		fmt.Printf("DEBUG DetectTask: query=%q lower=%q\n", input.Query, lower)
	}

	// RAG: project doc / knowledge base keywords
	ragKeywords := []string{"项目文档", "代码库", "知识库", "我的项目", "doc", "readme", "api文档", "documentation", "knowledge", "module"}
	for _, kw := range ragKeywords {
		if strings.Contains(lower, strings.ToLower(kw)) {
			result.RequiresRAG = true
			break
		}
	}

	// Tools
	toolKeywords := []string{"搜索", "查询", "计算", "calculate", "search", "curl", "api调用", "calculator", "tool"}
	for _, kw := range toolKeywords {
		if strings.Contains(lower, strings.ToLower(kw)) {
			result.RequiresTools = true
			break
		}
	}
	if input.AllowTools && len(input.AvailableTools) > 0 {
		result.RequiresTools = true
		result.DetectedTools = input.AvailableTools
	}

	// Sandbox
	sandboxKeywords := []string{
		"运行代码", "执行脚本", "沙箱", "sandbox", "编译", "wasi",
		"execute", "run", "python", "code", "脚本",
		// Phase 7I Fix-4: extended risk keywords. Any of these implies
		// "the user is about to run untrusted / unknown code" and
		// must trigger sandbox_execution + approval.
		"未知脚本", "不可信代码", "未知来源", "未验证", "untrusted", "unknown script",
		"读写文件", "临时文件", "临时目录", "read file", "write file",
		"shell", "bash", "python code", "execute code",
	}
	for _, kw := range sandboxKeywords {
		if strings.Contains(lower, strings.ToLower(kw)) {
			result.RequiresSandbox = true
			break
		}
	}

	// Research
	researchKeywords := []string{"研究", "报告", "调研", "多来源", "证据", "引用", "citation", "research", "literature", "evidence", "investigate", "study"}
	for _, kw := range researchKeywords {
		if strings.Contains(lower, strings.ToLower(kw)) {
			result.RequiresResearch = true
			break
		}
	}

	result.Summary = fmt.Sprintf("tools=%v sandbox=%v rag=%v research=%v",
		result.RequiresTools, result.RequiresSandbox, result.RequiresRAG, result.RequiresResearch)
	return result, nil
}

// ─── EvaluateRoutingPolicy ─────────────────────────────────────────────

type EvaluateRoutingPolicyInput struct {
	ComplexityScore  float64                    `json:"complexity_score"`
	RiskLevel        string                     `json:"risk_level"`
	ComplexitySummary string                    `json:"complexity_summary,omitempty"`
	RequiresTools    bool                       `json:"requires_tools"`
	RequiresSandbox  bool                       `json:"requires_sandbox"`
	RequiresRAG      bool                       `json:"requires_rag"`
	RequiresResearch bool                       `json:"requires_research"`
	RequireCitations bool                       `json:"require_citations"`
	// Phase 7I: User-allow flags. Distinct from Requires*:
	// Requires* = "the task needs this capability" (from detection);
	// Allow* = "the user has authorised this capability" (from request).
	// A user-blocked capability must reject the corresponding mode
	// regardless of Requires* state.
	AllowTools       bool                       `json:"allow_tools"`
	AllowSandbox     bool                       `json:"allow_sandbox"`
	AllowResearch    bool                       `json:"allow_research"`
	BudgetUSD        float64                    `json:"budget_usd"`
	RouterConfig     types.RouterConfigSnapshot `json:"router_config"`
	// Phase 7I: Query + UserIntent are passed through to the signal
	// builder so scoring has the exact text to keyword-match.
	Query      string `json:"query,omitempty"`
	UserIntent string `json:"user_intent,omitempty"`
}

type EvaluateRoutingPolicyResult struct {
	Decision    types.RoutingDecision         `json:"decision"`
	Explanation *types.RouterDecisionExplanation `json:"explanation,omitempty"`
	// Signals is the full multi-signal snapshot used to make the
	// decision. Persisted into the audit trail for replay / debugging.
	Signals *types.RouterDecisionSignals `json:"signals,omitempty"`
}

func (ra *RouterActivities) EvaluateRoutingPolicy(ctx context.Context, input EvaluateRoutingPolicyInput) (*EvaluateRoutingPolicyResult, error) {
	cfg := input.RouterConfig

	// Phase 7I: multi-signal v2 path (with legacy fallback).
	planned, expl := selectPlannedModeV2(ctx, input)

	// Determine executed mode (feature-flag + user-block gating,
	// consulting policy YAML's disabled_behavior for fallback targets).
	caps := types.CapabilityNeeds{
		NeedsTools:    input.RequiresTools,
		NeedsRAG:      input.RequiresRAG,
		NeedsSandbox:  input.RequiresSandbox,
		NeedsResearch: input.RequiresResearch,
	}
	executed, fallbackReason := resolveExecutedMode(planned, cfg, caps)

	// Determine addons (legacy RouteAddons for backward compat).
	addons := selectAddons(input, planned)

	// Workflow type string
	wfType := workflowTypeForMode(executed)

	// Model tier
	modelTier := "small"
	if input.ComplexityScore >= 0.45 {
		modelTier = "medium"
	}
	if input.ComplexityScore >= 0.75 {
		modelTier = "large"
	}

	// Token budget
	tokenBudget := estimateTokenBudget(planned, input.ComplexityScore)

	// Approval. Phase 7I Fix-5: sandbox_execution ALWAYS requires
	// approval regardless of risk level or complexity (per v3 §14.7).
	requiresApproval := input.ComplexityScore >= 0.60 ||
		input.RiskLevel == "high" ||
		input.RiskLevel == "critical" ||
		input.RequiresSandbox ||
		executed == types.RouteSandboxExecution ||
		planned == types.RouteSandboxExecution

	decision := types.RoutingDecision{
		PlannedMode:        planned,
		Mode:               executed,
		FallbackReason:     fallbackReason,
		AddonCapabilities:  addons,
		WorkflowType:       wfType,
		Reason:             fmt.Sprintf("complexity=%.2f risk=%s planned=%s executed=%s score=%.2f", input.ComplexityScore, input.RiskLevel, planned, executed, expl.ScoreBreakdown[planned]),
		ComplexityScore:    input.ComplexityScore,
		RiskLevel:          input.RiskLevel,
		RequiresApproval:   requiresApproval,
		RequiresRAG:        input.RequiresRAG,
		RequiresTools:      input.RequiresTools,
		RequiresSandbox:    input.RequiresSandbox,
		RequiresReflection: hasAddon(addons, types.AddonReflection),
		RequiresDebate:     hasAddon(addons, types.AddonDebate),
		RequiresToT:        hasAddon(addons, types.AddonToT),
		RequiresResearchV2: planned == types.RouteResearchV2,
		ModelTier:          modelTier,
		TokenBudget:        tokenBudget,
		CostBudgetUSD:      input.BudgetUSD,
		Confidence:         0.80,
		// Phase 7I v2 capability composition + frontend contract.
		V2AddonCapabilities:       capsToStrings(expl.AddonCapabilities),
		V2RequiredCapabilities:    capsToStrings(expl.RequiredCapabilities),
		V2DisabledCapabilities:    capsToStrings(expl.DisabledCapabilities),
		V2WorkspaceArtifactsExpected: expl.WorkspaceArtifactsExpected,
		V2EstimatedCostUSD:        expl.EstimatedCostUSD,
		V2EstimatedLatencyMs:      expl.EstimatedLatencyMs,
		V2AsyncRequired:           expl.AsyncRequired,
		V2AuditRequired:           expl.AuditRequired,
		V2PolicyVersion:           expl.PolicyVersion,
	}

	// ── P0 SAFETY STEP: sandbox_execution MUST require approval ────
	// Phase 7I P0: explicit safety guarantee, not bypassable.
	// - sandbox mode selected → approval_required = true
	// - allow_sandbox=false → executed mode forced to direct_answer
	if decision.Mode == types.RouteSandboxExecution && !decision.RequiresApproval {
		decision.RequiresApproval = true
		decision.Reason += "; p0_safety: sandbox_requires_approval_enforced"
	}
	if !input.AllowSandbox && decision.Mode == types.RouteSandboxExecution {
		decision.Mode = types.RouteDirectAnswer
		decision.FallbackReason = "p0_safety: sandbox_disallowed_by_user"
		decision.RequiresApproval = false
		decision.Reason += "; p0_safety: allow_sandbox=false forced fall back to direct_answer"
	}

	return &EvaluateRoutingPolicyResult{Decision: decision, Explanation: &expl, Signals: &expl.Signals}, nil
}

func capsToStrings(caps []types.Capability) []string {
	out := make([]string, len(caps))
	for i, c := range caps {
		out[i] = string(c)
	}
	return out
}

func selectPlannedMode(input EvaluateRoutingPolicyInput) types.RoutingMode {
	c := input.ComplexityScore
	summary := strings.ToLower(input.ComplexitySummary)
	query := strings.ToLower(input.Query)
	log.Printf("[P7J-V1] query=%q summary=%q c=%f", query, summary, c)

	// ── Phase 7J: Keyword short-circuit — multi-stage intents always go DAG ──
	dagKeywords := []string{
		"比较", "对比", "vs", "推荐", "挑选",
		"分析", "拆解", "分解", "规划", "策划",
		"列出", "列举", "步骤", "流程", "方案",
		"设计", "架构", "选型", "评估",
		"compare", "analyze", "plan", "list", "steps",
		"design", "architect", "evaluate", "recommend",
		"为什么", "如何", "怎么",
	}
	for _, kw := range dagKeywords {
		if strings.Contains(summary, kw) || strings.Contains(query, kw) {
			return types.RouteDAGWorkflow
		}
	}

	// Sandbox
	if input.RequiresSandbox {
		return types.RouteSandboxExecution
	}

	// Deep research (high complexity + citations or explicit research need)
	if input.RequireCitations && input.RequiresResearch && c >= 0.50 {
		return types.RouteResearchV2
	}
	if input.RequiresResearch && c >= 0.65 {
		return types.RouteResearchV2
	}

	// ToT: multi-path exploration/comparison at high complexity (c >= 0.60, before Swarm)
	if c >= 0.50 {
		return types.RouteTreeOfThoughts
	}

	// Debate: comparative/argumentative query (Phase 7E Slice 27). Mirrors
	// the ToT placement (before Swarm/DAG/Reflection). This is a
	// minimal addition to surface the already-declared RouteDebate
	// mode for medium complexity (0.20..0.60) when the heuristic
	// flagged debate keywords. Full Router Strategy refactor is
	// explicitly deferred to a later phase.
	if c >= 0.20 && c < 0.60 && strings.Contains(strings.ToLower(input.ComplexitySummary), "debate") {
		return types.RouteDebate
	}

	// Swarm (very high complexity with multi-agent keywords, c >= 0.70)
	if c >= 0.60 {
		return types.RouteSwarmWorkflow
	}

	// DAG (Phase 7J: lowered — any non-trivial complexity)
	if input.RequiresTools && c >= 0.15 {
		return types.RouteDAGWorkflow
	}
	if c >= 0.15 {
		return types.RouteDAGWorkflow
	}

	// Reflection: medium complexity writing/analysis tasks
	if c >= 0.25 {
		return types.RouteReflection
	}


	// ReAct (tools needed but not too complex)
	if input.RequiresTools && c >= 0.20 {
		return types.RouteReActTool
	}

	// RAG (local knowledge needed)
	if input.RequiresRAG {
		return types.RouteRAGAnswer
	}

	// Direct answer (default)
	return types.RouteDirectAnswer
}

func selectAddons(input EvaluateRoutingPolicyInput, planned types.RoutingMode) []types.RouteAddon {
	addons := make([]types.RouteAddon, 0) // non-nil for valid JSON

	if input.RequiresRAG && planned != types.RouteRAGAnswer {
		addons = append(addons, types.AddonRAG)
	}
	if input.RequiresSandbox && planned != types.RouteSandboxExecution {
		addons = append(addons, types.AddonSandbox)
	}
	if input.ComplexityScore >= 0.60 && planned != types.RouteReflection {
		addons = append(addons, types.AddonReflection)
	}

	// Limit to 3 addons
	if len(addons) > 3 {
		addons = addons[:3]
	}
	return addons
}

// resolveExecutedMode resolves the actual executed mode from the planned mode,
// taking feature flags into account. Research v2 falls back to research v1 when
// disabled (since v1 is always available via Phase 6F). Other future modes
// (reflection/tot/debate) return mode_disabled when their flags are off.
// resolveExecutedMode decides the final mode from the planned mode,
// consulting the policy YAML's `disabled_behavior` block for fallback
// targets. Two distinct reasons trigger a fallback:
//
//  1. **Disabled by config** (feature flag off): look up
//     `policy.DisabledBehavior[mode].WhenDisabled` for the fallback
//     mode name. A literal "mode_disabled" or empty value means no
//     fallback (gate closed).
//  2. **User-blocked** (allow_<flag> = false in capability detection):
//     look up `policy.DisabledBehavior[mode].WhenUserBlocks` for the
//     fallback target. Default is `direct_answer`.
//
// The returned reason is a short, human-readable string written into
// `Decision.FallbackReason` for audit / display.
func resolveExecutedMode(planned types.RoutingMode, cfg types.RouterConfigSnapshot, caps types.CapabilityNeeds) (types.RoutingMode, string) {
	// Fast-path: per-mode feature flags (v1 behaviour, kept for
	// backward compatibility — the YAML lookup below is a superset).
	switch planned {
	case types.RouteReflection:
		if !cfg.EnableReflection {
			return resolveDisabledFallback(planned, "reflection", "enable_reflection=false", false, caps)
		}
	case types.RouteTreeOfThoughts:
		if !cfg.EnableToT {
			return resolveDisabledFallback(planned, "tree_of_thoughts", "enable_tot=false", false, caps)
		}
	case types.RouteDebate:
		if !cfg.EnableDebate {
			return resolveDisabledFallback(planned, "debate", "enable_debate=false", false, caps)
		}
	case types.RouteResearchV2:
		if !cfg.EnableResearchV2 {
			return resolveDisabledFallback(planned, "research_v2", "research_v2_not_enabled_fallback_to_research_v1", false, caps)
		}
	case types.RouteSandboxExecution:
		// Sandbox is always gated by user flag, not config flag.
		if !caps.NeedsSandbox {
			return resolveDisabledFallback(planned, "sandbox_execution", "allow_sandbox=false", true, caps)
		}
	}

	// User-blocked fallback for any mode that requires a capability the
	// user disabled (handled here as a safety net in case the planned
	// mode reaches resolveExecutedMode without an explicit case above).
	switch planned {
	case types.RouteReActTool, types.RouteDAGWorkflow:
		if !caps.NeedsTools {
			return resolveDisabledFallback(planned, modeKeyString(planned), "allow_tools=false", true, caps)
		}
	case types.RouteRAGAnswer:
		if !caps.NeedsRAG {
			return resolveDisabledFallback(planned, "rag_answer", "allow_rag=false", true, caps)
		}
	}
	return planned, ""
}

// resolveDisabledFallback consults the policy YAML's DisabledBehavior
// for a fallback mode name. When the YAML has no entry for the given
// mode, defaults are: WhenDisabled="mode_disabled", WhenUserBlocks=
// "direct_answer".
func resolveDisabledFallback(planned types.RoutingMode, yamlKey, reason string, userBlocked bool, caps types.CapabilityNeeds) (types.RoutingMode, string) {
	policy := getPolicy()
	var fallbackName string
	if policy != nil {
		if entry, ok := policy.DisabledBehavior[yamlKey]; ok {
			if userBlocked {
				fallbackName = entry.WhenUserBlocks
			} else {
				fallbackName = entry.WhenDisabled
			}
		}
	}
	if fallbackName == "" {
		if userBlocked {
			fallbackName = "direct_answer"
		} else {
			fallbackName = "mode_disabled"
		}
	}
	mode, ok := lookupRoutingMode(fallbackName)
	if !ok {
		// Unknown fallback target — degrade safely.
		if userBlocked {
			return types.RouteDirectAnswer, reason + "; unknown_fallback=" + fallbackName
		}
		return types.RouteModeDisabled, reason + "; unknown_fallback=" + fallbackName
	}
	return mode, reason + "; fallback=" + fallbackName
}

// modeKeyString returns the YAML key for a RoutingMode, used for
// DisabledBehavior lookups. Routing mode names already use snake_case
// matching the YAML keys, so this is identity in practice — but we keep
// the indirection so future naming changes stay localised.
func modeKeyString(m types.RoutingMode) string {
	return string(m)
}

// lookupRoutingMode maps a YAML string back to a typed RoutingMode.
func lookupRoutingMode(name string) (types.RoutingMode, bool) {
	switch name {
	case string(types.RouteDirectAnswer):
		return types.RouteDirectAnswer, true
	case string(types.RouteRAGAnswer):
		return types.RouteRAGAnswer, true
	case string(types.RouteReActTool):
		return types.RouteReActTool, true
	case string(types.RouteSandboxExecution):
		return types.RouteSandboxExecution, true
	case string(types.RouteDAGWorkflow):
		return types.RouteDAGWorkflow, true
	case string(types.RouteSwarmWorkflow):
		return types.RouteSwarmWorkflow, true
	case string(types.RouteReflection):
		return types.RouteReflection, true
	case string(types.RouteTreeOfThoughts):
		return types.RouteTreeOfThoughts, true
	case string(types.RouteDebate):
		return types.RouteDebate, true
	case string(types.RouteResearchV1):
		return types.RouteResearchV1, true
	case string(types.RouteResearchV2):
		return types.RouteResearchV2, true
	case "mode_disabled":
		return types.RouteModeDisabled, true
	}
	return "", false
}

func workflowTypeForMode(mode types.RoutingMode) string {
	switch mode {
	case types.RouteDirectAnswer:
		return "SimpleWorkflow"
	case types.RouteRAGAnswer:
		return "RAGQueryWorkflow"
	case types.RouteReActTool:
		return "DAGWorkflow" // ReAct runs inside DAG node
	case types.RouteSandboxExecution:
		return "SandboxWorkflow"
	case types.RouteDAGWorkflow:
		return "DAGWorkflow"
	case types.RouteSwarmWorkflow:
		return "SwarmWorkflow"
	case types.RouteReflection:
		return "ReflectionWorkflow"
	case types.RouteTreeOfThoughts:
		return "TreeOfThoughtsWorkflow"
	case types.RouteDebate:
		return "DebateWorkflow"
	case types.RouteResearchV1:
		return "ResearchSynthesisWorkflow"
	case types.RouteResearchV2:
		return "ResearchSynthesisV2Workflow"
	case types.RouteModeDisabled, types.RouteNotImplemented:
		return "SimpleWorkflow" // safe fallback
	default:
		return "SimpleWorkflow"
	}
}

func estimateTokenBudget(mode types.RoutingMode, complexity float64) int {
	base := 2000
	switch mode {
	case types.RouteDirectAnswer:
		base = 500
	case types.RouteRAGAnswer:
		base = 1500
	case types.RouteReActTool:
		base = 3000
	case types.RouteSandboxExecution:
		base = 2000
	case types.RouteDAGWorkflow:
		base = 4000
	case types.RouteSwarmWorkflow:
		base = 6000
	case types.RouteReflection, types.RouteDebate, types.RouteTreeOfThoughts:
		base = 5000
	case types.RouteResearchV1, types.RouteResearchV2:
		base = 8000
	}
	return int(float64(base) * (1.0 + complexity))
}

func hasAddon(addons []types.RouteAddon, target types.RouteAddon) bool {
	for _, a := range addons {
		if a == target {
			return true
		}
	}
	return false
}

// ─── EstimateRouteCost ─────────────────────────────────────────────────

type EstimateRouteCostInput struct {
	Mode            types.RoutingMode `json:"mode"`
	ModelTier       string            `json:"model_tier"`
	ComplexityScore float64           `json:"complexity_score"`
}

type EstimateRouteCostResult struct {
	EstimatedTokens int     `json:"estimated_tokens"`
	EstimatedUSD    float64 `json:"estimated_usd"`
	TokenBudget     int     `json:"token_budget"`
	CostBudgetUSD   float64 `json:"cost_budget_usd"`
}

func (ra *RouterActivities) EstimateRouteCost(ctx context.Context, input EstimateRouteCostInput) (*EstimateRouteCostResult, error) {
	tokenBudget := estimateTokenBudget(input.Mode, input.ComplexityScore)
	costPer1K := 0.002 // $0.002/1K tokens (small model estimate)
	if input.ModelTier == "medium" {
		costPer1K = 0.01
	} else if input.ModelTier == "large" {
		costPer1K = 0.03
	}
	estimatedUSD := float64(tokenBudget) / 1000.0 * costPer1K
	return &EstimateRouteCostResult{
		EstimatedTokens: tokenBudget,
		EstimatedUSD:    estimatedUSD,
		TokenBudget:     tokenBudget,
		CostBudgetUSD:   estimatedUSD,
	}, nil
}

// ─── AuditRoutingDecision ──────────────────────────────────────────────

type AuditRoutingDecisionInput struct {
	SessionID   string                `json:"session_id"`
	WorkflowID  string                `json:"workflow_id"`
	RunID       string                `json:"run_id"`
	Decision    types.RoutingDecision `json:"decision"`
	PolicyTrace string                `json:"policy_trace"`
	// Phase 7I v2 — persisted into the migration 016 JSONB columns.
	// Both fields are optional (nil for legacy callers); driver.Valuer
	// returns NULL for nil so existing audit rows remain valid.
	Signals     *types.RouterDecisionSignals     `json:"signals,omitempty"`
	Explanation *types.RouterDecisionExplanation `json:"explanation,omitempty"`
}

type AuditRoutingDecisionResult struct {
	AuditID string `json:"audit_id"`
}

func (ra *RouterActivities) AuditRoutingDecision(ctx context.Context, input AuditRoutingDecisionInput) (*AuditRoutingDecisionResult, error) {
	if ra.db == nil {
		return &AuditRoutingDecisionResult{AuditID: "noop"}, nil
	}

	auditID := input.WorkflowID + ":routing:" + input.RunID[len(input.RunID)-8:]
	// Build score_breakdown JSONB from Explanation.Candidates (we don't
	// store the full map; just the score per mode from the candidate
	// list, which is what frontend / regression tooling consumes).
	var scoreBreakdownJSON []byte
	if input.Explanation != nil {
		if m, err := json.Marshal(input.Explanation.ScoreBreakdown); err == nil {
			scoreBreakdownJSON = m
		}
	}
	addonJSON := jsonAddonCapabilities(input.Explanation)
	if len(addonJSON) == 0 {
		addonJSON = nil
	}
	workspaceJSON := jsonWorkspaceArtifacts(input.Explanation)
	if len(workspaceJSON) == 0 {
		workspaceJSON = nil
	}
	_, err := ra.db.ExecContext(ctx, `
		INSERT INTO routing_audit_logs
			(session_id, workflow_id, run_id, planned_mode, mode, fallback_reason,
			 complexity_score, risk_level, requires_approval, requires_rag, requires_tools,
			 requires_sandbox, requires_reflection, requires_debate, requires_tot,
			 requires_research_v2, model_tier, token_budget, cost_budget_usd,
			 confidence, classifier_mode, short_reason, policy_trace_ref,
			 signals_json, explanation_json, policy_version, score_breakdown_json,
			 addon_capabilities, workspace_artifacts)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29)`,
		input.SessionID, input.WorkflowID, input.RunID,
		string(input.Decision.PlannedMode), string(input.Decision.Mode), input.Decision.FallbackReason,
		input.Decision.ComplexityScore, input.Decision.RiskLevel, input.Decision.RequiresApproval,
		input.Decision.RequiresRAG, input.Decision.RequiresTools,
		input.Decision.RequiresSandbox, input.Decision.RequiresReflection,
		input.Decision.RequiresDebate, input.Decision.RequiresToT,
		input.Decision.RequiresResearchV2, input.Decision.ModelTier,
		input.Decision.TokenBudget, input.Decision.CostBudgetUSD,
		input.Decision.Confidence, "heuristic", input.PolicyTrace,
		input.Decision.PolicyTraceRef,
		input.Signals, input.Explanation,
		nullablePolicyVersion(input.Explanation), scoreBreakdownJSON,
		addonJSON, workspaceJSON,
	)
	if err != nil {
		return nil, fmt.Errorf("audit routing decision: %w", err)
	}
	return &AuditRoutingDecisionResult{AuditID: auditID}, nil
}

// nullablePolicyVersion returns the policy version from the explanation
// or an empty string (so the column is never NULL when an explanation
// was provided).
func nullablePolicyVersion(e *types.RouterDecisionExplanation) interface{} {
	if e == nil {
		return nil
	}
	return e.PolicyVersion
}

// jsonAddonCapabilities marshals the addon capability list (or nil if no
// explanation). Returned as []byte so pgx encodes it as JSONB. Marshal
// errors on these typed structs are not recoverable; we drop the field
// rather than fail the audit write.
func jsonAddonCapabilities(e *types.RouterDecisionExplanation) []byte {
	if e == nil {
		return nil
	}
	b, err := json.Marshal(e.AddonCapabilities)
	if err != nil {
		return nil
	}
	return b
}

// jsonWorkspaceArtifacts marshals the predicted workspace artifacts (or
// nil if no explanation).
func jsonWorkspaceArtifacts(e *types.RouterDecisionExplanation) []byte {
	if e == nil {
		return nil
	}
	b, err := json.Marshal(e.WorkspaceArtifactsExpected)
	if err != nil {
		return nil
	}
	return b
}

// ─── EmitRoutingEvent ──────────────────────────────────────────────────

type EmitRoutingEventInput struct {
	WorkflowID string                 `json:"workflow_id"`
	EventType  string                 `json:"event_type"`
	Decision   types.RoutingDecision  `json:"decision"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
}

// EmitRoutingEvent is a no-op in Slice 23 (SSE infrastructure not wired yet for router events).
func (ra *RouterActivities) EmitRoutingEvent(ctx context.Context, input EmitRoutingEventInput) error {
	// TODO: wire to Redis event stream when SSE routing is needed
	return nil
}

// ─── WriteRoutingPolicyTrace ────────────────────────────────────────────

type WriteRoutingPolicyTraceInput struct {
	WorkflowID        string                `json:"workflow_id"`
	Decision          types.RoutingDecision `json:"decision"`
	ComplexitySummary string                `json:"complexity_summary"`
	CapabilitySummary string                `json:"capability_summary"`
}

type WriteRoutingPolicyTraceResult struct {
	PolicyTraceRef string `json:"policy_trace_ref"`
}

// WriteRoutingPolicyTrace is a placeholder — full Workspace integration is Phase 5 scope.
func (ra *RouterActivities) WriteRoutingPolicyTrace(ctx context.Context, input WriteRoutingPolicyTraceInput) (*WriteRoutingPolicyTraceResult, error) {
	ref := fmt.Sprintf("router:%s:policy_trace", input.WorkflowID)
	return &WriteRoutingPolicyTraceResult{PolicyTraceRef: ref}, nil
}

// ─── PersistRoutedExecutionResult (Phase 7E.5 async result API) ────────

// PersistRoutedExecutionResultInput is the structured final result for a
// routed workflow. The AdvancedRoutingWorkflow calls this Activity at
// the very end of execution (success or failure) so that async
// execute-routed clients can poll GET /api/v1/tasks/{workflow_id}/result
// and read the final RoutedExecutionResult out of the tasks table.
type PersistRoutedExecutionResultInput struct {
	WorkflowID  string                 `json:"workflow_id"`
	TaskID      string                 `json:"task_id"`
	SessionID   string                 `json:"session_id"`
	RunID       string                 `json:"run_id"`
	Status      string                 `json:"status"`       // "completed" | "failed"
	Result      *types.RoutedExecutionResult `json:"result"`  // nil for hard failure
	ErrorType   string                 `json:"error_type,omitempty"`
	ErrorMsg    string                 `json:"error_msg,omitempty"`
}

type PersistRoutedExecutionResultResult struct {
	Persisted bool   `json:"persisted"`
	TaskID    string `json:"task_id"`
}

// PersistRoutedExecutionResult writes the final RoutedExecutionResult into
// the tasks row keyed by workflow_id. It is the bridge between
// Temporal workflow completion and the async execute-routed polling API.
//
// Failure mode: if the DB write fails, the Activity returns the error to
// the Workflow, which surfaces a structured warning in
// RoutedExecutionResult.Metadata["persist_error"] so the polling API can
// still answer "failed_with_persist_error" rather than hang at "running".
func (ra *RouterActivities) PersistRoutedExecutionResult(ctx context.Context, input PersistRoutedExecutionResultInput) (*PersistRoutedExecutionResultResult, error) {
	if ra.db == nil {
		return nil, fmt.Errorf("router activities: db is nil")
	}
	if input.WorkflowID == "" {
		return nil, fmt.Errorf("workflow_id is required")
	}

	taskStatus := "completed"
	if input.Status == "failed" {
		taskStatus = "failed"
	}

	resultText := ""
	tokens := 0
	promptTokens := 0
	completionTokens := 0
	model := ""
	metadataBytes := []byte("{}")
	if input.Result != nil {
		resultText = input.Result.FinalAnswerText
		tokens = input.Result.TokensUsed
		// RoutedExecutionResult.CostUSD is float64; we keep it
		// inside metadata.
		// If the child workflow reported its own token counts, prefer those.
		// (Currently the router result does not propagate the per-step
		// counters; we still record what the RoutedExecutionResult
		// knows about.)
		model = input.Result.ModelUsed

		// Build metadata JSON: provider / model_used / mock / llm_calls
		// / total_tokens / mode / fallback_used / status + mode-specific
		// nested fields. Long transcripts are NOT included; the refs are.
		md := map[string]interface{}{
			"provider":        input.Result.Provider,
			"model_used":      input.Result.ModelUsed,
			"mock":            input.Result.Mock,
			"llm_calls":       derefIntFromMap(input.Result.Metadata, "llm_calls"),
			"total_tokens":    input.Result.TokensUsed,
			"cost_usd":        input.Result.CostUSD,
			"mode":            input.Result.Mode,
			"fallback_used":   input.Result.FallbackUsed,
			"workflow_status": input.Result.Status,
			"final_answer_ref": input.Result.FinalAnswerRef,
		}
		// Flatten Result.Metadata sub-keys (debate_*, tot_*, reflection_*).
		if input.Result.Metadata != nil {
			for k, v := range input.Result.Metadata {
				md[k] = v
			}
		}
		// Phase 7J: Emit typed ToT fields for smoke persistence.
		if input.Result.TotalThoughts > 0 {
			md["total_thoughts"] = input.Result.TotalThoughts
		}
		if input.Result.TreeDepth > 0 {
			md["tree_depth"] = input.Result.TreeDepth
		}
		if input.Result.BestPathCount > 0 {
			md["best_path_count"] = input.Result.BestPathCount
		}
		if input.Result.SolutionRef != "" {
			md["solution_ref"] = input.Result.SolutionRef
		}
		if input.Result.ExplorationRef != "" {
			md["exploration_tree_ref"] = input.Result.ExplorationRef
		}
		if input.Result.ToTConfidence > 0 {
			md["confidence"] = input.Result.ToTConfidence
		}
		if b, err := json.Marshal(md); err == nil {
			metadataBytes = b
		}
	}

	upd := db.TaskResultUpdate{
		WorkflowID:            input.WorkflowID,
		Result:                resultText,
		ResultStatus:          input.Status,
		Status:                taskStatus,
		UsagePromptTokens:     promptTokens,
		UsageCompletionTokens: completionTokens,
		UsageTotalTokens:      tokens,
		Model:                 model,
		Metadata:              metadataBytes,
		ErrorType:             input.ErrorType,
		ErrorMsg:              input.ErrorMsg,
	}

	if err := db.UpdateTaskResultStandalone(ctx, ra.db, upd); err != nil {
		return nil, fmt.Errorf("persist routed result: %w", err)
	}
	return &PersistRoutedExecutionResultResult{
		Persisted: true,
		TaskID:    input.TaskID,
	}, nil
}

// derefIntFromMap returns the int value at key k in m if it is one, else 0.
// Used to safely extract RoutedExecutionResult.Metadata counters.
func derefIntFromMap(m map[string]interface{}, k string) int {
	if m == nil {
		return 0
	}
	v, ok := m[k]
	if !ok {
		return 0
	}
	switch x := v.(type) {
	case int:
		return x
	case int32:
		return int(x)
	case int64:
		return int(x)
	case float64:
		return int(x)
	}
	return 0
}

// ─── UpdateTaskApprovalStatus (Phase 7F Approval UX) ──────────────────

type UpdateTaskApprovalStatusInput struct {
	WorkflowID  string `json:"workflow_id"`
	Status      string `json:"status"`
	ApprovalID  string `json:"approval_id,omitempty"`
	ApprovalURL string `json:"approval_url,omitempty"`
	RiskLevel   string `json:"risk_level,omitempty"`
	Mode        string `json:"mode,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

type UpdateTaskApprovalStatusResult struct{ Updated bool `json:"updated"` }

func (ra *RouterActivities) UpdateTaskApprovalStatus(ctx context.Context, input UpdateTaskApprovalStatusInput) (*UpdateTaskApprovalStatusResult, error) {
	if ra.db == nil { return nil, fmt.Errorf("db is nil") }
	md, _ := json.Marshal(map[string]interface{}{
		"approval_id": input.ApprovalID, "approval_url": input.ApprovalURL,
		"approval_risk": input.RiskLevel, "approval_mode": input.Mode,
		"approval_reason": input.Reason, "approval_status": input.Status,
	})
	_ = db.UpdateTaskResultStandalone(ctx, ra.db, db.TaskResultUpdate{
		WorkflowID: input.WorkflowID, Status: input.Status, Metadata: md,
	})
	return &UpdateTaskApprovalStatusResult{Updated: true}, nil
}

// ─── EnsureRouterTable ─────────────────────────────────────────────────

// EnsureRouterTable is idempotent. It creates the base 010 schema and
// adds the 016 v2 columns (JSONB) via ALTER TABLE IF NOT EXISTS so that
// fresh databases get the full schema and existing databases (which have
// already had 016 applied) remain untouched.
func EnsureRouterTable(db *sql.DB) error {
	if db == nil {
		return nil
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS routing_audit_logs (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			session_id VARCHAR(100) NOT NULL,
			workflow_id VARCHAR(100) NOT NULL,
			run_id VARCHAR(100),
			planned_mode VARCHAR(50) NOT NULL,
			mode VARCHAR(50) NOT NULL,
			fallback_reason VARCHAR(500),
			complexity_score FLOAT,
			risk_level VARCHAR(50),
			requires_approval BOOLEAN DEFAULT FALSE,
			requires_rag BOOLEAN DEFAULT FALSE,
			requires_tools BOOLEAN DEFAULT FALSE,
			requires_sandbox BOOLEAN DEFAULT FALSE,
			requires_reflection BOOLEAN DEFAULT FALSE,
			requires_debate BOOLEAN DEFAULT FALSE,
			requires_tot BOOLEAN DEFAULT FALSE,
			requires_research_v2 BOOLEAN DEFAULT FALSE,
			model_tier VARCHAR(50),
			token_budget INTEGER,
			cost_budget_usd FLOAT,
			confidence FLOAT,
			classifier_mode VARCHAR(50),
			short_reason VARCHAR(500),
			policy_trace_ref VARCHAR(200),
			classification_tokens INTEGER,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		)
	`); err != nil {
		return err
	}
	// 016 v2 columns — idempotent. Same DDL as migrations/016_routing_signals.sql.
	_, err := db.Exec(`
		ALTER TABLE routing_audit_logs
			ADD COLUMN IF NOT EXISTS signals_json JSONB,
			ADD COLUMN IF NOT EXISTS explanation_json JSONB,
			ADD COLUMN IF NOT EXISTS policy_version VARCHAR(20),
			ADD COLUMN IF NOT EXISTS score_breakdown_json JSONB,
			ADD COLUMN IF NOT EXISTS addon_capabilities JSONB,
			ADD COLUMN IF NOT EXISTS workspace_artifacts JSONB
	`)
	return err
}

// ─── EvaluateApprovalPolicy (placeholder for Slice 23) ─────────────────

type EvaluateApprovalPolicyInput struct {
	ComplexityScore float64  `json:"complexity_score"`
	TokenBudgetUSD  float64  `json:"token_budget_usd"`
	Tools           []string `json:"tools"`
	RiskLevel       string   `json:"risk_level"`
	Mode            string   `json:"mode"`
	RequiresSandbox bool     `json:"requires_sandbox"`
	RequiresPublish bool     `json:"requires_publish"`
	// RequireApproval is a test-only kill switch: when false, the
	// approval gate is disabled for this routed execution. Set from
	// RouterConfigSnapshot.RequireApproval (env ROUTER_REQUIRE_APPROVAL).
	RequireApproval bool `json:"require_approval"`
}

type EvaluateApprovalPolicyResult struct {
	Required  bool   `json:"required"`
	Reason    string `json:"reason"`
	RiskLevel string `json:"risk_level"`
}

func (ra *RouterActivities) EvaluateApprovalPolicy(ctx context.Context, input EvaluateApprovalPolicyInput) (*EvaluateApprovalPolicyResult, error) {
	// Test-only kill switch: ROUTER_REQUIRE_APPROVAL=false disables the
	// approval gate. Production default is true.
	if !input.RequireApproval {
		return &EvaluateApprovalPolicyResult{
			Required: false, Reason: "require_approval=false (test/smoke override)",
			RiskLevel: input.RiskLevel,
		}, nil
	}
	required := false
	reason := ""

	if input.RiskLevel == "critical" {
		required = true
		reason = "risk_level=critical"
	} else if input.RiskLevel == "high" && input.RequiresSandbox {
		required = true
		reason = "high_risk+sandbox"
	} else if input.RequiresSandbox {
		required = true
		reason = "sandbox_execution_requires_approval"
	} else if input.ComplexityScore >= 0.70 {
		required = true
		reason = "high_complexity"
	} else if input.TokenBudgetUSD > 0.50 {
		required = true
		reason = "high_token_budget"
	}

	return &EvaluateApprovalPolicyResult{
		Required:  required,
		Reason:    reason,
		RiskLevel: input.RiskLevel,
	}, nil
}
