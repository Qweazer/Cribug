-- Phase 7G Workspace Store Persistence
-- Replaces string-pointer Ref convention with true persistence.
-- Advanced workflows (Debate, ToT, Research v2, Reflection) write
-- their long-form artifacts here. Polling APIs can then GET the
-- actual content via /api/v1/workspace?ref=...
--
-- Only refs + metadata go into tasks.metadata / Workflow history.
-- Full content lives in workspace_entries.content.

CREATE TABLE IF NOT EXISTS workspace_entries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ref VARCHAR(500) NOT NULL UNIQUE,
    workflow_id VARCHAR(255) NOT NULL,
    task_id VARCHAR(255),
    mode VARCHAR(50),
    kind VARCHAR(50) NOT NULL DEFAULT 'artifact',
    title VARCHAR(500),
    content TEXT NOT NULL DEFAULT '',
    content_type VARCHAR(50) NOT NULL DEFAULT 'text/plain',
    metadata JSONB,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Lookup by ref (the primary access pattern for GET /api/v1/workspace?ref=...).
CREATE INDEX IF NOT EXISTS workspace_entries_ref_idx ON workspace_entries (ref);

-- Lookup by workflow for bulk query (GET /api/v1/tasks/{id}/workspace).
CREATE INDEX IF NOT EXISTS workspace_entries_wf_idx ON workspace_entries (workflow_id);
