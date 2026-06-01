-- Migration 010: routing_audit_logs for Phase 7A Advanced Strategy Router

CREATE TABLE IF NOT EXISTS routing_audit_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id VARCHAR(100) NOT NULL,
    workflow_id VARCHAR(100) NOT NULL,
    run_id VARCHAR(100),
    planned_mode VARCHAR(50) NOT NULL,
    mode VARCHAR(50) NOT NULL,
    fallback_reason VARCHAR(500),
    complexity_score FLOAT,
    risk_level VARCHAR(50),
    requires_approval BOOLEAN DEFAULT FALSE,
    requires_rag BOOLEAN DEFAULT FALSE,
    requires_tools BOOLEAN DEFAULT FALSE,
    requires_sandbox BOOLEAN DEFAULT FALSE,
    requires_workspace BOOLEAN DEFAULT FALSE,
    requires_reflection BOOLEAN DEFAULT FALSE,
    requires_debate BOOLEAN DEFAULT FALSE,
    requires_tot BOOLEAN DEFAULT FALSE,
    requires_research_v2 BOOLEAN DEFAULT FALSE,
    model_tier VARCHAR(50),
    token_budget INTEGER,
    cost_budget_usd FLOAT,
    confidence FLOAT,
    classifier_mode VARCHAR(50),
    short_reason VARCHAR(500),
    policy_trace_ref VARCHAR(200),
    classification_tokens INTEGER,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_routing_audit_workflow ON routing_audit_logs(workflow_id);
CREATE INDEX IF NOT EXISTS idx_routing_audit_session ON routing_audit_logs(session_id);
CREATE INDEX IF NOT EXISTS idx_routing_audit_created ON routing_audit_logs(created_at);
