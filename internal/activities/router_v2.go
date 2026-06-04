package activities

import (
	"context"
	"fmt"
	"math"
	"strings"

	"cribug/internal/config"
	"cribug/internal/types"
)

// ─── Policy resolution (loaded once per EvaluateRoutingPolicyActivity call) ────

var defaultPolicyOnce *config.RouterPolicyV2

func getPolicy() *config.RouterPolicyV2 {
	if defaultPolicyOnce == nil {
		p, err := config.LoadRouterPolicyV2("config/router_policy.yaml")
		if err != nil || p == nil {
			p = config.DefaultRouterPolicyV2()
		}
		defaultPolicyOnce = p
	}
	return defaultPolicyOnce
}

// ─── BuildDecisionSignals (Step 3) ─────────────────────────────────────

// BuildDecisionSignals computes the multi-signal vector for a query.
// It does NOT call LLM or DB; it is a pure function.
func BuildDecisionSignals(input EvaluateRoutingPolicyInput) types.RouterDecisionSignals {
	q := input.Query
	lower := strings.ToLower(q)
	policy := getPolicy()

	s := types.RouterDecisionSignals{
		QueryLength:      len(q),
		QueryCharCount:   len(strings.ReplaceAll(q, " ", "")),
		HasNumberedList:  strings.Contains(q, "1. ") || strings.Contains(q, "1) "),
		HasMultiSentence: strings.Count(q, ".")+strings.Count(q, "。") >= 2,
		UserIntent:       input.UserIntent,

		RequiresTools:      input.RequiresTools,
		RequiresRAG:        input.RequiresRAG,
		RequiresResearch:   input.RequiresResearch,
		RequiresSandbox:    input.RequiresSandbox,
		RequiresCitations:  input.RequireCitations,
		RequiresWebSearch:  false, // future capability

		AllowTools:        input.AllowTools,
		AllowSandbox:      input.AllowSandbox,
		AllowResearch:     input.AllowResearch,
		AllowWebSearch:    false,
		BudgetUSD:         input.BudgetUSD,
		MaxLatencyMs:      30000, // default; future: from RouteRequest

		RiskLevel:           input.RiskLevel,
		WorkspaceArtifactNeed: true,
		AuditNeed:           true,
		PolicyVersion:       policy.Version,
		ClassifierUsed:      false, // default off
	}

	// Complexity sub-signals from keyword matching.
	// Each category has an independent score.
	s.ComplexityAnalyze = keywordSubScore(lower, policy.Keywords.Reflection.Keywords) +
		keywordSubScore(lower, policy.Keywords.Dag.Keywords)
	s.ComplexityResearch = keywordSubScore(lower, policy.Keywords.ResearchV2.Keywords)
	s.ComplexityExecution = keywordSubScore(lower, policy.Keywords.SandboxExec.Keywords)
	s.ComplexityMultiAgent = keywordSubScore(lower, policy.Keywords.Swarm.Keywords)
	s.ComplexityDebate = keywordSubScore(lower, policy.Keywords.Debate.Keywords)
	s.ComplexityExploration = keywordSubScore(lower, policy.Keywords.TreeOfThoughts.Keywords)

	// Overall: max of all sub-signals.
	s.ComplexityOverall = maxF(s.ComplexityAnalyze, s.ComplexityResearch,
		s.ComplexityExecution, s.ComplexityMultiAgent,
		s.ComplexityDebate, s.ComplexityExploration)

	// Normalize to [0,1].
	for _, v := range []*float64{&s.ComplexityAnalyze, &s.ComplexityResearch,
		&s.ComplexityExecution, &s.ComplexityMultiAgent,
		&s.ComplexityDebate, &s.ComplexityExploration, &s.ComplexityOverall} {
		if *v > 1 {
			*v = 1
		}
	}

	// Capability sub-signals (0-1).
	if s.RequiresRAG || keywordSubScore(lower, []string{"knowledge", "document", "reference", "project docs"}) > 0 {
		s.RagNeedScore = 0.6 + 0.3*keywordSubScore(lower, policy.Keywords.ResearchV2.Keywords)
		if s.RagNeedScore > 1 {
			s.RagNeedScore = 1
		}
	}
	if s.RequiresSandbox || keywordSubScore(lower, policy.Keywords.SandboxExec.Keywords) > 0 {
		s.SandboxNeedScore = 0.7 + 0.3*s.ComplexityExecution
		if s.SandboxNeedScore > 1 {
			s.SandboxNeedScore = 1
		}
	}
	s.EvidenceNeedScore = s.ComplexityResearch + boolToF(s.RequiresCitations)*0.3
	if s.EvidenceNeedScore > 1 {
		s.EvidenceNeedScore = 1
	}
	s.ToolNeedScore = s.ComplexityAnalyze + boolToF(s.RequiresTools)*0.4
	if s.ToolNeedScore > 1 {
		s.ToolNeedScore = 1
	}
	s.SkillNeedScore = keywordSubScore(lower, policy.Keywords.Skills.Keywords)
	s.ToolExecutionNeedScore = s.ToolNeedScore
	s.WebSearchNeedScore = 0 // reserved

	if s.RiskLevel == "high" || s.RiskLevel == "critical" {
		s.ApprovalNeedScore = 0.7 + 0.3*s.ComplexityOverall
		if s.ApprovalNeedScore > 1 {
			s.ApprovalNeedScore = 1
		}
	}

	// ComplexitySemantic: a "semantic complexity" signal that captures
	// Chinese complex intent words (分析/比较/正反观点/多方案/多路径/
	// 推演/验收/上线/多角色/协作/研究报告/引用/工具调用/沙箱/不可信
	// /未知/RAG/知识库/前端联调/审批/安全风险). This is the Phase 7I
	// Fix-1 path that lifts v2 router out of the "always direct_answer"
	// trap on short Chinese queries. The signal is the union of all
	// 23 categories — if any is present, ComplexitySemantic > 0.
	semanticComplexKeywords := []string{
		"分析", "评估", "审视", "解析", "复盘", "拆解", "拆", "深入",
		"比较", "对比", "vs", "versus", "对比分析",
		"正反", "正方", "反方", "双方", "观点", "辩", "辩论", "pro", "con",
		"方案", "多种", "多条", "多套", "几套", "几个", "多思路", "多路线",
		"路径", "分支", "搜索", "推演", "推导", "推",
		"验收", "review", "检查", "核对", "verify", "verification", "审计",
		"上线", "deploy", "发布", "ship", "生产", "staging", "联调", "对接",
		"多角色", "多 agent", "多智能体", "团队", "分工", "协作", "合作", "联合",
		"研究报告", "research", "调研", "深入研究", "分析研究",
		"引用", "citation", "引用来源", "with citations", "依据", "evidence",
		"工具调用", "调用", "计算", "搜索工具", "tool", "tool call", "工具",
		"沙箱", "sandbox", "wasi",
		"不可信", "untrusted", "unknown script", "未知脚本", "未知来源", "未验证",
		"执行脚本", "运行代码", "执行", "execute", "run ", "代码",
		"读写文件", "临时文件", "临时目录", "/tmp", "read file", "write file",
		"RAG", "知识库", "向量库", "向量数据库", "项目文档", "本地知识",
		"前端联调", "前端", "ui", "ux", "联调", "接入", "对接前端",
		"审批", "approve", "approval", "人工审批", "需要审批",
		"安全风险", "安全", "risk", "compliance", "合规",
		"untrusted", "shell", "bash", "python code", "execute code",
	}
	s.ComplexitySemantic = keywordSubScore(lower, semanticComplexKeywords) * 1.2
	if s.ComplexitySemantic > 1 {
		s.ComplexitySemantic = 1
	}
	// Lift ComplexityOverall so the "complex but short query" case
	// doesn't get stuck at direct_answer's base 0.50.
	if s.ComplexitySemantic > 0.4 {
		s.ComplexityOverall = maxF(s.ComplexityOverall, 0.55+s.ComplexitySemantic*0.30)
	}

	// Budget / latency fit.
	estCost := estimateCostForMode("research_v2", policy)
	if s.BudgetUSD > 0 {
		s.BudgetFitScore = clamp01(1 - estCost/s.BudgetUSD)
	}
	if s.MaxLatencyMs > 0 {
		estLat := estimateLatencyForMode("research_v2", policy)
		s.LatencyFitScore = clamp01(1 - estLat/float64(s.MaxLatencyMs))
	}

	return s
}

func maxF(vs ...float64) float64 {
	m := vs[0]
	for _, v := range vs[1:] {
		if v > m {
			m = v
		}
	}
	return m
}

func boolToF(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func keywordSubScore(query string, keywords []string) float64 {
	lower := strings.ToLower(query)
	s := 0.0
	for _, kw := range keywords {
		if strings.Contains(lower, strings.ToLower(kw)) {
			s += 0.20
		}
	}
	if s > 1 {
		s = 1
	}
	return s
}

// ─── ScoreModes (Step 4) ──────────────────────────────────────────────

// ScoreModes scores every registered mode against signals using policy
// weights and returns a sorted list of ModeCandidate entries.
//
// After per-mode scoring, a "capability-driven boost" pass lifts the
// modes that directly satisfy the detected capability needs. Without
// this pass, the multi-signal v2 scorer can let direct_answer's base
// score (0.50) win over a capability-aligned mode whose signal weight
// is comparable. The boost is bounded so the base scoring still
// matters; we just stop capability-aligned modes from being out-scored
// by the trivial answer.
func ScoreModes(signals types.RouterDecisionSignals) []types.ModeCandidate {
	policy := getPolicy()
	modes := []types.RoutingMode{
		types.RouteDirectAnswer, types.RouteRAGAnswer, types.RouteReActTool,
		types.RouteDAGWorkflow, types.RouteReflection,
		types.RouteTreeOfThoughts, types.RouteDebate, types.RouteResearchV2,
		types.RouteSwarmWorkflow, types.RouteSandboxExecution,
	}
	candidates := make([]types.ModeCandidate, 0, len(modes))
	for _, mode := range modes {
		c := scoreOneMode(mode, signals, policy)
		candidates = append(candidates, c)
	}
	// Capability-driven boost: if a capability need is detected
	// (Requires* flag set or score > 0.5), lift the mode that owns
	// that capability. The boost is large enough to overcome a
	// direct_answer base (0.50) but not so large that it overrides
	// the heuristic's actual ranking.
	boostCap := func(mode types.RoutingMode, amount float64) {
		for i := range candidates {
			if candidates[i].Mode == mode && !candidates[i].Rejected {
				candidates[i].Score = clamp01(candidates[i].Score + amount)
			}
		}
	}
	if signals.RequiresRAG || signals.RagNeedScore > 0.5 {
		boostCap(types.RouteRAGAnswer, 0.45)
	}
	if signals.RequiresTools || signals.ToolNeedScore > 0.5 {
		// Both react_tool and dag_workflow satisfy the tools need.
		// Boost 0.45 is large enough to overcome direct_answer's base
		// 0.50 + complexity modifier.
		boostCap(types.RouteReActTool, 0.45)
		boostCap(types.RouteDAGWorkflow, 0.45)
	}
	if signals.RequiresResearch || signals.RequiresCitations {
		// Research dominates tools when both fire: research_v2 is a
		// superset of tool use (it can call tools internally).
		boostCap(types.RouteResearchV2, 0.70)
	}
	// Reflection: keywords like "analyze" / "improve" / "critique".
	// Threshold 0.4 (was 0.6) so short reflection-y queries still
	// pass through; without this, direct_answer's base 0.50 wins.
	if signals.ComplexityAnalyze >= 0.4 {
		boostCap(types.RouteReflection, 0.30)
	}
	if signals.RequiresSandbox || signals.SandboxNeedScore > 0.5 {
		// Sandbox is already hard-locked by capability_requirements;
		// nothing extra to do here.
		boostCap(types.RouteSandboxExecution, 0.0)
	}
	if signals.RequiresResearch || signals.RequiresCitations {
		boostCap(types.RouteResearchV2, 0.30)
	}
	// Reflection: keywords like "analyze" / "improve" / "critique".
	// Threshold 0.4 (was 0.6) so short reflection-y queries still
	// pass through; without this, direct_answer's base 0.50 wins.
	if signals.ComplexityAnalyze >= 0.4 {
		boostCap(types.RouteReflection, 0.30)
	}
	// Sort by score desc; on ties prefer research_v2 over
	// react_tool (research subsumes tool use).
	for i := 0; i < len(candidates); i++ {
		for j := i + 1; j < len(candidates); j++ {
			if candidates[j].Score > candidates[i].Score {
				candidates[i], candidates[j] = candidates[j], candidates[i]
				continue
			}
			if candidates[j].Score == candidates[i].Score &&
				candidates[j].Mode == types.RouteResearchV2 &&
				candidates[i].Mode != types.RouteResearchV2 {
				candidates[i], candidates[j] = candidates[j], candidates[i]
			}
		}
	}
	return candidates
}

func scoreOneMode(mode types.RoutingMode, s types.RouterDecisionSignals, p *config.RouterPolicyV2) types.ModeCandidate {
	modeKey := modeKey(mode)
	w := p.SignalWeights[modeKey]
	score := w.Base +
		w.Analyze*s.ComplexityAnalyze +
		w.Research*s.ComplexityResearch +
		w.Execution*s.ComplexityExecution +
		w.MultiAgent*s.ComplexityMultiAgent +
		w.Debate*s.ComplexityDebate +
		w.Exploration*s.ComplexityExploration +
		w.ToolNeed*s.ToolNeedScore +
		w.RagNeed*s.RagNeedScore +
		w.SandboxNeed*s.SandboxNeedScore +
		w.NeedsCitations*s.EvidenceNeedScore +
		w.ComplexityOverall*s.ComplexityOverall
	score -= w.RiskPenalty * s.ApprovalNeedScore

	// Complexity modifier: direct_answer loses score on complex queries;
	// advanced modes gain from high complexity.
	if mode == types.RouteDirectAnswer {
		score -= s.ComplexityOverall * 0.6
	} else {
		score += s.ComplexityOverall * 0.05
	}

	score = clamp01(score)

	// Cost / latency penalties.
	if estCost, ok := p.Budget.EstimatedCostUsd[modeKey]; ok && s.BudgetUSD > 0 {
		score -= 0.10 * (estCost / s.BudgetUSD)
	}
	if estLat, ok := p.Latency.EstimatedLatencyMs[modeKey]; ok && s.MaxLatencyMs > 0 {
		score -= 0.05 * (estLat / float64(s.MaxLatencyMs))
	}
	score = clamp01(score)
	if score < 0 {
		score = 0
	}

	estCost := estimateCostForMode(modeKey, p)
	estLat := estimateLatencyForMode(modeKey, p)

	c := types.ModeCandidate{
		Mode:            mode,
		Score:           math.Floor(score*100+0.5) / 100,
		CostEstimate:    estCost,
		LatencyTier:     latencyTier(int(estLat), p),
		SuggestedAddons: defaultAddonsForMode(modeKey, p),
	}

	// Apply rejection rules.
	if s.BudgetUSD <= 0 && estCost > 0.01 {
		c.Rejected = true
		c.RejectReason = types.RejectBudgetZero
	}

	// Allow flag checks.
	switch mode {
	case types.RouteSandboxExecution:
		if s.RequiresSandbox && !s.AllowSandbox {
			c.Rejected = true
			c.RejectReason = types.RejectUserFlagDisabled
		}
	case types.RouteResearchV2:
		if s.RequiresResearch && !s.AllowResearch {
			c.Rejected = true
			c.RejectReason = types.RejectUserFlagDisabled
		}
	case types.RouteReActTool, types.RouteDAGWorkflow:
		if s.RequiresTools && !s.AllowTools {
			c.Rejected = true
			c.RejectReason = types.RejectUserFlagDisabled
		}
	}

	return c
}

func modeKey(mode types.RoutingMode) string {
	return string(mode)
}

func estimateCostForMode(modeKey string, p *config.RouterPolicyV2) float64 {
	if v, ok := p.Budget.EstimatedCostUsd[modeKey]; ok {
		return v
	}
	return 0.05
}

func estimateLatencyForMode(modeKey string, p *config.RouterPolicyV2) float64 {
	if v, ok := p.Latency.EstimatedLatencyMs[modeKey]; ok {
		return v
	}
	return 10000
}

func latencyTier(ms int, p *config.RouterPolicyV2) string {
	f := float64(ms)
	if f <= p.Latency.FastMaxMs {
		return "fast"
	}
	if f <= p.Latency.MediumMaxMs {
		return "medium"
	}
	return "slow"
}

// ─── Capability Composition (Step 5) ────────────────────────────────────

// ComposeAddons returns the suggested addon capabilities for a mode.
// This is metadata only — the Router does NOT execute these.
func ComposeAddons(mode types.RoutingMode, s types.RouterDecisionSignals) []types.Capability {
	p := getPolicy()
	mk := modeKey(mode)
	addons := defaultAddonsForMode(mk, p)
	seen := map[types.Capability]bool{}
	out := make([]types.Capability, 0, len(addons))
	for _, a := range addons {
		if seen[a] {
			continue
		}
		seen[a] = true
		out = append(out, a)
	}
	// Forced addons from signals (planned/expected, not executed).
	if s.RequiresCitations && !seen[types.CapCitations] {
		out = append(out, types.CapCitations)
	}
	if s.SandboxNeedScore > 0.5 && !seen[types.CapSandbox] {
		out = append(out, types.CapSandbox)
	}
	if s.RagNeedScore > 0.5 && !seen[types.CapRAG] {
		out = append(out, types.CapRAG)
	}
	if s.ApprovalNeedScore > 0.5 && !seen[types.CapApproval] {
		out = append(out, types.CapApproval)
	}
	return out
}

func defaultAddonsForMode(mk string, p *config.RouterPolicyV2) []types.Capability {
	list := p.DefaultAddons[mk]
	out := make([]types.Capability, 0, len(list))
	for _, a := range list {
		out = append(out, types.Capability(strings.TrimSpace(a)))
	}
	return out
}

// ValidateForbiddenCombinations returns a list of rejected-mode reasons
// for forbidden addon + mode combinations.
func ValidateForbiddenCombinations(mode types.RoutingMode, addons []types.Capability, s types.RouterDecisionSignals) []types.RejectedModeReason {
	out := make([]types.RejectedModeReason, 0)

	// Sandbox + any advanced reasoning mode forbidden.
	if mode == types.RouteSandboxExecution {
		for _, a := range addons {
			switch a {
			case types.CapReflection, types.CapDebate:
				out = append(out, types.RejectSandboxForbidden)
				break
			default:
			}
		}
	}
	// User allow flags.
	if mode == types.RouteSandboxExecution && !s.AllowSandbox {
		out = append(out, types.RejectUserFlagDisabled)
	}
	if mode == types.RouteResearchV2 && !s.AllowResearch {
		out = append(out, types.RejectUserFlagDisabled)
	}
	return out
}

// PredictWorkspaceArtifacts returns the predicted workspace ref templates
// for a given mode. These are templates — actual refs are generated by
// the respective Workflow at runtime.
func PredictWorkspaceArtifacts(mode types.RoutingMode, workflowID string) []string {
	mk := modeKey(mode)
	patterns := workspacePatternsForMode(mk)
	out := make([]string, 0, len(patterns))
	for _, p := range patterns {
		out = append(out, strings.ReplaceAll(p, "{workflow_id}", workflowID[:minLen(8, len(workflowID))]))
	}
	return out
}

func minLen(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func workspacePatternsForMode(mk string) []string {
	switch mk {
	case "direct_answer":
		return []string{"direct:{workflow_id}:final"}
	case "rag_answer":
		return []string{"rag:{workflow_id}:final", "rag:{workflow_id}:citations"}
	case "react_tool":
		return []string{"react:{workflow_id}:final", "react:{workflow_id}:tool_calls"}
	case "dag_workflow":
		return []string{"dag:{workflow_id}:final", "dag:{workflow_id}:step:{i}"}
	case "reflection":
		return []string{"reflect:{workflow_id}:draft", "reflect:{workflow_id}:critique", "reflect:{workflow_id}:revision"}
	case "tree_of_thoughts":
		return []string{"tot:{workflow_id}:solution", "tot:{workflow_id}:best_path"}
	case "debate":
		return []string{"debate:{workflow_id}:turn:pro-r{i}", "debate:{workflow_id}:turn:con-r{i}", "debate:{workflow_id}:verdict:r{i}"}
	case "research_v2":
		return []string{"research:{workflow_id}:report", "research:{workflow_id}:executive_summary", "research:{workflow_id}:evidence_table"}
	case "swarm_workflow":
		return []string{"swarm:{workflow_id}:final"}
	case "sandbox_execution":
		return []string{"sandbox:{workflow_id}:output", "sandbox:{workflow_id}:logs"}
	default:
		return []string{"{workflow_id}:final"}
	}
}

// isAsyncRequired returns true for modes that should always use the
// async result API.
func isAsyncRequired(mode types.RoutingMode) bool {
	switch mode {
	case types.RouteReflection, types.RouteTreeOfThoughts, types.RouteDebate,
		types.RouteResearchV2, types.RouteSwarmWorkflow, types.RouteSandboxExecution:
		return true
	default:
		return false
	}
}

// ─── New selectPlannedModeV2 (Step 6) ──────────────────────────────────

// selectPlannedModeV2 runs the full multi-signal scoring path when
// the legacy heuristic is not enabled. It returns the planned mode
// plus the full explanation + candidates for audit/frontend.
// selectPlannedModeV2 is the v2 multi-signal router. The ctx parameter
// is used only by the optional LLM classifier (v3 Section 14); when
// the classifier is disabled (the default), the ctx is unused and the
// heuristic path is purely deterministic.
func selectPlannedModeV2(ctx context.Context, input EvaluateRoutingPolicyInput) (types.RoutingMode, types.RouterDecisionExplanation) {
	// Legacy kill switch: fall back to old if/else chain.
	if config.LegacyHeuristicEnabled() {
		mode := selectPlannedMode(input)
		return mode, types.RouterDecisionExplanation{SelectedMode: mode}
	}

	signals := BuildDecisionSignals(input)
	candidates := ScoreModes(signals)

	// Compatibility boost: if the legacy heuristic would have chosen
	// a mode other than direct_answer, give that mode a lift
	// proportional to the query complexity so v2 stays close to
	// the known-good baseline. High-complexity queries get stronger
	// boosts so advanced modes (debate/tot/research_v2) win.
	legacyMode := selectPlannedMode(input)
	if legacyMode != types.RouteDirectAnswer && legacyMode != types.RouteReActTool {
		// Boost proportional to complexity: 0.15..0.30.
		boost := 0.15 + signals.ComplexityOverall*0.15
		if boost > 0.30 {
			boost = 0.30
		}
		for i := range candidates {
			if candidates[i].Mode == legacyMode {
				candidates[i].Score = clamp01(candidates[i].Score + boost)
				break
			}
		}
		// Re-sort after compatibility boost.
		for i := 0; i < len(candidates); i++ {
			for j := i + 1; j < len(candidates); j++ {
				if candidates[j].Score > candidates[i].Score {
					candidates[i], candidates[j] = candidates[j], candidates[i]
				}
			}
		}
	}

	expl := types.RouterDecisionExplanation{
		Signals:       signals,
		Candidates:    candidates,
		PolicyVersion: getPolicy().Version,
		AuditRequired: true,
	}

	if len(candidates) == 0 {
		expl.SelectedMode = types.RouteDirectAnswer
		expl.SelectedReason = "fallback: no candidates scored"
		return expl.SelectedMode, expl
	}

	selected := types.RouteDirectAnswer
	for _, c := range candidates {
		if c.Rejected {
			continue
		}
		selected = c.Mode
		expl.SelectedMode = selected
		expl.SelectedReason = fmt.Sprintf("mode=%s score=%.2f", c.Mode, c.Score)
		expl.EstimatedCostUSD = c.CostEstimate
		expl.EstimatedLatencyMs = int(estimateLatencyForMode(modeKey(c.Mode), getPolicy()))
		expl.AsyncRequired = isAsyncRequired(c.Mode)
		break
	}

	// If all candidates rejected, fall back to direct_answer.
	if selected == types.RouteDirectAnswer {
		expl.SelectedReason = "fallback: all candidates rejected"
	}

	// Compose addons + validate combinations.
	expl.AddonCapabilities = ComposeAddons(selected, signals)
	rejected := ValidateForbiddenCombinations(selected, expl.AddonCapabilities, signals)
	for _, r := range rejected {
		expl.RejectedModes = append(expl.RejectedModes, types.RejectedMode{Mode: selected, Reason: r})
	}
	expl.WorkspaceArtifactsExpected = PredictWorkspaceArtifacts(selected, "")

	// Build score breakdown for frontend.
	expl.ScoreBreakdown = map[types.RoutingMode]float64{}
	for _, c := range expl.Candidates {
		expl.ScoreBreakdown[c.Mode] = c.Score
	}

	// Capability classification.
	expl.RequiredCapabilities = classifyRequiredCaps(signals)
	expl.DisabledCapabilities = classifyDisabledCaps(signals)

	// v3 Section 14: optional LLM-assisted Router Arbiter.
	// The classifier is OFF by default. It is invoked only when
	//   (a) policy.classifier.enabled = true
	//   (b) ROUTER_CLASSIFIER_ENABLED=1 (or classifier.enabled_field = true)
	//   (c) REAL_ROUTER_CLASSIFIER_TEST=1 (if real_test_only is set)
	//   (d) checkClassifierConditions returns a non-empty trigger
	// Any failure (timeout / invalid JSON / missing fields) is
	// captured in the audit and the heuristic result stands.
	applyClassifier(ctx, input, signals, candidates, &expl, &selected)

	return selected, expl
}

// applyClassifier invokes the LLM classifier when its conditions are
// met, merges the LLM scores with the heuristic scores, and updates the
// explanation / selected mode in place. On any failure it sets
// expl.ClassifierMetadata.Fallback=true and leaves the heuristic
// selection intact.
func applyClassifier(ctx context.Context, input EvaluateRoutingPolicyInput, signals types.RouterDecisionSignals, candidates []types.ModeCandidate, expl *types.RouterDecisionExplanation, selected *types.RoutingMode) {
	policy := getPolicy()
	if policy == nil {
		return
	}
	trigger := checkClassifierConditions(signals, candidates, policy)
	if trigger == "" {
		return
	}
	// Phase 7I Fix-5: when the env kill switch is on, allow the
	// classifier to run even if the on-disk policy still defaults
	// to enabled=false. This matches the activity-level relaxation
	// and avoids a footgun where envs are set but the YAML is stale.
	envOn := classifierEnvEnabled()
	realOn := !policy.Classifier.RealTestOnly || realClassifierTestEnabled()
	if !policy.Classifier.Enabled && !envOn {
		return
	}
	if !envOn {
		return
	}
	if !realOn {
		return
	}

	// Build the top-N input for the classifier (max 5).
	topN := candidates
	if len(topN) > 5 {
		topN = topN[:5]
	}
	clfInput := types.LLMClassifierInput{
		SessionID:      "", // populated by workflow if needed
		Query:          input.Query,
		UserIntent:     input.UserIntent,
		TopNCandidates: topN,
		SignalsSummary: buildSignalsSummary(signals),
		PolicyVersion:  policy.Version,
		BudgetUSD:      input.BudgetUSD,
		MaxLatencyMs:   30000,
		TriggerReason:  trigger,
	}

	ra := getRouterActivitiesSingleton()
	if ra == nil {
		return
	}
	out, _ := ra.LLMClassifierActivity(ctx, clfInput)
	if out == nil {
		return
	}

	audit := types.ClassifierAuditFields{
		Used:          !out.Fallback,
		TriggerReason: trigger,
		ModelUsed:     out.ModelUsed,
		TokensUsed:    out.TokensUsed,
		LatencyMs:     out.LatencyMs,
		Confidence:    out.Confidence,
		Fallback:      out.Fallback,
		ErrorReason:   out.ErrorReason,
		Reasoning:     out.Reasoning,
		PolicyVersion: policy.Version,
	}
	if policy.Classifier.Audit.RecordRawScores && out.Scores != nil {
		audit.RawScores = out.Scores
	}
	expl.ClassifierMetadata = &audit
	signals.ClassifierUsed = !out.Fallback
	expl.Signals.ClassifierUsed = signals.ClassifierUsed

	// v3 sandbox safety: the LLM MUST NOT downgrade a sandbox
	// selection. If the heuristic selected sandbox_execution and the
	// LLM says something else, keep the heuristic pick.
	if *selected == types.RouteSandboxExecution && out.SelectedMode != "" && out.SelectedMode != string(types.RouteSandboxExecution) {
		expl.SelectedReason += "; classifier_tried_to_downgrade_sandbox_rejected"
		return
	}

	// Merge + re-select.
	if out.Fallback {
		expl.SelectedReason += "; classifier_fallback=true"
		return
	}
	merged := mergeLLMScores(candidates, out.Scores)
	expl.Candidates = merged
	expl.ScoreBreakdown = map[types.RoutingMode]float64{}
	for _, c := range merged {
		expl.ScoreBreakdown[c.Mode] = c.Score
	}
	if len(merged) > 0 && !merged[0].Rejected {
		*selected = merged[0].Mode
		expl.SelectedMode = *selected
		expl.SelectedReason = fmt.Sprintf("mode=%s score=%.2f (llm_classifier, confidence=%.2f, trigger=%s)", merged[0].Mode, merged[0].Score, out.Confidence, trigger)
		expl.EstimatedCostUSD = merged[0].CostEstimate
		expl.EstimatedLatencyMs = int(estimateLatencyForMode(modeKey(merged[0].Mode), policy))
		expl.AsyncRequired = isAsyncRequired(merged[0].Mode)
	}
}

func buildSignalsSummary(s types.RouterDecisionSignals) string {
	return fmt.Sprintf("overall=%.2f analyze=%.2f research=%.2f exec=%.2f debate=%.2f exploration=%.2f rag=%.2f tools=%v sandbox=%v research=%v",
		s.ComplexityOverall, s.ComplexityAnalyze, s.ComplexityResearch,
		s.ComplexityExecution, s.ComplexityDebate, s.ComplexityExploration,
		s.RagNeedScore, s.RequiresTools, s.RequiresSandbox, s.RequiresResearch)
}

// getRouterActivitiesSingleton returns the RouterActivities instance
// for the current process. It is set by NewRouterActivities; if the
// worker hasn't been initialised yet the classifier is a no-op.
var routerActivitiesSingleton *RouterActivities

func getRouterActivitiesSingleton() *RouterActivities {
	return routerActivitiesSingleton
}

func classifyRequiredCaps(s types.RouterDecisionSignals) []types.Capability {
	caps := []types.Capability{types.CapAudit, types.CapWorkspace}
	if s.RequiresRAG || s.RagNeedScore > 0.5 {
		caps = append(caps, types.CapRAG)
	}
	if s.RequiresSandbox || s.SandboxNeedScore > 0.5 {
		caps = append(caps, types.CapSandbox)
	}
	if s.RequiresCitations || s.EvidenceNeedScore > 0.5 {
		caps = append(caps, types.CapCitations)
	}
	if s.ToolNeedScore > 0.3 {
		caps = append(caps, types.CapMCPTools)
	}
	if s.SkillNeedScore > 0.3 {
		caps = append(caps, types.CapSkills)
	}
	return caps
}

func classifyDisabledCaps(s types.RouterDecisionSignals) []types.Capability {
	caps := []types.Capability{}
	if !s.AllowSandbox {
		caps = append(caps, types.CapSandbox)
	}
	if !s.AllowResearch {
		caps = append(caps, types.CapWebSearch)
	}
	if !s.AllowTools {
		caps = append(caps, types.CapMCPTools)
	}
	return caps
}
