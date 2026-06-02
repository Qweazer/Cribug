-- Phase 7E.5: Add structured metadata column to tasks for routed execution
-- result persistence (long-running workflows: Debate, ToT, Reflection, Research v2).
--
-- Existing tasks.result TEXT already stores the long final answer / result
-- preview. The new metadata JSONB column carries structured LLM metadata
-- (provider, model_used, mock, llm_calls, total_tokens) plus
-- mode-specific fields (debate_rounds, debate_final_position, etc.)
-- so the smoke tests can assert them from a single column.
--
-- Long transcripts / reports / debate arguments still go through
-- WorkspaceRef. The metadata JSONB stores only short references and
-- structured counts.

ALTER TABLE tasks ADD COLUMN IF NOT EXISTS metadata JSONB;

-- Index by workflow_id is already UNIQUE in 001_init.
-- Add a partial index on status='running' to make polling lookups cheap.
CREATE INDEX IF NOT EXISTS tasks_status_running_idx ON tasks (status) WHERE status = 'running';
CREATE INDEX IF NOT EXISTS tasks_status_completed_idx ON tasks (status) WHERE status IN ('completed', 'failed');
