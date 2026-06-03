-- Phase 7H Audit DB Persistence
-- Stores routed execution audit events for compliance / debugging.
-- Each event has a type (task_started, routed, approval_requested,
-- approval_approved/rejected, workflow_completed/failed, llm_usage,
-- workspace_created). Only refs and summaries are stored; full
-- transcripts / reports / evidence always live in workspace_entries.

CREATE TABLE IF NOT EXISTS audit_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id VARCHAR(255),
    session_id VARCHAR(255),
    workflow_id VARCHAR(255) NOT NULL,
    run_id VARCHAR(255),
    mode VARCHAR(50),
    event_type VARCHAR(100) NOT NULL,
    actor VARCHAR(100) DEFAULT 'system',
    provider VARCHAR(100),
    model_used VARCHAR(100),
    mock BOOLEAN DEFAULT false,
    llm_calls INTEGER DEFAULT 0,
    prompt_tokens INTEGER,
    completion_tokens INTEGER,
    total_tokens INTEGER,
    cost_usd DOUBLE PRECISION,
    status VARCHAR(50),
    risk_level VARCHAR(50),
    approval_id VARCHAR(255),
    workspace_refs JSONB,
    metadata JSONB,
    error_message TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS audit_events_wf_idx ON audit_events (workflow_id);
CREATE INDEX IF NOT EXISTS audit_events_type_idx ON audit_events (event_type);
CREATE INDEX IF NOT EXISTS audit_events_task_idx ON audit_events (task_id);
