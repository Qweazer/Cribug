

Now I have enough information to produce the comprehensive research report. Let me compile everything into the structured format you requested.

---

# Shannon Web/Product Layer 架构探索报告

## 一、Shannon Web/Product 架构总览

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         Shannon 完整链路                                       │
└─────────────────────────────────────────────────────────────────────────────┘

  Browser (Next.js)              Backend Gateway (Go)           Orchestrator (Go)
  ┌──────────────────┐           ┌──────────────────┐           ┌─────────────┐
  │  ChatInput       │            │  REST API        │           │  Workflow   │
  │  (用户输入)       │────POST──▶│  /api/v1/tasks   │────gRPC──▶│  Engine     │
  │                  │            │                  │           │  (Temporal) │
  └──────────────────┘            └────────┬─────────┘           └──────┬──────┘
                                           │                           │
  ┌──────────────────┐                    │ gRPC + gRPC-web            │
  │  SSE EventSource │◀────────────GET───│  /api/v1/tasks/{id}        │
  │  (SSE Client)   │                  │                  │           │
  └────────┬─────────┘                  └────────┬─────────┘           │
           │                                   │                       │
           │         ┌──────────────────┐       │            ┌─────────┴───────┐
           └────────▶│  StreamingProxy  │◀──────┘            │  Activities      │
                     │  /stream/sse    │                     │  (emit events)  │
                     └────────┬─────────┘                     └────────┬────────┘
                              │                                       │
                     ┌────────▼─────────┐                     ┌────────▼────────┐
                     │  Event Logs DB  │◀────────────────────│  Redis Pub/Sub  │
                     │  (Postgres)     │                     │  (in-process)   │
                     └─────────────────┘                     └─────────────────┘

┌─────────────────────────────────────────────────────────────────────────────┐
│                         关键状态流                                            │
└─────────────────────────────────────────────────────────────────────────────┘

  Frontend Redux State          Backend Event Types           Frontend UI
  ┌──────────────────┐          ┌──────────────────┐         ┌────────────────┐
  │ run.status       │◀─────────│ WORKFLOW_STARTED │         │ Status Badge   │
  │ run.messages     │          │ AGENT_STARTED    │         │ Inline Pills   │
  │ run.isPaused     │          │ LLM_OUTPUT       │────────▶│ Message List   │
  │ run.connection   │          │ TOOL_INVOKED     │         │ Timeline       │
  │                  │          │ APPROVAL_REQUESTED│        │                │
  │                  │          │ WORKFLOW_PAUSED  │────────▶│ Pause UI       │
  │                  │          │ WORKFLOW_COMPLETED│───────▶│ Final Answer   │
  └──────────────────┘          └──────────────────┘         └────────────────┘
```

**核心模式：SSE + Redux 事件驱动**

1. **前端提交任务** → `POST /api/v1/tasks` → Gateway gRPC → Orchestrator
2. **后端推送事件** → Redis Pub/Sub → StreamingProxy → SSE EventSource → Redux
3. **前端状态更新** → Redux Reducer 处理事件 → React 重渲染
4. **最终结果获取** → 前端轮询 `GET /api/v1/tasks/{id}` 或通过 `fetchFinalOutput`

---

## 二、Shannon 关键源码路径清单

### 前端（`desktop/`）

| 文件                                      | 作用                                                                                                                                                         |
| ----------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `desktop/app/(app)/run-detail/page.tsx`   | **主页面** - 整合 conversation、timeline、summary 三个 tab；管理 session 历史加载、SSE 连接、task 完成后的 fetchFinalOutput                                  |
| `desktop/components/chat-input.tsx`       | **输入组件** - 发送消息、选择 agent 类型（normal/deep_research）、选择 research strategy；任务运行时显示 Pause/Resume/Cancel 按钮                            |
| `desktop/components/run-conversation.tsx` | **对话列表** - 渲染 messages（含 user/assistant/system/status 角色）；支持 Markdown 渲染、Citation 链接、代码高亮；status 消息显示为 inline pills            |
| `desktop/components/run-timeline.tsx`     | **时间线** - 显示 agent/llm/tool/system 四类事件的执行状态；支持折叠详情（JSON/text）                                                                        |
| `desktop/lib/shannon/api.ts`              | **API Client** - 统一封装所有 REST API：auth、task、session、approval、schedule；Auth 头优先 API Key → JWT → X-User-Id                                       |
| `desktop/lib/shannon/stream.ts`           | **SSE Client** - `useRunStream` hook；使用 EventSource 订阅 SSE；自动重连（指数退避）；支持 `last_event_id` 断点续传；发布事件到 Redux                       |
| `desktop/lib/shannon/types.ts`            | **Event 类型定义** - 完整的 `ShannonEvent` 类型联合，包括 30+ 事件类型（WORKFLOW_*、AGENT_*、LLM_*、TOOL_*、APPROVAL_* 等）                                  |
| `desktop/lib/features/runSlice.ts`        | **Redux Slice** - 管理 run 状态：events、messages、status、connectionState、sessionTitle、pause/resume/cancel 控制状态；核心 `addEvent` reducer 处理所有事件 |
| `desktop/lib/store.ts`                    | **Redux Store** - 配置 persist 中间件（localStorage 持久化）                                                                                                 |
| `desktop/components/app-sidebar.tsx`      | **侧边栏** - 显示 recent sessions 列表；session 激活状态；根据 `is_research_session` 显示不同图标                                                            |
| `desktop/app/(app)/settings/page.tsx`     | **设置页** - 显示用户信息、API Key、rate limits                                                                                                              |
| `desktop/lib/auth.ts`                     | **认证工具** - 获取/存储 access token、API key、user info                                                                                                    |
| `desktop/package.json`                    | **技术栈** - Next.js 16 + React 19 + Tailwind CSS 4 + Redux Toolkit + Dexie（IndexedDB） + Tauri 2                                                           |

### 后端（`go/orchestrator/cmd/gateway/`）

| 文件                                                        | 作用                                                                                                                                                                                                                             |
| ----------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `go/orchestrator/cmd/gateway/internal/handlers/task.go`     | **Task Handler** - `SubmitTask`（POST /api/v1/tasks）、`GetTaskStatus`（GET /api/v1/tasks/{id}）、`ListTasks`、`CancelTask`、`PauseTask`、`ResumeTask`、`GetControlState`；支持 research_strategy、model_tier、provider_override |
| `go/orchestrator/cmd/gateway/internal/handlers/approval.go` | **Approval Handler** - `SubmitDecision`（POST /api/v1/approvals/decision）；ApproveTask gRPC 调用                                                                                                                                |
| `go/orchestrator/cmd/gateway/internal/handlers/session.go`  | **Session Handler** - `GetSession`、`GetSessionHistory`、`GetSessionEvents`（含 turns + events）、`ListSessions`、`UpdateSessionTitle`、`DeleteSession`                                                                          |
| `go/orchestrator/cmd/gateway/internal/proxy/streaming.go`   | **Streaming Proxy** - 反向代理 SSE 请求到 admin server；路径重写 `/api/v1/stream/sse` → `/stream/sse`                                                                                                                            |
| `go/orchestrator/cmd/gateway/internal/middleware/auth.go`   | **Auth Middleware** - 验证 JWT token、API Key、X-User-Id；提取 user context                                                                                                                                                      |

### 后端事件系统（`go/orchestrator/internal/activities/`）

| 文件                                                   | 作用                                                                                                                                                                   |
| ------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `go/orchestrator/internal/activities/stream_events.go` | **事件类型定义** - `StreamEventType` 常量（WORKFLOW_STARTED、LLM_OUTPUT、TOOL_INVOKED、APPROVAL_REQUESTED 等 30+ 类型）；`EmitTaskUpdate` 发布事件到 streaming service |
| `go/orchestrator/internal/activities/streaming.go`     | **Streaming Service** - in-process pub/sub；`Publish` → Redis 或内存；SSE handler 订阅                                                                                 |
| `go/orchestrator/internal/activities/session_title.go` | **Session Title 生成** - title_generator agent；完成后通过 event 传递 title                                                                                            |

---

## 三、前后端交互流程

### 流程 1：用户发送一条消息

```
1. ChatInput.handleSubmit() 
   → submitTask({ query, session_id, context, research_strategy })
   
2. api.ts → POST /api/v1/tasks
   { query, session_id?, context: { force_research?, research_strategy? } }
   
3. task.go:SubmitTask()
   → 生成 session_id（若未提供）
   → 构建 orchpb.SubmitTaskRequest（包含 user_id、tenant_id、session_id）
   → gRPC SubmitTask
   → 返回 { task_id, workflow_id, status }
   
4. onTaskCreated callback 触发：
   - Redux: resetRun, setStatus("running"), addMessage(user message), addMessage(generating placeholder)
   - setCurrentTaskId(workflow_id)
   - setMainWorkflowId(workflow_id)
   
5. 前端立即开始 SSE 连接：useRunStream(workflowId)
   → EventSource → GET /api/v1/stream/sse?workflow_id=xxx
```

### 流程 2：后端 workflow 运行，状态如何推到前端

```
1. Temporal workflow 执行 → Activity 调用 EmitTaskUpdate
   
2. EmitTaskUpdate → streaming.Get().Publish(workflowId, Event{...})
   
3. SSE handler（admin server）订阅 Redis/内存 → 推送 SSE 事件
   
4. stream.ts:useRunStream 接收事件
   → dispatch({ type: "run/addEvent", payload: eventWithTimestamp })
   
5. runSlice.addEvent reducer 处理：
   - 状态事件（WORKFLOW_STARTED、AGENT_STARTED 等）→ addStatusMessage → 显示为 inline pill
   - 消息事件（thread.message.delta）→ 追加到 messages
   - 控制事件（workflow.paused）→ 更新 isPaused 等状态
```

### 流程 3：最终回答如何回到前端

```
1. Workflow 完成后 → WORKFLOW_COMPLETED event
   → Redux status 变为 "completed"
   
2. runDetailPage useEffect 监听 status === "completed"
   → fetchFinalOutput() 调用 getTask(currentTaskId)
   
3. getTask 返回 { result, metadata: { citations, model_breakdown } }
   → 权威 result 来自 task_executions.result 字段
   
4. 对于中间 agent（synthesis）：
   - stream 期间跳过其输出（per backend guidance）
   - 最终结果通过 API fetch 而非 stream 获得
   
5. 消息去重检查：已有 assistant message 则跳过添加
   → citations 更新到现有消息的 metadata
```

### 流程 4：出错时如何显示

```
1. WORKFLOW_FAILED event → Redux status = "failed"
   → 移除 generating placeholder 和 status pills
   → 可选添加 system error message
   
2. SSE onerror → dispatch streamError
   → 显示错误 banner + "Retry stream" / "Fetch final output" 按钮
   
3. 轮询 fallback：
   - 30s 无新事件 → poll getTask API
   - 5 次 max attempts → 停止
   
4. 任务状态 failed/cancelled 检测：
   → 额外通过 getTaskControlState 确认
```

### 流程 5：Approval/Control，用户操作如何传回 workflow

```
1. APPROVAL_REQUESTED event → 显示 approval UI（ChatInput 内置或 AlertDialog）
   → 用户点击 Approve/Reject
   
2. approval API 调用：
   → POST /api/v1/approvals/decision
   { workflow_id, approval_id, approved, feedback?, modified_action? }
   
3. approval.go:SubmitDecision → gRPC ApproveTask
   
4. Orchestrator 恢复 paused workflow 继续执行

5. Pause/Resume/Cancel 控制：
   - POST /api/v1/tasks/{id}/pause → gRPC PauseTask
   - POST /api/v1/tasks/{id}/resume → gRPC ResumeTask
   - POST /api/v1/tasks/{id}/cancel → gRPC CancelTask
   - GET /api/v1/tasks/{id}/control-state → 定期轮询
```

---

## 四、对 Cribug Phase 8 的启发

### 核心借鉴点

Shannon 的产品层是**事件驱动的状态机**：

1. **前端 Redux 管理所有状态**，包括：
   - 实时 events 数组（用于 timeline）
   - messages 数组（用于 conversation）
   - 派生状态（status、connectionState、sessionTitle）
   - 控制状态（isPaused、isCancelling）

2. **SSE 是唯一的实时通道**，连接建立后：
   - 后端主动推送所有事件（workflow/agent/llm/tool/approval）
   - 前端统一处理，路由到 Redux
   - 不存在 WebSocket，所有推送都是 text/event-stream

3. **Task 是核心资源**：
   - session_id 关联多个 task（一个 session = 一个 conversation thread）
   - task_id = workflow_id（同一个 ID）
   - task 有完整的生命周期：QUEUED → RUNNING → COMPLETED/FAILED/CANCELLED

4. **Session 管理会话**：
   - session_id 持久化
   - `/sessions/{id}/events` 返回 turns（含 events）
   - `/sessions/{id}/history` 返回 task 历史

5. **Auth 三级 fallback**：API Key → JWT → X-User-Id（开发模式）

---

## 五、Phase 8 v1.0 Slice 建议

```
┌─────────────────────────────────────────────────────────────────────┐
│                   Cribug Phase 8 v1.0 Slice Plan                     │
└─────────────────────────────────────────────────────────────────────┘

Phase 8A：Product API Consolidation
─────────────────────────────────────
目标：定义 Cribug 的 REST API 层，与 Phase 7 Router/Agent 对接

依赖 Shannon 设计：
  - task.go（SubmitTask、GetTask、CancelTask、PauseTask、ResumeTask）
  - session.go（Session CRUD、history、events）
  - approval.go（SubmitDecision）

Cribug 需补能力：
  - Phase 7 Advanced Router 的 HTTP handler 封装
  - HITL/Approval 的 `POST /api/v1/approvals/decision` 接口
  - Task context（force_research、research_strategy、model_tier）传递机制
  - Workflow/Actor 模式下的 task_id ≠ workflow_id 映射

交付物：go/cribug/cmd/gateway/internal/handlers/

Phase 8B：Conversation / Message Persistence
─────────────────────────────────────────────
目标：Cribug Session 和 Message 的持久化

依赖 Shannon 设计：
  - session.go 的 Session 数据模型
  - TaskHistory 结构（task_id、workflow_id、query、status、result、tokens、cost）
  - sessions 表 + task_executions 表

Cribug 需补能力：
  - Phase 5 Workspace/Swarm 的 session 关联
  - Phase 6 MCP/Skills 的 task metadata 存储
  - Message 历史（Turn）结构

交付物：数据库 schema + session/Task handler

Phase 8C：Streaming / Event Bridge
───────────────────────────────────
目标：实现 SSE 事件推送通道

依赖 Shannon 设计：
  - stream_events.go 的事件类型定义（30+ 种）
  - streaming.go 的 in-process pub/sub
  - stream.ts 的 useRunStream hook（EventSource + 重连 + 断点续传）
  - runSlice.addEvent 的事件处理逻辑

Cribug 需补能力：
  - Cribug 的 streaming service（Phase 7 的 emit event 机制）
  - Workflow 级别的 event 聚合（一个 task 可能包含多个 sub-workflow）
  - 断点续传（last_event_id）机制

交付物：SSE endpoint + frontend SSE client

Phase 8D：Frontend Foundation
─────────────────────────────
目标：Next.js 前端工程结构

依赖 Shannon 设计：
  - Next.js 16 + React 19 + Tailwind CSS 4 + Redux Toolkit
  - desktop/ 目录结构（app/、components/、lib/）
  - API client 统一封装（api.ts）
  - Redux store + persist 中间件

Cribug 需补能力：
  - Cribug 特色的目录结构（可能复用现有 Cribug 前端）
  - Auth 配置（API Key / JWT / Dev 模式）
  - NEXT_PUBLIC_API_URL 环境变量

交付物：Next.js 项目骨架 + API client + Redux store

Phase 8E：GPT-like Chat UI
───────────────────────────
目标：实现类似 ChatGPT 的对话界面

依赖 Shannon 设计：
  - run-conversation.tsx（消息列表、Markdown、Citation、status pills）
  - chat-input.tsx（输入框、agent 选择、research strategy、control buttons）
  - app-sidebar.tsx（session 列表）
  - run-detail/page.tsx（主页面，tab 切换）

Cribug 需补能力：
  - 中断/恢复 UI（Phase 7 HITL）
  - 多轮对话（Phase 7 的 debate/research multi-turn）
  - Citation 展示（Phase 6 RAG）

交付物：ChatUI 组件

Phase 8F：Router / Workflow Visibility Panel
─────────────────────────────────────────────
目标：展示 Router 决策和 Workflow 执行过程

依赖 Shannon 设计：
  - run-timeline.tsx（agent/llm/tool/system 四类事件）
  - runSlice 处理 WORKFLOW_STARTED、AGENT_STARTED、DELEGATION 等事件
  - collapsible-details.tsx（展开 JSON/text 详情）

Cribug 需补能力：
  - Phase 4 DAG 可视化（节点边）
  - Phase 7 Tree-of-Thoughts / Debate 的决策展示
  - Agent step 详情（ReAct loop、tool calls）

交付物：Timeline 组件 + DAG 可视化

Phase 8G：Approval UI
─────────────────────
目标：实现人工审批 UI

依赖 Shannon 设计：
  - chat-input.tsx 内置的 Pause/Resume/Cancel 按钮（运行时）
  - APPROVAL_REQUESTED event → status pill 显示 "Awaiting Approval"
  - approval.go 的 SubmitDecision API

Cribug 需补能力：
  - Phase 7 HITL 的 approval gate UI（AlertDialog 或 inline）
  - Feedback 输入（approve/reject 时可填写）
  - Modified action（用户修改后重新提交）

交付物：ApprovalDialog 组件 + API 集成

Phase 8H：Workspace / Report Viewer + Basic Settings
─────────────────────────────────────────────────────
目标：展示 Research Report 和 Workspace Artifact

依赖 Shannon 设计：
  - run-detail/page.tsx 的 Summary tab（token usage、cost、duration）
  - MarkdownWithCitations（支持 citation tooltip）
  - Settings 页面（API Key、User info）

Cribug 需补能力：
  - Phase 5 Workspace 的 artifact 展示
  - Phase 6 Research v2 的 report 格式
  - File viewer（代码、JSON、PDF 等）

交付物：ReportViewer 组件 + Settings 页面

Phase 8I：End-to-End Web Usability Test
───────────────────────────────────────
目标：验证完整的浏览器 → Gateway → Router → LLM/Workflow → Streaming Answer

依赖 Shannon 设计：
  - 完整的前后端联调流程
  - SSE 断连重连验证
  - Session 持久化验证
  - 多轮对话验证

交付物：E2E 测试脚本 + 验证文档
```

---

## 六、明确不做的内容

Phase 8 v1.0 暂不优先考虑：

| 排除项                        | 原因                                                 |
| ----------------------------- | ---------------------------------------------------- |
| Python SDK                    | Phase 8 目标是 Web UI，不是 SDK                      |
| Go SDK                        | 同上                                                 |
| CLI                           | 同上                                                 |
| OpenAI-compatible API         | Shannon 已有 OpenAI translator（Cribug 可后续参考）  |
| 完整 Multi-tenant             | Phase 8 专注单租户体验，后续扩展                     |
| 企业级 Auth / Billing / Quota | Phase 8 仅基础 API Key 认证，后续完善                |
| Tauri 桌面打包                | Shannon desktop 有 Tauri 配置，但 Phase 8 Web 先落地 |
| 复杂插件市场                  | Phase 8 后再议                                       |

---

## 七、最终结论

### 1. Shannon 的 Web 前端和后端配合核心模式是什么？

**SSE + Redux 事件驱动**。前端通过 EventSource 订阅 SSE，后端通过 EmitTaskUpdate 发布事件到 streaming service，事件被路由到 Redux store，前端 React 组件根据 Redux 状态渲染。这是一个**发布-订阅模式的单向数据流**。

### 2. Cribug Phase 8 最应该先复刻 Shannon 的哪些部分？

**优先级排序**：
1. **API Layer**（Phase 8A）—— 定义好 REST 接口，后面前端才有根
2. **SSE Event Bridge**（Phase 8C）—— 实时性是 GPT-like 体验的核心
3. **Redux Store**（Phase 8D）—— 统一状态管理，比直接操作 DOM 更可维护
4. **Task/Session 数据模型**（Phase 8B）—— session + task 的关联是 Shannon 的关键设计
5. **Chat UI**（Phase 8E）—— 用户直接感知的产品界面

### 3. 哪些 Shannon 设计可以直接借鉴？

| 设计                                           | 可借鉴程度                             |
| ---------------------------------------------- | -------------------------------------- |
| `lib/shannon/api.ts` 的 API 封装模式           | 高——统一 auth header、错误处理         |
| `lib/shannon/stream.ts` 的 SSE client          | 高——重连逻辑、断点续传、事件分发       |
| `lib/features/runSlice.ts` 的事件处理          | 高——状态转换、消息去重、streaming 累积 |
| `components/run-conversation.tsx` 的 UI        | 高——Markdown、Citation、status pill    |
| `components/chat-input.tsx` 的 control buttons | 高——pause/resume/cancel 一体化         |
| `go/.../handlers/task.go` 的 HTTP handler      | 高——context 传递、error mapping        |
| `stream_events.go` 的事件类型定义              | 中——30+ 种类型可裁剪使用               |
| `session.go` 的 history/events 查询            | 中——turns + events 聚合需适配          |

### 4. 哪些地方不能照搬，需要等回到 Cribug 项目后结合实际目录和 Phase 7 实现再设计？

| 领域                      | 不能照搬原因                                                                                         |
| ------------------------- | ---------------------------------------------------------------------------------------------------- |
| **Orchestrator 架构**     | Shannon 用 Temporal + gRPC；Cribug Phase 7 可能是自定义 workflow engine                              |
| **Task/Workflow ID 映射** | Shannon task_id = workflow_id；Cribug Phase 7 可能 DAG/actor 模式下不同                              |
| **Event 类型范围**        | Shannon 30+ 种 event（含 multi-agent team events）；Cribug Phase 7 的 event 集合待确认               |
| **Session 数据模型**      | Shannon 的 Session + TaskExecutions 表结构；Cribug 的 session 关联 Phase 5/6 的 workspace 需重新设计 |
| **前端工程结构**          | Cribug 可能已有前端目录；不能直接复用 `desktop/` 结构                                                |
| **Auth 机制**             | Shannon 有完整 JWT + multi-tenant；Cribug Phase 8 可能简化                                           |
| **前端 State 管理**       | Shannon 用 Redux + persist；Cribug 可能有自己偏好（Zustand/Context）                                 |

### 5. Phase 8 v1.0 到 Phase 8 v2.0 之间还需要在 Cribug 中补充验证哪些事实？

回到 Cribug 项目后，需要先验证：

1. **Phase 7 Advanced Router 是否已有 HTTP Handler 封装？** 还是需要从头写？
2. **Phase 7 的 workflow 执行引擎是什么？** Temporal 还是自定义？事件如何发出？
3. **Phase 7 HITL/Approval 的触发机制是什么？** event 驱动还是 checkpoint 驱动？
4. **Phase 5 Workspace 的 artifact 如何存储和读取？** 有没有 blob storage？
5. **Phase 6 Research v2 的 report 格式是什么？** 如何展示长文本？
6. **Cribug 现有前端目录是什么？** 能否复用？
7. **Phase 7 的 task_id 是否等于 workflow_id？** 还是不同的概念？
8. **Phase 7 的 task context（force_research、model_tier 等）如何通过 HTTP 传递？**

---

## 附录：Shannon 技术栈速查

```
前端框架：Next.js 16 (App Router)
UI 库：Radix UI + Tailwind CSS 4
状态管理：Redux Toolkit + redux-persist (localStorage)
实时通信：SSE (EventSource) + 自定义重连逻辑
Markdown：react-markdown + remark-gfm + rehype-highlight
数据库（后端）：PostgreSQL + Redis
Workflow 引擎：Temporal (Go SDK)
通信协议：gRPC + gRPC-web + REST (HTTP)
认证：JWT + API Key + X-User-Id (dev)
```

---

**报告完成**。以上是基于 Shannon 源码探索的 Phase 8 v1.0 规划思路，回 Cribug 项目后需结合 Phase 7 实际实现情况进行调整和细化。