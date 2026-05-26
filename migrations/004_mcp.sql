-- MCP Tables (Phase 6A Slice 17)
-- Run: psql -U admin -d orchestrator -f migrations/004_mcp.sql

CREATE TABLE IF NOT EXISTS mcp_servers (
    id UUID PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    command TEXT DEFAULT '',
    args TEXT DEFAULT '[]',
    env TEXT DEFAULT '[]',
    url VARCHAR(1024) DEFAULT '',
    status VARCHAR(20) NOT NULL DEFAULT 'registered',
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW(),
    CONSTRAINT mcp_servers_name_unique UNIQUE (name)
);

CREATE TABLE IF NOT EXISTS mcp_tools (
    id UUID PRIMARY KEY,
    server_id UUID NOT NULL REFERENCES mcp_servers(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    description TEXT DEFAULT '',
    input_schema JSONB DEFAULT '{}',
    permissions TEXT DEFAULT '[]',
    CONSTRAINT mcp_tools_server_name_unique UNIQUE (server_id, name)
);

CREATE TABLE IF NOT EXISTS mcp_audit_logs (
    id UUID PRIMARY KEY,
    server_id UUID NOT NULL,
    tool_id UUID DEFAULT NULL,
    tool_name VARCHAR(255) NOT NULL,
    agent_id VARCHAR(255) NOT NULL DEFAULT '',
    workflow_id VARCHAR(255) NOT NULL DEFAULT '',
    request_id VARCHAR(255) NOT NULL,
    success BOOLEAN NOT NULL DEFAULT false,
    error TEXT DEFAULT '',
    error_type VARCHAR(50) DEFAULT '',
    duration_ms BIGINT DEFAULT 0,
    overflow BOOLEAN DEFAULT false,
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_mcp_servers_name ON mcp_servers(name);
CREATE INDEX IF NOT EXISTS idx_mcp_servers_status ON mcp_servers(status);
CREATE INDEX IF NOT EXISTS idx_mcp_tools_server_id ON mcp_tools(server_id);
CREATE INDEX IF NOT EXISTS idx_mcp_audit_server_id ON mcp_audit_logs(server_id);
CREATE INDEX IF NOT EXISTS idx_mcp_audit_workflow_id ON mcp_audit_logs(workflow_id);
CREATE INDEX IF NOT EXISTS idx_mcp_audit_created_at ON mcp_audit_logs(created_at);
