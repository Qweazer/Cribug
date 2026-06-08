import { useState, useRef, useEffect, FormEvent, KeyboardEvent } from "react";
import { Send, Loader2, AlertTriangle, Wrench, Search, FlaskConical, FileText, Copy, Check, StopCircle } from "lucide-react";
import ReactMarkdown from "react-markdown";
import { Prism as SyntaxHighlighter } from "react-syntax-highlighter";
import { vscDarkPlus } from "react-syntax-highlighter/dist/esm/styles/prism";
import type { TaskMessage, SubmitOptions } from "../types";

interface ChatViewProps {
  messages: TaskMessage[];
  isRunning: boolean;
  errorMsg: string | null;
  backendOnline: boolean;
  streamingContent?: string;
  onSubmit: (query: string, options: SubmitOptions) => void;
  onCancel?: () => void;
}

// ── Copy button (memoized) ─────────────────────────────────────
const CopyBtn = ({ text }: { text: string }) => {
  const [done, setDone] = useState(false);
  return (
    <button onClick={() => { navigator.clipboard?.writeText(text).then(() => { setDone(true); setTimeout(() => setDone(false), 2000); }).catch(() => {}); }}
      className="flex items-center gap-1 rounded px-1.5 py-0.5 text-[10px] text-textMuted hover:text-textMain hover:bg-white/[0.04] transition">
      {done ? <Check className="h-3 w-3 text-jade" /> : <Copy className="h-3 w-3" />}
      {done ? "已复制" : "复制"}
    </button>
  );
};

// ── Code block with syntax highlighting ────────────────────────
const CodeBlock = ({ language, value }: { language?: string; value: string }) => (
  <SyntaxHighlighter
    language={language || "text"}
    style={vscDarkPlus}
    customStyle={{ borderRadius: 6, fontSize: "12px", margin: "8px 0" }}
  >
    {value}
  </SyntaxHighlighter>
);

// ── Citation badge: detect [1][2] in text ───────────────────────
function highlightCitations(text: string) {
  return text.split(/(\[\d+\])/g).map((part, i) => {
    if (/^\[\d+\]$/.test(part)) {
      return (
        <sup key={i} className="inline-flex items-center justify-center min-w-[16px] h-4 px-1 mx-0.5 rounded-full bg-energy/15 text-energy text-[9px] font-mono border border-energy/30 cursor-help" title={`引用 ${part.slice(1, -1)}`}>
          {part.slice(1, -1)}
        </sup>
      );
    }
    return <span key={i}>{part}</span>;
  });
}

// ── Strip out <think> blocks for cleaner display ────────────────
function cleanText(text: string): string {
  return text.replace(/<think>[\s\S]*?<\/think>/g, "").trim();
}

export function ChatView({ messages, isRunning, errorMsg, backendOnline, streamingContent = "", onSubmit, onCancel }: ChatViewProps) {
  const [input, setInput] = useState("");
  const [allowTools, setAllowTools] = useState(true);
  const [allowResearch, setAllowResearch] = useState(false);
  const [allowSandbox, setAllowSandbox] = useState(false);
  const [requireCitations, setRequireCitations] = useState(false);
  const messagesEndRef = useRef<HTMLDivElement>(null);
  const isComposingRef = useRef(false); // IME 中文输入法

  useEffect(() => { messagesEndRef.current?.scrollIntoView({ behavior: "smooth" }); }, [messages, streamingContent]);

  function handleSubmit(e?: FormEvent) {
    e?.preventDefault();
    if (!input.trim() || isRunning) return;
    onSubmit(input.trim(), { allowTools, allowResearch, allowSandbox, requireCitations });
    setInput("");
  }

  function handleKeyDown(e: KeyboardEvent<HTMLTextAreaElement>) {
    if (e.nativeEvent.isComposing) { isComposingRef.current = true; return; }
    isComposingRef.current = false;
    if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); handleSubmit(); }
  }

  function handleCompositionStart() { isComposingRef.current = true; }
  function handleCompositionEnd() { isComposingRef.current = false; }

  const toggleBtn = (label: string, icon: React.ReactNode, value: boolean, setter: (v: boolean) => void) => (
    <button type="button" onClick={() => setter(!value)}
      className={`flex items-center gap-1 rounded px-2 py-0.5 text-[11px] transition ${value ? "bg-energy/10 text-energy border border-energy/30" : "bg-white/[0.03] text-textMuted border border-white/10"}`}>
      {icon} {label}
    </button>
  );

  return (
    <div className="flex flex-col h-full">
      <div className="flex-1 overflow-y-auto px-4 py-6">
        {!backendOnline && (
          <div className="mb-4 rounded-md border border-bronze/30 bg-bronze/10 p-3 flex items-center gap-2 text-sm text-bronze">
            <AlertTriangle className="h-4 w-4 shrink-0" /> 后端服务暂不可用，当前显示示例数据
          </div>
        )}
        {errorMsg && (
          <div className="mb-4 rounded-md border border-red-400/30 bg-red-400/10 p-3 flex items-center gap-2 text-sm text-red-400">
            <AlertTriangle className="h-4 w-4 shrink-0" /> {errorMsg}
          </div>
        )}
        {messages.length === 0 && !streamingContent ? (
          <div className="flex items-center justify-center h-full">
            <div className="text-center max-w-md">
              <div className="text-4xl mb-4">🧭</div>
              <h2 className="text-lg font-semibold text-textMain">Cribug Agent 控制台</h2>
              <p className="mt-2 text-sm text-textMuted leading-7">在下方输入任务，系统将自动判断复杂度并选择最优执行路径。</p>
              <div className="mt-6 grid grid-cols-2 gap-2 text-xs text-textMuted">
                {["法国首都是哪里？", "解释什么是向量数据库", "写一个项目验收清单", "2的10次方是多少？"].map((q) => (
                  <button key={q} onClick={() => setInput(q)} className="rounded-md border border-white/10 bg-white/[0.03] px-3 py-2 text-left hover:bg-white/[0.06] transition truncate">{q}</button>
                ))}
              </div>
            </div>
          </div>
        ) : (
          <div className="space-y-4 max-w-3xl mx-auto">
            {messages.map((msg) => (
              <div key={msg.id} className={`flex ${msg.role === "user" ? "justify-end" : "justify-start"}`}>
                <div className={`max-w-[85%] rounded-xl px-4 py-3 relative group ${msg.role === "user" ? "bg-energy/10 border border-energy/20 text-textMain" : msg.role === "system" ? "bg-white/[0.03] border border-white/10 text-textMuted text-sm" : "bg-white/[0.04] border border-white/10 text-textMain"}`}>
                  {msg.role === "user" ? (
                    <p className="text-sm whitespace-pre-wrap">{msg.content}</p>
                  ) : msg.role === "system" ? (
                    <p className="text-xs font-mono">{msg.content}</p>
                  ) : (
                    <>
                      <div className="prose prose-invert prose-sm max-w-none">
                        <ReactMarkdown components={{
                          code({ inline, className, children, ...props }: any) {
                            const match = /language-(\w+)/.exec(className || "");
                            const value = String(children).replace(/\n$/, "");
                            if (!inline && value.includes("\n")) {
                              return <CodeBlock language={match?.[1]} value={value} />;
                            }
                            return <code className={className} {...props}>{children}</code>;
                          },
                        }}>{cleanText(msg.content)}</ReactMarkdown>
                      </div>
                      <div className="opacity-0 group-hover:opacity-100 transition absolute -bottom-3 right-2">
                        <CopyBtn text={cleanText(msg.content)} />
                      </div>
                    </>
                  )}
                  {msg.role === "assistant" && /\\[\\d+\\]/.test(msg.content) && (
                    <div className="mt-2 text-[10px] text-textMuted">{highlightCitations(msg.content)}</div>
                  )}
                </div>
              </div>
            ))}

            {/* Streaming preview while running */}
            {(isRunning && streamingContent) && (
              <div className="flex justify-start">
                <div className="max-w-[85%] rounded-xl px-4 py-3 bg-white/[0.04] border border-white/10 text-textMain">
                  <div className="prose prose-invert prose-sm max-w-none">
                    <ReactMarkdown>{cleanText(streamingContent)}</ReactMarkdown>
                  </div>
                  <span className="inline-block w-1.5 h-4 bg-energy animate-pulse ml-0.5 align-text-bottom" />
                </div>
              </div>
            )}

            {/* Loading state (no streaming content yet) */}
            {isRunning && !streamingContent && (
              <div className="flex justify-start">
                <div className="rounded-xl px-4 py-3 bg-white/[0.04] border border-white/10 flex items-center gap-2 text-sm text-textMuted">
                  <Loader2 className="h-4 w-4 animate-spin text-energy" /> 正在生成回复...
                </div>
              </div>
            )}

            <div ref={messagesEndRef} />
          </div>
        )}
      </div>

      <div className="border-t border-white/10 px-4 py-3 bg-[#030617]/40">
        <form onSubmit={handleSubmit} className="max-w-3xl mx-auto">
          <div className="flex items-center gap-2 mb-2 flex-wrap">
            {toggleBtn("允许工具", <Wrench className="h-3 w-3" />, allowTools, setAllowTools)}
            {toggleBtn("允许研究", <Search className="h-3 w-3" />, allowResearch, setAllowResearch)}
            {toggleBtn("允许沙箱", <FlaskConical className="h-3 w-3" />, allowSandbox, setAllowSandbox)}
            {toggleBtn("要求引用", <FileText className="h-3 w-3" />, requireCitations, setRequireCitations)}
          </div>
          <div className="flex gap-2">
            <textarea
              value={input}
              onChange={(e) => setInput(e.target.value)}
              onKeyDown={handleKeyDown}
              onCompositionStart={handleCompositionStart}
              onCompositionEnd={handleCompositionEnd}
              placeholder="输入任务，Enter 发送，Shift+Enter 换行"
              rows={2}
              className="flex-1 resize-none rounded-lg border border-white/10 bg-white/[0.04] px-4 py-2.5 text-sm text-textMain placeholder:text-textMuted outline-none focus:border-energy/40 transition"
              disabled={isRunning}
            />
            {isRunning && onCancel ? (
              <button type="button" onClick={onCancel}
                className="shrink-0 rounded-lg bg-red-400/10 border border-red-400/30 px-4 py-2.5 text-red-400 hover:bg-red-400/20 transition">
                <StopCircle className="h-5 w-5" />
              </button>
            ) : (
              <button type="submit" disabled={!input.trim() || isRunning}
                className="shrink-0 rounded-lg bg-energy/10 border border-energy/30 px-4 py-2.5 text-energy hover:bg-energy/20 transition disabled:opacity-30 disabled:cursor-not-allowed">
                <Send className="h-5 w-5" />
              </button>
            )}
          </div>
        </form>
      </div>
    </div>
  );
}
