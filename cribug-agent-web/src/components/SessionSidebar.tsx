import { useState } from "react";
import { MessageSquare, Plus, Clock, CheckCircle2, AlertCircle, Loader2, Edit2, Trash2, Check, X } from "lucide-react";
import type { SessionSummary } from "../types";

interface SessionSidebarProps {
  sessions: SessionSummary[];
  activeSessionId: string | null;
  onSelectSession: (id: string) => void;
  onNewSession: () => void;
  onDeleteSession?: (id: string) => void;
  onRenameSession?: (id: string, newTitle: string) => void;
}

const statusIcon: Record<string, React.ReactNode> = {
  completed: <CheckCircle2 className="h-3.5 w-3.5 text-jade" />,
  running: <Loader2 className="h-3.5 w-3.5 text-energy animate-spin" />,
  failed: <AlertCircle className="h-3.5 w-3.5 text-red-400" />,
  pending: <Clock className="h-3.5 w-3.5 text-textMuted" />,
  waiting_for_approval: <Clock className="h-3.5 w-3.5 text-bronze" />,
};

const statusLabel: Record<string, string> = {
  completed: "已完成", running: "执行中", failed: "失败", pending: "待处理", waiting_for_approval: "等待审批",
};

export function SessionSidebar({ sessions, activeSessionId, onSelectSession, onNewSession, onDeleteSession, onRenameSession }: SessionSidebarProps) {
  const [editingId, setEditingId] = useState<string | null>(null);
  const [editTitle, setEditTitle] = useState("");
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);

  function startEdit(s: SessionSummary) {
    setEditingId(s.session_id);
    setEditTitle(s.title);
  }

  function commitEdit() {
    if (editingId && editTitle.trim() && onRenameSession) onRenameSession(editingId, editTitle.trim());
    setEditingId(null);
  }

  return (
    <aside className="flex h-full flex-col border-r border-white/10 bg-[#030617]/60">
      <div className="flex items-center justify-between px-4 py-4 border-b border-white/10">
        <h2 className="text-sm font-semibold text-textMain">会话历史</h2>
        <button onClick={onNewSession} className="flex items-center gap-1 rounded-md border border-white/10 bg-white/[0.04] px-2.5 py-1.5 text-xs text-textMuted hover:text-textMain transition">
          <Plus className="h-3.5 w-3.5" /> 新建
        </button>
      </div>
      <div className="flex-1 overflow-y-auto">
        {sessions.length === 0 ? (
          <div className="px-4 py-8 text-center text-xs text-textMuted">暂无会话记录</div>
        ) : (
          sessions.map((s) => {
            const isEditing = editingId === s.session_id;
            const isConfirming = confirmDelete === s.session_id;
            return (
              <div key={s.session_id}
                className={`group flex items-center gap-1 px-3 py-2.5 transition hover:bg-white/[0.04] ${activeSessionId === s.session_id ? "bg-white/[0.06] border-r-2 border-energy" : ""}`}>
                {isConfirming ? (
                  <div className="flex-1 flex items-center gap-1 text-xs">
                    <span className="text-red-400">确定删除?</span>
                    <button onClick={() => { onDeleteSession?.(s.session_id); setConfirmDelete(null); }} className="text-red-400 hover:text-red-300">
                      <Check className="h-3.5 w-3.5" />
                    </button>
                    <button onClick={() => setConfirmDelete(null)} className="text-textMuted hover:text-textMain">
                      <X className="h-3.5 w-3.5" />
                    </button>
                  </div>
                ) : isEditing ? (
                  <div className="flex-1 flex items-center gap-1">
                    <input value={editTitle} onChange={(e) => setEditTitle(e.target.value)}
                      onKeyDown={(e) => e.key === "Enter" && commitEdit()}
                      className="flex-1 bg-white/[0.06] border border-energy/30 rounded px-2 py-0.5 text-sm text-textMain outline-none"
                      autoFocus
                    />
                    <button onClick={commitEdit} className="text-jade hover:text-jade/80"><Check className="h-3.5 w-3.5" /></button>
                    <button onClick={() => setEditingId(null)} className="text-textMuted hover:text-textMain"><X className="h-3.5 w-3.5" /></button>
                  </div>
                ) : (
                  <>
                    <button onClick={() => onSelectSession(s.session_id)} className="flex-1 text-left min-w-0">
                      <p className="text-sm text-textMain truncate">{s.title || "未命名任务"}</p>
                      <div className="mt-1 flex items-center gap-2 text-xs text-textMuted">
                        {statusIcon[s.latest_status] || statusIcon.pending}
                        <span>{statusLabel[s.latest_status] || s.latest_status}</span>
                        <span>· {s.task_count} 任务</span>
                      </div>
                    </button>
                    <div className="opacity-0 group-hover:opacity-100 flex items-center gap-0.5">
                      {onRenameSession && (
                        <button onClick={(e) => { e.stopPropagation(); startEdit(s); }}
                          className="p-1 text-textMuted hover:text-textMain rounded">
                          <Edit2 className="h-3 w-3" />
                        </button>
                      )}
                      {onDeleteSession && (
                        <button onClick={(e) => { e.stopPropagation(); setConfirmDelete(s.session_id); }}
                          className="p-1 text-textMuted hover:text-red-400 rounded">
                          <Trash2 className="h-3 w-3" />
                        </button>
                      )}
                    </div>
                  </>
                )}
              </div>
            );
          })
        )}
      </div>
    </aside>
  );
}
