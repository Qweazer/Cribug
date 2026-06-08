import { Activity, Bot, Clock, Coins, Eye, RotateCcw, Sparkles, type LucideIcon } from "lucide-react";
import type { ExecutionLog } from "../types";

type LogCategory = "reasoning" | "action" | "observation" | "reflection" | "cost" | "system";

const iconMap: Record<LogCategory, LucideIcon> = {
  reasoning: Bot,
  action: Activity,
  observation: Eye,
  reflection: RotateCcw,
  cost: Coins,
  system: Sparkles,
};

const labelMap: Record<LogCategory, string> = {
  reasoning: "推理",
  action: "行动",
  observation: "观察",
  reflection: "反思",
  cost: "成本",
  system: "系统",
};

interface ParsedLog {
  id: string;
  category: LogCategory;
  title: string;
  message: string;
  timestamp: string;
  provider?: string;
  model?: string;
  costUsd?: number;
  durationMs?: number;
}

function categoryFromEventType(eventType: string): LogCategory {
  switch (eventType) {
    case "task.reasoning":
    case "agent.reasoning":
      return "reasoning";
    case "task.action":
    case "agent.action":
      return "action";
    case "task.observation":
    case "agent.observation":
      return "observation";
    case "task.reflection":
    case "agent.reflection":
      return "reflection";
    case "task.cost":
    case "cost":
      return "cost";
    default:
      return "system";
  }
}

function parseLogPayload(log: ExecutionLog): ParsedLog {
  let parsed: Record<string, unknown> = {};
  try {
    parsed = JSON.parse(log.payload || "{}");
  } catch {
    // not JSON, use raw
  }

  const category = categoryFromEventType(log.event_type);

  return {
    id: log.id,
    category,
    title: (parsed.title as string) || (parsed.agent_role as string) || log.event_type,
    message: (parsed.message as string) || (parsed.output as string) || log.payload || "",
    timestamp: log.created_at,
    provider: parsed.provider as string | undefined,
    model: parsed.model as string | undefined,
    costUsd: parsed.cost_usd as number | undefined,
    durationMs: (parsed.duration_ms as number) || (parsed.latency_ms as number) || undefined,
  };
}

interface ExecutionLogPanelProps {
  logs: ExecutionLog[];
}

export function ExecutionLogPanel({ logs }: ExecutionLogPanelProps) {
  const parsedLogs = logs.map(parseLogPayload);

  return (
    <section className="trace-panel">
      <div className="mb-4 flex items-center justify-between gap-3">
        <div>
          <h2 className="text-lg font-semibold text-textMain">执行轨迹</h2>
          <p className="mt-1 text-sm text-textMuted">记录推理、行动、观察、反思、成本、耗时、模型与 Provider。</p>
        </div>
        <div className="flex items-center gap-2 rounded-full border border-white/10 px-3 py-1 text-xs text-textMuted">
          <Clock className="h-3.5 w-3.5 text-energy" />
          实时轨迹
        </div>
      </div>

      <div className="trace-list">
        {parsedLogs.map((log, index) => {
          const Icon = iconMap[log.category];
          return (
            <article key={log.id} className="trace-item">
              <div className="trace-index">{String(index + 1).padStart(2, "0")}</div>
              <span className="trace-icon">
                <Icon className="h-4 w-4" />
              </span>
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <div className="flex items-center gap-2">
                    <span className="text-sm font-semibold text-textMain">{log.title}</span>
                    <span className="rounded-full border border-energy/20 px-2 py-0.5 text-xs text-energy">{labelMap[log.category]}</span>
                  </div>
                  <time className="text-xs text-textMuted">{new Date(log.timestamp).toLocaleTimeString("zh-CN")}</time>
                </div>
                <p className="mt-2 text-sm leading-6 text-textMuted">{log.message}</p>
                {(log.provider || log.model || log.costUsd || log.durationMs) && (
                  <div className="mt-3 flex flex-wrap gap-2 text-xs text-textMuted">
                    {log.provider && <span className="tag">Provider: {log.provider}</span>}
                    {log.model && <span className="tag">模型: {log.model}</span>}
                    {log.durationMs && <span className="tag">耗时: {log.durationMs}ms</span>}
                    {log.costUsd && <span className="tag">成本: ${log.costUsd.toFixed(3)}</span>}
                  </div>
                )}
              </div>
            </article>
          );
        })}
      </div>
    </section>
  );
}
