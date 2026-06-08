import { FormEvent, useMemo, useState } from "react";
import { ArrowRight, Clock, Loader2, Map, Plus, Route, Send, Sparkles } from "lucide-react";
import { getExecutionLogs, getTaskStatus, submitTask } from "../api/client";
import { mockHealthStatus, mockLogs, mockNodes, mockRagStatus, mockSandboxStatus, mockTools } from "../api/mockData";
import type { ExecutionLog, TaskDetailResponse } from "../types";
import { CapabilityPanel } from "./CapabilityPanel";
import { ExecutionLogPanel } from "./ExecutionLogPanel";
import { TaskFlowGraph } from "./TaskFlowGraph";

const starterTasks = [
  {
    title: "复杂产品需求拆解",
    query: "分析一个复杂产品需求，拆解为可执行研发计划",
    mode: "DAG",
    status: "运行中",
    complexity: "高",
    time: "刚刚",
  },
  {
    title: "多来源资料冲突核查",
    query: "核查多来源资料中的冲突点并给出证据链",
    mode: "研究",
    status: "待命",
    complexity: "中高",
    time: "45 分钟前",
  },
  {
    title: "沙箱验证方案生成",
    query: "生成带沙箱验证步骤的数据处理方案",
    mode: "验证",
    status: "完成",
    complexity: "中",
    time: "90 分钟前",
  },
];

function deriveComplexity(status: TaskDetailResponse): number {
  if (status.usage?.total_tokens) {
    return Math.min(status.usage.total_tokens / 10000, 1.0);
  }
  return 0.5;
}

function statusToProgress(s: string): number {
  switch (s) {
    case "pending": return 5;
    case "running": return 50;
    case "completed": return 100;
    case "failed": return 100;
    default: return 0;
  }
}

export function WorkbenchPage() {
  const [query, setQuery] = useState(starterTasks[0].query);
  const [isRunning, setIsRunning] = useState(false);
  const [taskStatus, setTaskStatus] = useState<TaskDetailResponse | null>(null);
  const [logs, setLogs] = useState<ExecutionLog[]>(mockLogs);
  const [lastTaskId, setLastTaskId] = useState<string | null>(null);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  const complexityScore = taskStatus ? deriveComplexity(taskStatus) : 0.5;
  const activeNodes = useMemo(() => {
    return mockNodes.map((node) => {
      if (node.type === "plan" && taskStatus?.status === "running") return { ...node, status: "active" as const };
      if (node.type === "route" && taskStatus) return { ...node, status: "completed" as const };
      return node;
    });
  }, [taskStatus]);

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!query.trim()) return;

    setIsRunning(true);
    setErrorMsg(null);
    try {
      // 1. Submit the task
      const submitted = await submitTask({
        query,
        config: { mode: "simple" },
      });
      setLastTaskId(submitted.task_id);

      // 2. Poll for completion
      for (let i = 0; i < 30; i++) {
        await new Promise((r) => setTimeout(r, 1000));
        const status = await getTaskStatus(submitted.task_id);
        setTaskStatus(status);
        if (status.status === "completed" || status.status === "failed") {
          const taskLogs = await getExecutionLogs(submitted.task_id);
          setLogs(taskLogs);
          break;
        }
      }
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err);
      setErrorMsg(msg);
      alert("提交失败: " + msg);
    }
    setIsRunning(false);
  }

  return (
    <main className="workbench-grid">
      <aside className="archive-panel">
        <div className="mb-4 flex items-center justify-between">
          <div>
            <h2 className="text-base font-semibold text-textMain">远征档案</h2>
            <p className="mt-1 text-xs text-textMuted">任务记录 / 执行模式 / 复杂度</p>
          </div>
          <button className="icon-button" title="新建任务档案">
            <Plus className="h-4 w-4" />
          </button>
        </div>
        <div className="space-y-3">
          {starterTasks.map((task) => (
            <button key={task.title} className="archive-card" onClick={() => setQuery(task.query)}>
              <div className="flex items-start justify-between gap-3">
                <div className="text-sm font-semibold text-textMain">{task.title}</div>
                <span className="archive-status">{task.status}</span>
              </div>
              <p className="mt-2 text-xs leading-5 text-textMuted">{task.query}</p>
              <div className="mt-3 flex flex-wrap gap-2 text-[11px] text-textMuted">
                <span className="tag">模式: {task.mode}</span>
                <span className="tag">复杂度: {task.complexity}</span>
                <span className="tag">{task.time}</span>
              </div>
            </button>
          ))}
        </div>
        <div className="route-ribbon">
          用户意图 <ArrowRight className="h-3 w-3" /> 智能路由 <ArrowRight className="h-3 w-3" /> DAG 拆解 <ArrowRight className="h-3 w-3" /> 反思验证
        </div>
      </aside>

      <section className="space-y-5">
        <section className="mission-console">
          <div className="mb-4 flex flex-wrap items-start justify-between gap-3">
            <div>
              <div className="signal-label mb-3">
                <Sparkles className="h-4 w-4" />
                智能体远征控制台
              </div>
              <h1 className="text-2xl font-semibold text-textMain">任务执行主区域</h1>
              <p className="mt-2 text-sm text-textMuted">
                从用户意图出发，感知复杂度，展开路径，调度工具，最终得到可验证结果。
              </p>
              {lastTaskId && (
                <p className="mt-1 text-xs text-textMuted">
                  Task: {lastTaskId}
                </p>
              )}
              {errorMsg && (
                <p className="mt-1 text-xs text-red-400">
                  错误: {errorMsg}
                </p>
              )}
            </div>
            <div className="mission-progress">
              <span>{taskStatus?.status?.toUpperCase() || "IDLE"}</span>
              <strong>{taskStatus ? Math.round(statusToProgress(taskStatus.status)) : 0}%</strong>
            </div>
          </div>

          <div className="intent-grid">
            <div className="intent-card">
              <div className="mb-2 flex items-center gap-2 text-sm font-semibold text-textMain">
                <Map className="h-4 w-4 text-energy" />
                用户意图区
              </div>
              <form onSubmit={handleSubmit} className="space-y-3">
                <textarea
                  value={query}
                  onChange={(event) => setQuery(event.target.value)}
                  className="min-h-[118px] w-full resize-none rounded-md border border-white/10 bg-black/20 px-4 py-3 text-sm leading-7 text-textMain outline-none transition placeholder:text-textMuted focus:border-energy/50"
                  placeholder="输入一个复杂任务，让 Cribug Agent 选择执行路径"
                />
                <button className="primary-button w-full justify-center" disabled={isRunning}>
                  {isRunning ? <Loader2 className="h-4 w-4 animate-spin" /> : <Send className="h-4 w-4" />}
                  {isRunning ? "远征执行中" : "提交任务"}
                </button>
              </form>
            </div>
            <div className="agent-response-card">
              <div className="mb-2 flex items-center gap-2 text-sm font-semibold text-textMain">
                <Route className="h-4 w-4 text-jade" />
                当前阶段提示
              </div>
              <p className="text-sm leading-7 text-textMuted">
                {taskStatus?.result
                  ? taskStatus.result.substring(0, 200)
                  : "已识别为复杂任务，正在生成可追踪执行计划。系统将优先调用知识检索与网页探索，再进入沙箱验证和反思复核。"}
              </p>
              <div className="mt-4 grid grid-cols-3 gap-2 text-xs">
                <Phase label="感知" value={taskStatus ? "完成" : "待命"} active={!!taskStatus} />
                <Phase label="计划" value={taskStatus?.status === "running" ? "运行中" : "待命"} active={taskStatus?.status === "running"} />
                <Phase label="验证" value={taskStatus?.status === "completed" ? "完成" : "待命"} active={taskStatus?.status === "completed"} />
              </div>
            </div>
          </div>
        </section>

        <TaskFlowGraph nodes={activeNodes} />
        <ExecutionLogPanel logs={logs} />
      </section>

      <CapabilityPanel
        tools={mockTools}
        rag={mockRagStatus}
        sandbox={mockSandboxStatus}
        health={mockHealthStatus}
        provider="OpenAI"
        model={taskStatus?.model || "gpt-5-mini"}
        complexityScore={complexityScore}
      />
    </main>
  );
}

function Phase({ label, value, active = false }: { label: string; value: string; active?: boolean }) {
  return (
    <div className={`rounded-md border p-3 ${active ? "border-energy/40 bg-energy/10 text-energy" : "border-white/10 bg-white/[0.03] text-textMuted"}`}>
      <div className="flex items-center gap-1">
        <Clock className="h-3 w-3" />
        {label}
      </div>
      <div className="mt-1 font-semibold">{value}</div>
    </div>
  );
}
