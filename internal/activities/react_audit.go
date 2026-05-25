package activities

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	"go.temporal.io/sdk/activity"
)

// ReActAuditActivities handles audit logging for ReAct steps
type ReActAuditActivities struct {
	db *sql.DB
}

// NewReActAuditActivities creates a new ReActAuditActivities
func NewReActAuditActivities(db *sql.DB) *ReActAuditActivities {
	return &ReActAuditActivities{db: db}
}

// SaveReActStepAuditInput is the input for SaveReActStepAuditActivity
type SaveReActStepAuditInput struct {
	WorkflowID  string `json:"workflow_id"`
	RunID       string `json:"run_id"`
	NodeID      string `json:"node_id"`
	StepIndex   int    `json:"step_index"`
	Reasoning   string `json:"reasoning"`
	Action      string `json:"action"`
	Observation string `json:"observation"`
	CreatedAtNs int64  `json:"created_at_ns"`
}

// SaveReActStepAuditActivity writes a ReAct step to Postgres for audit purposes.
// This is audit-only - failures are logged but do NOT block the main workflow.
// Uses INSERT ... ON CONFLICT DO NOTHING for idempotency.
func (a *ReActAuditActivities) SaveReActStepAudit(ctx context.Context, input SaveReActStepAuditInput) error {
	logger := activity.GetLogger(ctx)
	logger.Info("SaveReActStepAuditActivity started",
		"workflow_id", input.WorkflowID,
		"node_id", input.NodeID,
		"step_index", input.StepIndex)

	if a.db == nil {
		logger.Warn("SaveReActStepAudit: no db connection, skipping audit write")
		return nil
	}

	createdAt := time.Unix(0, input.CreatedAtNs).UTC()

	query := `
		INSERT INTO react_steps (workflow_id, run_id, node_id, step_index, reasoning, action, observation, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (workflow_id, run_id, node_id, step_index) DO NOTHING
	`

	_, err := a.db.ExecContext(ctx, query,
		input.WorkflowID,
		input.RunID,
		input.NodeID,
		input.StepIndex,
		input.Reasoning,
		input.Action,
		input.Observation,
		createdAt,
	)

	if err != nil {
		// Audit failure must not block main flow, only log
		log.Printf("[WARN] SaveReActStepAudit: failed to write audit record: %v", err)
		logger.Warn("SaveReActStepAudit failed (non-blocking)", "error", err)
		return nil
	}

	logger.Info("SaveReActStepAudit completed",
		"workflow_id", input.WorkflowID,
		"step_index", input.StepIndex)

	return nil
}

// EnsureReActStepTable creates the react_steps table if it doesn't exist.
// Called during worker startup for migration-lite setups.
func EnsureReActStepTable(db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}

	query := `
		CREATE TABLE IF NOT EXISTS react_steps (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			workflow_id VARCHAR(128) NOT NULL,
			run_id VARCHAR(128) NOT NULL,
			node_id VARCHAR(128) NOT NULL,
			step_index INTEGER NOT NULL,
			reasoning TEXT NOT NULL DEFAULT '',
			action TEXT NOT NULL DEFAULT '',
			observation TEXT,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			UNIQUE(workflow_id, run_id, node_id, step_index)
		)
	`

	_, err := db.Exec(query)
	if err != nil {
		return fmt.Errorf("create react_steps table: %w", err)
	}

	// Ensure indexes exist
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_react_steps_workflow_id ON react_steps(workflow_id)`,
		`CREATE INDEX IF NOT EXISTS idx_react_steps_task ON react_steps(workflow_id, run_id)`,
		`CREATE INDEX IF NOT EXISTS idx_react_steps_created ON react_steps(created_at)`,
	}

	for _, idx := range indexes {
		if _, err := db.Exec(idx); err != nil {
			return fmt.Errorf("create index: %w", err)
		}
	}

	return nil
}
