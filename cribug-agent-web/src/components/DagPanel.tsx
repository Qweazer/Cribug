import { useEffect, useState } from "react";
import { GitBranch, Clock, CheckCircle2, Loader2, XCircle, SkipForward, Sparkles, Target } from "lucide-react";
import { apiConfig } from "../api/config";

interface DagNode {
  node_id: string;
  status: string;
  layer: number;
  dependencies: string[];
  name?: string;
  input?: string;
  type?: string;
  output?: string;
}

interface DagData {
  task_id: string;
  workflow_id: string;
  meta: Record<string, string>;
  nodes_status: Record<string, string>;
  nodes_detail: Record<string, string>;
}

interface DagPanelProps {
  taskId: string | null;
}

// Chinese-friendly name mapping with emoji
function nodeDisplay(node: DagNode): { icon: string; label: string } {
  const t = (node.type || node.node_id).toLowerCase();
  const name = node.name || node.node_id;
  // Use the Name field if available (e.g., "Research Topic" -> "调研主题")
  const shortName = name.length > 12 ? name.slice(0, 12) + "..." : name;

  if (t.includes("research")) return { icon: "🔍", label: shortName || "资料调研" };
  if (t.includes("analyz")) return { icon: "🧩", label: shortName || "需求分析" };
  if (t.includes("compar")) return { icon: "⚖️", label: shortName || "方案对比" };
  if (t.includes("valid")) return { icon: "✅", label: shortName || "交叉验证" };
  if (t.includes("conclud")) return { icon: "📋", label: shortName || "独立总结" };
  if (t.includes("draft")) return { icon: "📝", label: shortName || "草稿汇总" };
  if (t.includes("review")) return { icon: "🔎", label: shortName || "最终审查" };
  if (t.includes("extra") || t.includes("mock")) return { icon: "🔧", label: shortName || "辅助验证" };
  if (t.includes("synthesis")) return { icon: "🧠", label: shortName || "综合归纳" };
  return { icon: "📌", label: shortName || node.node_id };
}

const statusColors: Record<string, { fill: string; stroke: string; text: string; glow: string }> = {
  completed:   { fill: "#42F2B3", stroke: "#42F2B3", text: "text-jade", glow: "shadow-[0_0_12px_rgba(66,242,179,0.5)]" },
  running:     { fill: "#27D3FF", stroke: "#27D3FF", text: "text-energy", glow: "shadow-[0_0_14px_rgba(39,211,255,0.6)] animate-pulse" },
  pending:     { fill: "#3A3F50", stroke: "#555A6A", text: "text-textMuted", glow: "" },
  skipped:     { fill: "#2A2D35", stroke: "#3A3F50", text: "text-textMuted/50", glow: "" },
  failed:      { fill: "#F87171", stroke: "#F87171", text: "text-red-400", glow: "shadow-[0_0_10px_rgba(248,113,113,0.4)]" },
};

export function DagPanel({ taskId }: DagPanelProps) {
  const [dag, setDag] = useState<DagData | null>(null);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (!taskId) return;
    let cancelled = false;
    setLoading(true);
    async function fetchDag() {
      try {
        const resp = await fetch(`${apiConfig.baseUrl}/api/v1/tasks/${taskId}/dag`);
        if (!cancelled) setDag(await resp.json());
      } catch {}
      if (!cancelled) setLoading(false);
    }
    fetchDag();
    const interval = setInterval(fetchDag, 2000);
    return () => { cancelled = true; clearInterval(interval); };
  }, [taskId]);

  if (!taskId) return null;

  const nodes: DagNode[] = [];
  if (dag?.nodes_detail) {
    for (const [, raw] of Object.entries(dag.nodes_detail)) {
      try { nodes.push(JSON.parse(raw) as DagNode); } catch {}
    }
  }

  const layers: Record<number, DagNode[]> = {};
  nodes.forEach(n => { if (!layers[n.layer]) layers[n.layer] = []; layers[n.layer].push(n); });
  const sortedLayers = Object.keys(layers).map(Number).sort((a, b) => a - b);
  const maxLayer = sortedLayers.length > 0 ? sortedLayers[sortedLayers.length - 1] : 0;
  const maxNodesPerLayer = Math.max(1, ...sortedLayers.map(l => layers[l].length));

  // Map node to position for SVG connections
  const nodePos = new Map<string, { x: number; y: number }>();
  sortedLayers.forEach(layer => {
    const layerNodes = layers[layer];
    layerNodes.forEach((node, i) => {
      const x = 8 + (layer / Math.max(maxLayer, 1)) * 84;
      const y = 10 + ((i + 0.5) / layerNodes.length) * (80 / Math.max(maxNodesPerLayer / 6, 1));
      nodePos.set(node.node_id, { x: Math.min(x, 90), y: Math.min(y, 88) });
    });
  });

  // Build edges from dependencies
  const edges: Array<{ from: string; to: string }> = [];
  nodes.forEach(n => {
    n.dependencies?.forEach(depId => {
      if (nodePos.has(depId) && nodePos.has(n.node_id)) {
        edges.push({ from: depId, to: n.node_id });
      }
    });
  });

  const completedCount = nodes.filter(n => n.status === "completed").length;

  return (
    <div className="flex flex-col h-full border-l border-white/10 bg-[#030617]/60 overflow-y-auto">
      <div className="px-4 py-3 border-b border-white/10">
        <div className="flex items-center gap-2 text-sm font-semibold text-textMain">
          <GitBranch className="h-4 w-4 text-energy" /> DAG 探索地图
        </div>
        <div className="mt-1.5 flex items-center gap-3 text-xs text-textMuted">
          <span>{nodes.length} 节点</span>
          <span className="text-jade">{completedCount} 已到达</span>
          {loading && <Loader2 className="h-3 w-3 animate-spin" />}
        </div>
      </div>

      {/* Treasure map visualization */}
      <div className="relative mx-2 my-3 rounded-lg border border-white/10 bg-[#020412]/80 overflow-hidden" style={{ minHeight: "320px" }}>
        <svg className="absolute inset-0 w-full h-full" viewBox="0 0 100 100" preserveAspectRatio="xMidYMid meet">
          <defs>
            <linearGradient id="map-line" x1="0" y1="0" x2="1" y2="0">
              <stop offset="0%" stopColor="#27D3FF" stopOpacity="0.15" />
              <stop offset="50%" stopColor="#42F2B3" stopOpacity="0.6" />
              <stop offset="100%" stopColor="#C89B4A" stopOpacity="0.4" />
            </linearGradient>
            <filter id="glow">
              <feGaussianBlur stdDeviation="1.5" result="blur" />
              <feMerge><feMergeNode in="blur" /><feMergeNode in="SourceGraphic" /></feMerge>
            </filter>
          </defs>

          {/* Connection lines */}
          {edges.map((e, i) => {
            const from = nodePos.get(e.from);
            const to = nodePos.get(e.to);
            if (!from || !to) return null;
            const fromNode = nodes.find(n => n.node_id === e.from);
            const toNode = nodes.find(n => n.node_id === e.to);
            const fromDone = fromNode?.status === "completed";
            const toDone = toNode?.status === "completed";
            const active = fromDone && !toDone;
            return (
              <path
                key={i}
                d={`M ${from.x} ${from.y} C ${(from.x+to.x)/2} ${from.y-5} ${(from.x+to.x)/2} ${to.y+5} ${to.x} ${to.y}`}
                fill="none"
                stroke={fromDone ? (active ? "#27D3FF" : "#42F2B3") : "#3A3F50"}
                strokeWidth={active ? "0.7" : "0.35"}
                strokeDasharray={active ? "2 1" : "1 2"}
                opacity={fromDone ? 0.7 : 0.2}
                className={active ? "animate-pulse" : ""}
              />
            );
          })}
        </svg>

        {/* Nodes */}
        {nodes.map(node => {
          const pos = nodePos.get(node.node_id);
          if (!pos) return null;
          const cfg = statusColors[node.status] || statusColors.pending;
          const display = nodeDisplay(node);
          const isEndpoint = node.status === "completed" && !nodes.some(n => n.dependencies?.includes(node.node_id));

          return (
            <div
              key={node.node_id}
              className="absolute transform -translate-x-1/2 -translate-y-1/2 flex flex-col items-center gap-0.5"
              style={{ left: `${pos.x}%`, top: `${pos.y}%` }}
            >
              {/* Node circle */}
              <div
                className={`rounded-full flex items-center justify-center border-2 transition-all duration-700 ${cfg.glow}`}
                style={{
                  width: "22px", height: "22px",
                  backgroundColor: cfg.fill + "22",
                  borderColor: cfg.stroke,
                }}
                title={`${display.label}\n${node.input || ""}\n${node.output || ""}`}
              >
                <span className="text-[9px]">{display.icon}</span>
              </div>
              {/* Label */}
              <span className={`text-[8px] leading-tight text-center max-w-[60px] ${cfg.text} ${node.status === "completed" ? "font-medium" : ""}`}>
                {display.label}
              </span>
              {/* Endpoint marker */}
              {isEndpoint && node.status === "completed" && (
                <span className="text-[9px] mt-0.5">🏁</span>
              )}
            </div>
          );
        })}

        {/* Start marker */}
        {sortedLayers.length > 0 && sortedLayers[0] === 0 && (
          <div className="absolute left-0 top-1/2 -translate-y-1/2 text-[10px] text-textMuted pl-1">
            <span className="text-energy">▶</span>
          </div>
        )}
        {/* End marker */}
        {completedCount === nodes.length && nodes.length > 0 && (
          <div className="absolute right-0 top-1/2 -translate-y-1/2 pr-1">
            <span className="text-[14px]">🎯</span>
          </div>
        )}
      </div>

      {/* Legend + timeline below map */}
      <div className="px-3 pb-3 space-y-2">
        {/* Legend */}
        <div className="flex items-center gap-3 text-[10px] text-textMuted flex-wrap">
          <span className="flex items-center gap-1"><span className="w-2.5 h-2.5 rounded-full bg-jade/60" /> 已完成</span>
          <span className="flex items-center gap-1"><span className="w-2.5 h-2.5 rounded-full bg-energy/60 animate-pulse" /> 进行中</span>
          <span className="flex items-center gap-1"><span className="w-2.5 h-2.5 rounded-full bg-red-400/60" /> 失败</span>
          <span className="flex items-center gap-1"><span className="w-2.5 h-2.5 rounded-full bg-[#3A3F50]" /> 跳过</span>
        </div>

        {/* Node detail list */}
        {nodes.length > 0 && (
          <div className="space-y-1 max-h-48 overflow-y-auto">
            {sortedLayers.map(layer => (
              <div key={layer}>
                <div className="text-[10px] text-textMuted/60 mb-1">第 {layer + 1} 层</div>
                {layers[layer].map(node => {
                  const cfg = statusColors[node.status] || statusColors.pending;
                  const display = nodeDisplay(node);
                  return (
                    <div key={node.node_id} className={`flex items-center gap-2 rounded px-2 py-1 text-[11px] ${cfg.text}`}>
                      <span>{display.icon}</span>
                      <span className="flex-1">{display.label}</span>
                      <span className="text-[10px] opacity-60">
                        {node.status === "completed" ? "✓" : node.status === "failed" ? "✗" : node.status === "skipped" ? "→" : "○"}
                      </span>
                    </div>
                  );
                })}
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
