# Multi-Agent LLM Orchestration System — MVP Project Specification

## 一、项目定位

本项目第一版不是完整 Cribug，不是生产级多智能体系统，而是一个**教学型、可复刻的 AI Agent Orchestration MVP**。

第一版目标：

- 跑通从 Client 到 Gateway 到 Temporal 到 Python LLM Service 再回到 Client 的完整链路
- 支持任务提交
- 支持任务状态查询
- 支持基础 SSE 事件流
- 支持 session_id
- 支持 token usage 记录
- 支持基于 max_total_tokens 和 max_completion_tokens 的预算限制
- 支持 Postgres 持久化
- 支持 Redis 短期状态缓存
- 为 DAG、RAG、Sandbox、OPA、MCP 等后续能力预留扩展点

**核心原则：第一版不追求功能多，追求链路稳、边界清、能扩展。**

---

## 二、MVP 范围

### 1. Gateway

**职责**：
- 提供 REST API
- 接收任务
- **唯一 task 创建方**：生成 task_id、插入 tasks(status=pending)、指定 workflow_id = "task-" + task_id
- 调用 Temporal Client 启动 Workflow，获取 run_id
- 更新 tasks(status=running, workflow_id, run_id)
- 插入 executions(status=running, workflow_id, run_id)
- 缓存 Redis 状态
- 写 TASK_CREATED event
- 返回 task_id + workflow_id + run_id + status + stream_url
- 查询任务状态（Redis first，Postgres fallback）
- 提供 SSE 事件流

**不做**：
- 不直接调用 LLM
- 不直接做 Agent 推理
- 不直接执行 Workflow 逻辑
- 不做 RAG
- 不做代码执行

### 2. Go + Temporal Worker

**职责**：
- 注册 Workflow 和 Activity
- 执行 SimpleWorkflow
- Workflow **不直接** HTTP / DB / Redis 调用
- **外部 IO 只能发生在 Activity 中**
- 执行 EmitEventActivity
- 执行 LoadSessionActivity
- 执行 EstimatePromptTokensActivity
- 执行 CheckBudgetActivity
- 执行 AgentActivity
- 执行 RecordUsageActivity
- 执行 SaveSessionActivity
- 执行 SaveResultActivity
- 执行 RecordExecutionCompletedActivity
- 执行 SaveFailureActivity
- 执行 RecordExecutionFailedActivity

**不做**：
- Workflow 内不直接 HTTP 调 LLM
- Workflow 内不直接操作数据库
- Workflow 内不直接操作 Redis
- Workflow 内不使用 goroutine
- Workflow 内不使用随机数和当前时间

### 3. Python LLM Service

**职责**：
- 接收标准 LLMRequest
- 调用 OpenAI-compatible provider
- 返回 LLMResponse（含 usage、latency_ms）
- 提供 GET /health（只检查进程和配置，不调 LLM provider）
- 预留 Anthropic/Ollama 字段（第一版不实现）

**不做**：
- 不负责任务状态
- 不负责 Workflow 编排
- 不负责 session 持久化
- 不负责预算最终裁决

### 4. PostgreSQL

**职责**：
- 存 tasks（业务表）
- 存 executions（业务表）
- 存 llm_calls（业务表）
- 存 session_messages（可选，feature flag 控制）
- 作为审计日志和查询投影

**不做**：
- 不替代 Temporal 保存 Workflow 状态
- 不存短期事件缓冲
- 不做向量检索

### 5. Redis

**职责**：
- 存 session recent messages（List，最多 50 条，TTL 7 天）**MVP 必做**
- 存 task progress cache（Hash，TTL 1 天）
- 存 SSE events（Stream，MAXLEN 100，TTL 1 天）
- 存 idempotency key（可选）

**不做**：
- 不作为长期数据库
- 不作为 Workflow 状态事实来源
- 不存完整审计日志
- 不做 Postgres fallback（事件写入失败只记录日志）

### 6. SSE Event Streaming

**职责**：
- 让 Client 能实时看到任务执行过程
- Gateway 提供 GET /api/v1/stream/sse?task_id=xxx
- Workflow Activity 通过 EmitEventActivity 写 Redis Stream
- Gateway 从 Redis Stream 读取并推送

**第一版事件类型**：
- TASK_CREATED（Gateway 写）
- WORKFLOW_STARTED
- SESSION_LOADED
- LLM_STARTED
- LLM_COMPLETED
- USAGE_RECORDED
- TASK_COMPLETED
- TASK_FAILED
- TASK_BUDGET_EXCEEDED

### 7. Basic Budget Tracking

**职责**：
- 使用 max_total_tokens 和 max_completion_tokens
- 调用前估算 prompt tokens（MVP 使用 ceil(len(text)/4)）
- 若 estimated_prompt_tokens > max_total_tokens，直接标记 budget_exceeded
- 否则计算 allowed_completion_tokens = min(max_completion_tokens, max_total_tokens - estimated_prompt_tokens)
- AgentActivity 调 LLM 时传 allowed_completion_tokens
- 调用后记录真实 usage
- 若 actual_total_tokens > max_total_tokens，标记 budget_exceeded

**不做**：
- streaming token 截断
- fallback model
- max_cost_usd

### 8. Cancel API

**状态**：后续预留，第一版不实现。

---

## 三、非 MVP 范围

以下内容**不在第一版**实现，但会在后续路线图中说明如何接入：

- DAGWorkflow
- Multi-Agent
- RAG / Qdrant / Milvus / Pinecone
- Document ingestion
- Rust Code Runner / WASI / Docker / gVisor 隔离
- OPA Policy
- MCP Tool Registry
- Web Search / Web Fetch
- Prometheus / Grafana / OpenTelemetry 完整链路
- Web UI / Desktop
- SDK / CLI
- Human approval / Fine-grained tenant isolation
- Cancel API

---

## 四、总体架构

```
┌──────────────────────────────────────────────────────────────────────────────┐
│                                    Client                                     │
└──────────────────────────────────────┬───────────────────────────────────────┘
                                       │
                                       ▼
┌──────────────────────────────────────────────────────────────────────────────┐
│                                   Gateway (:8080)                             │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐  ┌─────────────────┐  │
│  │ REST API    │  │ Task Create│  │ Status Query │  │ SSE Event Stream │  │
│  │ /api/v1/*   │  │ (唯一创建方)│  │              │  │                  │  │
│  └──────┬──────┘  └──────┬──────┘  └──────┬──────┘  └────────┬────────┘  │
│         │                │                │                    │            │
└─────────┼────────────────┼────────────────┼────────────────────┼──────────┘
          │                │                │                    │
          ▼                ▼                ▼                    ▼
┌─────────────────┐  ┌───────────┐  ┌─────────────────┐  ┌─────────────────┐
│ Temporal Client │  │  Redis    │  │   PostgreSQL     │  │  Redis Stream   │
│ (Start Workflow)│  │ (Cache)   │  │ (orchestrator DB)│  │ (task:xxx:events)│
└────────┬────────┘  └─────┬─────┘  └────────┬────────┘  └─────────────────┘
         │                  │                  │
         ▼                  ▼                  ▼
┌──────────────────────────────────────────────────────────────────────────────┐
│                            Temporal Worker (:7233)                            │
│  ┌─────────────────────────────────────────────────────────────────────────┐ │
│  │                        SimpleWorkflow                                    │ │
│  │  Workflow 不直接 HTTP/DB/Redis，外部 IO 只在 Activity 中                  │ │
│  │  EmitEvent(WORKFLOW_STARTED) → LoadSession → EstimatePromptTokens       │ │
│  │  → CheckBudget → AgentActivity → RecordUsage → SaveSession              │ │
│  │  → SaveResult → RecordExecutionCompleted → EmitEvent(TASK_COMPLETED)   │ │
│  │  失败路径：SaveFailure → RecordExecutionFailed → EmitEvent(TASK_*)     │ │
│  └────────────────────────────────┬────────────────────────────────────────┘ │
│                                   │ Activities                                │
└───────────────────────────────────┼──────────────────────────────────────────┘
                                    │
         ┌──────────────────────────┼──────────────────────────┐
         │                          │                          │
         ▼                          ▼                          ▼
┌─────────────────┐        ┌─────────────────┐        ┌─────────────────┐
│ Python LLM      │        │ PostgreSQL       │        │ Redis           │
│ Service (:8000) │        │ (写入 tasks/    │        │ (Session Cache  │
│                 │        │  executions/     │        │  Events)        │
│                 │        │  llm_calls)      │        │                 │
└────────┬────────┘        └─────────────────┘        └─────────────────┘
         │
         ▼
┌─────────────────┐
│ OpenAI-         │
│ Compatible API  │
└─────────────────┘
```

---

## 五、技术栈分层

```
┌─────────────────────────────────────────────────────────────┐
│  1. 接入层 (:8080)                                          │
│     Gateway: Go HTTP (net/http + chi)                         │
│     REST API + SSE Endpoint                                  │
├─────────────────────────────────────────────────────────────┤
│  2. 编排层 (:7233)                                          │
│     Go + Temporal SDK                                        │
│     Worker + SimpleWorkflow + Activities                     │
│     Workflow 不直接 HTTP/DB/Redis                            │
├─────────────────────────────────────────────────────────────┤
│  3. 智能层 (:8000)                                          │
│     Python LLM Service (FastAPI)                            │
│     OpenAI-compatible Adapter                                │
├─────────────────────────────────────────────────────────────┤
│  4. 状态层                                                   │
│     Temporal History (执行事实来源)                          │
│     PostgreSQL :5432 (查询投影/审计)                        │
│     Redis :6379 (短期缓存/SSE事件)                          │
├─────────────────────────────────────────────────────────────┤
│  5. 配置层                                                   │
│     .env (secrets)                                          │
│     config/models.yaml                                        │
│     config/features.yaml                                     │
│     config/budget.yaml                                        │
├─────────────────────────────────────────────────────────────┤
│  6. 部署层                                                   │
│     Docker Compose                                           │
└─────────────────────────────────────────────────────────────┘
```

---

## 六、组件职责边界

| 组件 | 技术栈 | 做什么 | 不做什么 | MVP |
|------|--------|--------|----------|-----|
| Gateway | Go HTTP :8080 | 唯一 task 创建方、启动 Workflow、状态查询、SSE 推送 | 直接调用 LLM、做 Agent 推理 | 是 |
| Temporal | Temporal | Workflow 执行状态事实来源、Activity 调度 | 直接操作 DB/Redis | 是 |
| Go Worker | Go | 注册 Workflow/Activity、执行 SimpleWorkflow | Workflow 直接 HTTP/DB/Redis | 是 |
| SimpleWorkflow | Go | 确定性编排逻辑、调用 Activities | 直接 IO、goroutine/随机数 | 是 |
| Activity 层 | Go | 所有外部 IO（HTTP/DB/Redis） | 直接在 Workflow 中调用 | 是 |
| EmitEventActivity | Go | 写事件到 Redis Stream | 阻断主流程 | 是 |
| LoadSessionActivity | Go | 读取 Redis session messages | 写操作 | 是 |
| EstimatePromptTokensActivity | Go | 估算 prompt tokens (len/4) | LLM 调用 | 是 |
| CheckBudgetActivity | Go | 检查 max_total/max_completion | 直接拒绝请求 | 是 |
| AgentActivity | Go | 调用 Python LLM Service | 直接调 LLM provider | 是 |
| RecordUsageActivity | Go | 写 llm_calls 到 Postgres | 业务决策 | 是 |
| SaveSessionActivity | Go | 写 session 到 Redis（必做）、Postgres（可选） | - | 是 |
| SaveResultActivity | Go | 更新 task result 到 Postgres | - | 是 |
| RecordExecutionCompletedActivity | Go | 更新 execution status=completed | - | 是 |
| SaveFailureActivity | Go | 记录 task 失败信息 | - | 是 |
| RecordExecutionFailedActivity | Go | 更新 execution status=failed | - | 是 |
| Python LLM Service | Python FastAPI :8000 | OpenAI-compatible 调用、usage 返回、GET /health | 任务编排、状态管理 | 是 |
| PostgreSQL | PostgreSQL :5432 | tasks/executions/llm_calls 持久化 | 向量检索、短期缓存 | 是 |
| Redis | Redis :6379 | session cache（必做）、task status cache、events stream | 长期存储、事实来源 | 是 |
| Config System | Go + YAML | 加载配置、feature flag、model/budget 配置 | 运行时修改 | 是 |
| Temporal UI | Browser :8088 | Temporal History 可视化 | - | 是 |

---

## 七、核心数据流

### 1. Submit Task（Gateway 唯一创建）

```
Client
  → POST /api/v1/tasks
  → Gateway validates request
  → Gateway generates task_id (UUID)
  → Gateway inserts tasks(status=pending, workflow_id="task-"+task_id, run_id=NULL)
  → Gateway starts Temporal Workflow (sync)
  → Temporal returns run_id
  → Gateway updates tasks(status=running, workflow_id, run_id)
  → Gateway inserts executions(status=running, workflow_id, run_id)
  → Gateway caches task status in Redis (TTL=1d)
  → Gateway writes TASK_CREATED event to Redis Stream
  → Gateway returns {task_id, workflow_id, run_id, status:"running", stream_url}
```

### 2. Execute SimpleWorkflow

```
SimpleWorkflow
  → EmitEventActivity(WORKFLOW_STARTED)
  → LoadSessionActivity(session_id)
    → Redis: session:{session_id}:messages
  → EstimatePromptTokensActivity(prompt, model)
    → MVP: ceil(len(prompt) / 4)
  → CheckBudgetActivity(estimated_prompt_tokens, max_total_tokens, max_completion_tokens)
    → If estimated > max_total: return budget_exceeded
    → Else: allowed_completion = min(max_completion, max_total - estimated)
  → AgentActivity(task_id, query, session_messages, model, allowed_completion)
    → Python LLM Service POST /chat
    → return LLMResponse
  → RecordUsageActivity(task_id, usage, latency_ms)
    → Postgres: llm_calls
  → SaveSessionActivity(session_id, messages)
    → Redis: session:{session_id}:messages (LTRIM 50, TTL 7d) **MVP 必做**
    → Postgres: session_messages **可选，feature flag 控制**
  → SaveResultActivity(task_id, result, usage)
    → Postgres: tasks.result, tasks.usage_*
    → Redis: task:{task_id}:status
  → RecordExecutionCompletedActivity(task_id, execution_id)
    → Postgres: executions.status=completed, completed_at
  → EmitEventActivity(TASK_COMPLETED)

失败路径：
  → CheckBudgetActivity 返回 budget_exceeded
    → SaveFailureActivity(task_id, "budget_exceeded", error)
    → RecordExecutionFailedActivity(execution_id, "budget_exceeded")
    → EmitEventActivity(TASK_BUDGET_EXCEEDED)

  → AgentActivity 返回 error
    → SaveFailureActivity(task_id, "llm_error", error)
    → RecordExecutionFailedActivity(execution_id, "llm_error", error)
    → EmitEventActivity(TASK_FAILED)
```

### 3. Query Task

```
Client
  → GET /api/v1/tasks/{task_id}
  → Gateway reads Redis first (task:{task_id}:status)
  → fallback Postgres (tasks table)
  → return task with status/result/error/workflow_id/run_id
```

### 4. Stream Events

```
Client
  → GET /api/v1/stream/sse?task_id=xxx
  → Gateway reads Redis Stream from position 0 (已有事件)
  → blocks waiting for new events
  → pushes text/event-stream
  → on TASK_COMPLETED/TASK_FAILED/TASK_BUDGET_EXCEEDED: close connection
```

---

## 八、API 设计

### POST /api/v1/tasks

**Request:**
```json
{
  "query": "What is the capital of France?",
  "session_id": "optional-session-id",
  "config": {
    "max_total_tokens": 8000,
    "max_completion_tokens": 1024,
    "model": "gpt-4o-mini",
    "temperature": 0.7
  }
}
```

**Response (202 Accepted):**
```json
{
  "task_id": "uuid-xxx",
  "workflow_id": "task-uuid-xxx",
  "run_id": "run-id-xxx",
  "status": "running",
  "stream_url": "/api/v1/stream/sse?task_id=uuid-xxx"
}
```

### GET /api/v1/tasks/{task_id}

**Response:**
```json
{
  "task_id": "uuid-xxx",
  "workflow_id": "task-uuid-xxx",
  "run_id": "run-id-xxx",
  "session_id": "session-id",
  "status": "completed",
  "result": "The capital of France is Paris.",
  "error": null,
  "error_type": null,
  "usage": {
    "prompt_tokens": 20,
    "completion_tokens": 15,
    "total_tokens": 35
  },
  "model": "gpt-4o-mini",
  "max_total_tokens": 8000,
  "max_completion_tokens": 1024,
  "created_at": "2026-05-12T10:00:00Z",
  "updated_at": "2026-05-12T10:00:05Z"
}
```

### GET /api/v1/stream/sse?task_id=xxx

**SSE Event Format:**
```
event: TASK_CREATED
data: {"task_id":"uuid-xxx","timestamp":"2026-05-12T10:00:00Z"}

event: WORKFLOW_STARTED
data: {"task_id":"uuid-xxx","workflow_id":"task-uuid-xxx","run_id":"run-id-xxx","timestamp":"2026-05-12T10:00:01Z"}

event: LLM_STARTED
data: {"task_id":"uuid-xxx","model":"gpt-4o-mini","timestamp":"2026-05-12T10:00:02Z"}

event: LLM_COMPLETED
data: {"task_id":"uuid-xxx","usage":{"total_tokens":35},"latency_ms":1200,"timestamp":"2026-05-12T10:00:03Z"}

event: TASK_COMPLETED
data: {"task_id":"uuid-xxx","status":"completed","timestamp":"2026-05-12T10:00:03Z"}
```

### GET /health

**Response:**
```json
{
  "status": "healthy",
  "dependencies": {
    "temporal": "connected",
    "postgres": "connected",
    "redis": "connected"
  }
}
```

### POST /api/v1/tasks/{task_id}/cancel

**状态**：后续预留，第一版不实现。

---

## 九、Workflow 设计

### SimpleWorkflow

**输入：TaskRequest**
```go
type TaskRequest struct {
    TaskID                 string
    Query                  string
    SessionID              string
    Model                  string
    Temperature            float64
    MaxTotalTokens        int
    MaxCompletionTokens   int
}
```

**输出：TaskResult**
```go
type TaskResult struct {
    TaskID       string
    Status       string  // "completed", "failed", "budget_exceeded"
    Answer       string
    Usage        *Usage
    Error        string
    ErrorType    string
}
```

**Activities 清单：**

| Activity | 职责 | Timeout | Retry Policy |
|----------|------|---------|--------------|
| EmitEventActivity | 写事件到 Redis Stream | 5s | NoRetry |
| LoadSessionActivity | 读取 Redis session messages | 10s | Retry3x |
| EstimatePromptTokensActivity | 估算 prompt tokens (MVP: ceil(len/4)) | 5s | NoRetry |
| CheckBudgetActivity | 检查 max_total_tokens/max_completion_tokens | 5s | NoRetry |
| AgentActivity | 调用 Python LLM Service | 60s | Retry2x |
| RecordUsageActivity | 写 llm_calls 到 Postgres | 10s | Retry3x |
| SaveSessionActivity | 写 session 到 Redis（必做）/ Postgres（可选） | 10s | Retry3x |
| SaveResultActivity | 更新 task result 到 Postgres | 10s | Retry3x |
| RecordExecutionCompletedActivity | 更新 execution status=completed | 10s | Retry3x |
| SaveFailureActivity | 记录 task 失败信息 | 10s | Retry3x |
| RecordExecutionFailedActivity | 更新 execution status=failed | 10s | Retry3x |

**关键约束：**
- Workflow 内只写确定性逻辑
- **Workflow 不直接 HTTP / DB / Redis，所有外部 IO 都放 Activity**
- EmitEventActivity 失败只记录日志，不阻断主流程
- 数据库写入 Activity 必须幂等
- Workflow 不创建 task，只执行任务

---

## 十、Python LLM Service 设计

### 端口

`:8000`

### GET /health

```python
@app.get("/health")
async def health():
    return {"status": "healthy"}
```

### POST /chat

**LLMRequest:**
```python
from pydantic import BaseModel
from typing import Optional, List, Dict, Any

class LLMRequest(BaseModel):
    trace_id: str
    task_id: str
    session_id: Optional[str] = None
    provider: str = "openai_compatible"
    model: str = "gpt-4o-mini"
    messages: List[Dict[str, str]]
    temperature: float = 0.7
    max_completion_tokens: int = 1024
    response_format: Optional[str] = None
    metadata: Optional[Dict[str, Any]] = None
```

**LLMResponse:**
```python
class Usage(BaseModel):
    prompt_tokens: int
    completion_tokens: int
    total_tokens: int

class LLMResponse(BaseModel):
    content: str
    usage: Usage
    model: str
    provider: str
    finish_reason: str
    provider_response_id: Optional[str] = None
    latency_ms: int
    error: Optional[str] = None
```

**接口实现（FastAPI + OpenAI SDK）：**
```python
# python_llm_service/app.py
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel
from typing import Optional, List, Dict, Any
import time

app = FastAPI()

class LLMRequest(BaseModel):
    trace_id: str
    task_id: str
    session_id: Optional[str] = None
    provider: str = "openai_compatible"
    model: str = "gpt-4o-mini"
    messages: List[Dict[str, str]]
    temperature: float = 0.7
    max_completion_tokens: int = 1024
    response_format: Optional[str] = None
    metadata: Optional[Dict[str, Any]] = None

class Usage(BaseModel):
    prompt_tokens: int
    completion_tokens: int
    total_tokens: int

class LLMResponse(BaseModel):
    content: str
    usage: Usage
    model: str
    provider: str
    finish_reason: str
    provider_response_id: Optional[str] = None
    latency_ms: int
    error: Optional[str] = None

@app.get("/health")
async def health():
    return {"status": "healthy"}

@app.post("/chat", response_model=LLMResponse)
async def chat(req: LLMRequest):
    if req.provider != "openai_compatible":
        raise HTTPException(status_code=400, detail="Only openai_compatible provider implemented in MVP")

    start = time.time()
    try:
        # 使用 OpenAI SDK 调用
        # client = OpenAI(api_key=os.environ["OPENAI_API_KEY"])
        # response = client.chat.completions.create(
        #     model=req.model,
        #     messages=req.messages,
        #     temperature=req.temperature,
        #     max_tokens=req.max_completion_tokens,
        # )
        # 返回 LLMResponse
        pass
    except Exception as e:
        return LLMResponse(
            content="",
            usage=Usage(prompt_tokens=0, completion_tokens=0, total_tokens=0),
            model=req.model,
            provider=req.provider,
            finish_reason="error",
            latency_ms=int((time.time() - start) * 1000),
            error=str(e)
        )
```

---

## 十一、Redis 设计

### Key 设计

| Key Pattern | 类型 | 内容 | TTL | 说明 |
|-------------|------|------|-----|------|
| `session:{session_id}:messages` | List | 最近 50 条消息 JSON | 7 天 | LTRIM 保留最新 50 条 |
| `task:{task_id}:status` | Hash | status, progress, updated_at | 1 天 | TTL 1 天 |
| `task:{task_id}:events` | Stream | AgentEvent 列表 | 1 天 | MAXLEN 100 |
| `idempotency:{key}` | String | task_id | 1 小时 | 可选 |

### 数据结构示例

**session:{session_id}:messages:**
```
LPUSH session:sess-123:messages '{"role":"user","content":"Hello","created_at":"2026-05-12T10:00:00Z"}'
LTRIM session:sess-123:messages 0 49
EXPIRE session:sess-123:messages 604800
```

**task:{task_id}:status:**
```
HSET task:uuid-xxx status "running" progress "50%" updated_at "2026-05-12T10:00:03Z"
EXPIRE task:uuid-xxx:status 86400
```

**task:{task_id}:events:**
```
XADD task:uuid-xxx:events * event_type "LLM_COMPLETED" payload '{"usage":{"total_tokens":35}}' created_at "2026-05-12T10:00:03Z"
XTRIM task:uuid-xxx:events MAXLEN 100
EXPIRE task:uuid-xxx:events 86400
```

### 说明

- Redis 只是短期缓存
- 最终任务结果仍然写 Postgres
- EmitEventActivity 失败只记录日志，不 fallback Postgres
- **MVP 必做 Redis session recent messages**

---

## 十二、PostgreSQL 设计

### 表结构

```sql
-- tasks 表
CREATE TABLE tasks (
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
    CONSTRAINT tasks_id_unique UNIQUE (id),
    CONSTRAINT tasks_workflow_id_unique UNIQUE (workflow_id),
    CONSTRAINT tasks_status_check CHECK (status IN ('pending', 'running', 'completed', 'failed', 'budget_exceeded', 'cancelled'))
);

-- executions 表
CREATE TABLE executions (
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
    CONSTRAINT executions_status_check CHECK (status IN ('running', 'completed', 'failed', 'cancelled'))
);

-- llm_calls 表
CREATE TABLE llm_calls (
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

-- session_messages 表（可选，feature flag 控制是否写入）
CREATE TABLE session_messages (
    id UUID PRIMARY KEY,
    session_id VARCHAR(255) NOT NULL,
    task_id UUID REFERENCES tasks(id),
    role VARCHAR(20) NOT NULL,
    content TEXT NOT NULL,
    created_at TIMESTAMP DEFAULT NOW()
);

-- 索引
CREATE INDEX idx_tasks_session_id ON tasks(session_id);
CREATE INDEX idx_tasks_status ON tasks(status);
CREATE INDEX idx_tasks_workflow_id ON tasks(workflow_id);
CREATE INDEX idx_executions_task_id ON executions(task_id);
CREATE INDEX idx_executions_workflow_id ON executions(workflow_id);
CREATE INDEX idx_llm_calls_task_id ON llm_calls(task_id);
CREATE INDEX idx_llm_calls_call_id ON llm_calls(call_id);
CREATE INDEX idx_session_messages_session_id ON session_messages(session_id);
```

### 状态模型

**Task Status:**
- `pending` — Gateway 创建，Workflow 未启动
- `running` — Workflow 执行中
- `completed` — 成功完成
- `failed` — 执行失败
- `budget_exceeded` — 超出预算
- `cancelled` — 后续预留

**Executions Status:**
- `running` — 执行中
- `completed` — 成功完成
- `failed` — 执行失败
- `cancelled` — 后续预留

**状态流转：**
```
pending → running → completed
pending → running → failed
pending → running → budget_exceeded
pending → running → cancelled（后续预留）
```

**终态（completed/failed/budget_exceeded）不能被旧状态覆盖。**

```sql
-- 条件更新示例
UPDATE tasks
SET status = 'completed', result = $2, updated_at = NOW()
WHERE id = $1 AND status = 'running';
```

---

## 十三、Error Taxonomy

| error_type | 说明 | 来源 |
|-----------|------|------|
| `validation_error` | 请求参数校验失败 | Gateway |
| `workflow_start_error` | Gateway 启动 Workflow 失败 | Gateway |
| `workflow_error` | Workflow 执行内部错误 | Worker |
| `llm_error` | LLM 调用错误 | AgentActivity |
| `llm_timeout` | LLM 调用超时 | AgentActivity |
| `budget_exceeded` | 超出 token 预算 | CheckBudgetActivity / RecordUsageActivity |
| `db_error` | 数据库操作错误 | Activity |
| `redis_error` | Redis 操作错误 | Activity |
| `session_error` | Session 加载/保存错误 | LoadSessionActivity / SaveSessionActivity |
| `unknown_error` | 未知错误 | - |

**TASK_FAILED event payload:**
```json
{
  "task_id": "uuid-xxx",
  "error_type": "llm_error",
  "message": "OpenAI API error: rate limit exceeded",
  "timestamp": "2026-05-12T10:00:05Z"
}
```

---

## 十四、Event Streaming 设计

### AgentEvent 数据结构

```go
type AgentEvent struct {
    EventID     string    `json:"event_id"`
    TaskID      string    `json:"task_id"`
    WorkflowID  string    `json:"workflow_id,omitempty"`
    RunID       string    `json:"run_id,omitempty"`
    Type        string    `json:"type"`
    Payload     map[string]any `json:"payload,omitempty"`
    CreatedAt   time.Time `json:"created_at"`
}
```

### 事件类型

| 事件类型 | Payload | 说明 |
|---------|---------|------|
| TASK_CREATED | task_id, workflow_id, query, timestamp | Gateway 写 |
| WORKFLOW_STARTED | task_id, workflow_id, run_id, timestamp | SimpleWorkflow |
| SESSION_LOADED | task_id, message_count, timestamp | LoadSessionActivity |
| LLM_STARTED | task_id, model, timestamp | AgentActivity |
| LLM_COMPLETED | task_id, usage, latency_ms, finish_reason, timestamp | AgentActivity |
| USAGE_RECORDED | task_id, total_tokens, timestamp | RecordUsageActivity |
| TASK_COMPLETED | task_id, status, result_length, timestamp | SimpleWorkflow |
| TASK_FAILED | task_id, error_type, message, timestamp | SimpleWorkflow |
| TASK_BUDGET_EXCEEDED | task_id, estimated_prompt, max_total, timestamp | CheckBudgetActivity |

### EmitEventActivity 实现

```go
func EmitEventActivity(ctx context.Context, event *AgentEvent) error {
    data, _ := json.Marshal(event)

    err := redis.XAdd(ctx, "task:"+event.TaskID+":events", "*", map[string]interface{}{
        "event_type": event.Type,
        "payload":    string(data),
        "created_at": event.CreatedAt.Format(time.RFC3339),
    }).Err()

    if err != nil {
        // 失败只记录日志，不阻断主流程，不 fallback Postgres
        log.Printf("EmitEventActivity XAdd failed: %v, event: %s", err, event.Type)
    }

    return nil
}
```

### Gateway SSE 实现

```go
func (g *Gateway) SSEHandler(w http.ResponseWriter, r *http.Request) {
    taskID := r.URL.Query().Get("task_id")

    w.Header().Set("Content-Type", "text/event-stream")
    w.Header().Set("Cache-Control", "no-cache")
    w.Header().Set("Connection", "keep-alive")

    streamKey := "task:" + taskID + ":events"
    lastID := "0"

    // 先读取已有事件
    existing, _ := redis.XRange(ctx, streamKey, "0", "+").Result()
    for _, e := range existing {
        lastID = e.ID
        fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Values["event_type"], e.Values["payload"])
    }
    w.(http.Flusher).Flush()

    // 阻塞等待新事件
    for {
        events, err := redis.XRead(ctx, &redis.XReadArgs{
            Streams: []string{streamKey},
            Start:   lastID,
            Block:   30 * time.Second,
        }).Result()

        for _, e := range events {
            lastID = e.ID
            eventType := e.Values["event_type"].(string)
            payload := e.Values["payload"].(string)

            fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, payload)
            w.(http.Flusher).Flush()

            // 终态事件后关闭连接
            if eventType == "TASK_COMPLETED" || eventType == "TASK_FAILED" || eventType == "TASK_BUDGET_EXCEEDED" {
                return
            }
        }
    }
}
```

---

## 十五、Budget Tracking 设计

### Task Config

| 字段 | 类型 | 说明 |
|------|------|------|
| max_total_tokens | int | prompt + completion 总预算，默认 8000 |
| max_completion_tokens | int | provider 输出上限，默认 1024 |
| model | string | 使用的模型 |

### Budget 检查流程

```
1. Gateway 接收 max_total_tokens / max_completion_tokens（不传则使用 config 默认值）
2. SimpleWorkflow 调 EstimatePromptTokensActivity
   - MVP: ceil(len(prompt) / 4)
   - 后续替换为真实 tokenizer
3. SimpleWorkflow 调 CheckBudgetActivity
   - 若 estimated_prompt_tokens > max_total_tokens
     → return budget_exceeded
   - 否则
     → allowed_completion = min(max_completion_tokens, max_total_tokens - estimated_prompt_tokens)
     → return allowed_completion_tokens
4. AgentActivity 调 LLM 时传 allowed_completion_tokens
5. RecordUsageActivity 写 llm_calls
6. SimpleWorkflow 检查 actual_total_tokens > max_total_tokens
   - 若超限：标记 budget_exceeded
   - 若未超限：标记 completed
```

### Budget 配置 (config/budget.yaml)

```yaml
budget:
  default_max_total_tokens_per_task: 8000
  default_max_completion_tokens: 1024
  default_model: gpt-4o-mini
  allow_override: true
```

### EstimatePromptTokensActivity 实现

```go
func EstimatePromptTokensActivity(ctx context.Context, text string, model string) (int, error) {
    // MVP: 粗略估算
    estimated := int(math.Ceil(float64(len(text)) / 4.0))
    return estimated, nil
    // 后续替换为真实 tokenizer，如 tiktoken 或 anthropic tokenizer
}
```

### CheckBudgetActivity 实现

```go
type CheckBudgetInput struct {
    EstimatedPromptTokens int
    MaxTotalTokens        int
    MaxCompletionTokens   int
}

type CheckBudgetOutput struct {
    Allowed                  bool
    Reason                   string
    AllowedCompletionTokens  int
    MaxAllowed               int
}

func CheckBudgetActivity(ctx context.Context, input *CheckBudgetInput) (*CheckBudgetOutput, error) {
    if input.EstimatedPromptTokens > input.MaxTotalTokens {
        return &CheckBudgetOutput{
            Allowed: false,
            Reason:  "estimated_prompt_tokens exceeds max_total_tokens",
        }, nil
    }

    allowed := min(input.MaxCompletionTokens, input.MaxTotalTokens - input.EstimatedPromptTokens)

    return &CheckBudgetOutput{
        Allowed:                 true,
        AllowedCompletionTokens:  allowed,
        MaxAllowed:              input.MaxTotalTokens,
    }, nil
}
```

---

## 十六、Config System 设计

### 配置文件结构

```
my-orchestrator/
├── .env                          # Secrets（不上传到 git）
├── config/
│   ├── models.yaml               # 模型配置
│   ├── features.yaml             # Feature flags
│   └── budget.yaml               # Budget 配置
```

### .env 示例

```bash
# Secrets
OPENAI_API_KEY=sk-xxx

# Database
DATABASE_URL=postgres://admin:admin@postgres:5432/orchestrator

# Redis
REDIS_URL=redis://redis:6379/0

# Temporal
TEMPORAL_ADDRESS=temporal:7233

# LLM Service
LLM_SERVICE_URL=http://python-llm:8000
```

### config/models.yaml

```yaml
models:
  openai_compatible:
    api_base: https://api.openai.com/v1
    context_window: 128000
    default_max_completion_tokens: 1024
    # pricing 第一版不展开
```

### config/features.yaml

```yaml
features:
  enable_sse: true
  enable_budget: true
  enable_session_memory: true
  enable_session_postgres: false  # MVP: false, Redis session 必做
  enable_idempotency: false
```

### config/budget.yaml

```yaml
budget:
  default_max_total_tokens_per_task: 8000
  default_max_completion_tokens: 1024
  default_model: gpt-4o-mini
  allow_override: true
```

### 配置加载

```go
// internal/config/config.go
type Config struct {
    Models    ModelsConfig    `yaml:"models.yaml"`
    Features  FeaturesConfig  `yaml:"features.yaml"`
    Budget    BudgetConfig    `yaml:"budget.yaml"`
}

func Load(path string) (*Config, error) {
    // 加载 .env 到 os.Environ()
    // 加载 config/*.yaml
    // 返回合并后的 Config
}
```

---

## 十七、Docker Compose 设计

### 端口定义

| 服务 | 端口 | 说明 |
|------|------|------|
| Gateway | 8080 | HTTP API |
| Temporal UI | 8088 | Temporal Web UI |
| Temporal | 7233 | Temporal gRPC |
| Python LLM Service | 8000 | REST API |
| PostgreSQL | 5432 | Database |
| Redis | 6379 | Cache/Stream |

### Postgres 初始化说明

**Postgres 容器启动时只会执行 `/docker-entrypoint-initdb.d` 目录下的 .sql 和 .sh 文件，但不会递归执行子目录。**

因此：
- Temporal auto-setup 镜像会自动初始化 `temporal` 和 `temporal_visibility` database（通过其自带的 init script）
- 业务表 `orchestrator` database 需要单独初始化
- `migrations/001_init.sql` 放在 `orchestrator` 相关 init script 中执行

**目录结构：**
```
deploy/
├── docker-compose.yaml
├── postgres-init/
│   └── 01-init-orchestrator.sh    # 创建 orchestrator database 并执行 migrations
```

**01-init-orchestrator.sh:**
```bash
#!/bin/bash
set -e

# 创建 orchestrator database（如果不存在）
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres <<-EOSQL
    SELECT 'CREATE DATABASE orchestrator' WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'orchestrator')\gexec
EOSQL

# 执行 migrations
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname orchestrator <<-EOSQL
    \i /docker-entrypoint-initdb.d/migrations/001_init.sql
EOSQL
```

### docker-compose.yaml

```yaml
# deploy/docker-compose.yaml
version: '3.8'

services:
  # Temporal Core（auto-setup 会自动创建 temporal 和 temporal_visibility database）
  temporal:
    image: temporalio/auto-setup:latest
    ports:
      - "7233:7233"
    environment:
      - DB=postgresql
      - DB_PORT=5432
      - POSTGRES_USER=admin
      - POSTGRES_PWD=admin
      - POSTGRES_SEEDS=postgres
    depends_on:
      postgres:
        condition: service_healthy
    healthcheck:
      test: ["CMD-SHELL", "tctl namespace describe default || exit 1"]
      interval: 10s
      timeout: 5s
      retries: 5

  # Temporal UI
  temporal-ui:
    image: temporalio/ui:latest
    ports:
      - "8088:8080"
    environment:
      - TEMPORAL_ADDRESS=temporal:7233
    depends_on:
      temporal:
        condition: service_healthy

  # PostgreSQL（创建 admin user + orchestrator database）
  postgres:
    image: postgres:15
    environment:
      POSTGRES_USER: admin
      POSTGRES_PASSWORD: admin
    ports:
      - "5432:5432"
    volumes:
      - postgres_data:/var/lib/postgresql/data
      - ./postgres-init:/docker-entrypoint-initdb.d
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U admin"]
      interval: 5s
      timeout: 5s
      retries: 5

  # Redis
  redis:
    image: redis:7-alpine
    ports:
      - "6379:6379"
    volumes:
      - redis_data:/data
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 5s
      timeout: 5s
      retries: 5

  # Python LLM Service
  python-llm:
    build: ../python_llm_service
    ports:
      - "8000:8000"
    environment:
      - OPENAI_API_KEY=${OPENAI_API_KEY}
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8000/health"]
      interval: 10s
      timeout: 5s
      retries: 3

  # Gateway
  gateway:
    build: ..
    command: ./gateway
    ports:
      - "8080:8080"
    environment:
      - DATABASE_URL=postgres://admin:admin@postgres:5432/orchestrator
      - REDIS_URL=redis://redis:6379/0
      - TEMPORAL_ADDRESS=temporal:7233
      - LLM_SERVICE_URL=http://python-llm:8000
    depends_on:
      postgres:
        condition: service_healthy
      redis:
        condition: service_healthy
      temporal:
        condition: service_healthy
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8080/health"]
      interval: 10s
      timeout: 5s
      retries: 3

  # Worker
  worker:
    build: ..
    command: ./worker
    environment:
      - DATABASE_URL=postgres://admin:admin@postgres:5432/orchestrator
      - REDIS_URL=redis://redis:6379/0
      - TEMPORAL_ADDRESS=temporal:7233
      - LLM_SERVICE_URL=http://python-llm:8000
    depends_on:
      postgres:
        condition: service_healthy
      redis:
        condition: service_healthy
      temporal:
        condition: service_healthy
      python-llm:
        condition: service_healthy

volumes:
  postgres_data:
  redis_data:
```

---

## 十八、目录结构

```
my-orchestrator/
├── cmd/
│   ├── gateway/
│   │   └── main.go              # Gateway 入口
│   └── worker/
│       └── main.go              # Worker 入口
├── internal/
│   ├── api/
│   │   ├── handler.go           # HTTP handlers
│   │   ├── middleware.go        # 中间件
│   │   └── router.go            # 路由定义
│   ├── workflows/
│   │   ├── simple.go            # SimpleWorkflow
│   │   └── registry.go          # Workflow 注册
│   ├── activities/
│   │   ├── agent.go             # AgentActivity
│   │   ├── task.go              # SaveResultActivity, SaveFailureActivity
│   │   ├── execution.go         # RecordExecutionCompletedActivity, RecordExecutionFailedActivity
│   │   ├── session.go           # LoadSessionActivity, SaveSessionActivity
│   │   ├── usage.go             # RecordUsageActivity
│   │   ├── budget.go            # EstimatePromptTokensActivity, CheckBudgetActivity
│   │   └── events.go            # EmitEventActivity
│   ├── db/
│   │   └── postgres.go          # Postgres 连接和操作
│   ├── redis/
│   │   └── client.go            # Redis 连接和操作
│   ├── config/
│   │   └── config.go            # 配置加载
│   ├── events/
│   │   └── types.go             # AgentEvent 定义
│   └── types/
│       └── types.go             # 核心数据结构
├── python_llm_service/
│   ├── app.py                   # FastAPI 应用
│   ├── adapters/                # Provider adapters
│   │   └── openai.py            # openai_compatible adapter
│   └── requirements.txt
├── config/
│   ├── models.yaml
│   ├── features.yaml
│   └── budget.yaml
├── deploy/
│   ├── docker-compose.yaml
│   └── postgres-init/
│       └── 01-init-orchestrator.sh
├── migrations/
│   └── 001_init.sql             # 业务表结构
├── scripts/
│   └── smoke_test.sh
├── .env.example
├── go.mod
├── go.sum
└── README.md
```

---

## 十九、4 周实施计划

### Week 1：基础设施与 Gateway

**目标**：docker-compose 起基础服务，Gateway 提供任务提交和查询 API，不强求完整 Workflow

**任务**：
- [ ] 1.1 编写 docker-compose.yaml（Temporal + Postgres + Redis + Temporal UI）
- [ ] 1.2 编写 deploy/postgres-init/01-init-orchestrator.sh（创建 orchestrator database）
- [ ] 1.3 编写 migrations/001_init.sql（tasks/executions/llm_calls 表 + CHECK 约束）
- [ ] 1.4 实现 Go 项目结构（cmd/internal 分离）
- [ ] 1.5 实现配置加载（.env + YAML）
- [ ] 1.6 实现 Gateway HTTP handlers（POST /tasks, GET /tasks/{id}, GET /health）
- [ ] 1.7 实现 Redis client 连接
- [ ] 1.8 实现 Postgres client 连接
- [ ] 1.9 实现 Gateway 创建 task（生成 task_id、插入 tasks(status=pending)、启动 Workflow、更新 tasks(status=running, run_id)、插入 executions(status=running)）

**验收**：
- `docker-compose up` 能启动所有服务
- Gateway healthcheck 正常
- POST /api/v1/tasks 返回 task_id + workflow_id + run_id + status=running + stream_url
- GET /api/v1/tasks/{id} 能查询任务状态

---

### Week 2：Temporal Client + Worker + SimpleWorkflow

**目标**：Gateway 能启动 Temporal Workflow，Worker 能执行 SimpleWorkflow

**任务**：
- [ ] 2.1 实现 Go Worker main.go，注册 Workflow 和 Activities
- [ ] 2.2 实现 Temporal Client 从 Gateway 启动 Workflow（获取 run_id、更新 tasks）
- [ ] 2.3 实现 SimpleWorkflow（确定性编排逻辑，Workflow 不直接 HTTP/DB/Redis）
- [ ] 2.4 实现 EmitEventActivity（写 Redis Stream）
- [ ] 2.5 实现 RecordExecutionCompletedActivity / RecordExecutionFailedActivity
- [ ] 2.6 实现 SaveResultActivity / SaveFailureActivity
- [ ] 2.7 Gateway 启动 Workflow 成功后插入 executions(status=running)
- [ ] 2.8 端到端测试：POST /tasks → Gateway 启动 Workflow → executions 有记录

**验收**：
- POST /tasks 后 tasks.status=running，tasks.workflow_id="task-"+task_id，tasks.run_id 有值
- executions 表有对应记录 status=running
- Workflow 空跑后 tasks.status=completed，executions.status=completed

---

### Week 3：Python LLM Service + AgentActivity

**目标**：真实 LLM 调用，session 消息管理，usage 记录

**任务**：
- [ ] 3.1 实现 Python LLM Service（GET /health, POST /chat + openai_compatible adapter）
- [ ] 3.2 实现 AgentActivity（调用 Python LLM Service）
- [ ] 3.3 实现 LoadSessionActivity（读 Redis session messages）
- [ ] 3.4 实现 SaveSessionActivity（写 Redis session **必做**，Postgres 可选 feature flag）
- [ ] 3.5 实现 RecordUsageActivity（写 llm_calls 表）
- [ ] 3.6 实现 EstimatePromptTokensActivity（MVP: ceil(len/4)）
- [ ] 3.7 实现 CheckBudgetActivity（max_total/max_completion 检查）
- [ ] 3.8 Session 消息注入 LLM prompt
- [ ] 3.9 Budget 超限处理（budget_exceeded 标记）

**验收**：
- 输入 query，返回真实 LLM answer
- llm_calls 表有 usage 记录
- session_id 下能保存和加载最近消息（Redis）
- 超预算任务标记 budget_exceeded

---

### Week 4：SSE + Smoke Test + README + 完整联调

**目标**：完整事件流、smoke test、README

**任务**：
- [ ] 4.1 实现 Gateway SSE endpoint（从 Redis Stream 读取）
- [ ] 4.2 SSE 终态事件后关闭连接（TASK_COMPLETED/TASK_FAILED/TASK_BUDGET_EXCEEDED）
- [ ] 4.3 端到端测试 SSE 事件流（至少看到 TASK_CREATED, WORKFLOW_STARTED, LLM_STARTED, LLM_COMPLETED, TASK_COMPLETED）
- [ ] 4.4 编写 smoke_test.sh（使用 docker compose exec）
- [ ] 4.5 编写 README.md（架构图 + 启动说明 + API 示例）
- [ ] 4.6 完整联调，修复 bug

**验收**：
- 客户端 SSE 能看到至少 5 类事件
- smoke_test.sh 能完整跑通
- README 能让新开发者从零跑通

---

## 二十、验收标准

| # | 标准 | 验证方式 |
|---|------|----------|
| 1 | docker-compose up 能启动所有 MVP 服务 | 手动验证 |
| 2 | Gateway GET /health 返回 healthy | curl http://localhost:8080/health |
| 3 | Temporal UI 可访问 http://localhost:8088 | 浏览器验证 |
| 4 | POST /api/v1/tasks 能提交任务，返回 task_id + workflow_id + run_id | curl 测试 |
| 5 | tasks.workflow_id = "task-" + task_id，tasks.run_id 有值，tasks.status = "running" | psql 验证 |
| 6 | executions 表有对应记录，status = "running" | psql 验证 |
| 7 | tasks.status 和 executions.status 有 CHECK 约束 | psql 验证 |
| 8 | Worker 通过 Temporal 执行 SimpleWorkflow | Temporal UI 验证 |
| 9 | AgentActivity 调用 Python LLM Service | 日志验证 |
| 10 | llm_calls 表有 usage 记录（prompt/completion/total_tokens） | psql 验证 |
| 11 | Redis Stream 有 task:{task_id}:events | redis-cli 验证 |
| 12 | SSE 能看到 TASK_CREATED, WORKFLOW_STARTED, LLM_STARTED, LLM_COMPLETED, TASK_COMPLETED | curl + 日志验证 |
| 13 | max_total_tokens / max_completion_tokens 生效（超限标记 budget_exceeded） | curl 测试 |
| 14 | smoke_test.sh 完整跑通（使用 docker compose exec） | bash scripts/smoke_test.sh |

---

## 二十一、后续扩展路线图

### Phase 2：DAG + Synthesis

**目标**：支持复杂多步骤任务

```
新增组件：
- ClassifyTaskActivity（判断 low/high complexity）
- PlanDAGActivity（生成 DAG 图）
- ValidateDAGActivity（校验 DAG 有效性）
- DAGWorkflow（替代 SimpleWorkflow 处理复杂任务）
- SynthesisActivity（多节点结果合成）

接入方式：
- workflow_id/run_id 字段已在 MVP 表中预留
- Gateway 新增 /api/v1/tasks/{task_id}/dag 端点
```

### Phase 3：RAG

**目标**：支持知识库检索增强

```
新增组件：
- Python RAG Service（文档加载、chunking、embedding）
- Qdrant / Milvus（向量数据库）
- RAGActivity（检索相关文档）
- Context injection 到 AgentActivity

接入方式：
- AgentActivity 新增 RAG context 参数
- config/features.yaml 开启 enable_rag
```

### Phase 4：Rust Code Runner

**目标**：支持代码执行能力，使用 WASI / Docker / gVisor 隔离

```
新增组件：
- Rust Code Runner（代码执行引擎）
- WASI / Docker / gVisor 隔离层
- CodeExecutionActivity（调用沙盒）

安全考虑：
- CPU/内存/时间限制
- 不可信代码隔离（隔离层负责，非 Rust 本身）

接入方式：
- TaskActivity 改为调用 CodeExecutionActivity
- config/features.yaml 开启 enable_code_execution
```

### Phase 5：Governance and Observability

**目标**：生产级可观测性和策略控制

```
新增组件：
- OPA（Open Policy Agent）
- Prometheus metrics
- Grafana dashboards
- OpenTelemetry tracing

接入方式：
- Gateway middleware 集成 OPA
- Activity 添加 span tracking
```

### Phase 6：Tooling and UX

**目标**：完善开发者工具和用户体验

```
新增组件：
- MCP Tool Registry
- Web Search / Web Fetch Tool
- Web UI
- Python SDK
- CLI
- Cancel API
- OpenAI-compatible API

接入方式：
- MCP tools 通过 ToolRegistryActivity 接入
- Web UI 通过现有 Gateway API 获取数据
```

---

## 二十二、风险与取舍

| 取舍 | 理由 |
|------|------|
| 不做 DAG | 先保证单链路稳定，DAG 复杂度需要更完善的错误处理和状态管理 |
| 不做 RAG | 避免 ingestion/retrieval/context packing 过早复杂化，MVP 聚焦核心链路 |
| 不做 Rust Code Runner | 沙盒隔离需要 WASI/Docker/gVisor 等专门基础设施，第一版不需要 |
| 不做 OPA | 第一版没有复杂租户和权限场景 |
| 不做 Multi-Agent | 单 Agent 调用足够验证架构，后续可按 Phase 2 扩展 |
| 不做 Cancel API | 第一版简化，Cancel 需要 Workflow 取消传播机制，后续 Phase 6 实现 |
| Redis 只做短期缓存 | 不能替代 Postgres 作为事实来源，避免双写一致性问题 |
| EmitEventActivity 失败不 fallback Postgres | 简化第一版实现，事件只是观察性通道 |
| Temporal 和 Postgres 双状态 | Temporal 是执行事实来源，Postgres 是查询投影，明确边界避免混乱 |
| Gateway 不要变成上帝服务 | 只做 API、状态聚合、事件流，不做业务逻辑 |
| Python LLM Service 不要变成调度器 | 只做模型调用，职责单一 |
| 第一版重点是"跑通可靠闭环" | 不是"功能完整"，4 周内可演示的核心价值流 |

---

## 附录：Smoke Test 脚本

```bash
#!/bin/bash
# scripts/smoke_test.sh
#
# 依赖：curl, jq, docker compose
# psql 和 redis-cli 使用 docker compose exec 执行

set -e

GATEWAY_URL="http://localhost:8080"
TASK_ID=""
COMPOSE_FILE="deploy/docker-compose.yaml"

# 确保服务已启动
echo "=== Checking services ==="
curl -sf "$GATEWAY_URL/health" || { echo "FAIL: Gateway health check"; exit 1; }
echo "Gateway: OK"

echo ""
echo "=== 1. Create Task ==="
RESP=$(curl -sf -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{"query":"What is 2+2?","config":{"model":"gpt-4o-mini"}}')
TASK_ID=$(echo $RESP | jq -r '.task_id')
WORKFLOW_ID=$(echo $RESP | jq -r '.workflow_id')
RUN_ID=$(echo $RESP | jq -r '.run_id')
STATUS=$(echo $RESP | jq -r '.status')
echo "Task ID: $TASK_ID"
echo "Workflow ID: $WORKFLOW_ID"
echo "Run ID: $RUN_ID"
echo "Status: $STATUS"
[ -z "$TASK_ID" ] && { echo "FAIL: No task_id returned"; exit 1; }
[ "$STATUS" != "running" ] && { echo "FAIL: Expected status=running, got $STATUS"; exit 1; }
echo "PASS"

echo ""
echo "=== 2. Wait for Completion (polling) ==="
for i in $(seq 1 30); do
  STATUS=$(curl -sf "$GATEWAY_URL/api/v1/tasks/$TASK_ID" | jq -r '.status')
  echo "Status: $STATUS"
  if [ "$STATUS" = "completed" ] || [ "$STATUS" = "failed" ] || [ "$STATUS" = "budget_exceeded" ]; then
    break
  fi
  sleep 2
done

echo ""
echo "=== 3. Verify Result ==="
RESULT=$(curl -sf "$GATEWAY_URL/api/v1/tasks/$TASK_ID" | jq -r '.result')
[ -z "$RESULT" ] && { echo "FAIL: No result"; exit 1; }
echo "Result: $RESULT"
echo "PASS"

echo ""
echo "=== 4. Verify PostgreSQL tasks table ==="
DB_STATUS=$(docker compose -f "$COMPOSE_FILE" exec -T postgres psql -U admin -d orchestrator -t -c "SELECT status FROM tasks WHERE id='$TASK_ID';" 2>/dev/null | tr -d ' ')
echo "DB Status: $DB_STATUS"
[ -z "$DB_STATUS" ] && { echo "FAIL: No task in DB"; exit 1; }
echo "PASS"

echo ""
echo "=== 5. Verify PostgreSQL executions table ==="
EXEC_STATUS=$(docker compose -f "$COMPOSE_FILE" exec -T postgres psql -U admin -d orchestrator -t -c "SELECT status FROM executions WHERE task_id='$TASK_ID';" 2>/dev/null | tr -d ' ')
echo "Exec Status: $EXEC_STATUS"
[ -z "$EXEC_STATUS" ] && { echo "FAIL: No execution in DB"; exit 1; }
echo "PASS"

echo ""
echo "=== 6. Verify PostgreSQL llm_calls table ==="
LLM_TOKENS=$(docker compose -f "$COMPOSE_FILE" exec -T postgres psql -U admin -d orchestrator -t -c "SELECT total_tokens FROM llm_calls WHERE task_id='$TASK_ID';" 2>/dev/null | tr -d ' ')
echo "LLM Total Tokens: $LLM_TOKENS"
[ -z "$LLM_TOKENS" ] && { echo "FAIL: No llm_call in DB"; exit 1; }
echo "PASS"

echo ""
echo "=== 7. Verify Redis Events ==="
EVENT_COUNT=$(docker compose -f "$COMPOSE_FILE" exec -T redis redis-cli XLEN "task:$TASK_ID:events" 2>/dev/null)
echo "Redis Events: $EVENT_COUNT"
[ -z "$EVENT_COUNT" ] || [ "$EVENT_COUNT" = "0" ] && { echo "FAIL: No Redis events"; exit 1; }
echo "PASS"

echo ""
echo "=== ALL TESTS PASSED ==="
```

---

*MVP 版本任务书（最终实施级修正版）— 强调组件协同边界和可复刻性。*
