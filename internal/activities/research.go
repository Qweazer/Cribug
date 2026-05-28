package activities

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"cribug/internal/types"

	"github.com/google/uuid"
)

// ResearchActivities provides Temporal activities for research-synthesis workflows.
type ResearchActivities struct {
	ragActivities *RAGRetrievalActivities
	llmServiceURL string
	httpClient    *http.Client
}

// NewResearchActivities creates a new ResearchActivities.
func NewResearchActivities(ragActivities *RAGRetrievalActivities, llmServiceURL string) *ResearchActivities {
	return &ResearchActivities{
		ragActivities: ragActivities,
		llmServiceURL: llmServiceURL,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// ---------------------------------------------------------------------------
// Activity 1: DecomposeQueryActivity
// ---------------------------------------------------------------------------

// DecomposeQueryInput contains the query to decompose.
type DecomposeQueryInput struct {
	Query      string
	QueryID    string // auto-generated if empty
	AgentID    string
	WorkflowID string
}

// DecomposeQueryOutput contains the decomposed subqueries.
type DecomposeQueryOutput struct {
	QueryID    string
	Subqueries []types.ResearchSubquery
}

// DecomposeQueryActivity calls the Python LLM to decompose a research query
// into 2-5 specific sub-questions. If the LLM is unavailable or returns an
// invalid response, the original query is returned as a single subquery
// (graceful degradation).
func (a *ResearchActivities) DecomposeQueryActivity(ctx context.Context, input DecomposeQueryInput) (DecomposeQueryOutput, error) {
	queryID := input.QueryID
	if queryID == "" {
		queryID = uuid.New().String()
	}

	// Build decompose prompt
	prompt := fmt.Sprintf(
		`Break the following research query into 2-5 specific sub-questions. Return ONLY a JSON array of objects with a "text" field, like:
[{"text": "Sub-question 1?"}, {"text": "Sub-question 2?"}]

Query: %s`, input.Query)

	// Call LLM with system + user messages
	messages := []types.LLMMessage{
		{Role: "system", Content: "You are a research query decomposer. Break complex queries into specific, answerable sub-questions."},
		{Role: "user", Content: prompt},
	}

	reqBody := types.LLMRequest{
		TraceID:             queryID,
		TaskID:              queryID,
		Provider:            "openai_compatible",
		Model:               "gpt-4o-mini",
		Messages:            messages,
		Temperature:         0.7,
		MaxCompletionTokens: 1024,
		Metadata: map[string]any{
			"workflow_id": input.WorkflowID,
			"agent_id":    input.AgentID,
		},
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return singleSubqueryFallback(queryID, input.Query)
	}

	resp, err := a.httpClient.Post(
		a.llmServiceURL+"/chat",
		"application/json",
		bytes.NewBuffer(jsonBody),
	)
	if err != nil {
		return singleSubqueryFallback(queryID, input.Query)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return singleSubqueryFallback(queryID, input.Query)
	}

	var llmResp types.LLMResponse
	if err := json.NewDecoder(resp.Body).Decode(&llmResp); err != nil {
		return singleSubqueryFallback(queryID, input.Query)
	}

	if llmResp.Content == "" {
		return singleSubqueryFallback(queryID, input.Query)
	}

	subqueries, err := parseSubqueriesFromLLM(llmResp.Content, queryID)
	if err != nil {
		return singleSubqueryFallback(queryID, input.Query)
	}

	return DecomposeQueryOutput{
		QueryID:    queryID,
		Subqueries: subqueries,
	}, nil
}

// singleSubqueryFallback creates a single subquery from the original query
// when LLM decomposition fails (graceful degradation).
func singleSubqueryFallback(queryID, query string) (DecomposeQueryOutput, error) {
	return DecomposeQueryOutput{
		QueryID: queryID,
		Subqueries: []types.ResearchSubquery{
			{
				ID:       uuid.New().String(),
				ParentID: queryID,
				Text:     query,
				Status:   "pending",
			},
		},
	}, nil
}

// parseSubqueriesFromLLM extracts a JSON array of subqueries from the LLM
// response content. It handles markdown code blocks as well as raw JSON.
func parseSubqueriesFromLLM(content string, parentID string) ([]types.ResearchSubquery, error) {
	jsonStr := content

	// Check for ```json ... ``` block
	if start := strings.Index(content, "```json"); start >= 0 {
		start += len("```json")
		if end := strings.Index(content[start:], "```"); end >= 0 {
			jsonStr = strings.TrimSpace(content[start : start+end])
		}
	}

	// Find JSON array boundaries
	arrStart := strings.Index(jsonStr, "[")
	arrEnd := strings.LastIndex(jsonStr, "]")
	if arrStart < 0 || arrEnd < 0 || arrEnd <= arrStart {
		return nil, fmt.Errorf("no JSON array found in response")
	}

	var rawSubqueries []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(jsonStr[arrStart:arrEnd+1]), &rawSubqueries); err != nil {
		return nil, fmt.Errorf("parse subqueries: %w", err)
	}

	if len(rawSubqueries) == 0 {
		return nil, fmt.Errorf("empty subqueries")
	}

	subqueries := make([]types.ResearchSubquery, len(rawSubqueries))
	for i, sq := range rawSubqueries {
		subqueries[i] = types.ResearchSubquery{
			ID:       uuid.New().String(),
			ParentID: parentID,
			Text:     sq.Text,
			Status:   "pending",
		}
	}
	return subqueries, nil
}

// ---------------------------------------------------------------------------
// Activity 2: RetrieveEvidenceActivity
// ---------------------------------------------------------------------------

// RetrieveEvidenceInput contains the subquery and search parameters.
type RetrieveEvidenceInput struct {
	Subquery   types.ResearchSubquery
	Collection string
	TopK       int
}

// RetrieveEvidenceOutput contains the retrieved evidence.
type RetrieveEvidenceOutput struct {
	SubqueryID string
	ChunkIDs   []string
	Summary    string
	Score      float64
}

// RetrieveEvidenceActivity embeds the subquery text, searches the vector DB,
// fetches chunk content, and packs the context. Reuses the RAG retrieval
// activities internally. Returns empty results (no error) if no evidence found.
func (a *ResearchActivities) RetrieveEvidenceActivity(ctx context.Context, input RetrieveEvidenceInput) (RetrieveEvidenceOutput, error) {
	topK := input.TopK
	if topK <= 0 {
		topK = 5
	}
	collection := input.Collection
	if collection == "" {
		collection = "task_embeddings"
	}

	// Step 1: Embed and search
	searchOutput, err := a.ragActivities.EmbedAndSearchChunksActivity(ctx, EmbedAndSearchChunksInput{
		QueryText:  input.Subquery.Text,
		Collection: collection,
		TopK:       topK,
	})
	if err != nil {
		return RetrieveEvidenceOutput{SubqueryID: input.Subquery.ID}, nil
	}

	if len(searchOutput.Hits) == 0 {
		return RetrieveEvidenceOutput{SubqueryID: input.Subquery.ID}, nil
	}

	// Extract chunk IDs and top score
	chunkIDs := make([]string, 0, len(searchOutput.Hits))
	var topScore float64
	for _, hit := range searchOutput.Hits {
		chunkIDs = append(chunkIDs, hit.ChunkID)
		if hit.Score > topScore {
			topScore = hit.Score
		}
	}

	// Step 2: Fetch chunk content (best-effort)
	fetchOutput, err := a.ragActivities.FetchChunkContentActivity(ctx, FetchChunkContentInput{
		ChunkIDs: chunkIDs,
	})
	if err != nil {
		// Return chunk IDs and score even if fetch fails
		return RetrieveEvidenceOutput{
			SubqueryID: input.Subquery.ID,
			ChunkIDs:   chunkIDs,
			Score:      topScore,
		}, nil
	}

	// Step 3: Pack context (best-effort)
	packOutput, err := a.ragActivities.PackContextActivity(ctx, PackContextInput{
		Query:  input.Subquery.Text,
		Chunks: fetchOutput.Chunks,
	})
	if err != nil {
		return RetrieveEvidenceOutput{
			SubqueryID: input.Subquery.ID,
			ChunkIDs:   chunkIDs,
			Score:      topScore,
		}, nil
	}

	return RetrieveEvidenceOutput{
		SubqueryID: input.Subquery.ID,
		ChunkIDs:   chunkIDs,
		Summary:    packOutput.Context,
		Score:      topScore,
	}, nil
}

// ---------------------------------------------------------------------------
// Activity 3: SynthesizeResultActivity
// ---------------------------------------------------------------------------

// SynthesizeResultInput contains the query, subqueries, and evidence.
type SynthesizeResultInput struct {
	QueryID      string
	Query        string
	Subqueries   []types.ResearchSubquery
	EvidenceText []string // packed context per subquery
	Model        string
	MockLLM      bool
}

// SynthesizeResultOutput contains the synthesized answer.
type SynthesizeResultOutput struct {
	Answer          string
	Evidence        []types.EvidenceReference
	SubqueryAnswers []types.SubqueryAnswer
	TokenCount      int
}

// SynthesizeResultActivity calls the Python LLM with the query and evidence
// to produce a synthesized answer. If MockLLM is true, returns a mock answer
// with evidence summary for testing.
func (a *ResearchActivities) SynthesizeResultActivity(ctx context.Context, input SynthesizeResultInput) (SynthesizeResultOutput, error) {
	// Build evidence references from inputs
	evidenceRefs := buildEvidenceReferences(input.Subqueries, input.EvidenceText)

	// If MockLLM is true, return mock answer
	if input.MockLLM {
		return synthesizeMockResult(input), nil
	}

	// Build synthesis prompt
	evidenceStr := buildEvidenceString(input.Subqueries, input.EvidenceText)
	prompt := fmt.Sprintf(
		`Research Query: %s

Evidence from research:
%s

Synthesize a comprehensive answer to the research query based on the evidence provided.`,
		input.Query, evidenceStr)

	model := input.Model
	if model == "" {
		model = "gpt-4o-mini"
	}

	// Call LLM with system + user messages
	messages := []types.LLMMessage{
		{Role: "system", Content: "You are a research synthesis assistant. Synthesize answers from provided evidence."},
		{Role: "user", Content: prompt},
	}

	reqBody := types.LLMRequest{
		TraceID:             input.QueryID,
		TaskID:              input.QueryID,
		Provider:            "openai_compatible",
		Model:               model,
		Messages:            messages,
		Temperature:         0.3,
		MaxCompletionTokens: 2048,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return resultWithFallback(evidenceStr, evidenceRefs, input.Subqueries), nil
	}

	resp, err := a.httpClient.Post(
		a.llmServiceURL+"/chat",
		"application/json",
		bytes.NewBuffer(jsonBody),
	)
	if err != nil {
		return resultWithFallback(evidenceStr, evidenceRefs, input.Subqueries), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resultWithFallback(evidenceStr, evidenceRefs, input.Subqueries), nil
	}

	var llmResp types.LLMResponse
	if err := json.NewDecoder(resp.Body).Decode(&llmResp); err != nil {
		return resultWithFallback(evidenceStr, evidenceRefs, input.Subqueries), nil
	}

	answer := llmResp.Content
	if answer == "" {
		answer = evidenceStr
	}

	subqueryAnswers := buildFallbackSubqueryAnswers(input.Subqueries)

	return SynthesizeResultOutput{
		Answer:          answer,
		Evidence:        evidenceRefs,
		SubqueryAnswers: subqueryAnswers,
		TokenCount:      llmResp.Usage.TotalTokens,
	}, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// buildEvidenceReferences constructs EvidenceReference entries from subqueries
// and their corresponding evidence texts.
func buildEvidenceReferences(subqueries []types.ResearchSubquery, evidenceText []string) []types.EvidenceReference {
	refs := make([]types.EvidenceReference, 0, len(subqueries))
	for i, sq := range subqueries {
		ref := types.EvidenceReference{
			SubqueryID: sq.ID,
			Summary:    sq.Text,
		}
		if i < len(evidenceText) && evidenceText[i] != "" {
			ref.Summary = evidenceText[i]
			ref.ChunkCount = strings.Count(evidenceText[i], "---") + 1
			ref.TopScore = 0.95
		}
		refs = append(refs, ref)
	}
	return refs
}

// buildEvidenceString formats subqueries and their evidence into a single
// context string for the synthesis LLM prompt.
func buildEvidenceString(subqueries []types.ResearchSubquery, evidenceText []string) string {
	var builder strings.Builder
	for i, sq := range subqueries {
		builder.WriteString(fmt.Sprintf("--- Sub-question %d: %s ---\n", i+1, sq.Text))
		if i < len(evidenceText) && evidenceText[i] != "" {
			builder.WriteString(evidenceText[i])
		} else {
			builder.WriteString("(No evidence retrieved)")
		}
		builder.WriteString("\n\n")
	}
	return builder.String()
}

// buildFallbackSubqueryAnswers creates placeholder SubqueryAnswer entries
// using the subquery text as a stand-in answer.
func buildFallbackSubqueryAnswers(subqueries []types.ResearchSubquery) []types.SubqueryAnswer {
	answers := make([]types.SubqueryAnswer, len(subqueries))
	for i, sq := range subqueries {
		answers[i] = types.SubqueryAnswer{
			SubqueryID: sq.ID,
			Answer:     sq.Text,
			Confidence: 0.5,
		}
	}
	return answers
}

// resultWithFallback builds a SynthesizeResultOutput using the evidence string
// as the answer when the LLM call fails.
func resultWithFallback(answer string, evidenceRefs []types.EvidenceReference, subqueries []types.ResearchSubquery) SynthesizeResultOutput {
	return SynthesizeResultOutput{
		Answer:          answer,
		Evidence:        evidenceRefs,
		SubqueryAnswers: buildFallbackSubqueryAnswers(subqueries),
		TokenCount:      0,
	}
}

// synthesizeMockResult returns a deterministic mock result for testing.
func synthesizeMockResult(input SynthesizeResultInput) SynthesizeResultOutput {
	subqueryAnswers := make([]types.SubqueryAnswer, 0, len(input.Subqueries))
	for _, sq := range input.Subqueries {
		subqueryAnswers = append(subqueryAnswers, types.SubqueryAnswer{
			SubqueryID: sq.ID,
			Answer:     fmt.Sprintf("Mock answer for: %s", sq.Text),
			Confidence: 0.85,
		})
	}

	evidenceRefs := make([]types.EvidenceReference, 0, len(input.Subqueries))
	for i, sq := range input.Subqueries {
		ref := types.EvidenceReference{
			SubqueryID: sq.ID,
			ChunkCount: 2,
			TopScore:   0.92,
			Summary:    sq.Text,
		}
		if i < len(input.EvidenceText) && input.EvidenceText[i] != "" {
			ref.Summary = input.EvidenceText[i]
		}
		evidenceRefs = append(evidenceRefs, ref)
	}

	return SynthesizeResultOutput{
		Answer:          fmt.Sprintf("Mock synthesized answer for query: %s", input.Query),
		Evidence:        evidenceRefs,
		SubqueryAnswers: subqueryAnswers,
		TokenCount:      150,
	}
}
