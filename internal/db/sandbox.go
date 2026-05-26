package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"cribug/internal/types"

	"github.com/google/uuid"
)

func (p *Postgres) CreateSandboxAuditLog(ctx context.Context, audit types.SandboxAuditLog) error {
	if audit.ID == "" {
		audit.ID = uuid.New().String()
	}
	if audit.CreatedAt.IsZero() {
		audit.CreatedAt = time.Now().UTC()
	}

	query := `INSERT INTO sandbox_audit_logs
		(id, request_id, agent_id, workflow_id, language, code_len,
		 success, exit_code, error, error_type, duration_ms, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`
	_, err := p.db.ExecContext(ctx, query,
		audit.ID, audit.RequestID, audit.AgentID, audit.WorkflowID,
		audit.Language, audit.CodeLen, audit.Success, audit.ExitCode,
		audit.Error, audit.ErrorType, audit.DurationMs, audit.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert sandbox_audit_log: %w", err)
	}
	return nil
}

func (p *Postgres) GetSandboxAuditLogsByWorkflowID(ctx context.Context, workflowID string) ([]types.SandboxAuditLog, error) {
	query := `SELECT id, request_id, agent_id, workflow_id, language, code_len,
		success, exit_code, error, error_type, duration_ms, created_at
		FROM sandbox_audit_logs WHERE workflow_id = $1 ORDER BY created_at DESC`
	rows, err := p.db.QueryContext(ctx, query, workflowID)
	if err != nil {
		return nil, fmt.Errorf("list sandbox_audit_logs: %w", err)
	}
	defer rows.Close()

	var logs []types.SandboxAuditLog
	for rows.Next() {
		var l types.SandboxAuditLog
		if err := rows.Scan(&l.ID, &l.RequestID, &l.AgentID, &l.WorkflowID,
			&l.Language, &l.CodeLen, &l.Success, &l.ExitCode, &l.Error,
			&l.ErrorType, &l.DurationMs, &l.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan sandbox_audit_log: %w", err)
		}
		logs = append(logs, l)
	}
	return logs, rows.Err()
}

func (p *Postgres) scanSandboxAuditLog(row *sql.Row) (*types.SandboxAuditLog, error) {
	var l types.SandboxAuditLog
	err := row.Scan(&l.ID, &l.RequestID, &l.AgentID, &l.WorkflowID,
		&l.Language, &l.CodeLen, &l.Success, &l.ExitCode, &l.Error,
		&l.ErrorType, &l.DurationMs, &l.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &l, nil
}
