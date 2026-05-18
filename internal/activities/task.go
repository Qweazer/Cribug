package activities

import (
	"context"
	"database/sql"
	"fmt"
	"log"

	redisclient "cribug/internal/redis"
	"cribug/internal/types"

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
	TaskID         string
	Result         string
	PromptTokens   *int
	CompletionTokens *int
	TotalTokens    *int
}

type SaveFailureInput struct {
	TaskID    string
	ErrorType string
	ErrorMsg  string
}

func (a *TaskActivities) SaveResult(ctx context.Context, input SaveResultInput) error {
	logger := activity.GetLogger(ctx)
	logger.Info("SaveResultActivity started", "task_id", input.TaskID)

	// Build dynamic UPDATE with optional usage fields
	query := `UPDATE tasks SET status = 'completed', result = $2, updated_at = NOW()`
	args := []interface{}{input.TaskID, input.Result}
	argIdx := 3

	if input.PromptTokens != nil {
		query += fmt.Sprintf(", usage_prompt_tokens = $%d", argIdx)
		args = append(args, *input.PromptTokens)
		argIdx++
	}
	if input.CompletionTokens != nil {
		query += fmt.Sprintf(", usage_completion_tokens = $%d", argIdx)
		args = append(args, *input.CompletionTokens)
		argIdx++
	}
	if input.TotalTokens != nil {
		query += fmt.Sprintf(", usage_total_tokens = $%d", argIdx)
		args = append(args, *input.TotalTokens)
		argIdx++
	}

	query += ` WHERE id = $1 AND status = 'running'`

	result, err := a.db.ExecContext(ctx, query, args...)
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

	status := "failed"
	if input.ErrorType == types.TaskStatusBudgetExceeded {
		status = "budget_exceeded"
	}

	query := `
		UPDATE tasks
		SET status = $4, error_type = $2, error = $3, updated_at = NOW()
		WHERE id = $1 AND status IN ('pending', 'running')`

	result, err := a.db.ExecContext(ctx, query, input.TaskID, input.ErrorType, input.ErrorMsg, status)
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
		if input.ErrorType == types.TaskStatusBudgetExceeded {
			a.redis.UpdateTaskBudgetExceeded(ctx, input.TaskID, input.ErrorMsg)
		} else {
			a.redis.UpdateTaskError(ctx, input.TaskID, input.ErrorType, input.ErrorMsg)
		}
	}

	log.Printf("[INFO] SaveFailureActivity: task=%s error_type=%s", input.TaskID, input.ErrorType)
	logger.Info("SaveFailureActivity completed", "task_id", input.TaskID)
	return nil
}

func (a *TaskActivities) SaveBudgetExceeded(ctx context.Context, taskID, reason string) error {
	logger := activity.GetLogger(ctx)
	logger.Info("SaveBudgetExceededActivity started", "task_id", taskID)

	query := `
		UPDATE tasks
		SET status = 'budget_exceeded', error_type = 'budget_exceeded', error = $2, updated_at = NOW()
		WHERE id = $1 AND status IN ('pending', 'running')`

	result, err := a.db.ExecContext(ctx, query, taskID, reason)
	if err != nil {
		logger.Error("SaveBudgetExceededActivity failed", "error", err)
		return fmt.Errorf("update task budget exceeded: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		logger.Error("SaveBudgetExceededActivity: failed to get rows affected", "error", err)
		return fmt.Errorf("get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		logger.Warn("SaveBudgetExceededActivity: no matching row to update", "task_id", taskID)
		return nil
	}

	if a.redis != nil {
		a.redis.UpdateTaskBudgetExceeded(ctx, taskID, reason)
	}

	log.Printf("[INFO] SaveBudgetExceededActivity: task=%s reason=%s", taskID, reason)
	logger.Info("SaveBudgetExceededActivity completed", "task_id", taskID)
	return nil
}