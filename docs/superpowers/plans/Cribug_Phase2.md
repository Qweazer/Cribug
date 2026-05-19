# Cribug Phase 2/3 Roadmap — Toward Shannon 40% Capability

## 一、项目定位

本文件是 Phase 2/3 的路线图，**不是单次实现任务书**。

执行时必须按 Slice 分段推进。任何 coding agent prompt 一次只能执行一个 Slice。

**当前已完成基线：**
- Phase 2A Slice 1 — AgentActivity Mock Full Chain：已完成
- Phase 2B Slice 2 — Usage + Budget + Failure Path：已完成
- Phase 2C Slice 3 — Session Memory：已完成
- Week 4 — SSE + smoke_test + README：已完成
- scripts/smoke_test.sh：已通过
- scripts/test_sse.sh：已通过

**当前下一执行目标：**
- Phase 3A Slice 4.1 — mode 字段 + feature flag + validation
- 不回头重做 Slice 1/2/3
- 不直接实现完整 DAGWorkflow
- 不实现 Multi-Agent
- 不实现 Tool
- 不接真实 OpenAI

**后续 Slice 逐步推进：** Slice 4.1 → Slice 4.2 → Slice 4.3 → Slice 4.4 → Slice 4.5 → Slice 4.6 → Slice 4.7 → Slice 5 → Slice 6。

本阶段不是完整 Shannon 复刻，不是生产级多智能体系统，而是 **教学型、可渐进扩展的 AI Agent Orchestration 第二/三阶段**。

**核心原则：链路稳、边界清、能扩展的原则不变。本阶段追求能力面扩展（~40% Shannon 能力），不追求代码量。**

### Phase 2 范围（已完成）

Phase 2 已完成：
- ✅ Single Agent Runtime 完整化（AgentActivity mock 全链路）
- ✅ Usage 记录（llm_calls 实际写入）
- ✅ Budget 拦截（CheckBudgetActivity 生效）
- ✅ Session Memory（Redis recent messages）
- ✅ Failure Path（budget_exceeded / llm_error / service_down）
- ✅ SSE endpoint 完整事件

**Phase 2 不包含 DAGWorkflow、Multi-Agent、Tool Abstraction。**

### Phase 3 范围（Phase 3A / 3B / 3C）

Phase 3 包含：
- Phase 3A — DAGWorkflow Lite（Slice 4）
- Phase 3B — Multi-Agent Lite（Slice 5）
- Phase 3C — Tool Abstraction Lite + Smoke test 完整化（Slice 6）

**Phase 3 不做 swarm 并发、sandbox、MCP 完整协议。**

---

## 二、与第一阶段 PROJECT_SPEC.md 的衔接

### 第一阶段已完成

第一阶段已完成以下地基（**不重做**）：

- Gateway /health
- POST /api/v1/tasks
- GET /api/v1/tasks/{id}
- Gateway 启动 Temporal Workflow
- Worker 执行 SimpleWorkflow 空壳（返回 "empty workflow completed"）
- Postgres tasks/executions 状态闭环（pending → running → completed/failed）
- Redis task status Hash
- Redis Stream events（TASK_CREATED / WORKFLOW_STARTED / TASK_COMPLETED）
- SSE endpoint（GET /api/v1/stream/sse?task_id=xxx）
- Python LLM Service mock（/health + /chat 返回 mock answer + usage）
- llm_calls 表已预留（字段可能尚未完整闭环）
- session_id 字段已支持

### Phase 2/3 只在地基上增加能力

Phase 2/3 **不重构**以下稳定边界：

- 不重构 Gateway task 创建主流程
- 不重构 Redis Stream / SSE 主逻辑
- 不重构 docker-compose 基础设施
- 不把 Gateway 变成 LLM 调用方
- 不让 Workflow 直接 HTTP / DB / Redis

Phase 2/3 在以下稳定边界上增加能力：

- SimpleWorkflow 从空 result 升级为真实 AgentActivity 调用
- llm_calls 从字段预留升级为实际写入（prompt_tokens / completion_tokens / total_tokens / latency_ms / finish_reason）
- session_id 从字段支持升级为 Redis recent messages（RPUSH / LRANGE / LTRIM）
- budget 从字段支持升级为实际拦截（CheckBudgetActivity）
- SSE 从 3 类事件升级为完整 runtime 事件（SESSION_LOADED / LLM_STARTED / LLM_COMPLETED / USAGE_RECORDED / TASK_FAILED / TASK_BUDGET_EXCEEDED）
- 后续（Phase 3）再增加 mode=dag 和 mode=multi_agent

### 明确禁止

- 不在 Phase 2 做 DAG / Multi-Agent / Tool
- 不在 Phase 2 接真实 OpenAI（LLM_MODE=mock 默认）
- 不改第一阶段 PROJECT_SPEC.md
- 不把 Phase 2/3 合并成一次性实现任务

---

## 三、阶段范围

### Phase 2: Single Agent Runtime（已完成）

包含：
- ✅ Slice 1: AgentActivity Mock Full Chain
- ✅ Slice 2: Usage + Budget + Failure Path
- ✅ Slice 3: Session Memory + Failure Path

**职责：**

- Workflow 调用 AgentActivity，AgentActivity 调用 Python LLM Service（mock mode）
- Python LLM Service 支持 mock mode（固定 answer + usage）
- openai_compatible adapter 为可选增强，Phase 2 默认不启用
- RecordUsageActivity 写 llm_calls（prompt_tokens / completion_tokens / total_tokens / latency_ms / finish_reason）
- EstimatePromptTokensActivity（MVP：ceil(len(text)/4)）
- CheckBudgetActivity（estimated_prompt > max_total → budget_exceeded）
- LoadSessionActivity 从 Redis 加载 session recent messages
- SaveSessionActivity 写 Redis session messages（List，LTRIM 50，TTL 7d）
- SaveFailureActivity + RecordExecutionFailedActivity
- 新增事件：SESSION_LOADED / LLM_STARTED / LLM_COMPLETED / USAGE_RECORDED / TASK_FAILED / TASK_BUDGET_EXCEEDED

**不做：**

- 不做真实 tokenizer（Phase 4+）
- 不做 fallback model
- 不做 streaming token 截断
- 不做 openai_compatible（Phase 2 默认 mock）
- 不做 DAG / Multi-Agent / Tool

---

### Phase 3A: DAGWorkflow Lite

包含：
- Slice 4: DAGWorkflow Lite

**职责：**

- config.mode = "dag" 且 enable_dag_workflow=true 时进入 DAGWorkflow（否则 400 validation_error）
- ClassifyTaskActivity 估算任务复杂度（MVP：简单规则）
- PlanDAGActivity 生成固定简单 DAG（2-3 个 node，顺序依赖）
- DAG 表示：nodes = [{"node_id": "n1", "depends_on": []}, {"node_id": "n2", "depends_on": ["n1"]}, ...]
- DAGWorkflow 按拓扑顺序执行 nodes，每个 node 调用 AgentActivity
- 每个 DAG node 的 usage 写入 llm_calls 表（带 node_id 标识）
- DAG 总 usage 汇总到 tasks 表
- DAG 模式必须继续支持 budget 检查
- DAG node 结果存入 Redis dag:{task_id}:nodes（可选）
- SynthesisActivity 合并 DAG node results
- 新增事件：DAG_PLANNED / DAG_NODE_STARTED / DAG_NODE_COMPLETED / DAG_SYNTHESIZED

**DAG Node Usage 记录设计：**

每个 DAG node 执行时：
1. AgentActivity 被调用，带 node_id 参数
2. llm_calls 表新增 node_id 字段记录
3. 每个 node 产生一条 llm_calls 记录
4. DAG 完成后，SynthesisActivity 汇总所有 node 的 usage 到 tasks 表

```sql
-- llm_calls 表需要 node_id 字段（Phase 3A 新增）
ALTER TABLE llm_calls ADD COLUMN IF NOT EXISTS node_id VARCHAR(50);
```

**Budget 支持（Phase 3A 必须）：**

DAGWorkflow 必须继续支持 budget 检查：
- 在 DAG 开始前检查总 budget
- 可选：每个 node 的 allowed_completion_tokens = min(node_max, remaining_budget)
- 若任一 node 超预算，记录但继续执行（不打断 DAG）
- DAG 完成后汇总实际 usage

**不做：**

- 不做 DAG 并发执行（Phase 4+）
- 不做 DAG 可视化
- 不做 DAG 动态重规划

---

### Phase 3B: Multi-Agent Lite

包含：
- Slice 5: Multi-Agent Lite

**职责：**

- config.mode = "multi_agent" 且 enable_multi_agent=true 时进入 MultiAgentWorkflow（否则 400 validation_error）
- Agent role abstraction：planner / worker / critic / synthesizer
- AgentActivity 支持 role 参数，Python LLM Service 根据 role 生成不同 system prompt
- MultiAgentWorkflow 执行链：planner → worker → critic → synthesizer（顺序，不做 P2P）
- 每个 agent 的 usage 写入 llm_calls 表（带 agent_role 标识）
- 新增事件：AGENT_STARTED / AGENT_COMPLETED / CRITIC_REVIEWED / SYNTHESIS_COMPLETED

**不做：**

- 不做 swarm 并发
- 不做 agent 间 P2P 通信
- 不做 Lead Agent + workspace 文件共享
- 不做 agent 间的 idle / completed 状态机

---

### Phase 3C: Tool Abstraction Lite

包含：
- Slice 6: Tool Abstraction Lite

**职责：**

- ToolCall 数据结构：{tool_name, arguments: map}
- ToolResult 数据结构：{tool_name, output, error, latency_ms}
- ToolActivity 执行 safe tool（calculator / echo）
- Tool 调用由确定性规则触发（config.enable_tools=true 且 query 匹配规则，或 ToolDecisionActivity 决定）
- 不依赖模型"自觉调用 tool"
- tool_calls 写入 Postgres（可选，Phase 3C 可跳过）
- 新增事件：TOOL_STARTED / TOOL_COMPLETED / TOOL_FAILED

**不做：**

- 不做 MCP 完整协议
- 不做 WASI / Firecracker sandbox
- 不执行任意代码（calculator 用白名单算术解析器，无 eval）
- 不做浏览器 tool（Phase 4+）

---

## 四、非本阶段范围

以下暂不进入实现，但会在路线图中说明后续如何接入：

- 完整 swarm collaboration（P2P / Lead Agent / workspace 文件共享 / 多轮迭代 handoff）
- 复杂 strategy graph（ReAct / Tree-of-Thoughts / Debate / Scientific）
- RAG / Qdrant / Milvus / document ingestion
- MCP Tool Registry（完整协议，Phase 3C 以后）
- Sandbox / WASI / Firecracker / Docker code runner
- Human approval / HITL workflow
- Desktop UI / Web UI
- SDK / CLI
- Multi-tenant Auth / Quota / Billing
- OpenTelemetry / Prometheus / Grafana 完整化
- Time-travel debugging 完整实现
- Cancel API / Pause / Resume
- Vector DB semantic memory

---

## 五、总体架构

```
┌──────────────────────────────────────────────────────────────────────────────┐
│                                    Client                                     │
└──────────────────────────────────────┬───────────────────────────────────────┘
                                       │
                                       ▼
┌──────────────────────────────────────────────────────────────────────────────┐
│                                   Gateway (:8080)                             │
│  ┌──────────────┐  ┌─────────────┐  ┌──────────────┐  ┌──────────────────┐  │
│  │ REST API     │  │ Task Create │  │ Status Query │  │ SSE Event Stream │  │
│  │ /api/v1/*    │  │ (唯一创建方) │  │              │  │                  │  │
│  └──────┬───────┘  └──────┬──────┘  └──────┬───────┘  └────────┬─────────┘  │
└─────────┼────────────────┼────────────────┼────────────────────┼───────────┘
          │                │                │                    │
          ▼                ▼                ▼                    ▼
┌─────────────────┐
│ Temporal Client │
│ Start Workflow  │
└────────┬────────┘
         │ gRPC 127.0.0.1:17233
         ▼
┌─────────────────┐
│ Temporal Server │
│    (:7233)      │
└────────┬────────┘
         │ Task Queue
         ▼
┌──────────────────────────────────────────────────────────────────────────────┐
│                 Temporal Worker (no public port)                            │
│  - polls Task Queue                                                          │
│  - Workflow Router 根据 config.mode 路由到：                                  │
│      SimpleWorkflow / DAGWorkflow / MultiAgentWorkflow                       │
│  - Workflow 只做确定性编排，不直接 HTTP/DB/Redis                              │
│  - 所有外部 IO 都在 Activities 中                                             │
└──────────────────────────────────┬───────────────────────────────────────────┘
                                   │
                    ┌──────────────┼──────────────┐
                    ▼              ▼              ▼
           ┌────────────────┐ ┌────────────┐ ┌──────────────┐
           │ Python LLM    │ │ PostgreSQL │ │    Redis     │
           │ Service (:8000)│ │ (持久化)    │ │ (状态+事件)  │
           │ mock + openai │ │            │ │              │
           └───────┬────────┘ └────────────┘ └──────────────┘
                   │
          ┌────────▼────────┐
          │ OpenAI-Compatible│
          │ API (真实/模拟)  │
          └─────────────────┘
```

---

## 六、技术栈分层

```
┌─────────────────────────────────────────────────────────────────────────────┐
│  1. 接入层 (:8080)                                                           │
│     Gateway: Go HTTP (net/http + chi)                                         │
│     POST /api/v1/tasks / GET /api/v1/tasks/{id} / GET /api/v1/stream/sse     │
├─────────────────────────────────────────────────────────────────────────────┤
│  2. 编排层                                                                   │
│     Temporal Server: 127.0.0.1:17233                                          │
│     Temporal Worker: no public port                                           │
│     Go + Temporal SDK                                                         │
│     Workflow Router → SimpleWorkflow / DAGWorkflow / MultiAgentWorkflow       │
│     Workflow 不直接 HTTP/DB/Redis                                              │
├─────────────────────────────────────────────────────────────────────────────┤
│  3. 智能层 (:8000)                                                           │
│     Python LLM Service (FastAPI)                                              │
│     mock mode + openai_compatible mode                                        │
│     Agent role support (planner/worker/critic/synthesizer)                    │
├─────────────────────────────────────────────────────────────────────────────┤
│  4. 状态层                                                                   │
│     Temporal History（执行事实来源）                                          │
│     PostgreSQL :5432（tasks/executions/llm_calls/session_messages 持久化）  │
│     Redis :6379（短期缓存/session messages/SSE events）                     │
├─────────────────────────────────────────────────────────────────────────────┤
│  5. 事件层                                                                   │
│     Redis Stream (task:{task_id}:events)                                      │
│     SSE (Gateway → Client)                                                    │
│     终态事件：TASK_COMPLETED / TASK_FAILED / TASK_BUDGET_EXCEEDED            │
├─────────────────────────────────────────────────────────────────────────────┤
│  6. 配置层                                                                   │
│     .env (DATABASE_URL / REDIS_ADDR / TEMPORAL_ADDRESS=127.0.0.1:17233 / LLM_SERVICE_URL)   │
│     config/features.yaml (feature flags)                                      │
│     config/budget.yaml                                                        │
├─────────────────────────────────────────────────────────────────────────────┤
│  7. 部署层                                                                   │
│     Docker Compose (Gateway / Temporal / PostgreSQL / Redis / Python LLM)   │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## 六、组件职责边界

| 组件                             | 技术栈               | 做什么                                              | 不做什么                              | Phase |
| -------------------------------- | -------------------- | --------------------------------------------------- | ------------------------------------- | ----- |
| Gateway                          | Go HTTP :8080        | 唯一 task 创建方、启动 Workflow、状态查询、SSE 推送 | 直接调用 LLM、做 Agent 推理           | 全部  |
| Temporal                         | Temporal             | Workflow 执行事实来源、Activity 调度                | 直接操作 DB/Redis                     | 全部  |
| Go Worker                        | Go                   | 注册 Workflow/Activity、执行 DAGWorkflow/MultiAgent | Workflow 直接 HTTP/DB/Redis           | 全部  |
| Workflow Router                  | Go                   | 根据 config.mode 路由到对应 Workflow                | 直接执行 agent 逻辑                   | 3A+   |
| SimpleWorkflow                   | Go                   | 单 Agent 执行（Phase 2 已完成）                      | 直接 IO                               | 2 已完成 |
| DAGWorkflow Lite                 | Go                   | 顺序执行 DAG nodes、Synthesis 合并结果              | 并发 DAG、动态重规划                  | 3A     |
| MultiAgentWorkflow Lite          | Go                   | 顺序执行 planner→worker→critic→synthesizer          | P2P、swarm、动态 handoff              | 3B     |
| EmitEventActivity                | Go                   | 写事件到 Redis Stream                               | 阻断主流程                            | 全部  |
| LoadSessionActivity              | Go                   | 读取 Redis session:{session_id}:messages            | 写操作                                | 2 已完成 |
| SaveSessionActivity              | Go                   | 写 Redis session messages（LTRIM 50，TTL 7d）       | Postgres session_messages（Phase 4+） | 2 已完成 |
| EstimatePromptTokensActivity     | Go                   | 估算 prompt tokens（len/4 MVP）                     | 真实 tokenizer                        | 2 已完成 |
| CheckBudgetActivity              | Go                   | 检查 estimated > max_total → budget_exceeded        | 模型 fallback                         | 2 已完成 |
| AgentActivity                    | Go                   | 调用 Python LLM Service（含 role/node_id 参数）     | 直接调 LLM provider                   | 全部  |
| RecordUsageActivity              | Go                   | 写 llm_calls 到 Postgres（含 node_id/agent_role）   | 业务决策                              | 全部  |
| ClassifyTaskActivity             | Go                   | 估算任务复杂度（MVP 简单规则）                      | 复杂 ML 分类                          | 3A     |
| PlanDAGActivity                  | Go                   | 生成固定简单 DAG（2-3 nodes）                       | 复杂 DAG 规划                         | 3A     |
| SynthesisActivity                | Go                   | 合并 DAG node results 生成最终 answer               | 多轮迭代 synthesis                    | 3A     |
| ToolActivity                     | Go                   | 执行 safe tool（calculator/echo）                   | 任意代码执行、sandbox                 | 3C     |
| SaveResultActivity               | Go                   | 更新 task result 到 Postgres                        | -                                     | 全部  |
| RecordExecutionCompletedActivity | Go                   | 更新 execution status=completed                     | -                                     | 全部  |
| SaveFailureActivity              | Go                   | 记录 task 失败信息                                  | -                                     | 全部  |
| RecordExecutionFailedActivity    | Go                   | 更新 execution status=failed                        | -                                     | 全部  |
| Python LLM Service               | Python FastAPI :8000 | LLM 调用（mock/openai_compatible）、role-aware      | 任务编排、状态管理、workflow 调度     | 全部  |
| PostgreSQL                       | PostgreSQL :5432     | tasks/executions/llm_calls 持久化                   | 向量检索、短期缓存                    | 全部  |
| Redis                            | Redis :6379          | session cache（必做）、task status、events stream   | 长期存储、事实来源                    | 全部  |
| Config System                    | Go + YAML            | 加载配置、feature flag                              | 运行时修改                            | 全部  |

---

## 七、核心数据流

### 1. Submit Task（Gateway 唯一创建）

```
Client
  → POST /api/v1/tasks {query, session_id, config: {mode, model, max_total_tokens, ...}}
  → Gateway validates request
  → Gateway generates task_id (UUID)
  → Gateway inserts tasks(status=pending, workflow_id="task-"+task_id, ...)
  → Gateway starts Temporal Workflow (sync)
  → Temporal returns run_id
  → Gateway updates tasks(status=running, run_id)
  → Gateway inserts executions(status=running)
  → Gateway caches task status in Redis (TTL=1d)
  → Gateway writes TASK_CREATED event to Redis Stream
  → Gateway returns {task_id, workflow_id, run_id, status:"running", stream_url}
```

### 2. Workflow Routing

**方案选择：Workflow Router**

Gateway 启动 Workflow 时传入 TaskRequest，其中包含 config.mode。

Temporal Worker 注册多个 Workflow 类型：
- `SimpleWorkflow` — 默认，单 agent 执行
- `DAGWorkflow` — DAG 模式
- `MultiAgentWorkflow` — 多 agent 模式

Worker 内部通过 Workflow Router 根据 TaskRequest.config.mode 选择执行哪个 Workflow。

```
Gateway starts Temporal Workflow with TaskRequest:
  - task_id / query / session_id / config.mode / config.model / ...

Temporal Worker receives TaskRequest:
  → config.mode == "simple" or nil  → SimpleWorkflow
  → config.mode == "dag"           → DAGWorkflow
  → config.mode == "multi_agent"   → MultiAgentWorkflow
  → config.mode == nil/""          → Default: SimpleWorkflow
```

**注意：Gateway 不做 Workflow 类型判断，只把 config.mode 传给 Temporal。Workflow 类型选择由 Worker 的 Workflow Router 完成。**

### 3. Simple Agent Execution（Phase 2 已完成）

```
SimpleWorkflow
  → EmitEventActivity(WORKFLOW_STARTED)
  → LoadSessionActivity(session_id)
    → Redis: LRANGE session:{session_id}:messages 0 49
  → EstimatePromptTokensActivity(query, model)
    → MVP: ceil(len(query)/4)
  → CheckBudgetActivity(estimated_prompt, max_total, max_completion)
    → If estimated > max_total: return budget_exceeded
    → Else: allowed_completion = min(max_completion, max_total - estimated)
  → EmitEventActivity(SESSION_LOADED, {message_count: N})
  → EmitEventActivity(LLM_STARTED, {model: xxx})
  → AgentActivity(task_id, query, session_messages, model, role=nil, allowed_completion)
    → Python LLM Service POST /chat
    → return {content, usage, latency_ms, finish_reason}
  → EmitEventActivity(LLM_COMPLETED, {usage, latency_ms, finish_reason})
  → RecordUsageActivity(task_id, usage, latency_ms)
    → Postgres: llm_calls
  → SaveSessionActivity(session_id, user_msg, assistant_msg)
    → Redis: RPUSH session:{session_id}:messages JSON, LTRIM 0 49, EXPIRE 604800
  → SaveResultActivity(task_id, result, usage)
    → Postgres: tasks.result, tasks.usage_*
    → Redis: task:{task_id}:status
  → RecordExecutionCompletedActivity(task_id, execution_id)
    → Postgres: executions.status=completed
  → EmitEventActivity(TASK_COMPLETED)

失败路径（CheckBudgetActivity budget_exceeded）：
  → SaveFailureActivity(task_id, "budget_exceeded", "estimated > max_total")
  → RecordExecutionFailedActivity(execution_id, "budget_exceeded")
  → EmitEventActivity(TASK_BUDGET_EXCEEDED)

失败路径（AgentActivity error）：
  → SaveFailureActivity(task_id, "llm_error", error)
  → RecordExecutionFailedActivity(execution_id, "llm_error")
  → EmitEventActivity(TASK_FAILED)
```

### 4. DAG Execution

```
DAGWorkflow
  → EmitEventActivity(WORKFLOW_STARTED)
  → LoadSessionActivity(session_id)
  → CheckBudgetActivity(total_budget)  ← DAG 开始前检查总 budget
  → ClassifyTaskActivity(query)  ← 注意：Complexity 判断在 Activity，不在 Workflow
    → MVP: if len(query) > 200 chars → complexity=medium else low
    → return complexity string ("low" | "medium")
  → PlanDAGActivity(query, complexity)
    → low: return {nodes: [{node_id:"n1",prompt:"分解问题"},{node_id:"n2",prompt:"回答"}]}
    → medium: return {nodes: [n1,n2,n3]}
  → EmitEventActivity(DAG_PLANNED, {nodes: [...]})
  → For node in topological order:
      → EmitEventActivity(DAG_NODE_STARTED, {node_id})
      → AgentActivity(task_id, node.prompt, session_messages, model, role=nil, node_id=node.node_id)
        → 每个 node 调用 RecordUsageActivity 时写入 node_id
        → llm_calls 表记录该 node 的 usage
      → EmitEventActivity(DAG_NODE_COMPLETED, {node_id, result})
  → SynthesisActivity(node_results)
    → Merge DAG node outputs into final answer
    → 汇总所有 node usage 到 tasks 表
  → EmitEventActivity(DAG_SYNTHESIZED, {final_answer})
  → SaveResultActivity → TASK_COMPLETED
```

**DAG Usage 汇总：**

- 每个 DAG node 执行时，RecordUsageActivity 写入 llm_calls 表
- llm_calls.node_id 字段记录所属 node
- DAG 完成后，SynthesisActivity 汇总：
  - tasks.usage_prompt_tokens = sum(所有 node 的 prompt_tokens)
  - tasks.usage_completion_tokens = sum(所有 node 的 completion_tokens)
  - tasks.usage_total_tokens = sum(所有 node 的 total_tokens)

**注意：Complexity 判断在 ClassifyTaskActivity 中，不在 DAGWorkflow 中。DAGWorkflow 只接收 complexity 结果。**

### 5. Multi-Agent Execution

```
MultiAgentWorkflow
  → EmitEventActivity(WORKFLOW_STARTED)
  → LoadSessionActivity(session_id)
  → EmitEventActivity(AGENT_STARTED, {role: "planner"})
  → AgentActivity(task_id, query, [], model, role="planner")
    → planner generates plan (plain text or simple parseable format)
    → Phase 3B 初版接受 plain text plan，不强依赖 JSON parse
  → EmitEventActivity(AGENT_COMPLETED, {role: "planner", output: plan})
  → EmitEventActivity(AGENT_STARTED, {role: "worker"})
  → AgentActivity(task_id, subtask1, [], model, role="worker")
    → worker produces answer for subtask1
  → EmitEventActivity(AGENT_COMPLETED, {role: "worker", output: answer})
  → EmitEventActivity(AGENT_STARTED, {role: "critic"})
  → AgentActivity(task_id, worker_answer, [], model, role="critic")
    → critic reviews and gives critique / suggestions
  → EmitEventActivity(CRITIC_REVIEWED, {critique: xxx})
  → EmitEventActivity(AGENT_STARTED, {role: "synthesizer"})
  → AgentActivity(task_id, worker_answer + critique, [], model, role="synthesizer")
    → synthesizer produces final answer
  → EmitEventActivity(SYNTHESIS_COMPLETED, {final_answer: xxx})
  → SaveResultActivity → TASK_COMPLETED
```

**注意：Planner 输出不强制要求 JSON parse。初版用 plain text，接受 fallback parse。**

### 6. Tool Call

```
Tool call triggered by deterministic rule:
  - config.enable_tools = true AND query matches explicit pattern (contains "calculate" / "calculator" / numeric expression like "2+3")
  - OR future ToolDecisionActivity decides

ToolActivity({tool_name: "calculator", arguments: {"expr": "2+3"}})
  → Execute safe tool in Go (whitelist arithmetic parser: digits, spaces, + - * / ( ), decimal point)
  → TOOL_STARTED
  → TOOL_COMPLETED {output: "5", latency_ms: 1}
  → Tool result injected into prompt for next LLM call
```

**注意：Tool 调用由确定性规则触发，不依赖模型"自觉调用 tool"。Phase 3C 只允许 calculator / echo，禁止任意代码执行。**

### 7. Query Task

```
Client
  → GET /api/v1/tasks/{task_id}
  → Gateway reads Redis first (task:{task_id}:status)
  → fallback Postgres (tasks table)
  → return task with status/result/error/workflow_id/run_id/usage
```

### 8. Stream Events

```
Client
  → GET /api/v1/stream/sse?task_id=xxx
  → Gateway reads Redis Stream from position 0
  → blocks waiting for new events
  → pushes text/event-stream
  → on TASK_COMPLETED/TASK_FAILED/TASK_BUDGET_EXCEEDED: close connection
```

---

## 八、API 设计

### 已有 API（向后兼容）

**POST /api/v1/tasks** — 原请求继续有效，mode 默认 "simple"

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

**Phase 3 扩展 config 字段（向后兼容，不传则使用默认值）：**

```json
{
  "query": "Summarize the key points of this article...",
  "session_id": "my-session-123",
  "config": {
    "mode": "dag",
    "model": "gpt-4o-mini",
    "temperature": 0.7,
    "max_total_tokens": 8000,
    "max_completion_tokens": 1024,
    "enable_tools": false,
    "agent_roles": ["planner", "worker", "critic", "synthesizer"]
  }
}
```

| 字段                  | Phase 3 必做       | 说明                                          |
| --------------------- | ------------------ | --------------------------------------------- |
| mode                  | 是                 | "simple" / "dag" / "multi_agent"，默认 simple |
| model                 | 是                 | 模型名                                        |
| temperature           | 是                 | 温度                                          |
| max_total_tokens      | 是                 | token 预算                                    |
| max_completion_tokens | 是                 | 输出上限                                      |
| enable_tools          | 否（可解析不启用） | 是否启用 tool calling Phase 3C                |
| agent_roles           | 否（可解析不启用） | multi_agent 模式角色列表 Phase 3B             |

**GET /api/v1/tasks/{task_id}** — 响应扩展

```json
{
  "task_id": "uuid-xxx",
  "workflow_id": "task-uuid-xxx",
  "run_id": "run-id-xxx",
  "session_id": "my-session-123",
  "status": "completed",
  "mode": "dag",
  "result": "Synthesized final answer...",
  "error": null,
  "error_type": null,
  "usage": {
    "prompt_tokens": 120,
    "completion_tokens": 80,
    "total_tokens": 200
  },
  "model": "gpt-4o-mini",
  "max_total_tokens": 8000,
  "max_completion_tokens": 1024,
  "created_at": "2026-05-18T10:00:00Z",
  "updated_at": "2026-05-18T10:00:05Z"
}
```

**GET /api/v1/stream/sse?task_id=xxx** — Phase 3 新增事件

```
event: TASK_CREATED
data: {"task_id":"xxx","timestamp":"..."}

event: WORKFLOW_STARTED
data: {"task_id":"xxx","workflow_id":"xxx","run_id":"xxx","timestamp":"..."}

event: SESSION_LOADED
data: {"task_id":"xxx","message_count":5,"timestamp":"..."}

event: LLM_STARTED
data: {"task_id":"xxx","model":"gpt-4o-mini","timestamp":"..."}

event: LLM_COMPLETED
data: {"task_id":"xxx","usage":{"prompt_tokens":20,"completion_tokens":15,"total_tokens":35},"latency_ms":1200,"finish_reason":"stop","timestamp":"..."}

event: USAGE_RECORDED
data: {"task_id":"xxx","total_tokens":35,"timestamp":"..."}

event: DAG_PLANNED
data: {"task_id":"xxx","nodes":[{"node_id":"n1","depends_on":[]},{"node_id":"n2","depends_on":["n1"]}],"timestamp":"..."}

event: DAG_NODE_STARTED
data: {"task_id":"xxx","node_id":"n1","timestamp":"..."}

event: DAG_NODE_COMPLETED
data: {"task_id":"xxx","node_id":"n1","result":"node output","timestamp":"..."}

event: DAG_SYNTHESIZED
data: {"task_id":"xxx","final_answer":"synthesized answer","timestamp":"..."}

event: AGENT_STARTED
data: {"task_id":"xxx","role":"planner","timestamp":"..."}

event: AGENT_COMPLETED
data: {"task_id":"xxx","role":"planner","timestamp":"..."}

event: CRITIC_REVIEWED
data: {"task_id":"xxx","critique":"some suggestions","timestamp":"..."}

event: SYNTHESIS_COMPLETED
data: {"task_id":"xxx","final_answer":"final synthesized answer","timestamp":"..."}

event: TOOL_STARTED
data: {"task_id":"xxx","tool_name":"calculator","arguments":{"expr":"2+3"},"timestamp":"..."}

event: TOOL_COMPLETED
data: {"task_id":"xxx","tool_name":"calculator","output":"5","latency_ms":1,"timestamp":"..."}

event: TASK_COMPLETED
data: {"task_id":"xxx","status":"completed","timestamp":"..."}

终态事件后关闭 SSE 连接
```

**GET /health** — 保持不变

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

---

## 九、Workflow 设计

### SimpleWorkflow（Phase 2 已完成）

**输入：TaskRequest**

```go
type TaskRequest struct {
    TaskID              string
    Query               string
    SessionID           string
    Model               string
    Temperature         float64
    MaxTotalTokens      int
    MaxCompletionTokens int
    WorkflowID          string
    RunID               string
}
```

**输出：TaskResult**

```go
type TaskResult struct {
    TaskID   string
    Status   string  // "completed", "failed", "budget_exceeded"
    Answer   string
    Usage    *Usage
    Error    string
    ErrorType string
}
```

**Activity 清单（Phase 2 已完成）：**

| Activity                         | 职责                            | Timeout | Retry Policy | 幂等性要求 | Phase |
| -------------------------------- | ------------------------------- | ------- | ------------ | ---------- | ----- |
| EmitEventActivity                | 写事件到 Redis Stream           | 5s      | NoRetry      | N/A        | 全部  |
| LoadSessionActivity              | 读取 Redis session messages     | 10s     | Retry3x      | 是         | 2 已完成 |
| EstimatePromptTokensActivity     | 估算 prompt tokens (len/4)      | 5s      | NoRetry      | N/A        | 2 已完成 |
| CheckBudgetActivity              | 检查 max_total/max_completion   | 5s      | NoRetry      | N/A        | 2 已完成 |
| AgentActivity                    | 调用 Python LLM Service         | 60s     | Retry2x      | 是         | 全部  |
| RecordUsageActivity              | 写 llm_calls 到 Postgres        | 10s     | Retry3x      | 是         | 全部  |
| SaveSessionActivity              | 写 session 到 Redis（LTRIM 50） | 10s     | Retry3x      | 是         | 2 已完成 |
| SaveResultActivity               | 更新 task result 到 Postgres    | 10s     | Retry3x      | 是         | 全部  |
| RecordExecutionCompletedActivity | 更新 execution status=completed | 10s     | Retry3x      | 是         | 全部  |
| SaveFailureActivity              | 记录 task 失败信息              | 10s     | Retry3x      | 是         | 全部  |
| RecordExecutionFailedActivity    | 更新 execution status=failed    | 10s     | Retry3x      | 是         | 全部  |

**关键约束：**

- Workflow 内只写确定性逻辑
- Workflow 不直接 HTTP / DB / Redis，所有外部 IO 都放 Activity
- EmitEventActivity 失败只记录日志，不阻断主流程
- 数据库写入 Activity 必须幂等（使用 UPDATE WHERE status='running'）

---

### DAGWorkflow Lite（Phase 3A 新增）

**输入：TaskRequest（同上）**

**输出：TaskResult（同上）**

**Activity 清单（新增 + 复用）：**

| Activity              | 职责                          | Timeout | Retry Policy | 幂等性要求 | Phase 3A |
| --------------------- | ----------------------------- | ------- | ------------ | ---------- | -------- |
| ClassifyTaskActivity  | 估算任务复杂度（简单规则）    | 5s      | NoRetry      | N/A        | 是       |
| PlanDAGActivity       | 生成固定简单 DAG（2-3 nodes） | 10s     | Retry3x      | 是         | 是       |
| EmitEventActivity     | 写 DAG 事件到 Redis Stream    | 5s      | NoRetry      | N/A        | 是       |
| AgentActivity（复用） | 执行 DAG node（带 node_id）    | 60s     | Retry2x      | 是         | 是       |
| RecordUsageActivity（复用） | 写 llm_calls（含 node_id） | 10s     | Retry3x      | 是         | 是       |
| SynthesisActivity     | 合并 DAG node results + 汇总 usage | 30s | Retry3x      | 是         | 是       |
| SaveResultActivity    | 保存最终结果                  | 10s     | Retry3x      | 是         | 是       |

**DAG 表示（PlanDAGActivity 输出）：**

```go
type DAGNode struct {
    NodeID    string   `json:"node_id"`
    DependsOn []string `json:"depends_on"` // 空数组表示无依赖
    Prompt    string   `json:"prompt"`
    Role      string   `json:"role,omitempty"` // 可选：worker/critic/synthesizer
}

type DAGPlan struct {
    Nodes []DAGNode `json:"nodes"`
}
```

**Phase 3A MVP DAG 规则：**

- complexity == "low"：生成 2 个 node（n1=分解问题，n2=回答）
- complexity == "medium"：生成 3 个 node（n1=分解，n2=研究，n3=综合）
- 每个 node 顺序执行，不做并发
- DAG 保存在 Redis dag:{task_id}:nodes（TTL 1d），可选

**关键约束：**

- DAGWorkflow 内的 loop 是确定性的（for range over sorted nodes）
- 不使用 goroutine
- 不使用 time.Now()
- 不使用随机数

---

### MultiAgentWorkflow Lite（Phase 3B 新增）

**输入：TaskRequest（同上）**

**输出：TaskResult（同上）**

**Activity 清单（新增 + 复用）：**

| Activity                          | 职责                             | Timeout | Retry Policy | 幂等性要求 | Phase 3B |
| --------------------------------- | -------------------------------- | ------- | ------------ | ---------- | -------- |
| EmitEventActivity                 | 写 agent 事件到 Redis Stream     | 5s      | NoRetry      | N/A        | 是       |
| AgentActivity（role=planner）     | 生成 sub-task plan               | 60s     | Retry2x      | 是         | 是       |
| AgentActivity（role=worker）      | 执行 sub-task，生成答案          | 60s     | Retry2x      | 是         | 是       |
| AgentActivity（role=critic）      | 评审 worker 答案，给出 critique  | 60s     | Retry2x      | 是         | 是       |
| AgentActivity（role=synthesizer） | 综合 worker+critic，生成最终答案 | 60s     | Retry2x      | 是         | 是       |
| RecordUsageActivity（复用）      | 写 llm_calls（含 agent_role）   | 10s     | Retry3x      | 是         | 是       |
| SaveResultActivity                | 保存最终结果                     | 10s     | Retry3x      | 是         | 是       |

**Agent role system prompt（Python LLM Service 使用）：**

```python
ROLE_PROMPTS = {
    "planner": "You are a planner agent. Break down the user's query into 2-3 sub-tasks. Return your plan as a JSON list of subtask descriptions.",
    "worker": "You are a worker agent. Execute the assigned sub-task and provide a detailed answer.",
    "critic": "You are a critic agent. Review the worker's answer and provide constructive feedback. Be specific about what is good and what needs improvement.",
    "synthesizer": "You are a synthesizer agent. Combine the worker's answer and the critic's feedback to produce a final, polished response."
}
```

**关键约束：**

- MultiAgentWorkflow 顺序执行 agent chain，不做并发
- 不做 agent 间 P2P 通信
- 不做 workspace 文件共享

---

## 十、Python LLM Service 设计

### 端口

`:8000`

### 模式策略

- **mock mode（默认）**：返回固定 mock answer + usage，不依赖 OpenAI API，不读取 OPENAI_API_KEY
- **openai_compatible mode（可选增强）**：Phase 3B/3C 后可启用，smoke test 默认跑 mock mode
- GET /health 不调真实 provider，只检查进程和配置
- Python LLM Service 不变 orchestrator，不负责 workflow 调度

### GET /health

```python
@app.get("/health")
async def health():
    return {"status": "healthy"}
```

### POST /chat

**LLMRequest（Phase 3 扩展）：**

```python
from pydantic import BaseModel
from typing import Optional, List, Dict, Any

class LLMRequest(BaseModel):
    trace_id: str
    task_id: str
    session_id: Optional[str] = None
    provider: str = "mock"  # "mock" | "openai_compatible"，默认 mock
    model: str = "gpt-4o-mini"
    messages: List[Dict[str, str]]  # [{"role": "user", "content": "..."}]
    temperature: float = 0.7
    max_completion_tokens: int = 1024
    role: Optional[str] = None  # "planner" | "worker" | "critic" | "synthesizer" | None
    node_id: Optional[str] = None  # DAG node ID，Phase 3A
    tools: Optional[List[Dict]] = None  # Phase 3C
    metadata: Optional[Dict[str, Any]] = None
```

**LLMResponse：**

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
    finish_reason: str  # "stop" | "length" | "error" | "tool_calls"
    provider_response_id: Optional[str] = None
    latency_ms: int
    error: Optional[str] = None
```

### mock mode 实现

```python
@app.post("/chat", response_model=LLMResponse)
async def chat(req: LLMRequest):
    start = time.time()

    if req.provider == "mock":
        # Phase 2+: 返回固定 mock answer + usage
        return LLMResponse(
            content=f"Mock answer for: {req.messages[-1]['content'][:50]}...",
            usage=Usage(prompt_tokens=20, completion_tokens=15, total_tokens=35),
            model=req.model,
            provider="mock",
            finish_reason="stop",
            latency_ms=int((time.time() - start) * 1000),
            error=None
        )

    elif req.provider == "openai_compatible":
        # Phase 3B/3C 后可选：不读 OPENAI_API_KEY，smoke test 默认 mock
        # use OpenAI SDK or httpx
        # pass role via system message injection
        pass

    else:
        return LLMResponse(
            content="",
            usage=Usage(prompt_tokens=0, completion_tokens=0, total_tokens=0),
            model=req.model,
            provider=req.provider,
            finish_reason="error",
            latency_ms=int((time.time() - start) * 1000),
            error=f"Unknown provider: {req.provider}"
        )
```

### role-aware system prompt injection

```python
ROLE_PROMPTS = {
    None: "You are a helpful assistant.",
    "planner": "You are a planner agent. Analyze the user's query and break it down into 2-3 sub-tasks. Output a plan.",
    "worker": "You are a worker agent. Execute the assigned task and provide a detailed answer.",
    "critic": "You are a critic agent. Review the given answer and provide constructive feedback.",
    "synthesizer": "You are a synthesizer agent. Combine multiple inputs into a final, polished response."
}

def build_messages(req: LLMRequest) -> List[Dict]:
    system_content = ROLE_PROMPTS.get(req.role, ROLE_PROMPTS[None])
    messages = [{"role": "system", "content": system_content}]
    messages.extend(req.messages)
    return messages
```

**Python LLM Service 职责边界：**

- 只做模型调用/模拟调用
- 不负责任务编排
- 不负责 workflow 调度
- 不负责 session 持久化
- 不负责 budget 最终裁决
- GET /health 不调真实 provider

---

## 十一、Event Streaming 设计

### AgentEvent 数据结构（不变）

```go
type AgentEvent struct {
    EventType string                 `json:"event_type"`
    Payload   map[string]interface{} `json:"payload"`
    CreatedAt string                 `json:"created_at"`
}
```

### Phase 2/3 完整事件清单

| 事件类型             | Payload                                              | 由谁写              | 终态 | SSE | 阻断主流程 |
| -------------------- | ---------------------------------------------------- | ------------------- | ---- | --- | ---------- |
| TASK_CREATED         | task_id, workflow_id, query, timestamp               | Gateway             | 否   | 是  | 否         |
| WORKFLOW_STARTED     | task_id, workflow_id, run_id, timestamp              | SimpleWorkflow/DAGWorkflow/MultiAgentWorkflow | 否   | 是  | 否         |
| SESSION_LOADED       | task_id, message_count, timestamp                    | LoadSessionActivity | 否   | 是  | 否         |
| LLM_STARTED          | task_id, model, timestamp                            | AgentActivity       | 否   | 是  | 否         |
| LLM_COMPLETED        | task_id, usage, latency_ms, finish_reason, timestamp | AgentActivity       | 否   | 是  | 否         |
| USAGE_RECORDED       | task_id, total_tokens, timestamp                     | RecordUsageActivity | 否   | 是  | 否         |
| TASK_COMPLETED       | task_id, status, result_length, timestamp            | Workflow            | 是   | 是  | 关闭 SSE   |
| TASK_FAILED          | task_id, error_type, message, timestamp              | Workflow            | 是   | 是  | 关闭 SSE   |
| TASK_BUDGET_EXCEEDED | task_id, estimated_prompt, max_total, timestamp      | CheckBudgetActivity | 是   | 是  | 关闭 SSE   |
| DAG_PLANNED          | task_id, nodes, timestamp                           | PlanDAGActivity     | 否   | 是  | 否         |
| DAG_NODE_STARTED     | task_id, node_id, timestamp                         | DAGWorkflow         | 否   | 是  | 否         |
| DAG_NODE_COMPLETED   | task_id, node_id, result, timestamp                 | DAGWorkflow         | 否   | 是  | 否         |
| DAG_SYNTHESIZED      | task_id, final_answer, timestamp                    | SynthesisActivity   | 否   | 是  | 否         |
| AGENT_STARTED        | task_id, role, timestamp                            | MultiAgentWorkflow  | 否   | 是  | 否         |
| AGENT_COMPLETED      | task_id, role, output, timestamp                    | MultiAgentWorkflow  | 否   | 是  | 否         |
| CRITIC_REVIEWED      | task_id, critique, timestamp                        | MultiAgentWorkflow  | 否   | 是  | 否         |
| SYNTHESIS_COMPLETED  | task_id, final_answer, timestamp                     | MultiAgentWorkflow  | 否   | 是  | 否         |
| TOOL_STARTED         | task_id, tool_name, arguments, timestamp            | ToolActivity        | 否   | 是  | 否         |
| TOOL_COMPLETED       | task_id, tool_name, output, latency_ms, timestamp   | ToolActivity        | 否   | 是  | 否         |
| TOOL_FAILED          | task_id, tool_name, error, timestamp               | ToolActivity        | 否   | 是  | 否         |

### EmitEventActivity 实现（不变）

```go
func EmitEventActivity(ctx context.Context, event *AgentEvent) error {
    data, _ := json.Marshal(event)
    err := redis.XAdd(ctx, "task:"+event.TaskID+":events", "*", map[string]interface{}{
        "event_type": event.Type,
        "payload":    string(data),
        "created_at": event.CreatedAt.Format(time.RFC3339),
    }).Err()
    if err != nil {
        log.Printf("EmitEventActivity XAdd failed: %v", err)
    }
    return nil
}
```

---

## 十二、Redis 设计

### Key Pattern 表

| Key Pattern                     | 类型   | 内容                         | TTL  | MAXLEN/LTRIM | Phase |
| ------------------------------- | ------ | ---------------------------- | ---- | ------------ | ----- |
| `session:{session_id}:messages` | List   | 最近 50 条消息 JSON          | 7 天 | LTRIM 0 49   | 全部  |
| `task:{task_id}:status`         | Hash   | status, progress, updated_at | 1 天 | —            | 全部  |
| `task:{task_id}:events`         | Stream | AgentEvent 列表              | 1 天 | MAXLEN 200   | 全部  |
| `dag:{task_id}:nodes`          | String | DAG plan JSON（可选）        | 1 天 | —            | 3A    |
| `agent:{task_id}:trace`        | String | agent run trace JSON（可选） | 1 天 | —            | 3B（可选） |

### 数据结构示例

**session:{session_id}:messages：**

```
RPUSH session:sess-123:messages '{"role":"user","content":"Hello","created_at":"2026-05-18T10:00:00Z"}'
RPUSH session:sess-123:messages '{"role":"assistant","content":"Hi","created_at":"2026-05-18T10:00:01Z"}'
LTRIM session:sess-123:messages 0 49
EXPIRE session:sess-123:messages 604800
```

**task:{task_id}:status：**

```
HSET task:uuid-xxx status "running" progress "50%" updated_at "2026-05-18T10:00:03Z"
EXPIRE task:uuid-xxx:status 86400
```

**task:{task_id}:events：**

```
XADD task:uuid-xxx:events * event_type "LLM_COMPLETED" payload '{"usage":{"total_tokens":35}}' created_at "2026-05-18T10:00:03Z"
XTRIM task:uuid-xxx:events MAXLEN 200
EXPIRE task:uuid-xxx:events 86400
```

### 说明

- Redis 是短期缓存和事件缓冲
- 最终任务结果写 Postgres
- EmitEventActivity 失败只记录日志，不 fallback Postgres
- session messages 必须写（Phase 2 必做）
- dag nodes 可选（Phase 3A 可跳过）

---

## 十三、PostgreSQL 设计

### 现有表结构（Phase 1 已创建）

**tasks / executions / llm_calls / session_messages** — Phase 1 已创建，继续使用。

**Phase 2 已完成 llm_calls 实际写入。**

### Phase 3 最小 Migration

```sql
-- tasks 表新增 mode 字段（Phase 3 必须）
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS mode VARCHAR(30) DEFAULT 'simple';

-- llm_calls 表新增 agent_role / node_id 字段（Phase 3A/3B 必须）
ALTER TABLE llm_calls ADD COLUMN IF NOT EXISTS agent_role VARCHAR(50);
ALTER TABLE llm_calls ADD COLUMN IF NOT EXISTS node_id VARCHAR(50);
```

### budget_exceeded 状态模型（统一）

```
tasks.status = 'budget_exceeded'
tasks.error_type = 'budget_exceeded'
executions.status = 'failed'  ← 注意：executions.status 不新增 budget_exceeded，保持 running/completed/failed/cancelled
executions.error_type = 'budget_exceeded'
Redis task:{task_id}:status = 'budget_exceeded'
Redis Stream 写 TASK_BUDGET_EXCEEDED 事件
SSE 收到 TASK_BUDGET_EXCEEDED 后关闭连接
```

### 状态模型（统一）

Task Status：pending → running → completed / failed / budget_exceeded

Executions Status：running → completed / failed（**不新增 budget_exceeded**，失败统一用 failed）

终态不能被旧状态覆盖：`UPDATE tasks SET status='completed' WHERE id=$1 AND status='running'`

### Phase 3 是否需要新增表

**建议：不新增表，Phase 3 够用。**

理由：
- DAG node results 可存入 Redis dag:{task_id}:nodes（JSON）
- Multi-agent agent runs 可复用 llm_calls（每条记录 agent_role 字段）
- tool_calls 可复用 llm_calls（tool_name + arguments_json 字段）
- Postgres 不存 DAG plan，只存最终结果

**Phase 3 可选扩展（不强制）：**

若后续需要 Postgres 持久化 agent trace，可新增：

```sql
-- agent_runs 表（Phase 3 可选）
CREATE TABLE agent_runs (
    id UUID PRIMARY KEY,
    task_id UUID NOT NULL REFERENCES tasks(id),
    workflow_id VARCHAR(255) NOT NULL,
    run_id VARCHAR(255) NOT NULL,
    role VARCHAR(50),  -- planner/worker/critic/synthesizer
    node_id VARCHAR(50),  -- DAG node ID
    model VARCHAR(50),
    status VARCHAR(20) DEFAULT 'running',
    input_tokens INT,
    output_tokens INT,
    total_tokens INT,
    latency_ms BIGINT,
    output_text TEXT,
    error TEXT,
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX idx_agent_runs_task_id ON agent_runs(task_id);
```

---

## 十四、Budget 设计

### Budget 检查流程（Phase 2 已启用）

```
1. Gateway 接收 max_total_tokens / max_completion_tokens（不传则使用 config 默认值）
2. SimpleWorkflow 调 EstimatePromptTokensActivity
   - MVP: ceil(len(prompt) / 4)
3. SimpleWorkflow 调 CheckBudgetActivity
   - 若 estimated_prompt_tokens > max_total_tokens
     → return {Allowed: false, Reason: "estimated > max_total"}
   - 否则
     → allowed_completion = min(max_completion_tokens, max_total_tokens - estimated_prompt_tokens)
     → return {Allowed: true, AllowedCompletionTokens: allowed}
4. AgentActivity 调 LLM 时传 allowed_completion_tokens
5. RecordUsageActivity 写 llm_calls
6. SimpleWorkflow 检查 actual_total_tokens > max_total_tokens
   - 若超限：标记 budget_exceeded
   - 若未超限：标记 completed
```

### DAG Budget 支持（Phase 3A 必须）

DAGWorkflow 必须支持 budget 检查：
- 在 DAG 开始前，CheckBudgetActivity 检查总 budget
- 可选：每个 node 的 allowed_completion_tokens 考虑剩余 budget
- 若任一 node 超预算，记录事件但继续执行 DAG
- DAG 完成后，SynthesisActivity 汇总所有 node 的 usage 到 tasks 表

### Budget 配置（config/budget.yaml）

```yaml
budget:
  default_max_total_tokens_per_task: 8000
  default_max_completion_tokens: 1024
  default_model: gpt-4o-mini
  allow_override: true
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
        AllowedCompletionTokens: allowed,
    }, nil
}
```

---

## 十五、Session Memory 设计

### Redis List 结构（Phase 2 已完成）

**Key:** `session:{session_id}:messages`

**写入：** SaveSessionActivity

```
RPUSH session:{session_id}:messages '{"role":"user","content":"query","created_at":"2026-05-18T10:00:00Z"}'
RPUSH session:{session_id}:messages '{"role":"assistant","content":"answer","created_at":"2026-05-18T10:00:01Z"}'
LTRIM session:{session_id}:messages 0 49
EXPIRE session:{session_id}:messages 604800
```

**读取：** LoadSessionActivity

```
LRANGE session:{session_id}:messages 0 49
```

返回 message 列表，供 AgentActivity 注入到 LLM messages。

### TTL 策略

- session:{session_id}:messages：7 天（604800 秒）
- 若 session 持续活跃，TTL 每次写操作后 refresh

### Postgres session_messages

**Phase 3 暂不写入。** Phase 1 已建表但 feature flag 控制：

```yaml
features:
  enable_session_postgres: false # Phase 3 保持 false
```

Phase 4+ 可开启，此时 SaveSessionActivity 同时写 Redis（必做）+ Postgres（可选）。

---

## 十六、Tool Abstraction Lite 设计

### ToolCall 数据结构

```go
type ToolCall struct {
    ToolName   string                 `json:"tool_name"`
    Arguments  map[string]interface{} `json:"arguments"`
}
```

### ToolResult 数据结构

```go
type ToolResult struct {
    ToolName  string `json:"tool_name"`
    Output    string `json:"output"`
    Error     string `json:"error,omitempty"`
    LatencyMs int    `json:"latency_ms"`
}
```

### ToolActivity 实现（Phase 3C：calculator / echo）

```go
type ToolInput struct {
    TaskID     string
    ToolName   string
    Arguments  map[string]interface{}
}

func (a *ToolActivities) ExecuteTool(ctx context.Context, input ToolInput) (*ToolResult, error) {
    logger := activity.GetLogger(ctx)
    start := time.Now()

    var output string
    var err error

    switch input.ToolName {
    case "calculator":
        expr, ok := input.Arguments["expr"].(string)
        if !ok {
            return &ToolResult{
                ToolName: input.ToolName,
                Error:    "invalid arguments: 'expr' string required",
            }, nil
        }
        output, err = evaluateArithmetic(expr) // 安全 Eval，无 Eval()，纯字符串解析
        if err != nil {
            return &ToolResult{
                ToolName: input.ToolName,
                Error:    err.Error(),
            }, nil
        }

    case "echo":
        msg, ok := input.Arguments["message"].(string)
        if !ok {
            return &ToolResult{
                ToolName: input.ToolName,
                Error:    "invalid arguments: 'message' string required",
            }, nil
        }
        output = msg

    default:
        return &ToolResult{
            ToolName: input.ToolName,
            Error:    fmt.Sprintf("unknown tool: %s", input.ToolName),
        }, nil
    }

    return &ToolResult{
        ToolName:  input.ToolName,
        Output:    output,
        LatencyMs: int(time.Since(start).Milliseconds()),
    }, nil
}
```

### 安全算术解析器（白名单，无 eval）

```go
// 白名单解析器：只接受数字、空格、+ - * / ( ) 和小数点
// 不使用 eval()，纯字符串 token 解析
// 支持：1+2, 3*4, (5+3)/2, 10.5-2.1
// 不支持：函数调用、变量、赋值、任何代码执行
func evaluateArithmetic(expr string) (string, error) {
    // 手动 token 解析：scan digits, operators, parentheses, decimal point
    // 拒绝任何非白名单字符（字母、符号等）
    // 若解析失败返回 error，不尝试修复
    val, err := parseAndEvaluate(expr)
    if err != nil {
        return "", err
    }
    return fmt.Sprintf("%v", val), nil
}
```

**禁止使用 eval()、os/exec、任何能执行任意代码的方式。**

### Phase 3C 不做

- 不实现 MCP 协议
- 不实现 sandbox / WASI / Firecracker
- 不执行任意代码（calculator 纯字符串解析，无 eval）
- 不做远程 tool（web_search 等 Phase 4+）

---

## 十七、Docker / Config 设计

### Docker Compose（Phase 3 扩展）

Phase 3 新增服务：

```yaml
services:
  # 现有服务保持不变
  gateway:
    build: ../.
    ports:
      - "8080:8080"
    environment:
      - DATABASE_URL=postgres://admin:admin@postgres:5432/orchestrator
      - REDIS_ADDR=redis:6379
      - TEMPORAL_ADDRESS=temporal:7233
      - LLM_SERVICE_URL=http://python-llm:8000
      - LLM_MODE=mock # mock | openai_compatible
    depends_on:
      postgres:
        condition: service_healthy
      redis:
        condition: service_healthy
      temporal:
        condition: service_healthy

  python-llm:
    build: ../python_llm_service
    ports:
      - "8000:8000"
    environment:
      - LLM_MODE=mock # mock | openai_compatible
      - OPENAI_API_KEY=${OPENAI_API_KEY:-}
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8000/health"]

  worker:
    build: ../.
    command: worker
    environment:
      - DATABASE_URL=postgres://admin:admin@postgres:5432/orchestrator
      - REDIS_ADDR=redis:6379
      - TEMPORAL_ADDRESS=temporal:7233
      - LLM_SERVICE_URL=http://python-llm:8000
    depends_on:
      postgres:
        condition: service_healthy
      redis:
        condition: service_healthy
      temporal:
        condition: service_healthy
```

### 本地开发环境变量

```bash
# .env
DATABASE_URL=postgres://admin:admin@localhost:5432/orchestrator
REDIS_ADDR=127.0.0.1:6379
REDIS_PASSWORD=
REDIS_DB=0
TEMPORAL_ADDRESS=127.0.0.1:17233
LLM_SERVICE_URL=http://127.0.0.1:8000
LLM_MODE=mock  # mock | openai_compatible
OPENAI_API_KEY=sk-xxx  # only needed when LLM_MODE=openai_compatible
```

**重要：本地 WSL 环境下，TEMPORAL_ADDRESS 必须是 127.0.0.1:17233，不是 127.0.0.1:7233。**

### config/features.yaml

```yaml
features:
  enable_sse: true
  enable_budget: true
  enable_session_memory: true
  enable_session_postgres: false  # Phase 3: Redis only, Phase 4+ Postgres

  # Phase 3 features default off — slice 完成后再打开
  enable_dag_workflow: false    # Slice 4 完成后打开
  enable_multi_agent: false     # Slice 5 完成后打开
  enable_tools: false           # Slice 6 完成后打开
```

### Feature Flag 强制策略

当 feature 未启用时：
- 若 request config.mode=dag，但 enable_dag_workflow=false → 返回 **400 validation_error**，message 说明功能未启用
- 若 request config.mode=multi_agent，但 enable_multi_agent=false → 返回 **400 validation_error**
- 若 request config.enable_tools=true，但 enable_tools=false → 返回 **400 validation_error**

**不要 silent fallback**。让客户端明确知道功能未就绪。

### config/budget.yaml（不变）

```yaml
budget:
  default_max_total_tokens_per_task: 8000
  default_max_completion_tokens: 1024
  default_model: gpt-4o-mini
  allow_override: true
```

---

## 十八、实施计划

> 注意：Phase 2A / 2B / 2C 的 Slice 1/2/3 内容仅作为历史验收记录保留，不再作为当前执行任务。当前只执行 Phase 3A Slice 4.1。

### 执行原则

- 任何 coding agent prompt 一次只执行一个 Slice
- **当前只执行 Phase 3A Slice 4.1**：mode 字段 + feature flag + validation
- Phase 2A/2B/2C 已完成，只作为历史记录保留，不回头重做
- Slice 4.1 不实现 DAGWorkflow，只做请求解析、feature flag 校验和 validation_error 返回
- 每个 Slice 必须可独立验收（验收命令 + Pass 标准）
- 完成后才能进入下一个 Slice

---

### Phase 2A / 2B / 2C — Slice 1/2/3（已完成）

✅ **Phase 2 = Single Agent Runtime 已完成。**

所有 Phase 2 功能已实现并通过 smoke_test.sh：
- SimpleWorkflow → AgentActivity → Python LLM mock → result
- llm_calls 实际写入
- Budget 检查生效
- Session Memory 工作
- SSE 事件完整

---

### Phase 3A — Slice 4: DAGWorkflow Lite

**前置条件：Phase 2 已完成并验收通过，enable_dag_workflow=true**

**目标：**
- config.mode="dag" 且 enable_dag_workflow=true 时进入 DAGWorkflow（否则 400 validation_error）
- ClassifyTaskActivity 估算复杂度
- PlanDAGActivity 生成 2-3 nodes 简单 DAG
- 顺序执行 DAG nodes，每个 node 的 usage 写入 llm_calls（含 node_id）
- DAG 完成后汇总 usage 到 tasks 表
- DAG 模式必须继续支持 budget 检查
- SynthesisActivity 合并结果
- DAG 事件完整

**修改文件：**
- `internal/workflows/dag.go` — 新建 DAGWorkflow
- `internal/workflows/router.go` — 新建 Workflow Router，根据 config.mode + feature flag 路由
- `internal/activities/classify.go` — 新建 ClassifyTaskActivity（复杂度估算）
- `internal/activities/planner.go` — 新建 PlanDAGActivity（DAG 生成）
- `internal/activities/synthesis.go` — 新建 SynthesisActivity（DAG 结果合并 + usage 汇总）
- `internal/events/types.go` — 新增 DAG_PLANNED / DAG_NODE_STARTED / DAG_NODE_COMPLETED / DAG_SYNTHESIZED 事件

**新增 Migration：**
```sql
-- llm_calls 表新增 node_id 字段
ALTER TABLE llm_calls ADD COLUMN IF NOT EXISTS node_id VARCHAR(50);
```

**不允许修改的文件：**
- `internal/workflows/simple.go`

**数据流：**

```
DAGWorkflow(task_id, query, session_id, ...)
  → EmitEventActivity(WORKFLOW_STARTED)
  → LoadSessionActivity(session_id)
  → CheckBudgetActivity(total_budget)  ← DAG 开始前检查
  → ClassifyTaskActivity(query)  ← Activity 中判断 complexity
    → if len(query) > 200: return "medium" else "low"
  → PlanDAGActivity(query, complexity)  ← Activity 中生成 DAG
    → low: return {nodes: [{node_id:"n1",prompt:"分解问题"},{node_id:"n2",prompt:"回答"}]}
    → medium: return {nodes: [n1,n2,n3]}
  → EmitEventActivity(DAG_PLANNED, {nodes: [...]})
  → For node in topological order(nodes):
      → EmitEventActivity(DAG_NODE_STARTED, {node_id})
      → AgentActivity(task_id, node.prompt, session_messages, model, role=nil, node_id=node.node_id)
        → RecordUsageActivity(task_id, usage, node_id=node.node_id)  ← llm_calls 写入 node_id
      → EmitEventActivity(DAG_NODE_COMPLETED, {node_id, result})
  → SynthesisActivity(node_results)
    → Merge DAG node outputs into final answer
    → 汇总 usage: tasks.usage_* = sum(all nodes)
  → EmitEventActivity(DAG_SYNTHESIZED, {final_answer})
  → SaveResultActivity → TASK_COMPLETED
```

**验收命令：**

```bash
# 提交 dag mode task（enable_dag_workflow=true 时）
TASK_ID=$(curl --noproxy '*' -s -X POST http://127.0.0.1:8080/api/v1/tasks \
  -H "Content-Type: application/json" \
  -d '{"query": "Explain quantum computing in detail", "config": {"mode": "dag", "max_total_tokens": 16000}}' | jq -r '.task_id')

for i in $(seq 1 60); do
  STATUS=$(curl --noproxy '*' -s http://127.0.0.1:8080/api/v1/tasks/$TASK_ID | jq -r '.status')
  [[ "$STATUS" == "completed" ]] || [[ "$STATUS" == "failed" ]] && break
  sleep 1
done

curl --noproxy '*' -s http://127.0.0.1:8080/api/v1/tasks/$TASK_ID | jq '{status, result}'

# 检查 DAG 事件
docker.exe exec deploy-redis-1 redis-cli XRANGE "task:$TASK_ID:events" - + COUNT 20
# 应看到：DAG_PLANNED → DAG_NODE_STARTED → DAG_NODE_COMPLETED (x2-3) → DAG_SYNTHESIZED → TASK_COMPLETED

# 检查 llm_calls 有 node_id 记录
docker.exe exec deploy-postgres-1 psql -U admin -d orchestrator -c "SELECT id, node_id, total_tokens FROM llm_calls WHERE task_id='$TASK_ID';"
```

**Pass 标准：**
- [ ] mode=dag 提交成功，status=completed
- [ ] Redis DAG_PLANNED 事件有 nodes 数组
- [ ] 每个 node 有 DAG_NODE_STARTED + DAG_NODE_COMPLETED
- [ ] llm_calls 表有 node_id 字段记录
- [ ] DAG_SYNTHESIZED 事件在最后
- [ ] result 非空且包含多个 node 的合并内容（前缀 "合并结果："）
- [ ] tasks.usage_total_tokens = sum(所有 node 的 total_tokens)

**常见失败原因：**
- DAGWorkflow 内使用 time.Now() 或随机数 → 违反 Temporal 确定性，replay 会失败
- DAG node result 未正确收集 → 检查 for loop 收集结果
- usage 汇总逻辑缺失 → SynthesisActivity 需要更新 tasks 表

---

### Phase 3B — Slice 5: Multi-Agent Lite

**前置条件：Slice 4 已完成并验收通过，enable_multi_agent=true**

**目标：**
- config.mode="multi_agent" 且 enable_multi_agent=true 时进入 MultiAgentWorkflow（否则 400 validation_error）
- Planner → Worker → Critic → Synthesizer 顺序执行
- 每个 agent 的 usage 写入 llm_calls（含 agent_role）
- Python LLM Service role-aware（system prompt injection）
- AGENT_* / CRITIC_REVIEWED / SYNTHESIS_COMPLETED 事件完整

**修改文件：**
- `internal/workflows/multi_agent.go` — 新建 MultiAgentWorkflow
- `internal/workflows/router.go` — 更新路由逻辑
- `python_llm_service/app.py` — role-aware system prompt injection（若尚未实现）
- `internal/events/types.go` — 新增 AGENT_STARTED / AGENT_COMPLETED / CRITIC_REVIEWED / SYNTHESIS_COMPLETED

**新增 Migration：**
```sql
-- llm_calls 表新增 agent_role 字段
ALTER TABLE llm_calls ADD COLUMN IF NOT EXISTS agent_role VARCHAR(50);
```

**数据流：**

```
MultiAgentWorkflow(task_id, query, session_id, ...)
  → EmitEventActivity(WORKFLOW_STARTED)
  → LoadSessionActivity(session_id)
  → EmitEventActivity(AGENT_STARTED, {role:"planner"})
  → AgentActivity(task_id, query, [], model, role="planner")
    → RecordUsageActivity(task_id, usage, agent_role="planner")
    → return {content: "plan output"} (plain text，不强制 JSON parse)
  → EmitEventActivity(AGENT_COMPLETED, {role:"planner", output: plan})
  → EmitEventActivity(AGENT_STARTED, {role:"worker"})
  → AgentActivity(task_id, plan_or_subtask, [], model, role="worker")
    → RecordUsageActivity(task_id, usage, agent_role="worker")
    → return {content: "worker answer"}
  → EmitEventActivity(AGENT_COMPLETED, {role:"worker", output: answer})
  → EmitEventActivity(AGENT_STARTED, {role:"critic"})
  → AgentActivity(task_id, answer, [], model, role="critic")
    → RecordUsageActivity(task_id, usage, agent_role="critic")
    → return {content: "critique"}
  → EmitEventActivity(CRITIC_REVIEWED, {critique})
  → EmitEventActivity(AGENT_STARTED, {role:"synthesizer"})
  → AgentActivity(task_id, worker_answer + "\n" + critique, [], model, role="synthesizer")
    → RecordUsageActivity(task_id, usage, agent_role="synthesizer")
    → return {content: "final answer"}
  → EmitEventActivity(SYNTHESIS_COMPLETED, {final_answer})
  → SaveResultActivity → TASK_COMPLETED
```

**验收命令：**

```bash
curl --noproxy '*' -s -X POST http://127.0.0.1:8080/api/v1/tasks \
  -H "Content-Type: application/json" \
  -d '{"query": "What are the pros and cons of remote work?", "config": {"mode": "multi_agent", "max_total_tokens": 16000}}' | jq -r '.task_id'
# 记录 task_id，轮询

docker.exe exec deploy-redis-1 redis-cli XRANGE "task:$TASK_ID:events" - + COUNT 30
# 应看到：AGENT_STARTED(planner) → AGENT_COMPLETED(planner) → AGENT_STARTED(worker) → AGENT_COMPLETED(worker) → AGENT_STARTED(critic) → CRITIC_REVIEWED → AGENT_STARTED(synthesizer) → SYNTHESIS_COMPLETED → TASK_COMPLETED

curl --noproxy '*' -s http://127.0.0.1:8080/api/v1/tasks/$TASK_ID | jq '{status, result}'

# 检查 llm_calls 有 agent_role 记录
docker.exe exec deploy-postgres-1 psql -U admin -d orchestrator -c "SELECT id, agent_role, total_tokens FROM llm_calls WHERE task_id='$TASK_ID';"
```

**Pass 标准：**
- [ ] mode=multi_agent 提交成功，status=completed
- [ ] 事件序列包含 planner / worker / critic / synthesizer 所有角色
- [ ] llm_calls 表有 agent_role 字段记录
- [ ] CRITIC_REVIEWED 事件有 critique 内容
- [ ] SYNTHESIS_COMPLETED 在最后
- [ ] result 是 synthesizer 生成的最终答案

---

### Phase 3C — Slice 6: Tool Abstraction Lite + Smoke Test

**前置条件：Slice 5 已完成并验收通过，enable_tools=true**

**目标：**
- config.enable_tools=true 且 enable_tools=true 时启用 tool（否则 400 validation_error）
- 确定性 tool 决策规则（query 包含 calculate / calculator / 数字运算表达式）
- calculator + echo safe tool
- TOOL_STARTED / TOOL_COMPLETED / TOOL_FAILED 事件
- smoke_test.sh 覆盖全量路径（simple / dag / multi_agent / budget / session / tool）

**修改文件：**
- `internal/activities/tool.go` — 新建 ToolActivity（whitelist 算术解析器）
- `internal/workflows/simple.go` — 增加 tool 判断逻辑（确定性规则）
- `internal/events/types.go` — 新增 TOOL_STARTED / TOOL_COMPLETED / TOOL_FAILED
- `scripts/smoke_test.sh` — 重写，覆盖全量 6 种路径

**Tool 调用决策（确定性规则）：**

```
enable_tools == true AND (
  query contains "calculate" / "calculator" OR
  query matches numeric expression pattern like "2+3" / "15 * 23"
)
→ 执行 ToolActivity(calculator, {expr: extracted_expr})
→ TOOL_STARTED → TOOL_COMPLETED
→ Tool result 注入 prompt 再次调用 AgentActivity
```

**注意：Phase 3C 只支持单轮 tool call，不做 tool loop。**

**验收命令：**

```bash
# 测试 calculator tool
TASK_ID=$(curl --noproxy '*' -s -X POST http://127.0.0.1:8080/api/v1/tasks \
  -H "Content-Type: application/json" \
  -d '{"query": "Calculate 15 * 23 + 7", "config": {"enable_tools": true}}' | jq -r '.task_id')

for i in $(seq 1 60); do
  STATUS=$(curl --noproxy '*' -s http://127.0.0.1:8080/api/v1/tasks/$TASK_ID | jq -r '.status')
  [[ "$STATUS" == "completed" ]] || [[ "$STATUS" == "failed" ]] && break
  sleep 1
done

docker.exe exec deploy-redis-1 redis-cli XRANGE "task:$TASK_ID:events" - + COUNT 20
# 应看到：TOOL_STARTED(calculator) → TOOL_COMPLETED → LLM_STARTED → LLM_COMPLETED

# 全量 smoke test
bash scripts/smoke_test.sh
# 应通过：simple / dag / multi_agent / budget / session / tool
```

**smoke_test.sh 最终结构：**

```bash
#!/bin/bash
set -euo pipefail

GATEWAY_URL="${GATEWAY_URL:-http://127.0.0.1:8080}"
REDIS_HOST="${REDIS_HOST:-127.0.0.1}"
REDIS_PORT="${REDIS_PORT:-6379}"

log() { echo "[$(date +'%H:%M:%S')] $*"; }
fail() { echo "[FAIL] $*" >&2; exit 1; }

# Helper to run redis-cli
redis_cmd() {
  if command -v redis-cli &>/dev/null; then
    redis-cli -h "$REDIS_HOST" -p "$REDIS_PORT" "$@"
  else
    docker.exe exec deploy-redis-1 redis-cli "$@"
  fi
}

wait_task() {
  local task_id=$1
  for i in $(seq 1 60); do
    local status=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$task_id" | jq -r '.status')
    if [[ "$status" == "completed" ]] || [[ "$status" == "failed" ]] || [[ "$status" == "budget_exceeded" ]]; then
      echo "$status"
      return 0
    fi
    sleep 1
  done
  fail "Task $task_id timeout"
}

# ---- Phase 2 Tests (已完成) ----
test_simple() {
  log "Testing simple mode..."
  local task_id=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
    -H "Content-Type: application/json" \
    -d '{"query": "Hello, what is 1+1?", "config": {"mode": "simple"}}' | jq -r '.task_id')
  local status=$(wait_task $task_id)
  [[ "$status" == "completed" ]] || fail "simple: expected completed, got $status"
  pass "simple mode"
}

test_budget() {
  log "Testing budget exceeded..."
  local task_id=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
    -H "Content-Type: application/json" \
    -d '{"query": "This is a very long query...", "config": {"max_total_tokens": 5, "max_completion_tokens": 1}}' | jq -r '.task_id')
  local status=$(wait_task $task_id)
  [[ "$status" == "budget_exceeded" ]] || fail "budget: expected budget_exceeded, got $status"
  pass "budget exceeded"
}

test_session() {
  log "Testing session memory..."
  local session_id="smoke-session-$(date +%s)"
  local task_id1=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
    -H "Content-Type: application/json" \
    -d "{\"query\": \"Hello\", \"session_id\": \"$session_id\"}" | jq -r '.task_id')
  wait_task $task_id1 > /dev/null
  local task_id2=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
    -H "Content-Type: application/json" \
    -d "{\"query\": \"What did I just say?\", \"session_id\": \"$session_id\"}" | jq -r '.task_id')
  wait_task $task_id2 > /dev/null
  local count=$(redis_cmd LLEN "session:$session_id:messages" | head -1)
  [[ "$count" -ge 2 ]] || fail "session: expected >=2 messages, got $count"
  pass "session memory"
}

test_sse() {
  log "Testing SSE..."
  local task_id=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
    -H "Content-Type: application/json" \
    -d '{"query": "SSE test", "config": {"mode": "simple"}}' | jq -r '.task_id')
  wait_task $task_id > /dev/null
  local events=$(redis_cmd XRANGE "task:$task_id:events" - + COUNT 20 | grep -c "event:" || true)
  [[ "$events" -ge 5 ]] || fail "sse: expected >=5 events, got $events"
  pass "SSE"
}

# ---- Phase 3A Tests (Slice 4) ----
test_dag() {
  log "Testing dag mode..."
  local task_id=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
    -H "Content-Type: application/json" \
    -d '{"query": "Explain AI and ML", "config": {"mode": "dag", "max_total_tokens": 16000}}' | jq -r '.task_id')
  local status=$(wait_task $task_id)
  [[ "$status" == "completed" ]] || fail "dag: expected completed, got $status"
  local dag_events=$(redis_cmd XRANGE "task:$task_id:events" - + COUNT 20 | grep -c "DAG_PLANNED" || true)
  [[ "$dag_events" -gt 0 ]] || fail "dag: no DAG_PLANNED event"
  pass "dag mode"
}

# ---- Phase 3B Tests (Slice 5) ----
test_multi_agent() {
  log "Testing multi_agent mode..."
  local task_id=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
    -H "Content-Type: application/json" \
    -d '{"query": "Pros and cons of remote work?", "config": {"mode": "multi_agent", "max_total_tokens": 16000}}' | jq -r '.task_id')
  local status=$(wait_task $task_id)
  [[ "$status" == "completed" ]] || fail "multi_agent: expected completed, got $status"
  pass "multi_agent mode"
}

# ---- Phase 3C Tests (Slice 6) ----
test_tool() {
  log "Testing tool (calculator)..."
  local task_id=$(curl --noproxy '*' -s -X POST "$GATEWAY_URL/api/v1/tasks" \
    -H "Content-Type: application/json" \
    -d '{"query": "Calculate 15 * 23 + 7", "config": {"enable_tools": true}}' | jq -r '.task_id')
  local status=$(wait_task $task_id)
  [[ "$status" == "completed" ]] || fail "tool: expected completed, got $status"
  local tool_events=$(redis_cmd XRANGE "task:$task_id:events" - + COUNT 20 | grep -c "TOOL_COMPLETED" || true)
  [[ "$tool_events" -gt 0 ]] || fail "tool: no TOOL_COMPLETED event"
  pass "tool (calculator)"
}

pass() { echo "  [PASS] $*"; }

# ---- Main ----
log "=== Smoke Test Starting ==="

# Phase 2 tests (always run)
test_simple
test_budget
test_session
test_sse

# Phase 3A tests (if enable_dag_workflow=true)
if [ "${ENABLE_DAG:-false}" == "true" ]; then
  test_dag
fi

# Phase 3B tests (if enable_multi_agent=true)
if [ "${ENABLE_MULTI_AGENT:-false}" == "true" ]; then
  test_multi_agent
fi

# Phase 3C tests (if enable_tools=true)
if [ "${ENABLE_TOOLS:-false}" == "true" ]; then
  test_tool
fi

echo ""
echo "=== ALL SMOKE TESTS PASSED ==="
```

**Pass 标准：**
- [ ] Phase 2 tests 全部通过（simple / budget / session / SSE）
- [ ] Phase 3A tests 通过（dag，ENABLE_DAG=true 时）
- [ ] Phase 3B tests 通过（multi_agent，ENABLE_MULTI_AGENT=true 时）
- [ ] Phase 3C tests 通过（tool，ENABLE_TOOLS=true 时）
- [ ] 单模式可单独运行：`ENABLE_DAG=true bash scripts/smoke_test.sh`

---

## 十九、总体验收标准

| 验收项             | 标准                                                                                 | 验证方式                         | Phase |
| ------------------ | ------------------------------------------------------------------------------------ | -------------------------------- | ----- |
| Simple mode 基本链 | POST /tasks → GET completed → result 非空 → llm_calls 有记录                         | curl + psql                      | 2 已完成 |
| Simple mode SSE    | SSE 包含 TASK_CREATED/WORKFLOW_STARTED/LLM_STARTED/LLM_COMPLETED/TASK_COMPLETED      | curl -N + redis-cli XREAD        | 2 已完成 |
| Budget exceeded    | max_total=5 → status=budget_exceeded → TASK_BUDGET_EXCEEDED event                    | curl + psql                      | 2 已完成 |
| Session memory     | 两次同 session_id → LRANGE 2条+ → SESSION_LOADED message_count>0                     | redis-cli LRANGE                 | 2 已完成 |
| DAG mode           | mode=dag → DAG_PLANNED + N×DAG_NODE_* + DAG_SYNTHESIZED + completed                 | redis-cli XREAD + curl           | 3A     |
| DAG node usage     | llm_calls 表有 node_id 字段，usage 汇总到 tasks 表                                  | psql                             | 3A     |
| Multi-agent mode   | mode=multi_agent → AGENT_STARTED/COMPLETED×4 + CRITIC_REVIEWED + SYNTHESIS_COMPLETED | redis-cli XREAD + curl           | 3B     |
| Multi-agent usage  | llm_calls 表有 agent_role 字段                                                      | psql                             | 3B     |
| Tool (calculator)  | enable_tools + query含calculate → TOOL_STARTED/COMPLETED + result含tool output       | redis-cli XREAD + curl           | 3C     |
| Smoke test 通过    | scripts/smoke_test.sh 覆盖 6 种路径，exit 0                                          | bash scripts/smoke_test.sh       | 全部  |

---

## 二十、后续扩展路线图

| Phase   | 内容                              | 关键能力                                        |
| ------- | --------------------------------- | ----------------------------------------------- |
| Phase 4 | DAG 并发 / ReAct / 真实 tokenizer | DAG nodes 并发执行、ReAct 推理循环、tiktoken    |
| Phase 5 | Swarm / Agent P2P / workspace     | Lead Agent + worker agents + workspace 文件共享 |
| Phase 6 | RAG / Qdrant / MCP / Sandbox      | 向量检索、MCP 协议、WASI code execution         |
| Phase 7 | HITL / Approval / UI             | Human-in-loop approval、Desktop UI              |
| Phase 8 | SDK / CLI / Multi-tenant          | Python/Go SDK、CLI tool、Auth/Quota             |

**后续扩展时：**

- 每个 Phase 独立可验证
- 不在 Phase 3 预留过多抽象（YAGNI）
- Phase 4+ 可在 Phase 3 架构上增量开发

---

## 二十一、风险与取舍

| 风险                            | 描述                                                                   | 应对                                   |
| ------------------------------- | ---------------------------------------------------------------------- | -------------------------------------- |
| 直接做完整 swarm 会崩           | Shannon swarm 有 Lead Agent + workspace P2P + 多轮 handoff，复杂度极高 | Phase 3 只做顺序 multi-agent，不做 P2P |
| Gateway 写成调用 LLM 的上帝服务 | Gateway 直接调 LLM 会破坏 Gateway/Worker/Aactivity 边界                | 严格遵守 Gateway 只做 HTTP/DB 路由     |
| Workflow 直接 IO                | Workflow 内调用 HTTP/DB/Redis 违反 Temporal 确定性原则                 | 所有外部 IO 必须放 Activity            |
| 过早做 RAG/Sandbox/MCP          | 这些能力各自是独立的大工程，会拖垮 Phase 3 进度                      | Phase 3 跳过，Phase 6+ 再做          |
| 不保留 mock mode                | 无 mock mode 则测试依赖真实 OpenAI API，测试不可靠                     | LLM_MODE=mock 默认，CI 使用 mock       |
| 事件不完整                      | 调试时会非常痛苦，不知道 workflow 走到哪一步                           | Phase 3 完整事件清单必须实现           |
| 状态只看 Redis不看 Postgres     | Redis 是缓存，Postgres 是事实来源，必须两边一致                        | 所有终态结果同时写 Postgres            |
| 每个功能不走垂直薄片            | 堆功能而不在每个 slice 可验证，会积累技术债                            | 6 个 Slice 顺序执行，每个可独立验收    |

---

## 二十二、禁止事项

Phase 2/3 明确禁止：

- ❌ 不做并发 DAG（Phase 4+）
- ❌ 不做真实 OpenAI（LLM_MODE=mock 默认）
- ❌ 不做 Multi-Agent（Phase 3B 以后）
- ❌ 不做 Tool（Phase 3C 以后）
- ❌ 不做 RAG / MCP / Sandbox
- ❌ 不重构 Gateway 主流程
- ❌ 不让 Workflow 直接 HTTP / DB / Redis
- ❌ 不把 Phase 2 和 Phase 3 合并成一次性实现

---

## 二十三、修改摘要

本文档由 Cribug_Phase2.md 修订而来，主要改动：

### Phase 状态更新

| 原状态 | 新状态 | 说明 |
| ------ | ------ | ---- |
| Phase 2A/2B/2C 为"当前执行" | Phase 2 标记为"已完成" | ✅ |
| Phase 2 下一目标是 Slice 4 | 下一目标改为 Slice 4.1 | 已更新 |

### Phase 命名统一

| 原名称 | 新名称 | 说明 |
| ------ | ------ | ---- |
| Module A — Phase 2 | Phase 2: Single Agent Runtime | 统一命名 |
| Module B — Phase 3A | Phase 3A: DAGWorkflow Lite | 统一命名 |
| Module C — Phase 3B | Phase 3B: Multi-Agent Lite | 统一命名 |
| Module D — Phase 3C | Phase 3C: Tool Abstraction Lite | 统一命名 |

### 环境变量修正

- 环境变量修正：WSL 本机必须用 `TEMPORAL_ADDRESS=127.0.0.1:17233`
- 添加 WSL 环境说明：本地 WSL 必须用 127.0.0.1:17233

### Smoke Test 结构调整

- Phase 2 tests 标记为"已完成"
- Phase 3A/3B/3C tests 标记为渐进扩展
- 环境变量控制测试是否执行：`ENABLE_DAG=true`、`ENABLE_MULTI_AGENT=true`、`ENABLE_TOOLS=true`

### DAG Node Usage 设计（新增）

Phase 3A 新增内容：
- llm_calls 表新增 node_id 字段
- 每个 DAG node 产生一条 llm_calls 记录
- SynthesisActivity 汇总 usage 到 tasks 表
- DAG 模式必须继续支持 budget 检查

### Workflow Router 方案（明确）

选择：**Worker 内 Workflow Router**

- Gateway 不做 Workflow 类型判断，只传 config.mode
- Temporal Worker 注册多个 Workflow 类型
- Worker 内部通过 Workflow Router 根据 TaskRequest.config.mode 选择执行哪个 Workflow

### 禁止事项（明确）

新增"禁止事项"章节：
- 不做并发 DAG
- 不做真实 OpenAI
- 不做 Multi-Agent（Phase 3B 以前）
- 不做 Tool（Phase 3C 以前）
- 不做 RAG / MCP / Sandbox