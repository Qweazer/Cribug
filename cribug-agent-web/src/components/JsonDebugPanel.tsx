import { useState } from "react";
import { ChevronDown, ChevronRight, Copy, CheckCircle2 } from "lucide-react";

interface JsonDebugPanelProps {
  data: unknown;
  label?: string;
}

export function JsonDebugPanel({ data, label = "原始响应" }: JsonDebugPanelProps) {
  const [open, setOpen] = useState(false);
  const [copied, setCopied] = useState(false);

  const jsonStr = JSON.stringify(data, null, 2);

  function copyToClipboard() {
    navigator.clipboard.writeText(jsonStr).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    }).catch(() => {});
  }

  return (
    <div className="border-t border-white/10 bg-[#030617]/60">
      <button onClick={() => setOpen(!open)}
        className="flex items-center justify-between w-full px-4 py-2 text-xs text-textMuted hover:text-textMain transition">
        <span className="flex items-center gap-1.5">
          {open ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronRight className="h-3.5 w-3.5" />}
          {label}
        </span>
        <button onClick={(e) => { e.stopPropagation(); copyToClipboard(); }}
          className="flex items-center gap-1 text-xs text-textMuted hover:text-textMain">
          {copied ? <CheckCircle2 className="h-3.5 w-3.5 text-jade" /> : <Copy className="h-3.5 w-3.5" />}
          {copied ? "已复制" : "复制"}
        </button>
      </button>
      {open && (
        <div className="px-4 pb-3">
          <pre className="max-h-64 overflow-auto rounded-md border border-white/10 bg-black/30 p-3 text-xs font-mono text-textMuted whitespace-pre-wrap break-all">
            {jsonStr}
          </pre>
        </div>
      )}
    </div>
  );
}
