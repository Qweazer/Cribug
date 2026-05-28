package types

// ResearchQuery represents a research query with its subqueries.
type ResearchQuery struct {
	ID         string             `json:"id"`
	Query      string             `json:"query"`
	AgentID    string             `json:"agent_id"`
	WorkflowID string             `json:"workflow_id"`
	Status     string             `json:"status"`
	Subqueries []ResearchSubquery `json:"subqueries,omitempty"`
}

// ResearchSubquery represents a single sub-query within a research query.
type ResearchSubquery struct {
	ID          string `json:"id"`
	ParentID    string `json:"parent_id"`
	Text        string `json:"text"`
	Status      string `json:"status"`
	EvidenceRef string `json:"evidence_ref,omitempty"`
}

// ResearchEvidence holds the evidence retrieved for a subquery.
type ResearchEvidence struct {
	SubqueryID string                 `json:"subquery_id"`
	ChunkIDs   []string               `json:"chunk_ids"`
	Summary    string                 `json:"summary"`
	Score      float64                `json:"score"`
	Metadata   map[string]interface{} `json:"metadata"`
}

// EvidenceReference describes evidence found for a subquery.
type EvidenceReference struct {
	SubqueryID string  `json:"subquery_id"`
	ChunkCount int     `json:"chunk_count"`
	TopScore   float64 `json:"top_score"`
	Summary    string  `json:"summary"`
}

// SubqueryAnswer pairs a subquery with its answer and confidence.
type SubqueryAnswer struct {
	SubqueryID string  `json:"subquery_id"`
	Answer     string  `json:"answer"`
	Confidence float64 `json:"confidence"`
}

// ResearchSynthesisResult is the final output of a research-synthesis workflow.
type ResearchSynthesisResult struct {
	QueryID         string              `json:"query_id"`
	Answer          string              `json:"answer"`
	Evidence        []EvidenceReference `json:"evidence"`
	SubqueryAnswers []SubqueryAnswer    `json:"subquery_answers"`
	TokenCount      int                 `json:"token_count"`
	Error           string              `json:"error,omitempty"`
	Partial         bool                `json:"partial"`
}
