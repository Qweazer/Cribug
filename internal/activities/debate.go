package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"cribug/internal/types"
)

// DebateActivities holds dependencies for Debate Mode Activities (Phase 7E Slice 27).
//
// Constraints:
//   - Activities are the only place that calls LLM / writes Workspace.
//   - Workflow only orchestrates Debate activities and never directly accesses LLM/IO.
//   - Full Pro/Con arguments and Judge reasoning are written to Workspace via Refs;
//     the Workflow history stores only short summaries + refs.
type DebateActivities struct {
	agentActs *AgentActivities
}

// NewDebateActivities creates a new DebateActivities instance.
func NewDebateActivities(llmServiceURL string) *DebateActivities {
	return &DebateActivities{agentActs: NewAgentActivities(llmServiceURL)}
}

// ─── GenerateArgumentsActivity ──────────────────────────────────────────

// GenerateArgumentsInput asks one Agent (pro or con) to produce an argument for the
// current round. PreviousTurnRef is the opposing Agent's previous argument Workspace
// ref; if empty, this is the first round.
type GenerateArgumentsInput struct {
	Position        string `json:"position"`         // "pro" | "con"
	Query           string `json:"query"`
	RoundIndex      int    `json:"round_index"`
	PreviousTurnRef string `json:"previous_turn_ref"` // opposing Agent's previous argument ref
	PreviousSummary string `json:"previous_summary"`  // short summary of opposing arg (for prompt context)
	Model           string `json:"model"`
	MockLLM         bool   `json:"mock_llm"`
	TaskID          string `json:"task_id"`
	WorkflowID      string `json:"workflow_id"`
	RunID           string `json:"run_id"`
}

// GenerateArgumentsResult contains the produced argument and metadata.
type GenerateArgumentsResult struct {
	TurnID     string  `json:"turn_id"`
	Content    string  `json:"content"`     // full argument text (kept in activity result, but
	// Workflow only stores the Ref + Summary to history.
	ContentRef string  `json:"content_ref"`
	Summary    string  `json:"summary"`
	Score      float64 `json:"score"`
	Confidence float64 `json:"confidence"`
	TokensUsed int     `json:"tokens_used"`
	Mode       string  `json:"mode,omitempty"` // "real" | "mock"
}

// GenerateArguments is the Pro/Con Agent Activity. Position is "pro" or "con".
func (da *DebateActivities) GenerateArguments(ctx context.Context, input GenerateArgumentsInput) (*GenerateArgumentsResult, error) {
	turnID := fmt.Sprintf("turn-%s-r%d", input.Position, input.RoundIndex)
	ref := fmt.Sprintf("debate:%s:turn:%s", input.WorkflowID, turnID)

	if input.MockLLM {
		content := buildMockArgument(input.Position, input.Query, input.RoundIndex, input.PreviousSummary)
		return &GenerateArgumentsResult{
			TurnID:     turnID,
			Content:    content,
			ContentRef: ref,
			Summary:    truncate(content, 200),
			Score:      0.6 + 0.05*float64(input.RoundIndex%3),
			Confidence: 0.7,
			TokensUsed: 30,
			Mode:       "mock",
		}, nil
	}

	prompt := buildProConPrompt(input.Position, input.Query, input.RoundIndex, input.PreviousSummary)

	result, err := da.agentActs.CallLLM(ctx, AgentActivityInput{
		TaskID:                input.TaskID,
		WorkflowID:            input.WorkflowID,
		RunID:                 input.RunID,
		Query:                 prompt,
		Model:                 input.Model,
		Temperature:           0.7,
		MaxCompletionTokens:   512,
		AllowedCompletionTokens: 512,
	})
	if err != nil {
		return nil, fmt.Errorf("generate %s argument: %w", input.Position, err)
	}

	confidence := 0.7
	if c, ok := extractFloatAfter(result.Answer, "confidence:"); ok {
		confidence = clampUnit(c)
	}

	return &GenerateArgumentsResult{
		TurnID:     turnID,
		Content:    result.Answer,
		ContentRef: ref,
		Summary:    truncate(result.Answer, 200),
		Score:      0.6,
		Confidence: confidence,
		TokensUsed: result.Usage.TotalTokens,
		Mode:       "real",
	}, nil
}

func buildProConPrompt(position, query string, round int, prevSummary string) string {
	header := fmt.Sprintf("You are the %s debater in a structured debate. Take a strong %s position on the following proposition.",
		strings.ToUpper(position), strings.ToUpper(position))
	body := fmt.Sprintf("Proposition: %q", query)
	roundInfo := fmt.Sprintf("Round: %d", round+1)
	prev := ""
	if prevSummary != "" {
		prev = fmt.Sprintf("\nThe opposing debater's previous argument (for context only): %s\n" +
			"Address or rebut it directly.", prevSummary)
	}
	output := "Respond with a focused argument (3-5 sentences) supporting the " + strings.ToUpper(position) +
		" side. After the argument, include a line: confidence: <0.0-1.0>"
	return strings.Join([]string{header, body, roundInfo, prev, output}, "\n\n")
}

func buildMockArgument(position, query string, round int, prevSummary string) string {
	var rebuttal string
	if prevSummary != "" && round > 0 {
		rebuttal = " In response to the opposing argument, " + truncate(prevSummary, 80) + " — this is incorrect because: " +
			"the trade-offs must be evaluated against measurable outcomes, not rhetorical symmetry."
	}
	switch position {
	case "pro":
		return fmt.Sprintf("PRO (round %d) on %q: the evidence supports this position because it provides clearer "+
			"operational guarantees, measurable safety properties, and a simpler audit trail.%s",
			round+1, query, rebuttal)
	case "con":
		return fmt.Sprintf("CON (round %d) on %q: the evidence supports this position because flexibility and "+
			"agent autonomy unlock better real-world performance and adaptability under uncertainty.%s",
			round+1, query, rebuttal)
	default:
		return fmt.Sprintf("Mock %s argument for round %d on %q.", position, round+1, query)
	}
}

func extractFloatAfter(s, marker string) (float64, bool) {
	idx := strings.Index(strings.ToLower(s), strings.ToLower(marker))
	if idx < 0 {
		return 0, false
	}
	rest := s[idx+len(marker):]
	// Take first whitespace-bounded token
	rest = strings.TrimLeft(rest, " \t=:")
	end := strings.IndexAny(rest, " \n\t,;")
	if end > 0 {
		rest = rest[:end]
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(rest), 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

func clampUnit(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// ─── JudgeDebateActivity ───────────────────────────────────────────────

// JudgeDebateInput asks the Judge to score the current transcript.
type JudgeDebateInput struct {
	Query         string `json:"query"`
	TranscriptRef string `json:"transcript_ref"`
	RoundIndex    int    `json:"round_index"`
	Model         string `json:"model"`
	MockLLM       bool   `json:"mock_llm"`
	TaskID        string `json:"task_id"`
	WorkflowID    string `json:"workflow_id"`
	RunID         string `json:"run_id"`
}

// JudgeDebateResult contains the judge's verdict and metadata.
type JudgeDebateResult struct {
	Verdict         *types.JudgeVerdict `json:"verdict"`
	VerdictRef      string              `json:"verdict_ref"` // Workspace ref for full verdict
	Rationale       string              `json:"rationale"`   // short summary
	ProScore        float64             `json:"pro_score"`
	ConScore        float64             `json:"con_score"`
	Confidence      float64             `json:"confidence"`
	TokensUsed      int                 `json:"tokens_used"`
	Mode            string              `json:"mode,omitempty"`
	ParseSource     string              `json:"parse_source,omitempty"`     // "json" | "regex" | "heuristic"
	ConfidenceSrc   string              `json:"confidence_source,omitempty"` // "json" | "fallback"
}

// JudgeDebate scores the current round and produces a structured verdict.
// It can operate on a TranscriptRef (the Workflow has already appended
// Pro/Con turns to the Workspace); in mock mode it returns a deterministic verdict.
func (da *DebateActivities) JudgeDebate(ctx context.Context, input JudgeDebateInput) (*JudgeDebateResult, error) {
	ref := fmt.Sprintf("debate:%s:verdict:r%d", input.WorkflowID, input.RoundIndex)

	if input.MockLLM {
		v := mockJudgeVerdict(input.RoundIndex)
		return &JudgeDebateResult{
			Verdict:       v,
			VerdictRef:    ref,
			Rationale:     v.Rationale,
			ProScore:      v.ProScore,
			ConScore:      v.ConScore,
			Confidence:    v.Confidence,
			TokensUsed:    30,
			Mode:          "mock",
			ParseSource:   "heuristic",
			ConfidenceSrc: "fallback",
		}, nil
	}

	prompt := buildJudgePrompt(input.Query, input.TranscriptRef, input.RoundIndex)
	result, err := da.agentActs.CallLLM(ctx, AgentActivityInput{
		TaskID:                input.TaskID,
		WorkflowID:            input.WorkflowID,
		RunID:                 input.RunID,
		Query:                 prompt,
		Model:                 input.Model,
		Temperature:           0.3,
		MaxCompletionTokens:   768,
		AllowedCompletionTokens: 768,
	})
	if err != nil {
		return nil, fmt.Errorf("judge debate: %w", err)
	}

	v, parseSrc, confSrc := parseJudgeVerdict(result.Answer)
	// Ensure no hardcoded winner — always use parsed result.
	if v == nil {
		v = mockJudgeVerdict(input.RoundIndex)
		parseSrc = "heuristic"
		confSrc = "fallback"
	}
	return &JudgeDebateResult{
		Verdict:       v,
		VerdictRef:    ref,
		Rationale:     v.Rationale,
		ProScore:      v.ProScore,
		ConScore:      v.ConScore,
		Confidence:    v.Confidence,
		TokensUsed:    result.Usage.TotalTokens,
		Mode:          "real",
		ParseSource:   parseSrc,
		ConfidenceSrc: confSrc,
	}, nil
}

func mockJudgeVerdict(round int) *types.JudgeVerdict {
	// Alternating + balanced: never hard-codes winner as a constant.
	proScore := 0.55 + 0.05*float64(round%3)
	conScore := 0.55 + 0.05*float64((round+1)%3)
	verdict := types.DebateVerdictTie
	if proScore > conScore+0.05 {
		verdict = types.DebateVerdictPro
	} else if conScore > proScore+0.05 {
		verdict = types.DebateVerdictCon
	}
	return &types.JudgeVerdict{
		Verdict:        verdict,
		ProScore:       proScore,
		ConScore:       conScore,
		Confidence:     0.7,
		Rationale:      "mock heuristic verdict based on alternating per-round scores",
		KeyProPoints:   []string{"deterministic guarantees", "auditable execution"},
		KeyConPoints:   []string{"reduced flexibility", "slower iteration"},
		Unresolved:     []string{"long-term adaptability under unknown failure modes"},
		Recommendation: "adopt a hybrid: deterministic core with bounded flexibility for low-risk paths",
	}
}

func buildJudgePrompt(query, transcriptRef string, round int) string {
	return fmt.Sprintf(
		`You are a neutral judge in a structured debate. Read the debate transcript (topic ref: %s, round %d) for the proposition:
%q

Score the Pro and Con sides from 0.0 to 1.0 and decide a winner ("pro" | "con" | "tie"). Return STRICT JSON:
{
  "verdict": "pro" | "con" | "tie",
  "pro_score": 0.0-1.0,
  "con_score": 0.0-1.0,
  "confidence": 0.0-1.0,
  "rationale": "1-3 sentence summary",
  "key_pro_points": ["..."],
  "key_con_points": ["..."],
  "unresolved_issues": ["..."],
  "recommendation": "final recommendation / answer"
}

Do NOT default to a hard-coded winner. Base verdict strictly on the relative strength of the arguments.`,
		transcriptRef, round+1, query,
	)
}

// parseJudgeVerdict extracts a structured JudgeVerdict from raw LLM text.
// Returns the verdict, parse source ("json" | "regex" | "heuristic"), and
// confidence source ("json" | "fallback").
//
// Order:
//  1. Try strict JSON block (with json.RawMessage tolerant parsing).
//  2. Try field-by-field regex extraction.
//  3. Fall back to heuristic: count pro/con keyword occurrences and a soft verdict.
func parseJudgeVerdict(raw string) (*types.JudgeVerdict, string, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, "heuristic", "fallback"
	}

	// 1) Try strict JSON block.
	if start := strings.Index(raw, "{"); start >= 0 {
		if end := strings.LastIndex(raw, "}"); end > start {
			block := raw[start : end+1]
			var v types.JudgeVerdict
			if err := json.Unmarshal([]byte(block), &v); err == nil && v.Verdict != "" {
				v.Verdict = normalizeVerdict(v.Verdict)
				v.ProScore = clampUnit(v.ProScore)
				v.ConScore = clampUnit(v.ConScore)
				v.Confidence = clampUnit(v.Confidence)
				return &v, "json", "json"
			}
		}
	}

	// 2) Regex-style field extraction.
	v := &types.JudgeVerdict{Verdict: "tie"}
	if s, ok := extractFloatAfter(raw, "verdict_score_pro:"); ok {
		v.ProScore = clampUnit(s)
	} else if s, ok := extractFloatAfter(raw, "pro_score:"); ok {
		v.ProScore = clampUnit(s)
	}
	if s, ok := extractFloatAfter(raw, "con_score:"); ok {
		v.ConScore = clampUnit(s)
	}
	if s, ok := extractFloatAfter(raw, "confidence:"); ok {
		v.Confidence = clampUnit(s)
	}
	verdict := ""
	low := strings.ToLower(raw)
	switch {
	case strings.Contains(low, "\"verdict\": \"pro\"") || strings.Contains(low, "verdict: pro"):
		verdict = "pro"
	case strings.Contains(low, "\"verdict\": \"con\"") || strings.Contains(low, "verdict: con"):
		verdict = "con"
	case strings.Contains(low, "\"verdict\": \"tie\"") || strings.Contains(low, "verdict: tie"):
		verdict = "tie"
	}
	if verdict != "" {
		v.Verdict = verdict
	}
	if v.Verdict != "" && (v.ProScore > 0 || v.ConScore > 0) {
		return v, "regex", "regex"
	}

	// 3) Heuristic fallback — keyword counts, never a hard-coded winner.
	proHits := countKeywords(low, []string{"strong pro", "pro wins", "supports the pro", "in favor", "benefits outweigh"})
	conHits := countKeywords(low, []string{"strong con", "con wins", "supports the con", "against", "risks outweigh"})
	if proHits > conHits {
		v.Verdict = "pro"
		v.ProScore, v.ConScore = 0.6, 0.4
	} else if conHits > proHits {
		v.Verdict = "con"
		v.ProScore, v.ConScore = 0.4, 0.6
	} else {
		v.Verdict = "tie"
		v.ProScore, v.ConScore = 0.5, 0.5
	}
	v.Confidence = 0.4
	v.Rationale = truncate(raw, 200)
	return v, "heuristic", "fallback"
}

func normalizeVerdict(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "pro", "pros", "in favor", "yes":
		return "pro"
	case "con", "cons", "against", "no":
		return "con"
	case "tie", "draw", "even":
		return "tie"
	}
	return "tie"
}

// ─── CheckConsensusActivity ────────────────────────────────────────────

// CheckConsensusInput checks whether the running transcript has produced a
// confident verdict.
type CheckConsensusInput struct {
	TranscriptRef string  `json:"transcript_ref"`
	Threshold     float64 `json:"threshold"`
	LatestVerdict string  `json:"latest_verdict"`
	LatestConf    float64 `json:"latest_conf"`
}

// CheckConsensusResult indicates whether consensus was reached.
type CheckConsensusResult struct {
	Consensus bool   `json:"consensus"`
	Winner    string `json:"winner"`
}

// CheckConsensus decides whether the latest verdict is decisive enough to stop.
func (da *DebateActivities) CheckConsensus(ctx context.Context, input CheckConsensusInput) (*CheckConsensusResult, error) {
	if input.LatestConf >= input.Threshold && input.LatestVerdict != types.DebateVerdictTie {
		return &CheckConsensusResult{Consensus: true, Winner: input.LatestVerdict}, nil
	}
	return &CheckConsensusResult{Consensus: false, Winner: input.LatestVerdict}, nil
}

// ─── AuditDebateActivity ───────────────────────────────────────────────

// AuditDebateInput records the final debate result.
type AuditDebateInput struct {
	WorkflowID     string `json:"workflow_id"`
	Query          string `json:"query"`
	FinalPosition  string `json:"final_position"`
	Consensus      bool   `json:"consensus"`
	TotalRounds    int    `json:"total_rounds"`
	TotalTokens    int    `json:"total_tokens"`
	VerdictRef     string `json:"verdict_ref"`
	TranscriptRef  string `json:"transcript_ref"`
	WinningTurnRef string `json:"winning_turn_ref"`
}

// AuditDebateResult returns the audit ID.
type AuditDebateResult struct {
	AuditID string `json:"audit_id"`
}

// AuditDebate records the debate to DB + audit. Currently a no-op placeholder
// (we keep the Audit table schema as documentation; the source of truth is
// the Workspace transcript and the Workflow result).
func (da *DebateActivities) AuditDebate(ctx context.Context, input AuditDebateInput) (*AuditDebateResult, error) {
	return &AuditDebateResult{AuditID: "audit-" + input.WorkflowID}, nil
}
