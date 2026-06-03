package types

import "time"

// ─── Research v2 Configuration ─────────────────────────────────────────

// ResearchV2Config controls Research-Synthesis v2 (Phase 7F Slice 28).
//
// Constraints (per Phase 7 task book §7.2 / §7.7):
//   - Built on Phase 6F Research-Synthesis v1 (not a parallel implementation).
//   - Multi-source retrieval (v1 was single collection).
//   - Evidence with credibility scoring + contradiction detection + citation chain.
//   - Optional reflection / debate before final synthesis.
//   - Optional approval before publish.
//   - Long content (raw sources / evidence dumps / synthesis drafts) MUST
//     go via WorkspaceRef; result only carries refs + short metadata.
type ResearchV2Config struct {
	MaxSources                    int      `json:"max_sources"`                       // default 5
	MaxEvidenceItems              int      `json:"max_evidence_items"`                // default 20
	MaxSubqueries                 int      `json:"max_subqueries"`                    // default 4
	MaxIterations                 int      `json:"max_iterations"`                    // default 2 (re-plan loop)
	TokenBudget                   int      `json:"token_budget"`                      // default 6000
	CredibilityThreshold          float64  `json:"credibility_threshold"`             // default 0.4
	EnableContradictionDetection  bool     `json:"enable_contradiction_detection"`    // default true
	EnableReflection              bool     `json:"enable_reflection"`                 // default false (Slice 25)
	EnableDebate                  bool     `json:"enable_debate"`                     // default false (Slice 27)
	RequireCitations              bool     `json:"require_citations"`                 // default true
	RequireApprovalBeforePublish  bool     `json:"require_approval_before_publish"`  // default false
	SourceTypes                   []string `json:"source_types"`                      // e.g. ["local_rag", "docs"]; default ["local_rag"]
	ModelTier                     string   `json:"model_tier"`                        // "small" | "medium"
	MockLLM                       bool     `json:"mock_llm"`                          // explicit field; default true for CI
}

// ─── Source / Evidence Metadata ────────────────────────────────────────

// SourceCredibility scores a single source's reliability. Computed by
// ScoreSourceCredibilityActivity (Activity) — Workflow only carries the
// resulting values around via Evidence.
type SourceCredibility struct {
	SourceType      string  `json:"source_type"`       // "local_rag" | "docs" | "external"
	Domain          string  `json:"domain,omitempty"`
	CredibilityScore float64 `json:"credibility_score"` // 0.0 - 1.0
	QualityScore    float64  `json:"quality_score"`     // 0.0 - 1.0
}

// Evidence is a single piece of evidence with provenance metadata.
// Workflow history stores only this metadata; full content is via
// ContentRef in Workspace.
type Evidence struct {
	ID                string            `json:"id"`
	SubqueryID        string            `json:"subquery_id"`
	SourceID          string            `json:"source_id"`            // collection / doc id
	SourceType        string            `json:"source_type"`
	SourceCredibility SourceCredibility `json:"source_credibility"`
	Summary           string            `json:"summary"`              // short summary (≤500 chars)
	ContentRef        string            `json:"content_ref"`          // full content → Workspace
	CredibilityScore  float64           `json:"credibility_score"`    // per-evidence score
	IsContradiction   bool              `json:"is_contradiction"`
	ContradictsWith   []string          `json:"contradicts_with,omitempty"`
	CitationChain     []string          `json:"citation_chain,omitempty"`     // source_id list
	RetrievedAt       time.Time         `json:"retrieved_at"`
	Metadata          map[string]interface{} `json:"metadata,omitempty"`
}

// Contradiction is a detected conflict between evidence items.
type Contradiction struct {
	ID            string   `json:"id"`
	EvidenceIDs   []string `json:"evidence_ids"`
	Resolution    string   `json:"resolution,omitempty"`
	ResolutionType string  `json:"resolution_type,omitempty"` // "preference" | "evidence_based" | "uncertain"
}

// CitationStats summarises the citation graph (v2 enhancement over v1).
type CitationStats struct {
	UniqueSources     int     `json:"unique_sources"`
	UniqueDomains    int     `json:"unique_domains"`
	AverageCredibility float64 `json:"average_credibility"`
}

// ResearchSubqueryV2 is the v2 subquery (extends v1 with mode hints).
type ResearchSubqueryV2 struct {
	ID     string `json:"id"`
	Text   string `json:"text"`
	Intent string `json:"intent,omitempty"` // optional: "definition" | "comparison" | "evidence"
}

// ReportSection is a section of the final report with content ref.
type ReportSection struct {
	Title          string   `json:"title"`
	ContentSummary string   `json:"content_summary"` // ≤500 chars preview
	ContentRef     string   `json:"content_ref"`     // full content → Workspace
	Evidence       []string `json:"evidence"`        // evidence IDs cited
}

// ResearchReportV2 is the structured report object. Carries refs +
// short summaries, NOT full text.
type ResearchReportV2 struct {
	QueryID              string           `json:"query_id"`
	Title                string           `json:"title"`
	ExecutiveSummary     string           `json:"executive_summary"`               // short (≤2KB)
	ExecutiveSummaryRef  string           `json:"executive_summary_ref"`            // full summary → Workspace
	Sections             []ReportSection  `json:"sections"`
	AllEvidence          []Evidence       `json:"all_evidence"`                     // evidence metadata only
	Contradictions       []Contradiction  `json:"contradictions,omitempty"`
	ContradictionReportRef string         `json:"contradiction_report_ref,omitempty"` // full contradiction analysis → Workspace
	CitationStats        CitationStats    `json:"citation_stats"`
	ConfidenceScore      float64          `json:"confidence_score"`
	TotalSources         int              `json:"total_sources"`
	TotalEvidenceItems   int              `json:"total_evidence_items"`
	ReportRef            string           `json:"report_ref"`                        // full report → Workspace
	CreatedAt            time.Time        `json:"created_at"`
	Approved             *bool            `json:"approved,omitempty"`
}

// ResearchV2WorkflowInput is the Workflow input.
type ResearchV2WorkflowInput struct {
	TaskID     string                 `json:"task_id"`
	WorkflowID string                 `json:"workflow_id"`
	RunID      string                 `json:"run_id"`
	SessionID  string                 `json:"session_id"`
	Query      string                 `json:"query"`
	Context    map[string]interface{} `json:"context,omitempty"`
	Config     ResearchV2Config        `json:"config"`
}

// ResearchV2WorkflowResult is the result returned from the Workflow.
// Workflow history stores only refs + metadata.
type ResearchV2WorkflowResult struct {
	Report            ResearchReportV2 `json:"report"`
	ReportRef         string          `json:"report_ref"`
	SourceRefs        []string        `json:"source_refs"`         // per-source Workspace refs
	EvidenceRef       string          `json:"evidence_ref"`        // consolidated evidence table ref
	SynthesisRef      string          `json:"synthesis_ref"`       // synthesis draft ref
	FinalAnswerRef    string          `json:"final_answer_ref"`    // final answer ref
	FinalAnswerText   string          `json:"final_answer_text"`   // short preview (≤2KB)
	ContradictionRef  string          `json:"contradiction_ref,omitempty"`
	WorkspaceTopic    string          `json:"workspace_topic"`
	TotalTokens       int             `json:"total_tokens"`
	Iterations        int             `json:"iterations"`
	// LLM metadata for real-only assertions (Phase 7E.6 polish)
	Provider         string `json:"provider,omitempty"`
	ModelUsed        string `json:"model_used,omitempty"`
	Mode             string `json:"mode,omitempty"`
	Mock             bool   `json:"mock"`
	FallbackUsed     bool   `json:"fallback_used,omitempty"`
	LLMCalls         int    `json:"llm_calls"`
	SourceCount      int    `json:"source_count"`
	EvidenceCount    int    `json:"evidence_count"`
	ContradictionCount int  `json:"contradiction_count"`
}

const (
	// Source type tags for v2 multi-source retrieval.
	SourceTypeLocalRAG = "local_rag"
	SourceTypeDocs     = "docs"
	SourceTypeExternal = "external"

	// Resolution types for contradictions.
	ResolutionPreference  = "preference"
	ResolutionEvidence   = "evidence_based"
	ResolutionUncertain  = "uncertain"
)
