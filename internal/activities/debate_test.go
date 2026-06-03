package activities

import (
	"context"
	"strings"
	"testing"
)

func TestGenerateArgumentsMockPro(t *testing.T) {
	da := NewDebateActivities("")
	res, err := da.GenerateArguments(context.Background(), GenerateArgumentsInput{
		Position:   "pro",
		Query:      "Should we adopt X?",
		RoundIndex: 0,
		MockLLM:    true,
		WorkflowID: "wf-1",
		TaskID:     "task-1",
		RunID:      "run-1",
	})
	if err != nil {
		t.Fatalf("GenerateArguments(pro) failed: %v", err)
	}
	if res.Mode != "mock" {
		t.Errorf("mode = %q, want mock", res.Mode)
	}
	if !strings.Contains(res.Content, "PRO") {
		t.Errorf("pro argument must contain PRO: %q", res.Content)
	}
	if res.ContentRef == "" {
		t.Error("content_ref must be set")
	}
	if res.TokensUsed <= 0 {
		t.Error("tokens_used must be > 0 in mock mode")
	}
}

func TestGenerateArgumentsMockCon(t *testing.T) {
	da := NewDebateActivities("")
	res, err := da.GenerateArguments(context.Background(), GenerateArgumentsInput{
		Position:        "con",
		Query:           "Should we adopt X?",
		RoundIndex:      1,
		PreviousSummary: "Pro said deterministic workflows are safer",
		MockLLM:         true,
		WorkflowID:      "wf-1",
	})
	if err != nil {
		t.Fatalf("GenerateArguments(con) failed: %v", err)
	}
	if !strings.Contains(res.Content, "CON") {
		t.Errorf("con argument must contain CON: %q", res.Content)
	}
	// Round > 0 with previous summary should include a rebuttal
	if !strings.Contains(strings.ToLower(res.Content), "response") {
		t.Errorf("round>0 con arg should mention rebuttal: %q", res.Content)
	}
}

func TestGenerateArgumentsTurnIDsUnique(t *testing.T) {
	da := NewDebateActivities("")
	r0, _ := da.GenerateArguments(context.Background(), GenerateArgumentsInput{
		Position: "pro", RoundIndex: 0, MockLLM: true, WorkflowID: "wf-1",
	})
	r1, _ := da.GenerateArguments(context.Background(), GenerateArgumentsInput{
		Position: "pro", RoundIndex: 1, MockLLM: true, WorkflowID: "wf-1",
	})
	if r0.TurnID == r1.TurnID {
		t.Errorf("turn ids must differ across rounds: %s == %s", r0.TurnID, r1.TurnID)
	}
	if r0.ContentRef == r1.ContentRef {
		t.Errorf("content refs must differ across rounds: %s == %s", r0.ContentRef, r1.ContentRef)
	}
}

func TestJudgeDebateMockReturnsStructured(t *testing.T) {
	da := NewDebateActivities("")
	res, err := da.JudgeDebate(context.Background(), JudgeDebateInput{
		Query:      "Should X?",
		RoundIndex: 0,
		MockLLM:    true,
		WorkflowID: "wf-1",
		TaskID:     "task-1",
		RunID:      "run-1",
	})
	if err != nil {
		t.Fatalf("JudgeDebate failed: %v", err)
	}
	v := res.Verdict
	if v == nil {
		t.Fatal("verdict must not be nil in mock mode")
	}
	if v.Verdict == "" {
		t.Error("verdict string must be set")
	}
	if v.Rationale == "" {
		t.Error("rationale must be set")
	}
	if v.Recommendation == "" {
		t.Error("recommendation must be set")
	}
	if v.ProScore < 0 || v.ProScore > 1 {
		t.Errorf("pro_score out of [0,1]: %f", v.ProScore)
	}
	if v.ConScore < 0 || v.ConScore > 1 {
		t.Errorf("con_score out of [0,1]: %f", v.ConScore)
	}
	if v.Confidence < 0 || v.Confidence > 1 {
		t.Errorf("confidence out of [0,1]: %f", v.Confidence)
	}
}

func TestJudgeDebateAlternatesVerdict(t *testing.T) {
	// Mock judge must NOT hard-code a winner. Across rounds it should
	// produce at least 2 different verdicts OR show non-trivial score
	// variation, so we can detect a constant "pro" / "con" default.
	da := NewDebateActivities("")
	seen := map[string]int{}
	for r := 0; r < 6; r++ {
		res, _ := da.JudgeDebate(context.Background(), JudgeDebateInput{
			Query: "Q", RoundIndex: r, MockLLM: true, WorkflowID: "wf", TaskID: "t", RunID: "r",
		})
		seen[res.Verdict.Verdict]++
	}
	// Across 6 rounds, both 'pro' and 'con' or 'tie' should appear at
	// least once, otherwise the mock judge is hard-coded.
	proConTie := seen["pro"] + seen["con"] + seen["tie"]
	if proConTie == 0 {
		t.Fatal("no verdicts produced")
	}
	// Detect hard-coding: if all 6 rounds produce the exact same verdict
	// with no score variation, that smells like a constant default.
	if seen["pro"] == 6 || seen["con"] == 6 {
		t.Errorf("mock judge appears hard-coded to one side across 6 rounds: %v", seen)
	}
}

func TestParseJudgeVerdictJSON(t *testing.T) {
	raw := `{
		"verdict": "pro",
		"pro_score": 0.7,
		"con_score": 0.4,
		"confidence": 0.85,
		"rationale": "Pro side more compelling",
		"key_pro_points": ["a", "b"],
		"key_con_points": ["c"],
		"unresolved_issues": [],
		"recommendation": "Adopt X"
	}`
	v, src, confSrc := parseJudgeVerdict(raw)
	if v == nil {
		t.Fatal("expected parsed verdict")
	}
	if v.Verdict != "pro" {
		t.Errorf("verdict = %q, want pro", v.Verdict)
	}
	if v.ProScore < 0.69 || v.ProScore > 0.71 {
		t.Errorf("pro_score = %f, want ~0.7", v.ProScore)
	}
	if src != "json" {
		t.Errorf("parse_source = %q, want json", src)
	}
	if confSrc != "json" {
		t.Errorf("confidence_source = %q, want json", confSrc)
	}
}

func TestParseJudgeVerdictJSONTie(t *testing.T) {
	raw := `{"verdict":"tie","pro_score":0.5,"con_score":0.5,"confidence":0.5,"rationale":"balanced"}`
	v, src, _ := parseJudgeVerdict(raw)
	if v == nil || v.Verdict != "tie" {
		t.Errorf("verdict = %+v, want tie", v)
	}
	if src != "json" {
		t.Errorf("parse_source = %q, want json", src)
	}
}

func TestParseJudgeVerdictRegexFallback(t *testing.T) {
	// No JSON block, but with field-like syntax.
	raw := "verdict: con\npro_score: 0.3\ncon_score: 0.8\nconfidence: 0.6"
	v, src, _ := parseJudgeVerdict(raw)
	if v == nil {
		t.Fatal("expected non-nil verdict from regex")
	}
	if v.Verdict != "con" {
		t.Errorf("verdict = %q, want con", v.Verdict)
	}
	if src != "regex" && src != "heuristic" {
		t.Errorf("parse_source = %q, want regex or heuristic", src)
	}
}

func TestParseJudgeVerdictHeuristicFallback(t *testing.T) {
	// Pure free text: should still produce a non-nil verdict.
	raw := "After careful consideration, the strong pro arguments win this round."
	v, src, confSrc := parseJudgeVerdict(raw)
	if v == nil {
		t.Fatal("heuristic must yield non-nil")
	}
	if v.Verdict != "pro" {
		t.Errorf("verdict = %q, want pro", v.Verdict)
	}
	if src != "heuristic" {
		t.Errorf("parse_source = %q, want heuristic", src)
	}
	if confSrc != "heuristic" {
		t.Errorf("confidence_source = %q, want heuristic", confSrc)
	}
}

func TestParseJudgeVerdictEmptyRaw(t *testing.T) {
	v, src, _ := parseJudgeVerdict("")
	if v != nil {
		t.Errorf("empty raw should yield nil, got %+v", v)
	}
	if src != "heuristic" {
		t.Errorf("parse_source = %q, want heuristic", src)
	}
}

func TestParseJudgeVerdictStripsThinkAndMarkdown(t *testing.T) {
	// MiniMax / DeepSeek style: <think>...</think> + markdown fence
	// around the JSON. Stripping should yield a parseable JSON.
	raw := "<think>The user wants me to judge this debate. Let me consider both sides carefully.</think>\n```json\n{\n  \"verdict\": \"pro\",\n  \"pro_score\": 0.7,\n  \"con_score\": 0.4,\n  \"confidence\": 0.85,\n  \"rationale\": \"Pro stronger\"\n}\n```"
	v, src, confSrc := parseJudgeVerdict(raw)
	if v == nil {
		t.Fatalf("expected non-nil verdict, got nil")
	}
	if v.Verdict != "pro" {
		t.Errorf("verdict = %q, want pro", v.Verdict)
	}
	if src != "json" {
		t.Errorf("parse_source = %q, want json", src)
	}
	if confSrc != "json" {
		t.Errorf("confidence_source = %q, want json", confSrc)
	}
}

func TestParseJudgeVerdictJSONWithoutConfidence(t *testing.T) {
	// JSON parses but confidence is absent -> confidence_source=heuristic
	// (still extract verdict + scores from JSON).
	raw := `{"verdict": "con", "pro_score": 0.3, "con_score": 0.7, "rationale": "x"}`
	v, src, confSrc := parseJudgeVerdict(raw)
	if v == nil {
		t.Fatal("expected non-nil verdict")
	}
	if v.Verdict != "con" {
		t.Errorf("verdict = %q, want con", v.Verdict)
	}
	if src != "json" {
		t.Errorf("parse_source = %q, want json", src)
	}
	if confSrc != "heuristic" {
		t.Errorf("confidence_source = %q, want heuristic", confSrc)
	}
	if v.Confidence <= 0 {
		t.Errorf("confidence = %f, want > 0 (heuristic default)", v.Confidence)
	}
}

func TestParseJudgeVerdictRegexWithConfidence(t *testing.T) {
	// Regex path with confidence value -> confSrc=regex.
	raw := "verdict: pro\npro_score: 0.7\ncon_score: 0.3\nconfidence: 0.9"
	v, src, confSrc := parseJudgeVerdict(raw)
	if v == nil {
		t.Fatal("expected non-nil")
	}
	if src != "regex" {
		t.Errorf("parse_source = %q, want regex", src)
	}
	if confSrc != "regex" {
		t.Errorf("confidence_source = %q, want regex", confSrc)
	}
}

func TestParseJudgeVerdictRegexNoConfidence(t *testing.T) {
	// Regex path with no confidence value -> confSrc=heuristic.
	raw := "verdict: pro\npro_score: 0.7\ncon_score: 0.3"
	v, src, confSrc := parseJudgeVerdict(raw)
	if v == nil {
		t.Fatal("expected non-nil")
	}
	if src != "regex" {
		t.Errorf("parse_source = %q, want regex", src)
	}
	if confSrc != "heuristic" {
		t.Errorf("confidence_source = %q, want heuristic", confSrc)
	}
}

func TestStripThinkAndMarkdown(t *testing.T) {
	cases := []struct{ in, want string }{
		{"<think>foo</think>{}", "{}"},
		{"```json\n{}\n```", "{}"},
		{"```JSON\n{}\n```", "{}"},
		{"```{}{}{}```", "{}{}{}"}, // ```json strip first, then ``` strips
		{"json\n{}", "{}"},
		{"plain", "plain"},
		{"<think>a<think>b</think>c</think>{}", "c</think>{}"}, // nested think: outer strips first pass, leaves "c</think>{}"
		{"", ""},
	}
	for _, tc := range cases {
		if got := stripThinkAndMarkdown(tc.in); got != tc.want {
			t.Errorf("stripThinkAndMarkdown(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParseJudgeVerdictNoHardcodedWinner(t *testing.T) {
	// Across many different raw inputs, the parser must NOT always
	// produce the same verdict (no hard-coded default winner).
	inputs := []string{
		"the benefits outweigh the risks",
		"there are serious concerns that cannot be ignored",
		"both sides have merit",
		"the con side is clearly stronger here",
		"the pro side made a much better case",
		"we should adopt the pro position",
		"reject this proposal",
	}
	votes := map[string]int{}
	for _, in := range inputs {
		v, _, _ := parseJudgeVerdict(in)
		if v == nil {
			t.Fatal("must produce a verdict")
		}
		votes[v.Verdict]++
	}
	// We expect at least 2 distinct verdicts across these 7 inputs.
	if len(votes) < 2 {
		t.Errorf("parser appears hard-coded: votes=%v", votes)
	}
}

func TestCheckConsensusAboveThreshold(t *testing.T) {
	da := NewDebateActivities("")
	res, err := da.CheckConsensus(context.Background(), CheckConsensusInput{
		Threshold:     0.7,
		LatestVerdict: "pro",
		LatestConf:    0.8,
	})
	if err != nil {
		t.Fatalf("CheckConsensus failed: %v", err)
	}
	if !res.Consensus {
		t.Error("consensus must be true when conf >= threshold and verdict != tie")
	}
	if res.Winner != "pro" {
		t.Errorf("winner = %q, want pro", res.Winner)
	}
}

func TestCheckConsensusTieNeverConsensus(t *testing.T) {
	da := NewDebateActivities("")
	res, _ := da.CheckConsensus(context.Background(), CheckConsensusInput{
		Threshold:     0.5,
		LatestVerdict: "tie",
		LatestConf:    0.99,
	})
	if res.Consensus {
		t.Error("tie must never be consensus")
	}
}

func TestCheckConsensusBelowThreshold(t *testing.T) {
	da := NewDebateActivities("")
	res, _ := da.CheckConsensus(context.Background(), CheckConsensusInput{
		Threshold:     0.8,
		LatestVerdict: "pro",
		LatestConf:    0.5,
	})
	if res.Consensus {
		t.Error("low confidence must not produce consensus")
	}
}

func TestAuditDebateReturnsID(t *testing.T) {
	da := NewDebateActivities("")
	res, err := da.AuditDebate(context.Background(), AuditDebateInput{
		WorkflowID: "wf-1", Query: "Q", FinalPosition: "pro",
	})
	if err != nil {
		t.Fatalf("AuditDebate failed: %v", err)
	}
	if res.AuditID == "" {
		t.Error("audit_id must be set")
	}
}

func TestMockArgumentDeterministic(t *testing.T) {
	// Same inputs must produce same output (mock hygiene).
	a := buildMockArgument("pro", "Q", 0, "")
	b := buildMockArgument("pro", "Q", 0, "")
	if a != b {
		t.Errorf("mock must be deterministic: %q vs %q", a, b)
	}
}

func TestExtractFloatAfter(t *testing.T) {
	if v, ok := extractFloatAfter("confidence: 0.85", "confidence:"); !ok || v != 0.85 {
		t.Errorf("extractFloatAfter basic failed: v=%f ok=%v", v, ok)
	}
	if v, ok := extractFloatAfter("confidence:0.42\nfoo", "confidence:"); !ok || v != 0.42 {
		t.Errorf("extractFloatAfter inline failed: v=%f ok=%v", v, ok)
	}
	if _, ok := extractFloatAfter("no number", "confidence:"); ok {
		t.Error("extractFloatAfter should return false when no number")
	}
}

func TestClampUnit(t *testing.T) {
	if clampUnit(-0.5) != 0 {
		t.Error("clampUnit negative must clamp to 0")
	}
	if clampUnit(1.5) != 1 {
		t.Error("clampUnit > 1 must clamp to 1")
	}
	if clampUnit(0.5) != 0.5 {
		t.Error("clampUnit in-range must preserve")
	}
}

func TestNormalizeVerdict(t *testing.T) {
	cases := map[string]string{
		"pro":     "pro",
		"PRO":     "pro",
		"  pros":  "pro",
		"in favor": "pro",
		"yes":     "pro",
		"con":     "con",
		"CONS":    "con",
		"against": "con",
		"no":      "con",
		"tie":     "tie",
		"draw":    "tie",
		"even":    "tie",
		"foo":     "tie", // unknown -> tie
	}
	for in, want := range cases {
		if got := normalizeVerdict(in); got != want {
			t.Errorf("normalizeVerdict(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildProConPromptIncludesPosition(t *testing.T) {
	p := buildProConPrompt("pro", "Q", 0, "")
	if !strings.Contains(p, "PRO") {
		t.Error("pro prompt must mention PRO")
	}
	p2 := buildProConPrompt("con", "Q", 0, "")
	if !strings.Contains(p2, "CON") {
		t.Error("con prompt must mention CON")
	}
	p3 := buildProConPrompt("pro", "Q", 2, "opponent said X")
	if !strings.Contains(p3, "opponent said X") {
		t.Error("round>0 prompt should include previous summary")
	}
}

func TestBuildJudgePromptIncludesQuery(t *testing.T) {
	p := buildJudgePrompt("Should we X?", "ref:abc", 0)
	if !strings.Contains(p, "Should we X?") {
		t.Error("judge prompt must include query")
	}
	if !strings.Contains(p, "ref:abc") {
		t.Error("judge prompt must include transcript ref")
	}
}
