import {
  Activity,
  CheckCircle2,
  Compass,
  Database,
  FileCheck2,
  FlaskConical,
  GitBranch,
  Globe2,
  Network,
  ShieldCheck,
} from "lucide-react";
import type { ExecutionNode, ExecutionNodeStatus } from "../types";

const statusClass: Record<ExecutionNodeStatus, string> = {
  completed: "gate-completed",
  active: "gate-active",
  pending: "gate-pending",
  failed: "gate-failed",
};

const iconMap = {
  route: Compass,
  plan: Network,
  tool: Activity,
  rag: Database,
  sandbox: FlaskConical,
  web: Globe2,
  verify: ShieldCheck,
  result: CheckCircle2,
};

const semanticIcon: Record<string, typeof Compass> = {
  intent: Compass,
  route: Compass,
  plan: Network,
  dag: GitBranch,
  rag: Database,
  web: Globe2,
  sandbox: FlaskConical,
  tool: Activity,
  verify: ShieldCheck,
  result: FileCheck2,
};

interface TaskFlowGraphProps {
  nodes: ExecutionNode[];
}

export function TaskFlowGraph({ nodes }: TaskFlowGraphProps) {
  const nodeMap = new Map(nodes.map((node) => [node.id, node]));

  return (
    <section className="path-panel">
      <div className="mb-4 flex items-center justify-between gap-3">
        <div>
          <h2 className="text-lg font-semibold text-textMain">远征路径图</h2>
          <p className="mt-1 text-sm text-textMuted">节点像能力门一样依次点亮，复杂任务会展开为多分支 DAG。</p>
        </div>
        <div className="rounded-full border border-bronze/40 bg-bronze/10 px-3 py-1 text-xs text-bronze">DAG 多阶段执行</div>
      </div>

      <div className="expedition-path-canvas">
        <svg className="absolute inset-0 h-full w-full" viewBox="0 0 100 100" preserveAspectRatio="none" aria-hidden="true">
          <defs>
            <linearGradient id="path-line" x1="0" x2="1" y1="0" y2="0">
              <stop offset="0%" stopColor="#0E7490" stopOpacity="0.18" />
              <stop offset="45%" stopColor="#27D3FF" stopOpacity="0.76" />
              <stop offset="74%" stopColor="#42F2B3" stopOpacity="0.58" />
              <stop offset="100%" stopColor="#C89B4A" stopOpacity="0.3" />
            </linearGradient>
          </defs>
          {nodes.flatMap((node) =>
            node.links.map((targetId) => {
              const target = nodeMap.get(targetId);
              if (!target) return null;
              const midX = (node.x + target.x) / 2;
              const lift = node.y < target.y ? -8 : 8;
              return (
                <path
                  key={`${node.id}-${targetId}`}
                  d={`M ${node.x} ${node.y} Q ${midX} ${(node.y + target.y) / 2 + lift} ${target.x} ${target.y}`}
                  fill="none"
                  stroke="url(#path-line)"
                  strokeWidth="0.65"
                  strokeLinecap="round"
                  className="flow-line"
                />
              );
            }),
          )}
        </svg>

        {nodes.map((node) => {
          const Icon = semanticIcon[node.id] || iconMap[node.type];
          return (
            <div
              key={node.id}
              className={`expedition-gate ${statusClass[node.status]}`}
              style={{ left: `${node.x}%`, top: `${node.y}%` }}
              title={node.description}
            >
              <span className="gate-mark">
                <Icon className="h-4 w-4" aria-hidden="true" />
              </span>
              <span className="text-xs font-semibold leading-tight">{node.label}</span>
              <span className="mt-1 line-clamp-2 text-[10px] leading-tight opacity-75">{node.description}</span>
            </div>
          );
        })}
      </div>
    </section>
  );
}
