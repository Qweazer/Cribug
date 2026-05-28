package workflows

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func setupDocumentIngestionTest(t *testing.T) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()
	env.RegisterWorkflowWithOptions(DocumentIngestionWorkflow, workflow.RegisterOptions{Name: DocumentIngestionWorkflowName})

	// Register all activities as stubs (env.OnActivity overrides per-test)
	env.RegisterActivityWithOptions(
		func(ctx interface{}, input interface{}) (interface{}, error) {
			return SaveDocumentOutput{}, nil
		},
		activity.RegisterOptions{Name: "SaveDocumentMetadataActivity"},
	)
	env.RegisterActivityWithOptions(
		func(ctx interface{}, input interface{}) (interface{}, error) {
			return ChunkDocumentOutput{}, nil
		},
		activity.RegisterOptions{Name: "ChunkDocumentActivity"},
	)
	env.RegisterActivityWithOptions(
		func(ctx interface{}, input interface{}) (interface{}, error) {
			return EmbedAndUpsertOutput{}, nil
		},
		activity.RegisterOptions{Name: "EmbedAndUpsertChunksActivity"},
	)
	env.RegisterActivityWithOptions(
		func(ctx interface{}, input interface{}) (interface{}, error) {
			return UpdateIndexStatusOutput{}, nil
		},
		activity.RegisterOptions{Name: "UpdateDocumentIndexStatusActivity"},
	)

	return env
}

func TestDocumentIngestionWorkflow_Success(t *testing.T) {
	env := setupDocumentIngestionTest(t)

	env.OnActivity("SaveDocumentMetadataActivity", mock.Anything, mock.Anything).
		Return(SaveDocumentOutput{DocumentID: "doc-1"}, nil)
	env.OnActivity("ChunkDocumentActivity", mock.Anything, mock.Anything).
		Return(ChunkDocumentOutput{ChunkCount: 5}, nil)
	env.OnActivity("EmbedAndUpsertChunksActivity", mock.Anything, mock.Anything).
		Return(EmbedAndUpsertOutput{IndexedCount: 5}, nil)
	env.OnActivity("UpdateDocumentIndexStatusActivity", mock.Anything, mock.Anything).
		Return(UpdateIndexStatusOutput{Status: "indexed"}, nil)

	env.ExecuteWorkflow(DocumentIngestionWorkflowName, DocumentIngestionWorkflowInput{
		TenantID:  "tenant-1",
		SourceURI: "s3://bucket/doc.txt",
		Title:     "Test Document",
		Content:   "Hello World",
		Model:     "text-embedding-ada-002",
		AgentID:   "agent-1",
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}

	var result DocumentIngestionWorkflowOutput
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("workflow result error: %v", err)
	}
	if result.DocumentID != "doc-1" {
		t.Errorf("expected DocumentID 'doc-1', got '%s'", result.DocumentID)
	}
	if result.IsDuplicate {
		t.Error("expected IsDuplicate to be false")
	}
	if result.ChunkCount != 5 {
		t.Errorf("expected ChunkCount 5, got %d", result.ChunkCount)
	}
	if result.IndexedCount != 5 {
		t.Errorf("expected IndexedCount 5, got %d", result.IndexedCount)
	}
	if result.Status != "indexed" {
		t.Errorf("expected Status 'indexed', got '%s'", result.Status)
	}
}

func TestDocumentIngestionWorkflow_Duplicate(t *testing.T) {
	env := setupDocumentIngestionTest(t)

	// Save returns IsDuplicate=true; workflow should stop early
	env.OnActivity("SaveDocumentMetadataActivity", mock.Anything, mock.Anything).
		Return(SaveDocumentOutput{DocumentID: "doc-dup", IsDuplicate: true}, nil)
	// No other activities should be called

	env.ExecuteWorkflow(DocumentIngestionWorkflowName, DocumentIngestionWorkflowInput{
		TenantID:  "tenant-1",
		SourceURI: "s3://bucket/doc.txt",
		Title:     "Duplicate Document",
		Content:   "Hello World",
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}

	var result DocumentIngestionWorkflowOutput
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("workflow result error: %v", err)
	}
	if !result.IsDuplicate {
		t.Error("expected IsDuplicate to be true")
	}
	if result.DocumentID != "doc-dup" {
		t.Errorf("expected DocumentID 'doc-dup', got '%s'", result.DocumentID)
	}
	if result.Status != "duplicate" {
		t.Errorf("expected Status 'duplicate', got '%s'", result.Status)
	}
	if result.ChunkCount != 0 {
		t.Errorf("expected ChunkCount 0 for duplicate, got %d", result.ChunkCount)
	}
	if result.IndexedCount != 0 {
		t.Errorf("expected IndexedCount 0 for duplicate, got %d", result.IndexedCount)
	}
}

func TestDocumentIngestionWorkflow_ChunkFailure(t *testing.T) {
	env := setupDocumentIngestionTest(t)

	env.OnActivity("SaveDocumentMetadataActivity", mock.Anything, mock.Anything).
		Return(SaveDocumentOutput{DocumentID: "doc-1"}, nil)
	env.OnActivity("ChunkDocumentActivity", mock.Anything, mock.Anything).
		Return(ChunkDocumentOutput{}, errors.New("chunking failed"))
	// UpdateDocumentIndexStatusActivity should be called to mark document as failed
	env.OnActivity("UpdateDocumentIndexStatusActivity", mock.Anything, mock.Anything).
		Return(UpdateIndexStatusOutput{Status: "failed"}, nil)

	env.ExecuteWorkflow(DocumentIngestionWorkflowName, DocumentIngestionWorkflowInput{
		TenantID:  "tenant-1",
		SourceURI: "s3://bucket/doc.txt",
		Title:     "Failing Document",
		Content:   "Hello World",
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}

	var result DocumentIngestionWorkflowOutput
	err := env.GetWorkflowResult(&result)
	if err == nil {
		t.Fatal("expected workflow error on chunk failure")
	}
	if !strings.Contains(err.Error(), "chunking failed") {
		t.Errorf("expected error containing 'chunking failed', got '%v'", err)
	}
}
