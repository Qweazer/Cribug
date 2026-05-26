-- Sandbox Tables (Phase 6B Slice 18)

CREATE TABLE IF NOT EXISTS sandbox_audit_logs (
    id UUID PRIMARY KEY,
    request_id VARCHAR(255) NOT NULL,
    agent_id VARCHAR(255) NOT NULL DEFAULT '',
    workflow_id VARCHAR(255) NOT NULL DEFAULT '',
    language VARCHAR(50) NOT NULL DEFAULT '',
    code_len INT DEFAULT 0,
    success BOOLEAN NOT NULL DEFAULT false,
    exit_code INT DEFAULT 0,
    error TEXT DEFAULT '',
    error_type VARCHAR(100) DEFAULT '',
    duration_ms BIGINT DEFAULT 0,
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_sandbox_audit_request ON sandbox_audit_logs(request_id);
CREATE INDEX IF NOT EXISTS idx_sandbox_audit_workflow ON sandbox_audit_logs(workflow_id);
