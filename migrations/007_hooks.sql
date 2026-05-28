-- Hooks Event System (Phase 6D Slice 20)
-- Run: psql -U admin -d orchestrator -f migrations/007_hooks.sql

CREATE TABLE IF NOT EXISTS hooks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    name VARCHAR(100) NOT NULL,
    hook_point VARCHAR(50) NOT NULL,
    handler_url VARCHAR(500) NOT NULL,
    blocking BOOLEAN DEFAULT FALSE,
    filter JSONB,
    enabled BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    CONSTRAINT hooks_tenant_name_unique UNIQUE (tenant_id, name)
);

CREATE TABLE IF NOT EXISTS hook_audit_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id VARCHAR(100) NOT NULL,
    hook_point VARCHAR(50) NOT NULL,
    handler_name VARCHAR(100) NOT NULL,
    tenant_id UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000000',
    workflow_id VARCHAR(100) NOT NULL DEFAULT '',
    agent_id VARCHAR(100) NOT NULL DEFAULT '',
    correlation_id VARCHAR(100) NOT NULL DEFAULT '',
    handler_type VARCHAR(20) NOT NULL DEFAULT 'internal',
    blocking_configured BOOLEAN NOT NULL DEFAULT FALSE,
    blocking_effective BOOLEAN NOT NULL DEFAULT FALSE,
    decision_enforced BOOLEAN NOT NULL DEFAULT FALSE,
    suppressed_reason VARCHAR(200) NOT NULL DEFAULT '',
    continue_decision BOOLEAN NOT NULL DEFAULT TRUE,
    reject_code VARCHAR(50) NOT NULL DEFAULT '',
    reject_reason TEXT NOT NULL DEFAULT '',
    payload_hash VARCHAR(64) NOT NULL DEFAULT '',
    success BOOLEAN NOT NULL DEFAULT FALSE,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT '',
    warning TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_hooks_tenant_id ON hooks(tenant_id);
CREATE INDEX IF NOT EXISTS idx_hooks_hook_point ON hooks(hook_point);
CREATE INDEX IF NOT EXISTS idx_hooks_enabled_point ON hooks(tenant_id, hook_point, enabled);
CREATE INDEX IF NOT EXISTS idx_hooks_tenant_name ON hooks(tenant_id, name);

CREATE INDEX IF NOT EXISTS idx_hook_audit_event_id ON hook_audit_logs(event_id);
CREATE INDEX IF NOT EXISTS idx_hook_audit_workflow_id ON hook_audit_logs(workflow_id);
CREATE INDEX IF NOT EXISTS idx_hook_audit_tenant_id ON hook_audit_logs(tenant_id);
CREATE INDEX IF NOT EXISTS idx_hook_audit_created_at ON hook_audit_logs(created_at);
