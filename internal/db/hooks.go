package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	hookspkg "cribug/internal/hooks"

	"github.com/google/uuid"
)

// ── Hook Registration CRUD ─────────────────────────────────────────

func (p *Postgres) CreateHook(ctx context.Context, reg hookspkg.HookRegistration) (*hookspkg.HookRegistration, error) {
	if reg.ID == "" {
		reg.ID = uuid.New().String()
	}
	if reg.TenantID == "" {
		reg.TenantID = "00000000-0000-0000-0000-000000000000"
	}
	now := time.Now().UTC()
	reg.CreatedAt = now
	reg.UpdatedAt = now

	filterJSON := "{}"
	if reg.Filter != nil && len(reg.Filter) > 0 {
		b, err := json.Marshal(reg.Filter)
		if err != nil {
			return nil, fmt.Errorf("marshal filter: %w", err)
		}
		filterJSON = string(b)
	}

	query := `INSERT INTO hooks
		(id, tenant_id, name, hook_point, handler_url, blocking, filter, enabled, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`
	_, err := p.db.ExecContext(ctx, query,
		reg.ID, reg.TenantID, reg.Name, string(reg.HookPoint), reg.HandlerURL,
		reg.Blocking, filterJSON, reg.Enabled, reg.CreatedAt, reg.UpdatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "unique") {
			return nil, fmt.Errorf("hook '%s' already exists for tenant", reg.Name)
		}
		return nil, fmt.Errorf("insert hook: %w", err)
	}

	return &reg, nil
}

func (p *Postgres) GetHook(ctx context.Context, id string) (*hookspkg.HookRegistration, error) {
	query := `SELECT id, tenant_id, name, hook_point, handler_url, blocking, filter, enabled, created_at, updated_at
		FROM hooks WHERE id = $1`
	var reg hookspkg.HookRegistration
	var filterRaw string
	err := p.db.QueryRowContext(ctx, query, id).Scan(
		&reg.ID, &reg.TenantID, &reg.Name, &reg.HookPoint, &reg.HandlerURL,
		&reg.Blocking, &filterRaw, &reg.Enabled, &reg.CreatedAt, &reg.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get hook: %w", err)
	}
	if filterRaw != "" && filterRaw != "{}" {
		json.Unmarshal([]byte(filterRaw), &reg.Filter)
	}
	reg.HookPoint = hookspkg.HookPoint(reg.HookPoint) // it's stored as string, cast back
	return &reg, nil
}

func (p *Postgres) ListHooks(ctx context.Context, tenantID string, hookPoint string) ([]hookspkg.HookRegistration, error) {
	var args []interface{}
	var conditions []string
	argIdx := 1

	if tenantID != "" {
		conditions = append(conditions, fmt.Sprintf("tenant_id = $%d", argIdx))
		args = append(args, tenantID)
		argIdx++
	}
	if hookPoint != "" {
		conditions = append(conditions, fmt.Sprintf("hook_point = $%d", argIdx))
		args = append(args, hookPoint)
		argIdx++
	}

	query := `SELECT id, tenant_id, name, hook_point, handler_url, blocking, filter, enabled, created_at, updated_at
		FROM hooks`
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY created_at DESC"

	rows, err := p.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list hooks: %w", err)
	}
	defer rows.Close()

	var hooks []hookspkg.HookRegistration
	for rows.Next() {
		var reg hookspkg.HookRegistration
		var filterRaw string
		if err := rows.Scan(
			&reg.ID, &reg.TenantID, &reg.Name, &reg.HookPoint, &reg.HandlerURL,
			&reg.Blocking, &filterRaw, &reg.Enabled, &reg.CreatedAt, &reg.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan hook: %w", err)
		}
		if filterRaw != "" && filterRaw != "{}" {
			json.Unmarshal([]byte(filterRaw), &reg.Filter)
		}
		reg.HookPoint = hookspkg.HookPoint(reg.HookPoint)
		hooks = append(hooks, reg)
	}
	if hooks == nil {
		hooks = []hookspkg.HookRegistration{}
	}
	return hooks, rows.Err()
}

func (p *Postgres) DeleteHook(ctx context.Context, id string) error {
	query := `DELETE FROM hooks WHERE id = $1`
	result, err := p.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("delete hook: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("hook not found: %s", id)
	}
	return nil
}

func (p *Postgres) UpdateHookEnabled(ctx context.Context, id string, enabled bool) error {
	query := `UPDATE hooks SET enabled = $2, updated_at = NOW() WHERE id = $1`
	result, err := p.db.ExecContext(ctx, query, id, enabled)
	if err != nil {
		return fmt.Errorf("update hook enabled: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("hook not found: %s", id)
	}
	return nil
}

// ListEnabledHooksByPoint returns all enabled hooks for a given hook point and tenant.
func (p *Postgres) ListEnabledHooksByPoint(ctx context.Context, hookPoint hookspkg.HookPoint, tenantID string) ([]hookspkg.HookRegistration, error) {
	query := `SELECT id, tenant_id, name, hook_point, handler_url, blocking, filter, enabled, created_at, updated_at
		FROM hooks WHERE hook_point = $1 AND tenant_id = $2 AND enabled = TRUE
		ORDER BY created_at ASC`
	rows, err := p.db.QueryContext(ctx, query, string(hookPoint), tenantID)
	if err != nil {
		return nil, fmt.Errorf("list enabled hooks by point: %w", err)
	}
	defer rows.Close()

	var hooks []hookspkg.HookRegistration
	for rows.Next() {
		var reg hookspkg.HookRegistration
		var filterRaw string
		if err := rows.Scan(
			&reg.ID, &reg.TenantID, &reg.Name, &reg.HookPoint, &reg.HandlerURL,
			&reg.Blocking, &filterRaw, &reg.Enabled, &reg.CreatedAt, &reg.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan hook: %w", err)
		}
		if filterRaw != "" && filterRaw != "{}" {
			json.Unmarshal([]byte(filterRaw), &reg.Filter)
		}
		reg.HookPoint = hookspkg.HookPoint(reg.HookPoint)
		hooks = append(hooks, reg)
	}
	if hooks == nil {
		hooks = []hookspkg.HookRegistration{}
	}
	return hooks, rows.Err()
}

// ── Hook Audit Log Operations ──────────────────────────────────────

func (p *Postgres) InsertHookAuditLog(ctx context.Context, log hookspkg.HookAuditLog) error {
	if log.ID == "" {
		log.ID = uuid.New().String()
	}
	if log.CreatedAt.IsZero() {
		log.CreatedAt = time.Now().UTC()
	}

	query := `INSERT INTO hook_audit_logs
		(id, event_id, hook_point, handler_name, tenant_id, workflow_id, agent_id,
		 correlation_id, handler_type, blocking_configured, blocking_effective,
		 decision_enforced, suppressed_reason, continue_decision, reject_code,
		 reject_reason, payload_hash, success, duration_ms, error, warning, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22)`
	_, err := p.db.ExecContext(ctx, query,
		log.ID, log.EventID, string(log.HookPoint), log.HandlerName, log.TenantID,
		log.WorkflowID, log.AgentID, log.CorrelationID, log.HandlerType,
		log.BlockingConfigured, log.BlockingEffective, log.DecisionEnforced,
		log.SuppressedReason, log.ContinueDecision, log.RejectCode,
		log.RejectReason, log.PayloadHash, log.Success,
		log.DurationMs, log.Error, log.Warning, log.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert hook_audit_log: %w", err)
	}
	return nil
}

func (p *Postgres) InsertHookAuditLogs(ctx context.Context, logs []hookspkg.HookAuditLog) error {
	for i := range logs {
		if err := p.InsertHookAuditLog(ctx, logs[i]); err != nil {
			return fmt.Errorf("insert hook_audit_log %d: %w", i, err)
		}
	}
	return nil
}

func (p *Postgres) GetHookAuditLogsByWorkflowID(ctx context.Context, workflowID string) ([]hookspkg.HookAuditLog, error) {
	query := `SELECT id, event_id, hook_point, handler_name, tenant_id, workflow_id,
		agent_id, correlation_id, handler_type, blocking_configured, blocking_effective,
		decision_enforced, suppressed_reason, continue_decision, reject_code,
		reject_reason, payload_hash, success, duration_ms, error, warning, created_at
		FROM hook_audit_logs WHERE workflow_id = $1 ORDER BY created_at DESC`

	rows, err := p.db.QueryContext(ctx, query, workflowID)
	if err != nil {
		return nil, fmt.Errorf("list hook_audit_logs: %w", err)
	}
	defer rows.Close()

	var logs []hookspkg.HookAuditLog
	for rows.Next() {
		var l hookspkg.HookAuditLog
		if err := rows.Scan(
			&l.ID, &l.EventID, &l.HookPoint, &l.HandlerName, &l.TenantID,
			&l.WorkflowID, &l.AgentID, &l.CorrelationID, &l.HandlerType,
			&l.BlockingConfigured, &l.BlockingEffective, &l.DecisionEnforced,
			&l.SuppressedReason, &l.ContinueDecision, &l.RejectCode,
			&l.RejectReason, &l.PayloadHash, &l.Success,
			&l.DurationMs, &l.Error, &l.Warning, &l.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan hook_audit_log: %w", err)
		}
		logs = append(logs, l)
	}
	if logs == nil {
		logs = []hookspkg.HookAuditLog{}
	}
	return logs, rows.Err()
}
