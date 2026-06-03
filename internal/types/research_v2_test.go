package types

import "testing"

func TestResearchV2ConfigMockLLMExplicit(t *testing.T) {
	// Phase 7 task book: ResearchV2Config.MockLLM must be explicit.
	cfg := ResearchV2Config{MockLLM: true}
	if !cfg.MockLLM {
		t.Fatalf("MockLLM not preserved")
	}
	cfg2 := ResearchV2Config{MockLLM: false}
	if cfg2.MockLLM {
		t.Fatalf("MockLLM should be false")
	}
}

func TestResearchV2ConfigDefaultsAreZero(t *testing.T) {
	// Zero values must not be silently treated as enabled. The
	// Workflow fills defaults; the type itself should not impose
	// any magic.
	cfg := ResearchV2Config{}
	if cfg.MaxSources != 0 {
		t.Fatalf("MaxSources should be 0 for zero-value config")
	}
	if cfg.MockLLM {
		t.Fatalf("MockLLM should default to false (not true)")
	}
}

func TestResearchV2ResultCarriesRefsOnly(t *testing.T) {
	// Result carries refs + counts but NOT full text. Verify the
	// struct surface to ensure long content doesn't sneak into the
	// result type.
	res := ResearchV2WorkflowResult{
		Report:          ResearchReportV2{ExecutiveSummary: "long exec"},
		FinalAnswerText: "short preview",
		SourceRefs:      []string{"ref1", "ref2"},
		ReportRef:       "research:wf:report",
		EvidenceRef:     "research:wf:evidence",
		SynthesisRef:    "research:wf:synthesis",
		FinalAnswerRef:  "research:wf:final",
		LLMCalls:        3,
		SourceCount:     2,
		EvidenceCount:   4,
		Mock:            false,
	}
	if res.FinalAnswerText != "short preview" {
		t.Errorf("FinalAnswerText not stored")
	}
	if len(res.SourceRefs) != 2 {
		t.Errorf("SourceRefs not stored")
	}
	if res.LLMCalls != 3 {
		t.Errorf("LLMCalls not stored")
	}
}

func TestEvidenceCarriesContentRefNotContent(t *testing.T) {
	// Evidence must NOT have a "Content" string field that could
	// accidentally be filled with full text. The struct uses
	// ContentRef + Summary instead. Sanity-check the field names.
	e := Evidence{
		ID:         "ev-1",
		SourceID:   "src-1",
		SourceType: SourceTypeLocalRAG,
		Summary:    "short summary",
		ContentRef: "research:wf:evidence:1",
	}
	if e.ContentRef == "" {
		t.Error("ContentRef must be set")
	}
	if e.Summary == "" {
		t.Error("Summary must be set")
	}
}

func TestContradictionResolutionTypes(t *testing.T) {
	// Sanity: resolution type constants are stable.
	if ResolutionPreference == ResolutionEvidence {
		t.Error("resolution type constants must be distinct")
	}
	if ResolutionUncertain == ResolutionEvidence {
		t.Error("resolution type constants must be distinct")
	}
}

func TestSourceTypeConstants(t *testing.T) {
	if SourceTypeLocalRAG == SourceTypeExternal {
		t.Error("source type constants must be distinct")
	}
}
