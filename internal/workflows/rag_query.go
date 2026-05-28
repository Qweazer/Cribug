package workflows

import (
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const RAGQueryWorkflowName = "RAGQueryWorkflow"

// ── Workflow input/output types ────────────────────────────────────────────

type RAGQueryWorkflowInput struct {
	QueryText  string
	Collection string
	Model      string
	TopK       int
	Threshold  float64
	MaxTokens  int
	AgentID    string
	WorkflowID string
}

type RAGQueryWorkflowOutput struct {
	Context       string
	Citations     []string
	HitCount      int
	TokenEstimate int
}

// ── Activity input/output types ──────────────────────────────────────────
// These match the types being created simultaneously in internal/activities/rag_retrieval.go.

type RAGSearchInput struct {
	QueryText  string
	Collection string
	Model      string
	TopK       int
	Threshold  float64
}

type RAGSearchHit struct {
	ChunkID       string
	QdrantPointID string
	Score         float64
	Payload       map[string]interface{}
}

type RAGSearchOutput struct {
	Hits []RAGSearchHit
}

type RAGFetchInput struct {
	ChunkIDs []string
}

type RAGChunkContent struct {
	ChunkID     string
	DocumentID  string
	Content     string
	ContentHash string
	Title       string
	ChunkIndex  int
	Score       float64
}

type RAGFetchOutput struct {
	Chunks []RAGChunkContent
}

type RAGPackInput struct {
	Query     string
	Chunks    []RAGChunkContent
	MaxTokens int
}

type RAGPackOutput struct {
	Context       string
	Citations     []string
	TokenEstimate int
	ChunkCount    int
}

// RAGQueryWorkflow runs a RAG query end-to-end:
//  1. Embed the query text and search the vector store for similar chunks
//  2. Fetch the full chunk content from Postgres
//  3. Pack the chunks into a context string with citations
//
// NO direct HTTP, DB, Redis, LLM, goroutine, time.Now, rand, uuid.New in body.
func RAGQueryWorkflow(ctx workflow.Context, input RAGQueryWorkflowInput) (RAGQueryWorkflowOutput, error) {
	ao := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 3,
		},
	})

	// Step 1: Embed query + search vector store (vectors stay in Activity)
	var searchOutput RAGSearchOutput
	err := workflow.ExecuteActivity(ao, "EmbedAndSearchChunksActivity", RAGSearchInput{
		QueryText:  input.QueryText,
		Collection: input.Collection,
		Model:      input.Model,
		TopK:       input.TopK,
		Threshold:  input.Threshold,
	}).Get(ctx, &searchOutput)
	if err != nil {
		return RAGQueryWorkflowOutput{}, err
	}

	// If no hits, return early with empty result
	if len(searchOutput.Hits) == 0 {
		return RAGQueryWorkflowOutput{}, nil
	}

	// Step 2: Fetch chunk content from Postgres
	chunkIDs := make([]string, len(searchOutput.Hits))
	for i, h := range searchOutput.Hits {
		chunkIDs[i] = h.ChunkID
	}

	var fetchOutput RAGFetchOutput
	err = workflow.ExecuteActivity(ao, "FetchChunkContentActivity", RAGFetchInput{
		ChunkIDs: chunkIDs,
	}).Get(ctx, &fetchOutput)
	if err != nil {
		return RAGQueryWorkflowOutput{}, err
	}

	// Step 3: Pack context from fetched chunks
	var packOutput RAGPackOutput
	err = workflow.ExecuteActivity(ao, "PackContextActivity", RAGPackInput{
		Query:     input.QueryText,
		Chunks:    fetchOutput.Chunks,
		MaxTokens: input.MaxTokens,
	}).Get(ctx, &packOutput)
	if err != nil {
		return RAGQueryWorkflowOutput{}, err
	}

	return RAGQueryWorkflowOutput{
		Context:       packOutput.Context,
		Citations:     packOutput.Citations,
		HitCount:      packOutput.ChunkCount,
		TokenEstimate: packOutput.TokenEstimate,
	}, nil
}
