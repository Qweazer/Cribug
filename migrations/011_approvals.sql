-- Migration 011: approvals table for Phase 7B HITL / Approval

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
