-- Migration: Create skill_audit_logs table
-- Description: Audit table for skill execution tracking

CREATE TABLE IF NOT EXISTS skill_audit_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    skill_name VARCHAR(100) NOT NULL,
    agent_id VARCHAR(100),
    workflow_id VARCHAR(100) NOT NULL,
    request_id VARCHAR(100) NOT NULL,
    success BOOLEAN NOT NULL DEFAULT false,
    duration_ms INTEGER,
    error TEXT,
    error_type VARCHAR(50),
    overflow BOOLEAN DEFAULT false,
    provider VARCHAR(50),
    model VARCHAR(100),
    token_usage JSONB,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_skill_audit_logs_skill_name ON skill_audit_logs(skill_name);
CREATE INDEX IF NOT EXISTS idx_skill_audit_logs_agent_id ON skill_audit_logs(agent_id);
CREATE INDEX IF NOT EXISTS idx_skill_audit_logs_workflow_id ON skill_audit_logs(workflow_id);
CREATE INDEX IF NOT EXISTS idx_skill_audit_logs_request_id ON skill_audit_logs(request_id);
CREATE INDEX IF NOT EXISTS idx_skill_audit_logs_created_at ON skill_audit_logs(created_at DESC);
