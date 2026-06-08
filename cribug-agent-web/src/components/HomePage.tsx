import {
  ArrowRight,
  CheckCircle2,
  Compass,
  Database,
  FlaskConical,
  Globe2,
  Network,
  RotateCcw,
  ShieldCheck,
  Sparkles,
  UserCheck,
  Wrench,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";

const capabilities: Array<{
  icon: LucideIcon;
  title: string;
  desc: string;
  status: string;
  signal: number;
}> = [
  {
    icon: Compass,
    title: "智能路由核心",
    desc: "判断任务复杂度，选择简单回答、多阶段任务、研究模式或反思模式。",
    status: "运行中",
    signal: 86,
  },
  {
    icon: Database,
    title: "知识检索透镜",
    desc: "从本地知识库召回相关信息，帮助智能体建立上下文理解。",
    status: "已接入",
    signal: 74,
  },
  {
    icon: FlaskConical,
    title: "沙箱执行工坊",
    desc: "在隔离环境中执行代码、验证结果、生成可审计输出。",
    status: "可调用",
    signal: 68,
  },
  {
    icon: Globe2,
    title: "网页探索罗盘",
    desc: "连接外部世界，获取最新信息与证据来源。",
    status: "待命",
    signal: 52,
  },
  {
    icon: RotateCcw,
    title: "反思验证镜",
    desc: "对初始结果进行复核、修正和质量提升。",
    status: "已接入",
    signal: 79,
  },
  {
    icon: Network,
    title: "工作流引擎",
    desc: "编排多阶段任务流，追踪执行状态与结果。",
    status: "可调用",
    signal: 63,
  },
];

const orbitNodes: Array<{ label: string; x: number; y: number; tone: string; icon: LucideIcon }> = [
  { label: "记忆系统", x: 50, y: 10, tone: "bronze", icon: Database },
  { label: "智能路由", x: 80, y: 23, tone: "bronze", icon: Compass },
  { label: "知识检索", x: 88, y: 50, tone: "energy", icon: Database },
  { label: "沙箱执行", x: 78, y: 74, tone: "energy", icon: FlaskConical },
  { label: "网页探索", x: 50, y: 88, tone: "energy", icon: Globe2 },
  { label: "工具技能", x: 24, y: 74, tone: "bronze", icon: Wrench },
  { label: "工作流", x: 14, y: 50, tone: "energy", icon: Network },
  { label: "人工审批", x: 24, y: 25, tone: "bronze", icon: UserCheck },
];

interface HomePageProps {
  onEnterWorkbench: () => void;
}

export function HomePage({ onEnterWorkbench }: HomePageProps) {
  return (
    <main className="home-shell">
      <section className="hero-expedition">
        <div className="hero-map-layer" />
        <div className="hero-copy">
          <div className="signal-label">
            <Sparkles className="h-4 w-4" />
            探索复杂任务空间的特殊智能体
          </div>
          <h1 className="hero-title mt-6">
            Cribug Agent
          </h1>
          <p className="mt-5 max-w-2xl text-xl leading-9 text-textMain/90">
            一个为复杂任务而生的探索型智能体系统
          </p>
          <p className="mt-4 max-w-2xl text-base leading-8 text-textMuted">
            它感知任务脉络，选择执行路径，在知识库、沙箱、网页搜索、工作流与多模型之间穿行，把混沌问题拆解成可执行的远征。
          </p>
          <div className="mt-8 flex flex-wrap gap-3">
            <button className="primary-button" onClick={onEnterWorkbench}>
              进入工作台
              <ArrowRight className="h-4 w-4" />
            </button>
            <button className="secondary-button" onClick={() => document.getElementById("capabilities")?.scrollIntoView({ behavior: "smooth" })}>
              查看能力图谱
            </button>
          </div>
        </div>

        <div className="expedition-visual" aria-label="Cribug 远征核心图谱">
          <div className="rift rift-a" />
          <div className="rift rift-b" />
          <svg className="energy-network" viewBox="0 0 100 100" preserveAspectRatio="none" aria-hidden="true">
            <defs>
              <linearGradient id="hero-line" x1="0" x2="1" y1="0" y2="0">
                <stop offset="0%" stopColor="#27D3FF" stopOpacity="0.05" />
                <stop offset="48%" stopColor="#42F2B3" stopOpacity="0.58" />
                <stop offset="100%" stopColor="#C89B4A" stopOpacity="0.28" />
              </linearGradient>
            </defs>
            {orbitNodes.map((node) => (
              <line key={node.label} x1="50" y1="50" x2={node.x} y2={node.y} stroke="url(#hero-line)" strokeWidth="0.45" />
            ))}
            <path d="M10 54 C24 18, 72 7, 93 44 C88 82, 33 92, 10 54Z" fill="none" stroke="rgba(200,155,74,0.34)" strokeWidth="0.32" />
            <path d="M14 31 C42 4, 86 28, 80 70 C44 91, 9 66, 14 31Z" fill="none" stroke="rgba(39,211,255,0.22)" strokeWidth="0.32" />
            <path d="M26 23 C55 10, 83 43, 66 74 C34 84, 12 46, 26 23Z" fill="none" stroke="rgba(200,155,74,0.18)" strokeWidth="0.25" />
          </svg>

          <div className="orbit-shell orbit-shell-a" />
          <div className="orbit-shell orbit-shell-b" />
          <div className="orbit-shell orbit-shell-c" />

          <div className="cribug-core">
            <div className="core-sigil">
              <span className="sigil-blade sigil-blade-a" />
              <span className="sigil-blade sigil-blade-b" />
              <span className="sigil-blade sigil-blade-c" />
            </div>
          </div>

          {orbitNodes.map((node, index) => (
            <div
              key={node.label}
              className={`expedition-node expedition-node-${node.tone}`}
              style={{ left: `${node.x}%`, top: `${node.y}%`, animationDelay: `${index * 0.18}s` }}
            >
              <span className="node-orb">
                <node.icon className="h-5 w-5" />
              </span>
              <strong>{node.label}</strong>
            </div>
          ))}

          <div className="coordinate-strip">
            <span>远征坐标</span>
            <strong>X-08 / DAG-62 / RAG-14</strong>
          </div>
        </div>
      </section>

      <section id="capabilities" className="capability-grid">
        {capabilities.map((item, index) => (
          <article key={item.title} className="ability-card" style={{ animationDelay: `${index * 0.08}s` }}>
            <div className="ability-card-scan" />
            <div className="flex items-start justify-between gap-4">
              <div className="ability-sigil">
                <item.icon className="h-5 w-5" />
              </div>
              <span className="ability-status">{item.status}</span>
            </div>
            <h2 className="mt-5 text-lg font-semibold text-textMain">{item.title}</h2>
            <p className="mt-3 min-h-[74px] text-sm leading-7 text-textMuted">{item.desc}</p>
            <div className="mt-5 flex items-center gap-3">
              <div className="energy-progress">
                <span style={{ width: `${item.signal}%` }} />
              </div>
              <span className="text-xs text-bronze">{item.signal}%</span>
            </div>
          </article>
        ))}
      </section>
    </main>
  );
}
