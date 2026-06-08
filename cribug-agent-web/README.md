# Cribug Agent Web Prototype

这是一个放在 Shannon 项目目录下的独立前端原型，目录名为 `cribug-agent-web`。它只把 Shannon 作为工程参考，不复制 Shannon 的品牌、视觉、命名或页面文案。

## Shannon 前后端整合观察

- 前端位于 `desktop/`，使用 Next.js、React、Tailwind、Tauri 与集中式组件目录。
- 页面主要在 `desktop/app/`，组件在 `desktop/components/`，Shannon 后端 API 封装集中在 `desktop/lib/shannon/api.ts`。
- 类型定义集中在 `desktop/lib/shannon/types.ts`，事件、任务、会话、工具调用等结构与 UI 解耦。
- API Base URL 通过环境变量读取，桌面端没有把后端地址散落到页面组件中。
- Gateway 的核心 HTTP 路由定义在 `go/orchestrator/cmd/gateway/main.go`，任务、会话、工具、模型、健康检查、OpenAI 兼容接口都通过独立 handler 管理。
- 任务接口围绕 `/api/v1/tasks`、`/api/v1/tasks/{id}`、`/api/v1/tasks/{id}/events`、`/api/v1/stream/sse` 展开，支持提交、状态查询、事件流与控制信号。
- 会话接口围绕 `/api/v1/sessions`、`/api/v1/sessions/{sessionId}`、history、events 展开。
- 工具接口围绕 `/api/v1/tools`、`/api/v1/tools/{name}`、`/api/v1/tools/{name}/execute` 展开，并在 gateway 层阻止危险工具直接执行。

## Cribug 采用的设计

- 使用 Vite + React + Tailwind CSS，保持轻量、清晰、易迁移。
- 页面组件不直接写 `fetch`，所有请求集中在 `src/api/client.ts`。
- 环境变量集中在 `src/api/config.ts`，支持 `VITE_API_BASE_URL` 与 `VITE_ENABLE_MOCK`。
- 核心类型集中在 `src/types/index.ts`，包括 `TaskRequest`、`TaskResult`、`TaskStatus`、`TaskMode`、`ExecutionNode`、`ExecutionLog`、`Session`、`ChatMessage`、`ProviderConfig`、`ToolStatus`、`RagStatus`、`SandboxStatus`、`HealthStatus`。
- mock 数据集中在 `src/api/mockData.ts`，真实 API 替换时优先改 client 层。
- 视觉采用 Cribug 自己的深色控制台、发光任务流、青蓝能量色、金铜强调色与东方幻想科幻气质。

## 当前已实现

- 首页 / 项目入口页
- 智能体工作台
- 发光任务流节点图
- 能力与运行状态面板
- 执行日志面板
- 模型与接口配置页
- API client 封装
- mock 数据模式
- 环境变量示例

## 运行方式

```bash
cd cribug-agent-web
npm install
npm run dev
```

默认端口为 `5174`。

## Mock / Real API 切换

复制 `.env.example` 为 `.env`，然后调整：

```bash
VITE_API_BASE_URL=http://localhost:8080
VITE_ENABLE_MOCK=true
VITE_DEFAULT_PROVIDER=openai
VITE_DEFAULT_MODEL=gpt-5-mini
```

- `VITE_ENABLE_MOCK=true`：使用本地 mock 数据。
- `VITE_ENABLE_MOCK=false`：调用 `VITE_API_BASE_URL` 指向的真实后端。

## 迁移到 Cribug 项目时注意

- 整体复制 `cribug-agent-web` 目录即可。
- 不需要复制 Shannon 的 `desktop/`、Go handler 或任何 Shannon 品牌资源。
- 保留 `src/api/client.ts` 作为适配层，再按 Cribug 后端真实路由替换路径。
- 不要把 API Key、私密配置、绝对路径写进代码。
- 如果 Cribug 后端响应结构与当前类型不同，优先在 API client 层做转换，保持页面组件稳定。

## 仍待真实后端补齐的接口

- `POST /api/v1/tasks`
- `GET /api/v1/tasks/{taskId}`
- `GET /api/v1/tasks/{taskId}/result`
- `GET /api/v1/tasks/{taskId}/events`
- `GET /api/v1/sessions`
- `GET /api/v1/sessions/{sessionId}`
- `POST /api/v1/sessions`
- `GET /v1/llm-models`
- `POST /api/v1/provider-config`
- `POST /api/v1/provider-config/test`
- `GET /api/v1/rag/status`
- `GET /api/v1/sandbox/status`
- `GET /health`

## 下一步对接建议

1. 先确认 Cribug 后端任务、会话、日志、配置四组接口的真实响应结构。
2. 在 `src/types/index.ts` 调整类型，再在 `src/api/client.ts` 做字段映射。
3. 保持 `VITE_ENABLE_MOCK=true` 做 UI 回归验证，再切到真实 API。
4. 增加 SSE 或 WebSocket 事件流，用于持续刷新任务节点与日志。
