package activities

import (
	"context"
	"database/sql"
	"fmt"
	"log"

	redisclient "cribug/internal/redis"

	"go.temporal.io/sdk/activity"
)

type TaskActivities struct {
	db    *sql.DB
	redis *redisclient.Client
}

func NewTaskActivities(db *sql.DB, redisClient *redisclient.Client) *TaskActivities {
	return &TaskActivities{db: db, redis: redisClient}
}

type SaveResultInput struct {
	TaskID string
	Result string
}

type SaveFailureInput struct {
	TaskID    string
	ErrorType string
	ErrorMsg  string
}

func (a *TaskActivities) SaveResult(ctx context.Context, input SaveResultInput) error {
	logger := activity.GetLogger(ctx)
	logger.Info("SaveResultActivity started", "task_id", input.TaskID)

	query := `
		UPDATE tasks
		SET status = 'completed', result = $2, updated_at = NOW()
		WHERE id = $1 AND status = 'running'`

	result, err := a.db.ExecContext(ctx, query, input.TaskID, input.Result)
	if err != nil {
		logger.Error("SaveResultActivity failed", "error", err)
		return fmt.Errorf("update task result: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		logger.Error("SaveResultActivity: failed to get rows affected", "error", err)
		return fmt.Errorf("get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		logger.Warn("SaveResultActivity: no matching row to update", "task_id", input.TaskID)
		return nil
	}

	if a.redis != nil {
		a.redis.UpdateTaskCompleted(ctx, input.TaskID, input.Result)
	}

	logger.Info("SaveResultActivity completed", "task_id", input.TaskID, "rows_affected", rowsAffected)
	return nil
}

func (a *TaskActivities) SaveFailure(ctx context.Context, input SaveFailureInput) error {
	logger := activity.GetLogger(ctx)
	logger.Info("SaveFailureActivity started", "task_id", input.TaskID)

	query := `
		UPDATE tasks
		SET status = 'failed', error_type = $2, error = $3, updated_at = NOW()
		WHERE id = $1 AND status IN ('pending', 'running')`

	result, err := a.db.ExecContext(ctx, query, input.TaskID, input.ErrorType, input.ErrorMsg)
	if err != nil {
		logger.Error("SaveFailureActivity failed", "error", err)
		return fmt.Errorf("update task failure: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		logger.Error("SaveFailureActivity: failed to get rows affected", "error", err)
		return fmt.Errorf("get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		logger.Warn("SaveFailureActivity: no matching row to update", "task_id", input.TaskID)
		return nil
	}

	if a.redis != nil {
		a.redis.UpdateTaskError(ctx, input.TaskID, input.ErrorType, input.ErrorMsg)
	}

	log.Printf("[INFO] SaveFailureActivity: task=%s error_type=%s", input.TaskID, input.ErrorType)
	logger.Info("SaveFailureActivity completed", "task_id", input.TaskID)
	return nil
}