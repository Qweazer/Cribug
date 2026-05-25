CREATE TABLE IF NOT EXISTS react_steps (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workflow_id VARCHAR(128) NOT NULL,
    run_id VARCHAR(128) NOT NULL,
    node_id VARCHAR(128) NOT NULL,
    step_index INTEGER NOT NULL,
    reasoning TEXT NOT NULL DEFAULT '',
    action TEXT NOT NULL DEFAULT '',
    observation TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    UNIQUE(workflow_id, run_id, node_id, step_index)
);

CREATE INDEX IF NOT EXISTS idx_react_steps_workflow_id ON react_steps(workflow_id);
CREATE INDEX IF NOT EXISTS idx_react_steps_task ON react_steps(workflow_id, run_id);
CREATE INDEX IF NOT EXISTS idx_react_steps_created ON react_steps(created_at);
