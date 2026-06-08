import type {
  ExecutionLog,
  ExecutionNode,
  HealthStatus,
  ProviderConfig,
  ProviderSummary,
  RagStatus,
  SandboxStatus,
  Session,
  SessionDetail,
  TaskResult,
  TaskStatusDetail,
  ToolStatus,
} from "../types";

const now = new Date();

export const mockSessions: Session[] = [
  {
    session_id: "session-expedition-001",
    title: "复杂数据源巡检",
    task_count: 8,
    latest_status: "running",
    created_at: new Date(now.getTime() - 1000 * 60 * 120).toISOString(),
    updated_at: now.toISOString(),
  },
  {
    session_id: "session-memory-bridge",
    title: "知识库上下文整理",
    task_count: 4,
    latest_status: "completed",
    created_at: new Date(now.getTime() - 1000 * 60 * 90).toISOString(),
    updated_at: new Date(now.getTime() - 1000 * 60 * 45).toISOString(),
  },
];

export const mockNodes: ExecutionNode[] = [
  {
    id: "intent",
    label: "用户意图",
    description: "捕获目标、边界与隐含约束",
    status: "completed",
    type: "route",
    x: 8, y: 46,
    links: ["route"],
  },
  {
    id: "route",
    label: "智能路由",
    description: "感知复杂度并选择执行模式",
    status: "completed",
    type: "route",
    x: 24, y: 30,
    links: ["plan", "rag"],
  },
  {
    id: "plan",
    label: "计划生成",
    description: "形成可追踪执行计划",
    status: "active",
    type: "plan",
    x: 42, y: 18,
    links: ["dag"],
  },
  {
    id: "dag",
    label: "DAG 拆解",
    description: "展开多分支远征路径",
    status: "pending",
    type: "plan",
    x: 58, y: 20,
    links: ["web", "sandbox", "tool"],
  },
  {
    id: "rag",
    label: "知识检索",
    description: "召回本地知识库证据",
    status: "completed",
    type: "rag",
    x: 46, y: 54,
    links: ["verify"],
  },
  {
    id: "web",
    label: "网页探索",
    description: "连接外部来源并提取证据",
    status: "pending",
    type: "web",
    x: 76, y: 18,
    links: ["verify"],
  },
  {
    id: "sandbox",
    label: "沙箱执行",
    description: "隔离执行并验证假设",
    status: "pending",
    type: "sandbox",
    x: 78, y: 48,
    links: ["verify"],
  },
  {
    id: "tool",
    label: "工具调用",
    description: "调度可用技能和工作流",
    status: "pending",
    type: "tool",
    x: 64, y: 76,
    links: ["verify"],
  },
  {
    id: "verify",
    label: "反思验证",
    description: "复核结论、风险与引用",
    status: "pending",
    type: "verify",
    x: 88, y: 62,
    links: ["result"],
  },
  {
    id: "result",
    label: "最终结果",
    description: "交付可审计结论",
    status: "pending",
    type: "result",
    x: 96, y: 36,
    links: [],
  },
];

export const mockLogs: ExecutionLog[] = [
  {
    id: "log-001",
    event_type: "reasoning",
    payload: JSON.stringify({ title: "复杂度判断", message: "检测到多数据源、验证需求与可能的事实冲突，切换为 DAG 多阶段路径。", provider: "OpenAI", model: "gpt-5-mini", duration_ms: 1240 }),
    created_at: new Date(now.getTime() - 1000 * 60 * 12).toISOString(),
  },
  {
    id: "log-002",
    event_type: "action",
    payload: JSON.stringify({ title: "知识检索", message: "已从本地知识库召回 6 条相关片段，覆盖任务背景与约束条件。", duration_ms: 860 }),
    created_at: new Date(now.getTime() - 1000 * 60 * 9).toISOString(),
  },
  {
    id: "log-003",
    event_type: "observation",
    payload: JSON.stringify({ title: "网页探索", message: "外部信息需要二次确认，已标记 2 个来源作为待复核证据。", duration_ms: 2210 }),
    created_at: new Date(now.getTime() - 1000 * 60 * 6).toISOString(),
  },
  {
    id: "log-004",
    event_type: "reflection",
    payload: JSON.stringify({ title: "反思队列", message: "当前结论缺少沙箱验证结果，输出前需要补充可执行证明。", provider: "OpenAI", model: "gpt-5-mini", cost_usd: 0.018 }),
    created_at: new Date(now.getTime() - 1000 * 60 * 3).toISOString(),
  },
];

export const mockTaskStatus: TaskStatusDetail = {
  taskId: "task-cribug-vision-001",
  status: "RUNNING",
  progress: 62,
  mode: "dag",
  currentNodeId: "plan",
  complexityScore: 0.78,
  updatedAt: now.toISOString(),
};

export const mockTaskResult: TaskResult = {
  taskId: "task-cribug-vision-001",
  status: "COMPLETED",
  title: "远征式任务拆解结果",
  summary: "任务已拆解为路由、检索、验证、沙箱执行和最终交付五个阶段。",
  answer: "建议先收束关键约束，再执行知识库召回与沙箱验证，最后生成带证据来源的交付结果。",
  citations: [
    { title: "内部知识片段", url: "mock://rag/fragment-12" },
    { title: "外部来源占位", url: "mock://web/source-03" },
  ],
  costUsd: 0.043,
  durationMs: 18420,
  model: "gpt-5-mini",
  provider: "openai",
  completedAt: now.toISOString(),
};

export const mockProviders: ProviderSummary[] = [
  { id: "openai", name: "OpenAI", models: ["gpt-5-mini", "gpt-5"], enabled: true },
  { id: "anthropic", name: "Anthropic", models: ["claude-sonnet-4.5"], enabled: true },
  { id: "local", name: "本地模型", models: ["cribug-local-agent"], enabled: false },
];

export const mockProviderConfig: ProviderConfig = {
  provider: "openai",
  chat_model: "gpt-5-mini",
  embedding_model: "text-embedding-3-large",
  api_key_env: "OPENAI_API_KEY",
  base_url_override: "",
  allow_mock_fallback: true,
  require_real: false,
};

export const mockTools: ToolStatus[] = [
  { name: "知识检索", category: "检索", enabled: true, health: "online", latency_ms: 85 },
  { name: "网页搜索", category: "检索", enabled: true, health: "warning", latency_ms: 420 },
  { name: "沙箱执行", category: "执行", enabled: true, health: "online", latency_ms: 230 },
  { name: "反思验证", category: "分析", enabled: true, health: "online", latency_ms: 110 },
  { name: "权限护栏", category: "治理", enabled: true, health: "online", latency_ms: 35 },
];

export const mockRagStatus: RagStatus = {
  enabled: true,
  collections: 4,
  indexed_documents: 1284,
  last_sync_at: new Date(now.getTime() - 1000 * 60 * 18).toISOString(),
  health: "online",
};

export const mockSandboxStatus: SandboxStatus = {
  enabled: true,
  active_runners: 2,
  queued_jobs: 1,
  health: "online",
};

export const mockHealthStatus: HealthStatus = {
  status: "healthy" as const,
  api: "mock",
  version: "0.1.0-prototype",
  checked_at: now.toISOString(),
};

export const mockSessionDetail: SessionDetail = {
  session_id: "session-expedition-001",
  messages: [
    {
      id: "msg-001",
      role: "user",
      content: "请帮我分析当前多数据源报告中的冲突点，并给出可验证的结论。",
      task_id: null,
      created_at: new Date(now.getTime() - 1000 * 60 * 14).toISOString(),
    },
    {
      id: "msg-002",
      role: "assistant",
      content: "已进入多阶段远征路径：先识别数据冲突，再调用知识检索与网页探索，最后执行反思验证。",
      task_id: "task-cribug-vision-001",
      created_at: new Date(now.getTime() - 1000 * 60 * 13).toISOString(),
    },
  ],
  tasks: [
    {
      task_id: "task-cribug-vision-001",
      query: "分析多数据源报告中的冲突点",
      status: "running",
      created_at: new Date(now.getTime() - 1000 * 60 * 14).toISOString(),
    },
  ],
};
