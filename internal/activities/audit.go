package activities

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// AuditActivities holds the DB handle for audit persistence.
type AuditActivities struct{ db *sql.DB }

// NewAuditActivities creates a new AuditActivities instance.
func NewAuditActivities(d *sql.DB) *AuditActivities { return &AuditActivities{db: d} }

// ─── WriteAuditEventActivity (Phase 7H) ────────────────────────────────

type WriteAuditEventInput struct {
	TaskID          string  `json:"task_id,omitempty"`
	SessionID       string  `json:"session_id,omitempty"`
	WorkflowID      string  `json:"workflow_id"`
	RunID           string  `json:"run_id,omitempty"`
	Mode            string  `json:"mode,omitempty"`
	EventType       string  `json:"event_type"`
	Actor           string  `json:"actor,omitempty"`
	Provider        string  `json:"provider,omitempty"`
	ModelUsed       string  `json:"model_used,omitempty"`
	Mock            bool    `json:"mock"`
	LLMCalls        int     `json:"llm_calls"`
	PromptTokens    int     `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
	TotalTokens     int     `json:"total_tokens"`
	CostUSD         float64 `json:"cost_usd"`
	Status          string  `json:"status,omitempty"`
	RiskLevel       string  `json:"risk_level,omitempty"`
	ApprovalID      string  `json:"approval_id,omitempty"`
	WorkspaceRefs   []string `json:"workspace_refs,omitempty"`
	Metadata        map[string]interface{} `json:"metadata,omitempty"`
	ErrorMessage    string  `json:"error_message,omitempty"`
}

type WriteAuditEventResult struct {
	AuditID string `json:"audit_id"`
}

func (aa *AuditActivities) WriteAuditEvent(ctx context.Context, input WriteAuditEventInput) (*WriteAuditEventResult, error) {
	if aa.db == nil {
		return nil, fmt.Errorf("audit activities: db is nil")
	}
	wsJSON, _ := json.Marshal(input.WorkspaceRefs)
	mdJSON, _ := json.Marshal(input.Metadata)

	var id string
	err := aa.db.QueryRowContext(ctx, `
		INSERT INTO audit_events (task_id, session_id, workflow_id, run_id, mode,
			event_type, actor, provider, model_used, mock, llm_calls,
			prompt_tokens, completion_tokens, total_tokens, cost_usd,
			status, risk_level, approval_id, workspace_refs, metadata,
			error_message, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19::jsonb,$20::jsonb,$21,NOW())
		RETURNING id
	`, input.TaskID, input.SessionID, input.WorkflowID, input.RunID, input.Mode,
		input.EventType, input.Actor, input.Provider, input.ModelUsed, input.Mock,
		input.LLMCalls, nilIfZero(input.PromptTokens), nilIfZero(input.CompletionTokens),
		nilIfZero(input.TotalTokens), nilIfZeroF(input.CostUSD),
		input.Status, input.RiskLevel, input.ApprovalID,
		string(wsJSON), string(mdJSON), nilIfEmpty(input.ErrorMessage),
	).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("write audit event: %w", err)
	}
	return &WriteAuditEventResult{AuditID: id}, nil
}

func nilIfZero(v int) interface{} { if v == 0 { return nil }; return v }
func nilIfZeroF(v float64) interface{} { if v == 0 { return nil }; return v }
func nilIfEmpty(s string) interface{} { if s == "" { return nil }; return s }

// QueryAuditEventsInput is the query filter.
type QueryAuditEventsInput struct {
	WorkflowID string `json:"workflow_id,omitempty"`
	TaskID     string `json:"task_id,omitempty"`
	Limit      int    `json:"limit"`
}

// QueryAuditEventsResult is a list of events.
type QueryAuditEventsResult struct {
	Events []map[string]interface{} `json:"events"`
	Count  int                     `json:"count"`
}

// QueryAuditEvents returns audit events for a workflow or task.
func (aa *AuditActivities) QueryAuditEvents(ctx context.Context, input QueryAuditEventsInput) (*QueryAuditEventsResult, error) {
	if aa.db == nil {
		return nil, fmt.Errorf("audit activities: db is nil")
	}
	limit := input.Limit
	if limit <= 0 {
		limit = 50
	}
	var rows *sql.Rows
	var err error
	if input.WorkflowID != "" {
		rows, err = aa.db.QueryContext(ctx, `
			SELECT id, task_id, workflow_id, run_id, mode, event_type, status,
			       COALESCE(total_tokens,0), COALESCE(cost_usd,0), created_at
			FROM audit_events WHERE workflow_id = $1 ORDER BY created_at LIMIT $2`,
			input.WorkflowID, limit)
	} else if input.TaskID != "" {
		rows, err = aa.db.QueryContext(ctx, `
			SELECT id, task_id, workflow_id, run_id, mode, event_type, status,
			       COALESCE(total_tokens,0), COALESCE(cost_usd,0), created_at
			FROM audit_events WHERE task_id = $1 ORDER BY created_at LIMIT $2`,
			input.TaskID, limit)
	} else {
		return &QueryAuditEventsResult{Events: []map[string]interface{}{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query audit events: %w", err)
	}
	defer rows.Close()
	var events []map[string]interface{}
	for rows.Next() {
		var id, taskID, wfID, runID, mode, eventType, status string
		var tokens int
		var costUSD float64
		var createdAt interface{}
		rows.Scan(&id, &taskID, &wfID, &runID, &mode, &eventType, &status, &tokens, &costUSD, &createdAt)
		events = append(events, map[string]interface{}{
			"id": id, "task_id": taskID, "workflow_id": wfID, "run_id": runID,
			"mode": mode, "event_type": eventType, "status": status,
			"total_tokens": tokens, "cost_usd": costUSD, "created_at": createdAt,
		})
	}
	return &QueryAuditEventsResult{Events: events, Count: len(events)}, nil
}

// EnsureAuditTable creates the audit_events table. The DDL is in
// migrations/015_audit_events.sql; this function is a no-op helper
// for callers that want a programmatic table-ensure pattern.
func EnsureAuditTable(d *sql.DB) error { return nil }
