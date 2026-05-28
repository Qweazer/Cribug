# Cribug Phase 6 重构任务书 — MCP Tool Runtime + Sandbox + Skills + Hooks + RAG + Research-Synthesis

## 版本说明

本文档为 Phase 6 **修订版**（v2.0），基于 Phase 5G（Handoff Mechanism 完成后）已确立的架构原则，引入 MCP Tool Runtime、Sandbox/WASI、Skills System、Hooks Event System、RAG/Qdrant Long-term Memory 和 Research-Synthesis v1 能力。

**Phase 6 核心目标**：MCP Tool Runtime + Sandbox/WASI + Skills System + Hooks Event System + RAG/Qdrant Long-term Memory + Research-Synthesis v1

**关键约束**：
- 所有外部 IO 必须放在 Activity 中，Workflow 保持 deterministic
- Workspace 延续 Phase 5 Append-only 原则
- 不修改 Phase 4/5 已冻结主架构（DAG、ReAct、Swarm、P2P、Workspace、Handoff）
- 不塞入 Phase 7 能力（Reflection、TOT、Debate、Research-Synthesis v2、HITL/UI）

---

## 一、项目定位

### 1.1 当前已完成基线

| Phase | 内容 | 状态 |
|-------|------|------|
| Phase 1 | Gateway + Temporal Worker + SimpleWorkflow + Postgres + Redis | 完成后 |
| Phase 2 | AgentActivity 全链路 + Session Memory + Budget + SSE | 完成后 |
| Phase 3D Slice 7 | DAG Concurrency（LocalDispatchOptions 并发） | 完成后 |
| Phase 3D Slice 8 | ReAct Reasoning Loop（Workflow 级别） | 完成后 |
| Phase 3D Slice 9 | Real tiktoken Tokenizer（两级缓存） | 完成后 |
| Phase 4 Slice 10 | DAG 可视化 | 完成后 |
| Phase 4 Slice 11 | ReAct 暂停/恢复（Workflow 级别 Loop） | 完成后 |
| Phase 4 Slice 12 | 两级 LRU 缓存 | 完成后 |
| Phase 5A Slice 10 | Lead Agent / SwarmWorkflow（Selector + Timer 响应式等待） | 完成后 |
| Phase 5B Slice 11 | Agent P2P Communication（SendAgentMessage/FetchAgentMessages） | 完成后 |
| Phase 5C Slice 12 | Workspace Append-only + LLM Synthesis | 完成后 |
| Phase 5D Slice 13 | State Synchronization（SignalChannel 驱动） | 完成后 |
| Phase 5E Slice 14 | Conflict Resolution（已删除，Workspace 纯 Append-only） | 删除 |
| Phase 5F Slice 15 | Security & Access Control（Lead/Worker 权限分离） | 完成后 |
| **Phase 5G Slice 16** | **Agent Handoff Mechanism** | **完成后** |

### 1.2 Phase 6 重构目标

| Slice | 功能 | 描述 | 默认测试 |
|-------|------|------|----------|
| Phase 6A Slice 17 | **MCP Tool Runtime** | MCP server 注册、tool discovery、schema 校验、tool call、audit | mock |
| Phase 6B Slice 18 | **Sandbox / WASI Execution** | 受限代码执行、stdout/stderr、timeout、resource limit、audit | mock |
| Phase 6C Slice 19 | **Skills System** | Skill Manifest、Registry、Executor、Permission、skill_calls audit | mock |
| Phase 6D Slice 20 | **Hooks Event System** | before/after tool、before/after llm、on_agent_step、on_workspace_append、on_handoff、on_error | mock |
| Phase 6E Slice 21 | **RAG / Qdrant Long-term Memory** | 文档接入、chunking、embedding（mock/real）、Qdrant upsert/search、rerank、context packing | fixture embedding + fixture Qdrant |
| Phase 6F Slice 22 | **Research-Synthesis v1** | Query decomposition、多子问题 RAG、evidence Workspace、LLM synthesis、evidence metadata | mock LLM |

---

## 二、职责边界（强制约束 - Shannon 范式）

### 2.1 组件职责约束

| 组件 | Phase 6 职责 | 禁止 |
|------|-------------|------|
| Gateway | 接收 MCP/Sandbox/Skills/Hooks/RAG 请求，创建任务，返回 task_id/workflow_id/run_id/SSE | 直接调用 MCP server/Sandbox/WASI、直接做 Agent 推理、执行业务逻辑 |
| Workflow | 确定性编排逻辑、调用 Activities、维护 ReAct loop、维护 Swarm 协调、维护 Handoff | 直接 HTTP/Qdrant/MCP/Sandbox/Redis/DB、创建 goroutine、使用 `time.Now()`、使用随机数 |
| Activity | 所有外部 IO：Qdrant 向量读写、MCP server 调用、WASI sandbox 执行、Postgres/Redis 读写、LLM Service 调用、Hook handler 执行 | 做全局业务编排（只做外部 IO） |
| Python LLM Service | embedding 生成（mock/real）、tokenize、LLM call（mock/real） | 任务编排、状态管理、Workflow 调度 |
| Qdrant | 向量存储与检索 | 保存业务状态、执行业务逻辑 |
| MCP Server | 外部工具能力提供 | 直接修改 Cribug 状态、访问 Cribug 内部 API |
| WASI Sandbox | 代码执行隔离环境 | 访问宿主机敏感资源、默认网络访问 |
| Lead Agent | 决定是否调用工具/执行代码/使用 Skill/触发 Hook、在授权范围内调度 Worker | 自己直接访问外部系统（MCP/Sandbox/Qdrant） |
| Worker Agent | 在授权范围内执行 tool action、使用 Skill、触发 Hook | 越权调用工具、管理其他 Worker、私自注册 MCP server |
| Hook Handler | 执行 before/after hook、on_* event hook | 阻塞主流程（除 blocking permission/policy hook 外） |

### 2.2 MCP 约束

- **MCP server 必须注册才能调用**
- MCP tool call 必须有 timeout（默认 30s）
- MCP result size 必须限制（默认 64KB）
- MCP tool schema 必须校验
- MCP 调用必须记录 audit log
- **env secrets 必须脱敏存储，不能明文写 audit log**
- Worker Agent 不能私自注册 MCP server（必须通过 Lead 或 admin）
- MCP tool 权限必须与 Agent 角色关联
- MCP server 注册默认只能由 admin / lead / allowlist 完成

### 2.3 Sandbox / WASI 约束

- **Sandbox 默认禁止网络访问**
- **Sandbox 默认禁止访问宿主机文件系统**（只能写临时 /workspace mount）
- stdout/stderr 最大长度限制（默认 64KB）
- CPU timeout（默认 10s）
- memory limit（默认 128MB）
- wall-clock timeout（默认 30s）
- 禁止 fork bomb
- Sandbox result 只能通过 WorkspaceAppend 写入
- **Phase 6B 默认只要求 WASI demo / fixture code execution**
- Python/JS 执行如果实现，必须通过 WASI runtime / 受限 runner，不作为 Phase 6B 必须验收项

### 2.4 Skills System 约束

- **Skill 是工具组合能力，不是原子工具**
- Skill 可以封装 RAG、MCP、Sandbox、LLM 中的任意组合
- Skill 执行必须通过 SkillExecutorWorkflow 编排
- PlanSkillActivity 只负责读取 manifest、校验权限、生成 SkillPlan
- Skill 的每个原子步骤由 Workflow 调用对应 Activity 执行
- 禁止在 Activity 内调用 workflow.ExecuteActivity
- Skill result 只能通过 WorkspaceAppend 写入
- Skill 必须有 manifest（id、name、description、input_schema、output_schema、required_tools、sandbox_policy、permissions、timeout_seconds）
- Skill 调用必须记录 skill_calls audit

### 2.5 Hooks Event System 约束

- **Workflow 只产生 hook event，不直接执行外部 hook IO**
- Hook handler 如果需要外部 IO，必须通过 Activity 执行
- Hook 默认不阻塞主流程
- **blocking hook 只允许用于权限类 / policy 类检查**
- Hook 失败默认写 warning / audit，不破坏主流程
- HookPoint: before_tool_call、after_tool_call、before_llm_call、after_llm_call、on_agent_step、on_workspace_append、on_handoff、on_error

### 2.6 RAG / Qdrant 约束

- **Qdrant 所有操作必须在 Activity 中**
- Workflow 只能通过 Activity 间接调用 Qdrant
- Embedding 生成必须通过 Python LLM Service Activity（mock 或 real）
- 检索结果只能通过 WorkspaceAppend 写入，不能直接修改 Workspace 状态
- **大型 retrieved chunks 必须通过 Workspace 或外部存储引用**
- **Workflow history 只保存 retrieval_id / summary / metadata，不传递 chunk content / embedding vector**

### 2.7 Workspace / Swarm / ReAct / Handoff 继承约束（Phase 4/5 冻结）

**Workspace 约束（Phase 5 冻结）：**
- Workspace 是 Append-only 事件流，不是文件系统
- 所有结果（MCP/Sandbox/Skill/RAG/Research evidence）必须通过 WorkspaceAppend 写入
- 不做版本控制、乐观锁、文件锁
- 语义层冲突消解交给最后的 SynthesizeResults LLM 聚合层

**SwarmWorkflow 约束（Phase 5 冻结）：**
- Lead Agent 可以决定是否需要 MCP 工具调用 / Sandbox 执行 / Skill / RAG / Hook
- Worker Agent 可以在授权范围内调用 Activities
- Worker Agent 不能绕过 Lead 决策创建新的子任务

**P2P 约束（Phase 5 冻结）：**
- Agent 可以通过 SendAgentMessage 请求其他 Agent 的结果摘要
- P2P 消息本身不承载大 payload，大结果应写入 Workspace

**Handoff 约束（Phase 5G 冻结）：**
- Agent 可以通过 Handoff 转移控制权
- Handoff 后 on_handoff hook 触发
- 目标 Agent 继承源 Agent 的 context（可选）

**ReAct Loop 约束（Phase 4 冻结）：**
- ReAct Loop 不允许放回 Activity 内循环
- 每次 tool action 对应独立 Activity 调用
- tool observation 进入 Workflow 级别 history
- Skill / MCP tool / Sandbox 都是 ReAct action 的候选工具

---

## 三、阶段范围

### Phase 6A Slice 17：MCP Tool Runtime

#### 3.1 核心架构澄清

MCP Tool Runtime 使 Cribug Agent 可以发现并调用外部 MCP tools。MCP server 提供工具能力，Cribug 负责注册、发现、调用和审计。

**关键约束**：
- MCP server 必须注册才能调用
- env secrets 必须脱敏存储，不能明文写 audit log
- Worker Agent 不能私自注册 MCP server
- 所有 MCP 调用必须在 Activity 中执行

#### 3.2 数据结构

```go
// MCPServer - MCP 服务器
type MCPServer struct {
    ID          string    `json:"id"`
    Name        string    `json:"name"`         // 唯一名称
    Command     string    `json:"command"`       // 启动命令
    Args        []string  `json:"args"`          // 启动参数
    Env         []string  `json:"env"`           // 环境变量（脱敏后）
    EnvRaw      []string  `json:"-"`            // 原始敏感值，不持久化
    URL         string    `json:"url"`           // HTTP endpoint（可选）
    Status      string    `json:"status"`        // "registered" | "running" | "stopped" | "error"
    CreatedAt   time.Time `json:"created_at"`
    UpdatedAt   time.Time `json:"updated_at"`
}

// MCPTool - MCP 工具
type MCPTool struct {
    ID          string                 `json:"id"`
    ServerID    string                 `json:"server_id"`
    Name        string                 `json:"name"`         // 工具唯一名称
    Description string                 `json:"description"`
    InputSchema map[string]interface{} `json:"input_schema"`  // JSON Schema
    Permissions []string               `json:"permissions"`
}

// MCPToolCallInput - 工具调用请求
type MCPToolCallInput struct {
    ToolID      string                 `json:"tool_id"`
    ServerID    string                 `json:"server_id"`
    ToolName    string                 `json:"tool_name"`
    Arguments   map[string]interface{} `json:"arguments"`
    Timeout     int                    `json:"timeout"`      // 秒，默认 30
    RequestID   string                 `json:"request_id"`
    AgentID     string                 `json:"agent_id"`
    WorkflowID  string                 `json:"workflow_id"`
}

// MCPToolResult - 工具调用结果
type MCPToolResult struct {
    RequestID   string                 `json:"request_id"`
    ToolID      string                 `json:"tool_id"`
    ToolName    string                 `json:"tool_name"`
    Success     bool                   `json:"success"`
    Output      map[string]interface{} `json:"output,omitempty"`
    Error       string                 `json:"error,omitempty"`
    DurationMs  int64                  `json:"duration_ms"`
    OutputSize  int                    `json:"output_size"`    // bytes
}
```

#### 3.3 Activity 设计

```go
// RegisterMCPServerActivity - 注册 MCP server（仅 admin/lead/allowlist）
type RegisterMCPServerInput struct {
    Name    string   `json:"name"`
    Command string   `json:"command"`
    Args    []string `json:"args"`
    Env     []string `json:"env"`      // 敏感值，脱敏后存储
    URL     string   `json:"url"`
}

type RegisterMCPServerResult struct {
    ServerID string `json:"server_id"`
    Status   string `json:"status"`
}

// DiscoverMCPToolsActivity - 发现工具
type DiscoverMCPToolsInput struct {
    ServerID string `json:"server_id"`
}

type DiscoverMCPToolsResult struct {
    ServerID string    `json:"server_id"`
    Tools    []MCPTool `json:"tools"`
}

// CallMCPToolActivity - 调用工具
type CallMCPToolInput struct {
    ToolID     string                 `json:"tool_id"`
    ServerID   string                 `json:"server_id"`
    ToolName   string                 `json:"tool_name"`
    Arguments  map[string]interface{} `json:"arguments"`
    Timeout    int                    `json:"timeout"`
    AgentID    string                 `json:"agent_id"`
    WorkflowID string                 `json:"workflow_id"`
}

type CallMCPToolResult struct {
    Success    bool                   `json:"success"`
    Output     map[string]interface{} `json:"output,omitempty"`
    Error      string                 `json:"error,omitempty"`
    DurationMs int64                  `json:"duration_ms"`
}

// AuditMCPToolCallActivity - 脱敏审计 MCP 调用
type AuditMCPToolCallInput struct {
    ServerID   string `json:"server_id"`
    ToolID     string `json:"tool_id"`
    ToolName   string `json:"tool_name"`
    AgentID    string `json:"agent_id"`
    WorkflowID string `json:"workflow_id"`
    Success    bool   `json:"success"`
    DurationMs int64  `json:"duration_ms"`
    Error      string `json:"error,omitempty"`
}
```

#### 3.4 API 设计

```bash
# 注册 MCP server（仅 admin/lead）
POST /api/v1/mcp/servers
Content-Type: application/json

Request:
{
    "name": "filesystem-tools",
    "command": "npx",
    "args": ["-y", "@modelcontextprotocol/server-filesystem", "/workspace"],
    "env": ["API_KEY=***"]  # 敏感值脱敏
}

Response:
{
    "server_id": "mcp-server-xxx",
    "name": "filesystem-tools",
    "status": "registered"
}

# 列出已注册 servers
GET /api/v1/mcp/servers

# 发现 server 的工具
GET /api/v1/mcp/servers/{server_id}/tools

# 调用 MCP tool
POST /api/v1/mcp/tools/{tool_id}/call
Content-Type: application/json

Request:
{
    "server_id": "mcp-server-xxx",
    "arguments": {"path": "/workspace/test.txt"},
    "timeout": 30
}

Response:
{
    "request_id": "req-xxx",
    "success": true,
    "output": {"content": "Hello"},
    "duration_ms": 15
}
```

#### 3.5 安全约束

```go
// env secrets 脱敏存储
func (a *Activities) RegisterMCPServer(ctx context.Context, in RegisterMCPServerInput) (*RegisterMCPServerResult, error) {
    // 脱敏 env 中的敏感值
    sanitizedEnv := sanitizeEnv(in.Env)

    // 原始敏感值不持久化，只用于启动时注入
    server := MCPServer{
        Name:    in.Name,
        Command: in.Command,
        Args:    in.Args,
        Env:     sanitizedEnv, // 只存储脱敏后的值
        Status:  "registered",
    }
    // 写入 DB 时 env 为脱敏值
    // ...
}

// sanitizeEnv - 脱敏环境变量
func sanitizeEnv(env []string) []string {
    var sanitized []string
    sensitivePatterns := []string{"KEY", "TOKEN", "SECRET", "PASSWORD"}
    for _, e := range env {
        masked := e
        for _, pattern := range sensitivePatterns {
            if strings.Contains(e, pattern) {
                masked = regexp.ReplaceAllString(e, `=.*`, "=***")
                break
            }
        }
        sanitized = append(sanitized, masked)
    }
    return sanitized
}

// MCP server allowlist
var allowedMCPServers = map[string]bool{
    "filesystem-tools": true,  // 企业允许列表
    "github-tools":     true,
}
```

#### 3.6 与 ReAct 的衔接

```go
// ReAct action 选择 MCP tool
type ReActToolAction struct {
    ActionType string                 `json:"action_type"` // "mcp_tool"
    ToolName   string                 `json:"tool_name"`
    Args       map[string]interface{} `json:"args"`
}

// MCP tool result 进入 ReAct history
observations = append(observations, Observation{
    Type:    "mcp_tool",
    Content: fmt.Sprintf("Called %s, output: %v", action.ToolName, result.Output),
})

// 大结果写入 Workspace
_ = workflow.ExecuteActivity(ctx, "WorkspaceAppend", WorkspaceAppendInput{
    WorkflowID: workflowID,
    Topic:     fmt.Sprintf("mcp:%s:results", result.RequestID),
    Entry: map[string]interface{}{
        "type":      "mcp_result",
        "tool_name": result.ToolName,
        "success":   result.Success,
        "output":    result.Output,
        "duration_ms": result.DurationMs,
    },
    Timestamp: workflow.Now(ctx),
})
```

#### 3.7 成功路径

```
Lead Agent 决定调用 MCP tool
  → 验证 Worker 权限（blocking hook）
  → CallMCPToolActivity
  → MCP server 执行
  → 返回 result
  → after_tool_call hook 触发
  → SaveMCPToolResultActivity（WorkspaceAppend）
  → AuditMCPToolCallActivity（PostgreSQL，脱敏）
```

#### 3.8 失败路径

```
Server 未注册 → 返回 error，不执行
Tool 未发现 → 返回 error
权限不足（blocking hook） → 返回 permission_denied error
Tool call 超时 → Activity 重试，仍超时返回 error
Result size 超限 → 截断 + 标记 overflow
Server 不可用 → 返回 error，记录 audit log
```

#### 3.9 验收标准

| 验收项 | 通过条件 |
|--------|----------|
| Server 注册 | MCP server 可注册（admin/lead/allowlist） |
| env 脱敏 | 敏感环境变量不明文存储 |
| Tool discovery | 正确获取 tool schema |
| Tool call | 可调用并返回结果 |
| 权限控制 | 未授权 tool 被 blocking hook 拒绝 |
| Timeout | 超时正确处理 |
| Audit log | 所有调用记录（env 脱敏） |
| Workspace 写入 | 结果写入 Workspace topic |

---

### Phase 6B Slice 18：Sandbox / WASI Execution

#### 3.10 核心架构澄清

Sandbox / WASI Execution 提供安全代码执行能力，Agent 可以在受控隔离环境中执行代码片段，获取 stdout/stderr/exit_code。

**关键约束**：
- Sandbox 默认禁止网络访问
- Sandbox 默认禁止访问宿主机文件系统（只能写 /workspace）
- 所有 sandbox 执行必须在 Activity 中
- Sandbox result 只能通过 WorkspaceAppend 写入
- **Phase 6B 默认只要求 WASI demo / fixture code execution**

#### 3.11 数据结构

```go
// SandboxJob - Sandbox 任务
type SandboxJob struct {
    ID         string         `json:"id"`
    WorkflowID string         `json:"workflow_id"`
    AgentID    string         `json:"agent_id"`
    Language   string         `json:"language"`     // "wasi" | "python" | "javascript"
    Code       string         `json:"code"`
    Stdin      string         `json:"stdin,omitempty"`
    Policy     SandboxPolicy  `json:"policy"`
    Status     string         `json:"status"`      // "queued" | "running" | "completed" | "failed" | "timeout"
    CreatedAt  time.Time      `json:"created_at"`
}

// SandboxPolicy - 安全策略
type SandboxPolicy struct {
    MaxCPUSeconds   int  `json:"max_cpu_seconds"`    // 默认 10
    MaxWallSeconds  int  `json:"max_wall_seconds"`   // 默认 30
    MaxMemoryMB     int  `json:"max_memory_mb"`      // 默认 128
    MaxStdoutBytes  int  `json:"max_stdout_bytes"`   // 默认 65536
    MaxStderrBytes  int  `json:"max_stderr_bytes"`   // 默认 65536
    AllowNetwork    bool `json:"allow_network"`       // 默认 false
    AllowFilesystem bool `json:"allow_filesystem"`    // 默认 false（仅 /workspace）
}

// SandboxOutput - 执行输出
type SandboxOutput struct {
    JobID            string `json:"job_id"`
    ExitCode         int    `json:"exit_code"`
    Stdout           string `json:"stdout"`
    Stderr           string `json:"stderr"`
    DurationMs       int64  `json:"duration_ms"`
    MemoryUsedMB     int    `json:"memory_used_mb,omitempty"`
    OOMKilled        bool   `json:"oom_killed,omitempty"`
    TimedOut         bool   `json:"timed_out,omitempty"`
    StdoutTruncated  bool   `json:"stdout_truncated,omitempty"`
    StderrTruncated  bool   `json:"stderr_truncated,omitempty"`
}
```

#### 3.12 Activity 设计

```go
// ValidateSandboxPolicyActivity - 验证安全策略
type ValidateSandboxPolicyInput struct {
    Policy    SandboxPolicy `json:"policy"`
    AgentRole string        `json:"agent_role"` // "lead" | "worker"
}

type ValidateSandboxPolicyResult struct {
    Valid   bool     `json:"valid"`
    Errors  []string `json:"errors,omitempty"`
}

// RunWASIActivity - 执行 WASI sandbox
type RunWASIActivityInput struct {
    WorkflowID string        `json:"workflow_id"`
    AgentID    string        `json:"agent_id"`
    Language   string        `json:"language"`
    Code       string        `json:"code"`
    Stdin      string        `json:"stdin,omitempty"`
    Policy     SandboxPolicy `json:"policy"`
}

type RunWASIActivityResult struct {
    ExitCode        int    `json:"exit_code"`
    Stdout          string `json:"stdout"`
    Stderr          string `json:"stderr"`
    DurationMs      int64  `json:"duration_ms"`
    MemoryUsedMB    int    `json:"memory_used_mb,omitempty"`
    OOMKilled       bool   `json:"oom_killed,omitempty"`
    TimedOut        bool   `json:"timed_out,omitempty"`
    StdoutTruncated bool   `json:"stdout_truncated,omitempty"`
    StderrTruncated bool   `json:"stderr_truncated,omitempty"`
}

// SaveSandboxResultActivity - 保存结果到 Workspace
type SaveSandboxResultInput struct {
    WorkflowID string          `json:"workflow_id"`
    JobID      string          `json:"job_id"`
    Output     SandboxOutput   `json:"output"`
    Timestamp  time.Time      `json:"timestamp"`
}

// AuditSandboxExecutionActivity - 审计执行
type AuditSandboxExecutionInput struct {
    JobID      string `json:"job_id"`
    WorkflowID string `json:"workflow_id"`
    AgentID    string `json:"agent_id"`
    Language   string `json:"language"`
    CodeHash   string `json:"code_hash"`
    Policy     string `json:"policy"`
    ExitCode   int    `json:"exit_code"`
    DurationMs int64  `json:"duration_ms"`
    OOMKilled  bool   `json:"oom_killed"`
    TimedOut   bool   `json:"timed_out"`
}
```

#### 3.13 API 设计

```bash
# 执行 sandbox 代码
POST /api/v1/sandbox/run
Content-Type: application/json

Request:
{
    "workflow_id": "wf-xxx",
    "agent_id": "worker-1",
    "language": "wasi",
    "code": "...",  # WASI module bytes 或 fixture code
    "policy": {
        "max_cpu_seconds": 10,
        "max_memory_mb": 128,
        "allow_network": false
    }
}

Response:
{
    "job_id": "sandbox-xxx",
    "status": "queued"
}

# 获取任务状态
GET /api/v1/sandbox/jobs/{job_id}

# 获取任务日志
GET /api/v1/sandbox/jobs/{job_id}/logs
```

#### 3.14 默认安全策略

```go
var DefaultSandboxPolicy = SandboxPolicy{
    MaxCPUSeconds:   10,
    MaxWallSeconds:  30,
    MaxMemoryMB:     128,
    MaxStdoutBytes:  65536,
    MaxStderrBytes:  65536,
    AllowNetwork:    false,        // 默认禁止网络
    AllowFilesystem: false,        // 默认禁止文件系统
}
```

#### 3.15 与 ReAct 的衔接

```go
// ReAct action 请求 sandbox execution
type ReActToolAction struct {
    ActionType string                 `json:"action_type"` // "sandbox"
    ToolName   string                 `json:"tool_name"`   // "wasi" | "python" | "javascript"
    Args       map[string]interface{} `json:"args"`
    // Args: {"code": "...", "timeout": 30}
}

// Sandbox result 作为 observation
observations = append(observations, Observation{
    Type:    "sandbox",
    Content: fmt.Sprintf("Exit code: %d, stdout: %s", result.ExitCode, result.Stdout),
})

// 大 stdout/stderr 写入 Workspace
_ = workflow.ExecuteActivity(ctx, "WorkspaceAppend", WorkspaceAppendInput{
    WorkflowID: workflowID,
    Topic:     fmt.Sprintf("sandbox:%s:output", jobID),
    Entry: map[string]interface{}{
        "type":       "sandbox_result",
        "job_id":     jobID,
        "exit_code":  result.ExitCode,
        "stdout":     result.Stdout,
        "stderr":     result.Stderr,
        "duration_ms": result.DurationMs,
    },
    Timestamp: workflow.Now(ctx),
})
```

#### 3.16 成功路径

```
ReAct action 请求 sandbox
  → ValidateSandboxPolicyActivity（验证 policy）
  → RunWASIActivity（执行代码）
  → SaveSandboxResultActivity（WorkspaceAppend）
  → AuditSandboxExecutionActivity（PostgreSQL）
```

#### 3.17 失败路径

```
Policy validation 失败 → 返回 error，不执行
Sandbox timeout → Activity 返回 timeout error
OOM killed → 返回 error，记录 audit
Stdout/stderr 超限 → 截断 + 标记 truncated
Agent 无 sandbox 权限 → blocking hook 拒绝
Malicious code detection → 拒绝执行，记录 audit
```

#### 3.18 验收标准

| 验收项 | 通过条件 |
|--------|----------|
| 基本执行 | WASI / fixture code 可执行 |
| 输出捕获 | stdout/stderr 正确捕获 |
| Timeout | CPU/wall timeout 正确处理 |
| Memory limit | OOM 正确处理 |
| Network 禁止 | 默认禁止网络访问 |
| Filesystem 限制 | 默认只能写 /workspace |
| ReAct 集成 | ReAct action 可触发 sandbox |
| Workspace 写入 | 结果写入 Workspace topic |
| Audit log | 执行记录写入 PostgreSQL |

---

### Phase 6C Slice 19：Skills System

#### 3.19 核心架构澄清

Skill 是工具组合能力，可以封装 RAG、MCP、Sandbox、LLM 等多种原子能力。Tool 是原子能力，Skill 是工具组合能力。

**关键约束**：
- Skill 执行必须通过 SkillExecutorWorkflow 编排
- PlanSkillActivity 只负责读取 manifest、校验权限、生成 SkillPlan
- Skill 的每个原子步骤由 Workflow 调用对应 Activity 执行
- 禁止在 Activity 内调用 workflow.ExecuteActivity
- Skill result 只能通过 WorkspaceAppend 写入
- Skill 调用必须记录 skill_calls audit
- SkillManifest 支持 RequiredSkills，必须限制递归深度（max_skill_call_depth，默认 2）
- 注册时检测 dependency cycle，执行时禁止循环调用
- 不做 Skill Marketplace

**方案 A（推荐）：Workflow 编排 Skill**

```
SkillExecutionInput
  → PlanSkillActivity（读取 manifest、校验权限、返回 SkillPlan）
  → Workflow 根据 SkillPlan 调用原子 Activities（RAGSearch / CallMCPTool / RunWASI / LLMActivity）
  → SkillFinalizeActivity（汇总结果、写入 Workspace）
```

**方案 B（不推荐）：All-in-one Activity**
- ExecuteSkillActivity 内部只能调用普通 Go service/helper
- 不能调用 workflow.ExecuteActivity（违反 deterministic）
- 不能调用 workflow.Now(ctx)（必须由 Workflow 传入）
- 可观测性差、单步重试粒度粗

本任务书采用方案 A。

#### 3.20 数据结构

```go
// SkillManifest - Skill 定义
type SkillManifest struct {
    SkillID           string                 `json:"skill_id"`
    Name              string                 `json:"name"`              // 唯一名称
    Description       string                 `json:"description"`
    Version           string                 `json:"version"`           // semver
    InputSchema       map[string]interface{} `json:"input_schema"`      // JSON Schema
    OutputSchema      map[string]interface{} `json:"output_schema"`     // JSON Schema
    RequiredTools     []string               `json:"required_tools"`    // 需要的 tool names
    RequiredSkills    []string               `json:"required_skills"`   // 需要的 sub-skills（需检测 cycle）
    MaxSkillCallDepth int                    `json:"max_skill_call_depth"` // 递归深度限制，默认 2
    SandboxPolicy     *SandboxPolicy         `json:"sandbox_policy,omitempty"`
    Permissions       []string               `json:"permissions"`       // 需要的权限
    TimeoutSeconds    int                    `json:"timeout_seconds"`   // 默认 60
    CreatedAt         time.Time              `json:"created_at"`
    UpdatedAt         time.Time              `json:"updated_at"`
}

// SkillPlan - Skill 执行计划（PlanSkillActivity 返回）
type SkillPlan struct {
    SkillID        string                 `json:"skill_id"`
    SkillName      string                 `json:"skill_name"`
    Steps          []SkillStep           `json:"steps"`          // 执行步骤
    Permissions    []string              `json:"permissions"`   // 需要但可能缺失的权限
    TotalTimeoutMs int64                 `json:"total_timeout_ms"`
    Depth          int                    `json:"depth"`          // 当前递归深度
}

// SkillStep - 单个执行步骤
type SkillStep struct {
    StepIndex   int                    `json:"step_index"`
    ActionType  string                 `json:"action_type"`  // "rag_search" | "mcp_tool" | "sandbox" | "llm" | "workspace_append"
    ToolName    string                 `json:"tool_name"`
    Arguments   map[string]interface{} `json:"arguments"`
    SubSkillID  string                 `json:"sub_skill_id,omitempty"`  // 如果 action_type == "sub_skill"
    WorkspaceRef string               `json:"workspace_ref,omitempty"` // 本步骤结果写入的 topic
}

// SkillExecutionInput - Skill 执行输入（Workflow 接收）
type SkillExecutionInput struct {
    SkillID     string                 `json:"skill_id"`
    Arguments   map[string]interface{} `json:"arguments"`
    AgentID     string                 `json:"agent_id"`
    WorkflowID  string                 `json:"workflow_id"`
    CallDepth   int                    `json:"call_depth"`   // 当前递归深度
}

// SkillExecutionResult - Skill 执行结果（Workflow 返回）
type SkillExecutionResult struct {
    SkillID        string                 `json:"skill_id"`
    Success        bool                   `json:"success"`
    Output         map[string]interface{} `json:"output,omitempty"`
    Error          string                 `json:"error,omitempty"`
    DurationMs     int64                  `json:"duration_ms"`
    ToolCalls      []string               `json:"tool_calls"`      // 实际调用的 tool names
    WorkspaceTopic string                 `json:"workspace_topic"` // 主要结果写入的 topic
}

// SkillCallTrace - Skill 调用轨迹
type SkillCallTrace struct {
    ID             string    `json:"id"`
    SkillID        string    `json:"skill_id"`
    SkillName      string    `json:"skill_name"`
    Arguments      string    `json:"arguments"`     // JSON string
    Output         string    `json:"output"`       // JSON string（摘要）
    Status         string    `json:"status"`       // "started" | "completed" | "failed"
    DurationMs     int64     `json:"duration_ms"`
    ToolCalls      []string  `json:"tool_calls"`
    AgentID        string    `json:"agent_id"`
    WorkflowID     string    `json:"workflow_id"`
    CreatedAt      time.Time `json:"created_at"`
}
```

#### 3.21 Activity 设计

```go
// RegisterSkillActivity - 注册 Skill manifest
type RegisterSkillInput struct {
    Manifest SkillManifest `json:"manifest"`
}

type RegisterSkillResult struct {
    SkillID string `json:"skill_id"`
    Status  string `json:"status"`
}

// GetSkillActivity - 获取 Skill manifest
type GetSkillInput struct {
    SkillID string `json:"skill_id"`
}

// ListSkillsActivity - 列出可用的 Skills
type ListSkillsInput struct {
    Filter map[string]interface{} `json:"filter,omitempty"` // 按 category、tag 等过滤
}

// PlanSkillActivity - 读取 Skill manifest，校验权限，返回执行计划
// 注意：这是 Activity，不能调用 workflow.ExecuteActivity
type PlanSkillInput struct {
    SkillID    string                 `json:"skill_id"`
    Arguments  map[string]interface{} `json:"arguments"`
    AgentID    string                 `json:"agent_id"`
    CallDepth  int                    `json:"call_depth"` // 当前递归深度
}

type PlanSkillResult struct {
    Plan      SkillPlan `json:"plan"`
    Error     string    `json:"error,omitempty"` // 如果权限/cycle 校验失败
}

// SkillExecutorWorkflow - Workflow 编排技能执行（方案 A）
// 不在 Activity 内调用 workflow.ExecuteActivity，而是由 Workflow 自己编排
func SkillExecutorWorkflow(ctx workflow.Context, input SkillExecutionInput) (SkillExecutionResult, error) {
    workflowID := workflow.GetInfo(ctx).WorkflowExecution.ID

    // Step 1: PlanSkillActivity - 获取 manifest + 校验 + 生成计划
    var planResult PlanSkillResult
    planActivityOpts := workflow.ActivityOptions{
        StartToCloseTimeout: 30 * time.Second,
        RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
    }
    planCtx := workflow.WithActivityOptions(ctx, planActivityOpts)
    err := workflow.ExecuteActivity(planCtx, "PlanSkillActivity",
        PlanSkillInput{
            SkillID:   input.SkillID,
            Arguments: input.Arguments,
            AgentID:   input.AgentID,
            CallDepth:  input.CallDepth,
        },
    ).Get(planCtx, &planResult)
    if err != nil {
        return SkillExecutionResult{Success: false, Error: err.Error()}, err
    }
    if planResult.Error != "" {
        return SkillExecutionResult{Success: false, Error: planResult.Error}, nil
    }

    plan := planResult.Plan
    var executedToolCalls []string
    workspaceTopic := fmt.Sprintf("skill:%s:results:%s", plan.SkillName, workflowID[:8])

    // Step 2: Workflow 根据 plan 逐步执行原子 Activities
    for _, step := range plan.Steps {
        switch step.ActionType {
        case "rag_search":
            var result SearchQdrantResult
            err := workflow.ExecuteActivity(ctx, "SearchQdrantActivity",
                SearchQdrantInput{
                    Collection: step.Arguments["collection"].(string),
                    TopK:       int(step.Arguments["top_k"].(float64)),
                    Filter:     step.Arguments["filter"].(map[string]interface{}),
                },
            ).Get(ctx, &result)
            if err != nil {
                return SkillExecutionResult{Success: false, Error: err.Error()}, err
            }
            executedToolCalls = append(executedToolCalls, step.ToolName)

            // 写入 Workspace
            _ = workflow.ExecuteActivity(ctx, "WorkspaceAppend",
                WorkspaceAppendInput{
                    WorkflowID: workflowID,
                    Topic:       step.WorkspaceRef,
                    Entry: map[string]interface{}{
                        "type":    "rag_results",
                        "query_id": result.QueryID,
                        "chunk_ids": result.ChunkIDs,
                    },
                    Timestamp: planResult.Plan.Steps[0].Arguments["timestamp"].(time.Time), // Workflow 传入
                },
            )

        case "mcp_tool":
            var result CallMCPToolResult
            err := workflow.ExecuteActivity(ctx, "CallMCPToolActivity",
                CallMCPToolInput{
                    ServerID:  step.Arguments["server_id"].(string),
                    ToolName:  step.ToolName,
                    Arguments: step.Arguments,
                    AgentID:   input.AgentID,
                },
            ).Get(ctx, &result)
            if err != nil {
                return SkillExecutionResult{Success: false, Error: err.Error()}, err
            }
            executedToolCalls = append(executedToolCalls, step.ToolName)

            _ = workflow.ExecuteActivity(ctx, "WorkspaceAppend",
                WorkspaceAppendInput{
                    WorkflowID: workflowID,
                    Topic:       step.WorkspaceRef,
                    Entry: map[string]interface{}{
                        "type":       "mcp_result",
                        "tool_name":  result.ToolName,
                        "success":    result.Success,
                        "duration_ms": result.DurationMs,
                    },
                },
            )

        case "sandbox":
            var result RunWASIActivityResult
            err := workflow.ExecuteActivity(ctx, "RunWASIActivity",
                RunWASIActivityInput{
                    WorkflowID: workflowID,
                    AgentID:    input.AgentID,
                    Language:   step.Arguments["language"].(string),
                    Code:       step.Arguments["code"].(string),
                    Policy: SandboxPolicy{
                        MaxCPUSeconds:  10,
                        MaxWallSeconds: 30,
                        MaxMemoryMB:    128,
                        AllowNetwork:   false,
                    },
                },
            ).Get(ctx, &result)
            if err != nil {
                return SkillExecutionResult{Success: false, Error: err.Error()}, err
            }
            executedToolCalls = append(executedToolCalls, step.ToolName)

            _ = workflow.ExecuteActivity(ctx, "WorkspaceAppend",
                WorkspaceAppendInput{
                    WorkflowID: workflowID,
                    Topic:       step.WorkspaceRef,
                    Entry: map[string]interface{}{
                        "type":      "sandbox_result",
                        "exit_code": result.ExitCode,
                        "stdout":    result.Stdout,
                    },
                },
            )

        case "sub_skill":
            // 递归调用 sub-skill（检查深度限制）
            if input.CallDepth+1 >= planResult.Plan.Steps[0].Arguments["max_depth"].(int) {
                return SkillExecutionResult{Success: false, Error: "max skill call depth exceeded"}, nil
            }
            var subResult SkillExecutionResult
            err := workflow.ExecuteActivity(ctx, "SkillExecutorWorkflow",
                SkillExecutionInput{
                    SkillID:    step.SubSkillID,
                    Arguments:  input.Arguments,
                    AgentID:    input.AgentID,
                    WorkflowID: workflowID,
                    CallDepth:  input.CallDepth + 1,
                },
            ).Get(ctx, &subResult)
            if err != nil {
                return SkillExecutionResult{Success: false, Error: err.Error()}, err
            }
            executedToolCalls = append(executedToolCalls, step.ToolName)
        }
    }

    // Step 3: SkillFinalizeActivity - 汇总结果
    var finalizeResult SkillFinalizeResult
    _ = workflow.ExecuteActivity(ctx, "SkillFinalizeActivity",
        SkillFinalizeInput{
            WorkflowID:  workflowID,
            SkillID:     input.SkillID,
            WorkspaceTopic: workspaceTopic,
        },
    ).Get(ctx, &finalizeResult)

    return SkillExecutionResult{
        Success:        true,
        Output:         finalizeResult.Output,
        DurationMs:     finalizeResult.DurationMs,
        ToolCalls:      executedToolCalls,
        WorkspaceTopic: workspaceTopic,
    }, nil
}

// SkillFinalizeActivity - 从 Workspace refs 读取结果摘要，汇总 SkillExecutionResult
type SkillFinalizeInput struct {
    WorkflowID     string `json:"workflow_id"`
    SkillID        string `json:"skill_id"`
    WorkspaceTopic string `json:"workspace_topic"`
}

type SkillFinalizeResult struct {
    Output     map[string]interface{} `json:"output"`
    DurationMs int64                  `json:"duration_ms"`
}

// GetSkillActivity - 获取 Skill manifest
type GetSkillInput struct {
    SkillID string `json:"skill_id"`
}

// ListSkillsActivity - 列出可用的 Skills
type ListSkillsInput struct {
    Filter map[string]interface{} `json:"filter,omitempty"`
}

// AuditSkillCallActivity - 审计 Skill 调用
type AuditSkillCallInput struct {
    SkillID    string `json:"skill_id"`
    SkillName  string `json:"skill_name"`
    AgentID    string `json:"agent_id"`
    WorkflowID string `json:"workflow_id"`
    Success    bool   `json:"success"`
    DurationMs int64  `json:"duration_ms"`
    Error      string `json:"error,omitempty"`
}
```

#### 3.22 API 设计

```bash
# 注册 Skill manifest
POST /api/v1/skills
Content-Type: application/json

Request:
{
    "name": "research_summary",
    "description": "Research a topic and generate a summary with citations",
    "version": "1.0.0",
    "input_schema": {
        "type": "object",
        "properties": {
            "query": {"type": "string"}
        },
        "required": ["query"]
    },
    "output_schema": {
        "type": "object",
        "properties": {
            "summary": {"type": "string"},
            "sources": {"type": "array"}
        }
    },
    "required_tools": ["vector_search", "mcp:github:get_issue"],
    "permissions": ["document:read", "rag:query"],
    "timeout_seconds": 120
}

Response:
{
    "skill_id": "skill-xxx",
    "name": "research_summary",
    "status": "registered"
}

# 列出 Skills
GET /api/v1/skills

# 获取 Skill manifest
GET /api/v1/skills/{skill_id}

# 执行 Skill
POST /api/v1/skills/{skill_id}/execute
Content-Type: application/json

Request:
{
    "arguments": {"query": "产品安全特性"},
    "agent_id": "worker-1",
    "workflow_id": "wf-xxx"
}

Response:
{
    "success": true,
    "workspace_topic": "skill:research_summary:results:xxx",
    "duration_ms": 1523
}
```

#### 3.23 与 ReAct 的衔接

```go
// ReAct action 使用 Skill
type ReActToolAction struct {
    ActionType string                 `json:"action_type"` // "skill"
    ToolName   string                 `json:"tool_name"`   // skill name
    Args       map[string]interface{} `json:"args"`
}

// Skill result 作为 observation
observations = append(observations, Observation{
    Type:    "skill",
    Content: fmt.Sprintf("Executed skill %s, workspace_topic: %s", action.ToolName, result.WorkspaceTopic),
})
```

#### 3.25 成功路径（方案 A：Workflow 编排）

```
ReAct / Lead 调用 Skill
  → SkillExecutorWorkflow
    → PlanSkillActivity（读取 manifest、校验权限、检测 cycle、返回 SkillPlan）
    → Workflow 根据 SkillPlan 调用原子 Activities（RAGSearch / CallMCPTool / RunWASI / LLMActivity）
    → 每个步骤结果写入 Workspace
    → SkillFinalizeActivity（汇总结果）
  → AuditSkillCallActivity（PostgreSQL）
```

#### 3.26 失败路径

```
Skill 不存在 / cycle 检测失败 → PlanSkillActivity 返回 error
权限不足 → PlanSkillActivity 返回 error
Input schema 验证失败 → PlanSkillActivity 返回 error
Sub-skill 递归超深 → 返回 error
单个步骤失败 → Workflow 返回 partial result
Timeout → Workflow 超时终止，返回 partial result
```

#### 3.27 验收标准

| 验收项 | 通过条件 |
|--------|----------|
| 注册 manifest | 可以注册 skill manifest |
| 查询 skill | 可以获取/列出 skill |
| 执行 skill | 可以执行 skill |
| 工具组合 | skill 能调用 RAG / MCP / Sandbox / LLM 中至少一种 |
| Workspace 写入 | skill 执行结果能写入 Workspace |
| 结构化错误 | skill 失败能返回结构化错误 |
| Audit | skill_calls 有审计记录 |

---

### Phase 6D Slice 20：Hooks Event System

#### 3.28 核心架构澄清

Hooks Event System 提供事件驱动的扩展点，允许在 Agent 执行过程中插入自定义逻辑（权限检查、监控、审计等）。

**关键约束**：
- Workflow 只产生 hook event，不直接执行外部 hook IO
- Hook handler 如果需要外部 IO，必须通过 Activity 执行
- Hook 默认不阻塞主流程
- **blocking hook 只允许用于权限类 / policy 类检查**
- Hook 失败默认写 warning / audit，不破坏主流程

#### 3.29 HookPoint 定义

| HookPoint | 触发时机 | Blocking | 说明 |
|-----------|---------|----------|------|
| before_tool_call | tool call 执行前 | 可选 | 可用于权限检查 |
| after_tool_call | tool call 执行后 | 否 | 可用于日志/监控 |
| before_llm_call | LLM call 执行前 | 可选 | 可用于 prompt 检查 |
| after_llm_call | LLM call 执行后 | 否 | 可用于 response 记录 |
| on_agent_step | Agent 步骤完成 | 否 | 可用于进度跟踪 |
| on_workspace_append | Workspace 追加后 | 否 | 可用于触发后续处理 |
| on_handoff | Agent 控制权转移后 | 否 | 可用于通知 |
| on_error | 错误发生时 | 可选 | 可用于恢复逻辑 |

#### 3.30 数据结构

```go
// HookEvent - Hook 事件
type HookEvent struct {
    EventID     string                 `json:"event_id"`
    HookPoint   string                 `json:"hook_point"`    // HookPoint 枚举
    AgentID     string                 `json:"agent_id"`
    WorkflowID  string                 `json:"workflow_id"`
    Timestamp   time.Time              `json:"timestamp"`
    Payload     map[string]interface{} `json:"payload"`        // 事件相关数据
}

// HookResult - Hook 处理结果
type HookResult struct {
    EventID     string `json:"event_id"`
    HookPoint   string `json:"hook_point"`
    Success     bool   `json:"success"`
    Blocking    bool   `json:"blocking"`    // 是否阻塞主流程
    Error       string `json:"error,omitempty"`
    Continue    bool   `json:"continue"`   // blocking 为 true 时，是否继续主流程
}

// HookRegistration - Hook 注册
type HookRegistration struct {
    ID          string   `json:"id"`
    Name        string   `json:"name"`
    HookPoint   string   `json:"hook_point"`
    HandlerURL  string   `json:"handler_url"`  // HTTP endpoint 或 internal handler name
    Blocking    bool     `json:"blocking"`
    Filter      map[string]interface{} `json:"filter,omitempty"` // 过滤条件
    Enabled     bool     `json:"enabled"`
    CreatedAt   time.Time `json:"created_at"`
}

// HookAuditLog - Hook 审计日志
type HookAuditLog struct {
    ID          string    `json:"id"`
    EventID     string    `json:"event_id"`
    HookPoint   string    `json:"hook_point"`
    HandlerName string    `json:"handler_name"`
    Success     bool      `json:"success"`
    DurationMs  int64     `json:"duration_ms"`
    Error       string    `json:"error,omitempty"`
    CreatedAt   time.Time `json:"created_at"`
}
```

#### 3.31 Activity 设计

```go
// EmitHookEventActivity - 发出 hook event
type EmitHookEventInput struct {
    HookPoint  string                 `json:"hook_point"`
    AgentID    string                 `json:"agent_id"`
    WorkflowID string                 `json:"workflow_id"`
    Payload    map[string]interface{} `json:"payload"`
}

// ExecuteHookHandlersActivity - 执行 hook handlers
type ExecuteHookHandlersInput struct {
    Event HookEvent `json:"event"`
}

type ExecuteHookHandlersResult struct {
    EventID    string       `json:"event_id"`
    Results    []HookResult `json:"results"`
    Blocked    bool         `json:"blocked"`     // 是否有 blocking hook 阻止
    CanContinue bool        `json:"can_continue"` // blocking hook 是否允许继续
}

// RegisterHookActivity - 注册 hook
type RegisterHookInput struct {
    Name       string                 `json:"name"`
    HookPoint  string                 `json:"hook_point"`
    HandlerURL string                 `json:"handler_url"`
    Blocking   bool                   `json:"blocking"`
    Filter     map[string]interface{} `json:"filter,omitempty"`
}

// AuditHookExecutionActivity - 审计 hook 执行
type AuditHookExecutionInput struct {
    EventID     string `json:"event_id"`
    HookPoint   string `json:"hook_point"`
    HandlerName string `json:"handler_name"`
    Success     bool   `json:"success"`
    DurationMs  int64  `json:"duration_ms"`
    Error       string `json:"error,omitempty"`
}
```

#### 3.32 ReAct Loop 中的 Hook 调用

```go
// ReAct loop 中调用 hook
func ReActLoop(ctx workflow.Context, input ReActInput) (ReActResult, error) {
    workflowID := workflow.GetInfo(ctx).WorkflowExecution.ID
    var observations []Observation

    for iteration := 0; iteration < input.MaxIterations; iteration++ {
        // thought

        // action
        if action.ActionType == "tool_call" {
            // before_tool_call hook
            var hookResult HookResult
            err := workflow.ExecuteActivity(ctx, "EmitHookEventActivity",
                EmitHookEventInput{
                    HookPoint:  "before_tool_call",
                    AgentID:    input.AgentID,
                    WorkflowID: workflowID,
                    Payload: map[string]interface{}{
                        "tool_name": action.ToolName,
                        "arguments": action.Args,
                    },
                },
            ).Get(ctx, &hookResult)

            // 注意：如果 hookResult.Blocked && !hookResult.CanContinue，必须拒绝，不能 silent continue
            if err == nil && hookResult.Blocked && !hookResult.CanContinue {
                // 记录 observation：工具被 hook 拒绝
                observations = append(observations, Observation{
                    Type:    "hook_rejected",
                    Content: fmt.Sprintf("Tool %s rejected by hook: %s", action.ToolName, hookResult.Error),
                })
                // 返回结构化拒绝，不静默吞掉未授权工具调用
                return ReActResult{
                    Success: false,
                    Error:   fmt.Sprintf("tool %s permission denied: %s", action.ToolName, hookResult.Error),
                }, nil
            }

            // 执行 tool...
            // after_tool_call hook
            var afterHookResult HookResult
            _ = workflow.ExecuteActivity(ctx, "EmitHookEventActivity",
                EmitHookEventInput{
                    HookPoint:  "after_tool_call",
                    AgentID:    input.AgentID,
                    WorkflowID: workflowID,
                    Payload: map[string]interface{}{
                        "tool_name":   action.ToolName,
                        "success":     result.Success,
                        "duration_ms": result.DurationMs,
                    },
                },
            ).Get(ctx, &afterHookResult)
        }
    }
}
```

**Blocking hook 语义澄清**：
- non-blocking hook 失败：写 warning，继续主流程
- blocking hook 拒绝：返回 permission_denied 结构化结果，Workflow 返回 error，不继续执行 tool
- 禁止用 `continue` 静默吞掉被拒绝的工具调用（安全性风险）

#### 3.33 API 设计

```bash
# 注册 hook
POST /api/v1/hooks
Content-Type: application/json

Request:
{
    "name": "tool_permission_check",
    "hook_point": "before_tool_call",
    "handler_url": "internal:permission_check",
    "blocking": true,
    "filter": {
        "tool_types": ["mcp", "sandbox"]
    }
}

# 列出 hooks
GET /api/v1/hooks

# 获取 hook
GET /api/v1/hooks/{hook_id}

# 删除 hook
DELETE /api/v1/hooks/{hook_id}
```

#### 3.35 成功路径

```
Tool call / LLM call / Agent step / Workspace append / Handoff / Error
  → EmitHookEventActivity
  → 查询注册的 handlers
  → for each handler:
      if blocking && isPermissionCheck:
          同步执行 → 返回 continue/block
      else:
          异步执行（goroutine）
  → if blocked && !canContinue:
      返回 error，阻止主流程
  → else:
      继续主流程
  → AuditHookExecutionActivity
```

#### 3.36 失败路径

```
Hook handler 执行失败 → 写 warning，返回 HookResult{Success: false}
Hook 超时 → 写 warning，继续主流程（非 blocking）
Blocking hook 拒绝 → 返回 error，阻止主流程
```

#### 3.37 验收标准

| 验收项 | 通过条件 |
|--------|----------|
| before_tool_call | tool call 前能触发 hook |
| after_tool_call | tool call 后能触发 hook |
| before_llm_call | LLM call 前能触发 hook |
| after_llm_call | LLM call 后能触发 hook |
| on_agent_step | Agent 步骤完成能触发 hook |
| on_workspace_append | Workspace 追加后能触发 hook |
| on_handoff | Handoff 后能触发 hook |
| on_error | 错误发生能触发 hook |
| Non-blocking | 失败默认不影响主流程 |
| Blocking | blocking hook 可以拒绝未授权 tool call |
| Audit | hooks_audit 有记录 |

---

### Phase 6E Slice 21：RAG / Qdrant Long-term Memory

#### 3.38 核心架构澄清

RAG / Qdrant Long-term Memory 提供文档接入、chunking、embedding（mock/real 可选）、Qdrant upsert/search、rerank、context packing 能力。

**关键约束**：
- **Workflow 不传递 chunk content / embedding vector，只传 document_id / chunk_ids / job_id / retrieval_id / summary / metadata**
- embedding vectors 只在 Activity 内部流转
- GenerateEmbeddingResult 不返回 `[][]float32` 给 Workflow
- 大文本通过 Workspace 或 external storage ref 传递，Workflow history 只保存 retrieval_id、chunk_count、summary、metadata

#### 3.39 数据结构

```go
// Document - 文档元数据
type Document struct {
    ID          string    `json:"id"`
    UserID      string    `json:"user_id"`
    TenantID    string    `json:"tenant_id"`
    Title       string    `json:"title"`
    Source      string    `json:"source"`
    ContentHash string    `json:"content_hash"`  // SHA256，用于去重
    Status      string    `json:"status"`        // "pending" | "processing" | "completed" | "failed"
    ChunkCount  int       `json:"chunk_count"`
    Metadata    map[string]interface{} `json:"metadata"`
    CreatedAt   time.Time `json:"created_at"`
    UpdatedAt   time.Time `json:"updated_at"`
}

// DocumentChunk - 文档切片（chunk content 不进入 Workflow history）
type DocumentChunk struct {
    ID          string                 `json:"id"`
    DocumentID  string                 `json:"document_id"`
    ChunkIndex  int                    `json:"chunk_index"`
    ContentHash string                 `json:"content_hash"`  // SHA256
    TokenCount  int                    `json:"token_count"`
    Metadata    map[string]interface{} `json:"metadata"`
    // 注意：Content 字段不进入 Workflow，仅在 Activity 内部使用
}

// EmbeddingJob - Embedding 生成任务
type EmbeddingJob struct {
    ID          string    `json:"id"`
    DocumentID  string    `json:"document_id"`
    ChunkIDs    []string  `json:"chunk_ids"`
    Model       string    `json:"model"`
    Status      string    `json:"status"`
    CreatedAt   time.Time `json:"created_at"`
    CompletedAt time.Time `json:"completed_at,omitempty"`
}

// QdrantPoint - Qdrant 向量点（vector 在 Activity 内部，不进 Workflow）
type QdrantPoint struct {
    ID      string                 `json:"id"`       // chunk_id
    Vector  []float32              `json:"-"`       // 不序列化到 Workflow
    Payload map[string]interface{} `json:"payload"` // document_id, chunk_index, metadata
}

// RetrievalQuery - 检索查询
type RetrievalQuery struct {
    Query       string                 `json:"query"`
    Collection  string                 `json:"collection"`
    TopK        int                    `json:"top_k"`
    Filter      map[string]interface{} `json:"filter"`
    UserID      string                 `json:"user_id"`
    TenantID    string                 `json:"tenant_id"`
}

// RetrievedChunk - 检索到的切片
type RetrievedChunk struct {
    ChunkID    string                 `json:"chunk_id"`
    DocumentID string                 `json:"document_id"`
    Score      float64                `json:"score"`
    Rank       int                    `json:"rank"`
    Metadata   map[string]interface{} `json:"metadata"`
    // 注意：Content 不进 Workflow，通过 WorkspaceRef 传递
}

// RetrievalResult - 检索结果
type RetrievalResult struct {
    QueryID     string            `json:"query_id"`
    ChunkIDs    []string         `json:"chunk_ids"`    // Workflow 只保存 chunk_ids
    TotalFound  int               `json:"total_found"`
    SearchTimeMs int64            `json:"search_time_ms"`
    Summary     string            `json:"summary"`      // 摘要，不传完整 content
    WorkspaceRef string           `json:"workspace_ref"` // content 的 Workspace topic
}
```

#### 3.40 Activity 设计（避免大 payload）

```go
// CreateDocumentActivity - 创建文档记录
// 注意：Gateway / UploadActivity 负责接收原文并写入 DB 或 object storage
// Workflow 只传 document_id / content_ref / content_hash，不传原始 content
type CreateDocumentInput struct {
    UserID      string                 `json:"user_id"`
    TenantID    string                 `json:"tenant_id"`
    Title       string                 `json:"title"`
    Source      string                 `json:"source"`
    ContentRef  string                 `json:"content_ref"`  // DB record ID 或 object storage key，Workflow 只传 ref
    ContentHash string                 `json:"content_hash"`  // SHA256，用于去重
    Metadata    map[string]interface{} `json:"metadata"`
}

// ChunkDocumentActivity - 文档切分（content 从 DB/storage 读取，不从 input 传递）
type ChunkDocumentInput struct {
    DocumentID string `json:"document_id"`
    ContentRef string `json:"content_ref"` // DB record ID 或 object storage key，Activity 据此读取原文
    ChunkSize  int    `json:"chunk_size"`
    Overlap    int    `json:"overlap"`
}

type ChunkDocumentResult struct {
    ChunkIDs    []string `json:"chunk_ids"`    // Workflow 只接收 chunk IDs
    TokenCount  int      `json:"token_count"`
    Metadata    map[int]map[string]interface{} `json:"metadata"` // chunk_index -> metadata
}

// GenerateEmbeddingActivity - 生成 embedding（vector 不进 Workflow）
type GenerateEmbeddingInput struct {
    DocumentID string   `json:"document_id"`
    ChunkIDs   []string `json:"chunk_ids"`
    Model      string   `json:"model"`
}

type GenerateEmbeddingResult struct {
    ChunkIDs     []string `json:"chunk_ids"`   // Workflow 只接收 chunk IDs
    Model        string   `json:"model"`
    TokenCount   int      `json:"token_count"`
    EmbeddingIDs []string `json:"embedding_ids"` // Qdrant point IDs
    // 注意：不返回 [][]float32
}

// EmbedQueryActivity - query embedding
type EmbedQueryInput struct {
    Query  string `json:"query"`
    Model  string `json:"model"`
}

type EmbedQueryResult struct {
    Vector     []float32 `json:"-"`  // 不进 Workflow，仅在 SearchQdrantActivity 内使用
    TokenCount int       `json:"token_count"`
    Model      string    `json:"model"`
}

// SearchQdrantActivity - Qdrant 向量搜索
type SearchQdrantInput struct {
    Collection  string    `json:"collection"`
    QueryVector []float32 `json:"-"` // 不从 input 传递，从 EmbedQueryResult 获取
    TopK        int       `json:"top_k"`
    Filter      map[string]interface{} `json:"filter"`
}

type SearchQdrantResult struct {
    QueryID      string            `json:"query_id"`
    ChunkIDs     []string          `json:"chunk_ids"`
    Scores       []float64         `json:"scores"`
    TotalFound   int               `json:"total_found"`
    SearchTimeMs int64             `json:"search_time_ms"`
    WorkspaceRef string           `json:"workspace_ref"` // content 写入的 topic
}

// SaveRetrievalResultActivity - 保存检索结果到 Workspace
type SaveRetrievalResultInput struct {
    WorkflowID  string            `json:"workflow_id"`
    QueryID     string            `json:"query_id"`
    Chunks      []RetrievedChunk  `json:"chunks"` // content 在 Activity 内写入 Workspace
    WorkspaceRef string           `json:"workspace_ref"`
    Timestamp   time.Time        `json:"timestamp"`
}

// PackContextActivity - 上下文打包
type PackContextInput struct {
    ChunkIDs      []string `json:"chunk_ids"`
    Query         string   `json:"query"`
    MaxTokens     int      `json:"max_tokens"`
    WorkspaceRef  string   `json:"workspace_ref"` // 从 Workspace 读取 content
}

type PackContextResult struct {
    Content     string     `json:"content"`
    Citations   []Citation `json:"citations"`
    TokenCount  int        `json:"token_count"`
    ChunkCount  int        `json:"chunk_count"`
    Overflow    bool       `json:"overflow"`
}
```

#### 3.41 API 设计

```bash
# 上传文档
POST /api/v1/rag/documents
Content-Type: application/json

Request:
{
    "title": "产品手册 v2.1",
    "source": "upload",
    "content": "这里是文档内容...",
    "metadata": {"department": "engineering"}
}

Response:
{
    "document_id": "doc-xxx",
    "content_hash": "sha256:abc...",
    "status": "pending"
}

# 获取文档状态
GET /api/v1/rag/documents/{document_id}

# 触发摄取
POST /api/v1/rag/ingest
Content-Type: application/json

Request:
{
    "document_id": "doc-xxx",
    "chunk_size": 512,
    "collection": "default"
}

# 查询
POST /api/v1/rag/query
Content-Type: application/json

Request:
{
    "query": "产品安全特性",
    "collection": "default",
    "top_k": 10
}

Response:
{
    "query_id": "req-xxx",
    "chunk_ids": ["chunk-1", "chunk-2"],
    "total_found": 42,
    "workspace_ref": "rag:retrieval:req-xxx:content"
}
```

#### 3.42 Workflow 中的 RAG 调用

```go
// Workflow 只传递 ID / metadata，不传 content / vector
func RAGRetrievalWorkflow(ctx workflow.Context, input RAGInput) (RAGResult, error) {
    workflowID := workflow.GetInfo(ctx).WorkflowExecution.ID

    // 1. Embed query
    var embedResult EmbedQueryResult
    err := workflow.ExecuteActivity(ctx, "EmbedQueryActivity",
        EmbedQueryInput{Query: input.Query, Model: "default"},
    ).Get(ctx, &embedResult)
    if err != nil {
        return RAGResult{}, fmt.Errorf("embed query failed: %w", err)
    }

    // 2. Search Qdrant
    var searchResult SearchQdrantResult
    err = workflow.ExecuteActivity(ctx, "SearchQdrantActivity",
        SearchQdrantInput{
            Collection: input.Collection,
            TopK:        input.TopK,
            Filter:      input.Filter,
        },
    ).Get(ctx, &searchResult)
    if err != nil {
        return RAGResult{}, fmt.Errorf("qdrant search failed: %w", err)
    }

    // 3. Save to Workspace（content 在 Activity 内写入）
    err = workflow.ExecuteActivity(ctx, "SaveRetrievalResultActivity",
        SaveRetrievalResultInput{
            WorkflowID:   workflowID,
            QueryID:      searchResult.QueryID,
            WorkspaceRef: searchResult.WorkspaceRef,
            Timestamp:    workflow.Now(ctx),
        },
    ).Get(ctx, nil)
    if err != nil {
        // non-fatal，继续执行
    }

    // 4. Pack context（从 Workspace 读取 content，严格按 MaxTokens 裁剪）
    // 注意：PackContextResult.Content 是经过 MaxTokens 裁剪后的短上下文
    // 大段 retrieved chunks 必须通过 WorkspaceRef 或 external storage ref 传递
    var packResult PackContextResult
    err = workflow.ExecuteActivity(ctx, "PackContextActivity",
        PackContextInput{
            ChunkIDs:     searchResult.ChunkIDs,
            Query:        input.Query,
            MaxTokens:    input.MaxTokens,
            WorkspaceRef: searchResult.WorkspaceRef,
        },
    ).Get(ctx, &packResult)
    if err != nil {
        return RAGResult{}, fmt.Errorf("pack context failed: %w", err)
    }

    // 5. 返回给 ReAct（只有 metadata，不传完整 content）
    return RAGResult{
        QueryID:      searchResult.QueryID,
        ChunkIDs:     searchResult.ChunkIDs,
        TotalFound:   searchResult.TotalFound,
        Context:      packResult.Content,    // 裁剪后的短上下文
        Citations:     packResult.Citations,
        WorkspaceRef:  searchResult.WorkspaceRef, // 大段 content 的引用
    }, nil
}
```

#### 3.43 成功路径

```
用户上传文档
  → CreateDocumentActivity
  → ChunkDocumentActivity → 返回 chunk_ids（不是 chunks）
  → GenerateEmbeddingActivity → embedding 直接写 Qdrant，返回 embedding_ids
  → UpsertQdrantActivity
  → SaveDocumentMetadataActivity
  → 文档状态变为 completed
```

#### 3.44 失败路径

```
CreateDocument 失败 → 返回 error
ChunkDocument 失败 → 标记 failed
GenerateEmbedding 失败 → Activity 重试，仍失败标记 failed
UpsertQdrant 失败 → Activity 重试
```

#### 3.45 验收标准

| 验收项 | 通过条件 |
|--------|----------|
| content_hash 去重 | 相同内容重复上传返回已有 document_id |
| chunk_id 稳定 | 相同 content + chunk_index 生成相同 chunk_id |
| Qdrant upsert 幂等 | 重复 upsert 不产生重复 points |
| ID-only 传递 | Workflow 只接收 chunk_ids，不接收 content/embedding |
| Embedding 缓存 | 相同 content 命中 L1/L2 embedding 缓存 |
| SSE 事件 | chunked/embedded/stored/completed/failed 事件正确推送 |
| Workspace 写入 | retrieval content 写入 Workspace topic |
| Mock embedding | 默认 mock embedding，不依赖真实 LLM |

---

### Phase 6F Slice 22：Research-Synthesis v1

#### 3.46 核心架构澄清

Research-Synthesis v1 提供多子问题分解、RAG retrieval、evidence Workspace 写入、LLM synthesis 能力。

**非目标**：
- 不做 Debate
- 不做 Tree-of-Thoughts
- 不做复杂 citation graph
- 不做证据冲突仲裁
- 不做 Research-Synthesis v2

#### 3.47 数据结构

```go
// ResearchQuery - 研究查询
type ResearchQuery struct {
    ID          string    `json:"id"`
    Query       string    `json:"query"`
    AgentID     string    `json:"agent_id"`
    WorkflowID  string    `json:"workflow_id"`
    Status      string    `json:"status"`    // "pending" | "decomposing" | "retrieving" | "synthesizing" | "completed" | "failed"
    Subqueries  []ResearchSubquery `json:"subqueries,omitempty"`
    CreatedAt   time.Time `json:"created_at"`
    CompletedAt time.Time `json:"completed_at,omitempty"`
}

// ResearchSubquery - 子问题
type ResearchSubquery struct {
    ID           string   `json:"id"`
    ParentID     string   `json:"parent_id"`
    Text         string   `json:"text"`
    Status       string   `json:"status"`    // "pending" | "retrieving" | "retrieved" | "failed"
    RetrievalID  string   `json:"retrieval_id,omitempty"`
    EvidenceRef  string   `json:"evidence_ref"`  // Workspace topic
}

// ResearchEvidence - 研究证据
type ResearchEvidence struct {
    SubqueryID  string            `json:"subquery_id"`
    ChunkIDs    []string          `json:"chunk_ids"`
    Summary     string            `json:"summary"`
    Score       float64           `json:"score"`
    Metadata    map[string]interface{} `json:"metadata"`
}

// ResearchSynthesisResult - 综合结果
type ResearchSynthesisResult struct {
    QueryID         string               `json:"query_id"`
    Answer          string               `json:"answer"`
    Evidence        []EvidenceReference  `json:"evidence"`     // evidence metadata
    SubqueryAnswers []SubqueryAnswer     `json:"subquery_answers"`
    TokenCount      int                  `json:"token_count"`
}
```

#### 3.48 Activity 设计

```go
// DecomposeResearchQueryActivity - 分解研究查询
type DecomposeResearchQueryInput struct {
    Query      string `json:"query"`
    AgentID    string `json:"agent_id"`
    WorkflowID string `json:"workflow_id"`
}

type DecomposeResearchQueryResult struct {
    QueryID    string               `json:"query_id"`
    Subqueries []ResearchSubquery   `json:"subqueries"`
}

// RetrieveEvidenceActivity - 检索子问题证据
type RetrieveEvidenceInput struct {
    Subquery   ResearchSubquery `json:"subquery"`
    Collection string           `json:"collection"`
    TopK       int              `json:"top_k"`
}

type RetrieveEvidenceResult struct {
    SubqueryID    string             `json:"subquery_id"`
    RetrievalID   string             `json:"retrieval_id"`
    EvidenceRef   string             `json:"evidence_ref"` // Workspace topic
    ChunkIDs      []string           `json:"chunk_ids"`
    Summary       string             `json:"summary"`
}

// SynthesizeResearchResultActivity - LLM 综合
type SynthesizeResearchResultInput struct {
    QueryID      string                `json:"query_id"`
    Query        string                `json:"query"`
    Subqueries   []ResearchSubquery    `json:"subqueries"`
    EvidenceRefs []string              `json:"evidence_refs"` // Workspace topics
    Model        string                `json:"model"`
    MockLLM      bool                  `json:"mock_llm"`      // 默认 true
}

type SynthesizeResearchResultResult struct {
    Answer         string               `json:"answer"`
    Evidence       []EvidenceReference `json:"evidence"`
    SubqueryAnswers []SubqueryAnswer   `json:"subquery_answers"`
    TokenCount     int                 `json:"token_count"`
}
```

#### 3.49 Research-Synthesis v1 Workflow

```go
// ResearchSynthesisWorkflow - 研究综合 Workflow
func ResearchSynthesisWorkflow(ctx workflow.Context, input ResearchWorkflowInput) (ResearchResult, error) {
    workflowID := workflow.GetInfo(ctx).WorkflowExecution.ID

    // 1. 分解查询（失败直接返回 error）
    var decompResult DecomposeResearchQueryResult
    err := workflow.ExecuteActivity(ctx, "DecomposeResearchQueryActivity",
        DecomposeResearchQueryInput{
            Query:      input.Query,
            AgentID:    input.AgentID,
            WorkflowID: workflowID,
        },
    ).Get(ctx, &decompResult)
    if err != nil {
        return ResearchResult{}, fmt.Errorf("decompose failed: %w", err)
    }

    // 2. 并发检索每个子问题的证据（Temporal Future 模式）
    futures := make([]workflow.Future, len(decompResult.Subqueries))
    evidenceRefs := make([]string, len(decompResult.Subqueries))
    failedSubqueries := 0

    for i, sq := range decompResult.Subqueries {
        futures[i] = workflow.ExecuteActivity(ctx, "RetrieveEvidenceActivity",
            RetrieveEvidenceInput{
                Subquery:   sq,
                Collection: "default",
                TopK:       5,
            },
        )
    }

    // 3. 收集结果（不忽略错误，失败 subquery 标记 failed）
    for i, f := range futures {
        var evidenceResult RetrieveEvidenceResult
        err := f.Get(ctx, &evidenceResult)
        sq := decompResult.Subqueries[i]

        if err != nil {
            // 标记该 subquery failed，继续其他 subquery
            failedSubqueries++
            evidenceRefs[i] = ""
            _ = workflow.ExecuteActivity(ctx, "WorkspaceAppend",
                WorkspaceAppendInput{
                    WorkflowID: workflowID,
                    Topic:      "evidence",
                    Entry: map[string]interface{}{
                        "type":        "evidence_failed",
                        "subquery_id": sq.ID,
                        "error":       err.Error(),
                    },
                },
            )
            continue
        }

        evidenceRefs[i] = evidenceResult.EvidenceRef

        // 写入 Workspace（append-only evidence log）
        err = workflow.ExecuteActivity(ctx, "WorkspaceAppend",
            WorkspaceAppendInput{
                WorkflowID: workflowID,
                Topic:      "evidence",
                Entry: map[string]interface{}{
                    "type":        "evidence",
                    "subquery_id": sq.ID,
                    "chunk_ids":   evidenceResult.ChunkIDs,
                    "summary":     evidenceResult.Summary,
                },
            },
        ).Get(ctx, nil)
        if err != nil {
            // non-fatal，记录 warning，继续
        }
    }

    // 4. 读取 evidence from Workspace
    var evidence []WorkspaceEntry
    err = workflow.ExecuteActivity(ctx, "WorkspaceList",
        WorkspaceListInput{
            WorkflowID: workflowID,
            Topic:      "evidence",
            SinceSeq:   0,
            Limit:      1000,
        },
    ).Get(ctx, &evidence)
    if err != nil {
        return ResearchResult{}, fmt.Errorf("workspace list failed: %w", err)
    }

    // 5. LLM synthesis（可选 mock）
    var synthResult SynthesizeResearchResultResult
    synthOpts := workflow.ActivityOptions{
        StartToCloseTimeout: 120 * time.Second,
    }
    synthCtx := workflow.WithActivityOptions(ctx, synthOpts)
    err = workflow.ExecuteActivity(synthCtx, "SynthesizeResearchResultActivity",
        SynthesizeResearchResultInput{
            QueryID:      decompResult.QueryID,
            Query:        input.Query,
            Subqueries:   decompResult.Subqueries,
            EvidenceRefs: evidenceRefs,
            Model:        "gpt-4o",
            MockLLM:      input.MockLLM, // 默认 true
        },
    ).Get(ctx, &synthResult)
    if err != nil {
        // Synthesis 失败时返回 partial result + evidence metadata
        return ResearchResult{
            QueryID: decompResult.QueryID,
            Answer:  "",
            Evidence: buildPartialEvidence(decompResult.Subqueries, evidenceRefs, evidence),
            Error:    fmt.Sprintf("synthesis failed: %w", err),
        }, nil
    }

    // 6. 如果有 subquery 失败，部分成功返回
    if failedSubqueries > 0 {
        return ResearchResult{
            QueryID: decompResult.QueryID,
            Answer:  synthResult.Answer,
            Evidence: synthResult.Evidence,
            Partial: true,
            Error:   fmt.Sprintf("%d subqueries failed", failedSubqueries),
        }, nil
    }

    return ResearchResult{
        QueryID: decompResult.QueryID,
        Answer:  synthResult.Answer,
        Evidence: synthResult.Evidence,
    }, nil
}

#### 3.50 API 设计

```bash
# 发起研究查询
POST /api/v1/research
Content-Type: application/json

Request:
{
    "query": "产品安全特性有哪些？",
    "collection": "default",
    "mock_llm": true
}

Response:
{
    "query_id": "research-xxx",
    "status": "decomposing"
}

# 获取研究状态
GET /api/v1/research/{query_id}

Response:
{
    "query_id": "research-xxx",
    "status": "completed",
    "answer": "产品的安全特性包括...",
    "evidence": [
        {"subquery_id": "sq-1", "chunk_ids": ["chunk-1"], "score": 0.92}
    ]
}
```

#### 3.51 成功路径

```
ResearchWorkflow(query)
  → DecomposeResearchQueryActivity
  → for each subquery:
      RetrieveEvidenceActivity
      WorkspaceAppend(topic=evidence)
  → WorkspaceList(topic=evidence)
  → SynthesizeResearchResultActivity（mock 或 real LLM）
  → return answer + evidence metadata
```

#### 3.52 失败路径

```
Decompose 失败 → 返回 error
Subquery retrieval 失败 → 标记 subquery failed，继续其他
Synthesis 失败 → 返回 partial result
Empty evidence → Synthesis 返回空 answer 或 "no evidence found"
```

#### 3.53 验收标准

| 验收项 | 通过条件 |
|--------|----------|
| Query decomposition | 一个 research query 能拆成多个 subqueries |
| Evidence retrieval | 每个 subquery 能检索 evidence |
| Workspace append | evidence 写入 Workspace append-only log |
| Workspace read | WorkspaceList 能读回 evidence |
| Mock LLM | 默认 mock LLM synthesis，不进入默认 CI |
| Real LLM（可选） | REAL_LLM_TEST=1 时可跑真实 LLM synthesis |
| Final result 非空 | answer 非空 |
| Evidence metadata | final result 包含 evidence metadata |

---

## 四、Redis / Postgres / Qdrant 数据结构总结

### 4.1 Redis 数据结构（tenant_id / workflow_id 前缀）

```
# RAG retrieval tracking（tenant + task 前缀）
tenant:{tenant_id}:rag:{task_id}:retrievals   → List
tenant:{tenant_id}:rag:{query_id}:results     → List
tenant:{tenant_id}:rag:{query_id}:embedding   → String (mock/real embedding vector)

# Tool call tracking（workflow 前缀）
wf:{workflow_id}:tool:{task_id}:calls        → List

# Sandbox jobs（workflow 前缀）
wf:{workflow_id}:sandbox:{task_id}:jobs      → List

# MCP server tools cache（tenant 前缀）
tenant:{tenant_id}:mcp:{server_id}:tools     → Hash

# Skills cache（tenant 前缀）
tenant:{tenant_id}:skills:{skill_id}         → Hash

# Hooks registration（tenant 前缀）
tenant:{tenant_id}:hooks:{hook_point}        → List

# Embedding LRU cache（Phase 4 延续）
lru:embedding:{model}:{text_hash}           → String (TTL 1h)

# Tiktoken LRU cache（Phase 4 延续）
lru:tiktoken:{model}:{text_hash}            → String (TTL 1h)

# Workspace（Phase 5 延续，workflow 前缀）
wf:{workflow_id}:ws:seq                      → String
wf:{workflow_id}:ws:{topic}                 → List

# P2P 邮箱（Phase 5 延续）
wf:{workflow_id}:mbox:{agent_id}:seq        → String
wf:{workflow_id}:mbox:{agent_id}:msgs       → List

# Handoff state（Phase 5G 延续）
wf:{workflow_id}:handoff:{agent_id}         → Hash
```

### 4.2 Postgres Schema

```sql
-- MCP Servers (Phase 6A)
CREATE TABLE mcp_servers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    name VARCHAR(100) NOT NULL,
    command TEXT NOT NULL,
    args TEXT[],
    env TEXT[],  -- 脱敏后存储
    url TEXT,
    status VARCHAR(50) NOT NULL DEFAULT 'registered',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    UNIQUE(tenant_id, name)
);

-- MCP Tools (Phase 6A)
CREATE TABLE mcp_tools (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    server_id UUID NOT NULL REFERENCES mcp_servers(id) ON DELETE CASCADE,
    name VARCHAR(100) NOT NULL,
    description TEXT,
    input_schema JSONB NOT NULL,
    permissions TEXT[] DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    UNIQUE(server_id, name)
);

-- MCP Tool Calls (Phase 6A)
CREATE TABLE mcp_tool_calls (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    request_id VARCHAR(100) NOT NULL,
    tool_id UUID NOT NULL REFERENCES mcp_tools(id),
    server_id UUID NOT NULL REFERENCES mcp_servers(id),
    agent_id VARCHAR(100) NOT NULL,
    workflow_id VARCHAR(100) NOT NULL,
    arguments JSONB NOT NULL,
    result JSONB,
    status VARCHAR(50) NOT NULL,
    duration_ms INTEGER,
    error TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Sandbox Jobs (Phase 6B)
CREATE TABLE sandbox_jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workflow_id VARCHAR(100) NOT NULL,
    agent_id VARCHAR(100) NOT NULL,
    language VARCHAR(50) NOT NULL,
    code_hash VARCHAR(64) NOT NULL,
    policy JSONB NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'queued',
    exit_code INTEGER,
    stdout_size INTEGER,
    stderr_size INTEGER,
    duration_ms INTEGER,
    memory_used_mb INTEGER,
    oom_killed BOOLEAN DEFAULT FALSE,
    timed_out BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    started_at TIMESTAMP WITH TIME ZONE,
    completed_at TIMESTAMP WITH TIME ZONE
);

-- Sandbox Audit Logs (Phase 6B)
CREATE TABLE sandbox_audit_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id UUID NOT NULL REFERENCES sandbox_jobs(id),
    workflow_id VARCHAR(100) NOT NULL,
    agent_id VARCHAR(100) NOT NULL,
    language VARCHAR(50) NOT NULL,
    code_hash VARCHAR(64) NOT NULL,
    policy JSONB NOT NULL,
    exit_code INTEGER,
    duration_ms INTEGER,
    oom_killed BOOLEAN DEFAULT FALSE,
    timed_out BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Skills (Phase 6C)
CREATE TABLE skills (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    name VARCHAR(100) NOT NULL,
    description TEXT,
    version VARCHAR(20) NOT NULL,
    input_schema JSONB NOT NULL,
    output_schema JSONB NOT NULL,
    required_tools TEXT[] DEFAULT '{}',
    required_skills TEXT[] DEFAULT '{}',
    sandbox_policy JSONB,
    permissions TEXT[] DEFAULT '{}',
    timeout_seconds INTEGER DEFAULT 60,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    UNIQUE(tenant_id, name, version)
);

-- Skill Calls (Phase 6C)
CREATE TABLE skill_calls (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    skill_id UUID NOT NULL REFERENCES skills(id),
    skill_name VARCHAR(100) NOT NULL,
    agent_id VARCHAR(100) NOT NULL,
    workflow_id VARCHAR(100) NOT NULL,
    arguments JSONB NOT NULL,
    output JSONB,
    status VARCHAR(50) NOT NULL,
    duration_ms INTEGER,
    tool_calls TEXT[],
    workspace_topic VARCHAR(200),
    error TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Hooks Registration (Phase 6D)
CREATE TABLE hooks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    name VARCHAR(100) NOT NULL,
    hook_point VARCHAR(50) NOT NULL,
    handler_url VARCHAR(500) NOT NULL,
    blocking BOOLEAN DEFAULT FALSE,
    filter JSONB,
    enabled BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    UNIQUE(tenant_id, name)
);

-- Hook Audit Logs (Phase 6D)
CREATE TABLE hook_audit_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id VARCHAR(100) NOT NULL,
    hook_point VARCHAR(50) NOT NULL,
    handler_name VARCHAR(100) NOT NULL,
    success BOOLEAN NOT NULL,
    duration_ms INTEGER,
    error TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Documents (Phase 6E)
CREATE TABLE documents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,
    tenant_id UUID NOT NULL,
    title VARCHAR(500) NOT NULL,
    source VARCHAR(50) NOT NULL,
    content_hash VARCHAR(64) NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    chunk_count INTEGER DEFAULT 0,
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    UNIQUE(user_id, content_hash)
);

-- Document Chunks (Phase 6E)
CREATE TABLE document_chunks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id UUID NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    chunk_index INTEGER NOT NULL,
    content_hash VARCHAR(64) NOT NULL,
    token_count INTEGER NOT NULL,
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    UNIQUE(document_id, chunk_index)
);

-- Research Queries (Phase 6F)
CREATE TABLE research_queries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    query TEXT NOT NULL,
    agent_id VARCHAR(100) NOT NULL,
    workflow_id VARCHAR(100) NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    completed_at TIMESTAMP WITH TIME ZONE
);

-- Research Subqueries (Phase 6F)
CREATE TABLE research_subqueries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    parent_id UUID NOT NULL REFERENCES research_queries(id),
    text TEXT NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    retrieval_id VARCHAR(100),
    evidence_ref VARCHAR(200),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Research Synthesis Results (Phase 6F)
CREATE TABLE research_synthesis_results (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    query_id UUID NOT NULL REFERENCES research_queries(id),
    answer TEXT NOT NULL,
    evidence JSONB NOT NULL,
    subquery_answers JSONB,
    token_count INTEGER,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Audit Logs（横切，Phase 6F）
CREATE TABLE audit_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_type VARCHAR(100) NOT NULL,
    agent_id VARCHAR(100) NOT NULL,
    workflow_id VARCHAR(100),
    resource_type VARCHAR(100) NOT NULL,
    resource_id VARCHAR(100) NOT NULL,
    action VARCHAR(50) NOT NULL,
    success BOOLEAN NOT NULL DEFAULT TRUE,
    details JSONB DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);
```

### 4.3 Qdrant Collection Schema

```yaml
# Collection 命名
collections:
  default:
    vectors_size: 1536  # OpenAI text-embedding-3-small
  large:
    vectors_size: 3072  # OpenAI text-embedding-3-large

# Point 结构
point:
  id: string              # chunk_id (UUID)
  vector: float32[]       # embedding 向量（Activity 内部，不进 Workflow）
  payload:
    document_id: string
    chunk_index: int
    content_hash: string
    token_count: int
    metadata: object
```

---

## 五、验收标准总结

| Slice | 验收项 | 通过条件 |
|-------|--------|----------|
| 6A MCP | Server 注册 | MCP server 可注册（admin/lead/allowlist） |
| 6A MCP | env 脱敏 | 敏感环境变量不明文存储 |
| 6A MCP | Tool call | 可调用并返回结果 |
| 6A MCP | 权限控制 | 未授权 tool 被 blocking hook 拒绝 |
| 6A MCP | Audit | 所有调用记录（env 脱敏） |
| 6B Sandbox | 基本执行 | WASI / fixture code 可执行 |
| 6B Sandbox | 输出捕获 | stdout/stderr 正确捕获 |
| 6B Sandbox | Timeout | CPU/wall timeout 正确处理 |
| 6B Sandbox | Memory limit | OOM 正确处理 |
| 6B Sandbox | Network 禁止 | 默认禁止网络访问 |
| 6B Sandbox | Audit | 执行记录写入 PostgreSQL |
| 6C Skills | 注册 manifest | 可以注册 skill manifest |
| 6C Skills | 查询 skill | 可以获取/列出 skill |
| 6C Skills | 执行 skill | 可以执行 skill |
| 6C Skills | 工具组合 | skill 能调用 RAG/MCP/Sandbox/LLM 中至少一种 |
| 6C Skills | Workspace 写入 | skill 结果能写入 Workspace |
| 6C Skills | Audit | skill_calls 有审计记录 |
| 6D Hooks | before_tool_call | tool call 前能触发 hook |
| 6D Hooks | after_tool_call | tool call 后能触发 hook |
| 6D Hooks | on_error | 错误发生能触发 hook |
| 6D Hooks | Non-blocking | 失败默认不影响主流程 |
| 6D Hooks | Blocking | blocking hook 可以拒绝未授权 tool call |
| 6D Hooks | Audit | hooks_audit 有记录 |
| 6E RAG | content_hash 去重 | 相同内容重复上传返回已有 document_id |
| 6E RAG | chunk_id 稳定 | 相同 content + chunk_index 生成相同 chunk_id |
| 6E RAG | ID-only 传递 | Workflow 只接收 chunk_ids，不接收 content/embedding |
| 6E RAG | Mock embedding | 默认 mock embedding，不依赖真实 LLM |
| 6E RAG | Workspace 写入 | retrieval content 写入 Workspace topic |
| 6F Research | Query decomposition | research query 能拆成多个 subqueries |
| 6F Research | Evidence retrieval | 每个 subquery 能检索 evidence |
| 6F Research | Workspace append | evidence 写入 Workspace append-only log |
| 6F Research | Mock LLM | 默认 mock LLM，不进入默认 CI |

---

## 六、明确禁止事项

- ❌ **Workflow 内直接调用 Qdrant**（必须通过 Activity）
- ❌ **Workflow 内直接调用 MCP server**（必须通过 Activity）
- ❌ **Workflow 内直接执行 Sandbox / WASI**（必须通过 Activity）
- ❌ **Workflow 内直接访问 Redis / Postgres**（必须通过 Activity）
- ❌ **Workflow 内使用 `time.Now()`**（必须使用 `workflow.Now(ctx)`）
- ❌ **把 chunk content / embedding vector 塞进 Workflow history**（只传 ID / metadata）
- ❌ **把大型 stdout/stderr 塞进 Workflow history**（必须通过 Workspace 引用）
- ❌ **MCP server env 明文存储**（必须脱敏）
- ❌ **MCP server 未注册未授权就调用**（必须先注册 + 检查权限）
- ❌ **Sandbox 默认允许网络访问**（默认 `AllowNetwork: false`）
- ❌ **Sandbox 访问宿主机文件系统**（默认只能写 /workspace）
- ❌ **Worker Agent 越权调用工具**（必须检查 allowed_tools + blocking hook）
- ❌ **Worker Agent 私自注册 MCP server**（必须通过 admin/lead）
- ❌ **Activity 内调用 `workflow.ExecuteActivity`**（违反 Temporal deterministic 规则）
- ❌ **Activity 内调用 `workflow.Now(ctx)`**（违反 Temporal deterministic 规则）
- ❌ **Activity 内使用 goroutine 发起异步调用**（goroutine 生命周期超出 Activity scope）
- ❌ **Hook handler 阻塞主流程**（除 blocking permission/policy hook 外）
- ❌ **Hook 失败破坏主流程**（默认写 warning / audit，不破坏）
- ❌ **Blocking hook 被拒绝后 silent continue**（必须返回结构化 error）
- ❌ **Workspace 改回 Git-like 文件系统或锁机制**（Phase 5 Append-only 继续有效）
- ❌ **ReAct Loop 放回 Activity 内循环**（Phase 4 Workflow-level ReAct 继续有效）
- ❌ **Handoff 改写现有 P2P/Swarm 模式**（Phase 5G Handoff 独立扩展）
- ❌ **Skill 在 Activity 内递归调用自己或其他 Activity**（使用 Workflow 编排方案 A）

---

## 七、真实 LLM / Embedding / MCP 测试策略

### 7.1 测试分层

| 测试类型 | 环境变量 | 默认行为 | 进入 CI |
|---------|---------|---------|--------|
| Mock Regression | 默认 | 使用 mock/fixed data | 是 |
| Real Embedding | `REAL_EMBEDDING_TEST=1` | 调用真实 embedding service | 可选 |
| Real LLM | `REAL_LLM_TEST=1` | 调用真实 LLM | 可选 |
| Real MCP | `REAL_MCP_TEST=1` | 调用真实 MCP server | 可选 |

### 7.2 各 Slice 测试策略

**Phase 6A MCP**：
- 默认：mock MCP server
- `REAL_MCP_TEST=1`：可跑真实 MCP server smoke

**Phase 6B Sandbox**：
- 不需要真实 LLM
- 只测执行、安全、timeout、resource limit

**Phase 6C Skills**：
- 默认：mock skill 执行
- `REAL_LLM_TEST=1`：可跑包含 LLM 的 skill

**Phase 6D Hooks**：
- 默认：mock hook handler
- 不需要真实 LLM

**Phase 6E RAG**：
- 默认：fixture embedding + fixture Qdrant
- `REAL_EMBEDDING_TEST=1`：可跑真实 embedding + Qdrant
- **不要写"Vector Retrieval 必须有真实 LLM synthesis"**

**Phase 6F Research-Synthesis v1**：
- 默认：mock LLM synthesis
- `REAL_LLM_TEST=1`：可跑真实 LLM synthesis
- 无 API key 时 skip，不 fail

### 7.3 真实 LLM 测试默认行为

| 场景 | 行为 |
|------|------|
| 默认 | 只跑 mock 测试，不请求真实 LLM |
| `REAL_LLM_TEST=1` + 无 API key | skip 真实 LLM 测试，不 fail |
| `REAL_LLM_TEST=1` + 有 API key | 执行真实 LLM smoke |
| `REAL_EMBEDDING_TEST=1` + 无 API key | skip 真实 embedding 测试，不 fail |
| `REAL_MCP_TEST=1` + 无 MCP server | skip 真实 MCP 测试，不 fail |

---

## 八、参考实现

- MCP Protocol：参考 `modelcontextprotocol/specification`
- WASI Sandbox：`rust/agent-core/src/sandbox.rs`
- Skills System：参考 `docs/skills-system.md`（Shannon 原生）
- Hooks Event System：参考 `go/orchestrator/internal/hooks/`
- RAG / Qdrant：参考 `go/orchestrator/internal/rag/`
- Research-Synthesis：参考 `go/orchestrator/internal/workflows/patterns/research.go`

---

## 九、完整目录结构

Phase 6 完整 13 章结构：

| 章节 | 内容 |
|------|------|
| 一 | 项目定位 |
| 二 | 职责边界 |
| 三 | 阶段范围（6A-6F Slices 17-22） |
| 四 | Redis / Postgres / Qdrant 数据结构总结 |
| 五 | 验收标准总结 |
| 六 | 明确禁止事项 |
| 七 | 真实 LLM / Embedding / MCP 测试策略 |
| 八 | 参考实现 |
| 九 | 完整目录结构 |
| 十 | 成功路径 / 失败路径 |
| 十一 | 后续扩展路线图 |
| 十二 | Shannon 原生能力 vs Lite 实现对照 |
| 十三 | 开发者配置参考 |

---

## 十、成功路径 / 失败路径

### Slice 17：MCP Tool Runtime

**成功路径**：
```
Lead Agent 决定调用 MCP tool
  → before_tool_call hook（blocking permission check）
  → CallMCPToolActivity
  → MCP server 执行
  → after_tool_call hook
  → SaveMCPToolResultActivity（WorkspaceAppend）
  → AuditMCPToolCallActivity（PostgreSQL，脱敏）
```

**失败路径**：
```
Server 未注册 → 返回 error
Tool 未发现 → 返回 error
Permission denied（blocking hook） → 返回 error
Tool call 超时 → Activity 重试，仍超时返回 error
Result size 超限 → 截断 + 标记 overflow
```

### Slice 18：Sandbox / WASI Execution

**成功路径**：
```
ReAct action 请求 sandbox
  → before_tool_call hook
  → ValidateSandboxPolicyActivity
  → RunWASIActivity
  → SaveSandboxResultActivity（WorkspaceAppend）
  → after_tool_call hook
  → AuditSandboxExecutionActivity
```

**失败路径**：
```
Policy validation 失败 → 返回 error
Sandbox timeout → 返回 timeout error
OOM killed → 返回 error
Agent 无权限 → blocking hook 拒绝
```

### Slice 19：Skills System

**成功路径**：
```
ReAct / Lead 调用 Skill
  → before_tool_call hook
  → SkillExecutorWorkflow
    → PlanSkillActivity（读取 manifest、校验权限、生成 SkillPlan）
    → Workflow 按 SkillPlan 调用原子 Activities
    → SkillFinalizeActivity（汇总结果）
  → WorkspaceAppend（每个 tool result）
  → after_tool_call hook
  → AuditSkillCallActivity
```

**失败路径**：
```
Skill 不存在 → 返回 error
权限不足 → blocking hook 拒绝
Sub-skill 失败 → 返回 error
Timeout → 返回 partial result
```

### Slice 20：Hooks Event System

**成功路径**：
```
Tool call / LLM call / Agent step / Workspace append / Handoff / Error
  → EmitHookEventActivity
  → 查询注册的 handlers
  → 执行 handlers（blocking 或 non-blocking）
  → 写 audit log
```

**失败路径**：
```
Hook handler 失败 → 写 warning，不破坏主流程
Hook 超时 → 写 warning，继续主流程
Blocking hook 拒绝 → 返回 error，阻止主流程
```

### Slice 21：RAG / Qdrant Long-term Memory

**成功路径**：
```
上传文档
  → CreateDocumentActivity（content_hash 去重）
  → ChunkDocumentActivity → 返回 chunk_ids
  → GenerateEmbeddingActivity → embedding 直接写 Qdrant
  → UpsertQdrantActivity
  → SaveDocumentMetadataActivity
  → SSE 推送
```

**失败路径**：
```
CreateDocument 失败 → 返回 error
ChunkDocument 失败 → 标记 failed
GenerateEmbedding 失败 → Activity 重试
UpsertQdrant 失败 → Activity 重试
```

### Slice 22：Research-Synthesis v1

**成功路径**：
```
ResearchWorkflow(query)
  → DecomposeResearchQueryActivity
  → for each subquery 并发 retrieval
  → WorkspaceAppend(topic=evidence)
  → WorkspaceList(topic=evidence)
  → SynthesizeResearchResultActivity（mock 或 real LLM）
  → return answer + evidence metadata
```

**失败路径**：
```
Decompose 失败 → 返回 error
Subquery retrieval 失败 → 标记 failed，继续其他
Synthesis 失败 → 返回 partial result
Empty evidence → 返回 "no evidence found"
```

---

## 十一、后续扩展路线图

| Phase | 内容 | 关键能力 |
|-------|------|---------|
| Phase 6 | MCP / Sandbox / Skills / Hooks / RAG / Research-Synthesis v1 | Tool Runtime + Sandbox + Skills + Hooks + Memory |
| **Phase 7A** | **HITL / Approval / UI** | **人类审批、任务暂停恢复、Dashboard 操作** |
| Phase 7B | Reflection Production Mode | generate -> reflect -> revise，多轮 reflection |
| Phase 7C | Tree-of-Thoughts | 多候选路径、评分、剪枝、token budget 控制 |
| Phase 7D | Debate Mode | Pro Agent、Con Agent、Judge Agent、多轮辩论 |
| Phase 7E | Research-Synthesis v2 | 多来源证据、冲突证据处理、引用链路 |
| Phase 8 | SDK / CLI / Multi-tenant | Python/Go SDK、CLI、Auth/Quota、配置导入导出 |

---

## 十二、Shannon 原生能力 vs Lite 实现对照

| 功能 | Shannon 原生能力 | Cribug Phase 6 Lite | 说明 |
|------|-----------------|---------------------|------|
| MCP | Tool protocol / external tools | MCP server registry + tool call activity | Phase 6A 先支持 allowlist MCP |
| Sandbox | Code execution / WASI | RunWASIActivity | Phase 6B 默认 WASI demo |
| Skills | 内置 skills system | Skill manifest + executor | Phase 6C 先支持 basic skills |
| Hooks | 内置 event hooks | Hook registration + emit activity | Phase 6D 先支持 basic hooks |
| RAG | 原生 memory / retrieval | Qdrant + embedding activity | Phase 6E 先支持 basic retrieval |
| Research | 内置 research workflow | Research-Synthesis v1 | Phase 6F 先支持 basic synthesis |
| Workspace | Append-only event stream | WorkspaceAppend / WorkspaceList | Phase 5 延续 |
| ReAct Tools | Workflow-level tool action | MCP/Sandbox/Skill as ReAct tools | Phase 4 冻结 |
| Security | Agent policy | tool permission + hook + audit | Phase 5/6 扩展 |

---

## 十三、开发者配置参考

### 13.1 Feature Flags

| Feature Flag | 类型 | 默认值 | 说明 |
|-------------|------|--------|------|
| enable_mcp | bool | false | 开启 MCP 能力 |
| enable_sandbox | bool | false | 开启 Sandbox 能力 |
| enable_skills | bool | false | 开启 Skills System |
| enable_hooks | bool | false | 开启 Hooks Event System |
| enable_rag | bool | false | 开启 RAG 能力 |
| enable_research_synthesis | bool | false | 开启 Research-Synthesis v1 |
| mcp_default_timeout | int | 30 | MCP tool 超时（秒） |
| sandbox_default_timeout | int | 30 | Sandbox 超时（秒） |
| sandbox_max_memory_mb | int | 128 | 内存限制（MB） |
| sandbox_allow_network | bool | false | 默认禁止网络 |

### 13.2 Test Flags

| Flag | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| REAL_EMBEDDING_TEST | int | 0 | 真实 embedding 测试 |
| REAL_LLM_TEST | int | 0 | 真实 LLM 测试 |
| REAL_MCP_TEST | int | 0 | 真实 MCP 测试 |

### 13.3 Environment Variables

```bash
# Phase 6 Feature Flags
ENABLE_MCP=false
ENABLE_SANDBOX=false
ENABLE_SKILLS=false
ENABLE_HOOKS=false
ENABLE_RAG=false
ENABLE_RESEARCH_SYNTHESIS=false

# MCP
MCP_SERVER_ALLOWLIST=filesystem-tools,github-tools
MCP_DEFAULT_TIMEOUT=30
MCP_MAX_RESULT_SIZE=65536

# Sandbox
SANDBOX_DEFAULT_TIMEOUT=30
SANDBOX_MAX_MEMORY_MB=128
SANDBOX_ALLOW_NETWORK=false

# RAG / Qdrant
QDRANT_HOST=localhost
QDRANT_GRPC_PORT=6334
EMBEDDING_MODEL=text-embedding-3-small
EMBEDDING_DIM=1536
RAG_DEFAULT_TOP_K=10
RAG_MAX_CONTEXT_TOKENS=4096

# Skills
SKILL_REGISTRY_MODE=local

# Hooks
HOOKS_ENABLED=false
HOOKS_BLOCKING_ENABLED=false

# Real Test Flags（默认 0，不进入 CI）
REAL_EMBEDDING_TEST=0
REAL_LLM_TEST=0
REAL_MCP_TEST=0

# Real LLM 限制
OPENAI_API_KEY=
LLM_TEMPERATURE=0
LLM_MAX_TOKENS=4096
REAL_LLM_TEST_MAX_COST_USD=1

# Workspace（Phase 5 延续）
WORKSPACE_TTL_HOURS=24
```

---

## 附录 A：测试脚本说明

### smoke_test_phase6.sh 扩展

Phase 6 需要以下测试脚本：

| 脚本 | 内容 |
|------|------|
| `test_mcp_tool_runtime.sh` | MCP server 注册、tool discovery、tool call、权限检查、audit log |
| `test_sandbox_execution.sh` | WASI 执行、stdout/stderr 捕获、timeout、resource limit、audit |
| `test_skills_smoke.sh` | Skill manifest 注册、查询、执行、workspace 写入、audit |
| `test_hooks_smoke.sh` | Hook 注册、before/after tool hook、blocking hook、audit |
| `test_rag_qdrant_smoke.sh` | Document upload、chunk、embedding（mock）、Qdrant upsert/search、workspace 写入 |
| `test_research_synthesis_smoke.sh` | Query decomposition、evidence retrieval、workspace append/read、synthesis（mock LLM）|
| `test_phase6_e2e.sh` | 端到端集成测试（调用所有 Slices） |

每个测试脚本：
- 使用 mock/fixture data 作为默认
- `REAL_MCP_TEST=1` 时可跑真实 MCP
- `REAL_EMBEDDING_TEST=1` 时可跑真实 embedding
- `REAL_LLM_TEST=1` 时可跑真实 LLM synthesis

---

*Phase 6 重构任务书 v2.0*
