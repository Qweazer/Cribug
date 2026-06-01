package activities

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"cribug/internal/types"
)

// RouterActivities holds dependencies needed by Router Activities.
type RouterActivities struct {
	db *sql.DB
}

// NewRouterActivities creates a new RouterActivities instance.
func NewRouterActivities(db *sql.DB) *RouterActivities {
	return &RouterActivities{db: db}
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
	return &ClassifyTaskComplexityResult{
		ComplexityScore: score,
		RiskLevel:       risk,
		Summary:         fmt.Sprintf("complexity=%.2f risk=%s", score, risk),
		TokensUsed:      0, // heuristic uses 0 tokens
	}, nil
}

func classifyHeuristic(query, intent string) float64 {
	lower := strings.ToLower(query)
	score := 0.0

	// Short queries are simple
	if len(query) < 50 {
		score += 0.1
	}

	// Keywords indicating higher complexity
	complexKeywords := []string{"分析", "对比", "方案", "比较", "规划", "设计", "架构", "优化"}
	for _, kw := range complexKeywords {
		if strings.Contains(lower, kw) {
			score += 0.15
			break
		}
	}

	// Research / reporting keywords
	researchKeywords := []string{"研究", "报告", "引用", "多源", "证据", "调研"}
	for _, kw := range researchKeywords {
		if strings.Contains(lower, kw) {
			score += 0.20
			break
		}
	}

	// Sandbox / execution keywords
	execKeywords := []string{"执行", "运行", "沙箱", "代码", "编译", "测试"}
	for _, kw := range execKeywords {
		if strings.Contains(lower, kw) {
			score += 0.20
			break
		}
	}

	// Multi-agent / swarm keywords
	swarmKeywords := []string{"多agent", "多角色", "团队", "分工", "协作", "swarm"}
	for _, kw := range swarmKeywords {
		if strings.Contains(lower, kw) {
			score += 0.20
			break
		}
	}

	// Multi-step detection: multiple sentences or numbered lists
	if strings.Count(query, "。") >= 2 || strings.Count(query, ".") >= 3 {
		score += 0.10
	}
	if strings.Count(query, "\n") >= 2 {
		score += 0.05
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

	// RAG: project doc / knowledge base keywords
	ragKeywords := []string{"项目文档", "代码库", "知识库", "我的项目", "doc", "readme", "api文档"}
	for _, kw := range ragKeywords {
		if strings.Contains(lower, kw) {
			result.RequiresRAG = true
			break
		}
	}

	// Tools
	toolKeywords := []string{"搜索", "查询", "计算", "calculate", "search", "curl", "api调用"}
	for _, kw := range toolKeywords {
		if strings.Contains(lower, kw) {
			result.RequiresTools = true
			break
		}
	}
	if input.AllowTools && len(input.AvailableTools) > 0 {
		result.RequiresTools = true
		result.DetectedTools = input.AvailableTools
	}

	// Sandbox
	sandboxKeywords := []string{"运行代码", "执行脚本", "沙箱", "sandbox", "编译", "wasi"}
	for _, kw := range sandboxKeywords {
		if strings.Contains(lower, kw) {
			result.RequiresSandbox = true
			break
		}
	}

	// Research
	researchKeywords := []string{"研究", "报告", "调研", "多来源", "证据", "引用", "citation"}
	for _, kw := range researchKeywords {
		if strings.Contains(lower, kw) {
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
	RequiresTools    bool                       `json:"requires_tools"`
	RequiresSandbox  bool                       `json:"requires_sandbox"`
	RequiresRAG      bool                       `json:"requires_rag"`
	RequiresResearch bool                       `json:"requires_research"`
	RequireCitations bool                       `json:"require_citations"`
	BudgetUSD        float64                    `json:"budget_usd"`
	RouterConfig     types.RouterConfigSnapshot `json:"router_config"`
}

type EvaluateRoutingPolicyResult struct {
	Decision types.RoutingDecision `json:"decision"`
}

func (ra *RouterActivities) EvaluateRoutingPolicy(ctx context.Context, input EvaluateRoutingPolicyInput) (*EvaluateRoutingPolicyResult, error) {
	cfg := input.RouterConfig

	// Determine planned mode by complexity + capability
	planned := selectPlannedMode(input)

	// Determine addons
	addons := selectAddons(input, planned)

	// Determine executed mode (feature-flag gating)
	executed, fallbackReason := resolveExecutedMode(planned, cfg)

	// Determine workflow type string
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

	// Approval
	requiresApproval := input.ComplexityScore >= 0.60 || input.RiskLevel == "high" || input.RiskLevel == "critical" || input.RequiresSandbox

	decision := types.RoutingDecision{
		PlannedMode:        planned,
		Mode:               executed,
		FallbackReason:     fallbackReason,
		AddonCapabilities:  addons,
		WorkflowType:       wfType,
		Reason:             fmt.Sprintf("complexity=%.2f risk=%s planned=%s executed=%s", input.ComplexityScore, input.RiskLevel, planned, executed),
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
	}

	return &EvaluateRoutingPolicyResult{Decision: decision}, nil
}

func selectPlannedMode(input EvaluateRoutingPolicyInput) types.RoutingMode {
	c := input.ComplexityScore

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

	// Swarm (very high complexity, multi-agent keywords detected)
	if c >= 0.65 {
		return types.RouteSwarmWorkflow
	}

	// DAG (medium-high complexity, decomposable)
	if c >= 0.45 {
		return types.RouteDAGWorkflow
	}

	// ReAct (tools needed but not too complex)
	if input.RequiresTools && c >= 0.20 {
		return types.RouteReActTool
	}

	// RAG (local knowledge needed)
	if input.RequiresRAG {
		return types.RouteRAGAnswer
	}

	// Debate / comparison keywords
	lower := "" // TODO: pass query through; for now use complexity as proxy
	_ = lower

	// Direct answer (default)
	return types.RouteDirectAnswer
}

func selectAddons(input EvaluateRoutingPolicyInput, planned types.RoutingMode) []types.RouteAddon {
	var addons []types.RouteAddon

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
func resolveExecutedMode(planned types.RoutingMode, cfg types.RouterConfigSnapshot) (types.RoutingMode, string) {
	switch planned {
	case types.RouteReflection:
		if !cfg.EnableReflection {
			return types.RouteModeDisabled, "enable_reflection=false"
		}
	case types.RouteTreeOfThoughts:
		if !cfg.EnableToT {
			return types.RouteModeDisabled, "enable_tot=false"
		}
	case types.RouteDebate:
		if !cfg.EnableDebate {
			return types.RouteModeDisabled, "enable_debate=false"
		}
	case types.RouteResearchV2:
		if !cfg.EnableResearchV2 {
			return types.RouteResearchV1, "research_v2_not_enabled_fallback_to_research_v1"
		}
	}
	return planned, ""
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
		return "ReflectionProductionWorkflow"
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
}

type AuditRoutingDecisionResult struct {
	AuditID string `json:"audit_id"`
}

func (ra *RouterActivities) AuditRoutingDecision(ctx context.Context, input AuditRoutingDecisionInput) (*AuditRoutingDecisionResult, error) {
	if ra.db == nil {
		return &AuditRoutingDecisionResult{AuditID: "noop"}, nil
	}

	auditID := input.WorkflowID + ":routing:" + input.RunID[len(input.RunID)-8:]
	_, err := ra.db.ExecContext(ctx, `
		INSERT INTO routing_audit_logs
			(session_id, workflow_id, run_id, planned_mode, mode, fallback_reason,
			 complexity_score, risk_level, requires_approval, requires_rag, requires_tools,
			 requires_sandbox, requires_reflection, requires_debate, requires_tot,
			 requires_research_v2, model_tier, token_budget, cost_budget_usd,
			 confidence, classifier_mode, short_reason, policy_trace_ref)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23)`,
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
	)
	if err != nil {
		return nil, fmt.Errorf("audit routing decision: %w", err)
	}
	return &AuditRoutingDecisionResult{AuditID: auditID}, nil
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

// ─── EnsureRouterTable ─────────────────────────────────────────────────

func EnsureRouterTable(db *sql.DB) error {
	if db == nil {
		return nil
	}
	_, err := db.Exec(`
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
}

type EvaluateApprovalPolicyResult struct {
	Required  bool   `json:"required"`
	Reason    string `json:"reason"`
	RiskLevel string `json:"risk_level"`
}

func (ra *RouterActivities) EvaluateApprovalPolicy(ctx context.Context, input EvaluateApprovalPolicyInput) (*EvaluateApprovalPolicyResult, error) {
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
