package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type SkillAuditLog struct {
	ID         string
	SkillName  string
	AgentID    string
	WorkflowID string
	RequestID  string
	Success    bool
	DurationMs int
	Error      string
	ErrorType  string
	Overflow   bool
	Provider   string
	Model      string
	TokenUsage map[string]int
	CreatedAt  time.Time
}

func (p *Postgres) CreateSkillAuditLog(ctx context.Context, log SkillAuditLog) (string, error) {
	if log.ID == "" {
		log.ID = uuid.New().String()
	}
	if log.CreatedAt.IsZero() {
		log.CreatedAt = time.Now().UTC()
	}

	tokenUsageJSON := "{}"
	if len(log.TokenUsage) > 0 {
		b, _ := json.Marshal(log.TokenUsage)
		tokenUsageJSON = string(b)
	}

	query := `INSERT INTO skill_audit_logs
		(id, skill_name, agent_id, workflow_id, request_id, success, duration_ms, error, error_type, overflow, provider, model, token_usage, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING id`

	var auditID string
	err := p.db.QueryRowContext(ctx, query,
		log.ID, log.SkillName, log.AgentID, log.WorkflowID, log.RequestID,
		log.Success, log.DurationMs, log.Error, log.ErrorType,
		log.Overflow, log.Provider, log.Model, tokenUsageJSON, log.CreatedAt,
	).Scan(&auditID)

	if err != nil {
		return "", fmt.Errorf("insert skill_audit_log: %w", err)
	}
	return auditID, nil
}

func (p *Postgres) GetSkillAuditLogsByWorkflowID(ctx context.Context, workflowID string) ([]SkillAuditLog, error) {
	query := `SELECT id, skill_name, agent_id, workflow_id, request_id,
		success, duration_ms, error, error_type, overflow, provider, model, token_usage, created_at
		FROM skill_audit_logs WHERE workflow_id = $1 ORDER BY created_at DESC`
	rows, err := p.db.QueryContext(ctx, query, workflowID)
	if err != nil {
		return nil, fmt.Errorf("list skill_audit_logs: %w", err)
	}
	defer rows.Close()

	var logs []SkillAuditLog
	for rows.Next() {
		var l SkillAuditLog
		var tokenUsageJSON string
		if err := rows.Scan(
			&l.ID, &l.SkillName, &l.AgentID, &l.WorkflowID, &l.RequestID,
			&l.Success, &l.DurationMs, &l.Error, &l.ErrorType,
			&l.Overflow, &l.Provider, &l.Model, &tokenUsageJSON, &l.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan skill_audit_log: %w", err)
		}
		if tokenUsageJSON != "" && tokenUsageJSON != "{}" {
			json.Unmarshal([]byte(tokenUsageJSON), &l.TokenUsage)
		}
		logs = append(logs, l)
	}
	return logs, rows.Err()
}

func (p *Postgres) SkillAuditLogExists(ctx context.Context, requestID string) (bool, error) {
	query := `SELECT EXISTS(SELECT 1 FROM skill_audit_logs WHERE request_id = $1)`
	var exists bool
	err := p.db.QueryRowContext(ctx, query, requestID).Scan(&exists)
	if err != nil && err != sql.ErrNoRows {
		return false, fmt.Errorf("check skill_audit_log: %w", err)
	}
	return exists, nil
}
