CREATE TABLE IF NOT EXISTS tasks (
    id UUID PRIMARY KEY,
    session_id VARCHAR(255),
    query TEXT NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    result TEXT,
    error_type VARCHAR(50),
    error TEXT,
    max_total_tokens INT DEFAULT 8000,
    max_completion_tokens INT DEFAULT 1024,
    model VARCHAR(50) DEFAULT 'gpt-4o-mini',
    workflow_id VARCHAR(255) NOT NULL,
    run_id VARCHAR(255),
    usage_prompt_tokens INT,
    usage_completion_tokens INT,
    usage_total_tokens INT,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW(),
    CONSTRAINT tasks_workflow_id_unique UNIQUE (workflow_id),
    CONSTRAINT tasks_status_check CHECK (
        status IN ('pending', 'running', 'completed', 'failed', 'budget_exceeded', 'cancelled')
    )
);

CREATE TABLE IF NOT EXISTS executions (
    id UUID PRIMARY KEY,
    task_id UUID NOT NULL REFERENCES tasks(id),
    workflow_id VARCHAR(255) NOT NULL,
    run_id VARCHAR(255) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'running',
    error_type VARCHAR(50),
    error TEXT,
    started_at TIMESTAMP DEFAULT NOW(),
    completed_at TIMESTAMP,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW(),
    CONSTRAINT executions_workflow_run_unique UNIQUE (workflow_id, run_id),
    CONSTRAINT executions_status_check CHECK (
        status IN ('running', 'completed', 'failed', 'cancelled')
    )
);

CREATE TABLE IF NOT EXISTS llm_calls (
    id UUID PRIMARY KEY,
    call_id VARCHAR(255) NOT NULL,
    task_id UUID NOT NULL REFERENCES tasks(id),
    workflow_id VARCHAR(255) NOT NULL,
    run_id VARCHAR(255) NOT NULL,
    provider VARCHAR(50),
    model VARCHAR(50),
    estimated_prompt_tokens INT,
    max_completion_tokens INT,
    prompt_tokens INT,
    completion_tokens INT,
    total_tokens INT,
    latency_ms BIGINT,
    finish_reason VARCHAR(20),
    error_type VARCHAR(50),
    error TEXT,
    created_at TIMESTAMP DEFAULT NOW(),
    CONSTRAINT llm_calls_call_id_unique UNIQUE (call_id)
);

CREATE TABLE IF NOT EXISTS session_messages (
    id UUID PRIMARY KEY,
    session_id VARCHAR(255) NOT NULL,
    task_id UUID REFERENCES tasks(id),
    role VARCHAR(20) NOT NULL,
    content TEXT NOT NULL,
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_tasks_session_id ON tasks(session_id);
CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
CREATE INDEX IF NOT EXISTS idx_tasks_workflow_id ON tasks(workflow_id);
CREATE INDEX IF NOT EXISTS idx_executions_task_id ON executions(task_id);
CREATE INDEX IF NOT EXISTS idx_executions_workflow_id ON executions(workflow_id);
CREATE INDEX IF NOT EXISTS idx_llm_calls_task_id ON llm_calls(task_id);
CREATE INDEX IF NOT EXISTS idx_llm_calls_call_id ON llm_calls(call_id);
CREATE INDEX IF NOT EXISTS idx_session_messages_session_id ON session_messages(session_id);