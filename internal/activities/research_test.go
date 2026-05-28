package activities

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"cribug/internal/db"
	"cribug/internal/embeddings"
	"cribug/internal/types"
	"cribug/internal/vectordb"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Compile and type tests
// ---------------------------------------------------------------------------

func TestResearchActivities_Compiles(t *testing.T) {
	act := NewResearchActivities(nil, "http://localhost:8000")
	if act == nil {
		t.Fatal("NewResearchActivities returned nil")
	}
}

func TestResearchTypes(t *testing.T) {
	// ResearchQuery
	rq := types.ResearchQuery{
		ID:         "query-1",
		Query:      "test query",
		AgentID:    "agent-1",
		WorkflowID: "wf-1",
		Status:     "pending",
	}
	if rq.ID != "query-1" {
		t.Errorf("ResearchQuery.ID = %q", rq.ID)
	}
	if rq.Query != "test query" {
		t.Errorf("ResearchQuery.Query = %q", rq.Query)
	}

	// ResearchSubquery
	rs := types.ResearchSubquery{
		ID:       "sub-1",
		ParentID: "query-1",
		Text:     "sub question?",
		Status:   "pending",
	}
	if rs.ID != "sub-1" {
		t.Errorf("ResearchSubquery.ID = %q", rs.ID)
	}
	if rs.Text != "sub question?" {
		t.Errorf("ResearchSubquery.Text = %q", rs.Text)
	}

	// ResearchEvidence
	re := types.ResearchEvidence{
		SubqueryID: "sub-1",
		ChunkIDs:   []string{"chunk-1", "chunk-2"},
		Summary:    "evidence summary",
		Score:      0.95,
		Metadata:   map[string]interface{}{"source": "test"},
	}
	if len(re.ChunkIDs) != 2 {
		t.Errorf("ResearchEvidence.ChunkIDs length = %d", len(re.ChunkIDs))
	}
	if re.Score != 0.95 {
		t.Errorf("ResearchEvidence.Score = %f", re.Score)
	}

	// EvidenceReference
	er := types.EvidenceReference{
		SubqueryID: "sub-1",
		ChunkCount: 3,
		TopScore:   0.92,
		Summary:    "reference summary",
	}
	if er.ChunkCount != 3 {
		t.Errorf("EvidenceReference.ChunkCount = %d", er.ChunkCount)
	}
	if er.TopScore != 0.92 {
		t.Errorf("EvidenceReference.TopScore = %f", er.TopScore)
	}

	// SubqueryAnswer
	sa := types.SubqueryAnswer{
		SubqueryID: "sub-1",
		Answer:     "the answer",
		Confidence: 0.85,
	}
	if sa.Answer != "the answer" {
		t.Errorf("SubqueryAnswer.Answer = %q", sa.Answer)
	}
	if sa.Confidence != 0.85 {
		t.Errorf("SubqueryAnswer.Confidence = %f", sa.Confidence)
	}

	// ResearchSynthesisResult
	result := types.ResearchSynthesisResult{
		QueryID: "query-1",
		Answer:  "synthesized answer",
		Evidence: []types.EvidenceReference{er},
		SubqueryAnswers: []types.SubqueryAnswer{sa},
		TokenCount: 150,
	}
	if result.Answer != "synthesized answer" {
		t.Errorf("ResearchSynthesisResult.Answer = %q", result.Answer)
	}
	if len(result.Evidence) != 1 {
		t.Errorf("ResearchSynthesisResult.Evidence length = %d", len(result.Evidence))
	}
	if result.TokenCount != 150 {
		t.Errorf("ResearchSynthesisResult.TokenCount = %d", result.TokenCount)
	}
}

// ---------------------------------------------------------------------------
// DecomposeQueryActivity tests
// ---------------------------------------------------------------------------

func TestDecomposeQueryActivity_MockLLM(t *testing.T) {
	// Mock LLM server that returns subqueries JSON
	llmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		var req struct {
			Messages []types.LLMMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if len(req.Messages) < 2 {
			t.Errorf("expected at least 2 messages, got %d", len(req.Messages))
		}

		resp := types.LLMResponse{
			Content: `[{"text": "What is the historical context?"}, {"text": "What are the current trends?"}, {"text": "What are future implications?"}]`,
			Usage:   types.Usage{PromptTokens: 50, CompletionTokens: 30, TotalTokens: 80},
			Model:   "gpt-4o-mini",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer llmSrv.Close()

	act := NewResearchActivities(nil, llmSrv.URL)
	output, err := act.DecomposeQueryActivity(context.Background(), DecomposeQueryInput{
		Query:      "What is the impact of AI on society?",
		AgentID:    "agent-1",
		WorkflowID: "wf-1",
	})

	if err != nil {
		t.Fatalf("DecomposeQueryActivity failed: %v", err)
	}
	if output.QueryID == "" {
		t.Error("expected non-empty QueryID")
	}
	if len(output.Subqueries) != 3 {
		t.Fatalf("expected 3 subqueries, got %d", len(output.Subqueries))
	}

	// Verify each subquery
	for i, sq := range output.Subqueries {
		if sq.ID == "" {
			t.Errorf("subquery[%d] ID is empty", i)
		}
		if sq.ParentID != output.QueryID {
			t.Errorf("subquery[%d] ParentID = %q, want %q", i, sq.ParentID, output.QueryID)
		}
		if sq.Text == "" {
			t.Errorf("subquery[%d] Text is empty", i)
		}
		if sq.Status != "pending" {
			t.Errorf("subquery[%d] Status = %q, want %q", i, sq.Status, "pending")
		}
	}

	// Verify specific texts
	expectedTexts := []string{
		"What is the historical context?",
		"What are the current trends?",
		"What are future implications?",
	}
	for i, expected := range expectedTexts {
		if output.Subqueries[i].Text != expected {
			t.Errorf("subquery[%d].Text = %q, want %q", i, output.Subqueries[i].Text, expected)
		}
	}
}

func TestDecomposeQueryActivity_AutoGeneratedQueryID(t *testing.T) {
	llmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := types.LLMResponse{
			Content: `[{"text": "Sub-question 1?"}]`,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer llmSrv.Close()

	act := NewResearchActivities(nil, llmSrv.URL)
	output, err := act.DecomposeQueryActivity(context.Background(), DecomposeQueryInput{
		Query: "test query",
	})

	if err != nil {
		t.Fatalf("DecomposeQueryActivity failed: %v", err)
	}
	if output.QueryID == "" {
		t.Error("expected auto-generated QueryID")
	}
	if len(output.Subqueries) != 1 {
		t.Fatalf("expected 1 subquery, got %d", len(output.Subqueries))
	}
}

func TestDecomposeQueryActivity_EmptySubquery(t *testing.T) {
	// Mock LLM that returns an empty array (invalid decomposition)
	llmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := types.LLMResponse{
			Content: `[]`,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer llmSrv.Close()

	act := NewResearchActivities(nil, llmSrv.URL)
	output, err := act.DecomposeQueryActivity(context.Background(), DecomposeQueryInput{
		Query: "test query",
	})

	if err != nil {
		t.Fatalf("DecomposeQueryActivity failed: %v", err)
	}
	// Should fallback to single subquery
	if len(output.Subqueries) != 1 {
		t.Fatalf("expected 1 fallback subquery, got %d", len(output.Subqueries))
	}
	if output.Subqueries[0].Text != "test query" {
		t.Errorf("fallback subquery text = %q, want %q", output.Subqueries[0].Text, "test query")
	}
}

func TestDecomposeQueryActivity_LLMUnavailable(t *testing.T) {
	// Use an unreachable address
	act := NewResearchActivities(nil, "http://127.0.0.1:1")
	output, err := act.DecomposeQueryActivity(context.Background(), DecomposeQueryInput{
		Query: "test query",
	})

	if err != nil {
		t.Fatalf("DecomposeQueryActivity should not error on LLM failure: %v", err)
	}
	// Should fallback gracefully
	if len(output.Subqueries) != 1 {
		t.Fatalf("expected 1 fallback subquery, got %d", len(output.Subqueries))
	}
	if output.Subqueries[0].Text != "test query" {
		t.Errorf("fallback subquery text = %q, want %q", output.Subqueries[0].Text, "test query")
	}
}

func TestDecomposeQueryActivity_MarkdownJSON(t *testing.T) {
	// Mock LLM that returns JSON wrapped in markdown code block
	llmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := types.LLMResponse{
			Content: "```json\n[{\"text\": \"Sub-question 1?\"}, {\"text\": \"Sub-question 2?\"}]\n```",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer llmSrv.Close()

	act := NewResearchActivities(nil, llmSrv.URL)
	output, err := act.DecomposeQueryActivity(context.Background(), DecomposeQueryInput{
		Query: "test query",
	})

	if err != nil {
		t.Fatalf("DecomposeQueryActivity failed: %v", err)
	}
	if len(output.Subqueries) != 2 {
		t.Fatalf("expected 2 subqueries, got %d", len(output.Subqueries))
	}
	if output.Subqueries[0].Text != "Sub-question 1?" {
		t.Errorf("subquery[0].Text = %q, want %q", output.Subqueries[0].Text, "Sub-question 1?")
	}
}

// ---------------------------------------------------------------------------
// RetrieveEvidenceActivity tests
// ---------------------------------------------------------------------------

func TestRetrieveEvidenceActivity_MockServers(t *testing.T) {
	// Mock embedding server
	embedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embed" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		var req struct {
			Texts []string `json:"texts"`
			Model string   `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		vectors := make([][]float64, 1)
		vec := make([]float64, 1536)
		for j := range vec {
			vec[j] = float64(j)
		}
		vectors[0] = vec
		resp := map[string]interface{}{
			"vectors":  vectors,
			"model":    req.Model,
			"provider": "test-provider",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer embedSrv.Close()

	// Mock Qdrant search server
	qdrantSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		result := []map[string]interface{}{
			{
				"id":    "qdrant-point-1",
				"score": 0.95,
				"payload": map[string]interface{}{
					"chunk_id":    "chunk-1",
					"document_id": "doc-1",
					"title":       "Test Document",
				},
			},
			{
				"id":    "qdrant-point-2",
				"score": 0.88,
				"payload": map[string]interface{}{
					"chunk_id":    "chunk-2",
					"document_id": "doc-1",
					"title":       "Test Document",
				},
			},
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"result": result,
		})
	}))
	defer qdrantSrv.Close()

	qHost, qPortStr, err := netSplitHostPort(qdrantSrv.URL)
	if err != nil {
		t.Fatalf("parse qdrant URL: %v", err)
	}
	qPort, err := strconv.Atoi(qPortStr)
	if err != nil {
		t.Fatalf("parse qdrant port: %v", err)
	}

	embSvc := embeddings.NewService(embeddings.Config{
		BaseURL:     embedSrv.URL,
		ExpectedDim: 1536,
	})
	vdbClient, err := vectordb.NewClient(vectordb.Config{
		Host:        qHost,
		Port:        qPort,
		ExpectedDim: 1536,
	})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	ragAct := NewRAGRetrievalActivities(embSvc, vdbClient, db.NewDocumentRepository(nil))
	act := NewResearchActivities(ragAct, "http://localhost:8000")

	subquery := types.ResearchSubquery{
		ID:       uuid.New().String(),
		ParentID: "query-1",
		Text:     "What is the impact of AI?",
		Status:   "pending",
	}

	output, err := act.RetrieveEvidenceActivity(context.Background(), RetrieveEvidenceInput{
		Subquery: subquery,
		TopK:     5,
	})

	if err != nil {
		t.Fatalf("RetrieveEvidenceActivity failed: %v", err)
	}
	if output.SubqueryID != subquery.ID {
		t.Errorf("SubqueryID = %q, want %q", output.SubqueryID, subquery.ID)
	}

	// Chunk IDs should be populated from search hits
	if len(output.ChunkIDs) != 2 {
		t.Fatalf("expected 2 chunk IDs, got %d", len(output.ChunkIDs))
	}
	if output.ChunkIDs[0] != "chunk-1" {
		t.Errorf("ChunkIDs[0] = %q, want %q", output.ChunkIDs[0], "chunk-1")
	}
	if output.ChunkIDs[1] != "chunk-2" {
		t.Errorf("ChunkIDs[1] = %q, want %q", output.ChunkIDs[1], "chunk-2")
	}

	// Top score should be from the highest-scoring hit
	if output.Score != 0.95 {
		t.Errorf("Score = %f, want 0.95", output.Score)
	}

	// Summary may be empty since docRepo has nil db (fetch fails gracefully)
	// No error should be returned because we handle the fetch failure
}

func TestRetrieveEvidenceActivity_EmptyResults(t *testing.T) {
	// Mock embedding server
	embedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		vectors := make([][]float64, 1)
		vec := make([]float64, 1536)
		for j := range vec {
			vec[j] = float64(j)
		}
		vectors[0] = vec
		resp := map[string]interface{}{
			"vectors":  vectors,
			"provider": "test-provider",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer embedSrv.Close()

	// Mock Qdrant that returns empty results
	qdrantSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"result": []interface{}{},
		})
	}))
	defer qdrantSrv.Close()

	qHost, qPortStr, err := netSplitHostPort(qdrantSrv.URL)
	if err != nil {
		t.Fatalf("parse qdrant URL: %v", err)
	}
	qPort, err := strconv.Atoi(qPortStr)
	if err != nil {
		t.Fatalf("parse qdrant port: %v", err)
	}

	embSvc := embeddings.NewService(embeddings.Config{
		BaseURL:     embedSrv.URL,
		ExpectedDim: 1536,
	})
	vdbClient, err := vectordb.NewClient(vectordb.Config{
		Host:        qHost,
		Port:        qPort,
		ExpectedDim: 1536,
	})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	ragAct := NewRAGRetrievalActivities(embSvc, vdbClient, nil)
	act := NewResearchActivities(ragAct, "http://localhost:8000")

	output, err := act.RetrieveEvidenceActivity(context.Background(), RetrieveEvidenceInput{
		Subquery: types.ResearchSubquery{
			ID:       "sub-empty",
			ParentID: "query-1",
			Text:     "empty query",
			Status:   "pending",
		},
		TopK: 5,
	})

	if err != nil {
		t.Fatalf("RetrieveEvidenceActivity failed: %v", err)
	}
	if output.SubqueryID != "sub-empty" {
		t.Errorf("SubqueryID = %q, want %q", output.SubqueryID, "sub-empty")
	}
	if len(output.ChunkIDs) != 0 {
		t.Errorf("expected 0 chunk IDs, got %d", len(output.ChunkIDs))
	}
	if output.Score != 0 {
		t.Errorf("expected Score=0, got %f", output.Score)
	}
}

// ---------------------------------------------------------------------------
// SynthesizeResultActivity tests
// ---------------------------------------------------------------------------

func TestSynthesizeResultActivity_Mock(t *testing.T) {
	subqueries := []types.ResearchSubquery{
		{ID: "sub-1", ParentID: "query-1", Text: "What is the historical context?", Status: "completed"},
		{ID: "sub-2", ParentID: "query-1", Text: "What are the current trends?", Status: "completed"},
	}

	act := NewResearchActivities(nil, "http://localhost:8000")
	output, err := act.SynthesizeResultActivity(context.Background(), SynthesizeResultInput{
		QueryID:    "query-1",
		Query:      "What is the impact of AI?",
		Subqueries: subqueries,
		EvidenceText: []string{
			"Evidence for historical context",
			"Evidence for current trends",
		},
		MockLLM: true,
	})

	if err != nil {
		t.Fatalf("SynthesizeResultActivity failed: %v", err)
	}
	if !strings.Contains(output.Answer, "Mock synthesized answer") {
		t.Errorf("Answer = %q, want mock answer", output.Answer)
	}
	if len(output.Evidence) != 2 {
		t.Errorf("expected 2 evidence refs, got %d", len(output.Evidence))
	}
	if len(output.SubqueryAnswers) != 2 {
		t.Errorf("expected 2 subquery answers, got %d", len(output.SubqueryAnswers))
	}
	if output.TokenCount != 150 {
		t.Errorf("TokenCount = %d, want 150", output.TokenCount)
	}

	// Verify subquery answers
	for i, sa := range output.SubqueryAnswers {
		if sa.SubqueryID != subqueries[i].ID {
			t.Errorf("SubqueryAnswers[%d].SubqueryID = %q, want %q", i, sa.SubqueryID, subqueries[i].ID)
		}
		if sa.Confidence != 0.85 {
			t.Errorf("SubqueryAnswers[%d].Confidence = %f, want %f", i, sa.Confidence, 0.85)
		}
	}
}

func TestSynthesizeResultActivity_Mock_EmptyEvidence(t *testing.T) {
	subqueries := []types.ResearchSubquery{
		{ID: "sub-1", ParentID: "query-1", Text: "What is X?", Status: "pending"},
	}

	act := NewResearchActivities(nil, "http://localhost:8000")
	output, err := act.SynthesizeResultActivity(context.Background(), SynthesizeResultInput{
		QueryID:      "query-1",
		Query:        "Tell me about X",
		Subqueries:   subqueries,
		EvidenceText: []string{},
		MockLLM:      true,
	})

	if err != nil {
		t.Fatalf("SynthesizeResultActivity failed: %v", err)
	}
	if len(output.Evidence) != 1 {
		t.Fatalf("expected 1 evidence ref, got %d", len(output.Evidence))
	}
	// Evidence summary should fall back to subquery text
	if output.Evidence[0].Summary != "What is X?" {
		t.Errorf("Evidence[0].Summary = %q, want %q", output.Evidence[0].Summary, "What is X?")
	}
}

func TestSynthesizeResultActivity_WithMockLLM(t *testing.T) {
	llmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		resp := types.LLMResponse{
			Content: "The synthesized answer from the LLM.",
			Usage:   types.Usage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150},
			Model:   "gpt-4o-mini",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer llmSrv.Close()

	subqueries := []types.ResearchSubquery{
		{ID: "sub-1", ParentID: "query-1", Text: "What is X?", Status: "completed"},
	}

	act := NewResearchActivities(nil, llmSrv.URL)
	output, err := act.SynthesizeResultActivity(context.Background(), SynthesizeResultInput{
		QueryID:      "query-1",
		Query:        "Tell me about X",
		Subqueries:   subqueries,
		EvidenceText: []string{"Some evidence text"},
		MockLLM:      false,
	})

	if err != nil {
		t.Fatalf("SynthesizeResultActivity failed: %v", err)
	}
	if output.Answer != "The synthesized answer from the LLM." {
		t.Errorf("Answer = %q, want %q", output.Answer, "The synthesized answer from the LLM.")
	}
	if output.TokenCount != 150 {
		t.Errorf("TokenCount = %d, want 150", output.TokenCount)
	}
	if len(output.SubqueryAnswers) != 1 {
		t.Errorf("expected 1 subquery answer, got %d", len(output.SubqueryAnswers))
	}
}

func TestSynthesizeResultActivity_LLMUnavailable(t *testing.T) {
	subqueries := []types.ResearchSubquery{
		{ID: "sub-1", ParentID: "query-1", Text: "What is X?", Status: "pending"},
	}

	// Use unreachable address for LLM
	act := NewResearchActivities(nil, "http://127.0.0.1:1")
	output, err := act.SynthesizeResultActivity(context.Background(), SynthesizeResultInput{
		QueryID:      "query-1",
		Query:        "Tell me about X",
		Subqueries:   subqueries,
		EvidenceText: []string{"Some evidence"},
		MockLLM:      false,
	})

	if err != nil {
		t.Fatalf("SynthesizeResultActivity should not error on LLM failure: %v", err)
	}
	// Should fallback to evidence string as answer
	if output.Answer == "" {
		t.Error("expected non-empty fallback answer")
	}
	if len(output.Evidence) != 1 {
		t.Errorf("expected 1 evidence ref, got %d", len(output.Evidence))
	}
}

// ---------------------------------------------------------------------------
// Input/Output type tests
// ---------------------------------------------------------------------------

func TestDecomposeQueryInput_OutputTypes(t *testing.T) {
	input := DecomposeQueryInput{
		Query:      "test query",
		QueryID:    "q-1",
		AgentID:    "a-1",
		WorkflowID: "w-1",
	}
	if input.Query != "test query" {
		t.Errorf("Query = %q", input.Query)
	}
	if input.QueryID != "q-1" {
		t.Errorf("QueryID = %q", input.QueryID)
	}

	output := DecomposeQueryOutput{
		QueryID: "q-1",
		Subqueries: []types.ResearchSubquery{
			{ID: "s-1", Text: "sub?"},
		},
	}
	if output.QueryID != "q-1" {
		t.Errorf("QueryID = %q", output.QueryID)
	}
	if len(output.Subqueries) != 1 {
		t.Errorf("Subqueries length = %d", len(output.Subqueries))
	}
}

func TestRetrieveEvidenceInput_OutputTypes(t *testing.T) {
	input := RetrieveEvidenceInput{
		Subquery: types.ResearchSubquery{ID: "s-1", Text: "test"},
		TopK:     10,
	}
	if input.TopK != 10 {
		t.Errorf("TopK = %d", input.TopK)
	}

	output := RetrieveEvidenceOutput{
		SubqueryID: "s-1",
		ChunkIDs:   []string{"c-1", "c-2"},
		Summary:    "evidence text",
		Score:      0.95,
	}
	if len(output.ChunkIDs) != 2 {
		t.Errorf("ChunkIDs length = %d", len(output.ChunkIDs))
	}
	if output.Score != 0.95 {
		t.Errorf("Score = %f", output.Score)
	}
	if output.Summary != "evidence text" {
		t.Errorf("Summary = %q", output.Summary)
	}
}

func TestSynthesizeResultInput_OutputTypes(t *testing.T) {
	input := SynthesizeResultInput{
		QueryID:      "q-1",
		Query:        "test query",
		EvidenceText: []string{"evidence"},
		MockLLM:      true,
	}
	if !input.MockLLM {
		t.Errorf("MockLLM should be true")
	}

	output := SynthesizeResultOutput{
		Answer: "synthesized answer",
		Evidence: []types.EvidenceReference{
			{SubqueryID: "s-1", ChunkCount: 2, TopScore: 0.9},
		},
		SubqueryAnswers: []types.SubqueryAnswer{
			{SubqueryID: "s-1", Answer: "answer", Confidence: 0.85},
		},
		TokenCount: 150,
	}
	if output.Answer != "synthesized answer" {
		t.Errorf("Answer = %q", output.Answer)
	}
	if len(output.Evidence) != 1 {
		t.Errorf("Evidence length = %d", len(output.Evidence))
	}
	if len(output.SubqueryAnswers) != 1 {
		t.Errorf("SubqueryAnswers length = %d", len(output.SubqueryAnswers))
	}
	if output.TokenCount != 150 {
		t.Errorf("TokenCount = %d", output.TokenCount)
	}
}
