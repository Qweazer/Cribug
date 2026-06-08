import { useCallback, useEffect, useRef, useState } from "react";
import { Sparkles, Route, GitBranch, Edit2, Trash2, Check, X } from "lucide-react";
import { submitRoutedTask, previewRoute, listSessions, getHealthStatus, streamTaskEvents, cancelTask } from "./api/client";
import { loadSampleContract } from "./mocks/routerContracts";
import type { TaskMessage, SessionSummary, RouterContract, SubmitOptions, HealthStatus } from "./types";
import { SessionSidebar } from "./components/SessionSidebar";
import { ChatView } from "./components/ChatView";
import { RouterPanel } from "./components/RouterPanel";
import { DagPanel } from "./components/DagPanel";
import { JsonDebugPanel } from "./components/JsonDebugPanel";

export default function App() {
  const [messages, setMessages] = useState<TaskMessage[]>([]);
  const [sessions, setSessions] = useState<SessionSummary[]>([]);
  const [activeSessionId, setActiveSessionId] = useState<string | null>(null);
  const [isRunning, setIsRunning] = useState(false);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);
  const [routerContract, setRouterContract] = useState<RouterContract | null>(null);
  const [showRouter, setShowRouter] = useState(false);
  const [showDag, setShowDag] = useState(false);
  const [backendOnline, setBackendOnline] = useState(true);
  const [health, setHealth] = useState<HealthStatus | null>(null);
  const [rawResponse, setRawResponse] = useState<unknown>(null);
  const [activeTaskId, setActiveTaskId] = useState<string | null>(null);
  const [streamingContent, setStreamingContent] = useState("");
  const [routing, setRouting] = useState(false);
  const [routingPreview, setRoutingPreview] = useState<{ planned_mode: string; complexity: number; reason: string } | null>(null);
  const stopStreamRef = useRef<(() => void) | null>(null);

  useEffect(() => { getHealthStatus().then(h => { setHealth(h); setBackendOnline(true); }).catch(() => { setBackendOnline(false); }); }, []);
  useEffect(() => { listSessions().then(setSessions).catch(() => {}); }, []);

  const addMessage = useCallback((role: "user" | "assistant" | "system", content: string, taskId?: string) => {
    const msg: TaskMessage = { id: `${Date.now()}-${Math.random().toString(36).slice(2,8)}`, role, content, task_id: taskId, created_at: new Date().toISOString() };
    setMessages(p => [...p, msg]);
    return msg;
  }, []);

  const updateLastAssistant = useCallback((content: string) => {
    setMessages(p => {
      const arr = [...p];
      for (let i = arr.length - 1; i >= 0; i--) {
        if (arr[i].role === "assistant") { arr[i] = { ...arr[i], content }; break; }
      }
      return arr;
    });
  }, []);

  function handleSubmit(query: string, _options: SubmitOptions) {
    setIsRunning(true); setErrorMsg(null); setRouterContract(null); setRoutingPreview(null); setStreamingContent("");
    addMessage("user", query);
    addMessage("assistant", "", undefined);

    // Step 1: LLM 路由预览 — 让用户看到后端会用什么模式
    setRouting(true);
    previewRoute({ query })
      .then(preview => {
        const plan = preview.frontend_contract || preview.decision;
        const plannedMode = plan?.selected_mode || plan?.planned_mode || plan?.mode || "auto";
        const complexity = preview.decision?.mode || "auto";
        setRoutingPreview({ planned_mode: plannedMode, complexity: 0, reason: preview.reason || "" });
        return submitRoutedTask({ query });
      })
      .then(resp => {
        setRouting(false);
        setRawResponse(resp);

        // Set router contract from response
        if (resp.frontend_contract) {
          setRouterContract({
            task_id: resp.task_id || resp.workflow_id,
            status: resp.status,
            planned_mode: resp.frontend_contract.planned_mode,
            executed_mode: resp.frontend_contract.executed_mode,
            fallback_reason: resp.frontend_contract.fallback_reason,
            frontend_explanation: null,
            capability_summary: [],
            addon_capabilities: [],
            required_capabilities: [],
            disabled_capabilities: [],
            workspace_artifacts_expected: [],
            approval_required: resp.frontend_contract.approval_required,
            async_required: resp.frontend_contract.async_required,
            candidates: resp.frontend_contract.candidates || [],
            rejected_modes: resp.frontend_contract.rejected_modes || [],
            score_breakdown: resp.frontend_contract.score_breakdown || {},
            signals: {},
            classifier_used: true,
            classifier_reason: resp.reason || "",
            classifier_confidence: 0,
            provider: "auto",
            model_used: "auto",
            mock: false,
            fallback_used: !!resp.frontend_contract.fallback_reason,
            audit_id: undefined,
            audit_url_hint: undefined,
            estimated_cost_usd: resp.frontend_contract.estimated_cost_usd,
            estimated_latency_ms: resp.frontend_contract.estimated_latency_ms,
            risk_level: resp.frontend_contract.risk_level,
          });
          setShowRouter(true);
        }

        // Handle approval gate
        if (resp.pending_approval_id) {
          updateLastAssistant(`**等待审批中** (approval_id: ${resp.pending_approval_id})\n\n风险等级: ${resp.frontend_contract?.risk_level || "medium"}`);
          finalize(resp.workflow_id, "");
          return;
        }

        const taskId = resp.task_id || resp.workflow_id;
        setActiveTaskId(taskId);
        setShowDag(true);

        // If sync result already has answer
        if (resp.final_answer_text) {
          updateLastAssistant(resp.final_answer_text);
          finalize(taskId, resp.final_answer_text);
          return;
        }

        // Async: open SSE stream
        const stop = streamTaskEvents(taskId, (evt) => {
          if (evt.type === "delta" || evt.type === "message.delta" || evt.type === "LLM_PARTIAL") {
            const delta = (evt.data as { content?: string; delta?: string })?.content
              || (evt.data as { content?: string; delta?: string })?.delta
              || (typeof evt.data === "string" ? evt.data : "");
            if (delta) setStreamingContent(prev => prev + delta);
          } else if (evt.type === "TASK_COMPLETED" || evt.type === "task.completed") {
            const finalResult = (evt.data as { result?: string })?.result || "";
            if (finalResult) updateLastAssistant(finalResult);
            finalize(taskId, finalResult);
          } else if (evt.type === "TASK_FAILED" || evt.type === "task.failed") {
            const errMsg = (evt.data as { error?: string })?.error || "任务失败";
            setErrorMsg(errMsg);
            updateLastAssistant(`**任务失败：** ${errMsg}`);
            finalize(taskId, "");
          } else if (evt.type === "TASK_CANCELLED" || evt.type === "task.cancelled") {
            updateLastAssistant("**任务已取消**");
            finalize(taskId, "");
          }
        });
        stopStreamRef.current = stop;
      })
      .catch(() => {
        setRouting(false);
        setBackendOnline(false);
        setErrorMsg("后端服务暂不可用，已切换到示例数据模式");
        const mock = `这是一个模拟回复。\n\n您的问题是："${query}"\n\n后端不可用，这是示例回答。`;
        updateLastAssistant(mock);
        setRouterContract(loadSampleContract("fc_dag_pipeline") || null);
        setRawResponse({ error: "backend_unavailable", mock: true });
        setIsRunning(false);
      });
  }

  function finalize(taskId: string, _result: string) {
    stopStreamRef.current?.();
    stopStreamRef.current = null;
    setStreamingContent("");
    setIsRunning(false);
    listSessions().then(setSessions).catch(() => {});
  }

  function handleCancel() {
    if (activeTaskId) {
      cancelTask(activeTaskId).catch(() => {});
      stopStreamRef.current?.();
      stopStreamRef.current = null;
      updateLastAssistant("**任务已取消**");
      setIsRunning(false);
      setStreamingContent("");
    }
  }

  function handleNewSession() {
    setMessages([]); setActiveSessionId(null); setRouterContract(null);
    setErrorMsg(null); setRawResponse(null); setActiveTaskId(null);
    setShowRouter(false); setShowDag(false); setStreamingContent(""); setRoutingPreview(null);
  }

  function handleSelectSession(id: string) { setActiveSessionId(id); setMessages([]); }
  function handleDeleteSession(id: string) {
    setSessions(s => s.filter(s => s.session_id !== id));
    if (activeSessionId === id) handleNewSession();
  }
  function handleRenameSession(id: string, newTitle: string) {
    setSessions(s => s.map(s => s.session_id === id ? { ...s, title: newTitle, updated_at: new Date().toISOString() } : s));
  }

  const rightPanel = (() => {
    if (showRouter && routerContract) return <RouterPanel contract={routerContract} onClose={() => setShowRouter(false)} />;
    if (showDag && activeTaskId) return <DagPanel taskId={activeTaskId} />;
    return null;
  })();

  return (
    <div className="h-screen flex flex-col bg-[#050812] text-textMain">
      <header className="shrink-0 border-b border-white/10 bg-[#030617]/80 px-4 py-2.5 flex items-center justify-between">
        <div className="flex items-center gap-3">
          <Sparkles className="h-5 w-5" />
          <span className="text-sm font-semibold">Cribug Agent</span>
          <span className="hidden sm:inline text-xs text-textMuted">· 智能体控制台</span>
          {activeTaskId && <span className="text-xs text-textMuted font-mono">{activeTaskId.slice(0,8)}...</span>}
          {routingPreview && (
            <span className="text-[10px] text-energy bg-energy/10 border border-energy/30 rounded px-1.5 py-0.5">
              路由 → {routingPreview.planned_mode}
            </span>
          )}
        </div>
        <div className="flex items-center gap-2">
          {activeTaskId && (
            <button onClick={() => setShowDag(!showDag)}
              className={`flex items-center gap-1 rounded px-2.5 py-1 text-xs transition ${showDag ? "bg-energy/10 text-energy border border-energy/30" : "bg-white/[0.03] text-textMuted border border-white/10"}`}>
              <GitBranch className="h-3.5 w-3.5" /> {showDag ? "隐藏 DAG" : "DAG 过程"}
            </button>
          )}
          {routerContract && (
            <button onClick={() => { setShowRouter(!showRouter); setShowDag(false); }}
              className={`flex items-center gap-1 rounded px-2.5 py-1 text-xs transition ${showRouter ? "bg-jade/10 text-jade border border-jade/30" : "bg-white/[0.03] text-textMuted border border-white/10"}`}>
              <Route className="h-3.5 w-3.5" /> {showRouter ? "隐藏路由" : "路由详情"}
            </button>
          )}
          <span className={`h-2 w-2 rounded-full ${backendOnline ? "bg-jade" : "bg-bronze"}`} />
          <span className="text-xs text-textMuted">{backendOnline ? "API" : "Mock"}</span>
        </div>
      </header>

      <div className="flex-1 flex overflow-hidden">
        <div className="w-60 shrink-0 hidden md:block">
          <SessionSidebar
            sessions={sessions}
            activeSessionId={activeSessionId}
            onSelectSession={handleSelectSession}
            onNewSession={handleNewSession}
            onDeleteSession={handleDeleteSession}
            onRenameSession={handleRenameSession}
          />
        </div>
        <div className="flex-1 flex flex-col min-w-0">
          <ChatView
            messages={messages}
            isRunning={isRunning || routing}
            errorMsg={errorMsg}
            backendOnline={backendOnline}
            streamingContent={streamingContent}
            onSubmit={handleSubmit}
            onCancel={handleCancel}
          />
          {rawResponse && <JsonDebugPanel data={rawResponse} label="路由 + 任务响应" />}
        </div>
        {rightPanel && (
          <div className="w-80 shrink-0 border-l border-white/10 bg-[#030617]/60 overflow-hidden">
            {rightPanel}
          </div>
        )}
      </div>
    </div>
  );
}
