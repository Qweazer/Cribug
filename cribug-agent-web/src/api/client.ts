import { apiConfig } from "./config";
import { mockProviders, mockProviderConfig, mockRagStatus, mockSandboxStatus, mockHealthStatus, mockSessions, mockSessionDetail, mockTools } from "./mockData";
import type { HealthStatus, ProviderConfig, ProviderItem, RagStatus, SandboxStatus, SessionDetail, SessionSummary, TestConnectionResult } from "../types";

const delay = (ms = 240) => new Promise(r => setTimeout(r, ms));

// ── Helper ─────────────────────────────────────────────────────
export function getBaseUrl(): string { return apiConfig.baseUrl; }

// ── Health ─────────────────────────────────────────────────────
export async function getHealthStatus(): Promise<HealthStatus> {
  if (apiConfig.enableMock) { await delay(120); return mockHealthStatus; }
  return requestJson<HealthStatus>("/health");
}

// ── Provider / LLM config ─────────────────────────────────────
export async function listProviders(): Promise<ProviderItem[]> {
  if (apiConfig.enableMock) {
    await delay();
    return [
      { id: "openai", display_name: "OpenAI", default_chat_model: "gpt-5-mini", base_url: "https://api.openai.com/v1", openai_compatible: true, timeout: 60, models: [] },
      { id: "anthropic", display_name: "Anthropic", default_chat_model: "claude-sonnet-4.5", base_url: "https://api.anthropic.com", openai_compatible: false, timeout: 60, models: [] },
    ];
  }
  const r = await requestJson<{ providers: ProviderItem[] }>("/api/v1/llm/providers");
  return r.providers || [];
}

export async function saveProviderConfig(payload: ProviderConfig): Promise<ProviderConfig> {
  if (apiConfig.enableMock) { await delay(); return payload; }
  return requestJson<ProviderConfig>("/api/v1/llm/config", { method: "PUT", body: JSON.stringify(payload) });
}

export async function testProviderConnection(payload: ProviderConfig): Promise<TestConnectionResult> {
  if (apiConfig.enableMock) { await delay(320); return { status: "ok", provider: payload.provider, model: payload.chat_model, mock: true, fallback_used: true, answer: "Mock connection OK", answer_len: 17 }; }
  return requestJson<TestConnectionResult>("/api/v1/llm/config/test", { method: "POST", body: JSON.stringify(payload) });
}

export function getDefaultProviderConfig(): ProviderConfig { return { ...mockProviderConfig, provider: apiConfig.defaultProvider, chat_model: apiConfig.defaultModel }; }

// ── Task submit (non-streaming fallback) ───────────────────────
export async function submitTask(payload: { query: string; config?: Record<string, unknown> }): Promise<{ task_id: string; workflow_id: string; run_id: string | null; status: string; stream_url: string }> {
  if (apiConfig.enableMock) { await delay(); return { task_id: `mock-${Date.now()}`, workflow_id: `wf-${Date.now()}`, run_id: "mock", status: "running", stream_url: "" }; }
  return requestJson("/api/v1/tasks", { method: "POST", body: JSON.stringify(payload) });
}

// ── LLM-路由提交 (后端 LLM 自动判断 simple / dag / react / research) ─
export interface RouteResult {
  session_id: string;
  workflow_id: string;
  run_id: string;
  status: string;
  decision?: { mode: string; planned_mode: string; risk_level: string; requires_approval: boolean; workflow_type: string };
  frontend_contract?: {
    selected_mode: string; planned_mode: string; executed_mode: string; fallback_reason: string;
    approval_required: boolean; risk_level: string; model_tier: string;
    estimated_cost_usd: number; estimated_latency_ms: number;
    async_required: boolean; audit_required: boolean;
    candidates: Array<{ mode: string; score: number; reason: string }>;
    rejected_modes: Array<{ mode: string; reason: string }>;
    score_breakdown: Record<string, number>;
  };
  final_answer_text?: string;
  final_answer_ref?: string;
  pending_approval_id?: string;
  reason?: string;
  task_id?: string;
}

export async function submitRoutedTask(payload: { query: string; budget_usd?: number; max_latency_ms?: number }): Promise<RouteResult> {
  if (apiConfig.enableMock) {
    await delay();
    return {
      session_id: `session-${Date.now()}`,
      workflow_id: `route-exec-${Date.now()}`,
      run_id: `mock-${Date.now()}`,
      status: "running",
      decision: { mode: "dag", planned_mode: "dag", risk_level: "low", requires_approval: false, workflow_type: "AdvancedRoutingWorkflow" },
      frontend_contract: { selected_mode: "dag_workflow", planned_mode: "dag_workflow", executed_mode: "dag_workflow", fallback_reason: "", approval_required: false, risk_level: "low", model_tier: "medium", estimated_cost_usd: 0.05, estimated_latency_ms: 30000, async_required: false, audit_required: false, candidates: [], rejected_modes: [], score_breakdown: {} },
    };
  }
  return requestJson<RouteResult>("/api/v1/tasks/execute-routed", {
    method: "POST",
    body: JSON.stringify({ query: payload.query, budget_usd: payload.budget_usd || 0.5, max_latency_ms: payload.max_latency_ms || 60000 }),
  });
}

export async function previewRoute(payload: { query: string }): Promise<RouteResult> {
  if (apiConfig.enableMock) {
    await delay();
    return {
      session_id: `session-${Date.now()}`,
      workflow_id: `route-${Date.now()}`,
      run_id: `mock`,
      status: "preview",
      decision: { mode: "dag", planned_mode: "dag", risk_level: "low", requires_approval: false, workflow_type: "AdvancedRoutingWorkflow" },
      frontend_contract: { selected_mode: "dag_workflow", planned_mode: "dag_workflow", executed_mode: "dag_workflow", fallback_reason: "", approval_required: false, risk_level: "low", model_tier: "medium", estimated_cost_usd: 0.05, estimated_latency_ms: 30000, async_required: false, audit_required: false, candidates: [], rejected_modes: [], score_breakdown: {} },
    };
  }
  return requestJson<RouteResult>("/api/v1/tasks/route", {
    method: "POST",
    body: JSON.stringify({ query: payload.query, budget_usd: 0.5, max_latency_ms: 60000 }),
  });
}

// ── SSE streaming ─────────────────────────────────────────────
export type SseEventHandler = (event: { type: string; data: unknown; id: string }) => void;

/**
 * Open SSE stream for task events. Returns a stop() function.
 * Auto-reconnects with exponential backoff up to 10 times.
 */
export function streamTaskEvents(taskId: string, handler: SseEventHandler): () => void {
  if (apiConfig.enableMock) {
    // Mock: simulate streaming by emitting a few events
    let idx = 0;
    const mockEvents = [
      { type: "task.started", data: { task_id: taskId }, id: "0-0" },
      { type: "delta", data: { content: "正在分析" }, id: "0-1" },
      { type: "delta", data: { content: "您的问题..." }, id: "0-2" },
      { type: "delta", data: { content: "示例回答内容..." }, id: "0-3" },
      { type: "task.completed", data: { result: "这是一个模拟的流式回答。" }, id: "0-4" },
    ];
    const t = setInterval(() => {
      if (idx >= mockEvents.length) { clearInterval(t); return; }
      handler(mockEvents[idx++]);
    }, 300);
    return () => clearInterval(t);
  }

  let stopped = false;
  let attempt = 0;
  const maxAttempts = 10;
  let evtSource: EventSource | null = null;

  function connect() {
    if (stopped || attempt >= maxAttempts) return;
    const url = `${apiConfig.baseUrl}/api/v1/stream/sse?task_id=${encodeURIComponent(taskId)}`;
    evtSource = new EventSource(url);
    evtSource.onmessage = (e) => {
      attempt = 0; // reset on success
      try {
        const data = JSON.parse(e.data);
        handler({ type: e.type || "message", data, id: e.lastEventId || "" });
      } catch {
        handler({ type: e.type || "message", data: e.data, id: e.lastEventId || "" });
      }
    };
    evtSource.onerror = () => {
      evtSource?.close();
      attempt++;
      if (attempt < maxAttempts) {
        const delay = Math.min(1000 * 2 ** attempt, 16000);
        setTimeout(connect, delay);
      }
    };
  }
  connect();
  return () => { stopped = true; evtSource?.close(); };
}

// ── Task status / result / cancel ────────────────────────────
export async function getTaskStatus(taskId: string): Promise<Record<string, unknown>> {
  if (apiConfig.enableMock) {
    await delay(180);
    return { task_id: taskId, status: "completed", result: "Mock result", model: "gpt-5-mini" };
  }
  return requestJson(`/api/v1/tasks/${taskId}`);
}

export async function cancelTask(taskId: string): Promise<{ status: string }> {
  if (apiConfig.enableMock) { return { status: "cancelled" }; }
  // Use Temporal-style cancel via signal
  return requestJson(`/api/v1/tasks/${taskId}/result`, { method: "POST" }).catch(() => ({ status: "cancel_requested" }));
}

// ── Sessions ──────────────────────────────────────────────────
export async function listSessions(): Promise<SessionSummary[]> {
  if (apiConfig.enableMock) { await delay(160); return mockSessions; }
  return requestJson<SessionSummary[]>("/api/v1/sessions");
}

export async function getSession(sessionId: string): Promise<SessionDetail> {
  if (apiConfig.enableMock) { await delay(160); return { ...mockSessionDetail, session_id: sessionId }; }
  return requestJson<SessionDetail>(`/api/v1/sessions/${sessionId}`);
}

export async function createSession(payload: { title: string }): Promise<SessionSummary> {
  if (apiConfig.enableMock) {
    await delay();
    return { session_id: `session-${Date.now()}`, title: payload.title, task_count: 0, latest_status: "pending", created_at: new Date().toISOString(), updated_at: new Date().toISOString() };
  }
  return requestJson<SessionSummary>("/api/v1/sessions", { method: "POST", body: JSON.stringify(payload) });
}

// ── Capability status ─────────────────────────────────────────
export async function getToolStatus(): Promise<{ name: string; enabled: boolean; category: string; health: "online" | "warning" | "offline"; latency_ms: number }[]> {
  if (apiConfig.enableMock) { await delay(160); return mockTools; }
  return requestJson("/api/v1/tools");
}

export async function getRagStatus(): Promise<RagStatus> {
  if (apiConfig.enableMock) { await delay(120); return mockRagStatus; }
  return requestJson<RagStatus>("/api/v1/rag/status");
}

export async function getSandboxStatus(): Promise<SandboxStatus> {
  if (apiConfig.enableMock) { await delay(120); return mockSandboxStatus; }
  return requestJson<SandboxStatus>("/api/v1/sandbox/status");
}

// ── Internal fetch helper ─────────────────────────────────────
export async function requestJson<T = unknown>(path: string, init?: RequestInit): Promise<T> {
  const r = await fetch(`${apiConfig.baseUrl}${path}`, {
    ...init,
    headers: { "Content-Type": "application/json", ...init?.headers },
  });
  if (!r.ok) {
    const text = await r.text().catch(() => "");
    throw new Error(text || r.statusText);
  }
  if (r.status === 204) return undefined as T;
  return r.json() as Promise<T>;
}
