package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// WorkspaceEntry is a single stored artifact in the workspace store.
// Uses sql.Null* types to match the DB schema; consumers may use the
// typed WorkspaceEntryDomain type below for JSON serialization.
type WorkspaceEntry struct {
	ID          string         `db:"id"`
	Ref         string         `db:"ref"`
	WorkflowID  string         `db:"workflow_id"`
	TaskID      sql.NullString `db:"task_id"`
	Mode        sql.NullString `db:"mode"`
	Kind        string         `db:"kind"`
	Title       sql.NullString `db:"title"`
	Content     string         `db:"content"`
	ContentType string         `db:"content_type"`
	Metadata    sql.NullString `db:"metadata"`
	CreatedAt   time.Time      `db:"created_at"`
	UpdatedAt   time.Time      `db:"updated_at"`
}

// WorkspaceEntryDomain is the API-friendly form (no sql.Null*).
type WorkspaceEntryDomain struct {
	ID          string                 `json:"id"`
	Ref         string                 `json:"ref"`
	WorkflowID  string                 `json:"workflow_id"`
	TaskID      string                 `json:"task_id,omitempty"`
	Mode        string                 `json:"mode,omitempty"`
	Kind        string                 `json:"kind"`
	Title       string                 `json:"title,omitempty"`
	Content     string                 `json:"content"`
	ContentType string                 `json:"content_type"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt   string                 `json:"created_at"`
}

// PutWorkspaceEntry inserts or updates a workspace entry by ref.
func PutWorkspaceEntry(ctx context.Context, db *sql.DB, we WorkspaceEntry) error {
	if db == nil {
		return fmt.Errorf("nil db")
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO workspace_entries (ref, workflow_id, task_id, mode, kind, title, content, content_type, metadata, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb, NOW(), NOW())
		ON CONFLICT (ref) DO UPDATE SET
			content = EXCLUDED.content,
			title = COALESCE(NULLIF(EXCLUDED.title,''), workspace_entries.title),
			metadata = COALESCE(EXCLUDED.metadata, workspace_entries.metadata),
			updated_at = NOW()
	`, we.Ref, we.WorkflowID, we.TaskID, we.Mode, we.Kind, we.Title, we.Content, we.ContentType, we.Metadata)
	if err != nil {
		return fmt.Errorf("put workspace entry: %w", err)
	}
	return nil
}

// GetWorkspaceEntryByRef returns a single workspace entry by its ref.
func GetWorkspaceEntryByRef(ctx context.Context, db *sql.DB, ref string) (*WorkspaceEntry, error) {
	if db == nil {
		return nil, fmt.Errorf("nil db")
	}
	we := &WorkspaceEntry{}
	err := db.QueryRowContext(ctx, `
		SELECT id, ref, workflow_id, COALESCE(task_id,''), COALESCE(mode,''),
		       kind, COALESCE(title,''), content, content_type,
		       COALESCE(metadata::text,''), created_at, updated_at
		FROM workspace_entries WHERE ref = $1
	`, ref).Scan(&we.ID, &we.Ref, &we.WorkflowID, &we.TaskID, &we.Mode,
		&we.Kind, &we.Title, &we.Content, &we.ContentType,
		&we.Metadata, &we.CreatedAt, &we.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get workspace entry: %w", err)
	}
	return we, nil
}

// ListWorkspaceEntriesByWorkflow returns all workspace entries for a workflow.
func ListWorkspaceEntriesByWorkflow(ctx context.Context, db *sql.DB, workflowID string) ([]WorkspaceEntry, error) {
	if db == nil {
		return nil, fmt.Errorf("nil db")
	}
	rows, err := db.QueryContext(ctx, `
		SELECT id, ref, workflow_id, COALESCE(task_id,''), COALESCE(mode,''),
		       kind, COALESCE(title,''), content, content_type,
		       COALESCE(metadata::text,''), created_at, updated_at
		FROM workspace_entries WHERE workflow_id = $1 ORDER BY created_at
	`, workflowID)
	if err != nil {
		return nil, fmt.Errorf("list workspace entries: %w", err)
	}
	defer rows.Close()
	var out []WorkspaceEntry
	for rows.Next() {
		var we WorkspaceEntry
		if err := rows.Scan(&we.ID, &we.Ref, &we.WorkflowID, &we.TaskID, &we.Mode,
			&we.Kind, &we.Title, &we.Content, &we.ContentType,
			&we.Metadata, &we.CreatedAt, &we.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan workspace entry: %w", err)
		}
		out = append(out, we)
	}
	return out, rows.Err()
}

// ToDomain converts a DB row to the API-friendly form.
func (we *WorkspaceEntry) ToDomain() WorkspaceEntryDomain {
	d := WorkspaceEntryDomain{
		ID:          we.ID,
		Ref:         we.Ref,
		WorkflowID:  we.WorkflowID,
		Kind:        we.Kind,
		Content:     we.Content,
		ContentType: we.ContentType,
		CreatedAt:   we.CreatedAt.UTC().Format(time.RFC3339),
	}
	if we.TaskID.Valid {
		d.TaskID = we.TaskID.String
	}
	if we.Mode.Valid {
		d.Mode = we.Mode.String
	}
	if we.Title.Valid {
		d.Title = we.Title.String
	}
	if we.Metadata.Valid && we.Metadata.String != "" && we.Metadata.String != "null" {
		_ = json.Unmarshal([]byte(we.Metadata.String), &d.Metadata)
	}
	return d
}
