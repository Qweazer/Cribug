-- Phase 7I Router Strategy Upgrade (v2)
-- Adds multi-signal audit columns to routing_audit_logs.
-- Do NOT modify old 010 migration; all new columns go here.

ALTER TABLE routing_audit_logs
    ADD COLUMN IF NOT EXISTS signals_json JSONB,
    ADD COLUMN IF NOT EXISTS explanation_json JSONB,
    ADD COLUMN IF NOT EXISTS policy_version VARCHAR(20),
    ADD COLUMN IF NOT EXISTS score_breakdown_json JSONB,
    ADD COLUMN IF NOT EXISTS addon_capabilities JSONB,
    ADD COLUMN IF NOT EXISTS workspace_artifacts JSONB;
