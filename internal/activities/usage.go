package activities

import (
	"context"
	"database/sql"
	"fmt"
	"log"

	"go.temporal.io/sdk/activity"
)

type UsageActivities struct {
	db *sql.DB
}

func NewUsageActivities(db *sql.DB) *UsageActivities {
	return &UsageActivities{db: db}
}

type RecordUsageInput struct {
	TaskID                string
	WorkflowID            string
	RunID                 string
	Provider              string
	Model                 string
	EstimatedPromptTokens int
	MaxCompletionTokens   int
	PromptTokens          int
	CompletionTokens      int
	TotalTokens           int
	LatencyMS             int64
	FinishReason          string
	AgentRole             string // "critic" | "synthesizer" | "planner" | "researcher" (for multi-agent) or empty for simple
}

func (a *UsageActivities) RecordUsage(ctx context.Context, input RecordUsageInput) error {
	logger := activity.GetLogger(ctx)
	logger.Info("RecordUsageActivity started", "task_id", input.TaskID, "total_tokens", input.TotalTokens, "agent_role", input.AgentRole)

	// call_id is task_id + ":llm" for idempotency
	// For multi-agent, we need unique call_id per agent role
	callID := input.TaskID + ":llm"
	if input.AgentRole != "" {
		callID = input.TaskID + ":llm:" + input.AgentRole
	}

	query := `
		INSERT INTO llm_calls (
			id, call_id, task_id, workflow_id, run_id, provider, model,
			estimated_prompt_tokens, max_completion_tokens,
			prompt_tokens, completion_tokens, total_tokens,
			latency_ms, finish_reason, agent_role, created_at
		) VALUES (
			gen_random_uuid(), $1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10, $11, $12, $13, $14, NOW()
		)
		ON CONFLICT (call_id) DO UPDATE SET
			prompt_tokens = EXCLUDED.prompt_tokens,
			completion_tokens = EXCLUDED.completion_tokens,
			total_tokens = EXCLUDED.total_tokens,
			latency_ms = EXCLUDED.latency_ms,
			finish_reason = EXCLUDED.finish_reason
		RETURNING id`

	var id string
	err := a.db.QueryRowContext(ctx, query,
		callID,
		input.TaskID,
		input.WorkflowID,
		input.RunID,
		input.Provider,
		input.Model,
		input.EstimatedPromptTokens,
		input.MaxCompletionTokens,
		input.PromptTokens,
		input.CompletionTokens,
		input.TotalTokens,
		input.LatencyMS,
		input.FinishReason,
		input.AgentRole,
	).Scan(&id)

	if err != nil {
		logger.Error("RecordUsageActivity failed", "error", err)
		return fmt.Errorf("insert llm_calls: %w", err)
	}

	log.Printf("[INFO] RecordUsageActivity: task=%s id=%s tokens=%d", input.TaskID, id, input.TotalTokens)
	logger.Info("RecordUsageActivity completed", "task_id", input.TaskID)
	return nil
}