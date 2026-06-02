# Cribug Phase 8 v2.0 任务书 — Web Product Layer / GPT-like Agent Assistant

> 本文件为 **Cribug Phase 8 v2.0** 计划书。
> v1.0 仅是一份 Shannon Web/Product 探索报告（已归档为 `Cribug_Phase8.md`）。
> v2.0 在其基础上，把 Phase 8 重新定位为 **Web Product Layer / GPT-like Agent Assistant**：把 Phase 7 的 Advanced Router / HITL / Reflection / ToT / Debate / Research v2 等 Agent Runtime 能力，补齐 Web 前端、产品级后端 API、会话/消息持久化、SSE 流式事件、任务状态展示、Approval UI、Workspace/Report 查看、基础 Provider 配置，使 Cribug 在 Phase 8 结束后**可以通过浏览器像 GPT 一样实际使用 LLM 和 Agent workflow**。
>
> 本文是**计划书**，不包含实际代码实现；所有路径、文件结构、API 名称、字段都属于设计草案，最终落地时以代码为准。

---

## 一、版本说明

| 字段        | 值                                                                        |
| ----------- | ------------------------------------------------------------------------- |
| 文档版本    | Phase 8 v2.0                                                              |
| 前置版本    | Phase 8 v1.0（Shannon 探索报告，已归档为 `Cribug_Phase8.md`）             |
| 上游基线    | Phase 6（已冻结） + Phase 7（进行中 / 已完成 Advanced Router + HITL 部分） |
| 下游关联    | Phase 9（暂未规划；本文 §六定义 Phase 8 / Phase 9 边界）                 |
| 目标读者    | Cribug 开发者 / Phase 8 实施者 / Code Reviewer                            |
| 关键参考    | Shannon Web/Product（`/home/florian/code/cribug/desktop` + `go/orchestrator/cmd/gateway`，**只读**） |

**主要相对 v1.0 的调整**：

1. **目标改写**：从「Shannon 探索报告 + 复刻思路」改成「承接 Phase 7 的 Web 产品化实施方案」。
2. **范围改写**：明确 v2.0 = Web 产品层 + GPT-like Assistant；SDK / CLI / Multi-tenant 推到 Phase 9。
3. **可执行性改写**：v2.0 必须有 Slice 表、API 设计、DB 设计、SSE schema、前后端目录、测试脚本、验收路径。
4. **职责边界改写**：明确 Frontend / Gateway / Workflow / Activity / SSE Bridge / Workspace / DB / Config 的强制边界，不允许前端直接理解 Temporal workflow 细节。
5. **承上启下改写**：明确哪些依赖 Phase 7 完成（如 Router 的 `execute-routed`、HITL `respond` API、Research v2 final answer ref），哪些可以与 Phase 7 同步推进。

---

## 二、项目定位

### 2.1 Cribug 是什么

Cribug 是一个 **agent runtime / agent workflow engine**，核心能力包括：

* Phase 1-2：基础 LLM 任务执行 + Session / Budget
* Phase 3：Tools / ReAct
* Phase 4：DAG 工作流（动态重规划、并发控制、可视化）
* Phase 5：Multi-Agent（Swarm、Critic、Researcher、Synthesizer）
* Phase 6：MCP Tool Runtime / Sandbox（WASI）/ Skills System / Hooks Event System / RAG（Qdrant）/ Research-Synthesis v1
* Phase 7：Advanced Strategy Router（统一入口）/ HITL & Approval / Reflection / Tree-of-Thoughts / Debate / Research-Synthesis v2
* **Phase 8（本文）**：把上面所有能力**通过 Web 浏览器产品化**，提供 GPT-like Agent Assistant 体验

### 2.2 Phase 8 v2.0 是什么 / 不是什么

**Phase 8 是**：

* 一个 **Web Product Layer** —— 提供浏览器可用的 ChatGPT-like 界面
* 一个 **GPT-like Agent Assistant** —— 用户发消息，后端用 Advanced Router 选路径，前端看到流式回答、Router 决策、Workflow 时间线、Approval 卡片、Workspace 报告
* **产品化** Phase 7 的能力（不改写 Phase 7 Runtime，只补 Web 接入）
* **最小可用 Web MVP** —— 单用户 / 本地开发优先；不强求企业级 multi-tenant

**Phase 8 不是**：

* ❌ 重新实现 Phase 7 的 Agent Runtime（Router / HITL / Reflection / ToT / Debate / Research v2）
* ❌ SDK / CLI / OpenAI-compatible API（这些是 Phase 9）
* ❌ 完整 multi-tenant / 企业级 Auth / Billing / Quota
* ❌ Tauri 桌面打包（仅做 Web）
* ❌ 插件市场 / 工具市场
* ❌ 复杂团队权限系统

---

## 三、当前已完成基线

> 真实代码状态以 Cribug 仓库 `git log` + 现有 `migrations/` + `internal/` 为准。本节区分"已实现"与"未实现"。

### 3.1 Phase 6（已冻结）

已交付能力：

* Phase 6A：MCP Tool Runtime（`internal/activities/mcp*.go` + `internal/workflows/mcp.go` + `internal/api/mcp_handlers.go`）
* Phase 6B：Sandbox Runtime（`internal/activities/sandbox.go` + `internal/workflows/sandbox.go` + `internal/api/sandbox_handlers.go`）
* Phase 6C：Skills System（`internal/activities/skills.go` + `internal/workflows/skill_execution.go` + `internal/api/skills_handlers.go`）
* Phase 6D：Hooks Event System（`internal/hooks/` + `internal/api/hooks_handlers.go`）
* Phase 6E：Documents / Embeddings / Vector DB（Qdrant）
* Phase 6F：RAG Retrieval + Research-Synthesis v1
* Migrations：001-009
* E2E 测试脚本：`test_phase6_*.sh` 等

### 3.2 Phase 7（部分完成 / 进行中 / 未实现）

> Phase 7 总计 6 个 Slice（23-28）。本节以代码 + git log 为准。

| Slice                              | 计划状态 | 代码状态（截至本次勘察）                                                  | 备注                                                                                            |
| ---------------------------------- | -------- | -------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------- |
| 23 Advanced Strategy Router        | 计划     | **已实现**（`internal/workflows/router.go` / `internal/activities/router.go` / `internal/api/router_handlers.go` + migration 010） | 提供 `POST /api/v1/tasks/route`、`POST /api/v1/tasks/execute-routed`、`GET .../routing-decision`、`GET .../routing-events` |
| 24 HITL / Approval / UI Control    | 计划     | **已实现**（`internal/workflows/router.go`（含 approval signal 部分）/ `internal/activities/approval.go` / `internal/api/approval_handlers.go` + migrations 011-012） | 提供 `GET /api/v1/approvals/pending`、`GET /api/v1/approvals/{id}`、`POST /api/v1/approvals/{id}/respond` |
| 25 Reflection Production Mode      | 计划     | **计划中 / 部分落地**（见 `Cribug_Phase7.md` §四）                         | 依赖 Phase 7 实际收尾；本计划书假定 Phase 8 启动时 Slices 25-28 至少有 mock/基础实现可用         |
| 26 Tree-of-Thoughts                | 计划     | **计划中 / 部分落地**                                                       | 同上                                                                                            |
| 27 Debate Mode                     | 计划     | **计划中 / 部分落地**                                                       | 同上                                                                                            |
| 28 Research-Synthesis v2           | 计划     | **计划中 / 部分落地**                                                       | 同上                                                                                            |

**说明**：Phase 8 v2.0 假设 Slices 23-24 已稳定（HTTP handler / DB / event 都已经接好），Slices 25-28 至少可以走通 mock + 基础 workflow；Phase 8 不会假设 Slices 25-28 全部完成，但会为它们预留 Event Type、API 路径和 Frontend 展示位。

### 3.3 当前 Cribug 真实实现状态（仓库现状）

* **Backend（Go）**：
  * `cmd/gateway/main.go` + `cmd/worker/main.go` 启动 Gateway / Worker
  * `internal/api/` 已有 router（chi）：`/health` + `/api/v1/{tasks, stream/sse, mcp, sandbox, skills, hooks, tasks/route, tasks/execute-routed, tasks/{id}/routing-decision, tasks/{id}/routing-events, approvals}`
  * `internal/events/` 已有 100+ `EventType` 常量（`WORKFLOW_STARTED`、`LLM_OUTPUT`、`TOOL_INVOKED`、`APPROVAL_REQUESTED` 等）
  * `internal/workflows/` + `internal/activities/` 已有 router / dag / multi_agent / swarm / research / sandbox / skills / mcp / rag_query / simple / ingestion / skill_execution 等 workflow
  * `migrations/` 001-012
* **Python LLM Service（`python_llm_service/`）**：已实现 OpenAI / Anthropic / Google 适配器、embeddings、router classifier
* **SSE 已有**：`/api/v1/stream/sse?task_id=...` 走 Redis Streams 推送 `TASK_*` / `LLM_*` / `WORKFLOW_*` 事件
* **Workspace**：Phase 5 已冻结；`workspace_topics` 在 Redis（key 名见 Phase 7 计划书 §8.1），append-only；`workspace_objects` 在 Postgres（由 research / swarm 等 workflow 写入）
* **配置**：`config/features.yaml`、`config/budget.yaml`、`config/models.yaml`（空文件，仅占位）
* **前端**：**当前不存在**；仓库根目录没有 `web/` / `frontend/` / `desktop/` / `app/` / `*.tsx`。这是 Phase 8 v2.0 要从零搭建的部分。
* **Shannon 参考目录**（只读）：
  * `/home/florian/code/cribug/desktop/`（Next.js 16 + React 19 + Tailwind 4 + Redux Toolkit）
  * `/home/florian/code/cribug/go/orchestrator/cmd/gateway/internal/handlers/`（task.go / session.go / approval.go）
  * `/home/florian/code/cribug/go/orchestrator/internal/activities/stream_events.go`（30+ 事件类型）
  * `/home/florian/code/cribug/python/llm-service/`

---

## 四、Phase 8 目标

### 4.1 顶层目标

**让 Cribug 在浏览器里像 GPT 一样可用**，同时把 Phase 7 的 Agent Runtime 信息透明地展示给用户。

### 4.2 用户视角验收（Phase 8 收官时必须可演示）

1. 启动 Postgres + Redis + Temporal + Python LLM Service + Gateway + Worker + Web 前端
2. 浏览器打开 Cribug Web
3. **配置或选择 LLM Provider**（OpenAI / Anthropic / Google / 自定义 base_url）
4. **新建聊天会话**
5. 像 ChatGPT 一样输入"法国首都是哪里？"
6. 后端用 **Advanced Router** 自动选 `direct_answer`
7. 前端通过 **SSE** 实时看到流式回答
8. 输入需要 RAG 的问题 → 前端看到 `router_decision` 卡片（`planned_mode=dag`, `executed_mode=dag`, `requires_rag=true`）
9. 输入 sandbox / 高风险问题 → 前端弹出 **Approval 卡片**（Approve / Reject / Modify）
10. 点击 Reject → workflow 安全结束，前端展示状态
11. 打开 **Workspace / Report Viewer** 看 Research v2 报告
12. **刷新页面** → 会话和消息完整恢复

### 4.3 工程视角目标

* 一套 **产品级 REST API**（Chat Session / Message / Task Status / Approval / Workspace / Provider Config / Health）
* 一套 **SSE 事件协议**（统一事件类型 + `last_event_id` 续传 + 30s 心跳）
* 一个 **Next.js + React + TS + Tailwind** Web 前端
* 一组 **E2E / 烟雾 / 集成测试脚本**
* **不重写** Phase 7 任何 Runtime 代码

---

## 五、Phase 8 与 Phase 7 的承接关系

| 维度              | Phase 7 提供                                                       | Phase 8 接入方式                                                                  |
| ----------------- | ------------------------------------------------------------------ | --------------------------------------------------------------------------------- |
| 统一入口          | `AdvancedRoutingWorkflow` + `POST /api/v1/tasks/execute-routed`    | Chat Message API 内部调用，**前端不直接拼 routed 请求**                          |
| 路由决策展示      | `routing_audit_logs` + `routing_decision` 字段                     | `GET /api/v1/tasks/{workflow_id}/routing-decision` 暴露给前端                    |
| 路由事件流        | `routing-events` 路由                                              | SSE 端点订阅                                                                      |
| 审批              | `approvals` 表 + `approval_audit_logs` + `POST .../respond`        | 复用 ApprovalHandler，新增 `GET /api/v1/approvals/pending`（已存在）+ 卡片 UI     |
| 任务暂停 / 恢复 / 取消 | Phase 7B 计划预留（signal）                                        | 复用；Phase 8 增加 Web 控制按钮（仅展示，不引入新协议）                          |
| Reflection / ToT / Debate / Research v2 | Slices 25-28（mock / 基础 workflow）                       | Phase 8 **不重写**；只确保事件被 SSE 推送 + UI 展示 timeline + 报告打开           |
| Session           | `tasks.session_id`(Phase 1 已存在,本计划书统称 `runtime_session_id`) | Phase 8A 引入 `chat_sessions.cribug_session_id` + `chat_messages.cribug_session_id` **作为产品层会话**,与 `runtime_session_id` 通过 `message_task_links` 桥接(避免命名混淆) |
| Workspace / Report| `workspace_topics`（Redis）+ `workspace_objects`（Postgres）        | Phase 8H 引入 `GET /api/v1/workspace/objects/{ref}` + `GET /api/v1/reports/{ref}` 只读 API |
| Provider 配置     | 通过 `.env` 的 `OPENAI_API_KEY` / `ANTHROPIC_API_KEY` 注入          | Phase 8I 引入 Web UI + `config/model_providers.yaml`（非敏感元数据） + secrets 分层 |

**关键约束**：Phase 8 **不改写** Phase 7 任何 workflow / activity / DB migration 011-012；只新增产品和前端层。

---

## 六、Phase 8 与 Phase 9 的边界

| 能力                    | Phase 8 v2.0               | Phase 9+                    |
| ----------------------- | -------------------------- | --------------------------- |
| Web 前端（浏览器）      | ✅ 核心                    | 持续打磨                    |
| Chat / Session / Message API | ✅ 核心                  | 扩展（如消息编辑、branch） |
| SSE 事件流              | ✅ 核心                    | 增量事件类型                |
| Approval UI             | ✅ 核心                    | 模板化审批                  |
| Workspace / Report 查看 | ✅ 核心                    | 全文搜索 / 版本对比         |
| Basic Provider Config   | ✅ 单用户 / 本地开发        | Multi-tenant provider 管理  |
| **Python SDK**          | ❌                          | ✅ Phase 9                   |
| **Go SDK**              | ❌                          | ✅ Phase 9                   |
| **CLI**                 | ❌                          | ✅ Phase 9                   |
| **OpenAI-compatible API** | ❌                        | ✅ Phase 9                   |
| **完整 Multi-tenant**    | ❌（单租户占位）            | ✅ Phase 9                   |
| **企业级 Auth / Billing / Quota** | ❌（API Key 占位）     | ✅ Phase 9                   |
| **Tauri 桌面打包**      | ❌                          | 可选 Phase 9                |
| **插件 / 工具市场**     | ❌                          | Phase 10+                   |

---

## 七、Shannon Web/Product 参考点

> Shannon 仅作**只读参考**，不修改、不照搬目录、不强行 1:1 复刻文件。

### 7.1 Shannon 关键模式

* **SSE + 状态机**：`EventSource` 订阅 `text/event-stream`；后端通过 streaming service 推 `WORKFLOW_STARTED` / `LLM_OUTPUT` / `APPROVAL_REQUESTED` / `WORKFLOW_COMPLETED` 等事件
* **Task = Workflow**：Shannon 的 `task_id == workflow_id`，但 Cribug 当前已经分离 `tasks.id` (UUID) 与 `tasks.workflow_id` (Temporal string)，**Phase 8 沿用 Cribug 的现状，不强行合并**
* **Session ↔ Task**：一个 session 多个 task（多轮对话）
* **Event 30+ 类型**：Shannon 在 `stream_events.go` 定义大量 `EventType` 常量，Cribug 的 `internal/events/types.go` 已有 100+ 类型
* **3 级 Auth fallback**：API Key → JWT → X-User-Id（dev）
* **Redux run slice**：单一 reducer `addEvent` 处理所有事件 → 状态机驱动 UI

### 7.2 借鉴但必须 Cribug 化的部分

| Shannon 模式               | Cribug 8 v2.0 落地                                                   |
| -------------------------- | ------------------------------------------------------------------- |
| `lib/shannon/api.ts`       | `web/lib/api/cribug.ts`：统一 base URL、auth header、错误处理       |
| `lib/shannon/stream.ts`    | `web/lib/events/sse.ts`：`useRunStream` hook + 断点续传 + 心跳       |
| `lib/features/runSlice.ts` | 改用 **Zustand**（更轻量、无 Provider 嵌套）或 Redux Toolkit；事件 reducer 统一 |
| `run-conversation.tsx`     | `web/components/chat/Conversation.tsx` + 消息角色（user/assistant/system/status/approval） |
| `chat-input.tsx`           | `web/components/chat/Input.tsx` + Provider 选择 + 模型 tier（如果需要） |
| `app-sidebar.tsx`          | `web/components/chat/SessionSidebar.tsx`                            |
| `run-timeline.tsx`         | `web/components/task/Timeline.tsx` + 折叠 JSON 详情                 |
| `run-detail/page.tsx`      | `web/app/(app)/chat/[sessionId]/page.tsx`                           |
| `handlers/task.go`         | `internal/api/product/chat_handlers.go` + `task_handlers.go`        |
| `handlers/session.go`      | `internal/api/product/session_handlers.go`                          |
| `handlers/approval.go`     | 复用 `internal/api/approval_handlers.go`（已存在）                  |
| `streaming.go`             | 复用 Cribug 已有 `internal/api/handler.go::streamTaskEvents`        |

### 7.3 明确**不**照搬

* ❌ Shannon 的 Tauri 打包（Phase 8 仅 Web）
* ❌ Shannon 的 Dexie / IndexedDB（Phase 8 用 Postgres + Redis + localStorage 仅存 UI 偏好）
* ❌ Shannon 的 Next.js 16（Phase 8 用 **Next.js 14 LTS** 起步，避免 bleeding edge；最终以仓库环境为准）
* ❌ Shannon 的 Redux Toolkit + redux-persist（Phase 8 用 Zustand + persist 中间件；store 体积更小）
* ❌ Shannon 的 30+ 事件类型全量复制（Phase 8 先收敛到 §十三 列出的 12 类，后续增量）
* ❌ Shannon 的 `desktop/` 目录命名（Cribug 用 `web/`）

---

## 八、强制职责边界

> 这些是硬约束，违反任何一条都视为 Phase 8 范围失控。

### 8.1 总览

```
┌────────────────────────────────────────────────────────────────────────────┐
│  Browser (Next.js)                                                         │
│    ↑ SSE                                                                  │
│    │                                                                       │
│    │ JSON/HTTP                                                            │
│    ↓                                                                       │
│  Gateway (Go)  ←─── Cribug Phase 8 v2.0 在这里加 Product API Layer        │
│    │ gRPC (内嵌 Temporal client)                                          │
│    ↓                                                                       │
│  Worker (Go)                                                               │
│    │                                                                       │
│    ├── Workflows  (deterministic, Temporal)                               │
│    │     ├── Phase 7 Advanced Router (Slice 23)                            │
│    │     ├── Phase 7 HITL / Approval (Slice 24)                            │
│    │     ├── Phase 7 Reflection (Slice 25)                                │
│    │     ├── Phase 7 ToT (Slice 26)                                       │
│    │     ├── Phase 7 Debate (Slice 27)                                    │
│    │     └── Phase 7 Research v2 (Slice 28)                               │
│    │                                                                       │
│    ├── Activities  (external IO, Temporal-safe wrappers)                  │
│    │                                                                       │
│    ├── Postgres  (tasks, executions, approvals, audit, session, ...)      │
│    ├── Redis     (sessions, workspace topics, event bus)                 │
│    └── Python LLM Service  (OpenAI / Anthropic / Google 适配)            │
└────────────────────────────────────────────────────────────────────────────┘
```

### 8.2 Frontend 边界

* **只**通过 Gateway HTTP API 通信；不直连 Postgres / Redis / Temporal / Python LLM Service
* **不**直接拼 Temporal workflow 名（即使 API 暴露了 workflow_id，前端也不知道那是 Temporal 的）
* **不**存储 API Key 到 localStorage；Provider 配置 API Key **只**通过 Web → Gateway → 后端加密落盘（Phase 8 v2.0 = AES-256-GCM + `CRIBUG_PROVIDER_AES_KEY`；详见 §10.9）
* **不**做完整 LLM 流式解析；SSE 收到 `message_delta` 增量字符串，**前端只做渲染**和**状态机派发**
* **不**实现 Phase 7 Runtime 任何逻辑

### 8.3 Gateway API 边界

* 负责**产品级 REST API**(Chat / Session / Message / Approval / Workspace / Provider / Health)
* 负责**鉴权占位**(Phase 8 v2.0 = 单用户本地开发,header `X-User-Id: dev-user` 即可)
* 负责**消息持久化**(`chat_sessions` + `chat_messages`;通过 `chatRepo` + `ChatMessageLifecycleService`)
* **不**直接 HTTP 自调 Gateway 自己的 handler(详见 §10.2):
  * 启动 workflow → 调 `internal/service.WorkflowLauncher.StartRoutedTask(...)`,**不**调 `POST /api/v1/tasks/execute-routed`
  * 提交审批 → 调 `approvalSvc.Respond(...)`,**不**调 `POST /api/v1/approvals/{id}/respond`
  * 读 task 状态 → 调 `taskSvc.GetStatus(...)`,**不**调 `GET /api/v1/tasks/{id}`
* 负责**聚合状态**(前端拿到的是 `routing_decision + status + progress + final_answer`,不是一堆 raw events)
* 负责**SSE / Event Bridge**:把 workflow / activity 事件转成产品级 `SSEEvent`

### 8.11 Raw Workflow Event vs Product Message State 边界（强制）

> 这是 Phase 8 v2.0 新增的硬性边界,放在最显眼的位置。**违反即视为数据模型失控**。

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                          数据持久化责任划分                                    │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  Raw Workflow Event (不持久化到产品表)                                       │
│  ─────────────────────────────────────                                      │
│  • 来源:internal/activities/* 调用 EmitTaskUpdate(...)                       │
│  • 走向:Redis Stream(events bus) → SSE Bridge 实时推送 → 浏览器                 │
│  • 持久化:Redis Stream(短 TTL)+ 可选 task_events 表缓存(Phase 8+ 增量)            │
│  • 例:`AGENT_STARTED` / `LLM_OUTPUT` / `TOOL_INVOKED` / `REFLECTION_ROUND_1`  │
│                                                                             │
│  **绝对不**写入 chat_messages 表                                              │
│                                                                             │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  Product Message State (必须持久化到 chat_messages)                           │
│  ────────────────────────────────────────────                                │
│  • 来源:ChatMessageLifecycleService 调 chatRepo                              │
│  • 走向:Postgres chat_messages(长期)+ SSE 推送状态变更                          │
│  • 字段:role / status / content(短文本)/ final_answer_ref /                   │
│         citations / artifacts / metadata(归一化产品字段)/ error                 │
│  • 例:`status: pending → streaming → completed`                                │
│       `metadata.routing_decision = {planned_mode, requires_rag, ...}`         │
│       `artifacts = [{type: "workspace_ref", ref: "ws:..."}]`                  │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

**强制规范**：

1. **Raw event 永远不写 chat_messages**。`internal/api/product/sse_bridge.go` 收到的 raw event 只能:
   * 实时推给前端(经归一化)
   * (可选)写到 Redis Stream 缓存
   * (可选)增量到 `task_events` 缓存表(Phase 8 v2.0 不做)

2. **SSE Bridge 必须做归一化**,把 raw 事件归类为 12 类(§十三);归一化结果中**只有产品字段**(routing_decision / artifacts / status_text / final_answer_ref)才回写到 chat_messages。

3. **ChatMessageLifecycleService 内部事件 → 状态映射** 见 §10.2 表;不在表里的 raw event 一律不调 lifecycle。

4. **测试断言**(L1 必跑):
   ```bash
   # 跑完一个完整 E2E 后:
   psql -c "SELECT content FROM chat_messages WHERE message_id='<test_msg_id>';"
   # 断言:content 字段不包含 raw event payload(如 'AGENT_STARTED' / 'TOOL_INVOKED')
   # 断言:content 只包含用户可见的 assistant 文本
   ```

5. **反例**(禁止):
   ```go
   // ❌ 禁止:把 raw event 整个塞进 chat_messages
   chatRepo.AppendDelta(messageID, fmt.Sprintf("[%s] %s", rawEvent.Type, rawEvent.Payload))

   // ❌ 禁止:把 raw event 列表 JSON.stringify 到 chat_messages.metadata
   chatRepo.UpdateMetadata(messageID, map[string]any{"raw_events": allRawEvents})
   ```

6. **正例**(必须):
   ```go
   // ✅ 必须:从 raw event 抽取产品字段,再调 lifecycle
   switch rawEvent.Type {
   case "ROUTING_DECISION":
       decision := extractRoutingDecision(rawEvent.Payload)
       lifecycle.UpdateRoutingDecision(messageID, decision)
   case "LLM_DELTA":
       delta := extractDeltaText(rawEvent.Payload)
       lifecycle.AppendDelta(messageID, delta)  // 只追加可显示文本
   }
   ```

7. **为什么这条边界必须硬性**:
   * 写 raw event 进 chat_messages → DB 体积爆炸(一次任务可能产生数百个 raw event)
   * raw event 格式由 Phase 7 决定,Phase 8 改 schema 会牵动产品层
   * 前端 timeline / 实时性靠 SSE 推送,**不**需要 DB 持久化每条 raw event
   * 用户刷新页面后看到的是"产品状态"(routing decision / final answer / approval card),**不是** raw event 重放

### 8.4 Workflow 边界

* **不**感知 Web 前端存在
* **不**感知 chat_sessions / chat_messages 表存在
* **不**感知"产品层"概念
* 仍然只负责 deterministic orchestration（与 Phase 7 一致）
* Phase 8 不会新增或修改 workflow；只验证 Slices 25-28 走通后，事件能被 SSE Bridge 抓到

### 8.5 Activity 边界

* **不**感知 Web 前端
* 仍然只负责外部 IO（LLM / DB / Redis / MCP / Sandbox / Skills / RAG / Workspace）
* 不做 chat_sessions / chat_messages 写入（属于 Gateway 责任）

### 8.6 SSE / Event Bridge 边界

* Phase 8 v2.0 **新增模块**：`internal/api/product/sse_bridge.go`
* 输入：Redis Streams 上 `tasks:{id}:events` 原始 event payload
* 输出：HTTP SSE，事件类型 = §十三 定义的 12 类
* 断点续传：`?last_event_id=N` → 从 Redis Stream `XREAD ... ID N` 续读
* 心跳：30s 一条 `: keepalive` 注释行
* **不**做事件持久化（已由 Activity 写入 Redis Stream）
* **不**做事件修改（仅做类型归一化，例如把 `WORKFLOW_COMPLETED` + `LLM_COMPLETED` 合并为 `task_progress`）

### 8.7 Workspace 边界

* **不**在 chat_messages 存长文本答案
* 长答案、研究报告、citation 列表、artifact 内容**只**写到 `workspace_objects`（Postgres）或 `workspace_topics`（Redis append-only，Phase 5 已冻结）
* 前端通过 `GET /api/v1/workspace/objects/{ref}` 按 ref 读取

### 8.8 DB 边界

* **不**修改 migrations 001-012
* **新增** migrations 013-016（chat_sessions / chat_messages / message_task_links / provider_configs）
* **不**在 message 中存 workflow history 摘要（除非作为高亮标记，长度上限 500 char）
* **不**在 message 中存 LLM 完整 raw prompt / completion（那是 `usage_logs` / `llm_calls` 的事）

### 8.9 Config 边界

* **不**把 API Key 写进 `config/*.yaml`
* **不**把 API Key 写到数据库明文列（即使本地模式也用对称加密 + 启动时输入 master password 占位）
* `.env` **只**保存基础设施密钥（数据库密码、Redis 密码、Temporal 证书），LLM provider 的 key 由 Web UI 设置后加密存储
* 长期目标：`config/model_providers.yaml` 存 provider 非敏感元数据（provider name / base_url / default model / embedding model / cost profile），但 Phase 8 v2.0 可以先用最小可用版本

---

## 九、Phase 8 Slice 总表

> 10 个 Slice，编号 Phase 8A-8J。每个 Slice 可独立验收。

| Slice  | 名称                                       | 目标                                                                 | 主要交付物                                                                                                |
| ------ | ------------------------------------------ | -------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------- |
| 8A     | Conversation / Message Persistence         | 先把 GPT-like 产品的会话 / 消息 / provider 持久化层做稳,作为后续 API 的基础;**不写任何 HTTP handler** | Migration 013-016（`chat_sessions` / `chat_messages` / `message_task_links` / `provider_configs` / `app_settings`） + `internal/db/chat_repo.go` + `internal/db/provider_repo.go` |
| 8B     | Product API + Chat Message Lifecycle       | 在 8A 的 repo 之上搭产品 API;**新增** `ChatMessageLifecycleService` 负责 assistant message 的创建/流式 patch/完成/失败/取消/持久化;handler 是薄壳 | `internal/api/product/{common,chat_handlers,task_handlers,approval_handlers,workspace_handlers,provider_handlers,system_handlers}.go` + `internal/service/chat_message_lifecycle.go` + `internal/api/router.go` 增路由 |
| 8C     | SSE / Event Bridge                         | 把 workflow / router / approval 事件整理成 Web 可消费的事件流       | `internal/api/product/sse_bridge.go` + `internal/events/web_events.go`(12 类统一事件)                    |
| 8D     | Frontend Foundation                        | 建立 Web 前端工程基础（Next.js + React + TS + Tailwind）             | `web/` 工程骨架 + `web/lib/api/` + `web/lib/events/` + `web/lib/store/`                                 |
| 8E     | GPT-like Chat UI                           | 浏览器中的基本 ChatGPT-like 对话体验                                 | `web/app/(app)/chat/[sessionId]/page.tsx` + `web/components/chat/*`                                      |
| 8F     | Router / Workflow Visibility Panel         | 展示 Router 决策、Workflow 状态、Timeline                            | `web/components/task/RoutingDecisionCard.tsx` + `web/components/task/Timeline.tsx`                        |
| 8G     | Approval UI                                | 把 Phase 7B Approval API 产品化（卡片 + 列表）                       | `web/components/approval/ApprovalCard.tsx` + `web/components/approval/PendingList.tsx`                    |
| 8H     | Workspace / Research Report Viewer         | 打开和查看长内容（WorkspaceRef / ReportRef / Citation）              | `web/app/(app)/workspace/[ref]/page.tsx` + `web/components/workspace/ReportViewer.tsx`                    |
| 8I     | Basic Model Provider Settings              | 单用户 / 本地环境能通过 Web 配置基本 LLM Provider                    | `web/app/(app)/settings/page.tsx` + `web/components/settings/ProviderConfigForm.tsx` + Migration 015-016   |
| 8J     | End-to-End Web Usability Test              | 验证 Phase 7 + Phase 8 收尾后 Cribug 可通过 Web 使用                  | `scripts/test_phase8_*.sh` × 5 + 完整手动 E2E 验收文档                                                     |

---

## 十、每个 Slice 的详细设计

### 10.1 Phase 8A — Conversation / Message / Provider Persistence（无 HTTP 层）

**目标**：先把 GPT-like 产品的**持久化层**做稳,作为 8B 产品 API 的基础。**Phase 8A 不写任何 HTTP handler**,只产出 schema、repo、unit test;handler 留到 8B,避免 API 在 DB 之前过度实现后被迫"配 schema"。

**为什么 8A 在 8B 之前**：

* Chat API 的全部业务行为(创建/流式 patch/完成/失败/取消)都强依赖 `chat_messages` 的列定义;反过来如果先写 API,会出现"为占位字段写一半 handler,然后再改 schema"的返工
* Provider key 持久化是 8B Chat API 真正能调通 LLM 的前置条件;schema 不稳,后续要全链路重测
* Repo 层可以独立 unit test(`go test ./internal/db/...`),不依赖 Temporal / SSE / Web,反馈循环最短

**新增 Migration 013**（`013_chat_sessions.sql`）：

```sql
-- 设计草案,最终 schema 以代码为准
-- 命名:cribug_session_id 是"产品层会话 ID",与 Phase 1 tasks.session_id(下文统称 runtime_session_id)完全不同的概念

CREATE TABLE IF NOT EXISTS chat_sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cribug_session_id VARCHAR(100) NOT NULL UNIQUE,    -- ★ 产品层 ID,前端 URL 用的就是这个
    user_id VARCHAR(100) NOT NULL DEFAULT 'dev-user',
    title VARCHAR(500) NOT NULL DEFAULT '',
    summary TEXT NOT NULL DEFAULT '',
    message_count INT NOT NULL DEFAULT 0,
    last_message_at TIMESTAMP WITH TIME ZONE,
    pinned BOOLEAN NOT NULL DEFAULT FALSE,
    archived BOOLEAN NOT NULL DEFAULT FALSE,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_chat_sessions_user ON chat_sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_chat_sessions_last_message ON chat_sessions(last_message_at DESC);
```

**新增 Migration 014**（`014_chat_messages.sql`）：

```sql
-- 设计草案
-- 严格区分:本表只存"产品层 message 状态",**不**存 raw workflow event 流

CREATE TABLE IF NOT EXISTS chat_messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    message_id VARCHAR(100) NOT NULL UNIQUE,
    cribug_session_id VARCHAR(100) NOT NULL,           -- ★ 引用 chat_sessions.cribug_session_id
    parent_message_id VARCHAR(100) NOT NULL DEFAULT '',
    role VARCHAR(20) NOT NULL,                         -- 'user' | 'assistant' | 'system' | 'status' | 'approval'
    status VARCHAR(20) NOT NULL DEFAULT 'pending',     -- 'pending' | 'streaming' | 'completed' | 'failed' | 'cancelled'
    content TEXT NOT NULL DEFAULT '',                  -- 短答案 / 预览 / status 文本(≤ 4 KB)
    preview TEXT NOT NULL DEFAULT '',                  -- 用户消息 preview(截断到 500 char)
    final_answer_ref VARCHAR(200) NOT NULL DEFAULT '', -- 指向 workspace_objects(如果存在)
    citations JSONB NOT NULL DEFAULT '[]'::jsonb,
    artifacts JSONB NOT NULL DEFAULT '[]'::jsonb,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,      -- 归一化后的产品层字段(见 §10.2 / §十三)
    error_type VARCHAR(50) NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    completed_at TIMESTAMP WITH TIME ZONE,
    CONSTRAINT chat_messages_role_check CHECK (role IN ('user','assistant','system','status','approval'))
);

CREATE INDEX IF NOT EXISTS idx_chat_messages_session ON chat_messages(cribug_session_id, created_at);
CREATE INDEX IF NOT EXISTS idx_chat_messages_status ON chat_messages(status);

-- 设计草案:多对多桥

CREATE TABLE IF NOT EXISTS message_task_links (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    message_id VARCHAR(100) NOT NULL,
    workflow_id VARCHAR(255) NOT NULL,
    task_id UUID,
    runtime_session_id VARCHAR(100) NOT NULL DEFAULT '', -- ★ Phase 1 tasks.session_id(用于溯源 workflow)
    routing_decision JSONB NOT NULL DEFAULT '{}'::jsonb,
    approval_id VARCHAR(100) NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    CONSTRAINT message_task_links_unique UNIQUE (message_id, workflow_id)
);
```

**新增 Migration 015**（`015_provider_configs.sql`,MVP 方案,见 §10.9）：

```sql
-- 设计草案
-- MVP:本地开发模式,API key 用 AES-GCM 加密后存 BYTEA;**不做** OS keychain / Vault

CREATE TABLE IF NOT EXISTS provider_configs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id VARCHAR(100) NOT NULL DEFAULT 'dev-user',
    provider_name VARCHAR(50) NOT NULL,        -- 'openai' | 'anthropic' | 'google' | 'custom'
    label VARCHAR(200) NOT NULL DEFAULT '',
    base_url TEXT NOT NULL DEFAULT '',
    api_key_encrypted BYTEA,                   -- AES-GCM(nonce || ciphertext || tag)
    chat_model VARCHAR(100) NOT NULL DEFAULT '',
    embedding_model VARCHAR(100) NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    CONSTRAINT provider_configs_unique UNIQUE (user_id, provider_name, label)
);
```

**新增 Migration 016**（`016_app_settings.sql`）：

```sql
-- 设计草案
CREATE TABLE IF NOT EXISTS app_settings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id VARCHAR(100) NOT NULL DEFAULT 'dev-user',
    key VARCHAR(100) NOT NULL,
    value JSONB NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    CONSTRAINT app_settings_unique UNIQUE (user_id, key)
);
```

**新增文件**(全是 DB 层,无 HTTP)：

```
internal/db/chat_repo.go            # CRUD: ChatSession, ChatMessage, MessageTaskLink
internal/db/provider_repo.go        # CRUD: ProviderConfig(含加密 / 解密)
internal/db/app_settings_repo.go    # CRUD: AppSettings
internal/db/chat_repo_test.go       # 单元测试:用真 Postgres 跑 INSERT/SELECT/UPDATE
internal/db/provider_repo_test.go   # 单元测试:加密 round-trip、错误 key
```

**命名边界(强制)**：

| 概念                              | 代码字段名                       | 出处                                            |
| --------------------------------- | -------------------------------- | ----------------------------------------------- |
| 产品层会话(浏览器 URL 用)          | `cribug_session_id`              | `chat_sessions.cribug_session_id` / `chat_messages.cribug_session_id` |
| Workflow 运行层会话(Phase 1 已有) | `runtime_session_id`             | `tasks.session_id` / `message_task_links.runtime_session_id` |
| Temporal Workflow ID              | `workflow_id`                    | `tasks.workflow_id` / `message_task_links.workflow_id` |

* **绝对禁止** 在产品层代码里只用裸 `session_id`;Go 侧必须用 `CribugSessionID` / `RuntimeSessionID` 类型别名(参考 §十五 术语表附录)。
* 两层 ID 通过 `message_task_links.runtime_session_id` 桥接;Chat API 不直接查 `tasks.session_id`。

**8A 验收**(只测 repo,不起 HTTP server)：

```bash
# go test ./internal/db/... 必须通过
go test -run TestChatRepo_CRUD ./internal/db/
go test -run TestProviderRepo_EncryptRoundTrip ./internal/db/
go test -run TestProviderRepo_BadKeyRejected ./internal/db/
```

* `chatRepo.CreateSession` / `GetSession` / `ListSessions` / `DeleteSession` 行为正确
* `chatRepo.CreateMessage(role=user)` 立即可见
* `chatRepo.AppendDelta(message_id, "...", "+1s")` 把 `content` 拼接后写回
* `chatRepo.MarkCompleted(message_id, final_answer_ref, citations, artifacts, metadata)` 落库
* `chatRepo.MarkFailed(message_id, errType, errMsg)` 落库
* `chatRepo.MarkCancelled(message_id)` 落库
* `providerRepo.SaveEncrypted(provider, key)` → `providerRepo.LoadDecrypted(id)` round-trip 相等
* `providerRepo.LoadDecrypted(id)` 错误 key 返回 error 而非 panic
* `message_task_links` 通过 `(message_id, workflow_id)` 唯一约束防止重复 link

---

### 10.2 Phase 8B — Product API + Chat Message Lifecycle Service

**目标**：在 8A 的 repo 之上搭产品 API;**新增** `ChatMessageLifecycleService` 负责 assistant message 的**创建 / 流式 patch / 完成 / 失败 / 取消** 生命周期;HTTP handler 是**薄壳**,只做 JSON 编解码和 user_id 注入。

**Chat API 禁止 HTTP 自调(强制)**：

> 这是 Phase 8 的关键反模式。Chat API 不得通过 `http.Client` / `httptest` 调自己 Gateway 已有的 handler(如 `POST /api/v1/tasks`、`POST /api/v1/tasks/execute-routed`、`POST /api/v1/approvals/{id}/respond`)。所有跨层调用必须走 Go 函数调用 / Temporal client / 已有的 service 入口。

| Chat API 需要的能力        | 禁止做法                                  | 必须做法                                                                  |
| -------------------------- | ----------------------------------------- | ------------------------------------------------------------------------- |
| 启动 workflow              | ❌ `http.Post("/api/v1/tasks/execute-routed")` | ✅ `internal/service.WorkflowLauncher.Start(ctx, req)`(内含 Temporal client + Router 调用) |
| 提交审批决策                | ❌ `http.Post("/api/v1/approvals/{id}/respond")` | ✅ `approvalSvc.Respond(ctx, id, decision)`(Phase 7B 已有 service 入口;如果还没有,先抽出来) |
| 读 task 状态                | ❌ `http.Get("/api/v1/tasks/{id}")`         | ✅ `taskSvc.GetStatus(ctx, workflow_id)`                                  |
| 读 workspace                | ❌ `http.Get("/api/v1/workspace/objects/{ref}")` | ✅ `workspaceSvc.GetObject(ctx, ref)`                                     |
| 读 routing decision        | ❌ `http.Get("/api/v1/tasks/{id}/routing-decision")` | ✅ `routerSvc.GetDecision(ctx, workflow_id)`                          |

**反例**(禁止):

```go
// ❌ 禁止:在 chat handler 里用 HTTP client 调自己
resp, _ := http.Post("http://localhost:8080/api/v1/tasks/execute-routed", ...)
```

**正例**(必须):

```go
// ✅ 必须:直接调 service / Temporal client
wfID, err := h.workflowLauncher.StartRoutedTask(ctx, service.RoutedTaskRequest{
    RuntimeSessionID: runtimeSessID,  // Phase 1 tasks.session_id
    Query:            req.Content,
    CribugSessionID:  req.CribugSessionID,
})
```

**ChatMessageLifecycleService 设计**(新增,放在 `internal/service/`)：

```
internal/service/
  chat_message_lifecycle.go   # ★ 核心服务,Phase 8B 新增
  chat_message_lifecycle_test.go
  workflow_launcher.go        # 封装 Temporal client + Router 调用
```

**接口契约**(草案,具体签名以代码为准)：

```go
package service

// ChatMessageLifecycleService owns the assistant message state machine.
// All state changes are persisted to chat_messages via chatRepo.
// The service is the single source of truth for message status transitions.
type ChatMessageLifecycleService interface {
    // CreateUserMessage: 持久化 user message(role=user, status=pending → 立刻 completed)
    CreateUserMessage(ctx context.Context, in CreateUserMessageInput) (*ChatMessage, error)

    // CreateAssistantMessage: 创建 assistant message(role=assistant, status=pending),并启动 workflow。
    // ★ 内部调用 WorkflowLauncher,绝不通过 HTTP 自调。
    CreateAssistantMessage(ctx context.Context, in CreateAssistantMessageInput) (*ChatMessage, error)

    // AppendDelta: 流式追加文本;status: pending → streaming。带 in-memory debounce,
    // 落库频率 ≤ 1 次/200ms(避免 DB 写爆)。绝不把 raw workflow event 整个塞进 content。
    AppendDelta(ctx context.Context, messageID string, delta string) error

    // MarkCompleted: 写入 final_answer_ref / citations / artifacts / metadata / tokens;
    // status: streaming → completed;completed_at 落库。
    MarkCompleted(ctx context.Context, messageID string, in CompleteInput) error

    // MarkFailed: status → failed;error_type / error 落库;但 content 保留(显示给用户看到失败前那部分内容)。
    MarkFailed(ctx context.Context, messageID string, errType, errMsg string) error

    // MarkCancelled: status → cancelled;由 chat handler 调(用户按停止按钮);**不**直接 cancel Temporal workflow
    // ——cancel 由 WorkflowLauncher.Cancel(workflow_id) 做,这里只更新 product 状态。
    MarkCancelled(ctx context.Context, messageID string) error

    // GetMessage: SSE bridge / 前端轮询 fallback 都用这个,而不是直接查 DB
    GetMessage(ctx context.Context, messageID string) (*ChatMessage, error)
}

type CreateUserMessageInput struct {
    CribugSessionID    string
    ParentMessageID    string
    Content            string  // 真实文本(前端已 trim,后端再 trim + 校验长度)
    Preview            string  // 截断到 500 char 的 preview
}

type CreateAssistantMessageInput struct {
    CribugSessionID    string
    UserMessageID      string    // 父 user message
    WorkflowID         string    // WorkflowLauncher.Start 之后返回
    RuntimeSessionID   string    // Phase 1 tasks.session_id(可选,如果 launcher 暴露)
    RoutingDecision    *types.RoutingDecision
}

type CompleteInput struct {
    FinalAnswerRef  string
    Citations       []Citation
    Artifacts       []Artifact
    Metadata        map[string]any  // {tokens:{prompt,completion,total}, cost, duration_ms, model_tier}
    WorkspaceRefs   []string        // 触发 workspace_ref_created 通知
}
```

**Event → Lifecycle 映射规则**(强制,见 §十一 13.4):

| SSE 事件类型             | lifecycle 调用                | 备注                                                                                          |
| ------------------------ | ----------------------------- | --------------------------------------------------------------------------------------------- |
| `task_started`           | `MarkStatus(messageID, "pending")` | 落到 status 列;**不**单独发 SSE(避免双重推送)                                              |
| `router_decision`        | `UpdateRoutingDecision(messageID, decision)` | 写到 `metadata.routing_decision`;同时把 `planned_mode` 等塞 assistant message `metadata`  |
| `message_delta`          | `AppendDelta(messageID, delta)`       | debounce 200ms 落库                                                                             |
| `workflow_event`         | **不**调 lifecycle           | raw event **不**持久化到 chat_messages;SSE Bridge 实时推给前端即可;Timeline 用 `task_events` 表缓存(可选 Phase 8+,MVP 不做) |
| `tool_call` / `rag_retrieval` / `sandbox_event` | **不**调 lifecycle  | 同上;实时 SSE 推送,不入 chat_messages                                                              |
| `approval_requested`     | `CreateStatusMessage(...)`   | 新增一条 `role=status, status=pending, content="Awaiting approval..."` 消息卡片;同时 SSE 推送  |
| `approval_resolved`      | `UpdateStatusMessage(...)` + `AppendDelta(...)` 继续流式 | 若 reject,后续会收到 `error` → 走 `MarkFailed`                                                 |
| `workspace_ref_created`  | `AppendToArtifacts(messageID, ref)` | 写到 `artifacts` JSONB                                                                           |
| `final_answer`           | `MarkCompleted(...)`         | 落 final_answer_ref / citations / artifacts / tokens / cost                                    |
| `error`                  | `MarkFailed(...)`            | 落 error_type / error                                                                            |
| (无事件)用户点停止按钮   | `MarkCancelled(...)`         | 同时调 `WorkflowLauncher.Cancel(workflow_id)`                                                   |

**强制规范**：

* handler 只能解析 JSON、调 service、返回 JSON;**不**写 SQL、**不**直接操作 Temporal、**不**直接订阅 Redis Stream
* lifecycle service 内部所有 DB 写走 `chatRepo`;**不**让 handler 临时 `db.Exec(...)`
* raw workflow event 严格**不**持久化到 `chat_messages`;只能经归一化后,以产品字段(`routing_decision` / `artifacts` / `status_text`)写 metadata
* 流式 `AppendDelta` 必须 debounce(200ms 阈值或 1KB 阈值,先到先触发);不允许每 token 写库

**新增文件**：

```
internal/service/
  chat_message_lifecycle.go
  chat_message_lifecycle_test.go   # 单元测试:状态机迁移 + 并发 append + cancel 竞态
  workflow_launcher.go             # 封装 Temporal client + Router
  workflow_launcher_test.go
internal/api/product/
  common.go                        # 响应包装 / 错误码 / 鉴权占位
  chat_handlers.go                 # 薄壳,只调 lifecycle
  task_handlers.go                 # 薄壳,只调 taskSvc
  approval_handlers.go             # 薄壳,调 approvalSvc + lifecycle
  workspace_handlers.go            # 薄壳,调 workspaceSvc
  provider_handlers.go             # 薄壳,调 providerRepo(8A 已有)
  system_handlers.go               # /health, /readiness, /version
internal/api/router.go             # 追加路由
```

**8B 验收**：

* `curl POST /api/v1/chat/sessions` → 调 `chatRepo.CreateSession` → 返回 `{ data: { cribug_session_id, ... } }`
* `curl POST /api/v1/chat/sessions/{cribug_session_id}/messages` → 调 `lifecycle.CreateUserMessage` + `lifecycle.CreateAssistantMessage`(**后者内部走 WorkflowLauncher,不是 HTTP**) → 返回 `{ data: { user_message_id, assistant_message_id, workflow_id, status: "pending" } }`
* `curl GET /api/v1/chat/sessions/{cribug_session_id}/messages` → 调 `chatRepo.ListBySession` → 按 `created_at` 升序返回
* `curl POST /api/v1/chat/messages/{message_id}/cancel` → 调 `lifecycle.MarkCancelled` + `WorkflowLauncher.Cancel(workflow_id)`(service 内)
* 反例检查:`grep -r "http.Post\|http.Get" internal/api/product/` 必须 0 命中(`http.Get` 用于 health 探活除外)
* 反例检查:`grep -r "db.Exec\|db.Query" internal/api/product/` 必须 0 命中(SQL 只在 `internal/db/`)
* `go test ./internal/service/...` 通过(状态机迁移 + cancel 竞态 + debounce 行为)

**`/api/v1/system/health` 等 system handler**：

* `system_handlers.go` 走 health/readiness 探活,**允许**用 `http.Get` 调 LLM service / Temporal——因为它们是外部服务,不是自调
* 但**绝对不**调自己 Gateway 的 `/api/v1/*`

---

### 10.3 Phase 8C — SSE / Event Bridge

**目标**：把 workflow / router / approval / research 事件整理成 Web 可消费的事件流。

**统一事件 Schema**（草案，最终以代码为准）：

```json
{
  "event_id": "evt_01HXY...",
  "type": "task_progress",
  "workflow_id": "wf-12345",
  "run_id": "run-abc",
  "session_id": "sess-xyz",
  "message_id": "msg-789",
  "ts": "2026-06-01T12:34:56.789Z",
  "data": { ... }            // 类型相关 payload
}
```

**12 类统一事件**（§十三 详细定义）：

1. `message_delta` — 流式文本增量
2. `router_decision` — Advanced Router 决策
3. `task_started` — workflow 启动
4. `task_progress` — 通用进度（百分比 / 当前阶段 / 当前节点）
5. `workflow_event` — workflow 原始事件（agent / llm / tool / reflection / tot / debate / research）
6. `tool_call` — 工具调用（MCP / Skills / Sandbox）
7. `rag_retrieval` — RAG 命中
8. `sandbox_event` — Sandbox 事件
9. `approval_requested` — 等待人工审批
10. `approval_resolved` — 审批已决
11. `workspace_ref_created` — 长内容写到 workspace
12. `final_answer` / `error` — 终止事件

**新增文件**：

```
internal/events/web_events.go         # 12 类常量 + WebEvent struct
internal/api/product/sse_bridge.go    # 订阅 Redis Stream → 输出 SSE
```

**强制规范**：

* `event_id` 来自 Redis Stream message ID（`XADD` 自动生成 `1234-0`）
* `last_event_id` 续传：`XREAD ... ID {last_event_id}` 跳过已读
* 心跳：每 30s 写 `:` 注释行（SSE comment）
* 30s 无新事件 → 心跳；60s 无任何数据 → 关闭 + 让前端重连
* 客户端断线 → 立即停止订阅（避免泄漏 Redis connection）
* **不**在 SSE 通道里发大对象（> 4 KB）→ 用 `workspace_ref_created` 通知前端去拉 `GET /api/v1/workspace/objects/{ref}`

**验收**：

* `curl -N "http://localhost:8080/api/v1/stream/sse?workflow_id=...&last_event_id=1234-0"` 流式返回
* 断网 30s → 仍收到心跳
* 断网 5min 重连 → `last_event_id=1234-0` 续读，不丢事件
* 任务完成 → 收到 `final_answer` 或 `error` 终止事件
* 同一 workflow 多个客户端订阅 → 各自独立续传

---

### 10.4 Phase 8D — Frontend Foundation

**目标**：建立 Web 前端工程基础。**不做 Tauri 桌面打包**。

**目录选择**（推荐 `web/`，理由见下）：

| 选项           | 优点                                  | 缺点                                                       | 推荐 |
| -------------- | ------------------------------------- | ---------------------------------------------------------- | ---- |
| `web/`         | 通用、与 Shannon `desktop/` 区分清楚  | 略冗余（`web/web/`）                                       | ✅    |
| `frontend/`    | Next.js 生态常见命名                  | 与 `cmd/gateway` `cmd/worker` 命名风格不统一                | ⚠️   |
| `apps/web/`    | monorepo 风格                         | 当前 Cribug 不是 monorepo，没有 `pnpm-workspace.yaml`       | ❌    |
| `ui/`          | 简洁                                  | 与"用户界面"歧义                                            | ❌    |

**最终选择**：`web/`（Cribug 项目根下，与 `cmd/` `internal/` `migrations/` `scripts/` `config/` 并列）。

**技术栈**（最终以仓库实际环境为准）：

* **Next.js 14 LTS** + App Router（避免 Next 15/16 已知 regression）
* **React 18** + TypeScript
* **Tailwind CSS 3**（与 Shannon Tailwind 4 区分；3.x 更稳）
* **Zustand**（轻量；Redux 仅在需要 middleware 时再加）
* **SSE**：原生 `EventSource`（封装 `web/lib/events/sse.ts`）+ `useRunStream` hook
* **API Client**：`web/lib/api/cribug.ts`（fetch wrapper，类型化）
* **Markdown**：`react-markdown` + `remark-gfm` + `rehype-highlight`（与 Shannon 一致；Cribug Lite 可选 highlight）
* **UI 组件**：Radix UI primitives（与 Shannon 一致；Cribug Lite 可用 headless + Tailwind 手搓）

**目录结构**：

```
web/
  package.json
  tsconfig.json
  next.config.mjs
  tailwind.config.ts
  postcss.config.mjs
  .env.example                 # NEXT_PUBLIC_API_URL=http://localhost:8080
  README.md
  app/
    layout.tsx                 # 根 layout（Provider / Theme / Store）
    globals.css
    page.tsx                   # 落地 → 重定向到 /chat/new 或 /chat/latest
    (app)/
      layout.tsx               # 鉴权占位 + Sidebar
      chat/
        page.tsx               # 默认重定向到 /chat/new
        new/
          page.tsx             # 新建会话
        [sessionId]/
          page.tsx             # Chat UI 主页面
      workspace/
        [ref]/
          page.tsx             # Workspace / Report Viewer
      settings/
        page.tsx               # Provider Settings
  components/
    chat/
      SessionSidebar.tsx
      Conversation.tsx
      MessageBubble.tsx
      Input.tsx
      TypingIndicator.tsx
    task/
      RoutingDecisionCard.tsx
      Timeline.tsx
      ProgressBar.tsx
    approval/
      ApprovalCard.tsx
      PendingList.tsx
    workspace/
      ReportViewer.tsx
      CitationList.tsx
    settings/
      ProviderConfigForm.tsx
      ConnectionTestButton.tsx
    ui/                        # 基础 UI（Button / Dialog / Tooltip / ...）
  lib/
    api/
      cribug.ts                # base fetch wrapper
      chat.ts                  # chat API methods
      tasks.ts
      approvals.ts
      workspace.ts
      providers.ts
      system.ts
    events/
      sse.ts                   # EventSource wrapper
      useRunStream.ts          # React hook
      reducer.ts               # event reducer
    store/
      chat.ts                  # Zustand store
      settings.ts
    types/
      api.ts                   # request / response types
      events.ts                # 12 类事件 type union
      domain.ts                # Session, Message, Approval, Workspace types
  styles/
    tailwind.css
  public/
    favicon.ico
  tests/
    e2e/                       # Playwright
    unit/                      # Vitest
```

**关键约定**：

* 所有 API 调用走 `lib/api/*.ts`，**不**在组件里直接 `fetch`
* SSE 订阅走 `useRunStream` hook，**不**在组件里直接 `new EventSource`
* 长内容（Workspace / Report）走单独路由 `/workspace/[ref]`，**不**塞进消息气泡
* 鉴权占位：dev 模式 `X-User-Id: dev-user`；生产模式待 Phase 9
* 主题：默认 dark mode（与 GPT 一致）；light mode 可切换

**验收**：

* `pnpm dev`（或 `npm run dev`）启动 → `http://localhost:3000` 打开
* `NEXT_PUBLIC_API_URL=http://localhost:8080` 配置正确
* Sidebar 渲染；`/chat/new` 路由可用
* 不接后端也能渲染空 UI（前端 mock 数据）

---

### 10.5 Phase 8E — GPT-like Chat UI

**目标**：浏览器中的基本 ChatGPT-like 对话体验。

**关键组件**：

* `SessionSidebar`：左侧会话列表 + 新建按钮 + 搜索（Phase 8 v2.0 可选搜索）
* `Conversation`：消息流；自动滚动到底部；Streaming 时光标闪烁
* `MessageBubble`：按 `role` 渲染（user 右对齐 / assistant 左对齐 / status 中间 / approval 居中卡片）
* `Input`：底部输入框 + Provider 切换 + 发送按钮；Streaming 时显示"停止"按钮
* `TypingIndicator`：3 个跳动圆点

**强制规范**：

* 调用 `POST /api/v1/chat/sessions/{id}/messages` → 拿 `message_id + workflow_id`
* 立刻 `useRunStream(workflow_id)` 订阅 SSE
* `message_delta` 事件 → 追加到本地 assistant message content（乐观更新）
* `final_answer` 事件 → 标记 message `status=completed`，关闭 SSE
* 失败 → `error` 事件 → 标记 `status=failed`，显示错误信息
* **不**直接 polling（用 SSE）；SSE 断线 30s 后可降级到 `GET /api/v1/tasks/{workflow_id}` 拉一次

**多轮对话**：

* 每条 user message → 新 workflow（与 Cribug Phase 1 一致；session_id 关联）
* assistant message 引用 workflow_id
* `parent_message_id` 形成树（Phase 8 v2.0 可以只支持线性多轮，分支留 Phase 9）

**验收**：

* 输入"法国首都是哪里？" → 几秒后看到流式回答"巴黎"
* 输入"继续"（多轮）→ session 上下文保留
* 输入"计算 123 * 456" → router 选 tool → 流式输出 "56088"
* Streaming 时按"停止" → workflow cancel
* 刷新页面 → 历史会话 + 历史消息完整恢复

---

### 10.6 Phase 8F — Router / Workflow Visibility Panel

**目标**：展示 Cribug 相比普通 Chat UI 的 Agent Runtime 信息。这是 Cribug 的差异化亮点。

**展示字段**（`RoutingDecisionCard`）：

* `planned_mode`（Router 计划）
* `executed_mode`（实际执行；可能因 fallback 不同）
* `fallback_reason`（如果 fallback，注明原因）
* `risk_level`（low / medium / high / critical）
* `complexity_score`（0-1）
* `requires_approval` / `requires_rag` / `requires_tools` / `requires_sandbox`（布尔位）
* `workflow_id` / `run_id` / `current status`
* `token_estimate` / `cost_estimate`
* `confidence`（Phase 7 计划中可省略）

**Timeline 组件**：

* 折叠 / 展开 4 类事件（agent / llm / tool / system）
* 可选「显示 JSON 详情」
* 时间倒序或正序可切换

**数据源**：

* `GET /api/v1/tasks/{workflow_id}/routing-decision` → 一次性拉取 Router 决策
* `useRunStream` → 持续拉 `workflow_event` + `task_progress`
* `GET /api/v1/tasks/{workflow_id}/events` → 拉历史（用于断线重连补数据）

**验收**：

* 复杂问题（"分析这个 PDF 报告"）→ 卡片显示 `requires_rag=true` + `planned_mode=dag`
* Sandbox 问题 → 卡片显示 `requires_sandbox=true` + `requires_approval=true`
* Timeline 显示 "agent_started → tool_call(MCP:filesystem) → llm_call → reflection_round_1 → reflection_round_2 → final"

---

### 10.7 Phase 8G — Approval UI

**目标**：把 Phase 7B Approval API 产品化。

**入口**：

* **聊天气泡中**：assistant message 流中收到 `approval_requested` 事件 → 在 conversation 中插入一张 inline `ApprovalCard`
* **侧边栏**：`/approvals/pending` 列出所有 pending 审批（Phase 8 v2.0 可以合并到主 Sidebar，也可以独立页）

**`ApprovalCard` 字段**：

* `approval_id` / `workflow_id` / `query` / `risk_level` / `mode` / `reason` / `expires_at`
* 三个按钮：**Approve** / **Reject** / **Modify**
* "Modify" 展开文本框 → 用户编辑 `proposed_action` → 提交

**强制规范**：

* 调 `POST /api/v1/approvals/{approval_id}/respond`（已存在 handler）
* 收到 `approval_resolved` 事件 → 移除卡片
* Reject → 标记 workflow `status=cancelled`，assistant message `status=cancelled`
* Approve → workflow 继续 resume
* Timeout → 展示 "Approval expired, workflow was rejected for safety"

**验收**：

* 高风险问题 → 流式回答中断 → 出现 Approval Card
* 点 Reject → workflow 终止，前端展示拒绝原因
* 点 Approve → workflow 恢复，输出最终结果
* Modify → 编辑 action → 提交 → workflow 继续

---

### 10.8 Phase 8H — Workspace / Research Report Viewer

**目标**：让用户能打开和查看 Phase 5 Workspace、Phase 7 Research v2、Reflection、Debate、ToT 产生的长内容。

**入口**：

* **MessageBubble 内的链接**：assistant message 流中收到 `workspace_ref_created` 事件 → 渲染一个"📄 View Report"按钮
* 点击 → 跳转 `/workspace/{ref}` 或 弹 Modal

**新增 API**：

```
GET  /api/v1/workspace/objects/{ref}        # 读取单个 workspace object
GET  /api/v1/workspace/objects/{ref}/children  # 列出子对象
GET  /api/v1/reports/{report_ref}            # 读取研究报告（合并 multiple workspace_objects）
GET  /api/v1/citations/{ref}                 # 读取引用详情
```

**强制规范**：

* 读 API **只**返回 ref 内容 + metadata + size，**不**返回 secrets
* **不**做服务端 markdown 渲染（前端 `react-markdown` 渲染）
* **不**做 PDF 转换（Phase 8 v2.0 不做；Phase 9+）
* 长文本懒加载：先拉 metadata，再按需拉 content

**数据源**：

* `workspace_objects`（Postgres）—— Research v2 / Swarm / Reflection 写入的长文本
* `workspace_topics`（Redis）—— append-only 流（Phase 5 冻结）
* 引用 / source URI → 走 Phase 6E documents + Phase 6F RAG

**验收**：

* Research v2 报告 → 浏览器渲染 Markdown（标题 / 段落 / 列表 / 表格 / 代码块）
* 引用列表 → 点击跳转到 source（如果存在）
* Artifact（如 reflection_history）→ 折叠 JSON
* 大报告（> 100 KB）→ 分页 / 懒加载（Phase 8 v2.0 可以不分页，先测性能）

---

### 10.9 Phase 8I — Basic Model Provider Settings（MVP 简化版）

**目标**：让单用户本地开发环境能通过 Web 配置基本 LLM Provider,真正把 LLM 调通。**Phase 8 v2.0 不做完整的 secret vault、不做 OS keychain、不做多租户密钥管理**;走最简可行的加密存储方案。

**核心设计**：

* `web/app/(app)/settings/page.tsx` 路由
* `ProviderConfigForm`：每个 provider 一个表单(provider name / base_url / API key / default chat model / default embedding model / 是否启用)
* `ConnectionTestButton`：调 `POST /api/v1/config/model-providers/test` 验证连通性(**只验不存**)

**最小可用 Provider 列表**（Phase 8 v2.0 必须支持）：

* OpenAI(chat + embedding)
* Anthropic(chat)
* Google Gemini(chat + embedding)
* 自定义 OpenAI-compatible(base_url + API key)

**API Key 存储方案 — MVP(明确简化)**：

> **Phase 8 v2.0 不做** OS keychain 集成、不做 Vault 集成、不做 HSM、不做 KMS。
> 只做 **AES-256-GCM 加密后存 Postgres**,加密 key 来自 `CRIBUG_PROVIDER_AES_KEY` 环境变量。

* **存储**：`provider_configs.api_key_encrypted BYTEA`(nonce ‖ ciphertext ‖ tag,12 + N + 16 字节)
* **加密**：AES-256-GCM;nonce 每次写入随机生成
* **Key 来源**：`CRIBUG_PROVIDER_AES_KEY`(base64 编码的 32 字节)
  * **开发模式**：从 `.env` 读;`.env` 不进 git(已有 `.gitignore`)
  * **生产模式**(MVP 不做,Phase 9+ 再说)：由部署平台注入 secret
* **不**做的事(明确推后到 Phase 9+):
  * ❌ OS keychain(macOS Keychain / Windows Credential Manager / Linux Secret Service)
  * ❌ HashiCorp Vault / AWS Secrets Manager / GCP Secret Manager
  * ❌ 多租户密钥隔离
  * ❌ 密钥轮转 / 审计 / 撤销
  * ❌ HSM / KMS

**风险声明**(必须写进 README 和 Settings 页面)：

> **MVP 模式的安全边界**:
> 1. `.env` 中的 `CRIBUG_PROVIDER_AES_KEY` 是唯一的"主密钥"——它泄露,所有 provider key 都能解密
> 2. 本模式仅适用于**单用户本地开发**;多用户 / 远程部署**必须**升级到 OS keychain 或外部 vault(Phase 9+)
> 3. 升级路径:`api_key_encrypted` 列保留;只需把 `providerRepo.LoadDecrypted` 改成走 keychain,旧密文可以批量 re-encrypt

**新增 Migration 015**(已在 §10.1 给出草案,这里不再重复):`provider_configs` 表 + `api_key_encrypted BYTEA` 列。

**新增 Migration 016**(`016_app_settings.sql`,同上):`app_settings` 通用 KV 表,存 default provider / default model tier 等**非敏感**运行时配置。

**新增文件**：

```
internal/db/provider_repo.go            # 8A 已有,带 AES-GCM 加解密
internal/crypto/aesgcm.go               # 通用 AES-256-GCM helper(包装 stdlib)
internal/crypto/aesgcm_test.go          # round-trip / 错误 key / 错误 nonce 测试
internal/api/product/provider_handlers.go  # 薄壳
internal/llm/provider_registry.go       # 整合 provider_configs + 现有 Python LLM service
```

**强制规范**：

* `POST /api/v1/config/model-providers/test` → **不**存盘,只验连通性
* `PUT /api/v1/config/model-providers/{id}` → 用 `CRIBUG_PROVIDER_AES_KEY` 加密后存 `api_key_encrypted`
* `GET /api/v1/config/model-providers` → **不**返回明文 key,返回 `{ ..., api_key_present: true/false, key_fingerprint: "sha256:abc123..." }`(`key_fingerprint` 是 sha256(key) 的前 12 字符,用于 UI 区分"哪个 key")
* **不**在前端 localStorage / sessionStorage 缓存 API Key
* **不**把 API Key 写到任何 audit log / SSE payload / 错误响应
* 启动时若 `CRIBUG_PROVIDER_AES_KEY` 缺失 → 启动失败,提示 `CRIBUG_PROVIDER_AES_KEY must be set`(`config.Load()` 在 `cmd/gateway/main.go` 早期检查)
* 启动时若 key 长度不是 32 字节(原始)/ 44 字符(base64) → 启动失败,提示长度

**MVP 验收**：

* 启动:`CRIBUG_PROVIDER_AES_KEY=$(openssl rand -base64 32) ./bin/gateway` → 启动成功
* Settings 配 OpenAI key → Test 成功 → 保存
* 重启服务(`CRIBUG_PROVIDER_AES_KEY` 不变)→ 配置仍然有效
* `GET /api/v1/config/model-providers` 响应中 `api_key_present=true`,**无**明文
* `grep -r "sk-ant\|sk-\|AIza" /var/log/*` 必须 0 命中
* 删除配置 → `api_key_encrypted` 置 NULL
* Phase 9 升级:不破坏 schema,只换 `providerRepo.LoadDecrypted` 实现

**Phase 9+ 升级路径(留口子)**：

* `api_key_ref VARCHAR(200) NOT NULL DEFAULT ''` 列已经预留(在 8A schema 里),Phase 9 可用 keychain 模式写入
* `providerRepo` 接口保持稳定;keychain 实现作为新加的 `Provider` struct 字段
* `.env` → keychain 的迁移脚本:`scripts/migrate_provider_keys_to_keychain.sh`(Phase 9 再写)

---

### 10.10 Phase 8J — End-to-End Web Usability Test

**目标**：验证 Phase 7 + Phase 8 收尾后 Cribug 可通过 Web 使用,包含 3 个层次的 E2E:

* **L1 集成 E2E**(curl + bash,默认必跑,无 API key)
* **L2 真实 LLM E2E**(curl + bash,`REAL_WEB_LLM_TEST=1`,需要 API key)
* **L3 真实浏览器 E2E**(Playwright,`REAL_WEB_LLM_TEST=1`,需要 API key + 起 Web dev server)

**测试脚本**(`scripts/`)：

```
scripts/test_phase8_chat_persistence.sh      # 8A: chat_sessions / chat_messages 持久化(repo 层)
scripts/test_phase8_product_api.sh           # 8B: API smoke(不调 LLM)
scripts/test_phase8_lifecycle_state.sh       # 8B: assistant message 状态机迁移
scripts/test_phase8_no_http_self_call.sh     # 8B: 契约检查(grep http.Post/Get in product/* == 0)
scripts/test_phase8_sse_stream.sh            # 8C: SSE 事件流、断点续传、心跳
scripts/test_phase8_approval_e2e.sh          # 8B + 8G: Approval UI 联动
scripts/test_phase8_workspace_report.sh      # 8B + 8H: Workspace / Report 读取
scripts/test_phase8_provider_settings.sh     # 8I: Provider 配置 + AES 加解密 round-trip
scripts/test_phase8_web_e2e.sh               # L1: 完整 Web E2E(curl 模拟浏览器)
scripts/test_phase8_real_web_llm.sh          # L2: 真实 LLM(curl,不打开浏览器)
scripts/test_phase8_real_browser_llm.sh      # L3: 真实浏览器(Playwright)+ 真实 LLM,见下方详述
```

**L1 集成 E2E 路径**(`test_phase8_web_e2e.sh`,默认必跑):

1. 启动 Postgres + Redis + Temporal + Python LLM Service(mocker 模式)+ Gateway + Worker
2. `curl /api/v1/system/health` → 200,`components.llm_service.status=mock_ok`
3. `POST /api/v1/chat/sessions` → 拿到 `cribug_session_id`
4. `POST /api/v1/chat/sessions/{cribug_session_id}/messages` → 拿到 `user_message_id` + `assistant_message_id` + `workflow_id`
5. `curl -N /api/v1/stream/sse?workflow_id={wf}` → 看到 `router_decision(planned_mode=direct_answer)` + 多次 `message_delta` + `final_answer`
6. `GET /api/v1/chat/sessions/{cribug_session_id}/messages` → 看到 user + assistant 两条,assistant `status=completed`
7. 复杂问题 → `router_decision(planned_mode=dag, requires_rag=true)` + 多 `workflow_event`
8. Sandbox / 高风险问题 → `approval_requested` 事件
9. `POST /api/v1/approvals/{id}/respond -d '{"decision":"reject"}'` → `approval_resolved` 事件 + workflow 终止
10. 再次 `GET messages` → 历史完整
11. **契约检查**:`./scripts/test_phase8_no_http_self_call.sh` → `internal/api/product/` 不得出现 `http.Post` / `http.Get`(system_handlers 的外部 health 探活除外)

**L2 真实 LLM E2E 路径**(`test_phase8_real_web_llm.sh`,`REAL_WEB_LLM_TEST=1`):

* 与 L1 相同,但 `components.llm_service.status=ok`;通过 `POST /api/v1/config/model-providers/openai` 注入真实 API key
* 验证:发"法国首都是哪里?" → 5 秒内收到 `final_answer`,`answer_preview` 包含 "Paris"
* 验证:发 sandbox 风险问题 → 收到 `approval_requested`
* 验证:发 reflection 类问题 → `workflow_event` 包含 "reflection_round"
* 成本上限:$1 / run

**L3 真实浏览器 E2E 路径**(`test_phase8_real_browser_llm.sh`,`REAL_WEB_LLM_TEST=1`)— **本计划书新增**:

> Phase 8 v2.0 把"真实浏览器到 LLM"列为正式验收项。L1/L2 走的是 HTTP API,可能掩盖前端集成 bug(如 SSE 在浏览器里断流、React 状态机错误、Provider UI 实际不工作等)。L3 用 Playwright 打开真的 Web 页面,模拟真人点击。

**L3 前置条件**：

```bash
export REAL_WEB_LLM_TEST=1
export OPENAI_API_KEY=sk-...           # 真实 LLM 必须
export ANTHROPIC_API_KEY=...           # 二选一即可
export PLAYWRIGHT_BROWSERS_PATH=...    # Playwright 浏览器路径
# Cribug Web dev server 必须启动
cd web && npm install && npm run build && npm run start &  # 127.0.0.1:3000
```

**L3 测试步骤**(Playwright + Chromium,headless):

```
1. 打开 http://localhost:3000 → 断言页面 title 包含 "Cribug"
2. 点击 "New chat" → 断言 URL 包含 /chat/new
3. 打开 /settings → 填入 OpenAI API key → 点 "Test Connection" → 断言 toast "Connected"
4. 保存 provider 配置 → 断言 "Provider saved" toast
5. 回到 /chat/new → 在输入框输入 "What is the capital of France?" → 点 "Send"
6. ★ 等待助手消息气泡出现,且 status=streaming
7. ★ 30s 内断言助手消息 status=completed
8. ★ 断言助手消息内容文本包含 "Paris"(不区分大小写)
9. 在 conversation 区域断言 "router_decision" 卡片出现(可选,Phase 8F 完成后断言)
10. 发送 "Calculate 123 * 456" → 等待流式结束 → 断言消息内容包含 "56088"
11. F5 刷新页面 → 断言两个会话都还在 sidebar
12. 点击第一个 session → 断言历史消息完整恢复
```

**L3 输出**：

```
✓ step 1: page loads (Cribug title)
✓ step 2: new chat route
✓ step 3: provider test connection
✓ step 4: provider saved
✓ step 5: user message submitted
✓ step 6: assistant message streaming
✓ step 7: assistant message completed within 30s
✓ step 8: answer contains "Paris"
✓ step 9: router_decision card visible
✓ step 10: tool call answer contains "56088"
✓ step 11: page refresh persists sessions
✓ step 12: history restore on session click
All 12 steps passed.
```

**L3 失败行为**：

* 任意步骤失败 → 截图保存到 `test-results/phase8-real-browser-*.png`;非零退出
* 30s 超时(步骤 7)→ 抓取页面 console log + network 截图
* 无 `OPENAI_API_KEY` 且 `REAL_WEB_LLM_TEST=1` → 脚本在步骤 3 直接 skip(打印 "SKIP: REAL_WEB_LLM_TEST=1 but no API key"),**不**让 L3 静默通过

**L3 新增文件**：

```
web/tests/e2e/
  chat.spec.ts                       # 真实 LLM 浏览器 smoke
  approval.spec.ts
  workspace.spec.ts
playwright.config.ts                 # web/ 顶层
scripts/test_phase8_real_browser_llm.sh  # 启动 Playwright + 解析结果
```

**Web 端手动验收**(开发者本地):

1. 打开 `http://localhost:3000`
2. Sidebar 显示会话列表(dev 模式可以先空)
3. 点击「New chat」
4. 输入"法国首都是哪里?" → 流式输出
5. 点击消息旁的「View Report」(如果有)→ `/workspace/{ref}` 渲染
6. 点击 Settings → 配置 OpenAI key → Test 成功
7. 输入 sandbox 风险问题 → Approval Card 出现 → 点击 Reject → 状态更新
8. 刷新页面 → 会话和消息完整

**REAL_WEB_LLM_TEST 行为总表**：

| 环境变量                          | L1 跑 | L2 跑 | L3 跑 | 无 key 行为       |
| --------------------------------- | ----- | ----- | ----- | ----------------- |
| 都不设(默认)                      | ✅    | ❌ skip | ❌ skip | (与 L1 一致)    |
| `REAL_WEB_LLM_TEST=1` + 有 key    | ✅    | ✅    | ✅    | (都跑)            |
| `REAL_WEB_LLM_TEST=1` + 无 key    | ✅    | ❌ skip(exit 0) | ❌ skip(exit 0) | skip 不 fail |

---

## 十一、API 设计

> 完整路径表。前缀统一 `/api/v1/`。

### 11.1 Chat / Session

```
POST   /api/v1/chat/sessions                        # 新建 session
GET    /api/v1/chat/sessions                        # 列 session
GET    /api/v1/chat/sessions/{session_id}           # session 详情
DELETE /api/v1/chat/sessions/{session_id}           # 删除 session
PATCH  /api/v1/chat/sessions/{session_id}           # 改 title / pinned / archived

GET    /api/v1/chat/sessions/{session_id}/messages  # 列 messages（按时间升序）
POST   /api/v1/chat/sessions/{session_id}/messages  # 发消息（内部 Router）
GET    /api/v1/chat/messages/{message_id}           # 单 message
POST   /api/v1/chat/messages/{message_id}/cancel    # 取消生成中 message
```

### 11.2 Task / Routing

```
GET    /api/v1/tasks/{workflow_id}                  # task 状态聚合
GET    /api/v1/tasks/{workflow_id}/events           # 事件历史（用于断线补数据）
GET    /api/v1/tasks/{workflow_id}/routing-decision # router 决策
GET    /api/v1/tasks/{workflow_id}/routing-events   # router 事件
POST   /api/v1/tasks/{workflow_id}/pause            # 暂停（Phase 7B signal）
POST   /api/v1/tasks/{workflow_id}/resume           # 恢复
POST   /api/v1/tasks/{workflow_id}/cancel           # 取消
```

### 11.3 Approval

```
GET    /api/v1/approvals/pending                    # 待审批列表
GET    /api/v1/approvals/{approval_id}              # 审批详情
POST   /api/v1/approvals/{approval_id}/respond      # 提交决策
GET    /api/v1/approvals/{approval_id}/audit        # 审批审计
```

### 11.4 Workspace / Report

```
GET    /api/v1/workspace/objects/{ref}              # 单 workspace object
GET    /api/v1/workspace/objects/{ref}/children     # 子对象
GET    /api/v1/workspace/topics/{topic}             # topic 流（Phase 5 兼容）
GET    /api/v1/reports/{report_ref}                 # 研究报告
GET    /api/v1/citations/{ref}                      # 引用详情
```

### 11.5 Provider / Settings

```
GET    /api/v1/config/model-providers               # 列 provider（不返回 key）
POST   /api/v1/config/model-providers               # 新建
PUT    /api/v1/config/model-providers/{id}          # 更新（含 API key）
DELETE /api/v1/config/model-providers/{id}          # 删除
POST   /api/v1/config/model-providers/test          # 测试连通性（不存盘）
PUT    /api/v1/config/runtime                       # 运行时配置（default model / mode 等）
```

### 11.6 System

```
GET    /api/v1/system/health                        # 健康检查
GET    /api/v1/system/readiness                     # readiness（worker / temporal / llm / db / redis）
GET    /api/v1/system/version                       # Cribug version
GET    /api/v1/system/capabilities                  # 启用的 features
```

### 11.7 SSE

```
GET    /api/v1/stream/sse?workflow_id={wf}&last_event_id={id}  # 主事件流
```

> 现有 `/api/v1/stream/sse?task_id=...` 路径保留兼容；Phase 8 v2.0 优先用 `workflow_id`，但 handler 内部仍然按 task 维度发事件。

### 11.8 响应规范

```json
// 成功
{
  "data": { ... },
  "meta": { "request_id": "req-..." }
}

// 失败
{
  "error": {
    "type": "validation_error",
    "message": "query is required",
    "code": 400
  }
}
```

---

## 十二、DB / Migration 设计

> 新增 migration 013-016。**不**修改 001-012。

### 12.1 Migration 013 — `chat_sessions`

```sql
-- 设计草案，详见 §10.2
CREATE TABLE chat_sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id VARCHAR(100) NOT NULL UNIQUE,
    user_id VARCHAR(100) NOT NULL DEFAULT 'dev-user',
    title VARCHAR(500) NOT NULL DEFAULT '',
    summary TEXT NOT NULL DEFAULT '',
    message_count INT NOT NULL DEFAULT 0,
    last_message_at TIMESTAMPTZ,
    pinned BOOLEAN NOT NULL DEFAULT FALSE,
    archived BOOLEAN NOT NULL DEFAULT FALSE,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX idx_chat_sessions_user ON chat_sessions(user_id);
CREATE INDEX idx_chat_sessions_last_message ON chat_sessions(last_message_at DESC);
```

### 12.2 Migration 014 — `chat_messages` + `message_task_links`

```sql
-- 设计草案
CREATE TABLE chat_messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    message_id VARCHAR(100) NOT NULL UNIQUE,
    session_id VARCHAR(100) NOT NULL,
    parent_message_id VARCHAR(100) NOT NULL DEFAULT '',
    role VARCHAR(20) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    content TEXT NOT NULL DEFAULT '',
    preview TEXT NOT NULL DEFAULT '',
    final_answer_ref VARCHAR(200) NOT NULL DEFAULT '',
    citations JSONB NOT NULL DEFAULT '[]'::jsonb,
    artifacts JSONB NOT NULL DEFAULT '[]'::jsonb,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    error_type VARCHAR(50) NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    CONSTRAINT chat_messages_role_check CHECK (role IN ('user','assistant','system','status','approval'))
);
CREATE INDEX idx_chat_messages_session ON chat_messages(session_id, created_at);
CREATE INDEX idx_chat_messages_status ON chat_messages(status);

CREATE TABLE message_task_links (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    message_id VARCHAR(100) NOT NULL,
    workflow_id VARCHAR(255) NOT NULL,
    task_id UUID,
    routing_decision JSONB NOT NULL DEFAULT '{}'::jsonb,
    approval_id VARCHAR(100) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    CONSTRAINT message_task_links_unique UNIQUE (message_id, workflow_id)
);
```

### 12.3 Migration 015 — `provider_configs`

```sql
-- 设计草案，详见 §10.9
CREATE TABLE provider_configs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id VARCHAR(100) NOT NULL DEFAULT 'dev-user',
    provider_name VARCHAR(50) NOT NULL,
    label VARCHAR(200) NOT NULL DEFAULT '',
    base_url TEXT NOT NULL DEFAULT '',
    api_key_encrypted BYTEA,
    api_key_ref VARCHAR(200) NOT NULL DEFAULT '',
    chat_model VARCHAR(100) NOT NULL DEFAULT '',
    embedding_model VARCHAR(100) NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    CONSTRAINT provider_configs_unique UNIQUE (user_id, provider_name, label)
);
```

### 12.4 Migration 016 — `app_settings`

```sql
-- 设计草案
CREATE TABLE app_settings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id VARCHAR(100) NOT NULL DEFAULT 'dev-user',
    key VARCHAR(100) NOT NULL,
    value JSONB NOT NULL,
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    CONSTRAINT app_settings_unique UNIQUE (user_id, key)
);
```

### 12.5 显式禁止（与 §8.8 一致）

* ❌ `chat_messages.content` 不存 > 4 KB 长文本
* ❌ `chat_messages` 不存完整 prompt / completion
* ❌ `provider_configs` 不存明文 API key
* ❌ `message_task_links` 不存完整 routing audit（那是 `routing_audit_logs` 的事）

---

## 十三、SSE / Event Schema 设计

### 13.1 12 类统一事件

> Phase 8 v2.0 收敛到 12 类（Shannon 有 30+）。后续可增量。

| `type`                    | 触发时机                                | `data` payload 草案                                                                                                                            |
| ------------------------- | --------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------- |
| `message_delta`           | 流式回答新增 token                       | `{ "message_id": "...", "delta": "...", "index": 42 }`                                                                                          |
| `router_decision`         | Advanced Router 完成决策                  | `{ "decision": { "planned_mode": "dag", "executed_mode": "...", "complexity_score": 0.7, "risk_level": "medium", "requires_approval": false, ... } }` |
| `task_started`            | workflow 启动                            | `{ "workflow_id": "...", "run_id": "...", "mode": "direct_answer" }`                                                                            |
| `task_progress`           | 通用进度                                 | `{ "phase": "research", "progress": 0.6, "current_node": "...", "message": "..." }`                                                            |
| `workflow_event`          | workflow 原始事件（归一化后）            | `{ "raw_type": "AGENT_STARTED", "agent": "researcher", "step": 1, "details": {...} }`                                                            |
| `tool_call`               | 工具调用                                 | `{ "tool": "mcp:filesystem", "action": "read", "args_preview": "...", "result_preview": "..." }`                                                |
| `rag_retrieval`           | RAG 命中                                 | `{ "query": "...", "hits": 5, "top_score": 0.92, "sources": ["doc-1", "doc-2"] }`                                                                |
| `sandbox_event`           | Sandbox 事件                             | `{ "sandbox_id": "...", "status": "running", "stdout_preview": "..." }`                                                                          |
| `approval_requested`      | 等待人工审批                              | `{ "approval_id": "...", "workflow_id": "...", "query": "...", "risk_level": "high", "mode": "sandbox", "reason": "...", "expires_at": "..." }` |
| `approval_resolved`       | 审批已决                                 | `{ "approval_id": "...", "decision": "approve|reject|modify", "feedback_summary": "..." }`                                                      |
| `workspace_ref_created`   | 长内容写到 workspace                     | `{ "ref": "ws:research-2026-06-01-001", "type": "research_report", "title": "...", "size": 12345, "url": "/workspace/ws:..." }`                  |
| `final_answer`            | workflow 成功完成                        | `{ "message_id": "...", "answer_preview": "...", "final_answer_ref": "ws:...", "tokens": { "prompt": 123, "completion": 456, "total": 579 }, "duration_ms": 12345 }` |
| `error`                   | workflow / system 失败                  | `{ "code": "...", "message": "...", "details": {...}, "recoverable": true|false }`                                                              |

### 13.2 通用 Event Envelope

```json
{
  "event_id": "evt_1234-0",
  "type": "router_decision",
  "workflow_id": "wf-abc",
  "run_id": "run-xyz",
  "session_id": "sess-123",
  "message_id": "msg-456",
  "ts": "2026-06-01T12:34:56.789Z",
  "data": { ... }
}
```

### 13.3 SSE 协议细节

* `Content-Type: text/event-stream`
* `Cache-Control: no-cache`
* `Connection: keep-alive`
* `X-Accel-Buffering: no`（防止 nginx 缓冲）
* 每条事件：`id: <event_id>\nevent: <type>\ndata: <json>\n\n`
* 心跳：每 30s 一行 `:\n\n`（SSE comment）
* `last_event_id` 续传：客户端重连时把上一次 `event_id` 放入 `Last-Event-ID` header 或 query param

### 13.4 客户端契约（前端）

```ts
// web/lib/events/sse.ts
type SSEEvent = {
  event_id: string;
  type:
    | 'message_delta' | 'router_decision' | 'task_started' | 'task_progress'
    | 'workflow_event' | 'tool_call' | 'rag_retrieval' | 'sandbox_event'
    | 'approval_requested' | 'approval_resolved' | 'workspace_ref_created'
    | 'final_answer' | 'error';
  workflow_id: string;
  run_id: string;
  session_id: string;
  message_id: string;
  ts: string;
  data: Record<string, unknown>;
};

function useRunStream(workflowId: string, opts: {
  lastEventId?: string;
  onEvent: (e: SSEEvent) => void;
  onError?: (e: Error) => void;
}) { ... }
```

---

## 十四、Frontend 目录结构设计

> 见 §10.4 Phase 8D。此处补充关键约定。

```
web/
├── app/                                # Next.js App Router
│   ├── layout.tsx                      # 根 layout（Store / Theme / ErrorBoundary）
│   ├── page.tsx                        # 首页（重定向到 /chat）
│   ├── (app)/                          # 鉴权占位（dev 模式透传）
│   │   ├── layout.tsx                  # Sidebar + Main
│   │   ├── chat/
│   │   │   ├── page.tsx                # /chat → 重定向 /chat/new
│   │   │   ├── new/page.tsx            # /chat/new
│   │   │   └── [sessionId]/page.tsx    # /chat/{id}
│   │   ├── workspace/
│   │   │   └── [ref]/page.tsx          # /workspace/{ref}
│   │   ├── approvals/
│   │   │   └── page.tsx                # /approvals（pending 列表）
│   │   └── settings/
│   │       └── page.tsx                # /settings
│   └── api/                            # Next.js API routes（仅 proxy 用）
│       └── proxy/[...path]/route.ts    # 可选：把 /api/* 代理到 Gateway
│
├── components/                         # 业务组件
│   ├── chat/
│   │   ├── SessionSidebar.tsx
│   │   ├── Conversation.tsx
│   │   ├── MessageBubble.tsx
│   │   ├── Input.tsx
│   │   ├── TypingIndicator.tsx
│   │   └── ProviderBadge.tsx
│   ├── task/
│   │   ├── RoutingDecisionCard.tsx
│   │   ├── Timeline.tsx
│   │   ├── ProgressBar.tsx
│   │   └── EventDetails.tsx
│   ├── approval/
│   │   ├── ApprovalCard.tsx
│   │   ├── ApprovalForm.tsx
│   │   └── PendingList.tsx
│   ├── workspace/
│   │   ├── ReportViewer.tsx
│   │   ├── CitationList.tsx
│   │   └── ArtifactTree.tsx
│   ├── settings/
│   │   ├── ProviderConfigForm.tsx
│   │   ├── ConnectionTestButton.tsx
│   │   └── RuntimeSettingsForm.tsx
│   └── ui/                             # 基础 UI
│       ├── Button.tsx
│       ├── Dialog.tsx
│       ├── Tooltip.tsx
│       └── ScrollArea.tsx
│
├── lib/
│   ├── api/
│   │   ├── cribug.ts                   # fetch wrapper
│   │   ├── chat.ts
│   │   ├── tasks.ts
│   │   ├── approvals.ts
│   │   ├── workspace.ts
│   │   ├── providers.ts
│   │   └── system.ts
│   ├── events/
│   │   ├── sse.ts                      # EventSource wrapper
│   │   ├── useRunStream.ts             # React hook
│   │   └── reducer.ts                  # event reducer
│   ├── store/
│   │   ├── chat.ts                     # Zustand
│   │   ├── settings.ts
│   │   └── index.ts
│   └── types/
│       ├── api.ts
│       ├── events.ts
│       └── domain.ts
│
├── styles/
│   └── tailwind.css
│
├── public/
│
├── tests/
│   ├── e2e/                            # Playwright
│   │   ├── chat.spec.ts
│   │   ├── approval.spec.ts
│   │   └── workspace.spec.ts
│   └── unit/                           # Vitest
│       ├── api.test.ts
│       └── events.test.ts
│
├── package.json
├── tsconfig.json
├── next.config.mjs
├── tailwind.config.ts
├── postcss.config.mjs
├── .env.example
└── README.md
```

---

## 十五、Backend 目录结构设计

> Phase 8 v2.0 在 Cribug 已有结构上**新增** `internal/api/product/` 子包，**不**改 `internal/api/router.go` 之外的现有结构。

```
cribug/
├── cmd/
│   ├── gateway/main.go                # 启动时注册 /api/v1/* 路由
│   └── worker/main.go
├── internal/
│   ├── api/
│   │   ├── router.go                  # 现有：/health + /api/v1/{tasks, mcp, sandbox, skills, hooks, router, approvals}
│   │   ├── handler.go                 # 现有：createTask / getTask / streamTaskEvents
│   │   ├── approval_handlers.go       # 现有（Phase 7B）
│   │   ├── router_handlers.go         # 现有（Phase 7A）
│   │   ├── hooks_handlers.go          # 现有（Phase 6D）
│   │   ├── mcp_handlers.go            # 现有（Phase 6A）
│   │   ├── sandbox_handlers.go        # 现有（Phase 6B）
│   │   ├── skills_handlers.go         # 现有（Phase 6C）
│   │   ├── middleware.go              # 现有
│   │   └── product/                   # 新增（Phase 8A-8I）
│   │       ├── common.go              # 响应包装 / 错误 / 鉴权占位
│   │       ├── chat_handlers.go       # session / message
│   │       ├── task_handlers.go       # task 状态 / events 聚合
│   │       ├── approval_handlers.go   # 包一层 product 风格
│   │       ├── workspace_handlers.go  # workspace / report / citation
│   │       ├── provider_handlers.go   # provider config
│   │       ├── system_handlers.go     # health / readiness / version
│   │       └── sse_bridge.go          # SSE / Event Bridge
│   ├── db/
│   │   ├── chat_repo.go               # 新增（Phase 8B）
│   │   ├── provider_repo.go           # 新增（Phase 8I）
│   │   └── ...                        # 现有
│   ├── events/
│   │   ├── types.go                   # 现有
│   │   └── web_events.go              # 新增（Phase 8C）
│   ├── llm/
│   │   ├── provider_registry.go       # 新增（Phase 8I）：整合 provider_configs + 现有 LLM service
│   │   └── ...                        # 现有
│   ├── workflows/                     # 现有（Phase 1-7），Phase 8 不动
│   ├── activities/                    # 现有（Phase 1-7），Phase 8 不动
│   ├── config/                        # 现有
│   └── ...                            # 现有
├── migrations/
│   ├── 001-012 ...                    # 现有，不改
│   ├── 013_chat_sessions.sql          # 新增
│   ├── 014_chat_messages.sql          # 新增
│   ├── 015_provider_configs.sql       # 新增
│   └── 016_app_settings.sql           # 新增
├── web/                               # 新增（Phase 8D-8I）
├── scripts/
│   ├── test_phase8_*.sh               # 新增
│   └── ...                            # 现有
├── config/
│   ├── features.yaml                  # 现有
│   ├── budget.yaml                    # 现有
│   ├── models.yaml                    # 现有（空）；Phase 8I 引入 provider_registry 后可填充
│   └── model_providers.yaml.example   # 新增（Phase 8I）：provider 非敏感元数据模板
└── docs/
    └── superpowers/plans/
        ├── Cribug_Phase8.md           # v1.0（Shannon 探索报告，已归档）
        ├── Cribug_Phase8_v2.md        # v2.0（本文）
        └── ...
```

---

## 十六、配置设计

### 16.1 Feature Flags

`config/features.yaml` 新增（沿用现有结构）：

```yaml
features:
  # ... 现有 features 保持不变 ...

  # Phase 8 v2.0
  enable_web: true
  enable_chat_sessions: true
  enable_provider_settings: true
  enable_sse_bridge: true
  web_provider_storage: aes_gcm_env_key   # MVP:仅 aes_gcm_env_key;Phase 9+ 再加 keychain / vault
  web_sse_heartbeat_seconds: 30
  web_sse_max_idle_seconds: 60
  web_workspace_max_ref_size_kb: 1024
  web_lifecycle_delta_debounce_ms: 200    # AppendDelta 落库 debounce 阈值(详见 §10.2)
  web_lifecycle_delta_debounce_bytes: 1024
```

### 16.2 Environment Variables

`.env` **不**保存 LLM provider API Key(基础设施密钥照常存)。MVP 模式下,`.env` 保存**一个**主密钥 `CRIBUG_PROVIDER_AES_KEY`,所有 provider key 都用它 AES-256-GCM 加密后存 DB。

```bash
# === 现有 .env 内容保持不变 ===

# === Phase 8 v2.0 新增(基础设施 + 主密钥) ===
WEB_PORT=3000                              # Next.js dev server
WEB_API_PROXY=http://localhost:8080        # Gateway 内部调用(Next.js API route 代理用)
GATEWAY_WEB_CORS_ORIGINS=http://localhost:3000

# ★ MVP 主密钥:openssl rand -base64 32 生成;32 字节原始 / 44 字符 base64
#   启动时若缺失或长度不对,config.Load() 直接失败
CRIBUG_PROVIDER_AES_KEY=                   # base64 编码的 32 字节

# === Phase 8 v2.0 不再设置(Phase 9+ 升级时再开) ===
# PROVIDER_KEYCHAIN_SERVICE=              # OS keychain service name(Phase 9+)
# PROVIDER_VAULT_ADDR=                     # HashiCorp Vault(Phase 9+)
```

**Provider key 在 .env 中的明确禁止**：

```bash
# ❌ 绝对不要这样做:
OPENAI_API_KEY=sk-...                     # Provider key 禁止走 .env
ANTHROPIC_API_KEY=sk-ant-...
GOOGLE_API_KEY=AIza...
# 原因:违反 §8.2 §10.9 强制规范;Settings 页面无法管理;审计 / 轮转困难

# ✅ 唯一允许走 .env 的 key:
CRIBUG_PROVIDER_AES_KEY=...               # 主密钥(且不进 git,已有 .gitignore 覆盖)
```

### 16.3 `config/model_providers.yaml.example`（非敏感元数据）

```yaml
# Provider non-sensitive metadata template (Phase 8I)
# Real API keys are NEVER stored here.
# Edit via Web /settings or via the /api/v1/config/model-providers API.

providers:
  - name: openai
    display_name: OpenAI
    default_base_url: https://api.openai.com/v1
    default_chat_model: gpt-4o-mini
    default_embedding_model: text-embedding-3-small
    capabilities: [chat, embedding]
  - name: anthropic
    display_name: Anthropic
    default_base_url: https://api.anthropic.com
    default_chat_model: claude-3-5-sonnet-latest
    capabilities: [chat]
  - name: google
    display_name: Google Gemini
    default_base_url: https://generativelanguage.googleapis.com
    default_chat_model: gemini-2.0-flash
    default_embedding_model: text-embedding-004
    capabilities: [chat, embedding]
  - name: custom
    display_name: Custom OpenAI-compatible
    default_base_url: http://localhost:11434/v1
    capabilities: [chat, embedding]
```

---

## 十七、测试脚本设计

### 17.1 测试矩阵

| 测试脚本                                  | 覆盖 Slice      | 真实 LLM          | 默认行为 |
| ----------------------------------------- | --------------- | ----------------- | -------- |
| `test_phase8_chat_persistence.sh`         | 8A(repo)        | 否                | 必跑     |
| `test_phase8_product_api.sh`              | 8B(api)         | 否                | 必跑     |
| `test_phase8_lifecycle_state.sh`          | 8B(lifecycle)   | 否                | 必跑     |
| `test_phase8_no_http_self_call.sh`        | 8B(契约)        | 否                | 必跑     |
| `test_phase8_sse_stream.sh`               | 8C              | 否                | 必跑     |
| `test_phase8_approval_e2e.sh`             | 8B + 8G         | 否                | 必跑     |
| `test_phase8_workspace_report.sh`         | 8B + 8H         | 否                | 必跑     |
| `test_phase8_provider_settings.sh`        | 8I              | 否(只测连通性)    | 必跑     |
| `test_phase8_web_e2e.sh`                  | 8A-8I 集成(mock LLM) | 否           | 必跑     |
| `test_phase8_real_web_llm.sh`             | 8E-8G 真实 LLM(curl) | 是(API key)   | 可选     |
| `test_phase8_real_browser_llm.sh`         | 8J 真实浏览器(Playwright) | 是(API key) | 可选     |

### 17.2 通用断言策略

```bash
# 每个 test_phase8_*.sh 统一：
# 1. 启动 backend（如果未启动）
# 2. 等待 /api/v1/system/health → 200
# 3. 跑业务流
# 4. 断言：
#    - HTTP status code 正确
#    - JSON 结构字段完整
#    - DB row 存在
#    - SSE 事件序列正确
# 5. 清理
```

### 17.3 无 API key 行为

```bash
if [ -z "$OPENAI_API_KEY" ] && [ -z "$ANTHROPIC_API_KEY" ]; then
    if [ "$REAL_WEB_LLM_TEST" = "1" ]; then
        echo "SKIP: no API key configured"
        exit 0
    fi
fi
```

### 17.4 真实 LLM 测试

```bash
export REAL_WEB_LLM_TEST=1
export OPENAI_API_KEY=sk-...
./scripts/test_phase8_real_web_llm.sh
```

* 验证：发"法国首都是哪里？" → 5 秒内收到 `final_answer`，`data.answer_preview` 包含 "Paris"
* 验证：发 sandbox 风险问题 → 收到 `approval_requested`
* 验证：发 reflection 类问题 → `workflow_event` 包含 "reflection_round"
* 成本上限：$1 / run（环境变量可调）

### 17.5 Frontend 测试

* **Unit（Vitest）**：`api.test.ts`、`events.test.ts`、`reducer.test.ts`
* **Component（React Testing Library）**：`MessageBubble.test.tsx`、`ApprovalCard.test.tsx`
* **E2E（Playwright）**：`chat.spec.ts`（开浏览器模拟用户）

---

## 十八、Web E2E 验收路径

### 18.1 启动流程

```bash
# 1. 启动基础设施
docker compose -f deploy/docker-compose.yaml up -d postgres redis temporal

# 2. 启动 Python LLM Service
bash scripts/run-llm-service.sh &

# 3. 启动 Worker
bash scripts/run-worker.sh &

# 4. 启动 Gateway
bash scripts/run-gateway.sh &

# 5. 启动 Web
cd web
npm install
npm run dev
# → http://localhost:3000
```

### 18.2 手动验收 12 步

> 同 §4.2 用户视角验收。

1. 浏览器打开 `http://localhost:3000`
2. Sidebar 显示会话列表
3. 点击「New chat」→ `/chat/new`
4. 输入"法国首都是哪里？" → 几秒后看到 "Paris" 流式输出
5. 输入"继续"（多轮） → 上下文保留
6. 输入"分析一下 /workspace/report-2026-06-01.pdf 的内容" → Router 选 dag + RAG → 卡片显示 `requires_rag=true`
7. Timeline 显示 agent_started → tool_call → rag_retrieval → final
8. 输入"在 sandbox 里运行 `rm -rf /`" → Approval Card 出现
9. 点击 Reject → workflow 终止，前端展示拒绝原因
10. 点击消息旁「📄 View Report」→ 跳到 `/workspace/{ref}` 渲染 Markdown
11. 打开 Settings → 配置 OpenAI API key → Test 成功
12. 刷新页面 → 会话和消息完整恢复

### 18.3 自动化 E2E

```bash
# 完整 Web E2E（默认 mock，可选真实 LLM）
REAL_WEB_LLM_TEST=1 ./scripts/test_phase8_web_e2e.sh

# 输出：
# ✓ step 1: health check
# ✓ step 2: create session
# ✓ step 3: send direct_answer question
# ✓ step 4: receive streaming answer
# ✓ step 5: multi-turn continuity
# ✓ step 6: RAG question
# ✓ step 7: approval request
# ✓ step 8: reject approval
# ✓ step 9: workspace report
# ✓ step 10: provider settings
# ✓ step 11: page refresh persistence
# All 11 steps passed.
```

---

## 十九、明确禁止事项

> 违反任一视为 Phase 8 范围失控。

1. ❌ 重新实现 Phase 7 的 Advanced Router / HITL / Reflection / ToT / Debate / Research v2
2. ❌ 修改 migrations 001-012
3. ❌ 修改 `internal/workflows/` `internal/activities/` `internal/types/` 中 Phase 1-7 的文件（除非是新增字段且向后兼容）
4. ❌ 前端直连 Postgres / Redis / Temporal / Python LLM Service
5. ❌ 前端 localStorage 存 API Key
6. ❌ 数据库明文存 API Key
7. ❌ `.env` 保存 LLM provider API Key
8. ❌ 在 `chat_messages.content` 存 > 4 KB 长文本
9. ❌ 在 `chat_messages` 存完整 prompt / completion
10. ❌ 实现 Python SDK / Go SDK / CLI / OpenAI-compatible API
11. ❌ 实现完整 multi-tenant
12. ❌ 实现企业级 Auth / Billing / Quota
13. ❌ Tauri 桌面打包
14. ❌ 插件市场 / 工具市场
15. ❌ 在 `chat_sessions` / `chat_messages` 上建任何形式的工作流（那是 workflow / activity 的事）
16. ❌ SSE 通道发送 > 4 KB payload（用 `workspace_ref_created` 通知前端去拉）
17. ❌ 把 Shannon `desktop/` / `go/orchestrator/` / `python/llm-service/` 的代码复制 / 移动 / 提交到 Cribug

---

## 二十、成功路径 / 失败路径

### 20.1 成功路径

```
1. 用户在 /settings 配置 OpenAI API key
2. 用户在 /chat/new 输入问题
3. Frontend POST /api/v1/chat/sessions → session_id
4. Frontend POST /api/v1/chat/sessions/{id}/messages → message_id + workflow_id
5. Gateway 内部调 Advanced Router
6. Router 分类 + 决策 → 写 routing_audit_logs
7. Router dispatch 到 Phase 4-7 workflow
8. workflow 执行 → Activity 发出事件
9. SSE Bridge 收到事件 → 推给 Frontend
10. Frontend 实时渲染 timeline + 流式 delta
11. workflow 完成 → 写 workspace_objects（如有长答案）
12. SSE 推 final_answer + workspace_ref_created
13. Frontend 跳到 /workspace/{ref}（如需要）
14. 用户刷新页面 → chat_sessions + chat_messages 完整恢复
```

### 20.2 失败路径

| 失败场景                  | 行为                                                                                |
| ------------------------- | ----------------------------------------------------------------------------------- |
| Provider 未配置           | 提示「请先在 Settings 配置 LLM Provider」；chat API 返回 4xx                        |
| LLM API 调用失败          | SSE 推 `error` 事件；前端显示错误信息 + Retry 按钮                                  |
| Router 决策失败           | SSE 推 `error` 事件；前端显示「Router 分类失败」                                    |
| Workflow 失败             | SSE 推 `error` 事件 + `task_progress(stage=failed)`；前端显示错误 + 折叠 trace       |
| Approval timeout          | 工作流自动拒绝；SSE 推 `approval_resolved(decision=timeout)`；前端显示超时提示       |
| 用户 Reject               | 工作流 cancel；SSE 推 `final_answer(status=cancelled)`；前端显示「已取消」           |
| SSE 断线 30s              | Frontend 收到 `error`，自动重连（指数退避）                                         |
| SSE 断线 5min             | Frontend 用 `last_event_id` 续传；不丢事件                                          |
| 数据库写入失败            | Chat API 返回 5xx；前端显示「保存失败，请重试」                                     |
| Workflow 超时（> 5 min）  | 前端显示「任务超时」+ 停止按钮                                                       |
| Phase 7 某个 Slice 未完成  | Frontend 优雅降级（status 卡片显示「该模式未启用」），不 crash                        |
| 前端版本不兼容后端         | 显示「Please refresh the page」banner                                               |

### 20.3 安全 / 隐私失败

| 场景                       | 行为                                                                                |
| -------------------------- | ----------------------------------------------------------------------------------- |
| API Key 泄露到日志         | 测试断言：grep 日志不包含 `sk-` `sk-ant-` `AIza` 前缀                                |
| API Key 泄露到 SSE         | 测试断言：SSE 事件 payload 不含 key                                                  |
| API Key 泄露到前端响应     | `GET /api/v1/config/model-providers` 返回 `{ ..., api_key_present: bool }` 而非明文   |
| 长答案写到 messages        | 测试断言：`chat_messages.content` 长度 ≤ 4096 char                                  |
| Provider keychain 失败     | 启动时报错，提示用户切换 fallback 模式                                               |

---

## 二十一、后续 Phase 9 扩展路线

> Phase 8 v2.0 **不**做，但需要在 Roadmap 留位置。

| 能力                    | Phase 9 设计方向                                                                 |
| ----------------------- | -------------------------------------------------------------------------------- |
| Python SDK              | 复用 `web/lib/api/` 抽 Python 客户端；同步 + 异步；OpenAI-compatible wrapper     |
| Go SDK                  | 复用 `internal/api/` 抽 Go 客户端；`cribug/sdk` 子模块                           |
| CLI                     | `cribug-cli` 用 cobra 封装；`cribug chat` / `cribug task` / `cribug approval`    |
| OpenAI-compatible API   | `/v1/chat/completions` / `/v1/embeddings` 翻译到 Cribug Router；SSE 兼容         |
| 完整 Multi-tenant       | `tenant_id` 列 + row-level security + tenant 资源隔离                             |
| 企业级 Auth             | JWT / OIDC / SAML；`cribug_users` / `cribug_tenants` 表                           |
| Billing / Quota         | `usage_logs` 聚合 + Stripe 集成                                                   |
| Tauri 桌面打包           | Shannon `desktop/` 模式参考；离线缓存 + sync                                     |
| 插件市场                | 公开 Skills / MCP 注册表；社区贡献流                                              |
| Workspace 全文搜索      | `documents` + `document_chunks` 加 full-text 索引                                |
| 消息分支 / 重新生成     | `parent_message_id` 树；fork session                                               |
| 协同 / 分享             | session 分享链接 + read-only viewer                                               |
| 监控 / Tracing          | OpenTelemetry + Langfuse / Phoenix 集成                                          |

---

## 二十二、附录 A：Shannon 参考文件路径（只读）

> **绝对不要修改、移动、提交**。

| 路径                                                                          | 用途                                  |
| ----------------------------------------------------------------------------- | ------------------------------------- |
| `/home/florian/code/cribug/desktop/`                                          | Shannon Next.js 前端（参考 UI/Redux） |
| `/home/florian/code/cribug/desktop/app/(app)/run-detail/page.tsx`              | 主页面：conversation / timeline       |
| `/home/florian/code/cribug/desktop/components/chat-input.tsx`                 | 输入组件（含 agent / strategy 选择）  |
| `/home/florian/code/cribug/desktop/components/run-conversation.tsx`           | 消息列表（Markdown / Citation）        |
| `/home/florian/code/cribug/desktop/components/run-timeline.tsx`               | 时间线（agent / llm / tool / system） |
| `/home/florian/code/cribug/desktop/components/app-sidebar.tsx`                 | 侧边栏（session 列表）                 |
| `/home/florian/code/cribug/desktop/lib/shannon/api.ts`                        | API client（auth / error handling）   |
| `/home/florian/code/cribug/desktop/lib/shannon/stream.ts`                      | SSE client（EventSource + 重连）      |
| `/home/florian/code/cribug/desktop/lib/shannon/types.ts`                       | Event 类型定义                         |
| `/home/florian/code/cribug/desktop/lib/features/runSlice.ts`                   | Redux run slice                       |
| `/home/florian/code/cribug/desktop/lib/store.ts`                               | Redux store + persist                  |
| `/home/florian/code/cribug/desktop/app/(app)/settings/page.tsx`               | Settings 页面                          |
| `/home/florian/code/cribug/go/orchestrator/cmd/gateway/internal/handlers/task.go`     | Task HTTP handler              |
| `/home/florian/code/cribug/go/orchestrator/cmd/gateway/internal/handlers/session.go`   | Session handler                |
| `/home/florian/code/cribug/go/orchestrator/cmd/gateway/internal/handlers/approval.go`  | Approval handler               |
| `/home/florian/code/cribug/go/orchestrator/internal/activities/stream_events.go`      | Event type 常量                |
| `/home/florian/code/cribug/go/orchestrator/internal/activities/streaming.go`          | Streaming service              |
| `/home/florian/code/cribug/python/llm-service/`                                | Python LLM Service（仅参考）        |

---

## 二十三、附录 B：Cribug 新增文件结构（Phase 8 v2.0）

### 23.1 Backend Go

```
internal/api/product/
  common.go
  chat_handlers.go
  task_handlers.go
  approval_handlers.go       # 包装 Phase 7B
  workspace_handlers.go
  provider_handlers.go
  system_handlers.go
  sse_bridge.go
internal/api/router.go       # 追加路由
internal/db/
  chat_repo.go
  provider_repo.go
internal/events/
  web_events.go
internal/llm/
  provider_registry.go
migrations/
  013_chat_sessions.sql
  014_chat_messages.sql      # 含 message_task_links
  015_provider_configs.sql
  016_app_settings.sql
config/
  model_providers.yaml.example
scripts/
  test_phase8_product_api.sh
  test_phase8_chat_persistence.sh
  test_phase8_sse_stream.sh
  test_phase8_approval_e2e.sh
  test_phase8_workspace_report.sh
  test_phase8_provider_settings.sh
  test_phase8_web_e2e.sh
  test_phase8_real_web_llm.sh   # 可选，REAL_WEB_LLM_TEST=1
```

### 23.2 Frontend TypeScript

```
web/
  package.json
  tsconfig.json
  next.config.mjs
  tailwind.config.ts
  postcss.config.mjs
  .env.example
  README.md
  app/
    layout.tsx
    page.tsx
    globals.css
    (app)/
      layout.tsx
      chat/page.tsx
      chat/new/page.tsx
      chat/[sessionId]/page.tsx
      workspace/[ref]/page.tsx
      approvals/page.tsx
      settings/page.tsx
    api/proxy/[...path]/route.ts   # 可选
  components/
    chat/{SessionSidebar,Conversation,MessageBubble,Input,TypingIndicator,ProviderBadge}.tsx
    task/{RoutingDecisionCard,Timeline,ProgressBar,EventDetails}.tsx
    approval/{ApprovalCard,ApprovalForm,PendingList}.tsx
    workspace/{ReportViewer,CitationList,ArtifactTree}.tsx
    settings/{ProviderConfigForm,ConnectionTestButton,RuntimeSettingsForm}.tsx
    ui/{Button,Dialog,Tooltip,ScrollArea}.tsx
  lib/
    api/{cribug,chat,tasks,approvals,workspace,providers,system}.ts
    events/{sse,useRunStream,reducer}.ts
    store/{chat,settings,index}.ts
    types/{api,events,domain}.ts
  styles/tailwind.css
  public/
  tests/
    e2e/{chat,approval,workspace}.spec.ts
    unit/{api,events}.test.ts
```

---

## 二十四、附录 C：验收标准总结

### 24.1 Phase 8 v2.0 收官必须达成

| 验收项                                                                              | 是否必达 |
| ----------------------------------------------------------------------------------- | -------- |
| 启动 Postgres / Redis / Temporal / Python LLM Service / Gateway / Worker / Web      | ✅        |
| `curl http://localhost:8080/api/v1/system/health` 返回 200                            | ✅        |
| `curl http://localhost:8080/api/v1/system/readiness` 返回所有组件 ready              | ✅        |
| Web UI `http://localhost:3000` 渲染 Sidebar + Chat 主页面                              | ✅        |
| Settings 页面能配置至少 OpenAI Provider + Test 成功                                    | ✅        |
| 新建 session → 发送"法国首都是哪里？" → 流式收到 "Paris"                                | ✅        |
| 发送需要 RAG 的问题 → 卡片显示 `planned_mode=dag` + `requires_rag=true`                | ✅        |
| 发送 sandbox / 高风险问题 → Approval Card 出现 → Reject → workflow 终止                | ✅        |
| 消息旁"View Report" → `/workspace/{ref}` 渲染 Markdown + Citations                      | ✅        |
| Timeline 显示 agent / llm / tool / system 四类事件                                    | ✅        |
| 刷新页面 → 会话和消息完整恢复                                                          | ✅        |
| SSE 30s 心跳 + last_event_id 断点续传                                                  | ✅        |
| `GET /api/v1/config/model-providers` **不**返回明文 API Key                            | ✅        |
| `chat_messages.content` 长度 ≤ 4096 char（不存长文本）                                | ✅        |
| `provider_configs.api_key_encrypted` 非明文 OR `api_key_ref` 指向 keychain            | ✅        |
| API Key 不会出现在 `/var/log/*` / SSE payload / 前端响应 / 前端 localStorage           | ✅        |
| 至少 1 个 Playwright E2E + 7 个 bash 测试脚本通过                                     | ✅        |
| Phase 7 既有测试套件（`test_phase7_*.sh`）仍然通过                                    | ✅        |

### 24.2 Phase 8 v2.0 收官**不**必达（仅 Phase 9+）

| 不必达项                                                                       | 推到     |
| ------------------------------------------------------------------------------ | -------- |
| Python / Go SDK                                                                | Phase 9  |
| CLI                                                                            | Phase 9  |
| OpenAI-compatible API                                                          | Phase 9  |
| 完整 multi-tenant / 企业级 Auth                                                | Phase 9  |
| Tauri 桌面打包                                                                  | Phase 9+ |
| 插件市场 / 工具市场                                                             | Phase 10 |
| 协同 / 分享 / 消息分支                                                          | Phase 9+ |

### 24.3 显式风险与缓解

| 风险                                                         | 缓解                                                                                  |
| ------------------------------------------------------------ | ------------------------------------------------------------------------------------- |
| Phase 7 Slices 25-28 未完成 → 部分 Router mode 不可用          | Feature flag 控制；前端显示「该模式未启用」；E2E 测试用 `mock_router_mode`            |
| Shannon 探索结论与 Cribug 实际实现有偏差                       | Phase 8A-8B 实施前先跑通 Phase 7 已有 E2E；Phase 8 不假设未实现的 Phase 7 能力        |
| Web 前端技术栈选型被锁定                                      | Phase 8D 明确 Next.js 14 + React 18 + TS + Tailwind 3 + Zustand；如需替换必须评审       |
| API Key 存储安全                                              | OS keychain 优先；fallback 模式文档化风险；Phase 9 升级为完整 vault                    |
| SSE 大流量压力                                                | Phase 8J 跑 100 并发会话 benchmark；超出再优化                                         |
| 浏览器兼容性                                                  | Phase 8D 锁定 evergreen Chrome / Edge / Firefox / Safari；不支持 IE                  |

---

## 附录 D：与 Phase 7 的差异（不重写确认清单）

> 实施 Phase 8 v2.0 时，**必须**遵守。

| 文件 / 模块                                     | Phase 7 行为                          | Phase 8 行为（不重写）                                                  |
| ----------------------------------------------- | ------------------------------------- | ----------------------------------------------------------------------- |
| `internal/workflows/router.go`                  | Advanced Router workflow              | **不修改**；Phase 8 通过 `POST /api/v1/tasks/execute-routed` 调用        |
| `internal/activities/router.go`                 | 路由决策 Activity                      | **不修改**                                                              |
| `internal/activities/approval.go`               | Approval Activity                     | **不修改**                                                              |
| `internal/workflows/*（Slice 25-28）`           | Reflection / ToT / Debate / Research v2 | **不修改**（仅在 E2E 中验证可被路由到）                                  |
| `migrations/011-012`                            | approvals / approval_audit_logs       | **不修改**                                                              |
| `internal/events/types.go`                      | 100+ EventType 常量                    | **不修改**；Phase 8C 在 `web_events.go` 归一化为 12 类                   |
| `internal/api/handler.go`                       | createTask / getTask / streamTaskEvents | **不修改**；Phase 8B 在 `product/*` 子包下新增 handler |
| `internal/api/approval_handlers.go`             | Phase 7B Approval HTTP                | **不修改**；Phase 8G 复用其下层 handler                                   |
| `python_llm_service/`                           | LLM 适配                              | **不修改**；Phase 8I 接入 provider_registry 即可                         |
| `config/features.yaml`                          | 现有 features                         | **追加** Phase 8 字段；不删任何现有字段                                  |

---

## 附录 E：术语表

| 术语                      | 含义                                                                                  |
| ------------------------- | ------------------------------------------------------------------------------------- |
| **Session**               | 一次连续对话的产品层 ID（`chat_sessions.session_id`）                                  |
| **Message**               | 一条对话消息（user / assistant / system / status / approval）                          |
| **Task**                  | Phase 1 既有 `tasks` 表的一行；一次工作流执行                                          |
| **Workflow**              | Temporal workflow 实例（`tasks.workflow_id`）                                          |
| **Run**                   | Temporal workflow run id                                                              |
| **Routing Decision**      | Advanced Router 的决策（`planned_mode` / `executed_mode` / 各种 requires_*）           |
| **Approval**              | 一次人工审批请求 / 决策                                                                |
| **Workspace**             | Phase 5 append-only 持久层（`workspace_topics` Redis + `workspace_objects` Postgres）  |
| **WorkspaceRef**          | 指向 workspace object 的引用 ID                                                       |
| **ReportRef**             | 指向研究报告（合并多个 workspace objects）的引用 ID                                     |
| **SSE**                   | Server-Sent Events（text/event-stream）                                                |
| **Provider**              | LLM 提供方（OpenAI / Anthropic / Google / 自定义）                                     |
| **Keychain**              | OS 级密钥管理（macOS Keychain / Windows Credential Manager / Linux Secret Service）   |
| **Cribug Lite**           | Cribug 当前的最小可用版本，对照 Shannon 的全功能版本                                    |

---

## 附录 F：文档与计划书索引

| 文件                                  | 内容                                                                       |
| ------------------------------------- | -------------------------------------------------------------------------- |
| `Cribug_Phase1.md`                    | Phase 1 计划书                                                              |
| `Cribug_Phase2.md`                    | Phase 2 计划书                                                              |
| `Cribug_Phase3.md`                    | Phase 3 计划书                                                              |
| `Cribug_Phase4.md`                    | Phase 4 计划书                                                              |
| `Cribug_Phase5.md`                    | Phase 5 计划书                                                              |
| `Cribug_Phase6.md`                    | Phase 6 计划书                                                              |
| `Cribug_Phase7.md`                    | Phase 7 计划书                                                              |
| `Cribug_Phase8.md`                    | **Phase 8 v1.0**（Shannon 探索报告，已归档）                                |
| `Cribug_Phase8_v2.md`                 | **Phase 8 v2.0**（本文：Web Product Layer / GPT-like Agent Assistant）      |

---

**Phase 8 v2.0 计划书结束。**
