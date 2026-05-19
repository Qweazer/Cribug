-- Add agent_role column to llm_calls for multi-agent workflow
ALTER TABLE llm_calls ADD COLUMN IF NOT EXISTS agent_role VARCHAR(50);

-- Add index for agent_role queries
CREATE INDEX IF NOT EXISTS idx_llm_calls_agent_role ON llm_calls(agent_role);