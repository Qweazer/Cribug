import { useState } from "react";
import { Route, ChevronDown, ChevronRight, X, Shield, AlertTriangle, CheckCircle2, Cpu, Zap } from "lucide-react";
import type { RouterContract } from "../types";

interface RouterPanelProps {
  contract: RouterContract | null;
  onClose: () => void;
}

export function RouterPanel({ contract, onClose }: RouterPanelProps) {
  const [showCandidates, setShowCandidates] = useState(false);
  const [showRejected, setShowRejected] = useState(false);
  const [showScore, setShowScore] = useState(false);

  if (!contract) return null;

  const modeBadge = (mode: string) => {
    const colors: Record<string, string> = {
      direct_answer: "bg-jade/10 text-jade border-jade/30",
      rag_answer: "bg-energy/10 text-energy border-energy/30",
      dag_workflow: "bg-blue-400/10 text-blue-400 border-blue-400/30",
      reflection: "bg-purple-400/10 text-purple-400 border-purple-400/30",
      debate: "bg-bronze/10 text-bronze border-bronze/30",
      tot: "bg-orange-400/10 text-orange-400 border-orange-400/30",
      research_v2: "bg-pink-400/10 text-pink-400 border-pink-400/30",
      sandbox_execution: "bg-cyan-400/10 text-cyan-400 border-cyan-400/30",
      react_tool: "bg-green-400/10 text-green-400 border-green-400/30",
    };
    const cls = colors[mode] || "bg-white/[0.06] text-textMuted border-white/20";
    return <span className={`inline-block rounded px-2 py-0.5 text-[11px] border ${cls}`}>{mode}</span>;
  };

  return (
    <aside className="flex flex-col h-full border-l border-white/10 bg-[#030617]/60 overflow-y-auto">
      <div className="flex items-center justify-between px-4 py-3 border-b border-white/10">
        <div className="flex items-center gap-2">
          <Route className="h-4 w-4 text-energy" />
          <h2 className="text-sm font-semibold text-textMain">路由决策</h2>
        </div>
        <button onClick={onClose} className="text-textMuted hover:text-textMain"><X className="h-4 w-4" /></button>
      </div>

      <div className="p-3 space-y-3 text-sm">
        {/* Mode summary */}
        <div className="rounded-md border border-white/10 bg-white/[0.02] p-3 space-y-2">
          <Row label="计划模式" value={modeBadge(contract.planned_mode)} />
          <Row label="执行模式" value={modeBadge(contract.executed_mode)} />
          {contract.risk_level && <Row label="风险等级" value={<span className="text-textMain">{contract.risk_level}</span>} />}
          {contract.fallback_reason && <Row label="降级原因" value={<span className="text-bronze text-xs">{contract.fallback_reason}</span>} />}
        </div>

        {/* Classifier info */}
        <div className="rounded-md border border-white/10 bg-white/[0.02] p-3 space-y-2">
          <div className="text-xs font-semibold text-textMuted mb-1 flex items-center gap-1"><Cpu className="h-3.5 w-3.5" /> 分类器</div>
          <Row label="LLM 路由" value={contract.classifier_used ? <CheckCircle2 className="h-4 w-4 text-jade" /> : <X className="h-4 w-4 text-textMuted" />} />
          {contract.classifier_reason && <Row label="理由" value={<span className="text-textMuted text-xs">{contract.classifier_reason}</span>} />}
          <Row label="置信度" value={<span className="text-textMain">{contract.classifier_confidence.toFixed(2)}</span>} />
        </div>

        {/* Provider / Model */}
        <div className="rounded-md border border-white/10 bg-white/[0.02] p-3 space-y-2">
          <Row label="Provider" value={<span className="text-textMain">{contract.provider || "—"}</span>} />
          <Row label="Model" value={<span className="text-textMain">{contract.model_used || "—"}</span>} />
          <Row label="Mock" value={contract.mock ? <AlertTriangle className="h-4 w-4 text-bronze" /> : <CheckCircle2 className="h-4 w-4 text-jade" />} />
          <Row label="降级" value={contract.fallback_used ? <span className="text-bronze text-xs">是</span> : <span className="text-textMuted text-xs">否</span>} />
          <Row label="需审批" value={contract.approval_required ? <span className="text-bronze text-xs">是</span> : <span className="text-textMuted text-xs">否</span>} />
          <Row label="异步" value={contract.async_required ? <span className="text-textMain text-xs">是</span> : <span className="text-textMuted text-xs">否</span>} />
        </div>

        {/* Candidates (collapsible) */}
        {contract.candidates.length > 0 && (
          <div className="rounded-md border border-white/10 bg-white/[0.02] p-3">
            <button onClick={() => setShowCandidates(!showCandidates)} className="flex items-center justify-between w-full text-xs font-semibold text-textMuted">
              <span>候选模式 ({contract.candidates.length})</span>
              {showCandidates ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronRight className="h-3.5 w-3.5" />}
            </button>
            {showCandidates && (
              <div className="mt-2 space-y-1.5">
                {contract.candidates.map((c, i) => (
                  <div key={i} className="flex items-center justify-between text-xs">
                    <span className="flex items-center gap-1">{modeBadge(c.mode)}</span>
                    <span className="text-textMuted">{(c.score * 100).toFixed(0)}% {c.reason}</span>
                  </div>
                ))}
              </div>
            )}
          </div>
        )}

        {/* Rejected modes (collapsible) */}
        {contract.rejected_modes.length > 0 && (
          <div className="rounded-md border border-white/10 bg-white/[0.02] p-3">
            <button onClick={() => setShowRejected(!showRejected)} className="flex items-center justify-between w-full text-xs font-semibold text-textMuted">
              <span>被拒绝模式 ({contract.rejected_modes.length})</span>
              {showRejected ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronRight className="h-3.5 w-3.5" />}
            </button>
            {showRejected && (
              <div className="mt-2 space-y-1.5">
                {contract.rejected_modes.map((m, i) => (
                  <div key={i} className="text-xs">
                    <span className="mr-1">{modeBadge(m.mode)}</span>
                    <span className="text-textMuted">{m.reason || "后端未返回具体原因"}</span>
                  </div>
                ))}
              </div>
            )}
          </div>
        )}

        {/* Score breakdown (collapsible) */}
        {Object.keys(contract.score_breakdown).length > 0 && (
          <div className="rounded-md border border-white/10 bg-white/[0.02] p-3">
            <button onClick={() => setShowScore(!showScore)} className="flex items-center justify-between w-full text-xs font-semibold text-textMuted">
              <span>评分详情</span>
              {showScore ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronRight className="h-3.5 w-3.5" />}
            </button>
            {showScore && (
              <div className="mt-2 space-y-1">
                {Object.entries(contract.score_breakdown).map(([k, v]) => (
                  <div key={k} className="flex justify-between text-xs">
                    <span className="text-textMuted">{k}</span>
                    <span className="text-textMain">{(v * 100).toFixed(0)}%</span>
                  </div>
                ))}
              </div>
            )}
          </div>
        )}

        {/* Capabilities */}
        <div className="rounded-md border border-white/10 bg-white/[0.02] p-3 space-y-1.5">
          <div className="text-xs font-semibold text-textMuted mb-1 flex items-center gap-1"><Zap className="h-3.5 w-3.5" /> 能力状态</div>
          {contract.required_capabilities.length > 0 && (
            <div className="text-xs"><span className="text-jade">✓ 必需:</span> <span className="text-textMain">{contract.required_capabilities.join(", ")}</span></div>
          )}
          {contract.addon_capabilities.length > 0 && (
            <div className="text-xs"><span className="text-energy">+ 附加:</span> <span className="text-textMain">{contract.addon_capabilities.join(", ")}</span></div>
          )}
          {contract.disabled_capabilities.length > 0 && (
            <div className="text-xs"><span className="text-red-400">✗ 禁用:</span> <span className="text-textMain">{contract.disabled_capabilities.join(", ")}</span></div>
          )}
        </div>

        {/* Workspace artifacts */}
        {contract.workspace_artifacts_expected.length > 0 && (
          <div className="rounded-md border border-white/10 bg-white/[0.02] p-3">
            <div className="text-xs font-semibold text-textMuted mb-1 flex items-center gap-1"><Shield className="h-3.5 w-3.5" /> Workspace</div>
            <div className="text-xs text-textMain">{contract.workspace_artifacts_expected.join(", ")}</div>
          </div>
        )}
      </div>
    </aside>
  );
}

function Row({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-2">
      <span className="text-xs text-textMuted shrink-0">{label}</span>
      <span className="text-right">{value}</span>
    </div>
  );
}
