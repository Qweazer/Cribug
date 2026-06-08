export type TaskMode = "simple" | "dag" | "multi_agent" | "swarm";
export type TaskStatus = "pending" | "running" | "completed" | "failed" | "budget_exceeded" | "cancelled" | "waiting_for_approval";
export type ExecutionLogType = "reasoning" | "action" | "observation" | "reflection" | "cost" | "system";

// ── Router Contract (frontend-aligned) ────────────────────────────

export interface FrontendExplanation {
  selected_mode_human: string;
  why: string;
  capability_summary: string[];
  cost_estimate: string;
  approval_needed: boolean;
}

export interface ModeCandidate {
  mode: string;
  score: number;
  reason: string;
}

export interface RejectedMode {
  mode: string;
  reason: string;
}

export interface RouterContract {
  case_id?: string;
  task_id: string;
  status: string;
  planned_mode: string;
  executed_mode: string;
  fallback_reason: string;
  frontend_explanation: FrontendExplanation | null;
  capability_summary: string[];
  addon_capabilities: string[];
  required_capabilities: string[];
  disabled_capabilities: string[];
  workspace_artifacts_expected: string[];
  approval_required: boolean;
  async_required: boolean;
  candidates: ModeCandidate[];
  rejected_modes: RejectedMode[];
  score_breakdown: Record<string, number>;
  signals: Record<string, unknown>;
  classifier_used: boolean;
  classifier_reason: string;
  classifier_confidence: number;
  provider: string;
  model_used: string;
  mock: boolean;
  fallback_used: boolean;
  audit_id?: string;
  audit_url_hint?: string;
  estimated_cost_usd?: number;
  estimated_latency_ms?: number;
  risk_level?: string;
}

// ── Backend-aligned request types ──────────────────────────────

export interface TaskRequest {
  query: string;
  session_id?: string;
  config?: {
    mode?: string;
    model?: string;
    temperature?: number;
    max_total_tokens?: number;
    max_completion_tokens?: number;
    enable_tools?: boolean;
    enable_react?: boolean;
    max_parallel_agents?: number;
  };
}

export interface TaskSubmitRequest extends TaskRequest {}

export interface TaskSubmitResponse {
  task_id: string;
  workflow_id: string;
  run_id: string | null;
  status: string;
  stream_url: string;
}

export interface CreateTaskResponse extends TaskSubmitResponse {}

// ── Backend-aligned response types ────────────────────────────

export interface TaskUsage {
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
}

export interface TaskDetailResponse {
  task_id: string;
  workflow_id: string;
  run_id: string | null;
  session_id: string | null;
  status: TaskStatus;
  result: string | null;
  error: string | null;
  error_type: string | null;
  usage: TaskUsage | null;
  model: string;
  max_total_tokens: number;
  max_completion_tokens: number;
  created_at: string;
  updated_at: string;
}

// ── Task result (GET /api/v1/tasks/{id}/result) ────────────────

export interface FrontendRouterContract {
  selected_mode: string;
  planned_mode: string;
  executed_mode: string;
  fallback_reason: string;
  approval_required: boolean;
  approval_reason: string;
  risk_level: string;
  model_tier: string;
  estimated_cost_usd: number;
  estimated_latency_ms: number;
  async_required: boolean;
  audit_required: boolean;
  policy_version: string;
  frontend_explanation: FrontendExplanation | null;
  addon_capabilities: string[];
  required_capabilities: string[];
  disabled_capabilities: string[];
  candidates: unknown[];
  rejected_modes: unknown[];
  score_breakdown: Record<string, number>;
  reason_codes: string[];
  workspace_artifacts_expected: string[];
}

export interface FrontendExplanation {
  selected_mode_human: string;
  why: string;
  capability_summary: string[];
  cost_estimate: string;
  approval_needed: boolean;
}

export interface RoutedExecutionResult {
  session_id: string;
  workflow_id: string;
  run_id: string;
  status: string;
  decision?: {
    mode: string;
    planned_mode: string;
    risk_level: string;
    requires_approval: boolean;
    workflow_type: string;
  };
  final_answer_text: string;
  final_answer_ref: string;
  pending_approval_id: string;
  reason: string;
  provider: string;
  model_used: string;
  mode: string;
  mock: boolean;
  fallback_used: boolean;
  tokens_used: number;
  cost_usd: number;
  metadata: Record<string, unknown>;
}

export interface TaskResultResponse {
  task_id: string;
  workflow_id: string;
  run_id: string | null;
  session_id: string | null;
  status: string;
  result: RoutedExecutionResult | null;
  frontend_contract: FrontendRouterContract | null;
  error: string | null;
  error_type: string | null;
  polled_at: string;
}

// ── Chat Message ──────────────────────────────────────────────

export interface TaskMessage {
  id: string;
  role: "user" | "assistant" | "system";
  content: string;
  task_id?: string;
  created_at: string;
}

export interface SubmitOptions {
  mode?: string;
  allowTools?: boolean;
  allowResearch?: boolean;
  allowSandbox?: boolean;
  requireCitations?: boolean;
}

// ── Session types ──────────────────────────────────────────────

export interface Session {
  session_id: string;
  title: string;
  task_count: number;
  latest_status: string;
  created_at: string;
  updated_at: string;
}

export interface ChatMessage {
  id: string;
  role: "user" | "assistant" | "system";
  content: string;
  task_id: string | null;
  created_at: string;
}

export interface SessionDetail {
  session_id: string;
  messages: ChatMessage[];
  tasks: TaskSummary[];
}

export interface TaskSummary {
  task_id: string;
  query: string;
  status: string;
  created_at: string;
}

// ── Provider / Config types (aligned with backend) ─────────────

export interface ProviderItem {
  id: string;
  display_name: string;
  default_chat_model: string;
  base_url: string;
  openai_compatible: boolean;
  timeout: number;
  models: Array<{ id: string; display_name: string }>;
}

export interface ProviderSummary {
  id: string;
  name: string;
  models: string[];
  enabled: boolean;
}

export interface ProviderConfig {
  provider: string;
  chat_model: string;
  embedding_model: string;
  api_key_env: string;
  base_url_override: string;
  allow_mock_fallback: boolean;
  require_real: boolean;
}

export interface TestConnectionResult {
  status: string;
  provider: string;
  model: string;
  mock: boolean;
  fallback_used: boolean;
  answer: string;
  answer_len: number;
}

// ── Capability / Status types ──────────────────────────────────

export interface ToolStatus {
  name: string;
  enabled: boolean;
  category: string;
  health: "online" | "warning" | "offline";
  latency_ms: number;
}

export interface RagStatus {
  enabled: boolean;
  collections: number;
  indexed_documents: number;
  last_sync_at: string;
  health: "online" | "warning" | "offline";
}

export interface SandboxStatus {
  enabled: boolean;
  active_runners: number;
  queued_jobs: number;
  health: "online" | "warning" | "offline";
}

export interface HealthStatus {
  status: "healthy" | "degraded" | "down";
  api: "online" | "mock" | "offline";
  version: string;
  checked_at: string;
  dependencies?: Record<string, string>;
}

// ── Log types ──────────────────────────────────────────────────

export interface ExecutionLog {
  id: string;
  event_type: string;
  payload: string;
  created_at: string;
}

// ── Legacy UI types (kept for component compatibility) ─────────

export interface TaskResult {
  taskId: string;
  status: TaskStatus;
  title: string;
  summary: string;
  answer: string;
  citations: Array<{ title: string; url: string }>;
  costUsd: number;
  durationMs: number;
  model: string;
  provider: string;
  completedAt?: string;
}

export interface TaskStatusDetail {
  taskId: string;
  status: TaskStatus;
  progress: number;
  mode: TaskMode;
  currentNodeId?: string;
  complexityScore: number;
  updatedAt: string;
}

export interface ExecutionNode {
  id: string;
  label: string;
  description: string;
  status: "pending" | "active" | "completed" | "failed";
  type: "route" | "plan" | "tool" | "rag" | "sandbox" | "web" | "verify" | "result";
  x: number;
  y: number;
  links: string[];
}
