package activities

import (
	"context"
	"database/sql"
	"fmt"

	"cribug/internal/db"
)

// WorkspaceActivities holds the DB handle for workspace persistence.
type WorkspaceActivities struct {
	db *sql.DB
}

// NewWorkspaceActivities creates a new WorkspaceActivities instance.
func NewWorkspaceActivities(d *sql.DB) *WorkspaceActivities {
	return &WorkspaceActivities{db: d}
}

// ─── WorkspacePutActivity (Phase 7G) ────────────────────────────────────

// WorkspacePutInput stores a single workspace artifact.
type WorkspacePutInput struct {
	Ref         string `json:"ref"`
	WorkflowID  string `json:"workflow_id"`
	TaskID      string `json:"task_id,omitempty"`
	Mode        string `json:"mode,omitempty"`
	Kind        string `json:"kind"`
	Title       string `json:"title,omitempty"`
	Content     string `json:"content"`
	ContentType string `json:"content_type"`
}

// WorkspacePutResult reports success.
type WorkspacePutResult struct {
	Ref     string `json:"ref"`
	Written bool   `json:"written"`
}

// WorkspacePut persists a workspace artifact. Non-fatal by design.
func (wa *WorkspaceActivities) WorkspacePut(ctx context.Context, input WorkspacePutInput) (*WorkspacePutResult, error) {
	if wa.db == nil {
		return nil, fmt.Errorf("workspace activities: db is nil")
	}
	kind := input.Kind
	if kind == "" {
		kind = "artifact"
	}
	ct := input.ContentType
	if ct == "" {
		ct = "text/plain"
	}
	if err := db.PutWorkspaceEntry(ctx, wa.db, db.WorkspaceEntry{
		Ref:         input.Ref,
		WorkflowID:  input.WorkflowID,
		TaskID:      sql.NullString{String: input.TaskID, Valid: input.TaskID != ""},
		Mode:        sql.NullString{String: input.Mode, Valid: input.Mode != ""},
		Kind:        kind,
		Title:       sql.NullString{String: input.Title, Valid: input.Title != ""},
		Content:     input.Content,
		ContentType: ct,
	}); err != nil {
		return &WorkspacePutResult{Ref: input.Ref, Written: false}, err
	}
	return &WorkspacePutResult{Ref: input.Ref, Written: true}, nil
}
