import {
  AlertTriangle,
  Box,
  Database,
  Gauge,
  Globe2,
  KeyRound,
  LockKeyhole,
  ServerCog,
  ShieldCheck,
  WalletCards,
  type LucideIcon,
} from "lucide-react";
import type { HealthStatus, RagStatus, SandboxStatus, ToolStatus } from "../types";

interface CapabilityPanelProps {
  tools: ToolStatus[];
  rag: RagStatus;
  sandbox: SandboxStatus;
  health: HealthStatus;
  provider: string;
  model: string;
  complexityScore: number;
}

const healthLabel = {
  online: "在线",
  warning: "注意",
  offline: "离线",
  healthy: "健康",
  degraded: "降级",
  down: "中断",
  mock: "Mock",
};

export function CapabilityPanel({ tools, rag, sandbox, health, provider, model, complexityScore }: CapabilityPanelProps) {
  const webTool = tools.find((tool) => tool.name.includes("网页"));
  const riskLevel = complexityScore > 0.75 ? "需复核" : "稳定";

  return (
    <aside className="flex h-full flex-col gap-4">
      <section className="status-cabin">
        <div className="mb-4 flex items-center justify-between gap-3">
          <div className="flex items-center gap-2 text-textMain">
            <ServerCog className="h-4 w-4 text-energy" />
            <h2 className="text-base font-semibold">运行状态舱</h2>
          </div>
          <span className="status-ok">{healthLabel[health.status]}</span>
        </div>
        <div className="space-y-3 text-sm">
          <Metric icon={KeyRound} label="模型供应商" value={provider} />
          <Metric icon={Box} label="当前模型" value={model} />
          <Metric icon={Gauge} label="复杂度评分" value={`${Math.round(complexityScore * 100)}%`} />
          <Metric icon={WalletCards} label="预算上限" value="$3.00" />
          <Metric icon={AlertTriangle} label="风险等级" value={riskLevel} tone={riskLevel === "需复核" ? "risk" : "jade"} />
        </div>
      </section>

      <section className="status-cabin">
        <div className="mb-4 flex items-center gap-2 text-textMain">
          <Database className="h-4 w-4 text-jade" />
          <h2 className="text-base font-semibold">能力矩阵</h2>
        </div>
        <div className="grid grid-cols-2 gap-3 text-sm">
          <StatusTile label="RAG" value={rag.enabled ? "已接入" : "关闭"} tone={rag.health} />
          <StatusTile label="Sandbox" value={sandbox.enabled ? "待命" : "关闭"} tone={sandbox.health} />
          <StatusTile label="Web Search" value={webTool?.enabled ? "可调用" : "待配置"} tone={webTool?.health || "warning"} />
          <StatusTile label="工具权限" value="受控" tone="online" />
        </div>
      </section>

      <section className="status-cabin flex-1">
        <div className="mb-4 flex items-center gap-2 text-textMain">
          <Globe2 className="h-4 w-4 text-bronze" />
          <h2 className="text-base font-semibold">工具链状态</h2>
        </div>
        <div className="space-y-2">
          {tools.map((tool) => (
            <div key={tool.name} className="tool-row">
              <div>
                <div className="font-medium text-textMain">{tool.name}</div>
                <div className="text-xs text-textMuted">{tool.category} / {tool.latencyMs}ms</div>
              </div>
              <span className={tool.health === "online" ? "status-ok" : tool.health === "warning" ? "status-warn" : "status-risk"}>
                {healthLabel[tool.health]}
              </span>
            </div>
          ))}
        </div>
      </section>

      <section className="status-cabin">
        <div className="flex items-center gap-2 text-sm text-textMuted">
          <LockKeyhole className="h-4 w-4 text-bronze" />
          高风险工具仅在编排任务内调用，直连执行保持受控。
        </div>
      </section>
    </aside>
  );
}

function Metric({ icon: Icon, label, value, tone = "energy" }: { icon: LucideIcon; label: string; value: string; tone?: "energy" | "jade" | "risk" }) {
  const color = tone === "jade" ? "text-jade" : tone === "risk" ? "text-risk" : "text-energy";
  return (
    <div className="flex items-center justify-between gap-3 rounded-md border border-white/10 bg-white/[0.03] px-3 py-2">
      <span className="flex items-center gap-2 text-textMuted">
        <Icon className={`h-4 w-4 ${color}`} />
        {label}
      </span>
      <span className="max-w-[150px] truncate text-right font-medium text-textMain">{value}</span>
    </div>
  );
}

function StatusTile({ label, value, tone }: { label: string; value: string; tone: "online" | "warning" | "offline" }) {
  const cls = tone === "online" ? "border-jade/30 text-jade" : tone === "warning" ? "border-bronze/35 text-bronze" : "border-risk/35 text-risk";
  return (
    <div className={`matrix-tile ${cls}`}>
      <div className="text-xs opacity-75">{label}</div>
      <div className="mt-1 text-base font-semibold">{value}</div>
    </div>
  );
}
