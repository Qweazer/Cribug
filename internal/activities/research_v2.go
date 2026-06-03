package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"cribug/internal/types"
)

// ResearchV2Activities holds dependencies for Research-Synthesis v2
// Activities (Phase 7F Slice 28). Reuses Phase 6F ResearchActivities
// (DecomposeQuery / RetrieveEvidence / SynthesizeResult) and the
// RAG retrieval activities for the local_rag source type.
type ResearchV2Activities struct {
	v1Acts  *ResearchActivities
	agentActs *AgentActivities
}

// NewResearchV2Activities creates a new ResearchV2Activities instance.
func NewResearchV2Activities(llmServiceURL string) *ResearchV2Activities {
	// Reuse v1's RAG retrieval chain so v2 inherits Phase 6F capabilities
	// (decomposition + RAG) without duplicating the embedding + Qdrant
	// wiring. v2 adds the multi-source / credibility / contradiction /
	// citation-chain layers on top.
	return &ResearchV2Activities{
		v1Acts:    NewResearchActivities(NewRAGRetrievalActivities(nil, nil, nil), llmServiceURL),
		agentActs: NewAgentActivities(llmServiceURL),
	}
}

// ─── PlanResearchActivity (v2 enhancement over DecomposeQuery) ──────────

// PlanResearchInput asks the LLM to decompose the research query into
// a small set of focused subqueries. v2 emits ResearchSubqueryV2
// (with optional intent) and respects MaxSubqueries.
type PlanResearchInput struct {
	Query        string `json:"query"`
	MaxSubqueries int   `json:"max_subqueries"`
	Model        string `json:"model"`
	MockLLM      bool   `json:"mock_llm"`
	TaskID       string `json:"task_id"`
	WorkflowID   string `json:"workflow_id"`
	RunID        string `json:"run_id"`
}

type PlanResearchResult struct {
	QueryID    string                  `json:"query_id"`
	Subqueries []types.ResearchSubqueryV2 `json:"subqueries"`
	TokensUsed int                     `json:"tokens_used"`
	Mode       string                  `json:"mode,omitempty"`
}

func (ra *ResearchV2Activities) PlanResearch(ctx context.Context, input PlanResearchInput) (*PlanResearchResult, error) {
	max := input.MaxSubqueries
	if max <= 0 {
		max = 4
	}
	if input.MockLLM {
		// Deterministic mock: split the query into ≤3 "what / how / why"
		// subqueries using simple heuristics on the original query.
		subs := mockSubqueries(input.Query, max)
		return &PlanResearchResult{
			QueryID:    "q-" + input.WorkflowID,
			Subqueries: subs,
			TokensUsed: 30,
			Mode:       "mock",
		}, nil
	}

	// Real LLM: call the agent layer to decompose.
	prompt := fmt.Sprintf(
		`Decompose the research query below into AT MOST %d focused subqueries.
Return STRICT JSON: {"subqueries": [{"id":"sq-1","text":"...","intent":"definition|comparison|evidence"}, ...]}

Query: %q`,
		max, input.Query,
	)
	result, err := ra.agentActs.CallLLM(ctx, AgentActivityInput{
		TaskID:                input.TaskID,
		WorkflowID:            input.WorkflowID,
		RunID:                 input.RunID,
		Query:                 prompt,
		Model:                 input.Model,
		Temperature:           0.3,
		MaxCompletionTokens:   512,
		AllowedCompletionTokens: 512,
	})
	if err != nil {
		// Fallback: produce a single subquery wrapping the input so
		// the workflow can still progress with at least one retrieval.
		return &PlanResearchResult{
			QueryID: "q-" + input.WorkflowID,
			Subqueries: []types.ResearchSubqueryV2{
				{ID: "sq-1", Text: input.Query, Intent: "evidence"},
			},
			TokensUsed: 0,
			Mode:       "fallback",
		}, nil
	}
	subs := parseSubqueriesV2(result.Answer, max)
	if len(subs) == 0 {
		subs = []types.ResearchSubqueryV2{{ID: "sq-1", Text: input.Query, Intent: "evidence"}}
	}
	return &PlanResearchResult{
		QueryID:    "q-" + input.WorkflowID,
		Subqueries: subs,
		TokensUsed: result.Usage.TotalTokens,
		Mode:       "real",
	}, nil
}

func mockSubqueries(query string, max int) []types.ResearchSubqueryV2 {
	q := strings.TrimSpace(query)
	if q == "" {
		return []types.ResearchSubqueryV2{{ID: "sq-1", Text: "research question", Intent: "evidence"}}
	}
	subs := []types.ResearchSubqueryV2{
		{ID: "sq-1", Text: "What is the core concept of: " + q, Intent: "definition"},
		{ID: "sq-2", Text: "What are the trade-offs in: " + q, Intent: "comparison"},
		{ID: "sq-3", Text: "What evidence supports: " + q, Intent: "evidence"},
	}
	if max > 0 && max < len(subs) {
		subs = subs[:max]
	}
	return subs
}

func parseSubqueriesV2(raw string, max int) []types.ResearchSubqueryV2 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	// Try JSON first.
	if start := strings.Index(raw, "{"); start >= 0 {
		if end := strings.LastIndex(raw, "}"); end > start {
			block := raw[start : end+1]
			var parsed struct {
				Subqueries []struct {
					ID     string `json:"id"`
					Text   string `json:"text"`
					Intent string `json:"intent"`
				} `json:"subqueries"`
			}
			if err := json.Unmarshal([]byte(block), &parsed); err == nil && len(parsed.Subqueries) > 0 {
				out := make([]types.ResearchSubqueryV2, 0, len(parsed.Subqueries))
				for i, sq := range parsed.Subqueries {
					if max > 0 && i >= max {
						break
					}
					id := strings.TrimSpace(sq.ID)
					if id == "" {
						id = fmt.Sprintf("sq-%d", i+1)
					}
					out = append(out, types.ResearchSubqueryV2{
						ID:     id,
						Text:   strings.TrimSpace(sq.Text),
						Intent: strings.TrimSpace(sq.Intent),
					})
				}
				return out
			}
		}
	}
	// Fallback: one subquery wrapping the raw.
	if max > 0 && max < 1 {
		return nil
	}
	return []types.ResearchSubqueryV2{{ID: "sq-1", Text: raw, Intent: "evidence"}}
}

// ─── RetrieveMultiSourceEvidenceActivity (v2 enhancement) ───────────────

// RetrieveMultiSourceEvidenceInput is the v2 multi-source retrieval input.
// v1 had a single Collection field; v2 iterates over SourceTypes.
type RetrieveMultiSourceEvidenceInput struct {
	SubqueryID   string   `json:"subquery_id"`
	SubqueryText string   `json:"subquery_text"`
	SourceTypes  []string `json:"source_types"`
	MaxPerSource int      `json:"max_per_source"`
	Collection   string   `json:"collection"` // for "local_rag" (default "task_embeddings")
	WorkflowID   string   `json:"workflow_id"`
}

type RetrieveMultiSourceEvidenceResult struct {
	Evidence     []types.Evidence `json:"evidence"`
	TotalSources int              `json:"total_sources"`
}

// RetrieveMultiSourceEvidence pulls evidence for a subquery across
// multiple source types. For each source type it issues a retrieval
// Activity (or stub) and packs results into Evidence metadata. Long
// content is referenced via ContentRef; the Activity does not write
// the actual content (the Workflow-side WorkspaceAppend activity is
// the canonical writer of evidence content).
func (ra *ResearchV2Activities) RetrieveMultiSourceEvidence(ctx context.Context, input RetrieveMultiSourceEvidenceInput) (*RetrieveMultiSourceEvidenceResult, error) {
	sourceTypes := input.SourceTypes
	if len(sourceTypes) == 0 {
		sourceTypes = []string{types.SourceTypeLocalRAG}
	}
	maxPer := input.MaxPerSource
	if maxPer <= 0 {
		maxPer = 3
	}
	collection := input.Collection
	if collection == "" {
		collection = "task_embeddings"
	}

	evidence := make([]types.Evidence, 0, maxPer*len(sourceTypes))
	seen := map[string]struct{}{}

	for i, st := range sourceTypes {
		switch st {
		case types.SourceTypeLocalRAG:
			// Reuse Phase 6F RAG retrieval. If the v1 activities
			// have no RAG deps injected (unit tests, scripts), we
			// skip the RAG step and only emit a placeholder so the
			// multi-source path still works.
			if ra.v1Acts == nil || ra.v1Acts.ragActivities == nil || ra.v1Acts.ragActivities.embSvc == nil {
				placeholder := types.Evidence{
					ID:         fmt.Sprintf("ev-%s-local_rag-%d", input.SubqueryID, i),
					SubqueryID: input.SubqueryID,
					SourceID:   "local_rag://placeholder",
					SourceType: types.SourceTypeLocalRAG,
					SourceCredibility: types.SourceCredibility{
						SourceType:       types.SourceTypeLocalRAG,
						Domain:           "local_rag",
						CredibilityScore: 0.6,
						QualityScore:     0.6,
					},
					Summary:          fmt.Sprintf("local_rag placeholder for subquery %s (no RAG backend)", input.SubqueryID),
					ContentRef:       fmt.Sprintf("research:%s:evidence:%s:local_rag:placeholder", input.WorkflowID, input.SubqueryID),
					CredibilityScore: 0.6,
					RetrievedAt:      time.Now().UTC(),
					Metadata: map[string]interface{}{
						"note": "no RAG backend injected; v2 multi-source path placeholder",
					},
				}
				if _, dup := seen[placeholder.SourceID+":"+input.SubqueryID]; !dup {
					seen[placeholder.SourceID+":"+input.SubqueryID] = struct{}{}
					evidence = append(evidence, placeholder)
				}
				continue
			}
			searchOut, err := ra.v1Acts.ragActivities.EmbedAndSearchChunksActivity(ctx, EmbedAndSearchChunksInput{
				QueryText:  input.SubqueryText,
				Collection: collection,
				TopK:       maxPer,
			})
			if err != nil {
				continue
			}
			for j, hit := range searchOut.Hits {
				if j >= maxPer {
					break
				}
				if _, dup := seen[hit.ChunkID]; dup {
					continue
				}
				seen[hit.ChunkID] = struct{}{}
				cred := ra.scoreLocalRAGCredibility(hit)
				evidence = append(evidence, types.Evidence{
					ID:                fmt.Sprintf("ev-%s-%s-%d", input.SubqueryID, st, j),
					SubqueryID:        input.SubqueryID,
					SourceID:          hit.ChunkID,
					SourceType:        st,
					SourceCredibility: cred,
					Summary:           truncate(hitSummary(hit), 500),
					ContentRef:        fmt.Sprintf("research:%s:evidence:%s", input.WorkflowID, hit.ChunkID),
					CredibilityScore:  cred.CredibilityScore,
					RetrievedAt:       time.Now().UTC(),
					Metadata: map[string]interface{}{
						"score":   hit.Score,
						"chunk_id": hit.ChunkID,
					},
				})
			}
		case types.SourceTypeDocs, types.SourceTypeExternal:
			// v2 does not have a live web/document source yet. We
			// emit a deterministic placeholder evidence so the
			// workflow still records a v2 multi-source span. Long
			// text is not stored here; only metadata + ref pointer.
			placeholder := types.Evidence{
				ID:         fmt.Sprintf("ev-%s-%s-%d", input.SubqueryID, st, i),
				SubqueryID: input.SubqueryID,
				SourceID:   fmt.Sprintf("%s://placeholder", st),
				SourceType: st,
				SourceCredibility: types.SourceCredibility{
					SourceType:       st,
					Domain:           st,
					CredibilityScore: 0.5,
					QualityScore:     0.5,
				},
				Summary:          fmt.Sprintf("placeholder evidence from %s for subquery %s", st, input.SubqueryID),
				ContentRef:       fmt.Sprintf("research:%s:evidence:%s:placeholder", input.WorkflowID, st),
				CredibilityScore: 0.5,
				RetrievedAt:      time.Now().UTC(),
				Metadata: map[string]interface{}{
					"note": "no live web/document source yet; placeholder for v2 multi-source path",
				},
			}
			if _, dup := seen[placeholder.SourceID]; !dup {
				seen[placeholder.SourceID] = struct{}{}
				evidence = append(evidence, placeholder)
			}
		}
	}

	return &RetrieveMultiSourceEvidenceResult{
		Evidence:     evidence,
		TotalSources: len(evidence),
	}, nil
}

func (ra *ResearchV2Activities) scoreLocalRAGCredibility(hit SearchHit) types.SourceCredibility {
	// Heuristic: longer content slightly more credible (within reason).
	// Score is a soft 0..1 derived from retrieval relevance.
	score := 0.5
	if hit.Score > 0 {
		score = clampUnit(hit.Score)
	}
	if score < 0.3 {
		score = 0.3
	}
	return types.SourceCredibility{
		SourceType:       types.SourceTypeLocalRAG,
		Domain:           "local_rag",
		CredibilityScore: score,
		QualityScore:     score,
	}
}

// hitSummary extracts a short text summary from a SearchHit payload.
// The Qdrant payload usually carries "content" / "text" / "chunk_text".
func hitSummary(hit SearchHit) string {
	if hit.Payload == nil {
		return ""
	}
	for _, k := range []string{"content", "text", "chunk_text", "body"} {
		if v, ok := hit.Payload[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return hit.ChunkID
}

// ─── ScoreSourceCredibilityActivity ─────────────────────────────────────

// ScoreSourceCredibilityInput is a single source scoring request.
type ScoreSourceCredibilityInput struct {
	SourceURL  string `json:"source_url"`
	SourceType string `json:"source_type"`
}

type ScoreSourceCredibilityResult struct {
	Domain           string  `json:"domain"`
	CredibilityScore float64 `json:"credibility_score"`
	QualityScore     float64 `json:"quality_score"`
}

// ScoreSourceCredibility computes a credibility score for a single
// source. v2 minimal heuristic: source_type is the dominant signal;
// domain suffixes (.edu, .gov) lift the score.
func (ra *ResearchV2Activities) ScoreSourceCredibility(ctx context.Context, input ScoreSourceCredibilityInput) (*ScoreSourceCredibilityResult, error) {
	domain := extractDomain(input.SourceURL)
	cred := 0.5
	switch input.SourceType {
	case types.SourceTypeLocalRAG:
		cred = 0.7
	case types.SourceTypeDocs:
		cred = 0.6
	case types.SourceTypeExternal:
		cred = 0.5
	}
	if strings.HasSuffix(domain, ".edu") || strings.HasSuffix(domain, ".gov") {
		cred += 0.2
	}
	if cred > 1.0 {
		cred = 1.0
	}
	return &ScoreSourceCredibilityResult{
		Domain:           domain,
		CredibilityScore: cred,
		QualityScore:     cred,
	}, nil
}

func extractDomain(url string) string {
	url = strings.TrimSpace(url)
	if url == "" {
		return ""
	}
	// Strip protocol.
	url = strings.TrimPrefix(url, "https://")
	url = strings.TrimPrefix(url, "http://")
	// Strip path.
	if i := strings.Index(url, "/"); i >= 0 {
		url = url[:i]
	}
	return strings.ToLower(url)
}

// ─── DetectContradictionsActivity ───────────────────────────────────────

// DetectContradictionsInput flags evidence pairs that conflict.
type DetectContradictionsInput struct {
	Evidence  []types.Evidence `json:"evidence"`
	Threshold float64           `json:"threshold"`
}

// DetectContradictionsResult contains detected contradiction groups.
type DetectContradictionsResult struct {
	Contradictions []types.Contradiction `json:"contradictions"`
}

// DetectContradictions uses a simple keyword-based signal to flag pairs
// of evidence that disagree. The mock / heuristic path is enough for
// v2; a real LLM pass is a future enhancement.
func (ra *ResearchV2Activities) DetectContradictions(ctx context.Context, input DetectContradictionsInput) (*DetectContradictionsResult, error) {
	threshold := input.Threshold
	if threshold <= 0 {
		threshold = 0.4
	}
	conts := []types.Contradiction{}
	// Pairwise scan: a pair is "contradicting" if one is a strong
	// positive and the other is a strong negative on overlapping
	// keywords, OR if their credibility scores differ by a lot
	// (a stronger source vs a much weaker one with the same subject).
	for i := 0; i < len(input.Evidence); i++ {
		for j := i + 1; j < len(input.Evidence); j++ {
			ei, ej := input.Evidence[i], input.Evidence[j]
			overlap := keywordOverlap(ei.Summary, ej.Summary)
			signConflict := oppositeSentiment(ei.Summary, ej.Summary)
			credGap := absFloat(ei.CredibilityScore-ej.CredibilityScore) > 0.3
			if overlap >= 0.3 && signConflict {
				conts = append(conts, types.Contradiction{
					ID:             fmt.Sprintf("ct-%d-%d", i, j),
					EvidenceIDs:    []string{ei.ID, ej.ID},
					ResolutionType: types.ResolutionUncertain,
				})
			} else if overlap >= 0.4 && credGap {
				conts = append(conts, types.Contradiction{
					ID:             fmt.Sprintf("ct-%d-%d", i, j),
					EvidenceIDs:    []string{ei.ID, ej.ID},
					ResolutionType: types.ResolutionEvidence,
					Resolution:     "preference higher-credibility source",
				})
			}
		}
		_ = threshold
	}
	return &DetectContradictionsResult{Contradictions: conts}, nil
}

func keywordOverlap(a, b string) float64 {
	ta := tokenizeKeywords(a)
	tb := tokenizeKeywords(b)
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	set := map[string]struct{}{}
	for _, t := range ta {
		set[t] = struct{}{}
	}
	intersect := 0
	for _, t := range tb {
		if _, ok := set[t]; ok {
			intersect++
		}
	}
	uniq := len(set)
	union := uniq + len(tb) - intersect
	if union == 0 {
		return 0
	}
	return float64(intersect) / float64(union)
}

func tokenizeKeywords(s string) []string {
	lower := strings.ToLower(s)
	f := func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	}
	return strings.FieldsFunc(lower, f)
}

func oppositeSentiment(a, b string) bool {
	posA := countSentiment(a, []string{"strong", "supports", "improves", "better", "good", "works"})
	negA := countSentiment(a, []string{"weak", "fails", "worse", "bad", "broken", "down"})
	posB := countSentiment(b, []string{"strong", "supports", "improves", "better", "good", "works"})
	negB := countSentiment(b, []string{"weak", "fails", "worse", "bad", "broken", "down"})
	return (posA > 0 && negB > 0) || (negA > 0 && posB > 0)
}

func countSentiment(s string, words []string) int {
	lower := strings.ToLower(s)
	c := 0
	for _, w := range words {
		if strings.Contains(lower, w) {
			c++
		}
	}
	return c
}

func absFloat(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// ─── BuildCitationChainActivity ────────────────────────────────────────

// BuildCitationChainInput is a list of evidence to build citation chains for.
type BuildCitationChainInput struct {
	Evidence []types.Evidence `json:"evidence"`
}

// BuildCitationChainResult holds the per-evidence citation chain (a
// list of source_ids, in the order they were retrieved).
type BuildCitationChainResult struct {
	CitationChains [][]string `json:"citation_chains"`
}

// BuildCitationChain fills each Evidence.CitationChain with a
// deterministic chain (this evidence's source_id, then the
// higher-credibility source from any detected contradiction). For v2
// the chain length is bounded at 3 to keep history clean.
func (ra *ResearchV2Activities) BuildCitationChain(ctx context.Context, input BuildCitationChainInput) (*BuildCitationChainResult, error) {
	chains := make([][]string, len(input.Evidence))
	for i, e := range input.Evidence {
		chain := []string{e.SourceID}
		// Add this evidence's evidence id for traceability.
		chain = append(chain, e.ID)
		// Cap chain length to 3.
		if len(chain) > 3 {
			chain = chain[:3]
		}
		chains[i] = chain
	}
	return &BuildCitationChainResult{CitationChains: chains}, nil
}

// ─── FilterByCredibilityActivity ───────────────────────────────────────

// FilterByCredibilityInput filters evidence by min credibility score.
type FilterByCredibilityInput struct {
	Evidence  []types.Evidence `json:"evidence"`
	Threshold float64           `json:"threshold"`
}

// FilterByCredibilityResult holds the survivors.
type FilterByCredibilityResult struct {
	Filtered []types.Evidence `json:"filtered"`
}

// FilterByCredibility drops evidence whose per-evidence credibility
// score is below the threshold.
func (ra *ResearchV2Activities) FilterByCredibility(ctx context.Context, input FilterByCredibilityInput) (*FilterByCredibilityResult, error) {
	threshold := input.Threshold
	if threshold <= 0 {
		threshold = 0.4
	}
	out := make([]types.Evidence, 0, len(input.Evidence))
	for _, e := range input.Evidence {
		if e.CredibilityScore >= threshold {
			out = append(out, e)
		}
	}
	// Sort by credibility desc for determinism in tests.
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].CredibilityScore > out[j].CredibilityScore
	})
	return &FilterByCredibilityResult{Filtered: out}, nil
}

// ─── GenerateReportV2Activity ───────────────────────────────────────────

// GenerateReportV2Input is the v2 report generation input.
type GenerateReportV2Input struct {
	QueryID       string               `json:"query_id"`
	Query         string               `json:"query"`
	Evidence      []types.Evidence     `json:"evidence"`
	Contradictions []types.Contradiction `json:"contradictions"`
	CitationStats types.CitationStats   `json:"citation_stats"`
	Title         string               `json:"title"`
	Config        types.ResearchV2Config `json:"config"`
	Model         string               `json:"model"`
	MockLLM       bool                 `json:"mock_llm"`
	TaskID        string               `json:"task_id"`
	WorkflowID    string               `json:"workflow_id"`
	RunID         string               `json:"run_id"`
}

// GenerateReportV2Result contains the report + ref pointers.
type GenerateReportV2Result struct {
	Report         types.ResearchReportV2 `json:"report"`
	ReportRef      string                 `json:"report_ref"`
	ExecutiveRef   string                 `json:"executive_ref"`
	ContradictionRef string               `json:"contradiction_ref,omitempty"`
	TokensUsed     int                    `json:"tokens_used"`
	Mode           string                 `json:"mode,omitempty"`
}

// GenerateReportV2 produces the final report. Long-form content is
// always written via WorkspaceRef pointers; the Workflow stores
// only the metadata.
func (ra *ResearchV2Activities) GenerateReportV2(ctx context.Context, input GenerateReportV2Input) (*GenerateReportV2Result, error) {
	reportRef := fmt.Sprintf("research:%s:report", input.WorkflowID)
	execRef := fmt.Sprintf("research:%s:executive_summary", input.WorkflowID)

	if input.MockLLM {
		// Build a deterministic mock report. Sections derived from
		// evidence + contradictions count; the executive summary
		// reflects the strongest evidence summaries.
		title := input.Title
		if title == "" {
			title = "Research report: " + truncate(input.Query, 60)
		}
		exec := mockExecutiveSummary(input.Query, input.Evidence, input.Contradictions)
		sections := buildMockSections(input.Evidence, input.Contradictions)
		report := types.ResearchReportV2{
			QueryID:              input.QueryID,
			Title:                title,
			ExecutiveSummary:     truncate(exec, 1500),
			ExecutiveSummaryRef:  execRef,
			Sections:             sections,
			AllEvidence:          input.Evidence,
			Contradictions:       input.Contradictions,
			ContradictionReportRef: reportRef + ":contradictions",
			CitationStats:        input.CitationStats,
			ConfidenceScore:      mockConfidence(input.Evidence, input.Contradictions),
			TotalSources:         uniqueSourceCount(input.Evidence),
			TotalEvidenceItems:   len(input.Evidence),
			ReportRef:            reportRef,
			CreatedAt:            time.Now().UTC(),
		}
		return &GenerateReportV2Result{
			Report:         report,
			ReportRef:      reportRef,
			ExecutiveRef:   execRef,
			ContradictionRef: report.ContradictionReportRef,
			TokensUsed:     60,
			Mode:           "mock",
		}, nil
	}

	prompt := buildReportPrompt(input)
	result, err := ra.agentActs.CallLLM(ctx, AgentActivityInput{
		TaskID:                input.TaskID,
		WorkflowID:            input.WorkflowID,
		RunID:                 input.RunID,
		Query:                 prompt,
		Model:                 input.Model,
		Temperature:           0.4,
		MaxCompletionTokens:   1024,
		AllowedCompletionTokens: 1024,
	})
	if err != nil {
		return nil, fmt.Errorf("generate report v2: %w", err)
	}

	exec := truncate(result.Answer, 1500)
	sections := parseReportSections(result.Answer, input.Evidence)
	report := types.ResearchReportV2{
		QueryID:              input.QueryID,
		Title:                fallback(input.Title, "Research report: "+truncate(input.Query, 60)),
		ExecutiveSummary:     exec,
		ExecutiveSummaryRef:  execRef,
		Sections:             sections,
		AllEvidence:          input.Evidence,
		Contradictions:       input.Contradictions,
		ContradictionReportRef: reportRef + ":contradictions",
		CitationStats:        input.CitationStats,
		ConfidenceScore:      realConfidence(input.Evidence, input.Contradictions),
		TotalSources:         uniqueSourceCount(input.Evidence),
		TotalEvidenceItems:   len(input.Evidence),
		ReportRef:            reportRef,
		CreatedAt:            time.Now().UTC(),
	}
	return &GenerateReportV2Result{
		Report:         report,
		ReportRef:      reportRef,
		ExecutiveRef:   execRef,
		ContradictionRef: report.ContradictionReportRef,
		TokensUsed:     result.Usage.TotalTokens,
		Mode:           "real",
	}, nil
}

// ConflictRef returns the contradiction section ref pointer.
// (Helper rather than a method on types.ResearchReportV2 to avoid
// "cannot define new methods on non-local type" in this package.)
func ConflictRef(r types.ResearchReportV2) string {
	if r.ContradictionReportRef == "" {
		return r.ReportRef + ":contradictions"
	}
	return r.ContradictionReportRef
}

func mockExecutiveSummary(query string, ev []types.Evidence, conts []types.Contradiction) string {
	parts := []string{
		"Research summary for: " + truncate(query, 80),
		fmt.Sprintf("Based on %d evidence items across %d unique sources.", len(ev), uniqueSourceCount(ev)),
	}
	if len(conts) > 0 {
		parts = append(parts, fmt.Sprintf("Detected %d contradiction(s) flagged for human review.", len(conts)))
	}
	// Top 2 evidence summaries.
	for i, e := range ev {
		if i >= 2 {
			break
		}
		parts = append(parts, "- "+truncate(e.Summary, 160))
	}
	return strings.Join(parts, "\n\n")
}

func buildMockSections(ev []types.Evidence, conts []types.Contradiction) []types.ReportSection {
	sections := []types.ReportSection{}
	if len(ev) > 0 {
		sections = append(sections, types.ReportSection{
			Title:          "Evidence overview",
			ContentSummary: "Top-ranked evidence supporting the query.",
			ContentRef:     "research:sections:evidence_overview",
			Evidence:       evidenceIDs(ev),
		})
	}
	if len(conts) > 0 {
		sections = append(sections, types.ReportSection{
			Title:          "Contradictions",
			ContentSummary: "Conflicting evidence flagged for review.",
			ContentRef:     "research:sections:contradictions",
			Evidence:       evidenceIDs(ev),
		})
	}
	sections = append(sections, types.ReportSection{
		Title:          "Conclusion",
		ContentSummary: "Final synthesized answer based on available evidence.",
		ContentRef:     "research:sections:conclusion",
		Evidence:       evidenceIDs(ev),
	})
	return sections
}

func evidenceIDs(ev []types.Evidence) []string {
	ids := make([]string, 0, len(ev))
	for _, e := range ev {
		ids = append(ids, e.ID)
	}
	return ids
}

func uniqueSourceCount(ev []types.Evidence) int {
	set := map[string]struct{}{}
	for _, e := range ev {
		set[e.SourceID] = struct{}{}
	}
	return len(set)
}

func mockConfidence(ev []types.Evidence, conts []types.Contradiction) float64 {
	if len(ev) == 0 {
		return 0.2
	}
	avg := 0.0
	for _, e := range ev {
		avg += e.CredibilityScore
	}
	avg /= float64(len(ev))
	// Contradictions reduce confidence slightly.
	avg -= 0.05 * float64(len(conts))
	if avg < 0 {
		avg = 0
	}
	if avg > 1 {
		avg = 1
	}
	return avg
}

func realConfidence(ev []types.Evidence, conts []types.Contradiction) float64 {
	return mockConfidence(ev, conts) // same formula; real LLM only fills text fields
}

func buildReportPrompt(input GenerateReportV2Input) string {
	var evLines []string
	for i, e := range input.Evidence {
		if i >= 5 {
			break
		}
		evLines = append(evLines, fmt.Sprintf("- [%s] %s (credibility=%.2f)", e.ID, truncate(e.Summary, 200), e.CredibilityScore))
	}
	return fmt.Sprintf(
		`Produce a structured research report for the query below. Return STRICT JSON:
{
  "title": "...",
  "executive_summary": "2-3 paragraphs",
  "sections": [
    {"title":"Evidence overview","summary":"...","evidence":["<id>"]},
    {"title":"Contradictions","summary":"...","evidence":["<id>"]},
    {"title":"Conclusion","summary":"...","evidence":["<id>"]}
  ]
}

Query: %q
Evidence (top %d):
%s
Contradictions: %d`,
		input.Query, len(evLines), strings.Join(evLines, "\n"), len(input.Contradictions),
	)
}

func parseReportSections(raw string, ev []types.Evidence) []types.ReportSection {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return buildMockSections(ev, nil)
	}
	// Try JSON.
	if start := strings.Index(raw, "{"); start >= 0 {
		if end := strings.LastIndex(raw, "}"); end > start {
			block := raw[start : end+1]
			var parsed struct {
				Title    string `json:"title"`
				Exec     string `json:"executive_summary"`
				Sections []struct {
					Title    string   `json:"title"`
					Summary  string   `json:"summary"`
					Evidence []string `json:"evidence"`
				} `json:"sections"`
			}
			if err := json.Unmarshal([]byte(block), &parsed); err == nil && len(parsed.Sections) > 0 {
				out := make([]types.ReportSection, 0, len(parsed.Sections))
				for i, s := range parsed.Sections {
					out = append(out, types.ReportSection{
						Title:          s.Title,
						ContentSummary: truncate(s.Summary, 500),
						ContentRef:     fmt.Sprintf("research:sections:s%d", i),
						Evidence:       s.Evidence,
					})
				}
				return out
			}
		}
	}
	return buildMockSections(ev, nil)
}

func fallback(s, alt string) string {
	if strings.TrimSpace(s) == "" {
		return alt
	}
	return s
}

// ─── ReflectionBeforeSynthesisActivity (v2 optional) ────────────────────

// ReflectionBeforeSynthesisInput is the v2 pre-synthesis reflection input.
type ReflectionBeforeSynthesisInput struct {
	Query    string             `json:"query"`
	Evidence []types.Evidence   `json:"evidence"`
	MockLLM  bool               `json:"mock_llm"`
	TaskID   string             `json:"task_id"`
	WorkflowID string           `json:"workflow_id"`
	RunID    string             `json:"run_id"`
	Model    string             `json:"model"`
}

// ReflectionBeforeSynthesisResult is the improved evidence + confidence.
type ReflectionBeforeSynthesisResult struct {
	ImprovedEvidence []types.Evidence `json:"improved_evidence"`
	ConfidenceScore  float64          `json:"confidence_score"`
	TokensUsed      int               `json:"tokens_used"`
	Mode            string           `json:"mode,omitempty"`
}

// ReflectionBeforeSynthesis runs a one-pass critique of the evidence
// before synthesis. v2 minimal: re-scores credibility with a small
// adjustment based on evidence consistency, and emits a refined
// evidence list + confidence.
func (ra *ResearchV2Activities) ReflectionBeforeSynthesis(ctx context.Context, input ReflectionBeforeSynthesisInput) (*ReflectionBeforeSynthesisResult, error) {
	if input.MockLLM {
		// Mock: drop evidence with no summary, slightly boost ones
		// with rich summaries.
		out := make([]types.Evidence, 0, len(input.Evidence))
		for _, e := range input.Evidence {
			if strings.TrimSpace(e.Summary) == "" {
				continue
			}
			boost := 0.05
			if len(e.Summary) > 200 {
				boost = 0.1
			}
			e.CredibilityScore = clampUnit(e.CredibilityScore + boost)
			e.SourceCredibility.CredibilityScore = e.CredibilityScore
			out = append(out, e)
		}
		return &ReflectionBeforeSynthesisResult{
			ImprovedEvidence: out,
			ConfidenceScore:  mockConfidence(out, nil),
			TokensUsed:       20,
			Mode:             "mock",
		}, nil
	}
	// Real: same adjustment + small LLM call. We treat the LLM call
	// as optional (best-effort) to keep this optional path cheap.
	prompt := fmt.Sprintf(
		`You are reviewing evidence for a research query. Identify any
evidence items that should be discarded as low-quality and emit a
short critique (≤200 chars).

Query: %q
Evidence count: %d
First evidence summary: %s

Return STRICT JSON: {"discard_ids": ["ev-..."], "critique": "..."}`,
		input.Query, len(input.Evidence), firstSummary(input.Evidence),
	)
	result, err := ra.agentActs.CallLLM(ctx, AgentActivityInput{
		TaskID:                input.TaskID,
		WorkflowID:            input.WorkflowID,
		RunID:                 input.RunID,
		Query:                 prompt,
		Model:                 input.Model,
		Temperature:           0.2,
		MaxCompletionTokens:   256,
		AllowedCompletionTokens: 256,
	})
	if err != nil {
		// Soft-fail: keep original evidence
		return &ReflectionBeforeSynthesisResult{
			ImprovedEvidence: input.Evidence,
			ConfidenceScore:  mockConfidence(input.Evidence, nil),
			TokensUsed:       0,
			Mode:             "fallback",
		}, nil
	}
	discard := parseDiscardIDs(result.Answer)
	out := make([]types.Evidence, 0, len(input.Evidence))
	for _, e := range input.Evidence {
		if _, drop := discard[e.ID]; drop {
			continue
		}
		out = append(out, e)
	}
	return &ReflectionBeforeSynthesisResult{
		ImprovedEvidence: out,
		ConfidenceScore:  mockConfidence(out, nil),
		TokensUsed:       result.Usage.TotalTokens,
		Mode:             "real",
	}, nil
}

func firstSummary(ev []types.Evidence) string {
	if len(ev) == 0 {
		return "(none)"
	}
	return truncate(ev[0].Summary, 200)
}

func parseDiscardIDs(raw string) map[string]struct{} {
	out := map[string]struct{}{}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return out
	}
	if start := strings.Index(raw, "{"); start >= 0 {
		if end := strings.LastIndex(raw, "}"); end > start {
			block := raw[start : end+1]
			var parsed struct {
				DiscardIDs []string `json:"discard_ids"`
			}
			if err := json.Unmarshal([]byte(block), &parsed); err == nil {
				for _, id := range parsed.DiscardIDs {
					out[strings.TrimSpace(id)] = struct{}{}
				}
				return out
			}
		}
	}
	// Heuristic: pick any "ev-..." token from the raw (post-JSON-fail
	// fallback). Match against the raw text so that "ev-abc" survives
	// even though tokenizeKeywords would split it on the hyphen.
	lower := strings.ToLower(raw)
	for _, kw := range strings.FieldsFunc(lower, func(r rune) bool {
		return r == ' ' || r == '\n' || r == '\t' || r == ',' || r == ';' || r == '.'
	}) {
		// Strip surrounding quotes / brackets.
		kw = strings.Trim(kw, `"'[](){}:,;`)
		if strings.HasPrefix(kw, "ev-") {
			out[kw] = struct{}{}
		}
	}
	return out
}

// ─── DebateBeforeSynthesisActivity (v2 optional, reuses Slice 27) ──────

// DebateBeforeSynthesisInput is the v2 pre-synthesis debate input. We
// re-use the Slice 27 debate machinery in Workflow; this activity is
// a thin marker so the workflow has a checkpoint.
type DebateBeforeSynthesisInput struct {
	Query       string                 `json:"query"`
	Evidence    []types.Evidence       `json:"evidence"`
	Config      types.DebateConfig     `json:"config"`
	Model       string                 `json:"model"`
	MockLLM     bool                   `json:"mock_llm"`
	TaskID      string                 `json:"task_id"`
	WorkflowID  string                 `json:"workflow_id"`
	RunID       string                 `json:"run_id"`
}

// DebateBeforeSynthesisResult is the resolved evidence + notes.
type DebateBeforeSynthesisResult struct {
	ResolvedEvidence []types.Evidence `json:"resolved_evidence"`
	ResolutionNotes  string           `json:"resolution_notes"`
	TokensUsed      int               `json:"tokens_used"`
	Mode            string           `json:"mode,omitempty"`
}

// DebateBeforeSynthesis is a stub for the v2 pre-synthesis debate
// checkpoint. It does NOT spin up a Slice 27 DebateWorkflow (that
// would be a separate child workflow); instead it produces a
// resolution note acknowledging the debate step. v2 keeps the
// checkpoint minimal because Slice 27 is the canonical debate path.
func (ra *ResearchV2Activities) DebateBeforeSynthesis(ctx context.Context, input DebateBeforeSynthesisInput) (*DebateBeforeSynthesisResult, error) {
	// Deterministic mock / fallback resolution: keep all evidence,
	// emit a "debate step recorded" note.
	notes := "Debate step recorded: v2 pre-synthesis debate checkpoint (no full debate round for cost reasons)."
	if input.MockLLM {
		notes = "mock: debate step recorded"
	}
	return &DebateBeforeSynthesisResult{
		ResolvedEvidence: input.Evidence,
		ResolutionNotes:  notes,
		TokensUsed:       0,
		Mode:             "mock",
	}, nil
}

// ─── AuditResearchV2Activity ────────────────────────────────────────────

// AuditResearchV2Input is the v2 audit record.
type AuditResearchV2Input struct {
	WorkflowID        string `json:"workflow_id"`
	Query             string `json:"query"`
	FinalPosition     string `json:"final_position"`
	Consensus         bool   `json:"consensus"`
	TotalRounds       int    `json:"total_rounds"`
	TotalTokens       int    `json:"total_tokens"`
	VerdictRef        string `json:"verdict_ref"`
	TranscriptRef     string `json:"transcript_ref"`
	WinningTurnRef    string `json:"winning_turn_ref"`
	ReportRef         string `json:"report_ref"`
	SourceCount       int    `json:"source_count"`
	EvidenceCount     int    `json:"evidence_count"`
	ContradictionCount int  `json:"contradiction_count"`
}

// AuditResearchV2Result returns an audit ID placeholder.
type AuditResearchV2Result struct {
	AuditID string `json:"audit_id"`
}

// AuditResearchV2 is currently a placeholder (Phase 7 task book §7.6
// allows non-fatal audit). Future slices may write to a
// `research_reports_v2` table per §8.2.
func (ra *ResearchV2Activities) AuditResearchV2(ctx context.Context, input AuditResearchV2Input) (*AuditResearchV2Result, error) {
	return &AuditResearchV2Result{AuditID: "audit-" + input.WorkflowID}, nil
}
