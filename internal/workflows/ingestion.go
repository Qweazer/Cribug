package workflows

import (
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const DocumentIngestionWorkflowName = "DocumentIngestionWorkflow"

type DocumentIngestionWorkflowInput struct {
	TenantID   string
	SourceType string
	SourceURI  string
	Title      string
	Content    string
	Collection string
	Model      string
	AgentID    string
	WorkflowID string
}

type DocumentIngestionWorkflowOutput struct {
	DocumentID   string
	IsDuplicate  bool
	ChunkCount   int
	IndexedCount int
	Status       string
}

// ── Activity input/output types ──────────────────────────────────────────
// These match the types being created simultaneously in internal/activities/ingestion.go.

type SaveDocumentInput struct {
	TenantID   string
	SourceType string
	SourceURI  string
	Title      string
	Content    string
}

type SaveDocumentOutput struct {
	DocumentID  string
	IsDuplicate bool
}

type ChunkDocumentInput struct {
	DocumentID string
}

type ChunkDocumentOutput struct {
	ChunkCount int
}

type EmbedAndUpsertInput struct {
	DocumentID string
	Collection string
	Model      string
}

type EmbedAndUpsertOutput struct {
	IndexedCount int
}

type UpdateIndexStatusInput struct {
	DocumentID string
	Status     string
	Error      string
}

type UpdateIndexStatusOutput struct {
	Status string
}

// DocumentIngestionWorkflow ingests a document end-to-end:
//  1. Save metadata to document store
//  2. Chunk the document
//  3. Embed and upsert chunks into vector store
//  4. Update document index status
//
// No direct HTTP, DB, Redis, LLM, goroutine, time.Now, rand, uuid.New in body.
func DocumentIngestionWorkflow(ctx workflow.Context, input DocumentIngestionWorkflowInput) (DocumentIngestionWorkflowOutput, error) {
	ao := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Minute, // embedding can be slow
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 3,
		},
	})

	// Step 1: Save document metadata
	var saveOutput SaveDocumentOutput
	err := workflow.ExecuteActivity(ao, "SaveDocumentMetadataActivity", SaveDocumentInput{
		TenantID:   input.TenantID,
		SourceType: input.SourceType,
		SourceURI:  input.SourceURI,
		Title:      input.Title,
		Content:    input.Content,
	}).Get(ctx, &saveOutput)
	if err != nil {
		return DocumentIngestionWorkflowOutput{}, err
	}

	// If duplicate, return early
	if saveOutput.IsDuplicate {
		return DocumentIngestionWorkflowOutput{
			DocumentID:  saveOutput.DocumentID,
			IsDuplicate: true,
			Status:      "duplicate",
		}, nil
	}

	documentID := saveOutput.DocumentID

	// Step 2: Chunk document
	var chunkOutput ChunkDocumentOutput
	err = workflow.ExecuteActivity(ao, "ChunkDocumentActivity", ChunkDocumentInput{
		DocumentID: documentID,
	}).Get(ctx, &chunkOutput)
	if err != nil {
		// Mark document failed (best-effort)
		_ = workflow.ExecuteActivity(ao, "UpdateDocumentIndexStatusActivity", UpdateIndexStatusInput{
			DocumentID: documentID,
			Status:     "failed",
			Error:      err.Error(),
		}).Get(ctx, nil)
		return DocumentIngestionWorkflowOutput{}, err
	}

	// Step 3: Embed and upsert chunks (vectors stay inside Activity)
	var embedOutput EmbedAndUpsertOutput
	err = workflow.ExecuteActivity(ao, "EmbedAndUpsertChunksActivity", EmbedAndUpsertInput{
		DocumentID: documentID,
		Collection: input.Collection,
		Model:      input.Model,
	}).Get(ctx, &embedOutput)
	if err != nil {
		_ = workflow.ExecuteActivity(ao, "UpdateDocumentIndexStatusActivity", UpdateIndexStatusInput{
			DocumentID: documentID,
			Status:     "failed",
			Error:      err.Error(),
		}).Get(ctx, nil)
		return DocumentIngestionWorkflowOutput{}, err
	}

	// Step 4: Update document status to indexed
	var statusOutput UpdateIndexStatusOutput
	err = workflow.ExecuteActivity(ao, "UpdateDocumentIndexStatusActivity", UpdateIndexStatusInput{
		DocumentID: documentID,
		Status:     "indexed",
	}).Get(ctx, &statusOutput)
	if err != nil {
		workflow.GetLogger(ctx).Error("Failed to update document status", "error", err)
	}

	return DocumentIngestionWorkflowOutput{
		DocumentID:   documentID,
		ChunkCount:   chunkOutput.ChunkCount,
		IndexedCount: embedOutput.IndexedCount,
		Status:       statusOutput.Status,
	}, nil
}
