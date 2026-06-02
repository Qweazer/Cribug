package activities

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"cribug/internal/types"

	"github.com/google/uuid"
)

// ApprovalActivities holds dependencies for HITL/Approval Activities.
type ApprovalActivities struct {
	db *sql.DB
}

// NewApprovalActivities creates a new ApprovalActivities instance.
func NewApprovalActivities(db *sql.DB) *ApprovalActivities {
	return &ApprovalActivities{db: db}
}

// ─── RequestApprovalActivity ───────────────────────────────────────────

type RequestApprovalInput struct {
	SessionID      string                 `json:"session_id"`
	WorkflowID     string                 `json:"workflow_id"`
	RunID          string                 `json:"run_id"`
	Query          string                 `json:"query"`
	Context        map[string]interface{} `json:"context"`
	ProposedAction map[string]interface{} `json:"proposed_action"`
	Reason         string                 `json:"reason"`
	RiskLevel      string                 `json:"risk_level"`
	Mode           string                 `json:"mode"`
	TimeoutSeconds int                    `json:"timeout_seconds"`
	RequestedAt    time.Time              `json:"requested_at"` // Workflow generated, passed in
	ExpiresAt      time.Time              `json:"expires_at"`   // Workflow generated, passed in
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
}

type RequestApprovalResult struct {
	ApprovalID string `json:"approval_id"`
}

func (aa *ApprovalActivities) RequestApproval(ctx context.Context, input RequestApprovalInput) (*RequestApprovalResult, error) {
	approvalID := "approval-" + uuid.New().String()

	if aa.db != nil {
		_, err := aa.db.ExecContext(ctx, `
			INSERT INTO approvals
				(approval_id, workflow_id, run_id, session_id, query, proposed_action,
				 reason, risk_level, mode, status, requested_at, expires_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
			approvalID, input.WorkflowID, input.RunID, input.SessionID,
			input.Query, fmt.Sprintf("%v", input.ProposedAction),
			input.Reason, input.RiskLevel, input.Mode,
			types.ApprovalStatusPending, input.RequestedAt, input.ExpiresAt,
		)
		if err != nil {
			return nil, fmt.Errorf("request approval: %w", err)
		}
	}

	return &RequestApprovalResult{ApprovalID: approvalID}, nil
}

// ─── RecordApprovalResponseActivity ─────────────────────────────────────

type RecordApprovalResponseInput struct {
	ApprovalID     string                 `json:"approval_id"`
	WorkflowID     string                 `json:"workflow_id"`
	Approved       bool                   `json:"approved"`
	Feedback       string                 `json:"feedback_summary,omitempty"`
	FeedbackRef    string                 `json:"feedback_ref,omitempty"`
	ModifiedAction map[string]interface{} `json:"modified_action,omitempty"`
	ApprovedBy     string                 `json:"approved_by,omitempty"`
	RespondedAt    time.Time              `json:"responded_at"`
	RequestedAt    time.Time              `json:"requested_at"` // for duration calculation
}

type RecordApprovalResponseResult struct {
	AuditID string `json:"audit_id"`
}

func (aa *ApprovalActivities) RecordApprovalResponse(ctx context.Context, input RecordApprovalResponseInput) (*RecordApprovalResponseResult, error) {
	status := types.ApprovalStatusApproved
	if !input.Approved {
		if input.ModifiedAction != nil {
			status = types.ApprovalStatusModified
		} else {
			status = types.ApprovalStatusRejected
		}
	}

	durationMs := input.RespondedAt.Sub(input.RequestedAt).Milliseconds()
	auditID := "audit-" + uuid.New().String()

	if aa.db != nil {
		// Update approval status
		_, err := aa.db.ExecContext(ctx, `
			UPDATE approvals SET status=$1, responded_at=$2 WHERE approval_id=$3`,
			status, input.RespondedAt, input.ApprovalID,
		)
		if err != nil {
			return nil, fmt.Errorf("update approval status: %w", err)
		}

		// Write audit log
		var approvedPtr *bool
		if status != types.ApprovalStatusTimeout {
			approvedPtr = &input.Approved
		}
		_, err = aa.db.ExecContext(ctx, `
			INSERT INTO approval_audit_logs
				(approval_id, workflow_id, query, risk_level, approved,
				 feedback_summary, feedback_ref, approved_by, duration_ms, status)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			input.ApprovalID, input.WorkflowID, "", "",
			approvedPtr, truncate(input.Feedback, 500), input.FeedbackRef, input.ApprovedBy,
			durationMs, status,
		)
		if err != nil {
			return nil, fmt.Errorf("write approval audit: %w", err)
		}
	}

	return &RecordApprovalResponseResult{AuditID: auditID}, nil
}

// ─── MarkApprovalTimeoutActivity ────────────────────────────────────────

type MarkApprovalTimeoutInput struct {
	ApprovalID  string    `json:"approval_id"`
	WorkflowID  string    `json:"workflow_id"`
	RequestedAt time.Time `json:"requested_at"`
}

type MarkApprovalTimeoutResult struct {
	AuditID string `json:"audit_id"`
}

func (aa *ApprovalActivities) MarkApprovalTimeout(ctx context.Context, input MarkApprovalTimeoutInput) (*MarkApprovalTimeoutResult, error) {
	auditID := "audit-" + uuid.New().String()

	if aa.db != nil {
		_, err := aa.db.ExecContext(ctx, `
			UPDATE approvals SET status=$1 WHERE approval_id=$2`,
			types.ApprovalStatusTimeout, input.ApprovalID,
		)
		if err != nil {
			return nil, fmt.Errorf("mark approval timeout: %w", err)
		}

		_, err = aa.db.ExecContext(ctx, `
			INSERT INTO approval_audit_logs
				(approval_id, workflow_id, query, risk_level, approved,
				 feedback_summary, feedback_ref, approved_by, duration_ms, status)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			input.ApprovalID, input.WorkflowID, "", "",
			nil, "", "", "", 0, types.ApprovalStatusTimeout,
		)
		if err != nil {
			return nil, fmt.Errorf("write timeout audit: %w", err)
		}
	}

	return &MarkApprovalTimeoutResult{AuditID: auditID}, nil
}

// ─── EmitApprovalEventActivity ──────────────────────────────────────────

type EmitApprovalEventInput struct {
	WorkflowID string `json:"workflow_id"`
	ApprovalID string `json:"approval_id"`
	EventType  string `json:"event_type"` // "APPROVAL_REQUESTED" | "APPROVAL_APPROVED" | "APPROVAL_REJECTED" | "APPROVAL_TIMEOUT"
	Status     string `json:"status"`
	ApprovedBy string `json:"approved_by,omitempty"`
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

func (aa *ApprovalActivities) EmitApprovalEvent(ctx context.Context, input EmitApprovalEventInput) error {
	// Slice 24 MVP: no-op. Will be wired to Redis SSE when event infrastructure matures.
	return nil
}

// ─── EnsureApprovalTables (DEV/TEST ONLY) ──────────────────────────────
// DEV/TEST ONLY: auto-creates tables on worker start.
// Production MUST use migrations/011 and 012.

func EnsureApprovalTables(db *sql.DB) error {
	if db == nil {
		return nil
	}
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS approvals (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			approval_id VARCHAR(100) NOT NULL UNIQUE,
			workflow_id VARCHAR(100) NOT NULL,
			run_id VARCHAR(100),
			session_id VARCHAR(100),
			query TEXT NOT NULL DEFAULT '',
			proposed_action TEXT NOT NULL DEFAULT '',
			reason TEXT NOT NULL DEFAULT '',
			risk_level VARCHAR(50) NOT NULL DEFAULT 'medium',
			mode VARCHAR(50) NOT NULL DEFAULT '',
			status VARCHAR(50) NOT NULL DEFAULT 'pending',
			feedback_summary VARCHAR(500) NOT NULL DEFAULT '',
			feedback_ref VARCHAR(200) NOT NULL DEFAULT '',
			modified_action JSONB,
			approved_by VARCHAR(100) NOT NULL DEFAULT '',
			requested_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			expires_at TIMESTAMP WITH TIME ZONE,
			responded_at TIMESTAMP WITH TIME ZONE,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		);

		CREATE INDEX IF NOT EXISTS idx_approvals_workflow ON approvals(workflow_id);
		CREATE INDEX IF NOT EXISTS idx_approvals_status ON approvals(status);
		CREATE INDEX IF NOT EXISTS idx_approvals_approval_id ON approvals(approval_id);

		CREATE TABLE IF NOT EXISTS approval_audit_logs (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			approval_id VARCHAR(100) NOT NULL,
			workflow_id VARCHAR(100) NOT NULL,
			query TEXT NOT NULL DEFAULT '',
			risk_level VARCHAR(50) NOT NULL DEFAULT '',
			approved BOOLEAN,
			feedback TEXT NOT NULL DEFAULT '',
			approved_by VARCHAR(100) NOT NULL DEFAULT '',
			duration_ms BIGINT NOT NULL DEFAULT 0,
			status VARCHAR(50) NOT NULL DEFAULT '',
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		);

		CREATE INDEX IF NOT EXISTS idx_approval_audit_workflow ON approval_audit_logs(workflow_id);
		CREATE INDEX IF NOT EXISTS idx_approval_audit_approval ON approval_audit_logs(approval_id);
	`)
	return err
}
