package activities

import (
	"context"
	"database/sql"
	"fmt"
	"log"

	"go.temporal.io/sdk/activity"
)

type ExecutionActivities struct {
	db *sql.DB
}

func NewExecutionActivities(db *sql.DB) *ExecutionActivities {
	return &ExecutionActivities{db: db}
}

type RecordExecutionInput struct {
	TaskID     string
	WorkflowID string
	RunID      string
}

type RecordExecutionFailedInput struct {
	TaskID     string
	WorkflowID string
	RunID      string
	ErrorType  string
	ErrorMsg   string
}

func (a *ExecutionActivities) RecordCompleted(ctx context.Context, input RecordExecutionInput) error {
	logger := activity.GetLogger(ctx)
	logger.Info("RecordExecutionCompletedActivity started", "task_id", input.TaskID, "workflow_id", input.WorkflowID, "run_id", input.RunID)

	query := `
		UPDATE executions
		SET status = 'completed', completed_at = NOW(), updated_at = NOW()
		WHERE task_id = $1 AND workflow_id = $2 AND status = 'running'
		RETURNING id`

	var id string
	err := a.db.QueryRowContext(ctx, query, input.TaskID, input.WorkflowID).Scan(&id)
	if err == sql.ErrNoRows {
		logger.Warn("RecordExecutionCompletedActivity: no matching row to update",
			"task_id", input.TaskID, "workflow_id", input.WorkflowID, "run_id", input.RunID)
		return nil
	}
	if err != nil {
		logger.Error("RecordExecutionCompletedActivity failed", "error", err)
		return fmt.Errorf("update execution completed: %w", err)
	}

	log.Printf("[INFO] RecordExecutionCompletedActivity: task=%s workflow=%s run=%s",
		input.TaskID, input.WorkflowID, input.RunID)
	logger.Info("RecordExecutionCompletedActivity completed", "task_id", input.TaskID)
	return nil
}

func (a *ExecutionActivities) RecordFailed(ctx context.Context, input RecordExecutionFailedInput) error {
	logger := activity.GetLogger(ctx)
	logger.Info("RecordExecutionFailedActivity started", "task_id", input.TaskID)

	query := `
		UPDATE executions
		SET status = 'failed', error_type = $4, error = $5, completed_at = NOW(), updated_at = NOW()
		WHERE task_id = $1 AND workflow_id = $2 AND run_id = $3 AND status = 'running'
		RETURNING id`

	var id string
	err := a.db.QueryRowContext(ctx, query,
		input.TaskID, input.WorkflowID, input.RunID, input.ErrorType, input.ErrorMsg).Scan(&id)
	if err == sql.ErrNoRows {
		logger.Warn("RecordExecutionFailedActivity: no matching row to update",
			"task_id", input.TaskID, "workflow_id", input.WorkflowID, "run_id", input.RunID)
		return nil
	}
	if err != nil {
		logger.Error("RecordExecutionFailedActivity failed", "error", err)
		return fmt.Errorf("update execution failed: %w", err)
	}

	log.Printf("[INFO] RecordExecutionFailedActivity: task=%s error_type=%s",
		input.TaskID, input.ErrorType)
	logger.Info("RecordExecutionFailedActivity completed", "task_id", input.TaskID)
	return nil
}

func CreateExecution(db *sql.DB, id, taskID, workflowID, runID string) error {
	query := `
		INSERT INTO executions (id, task_id, workflow_id, run_id, status, started_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'running', NOW(), NOW(), NOW())
		ON CONFLICT (workflow_id, run_id) DO NOTHING`

	log.Printf("[DEBUG] CreateExecution SQL: id=%s task_id=%s workflow_id=%s run_id=%s", id, taskID, workflowID, runID)
	_, err := db.ExecContext(context.Background(), query, id, taskID, workflowID, runID)
	if err != nil {
		log.Printf("[ERROR] CreateExecution failed: %v", err)
	}
	return err
}