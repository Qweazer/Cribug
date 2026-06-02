-- Migration 012: approval_audit_logs table for Phase 7B HITL / Approval
-- feedback_summary <= 500 chars; feedback_ref points to Workspace.
-- Long feedback text MUST NOT be stored in this table.

CREATE TABLE IF NOT EXISTS approval_audit_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    approval_id VARCHAR(100) NOT NULL,
    workflow_id VARCHAR(100) NOT NULL,
    query TEXT NOT NULL DEFAULT '',
    risk_level VARCHAR(50) NOT NULL DEFAULT '',
    approved BOOLEAN,
    feedback_summary VARCHAR(500) NOT NULL DEFAULT '',
    feedback_ref VARCHAR(200) NOT NULL DEFAULT '',
    approved_by VARCHAR(100) NOT NULL DEFAULT '',
    duration_ms BIGINT NOT NULL DEFAULT 0,
    status VARCHAR(50) NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_approval_audit_workflow ON approval_audit_logs(workflow_id);
CREATE INDEX IF NOT EXISTS idx_approval_audit_approval ON approval_audit_logs(approval_id);
