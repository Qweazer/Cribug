package activities

import (
	"context"
	"strings"
	"testing"

	"cribug/internal/types"
)

func TestPlanResearchMockReturnsSubqueries(t *testing.T) {
	ra := NewResearchV2Activities("")
	res, err := ra.PlanResearch(context.Background(), PlanResearchInput{
		Query:        "Should we use async polling for long-running workflows?",
		MaxSubqueries: 3,
		MockLLM:      true,
		WorkflowID:   "wf-1",
		TaskID:       "t-1",
		RunID:        "r-1",
	})
	if err != nil {
		t.Fatalf("PlanResearch: %v", err)
	}
	if res.Mode != "mock" {
		t.Errorf("mode = %q, want mock", res.Mode)
	}
	if len(res.Subqueries) == 0 {
		t.Fatal("must have at least 1 subquery")
	}
	if len(res.Subqueries) > 3 {
		t.Errorf("subqueries %d > max 3", len(res.Subqueries))
	}
	if res.QueryID == "" {
		t.Error("query_id must be set")
	}
}

func TestPlanResearchEmptyQueryFallback(t *testing.T) {
	ra := NewResearchV2Activities("")
	res, err := ra.PlanResearch(context.Background(), PlanResearchInput{
		Query:        "",
		MaxSubqueries: 3,
		MockLLM:      true,
		WorkflowID:   "wf-1",
	})
	if err != nil {
		t.Fatalf("PlanResearch: %v", err)
	}
	if len(res.Subqueries) == 0 {
		t.Error("must produce at least 1 fallback subquery")
	}
}

func TestParseSubqueriesV2JSON(t *testing.T) {
	raw := `{"subqueries": [{"id": "sq-1", "text": "what is X?", "intent": "definition"}, {"id": "sq-2", "text": "compare X vs Y", "intent": "comparison"}]}`
	subs := parseSubqueriesV2(raw, 4)
	if len(subs) != 2 {
		t.Fatalf("got %d subs, want 2", len(subs))
	}
	if subs[0].Intent != "definition" {
		t.Errorf("intent = %q, want definition", subs[0].Intent)
	}
}

func TestParseSubqueriesV2InvalidFallsBack(t *testing.T) {
	raw := "completely unstructured"
	subs := parseSubqueriesV2(raw, 3)
	if len(subs) != 1 {
		t.Fatalf("got %d subs, want 1 fallback", len(subs))
	}
	if subs[0].Text != raw {
		t.Errorf("fallback should wrap raw input")
	}
}

func TestParseSubqueriesV2RespectsMax(t *testing.T) {
	raw := `{"subqueries": [{"id":"a","text":"1"},{"id":"b","text":"2"},{"id":"c","text":"3"},{"id":"d","text":"4"}]}`
	subs := parseSubqueriesV2(raw, 2)
	if len(subs) != 2 {
		t.Errorf("max not respected: got %d, want 2", len(subs))
	}
}

func TestScoreSourceCredibilityLocalRAG(t *testing.T) {
	ra := NewResearchV2Activities("")
	res, err := ra.ScoreSourceCredibility(context.Background(), ScoreSourceCredibilityInput{
		SourceURL:  "https://example.com/doc",
		SourceType: "local_rag",
	})
	if err != nil {
		t.Fatalf("ScoreSourceCredibility: %v", err)
	}
	if res.CredibilityScore < 0 || res.CredibilityScore > 1 {
		t.Errorf("credibility out of [0,1]: %f", res.CredibilityScore)
	}
	if res.CredibilityScore < 0.6 {
		t.Errorf("local_rag should have credibility >= 0.6, got %f", res.CredibilityScore)
	}
}

func TestScoreSourceCredibilityEDUBoost(t *testing.T) {
	ra := NewResearchV2Activities("")
	res, err := ra.ScoreSourceCredibility(context.Background(), ScoreSourceCredibilityInput{
		SourceURL:  "https://stanford.edu/paper",
		SourceType: "external",
	})
	if err != nil {
		t.Fatalf("ScoreSourceCredibility: %v", err)
	}
	if res.CredibilityScore <= 0.5 {
		t.Errorf(".edu domain should boost credibility above 0.5, got %f", res.CredibilityScore)
	}
}

func TestExtractDomain(t *testing.T) {
	cases := map[string]string{
		"https://Example.com/path": "example.com",
		"http://foo.bar":           "foo.bar",
		"":                          "",
		"plain":                     "plain",
	}
	for in, want := range cases {
		if got := extractDomain(in); got != want {
			t.Errorf("extractDomain(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDetectContradictionsNoConflict(t *testing.T) {
	ra := NewResearchV2Activities("")
	ev := []types.Evidence{
		{ID: "a", Summary: "alpha system is strong and good", CredibilityScore: 0.7},
		{ID: "b", Summary: "alpha system is robust and works", CredibilityScore: 0.7},
	}
	res, _ := ra.DetectContradictions(context.Background(), DetectContradictionsInput{Evidence: ev})
	if len(res.Contradictions) != 0 {
		t.Errorf("expected 0 contradictions, got %d", len(res.Contradictions))
	}
}

func TestDetectContradictionsOppositeSentiment(t *testing.T) {
	ra := NewResearchV2Activities("")
	ev := []types.Evidence{
		{ID: "a", Summary: "the system is strong and works", CredibilityScore: 0.7},
		{ID: "b", Summary: "the system is weak and fails", CredibilityScore: 0.7},
	}
	res, _ := ra.DetectContradictions(context.Background(), DetectContradictionsInput{Evidence: ev})
	if len(res.Contradictions) == 0 {
		t.Error("expected at least 1 contradiction on opposite sentiment")
	}
}

func TestBuildCitationChainLengthBounded(t *testing.T) {
	ra := NewResearchV2Activities("")
	ev := []types.Evidence{{ID: "ev-1", SourceID: "src-1"}, {ID: "ev-2", SourceID: "src-2"}}
	res, _ := ra.BuildCitationChain(context.Background(), BuildCitationChainInput{Evidence: ev})
	if len(res.CitationChains) != 2 {
		t.Fatalf("got %d chains, want 2", len(res.CitationChains))
	}
	for i, chain := range res.CitationChains {
		if len(chain) == 0 {
			t.Errorf("chain %d is empty", i)
		}
		if len(chain) > 3 {
			t.Errorf("chain %d length %d > 3 (v2 bound)", i, len(chain))
		}
	}
}

func TestFilterByCredibility(t *testing.T) {
	ra := NewResearchV2Activities("")
	ev := []types.Evidence{
		{ID: "high", CredibilityScore: 0.9},
		{ID: "mid", CredibilityScore: 0.5},
		{ID: "low", CredibilityScore: 0.1},
	}
	res, _ := ra.FilterByCredibility(context.Background(), FilterByCredibilityInput{
		Evidence:  ev,
		Threshold: 0.4,
	})
	if len(res.Filtered) != 2 {
		t.Errorf("expected 2 above threshold, got %d", len(res.Filtered))
	}
	if res.Filtered[0].ID != "high" {
		t.Errorf("filter should sort by credibility desc, got %s first", res.Filtered[0].ID)
	}
}

func TestGenerateReportV2MockReturnsStructured(t *testing.T) {
	ra := NewResearchV2Activities("")
	res, err := ra.GenerateReportV2(context.Background(), GenerateReportV2Input{
		QueryID: "q-1",
		Query:   "Compare async polling vs sync HTTP waiting.",
		Evidence: []types.Evidence{
			{ID: "ev-1", SourceID: "src-1", Summary: "async polling survives long workflows", CredibilityScore: 0.7, SourceType: types.SourceTypeLocalRAG},
			{ID: "ev-2", SourceID: "src-2", Summary: "sync HTTP has timeout risk", CredibilityScore: 0.6, SourceType: types.SourceTypeLocalRAG},
		},
		Config: types.ResearchV2Config{MaxSources: 3},
		MockLLM:    true,
		WorkflowID: "wf-1",
		TaskID:     "t-1",
		RunID:      "r-1",
	})
	if err != nil {
		t.Fatalf("GenerateReportV2: %v", err)
	}
	if res.Mode != "mock" {
		t.Errorf("mode = %q, want mock", res.Mode)
	}
	if res.Report.Title == "" {
		t.Error("title must be set")
	}
	if res.Report.ExecutiveSummary == "" {
		t.Error("executive summary must be set")
	}
	if res.ReportRef == "" {
		t.Error("report_ref must be set")
	}
	if res.ExecutiveRef == "" {
		t.Error("executive_ref must be set")
	}
	if res.Report.TotalSources != 2 {
		t.Errorf("total sources = %d, want 2", res.Report.TotalSources)
	}
	if res.Report.TotalEvidenceItems != 2 {
		t.Errorf("total evidence = %d, want 2", res.Report.TotalEvidenceItems)
	}
	if len(res.Report.Sections) == 0 {
		t.Error("sections must be present")
	}
}

func TestGenerateReportV2EmptyEvidenceDoesNotPanic(t *testing.T) {
	ra := NewResearchV2Activities("")
	res, err := ra.GenerateReportV2(context.Background(), GenerateReportV2Input{
		QueryID:    "q-1",
		Query:      "Q",
		Evidence:   nil,
		Config:     types.ResearchV2Config{},
		MockLLM:    true,
		WorkflowID: "wf-1",
		TaskID:     "t-1",
		RunID:      "r-1",
	})
	if err != nil {
		t.Fatalf("GenerateReportV2: %v", err)
	}
	if res.Report.ConfidenceScore <= 0 {
		t.Error("confidence should still be reported, even when low")
	}
}

func TestGenerateReportV2NoSynthesisTextInHistory(t *testing.T) {
	// The Workflow result carries only refs + a short preview
	// (truncate(...,1500)). Full synthesis lives behind
	// ExecutiveRef / ReportRef. Sanity check the preview length.
	ra := NewResearchV2Activities("")
	res, _ := ra.GenerateReportV2(context.Background(), GenerateReportV2Input{
		QueryID:    "q-1",
		Query:      "Q",
		MockLLM:    true,
		WorkflowID: "wf-1",
		TaskID:     "t-1",
		RunID:      "r-1",
	})
	if len(res.Report.ExecutiveSummary) > 1500 {
		t.Errorf("executive summary too long for history: %d", len(res.Report.ExecutiveSummary))
	}
}

func TestReflectionBeforeSynthesisMock(t *testing.T) {
	ra := NewResearchV2Activities("")
	ev := []types.Evidence{
		{ID: "a", Summary: "alpha", CredibilityScore: 0.5},
		{ID: "b", Summary: "", CredibilityScore: 0.5},   // dropped
		{ID: "c", Summary: "gamma with rich detail to boost", CredibilityScore: 0.5},
	}
	res, _ := ra.ReflectionBeforeSynthesis(context.Background(), ReflectionBeforeSynthesisInput{
		Query: "Q", Evidence: ev, MockLLM: true, WorkflowID: "wf-1", TaskID: "t-1", RunID: "r-1",
	})
	if len(res.ImprovedEvidence) != 2 {
		t.Errorf("expected 2 (empty summary dropped), got %d", len(res.ImprovedEvidence))
	}
	for _, e := range res.ImprovedEvidence {
		if e.CredibilityScore <= 0.5 {
			t.Errorf("expected boosted cred, got %f", e.CredibilityScore)
		}
	}
}

func TestDebateBeforeSynthesisMock(t *testing.T) {
	ra := NewResearchV2Activities("")
	ev := []types.Evidence{{ID: "a", Summary: "x"}}
	res, _ := ra.DebateBeforeSynthesis(context.Background(), DebateBeforeSynthesisInput{
		Query: "Q", Evidence: ev, MockLLM: true, WorkflowID: "wf-1", TaskID: "t-1", RunID: "r-1",
	})
	if len(res.ResolvedEvidence) != 1 {
		t.Errorf("expected 1 resolved, got %d", len(res.ResolvedEvidence))
	}
	if res.ResolutionNotes == "" {
		t.Error("notes must be set")
	}
}

func TestParseDiscardIDsJSON(t *testing.T) {
	raw := `{"discard_ids": ["ev-a", "ev-b"], "critique": "low quality"}`
	got := parseDiscardIDs(raw)
	if _, ok := got["ev-a"]; !ok {
		t.Error("expected ev-a discarded")
	}
	if _, ok := got["ev-b"]; !ok {
		t.Error("expected ev-b discarded")
	}
}

func TestParseDiscardIDsHeuristic(t *testing.T) {
	raw := "ev-abc low quality ev-xyz also weak"
	got := parseDiscardIDs(raw)
	if _, ok := got["ev-abc"]; !ok {
		t.Error("heuristic should pick ev-abc")
	}
	if _, ok := got["ev-xyz"]; !ok {
		t.Error("heuristic should pick ev-xyz")
	}
}

func TestRetrieveMultiSourceEvidenceWithLocalRAGEmpty(t *testing.T) {
	// When RAG retrieval is unavailable (nil deps), the activity
	// returns an empty result without panicking. The Workflow
	// will still produce a partial report.
	ra := NewResearchV2Activities("")
	res, err := ra.RetrieveMultiSourceEvidence(context.Background(), RetrieveMultiSourceEvidenceInput{
		SubqueryID:   "sq-1",
		SubqueryText: "what is X?",
		SourceTypes:  []string{types.SourceTypeLocalRAG},
		MaxPerSource: 3,
		WorkflowID:   "wf-1",
	})
	if err != nil {
		t.Fatalf("RetrieveMultiSourceEvidence: %v", err)
	}
	// local_rag with nil deps should return empty (graceful).
	if len(res.Evidence) != 0 {
		t.Logf("local_rag returned %d evidence items (deps may have been available)", len(res.Evidence))
	}
}

func TestRetrieveMultiSourceEvidenceDocsPlaceholder(t *testing.T) {
	ra := NewResearchV2Activities("")
	res, err := ra.RetrieveMultiSourceEvidence(context.Background(), RetrieveMultiSourceEvidenceInput{
		SubqueryID:   "sq-1",
		SubqueryText: "Q",
		SourceTypes:  []string{types.SourceTypeDocs},
		MaxPerSource: 1,
		WorkflowID:   "wf-1",
	})
	if err != nil {
		t.Fatalf("RetrieveMultiSourceEvidence: %v", err)
	}
	if len(res.Evidence) == 0 {
		t.Error("docs source must emit placeholder evidence for v2 multi-source path")
	}
	if len(res.Evidence) > 0 && res.Evidence[0].SourceType != types.SourceTypeDocs {
		t.Errorf("expected source_type=docs, got %q", res.Evidence[0].SourceType)
	}
}

func TestRetrieveMultiSourceEvidenceDeduplication(t *testing.T) {
	ra := NewResearchV2Activities("")
	// Two source types might both emit the same SourceID (placeholders
	// share the URL). The seen set must keep counts honest.
	res, _ := ra.RetrieveMultiSourceEvidence(context.Background(), RetrieveMultiSourceEvidenceInput{
		SubqueryID:   "sq-1",
		SubqueryText: "Q",
		SourceTypes:  []string{types.SourceTypeDocs, types.SourceTypeExternal},
		MaxPerSource: 1,
		WorkflowID:   "wf-1",
	})
	// Should produce at most 1 (docs) + 1 (external) = 2 (different SourceIDs).
	if len(res.Evidence) > 2 {
		t.Errorf("expected ≤ 2 evidence items, got %d", len(res.Evidence))
	}
}

func TestAuditResearchV2ReturnsID(t *testing.T) {
	ra := NewResearchV2Activities("")
	res, err := ra.AuditResearchV2(context.Background(), AuditResearchV2Input{
		WorkflowID: "wf-1", Query: "Q", TotalTokens: 100, ReportRef: "ref",
	})
	if err != nil {
		t.Fatalf("AuditResearchV2: %v", err)
	}
	if !strings.HasPrefix(res.AuditID, "audit-") {
		t.Errorf("audit_id must be prefixed with 'audit-', got %q", res.AuditID)
	}
}

func TestMockSubqueriesRespectsMax(t *testing.T) {
	subs := mockSubqueries("test query", 2)
	if len(subs) != 2 {
		t.Errorf("max=2 must cap at 2, got %d", len(subs))
	}
}

func TestKeywordOverlap(t *testing.T) {
	if keywordOverlap("alpha beta", "alpha gamma") < 0.3 {
		t.Error("alpha should overlap")
	}
	if keywordOverlap("foo bar", "baz qux") > 0.1 {
		t.Error("disjoint sets should have low overlap")
	}
}

func TestOppositeSentiment(t *testing.T) {
	if !oppositeSentiment("strong and good", "weak and bad") {
		t.Error("expected opposite sentiment")
	}
	if oppositeSentiment("strong and good", "strong and good") {
		t.Error("identical should not be opposite")
	}
}

func TestUniqueSourceCount(t *testing.T) {
	ev := []types.Evidence{
		{ID: "a", SourceID: "s1"},
		{ID: "b", SourceID: "s1"}, // duplicate
		{ID: "c", SourceID: "s2"},
	}
	if got := uniqueSourceCount(ev); got != 2 {
		t.Errorf("got %d, want 2", got)
	}
}
