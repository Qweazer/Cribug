package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"cribug/internal/types"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type Postgres struct {
	db *sql.DB
}

func New(databaseURL string) (*Postgres, error) {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	return &Postgres{db: db}, nil
}

func (p *Postgres) Close() error {
	return p.db.Close()
}

func (p *Postgres) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return p.db.PingContext(ctx)
}

func (p *Postgres) CreateTask(ctx context.Context, task *types.Task) error {
	query := `
		INSERT INTO tasks (
			id, session_id, query, status, workflow_id, model,
			max_total_tokens, max_completion_tokens,
			created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10
		)`

	_, err := p.db.ExecContext(ctx, query,
		task.ID,
		task.SessionID,
		task.Query,
		task.Status,
		task.WorkflowID,
		task.Model,
		task.MaxTotalTokens,
		task.MaxCompletionTokens,
		task.CreatedAt,
		task.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert task: %w", err)
	}

	return nil
}

func (p *Postgres) GetTaskByID(ctx context.Context, id string) (*types.Task, error) {
	query := `
		SELECT id, session_id, query, status, result, error_type, error,
			   max_total_tokens, max_completion_tokens, model, workflow_id,
			   run_id, usage_prompt_tokens, usage_completion_tokens,
			   usage_total_tokens, created_at, updated_at
		FROM tasks
		WHERE id = $1`

	task := &types.Task{}
	err := p.db.QueryRowContext(ctx, query, id).Scan(
		&task.ID,
		&task.SessionID,
		&task.Query,
		&task.Status,
		&task.Result,
		&task.ErrorType,
		&task.Error,
		&task.MaxTotalTokens,
		&task.MaxCompletionTokens,
		&task.Model,
		&task.WorkflowID,
		&task.RunID,
		&task.UsagePromptTokens,
		&task.UsageCompletionTokens,
		&task.UsageTotalTokens,
		&task.CreatedAt,
		&task.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get task: %w", err)
	}

	return task, nil
}

// Stdlib returns the underlying sql.DB for compatibility
func (p *Postgres) Stdlib() *sql.DB {
	return p.db
}

func (p *Postgres) UpdateTaskRunning(ctx context.Context, taskID, runID string) error {
	query := `UPDATE tasks SET status = 'running', run_id = $2, updated_at = NOW() WHERE id = $1`
	_, err := p.db.ExecContext(ctx, query, taskID, runID)
	if err != nil {
		return fmt.Errorf("update task running: %w", err)
	}
	return nil
}

func (p *Postgres) UpdateTaskError(ctx context.Context, taskID, errorType, errorMsg string) error {
	query := `UPDATE tasks SET status = 'failed', error_type = $2, error = $3, updated_at = NOW() WHERE id = $1`
	_, err := p.db.ExecContext(ctx, query, taskID, errorType, errorMsg)
	if err != nil {
		return fmt.Errorf("update task error: %w", err)
	}
	return nil
}