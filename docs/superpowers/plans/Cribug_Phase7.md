# Cribug Phase 7 任务书 — Advanced Router / HITL / Advanced Reasoning

## 版本说明

本文档为 Phase 7（v1.1），基于 Phase 6 已冻结的 MCP Tool Runtime、Sandbox/WASI、Skills System、Hooks Event System、RAG/Qdrant Long-term Memory 和 Research-Synthesis v1 能力，引入 **Advanced Strategy Router（统一入口）**、HITL/Approval/UI、Reflection Production Mode、Tree-of-Thoughts、Debate Mode 和 Research-Synthesis v2 能力。

**Phase 7 核心目标**：统一路由入口 + 人类介入审批 + 可视化控制 + 高级推理 Runtime

**Phase 7 与 Phase 6 承接关系**：
- **Phase 7A Advanced Router（Slice 23）**：实现 Router contract、task complexity classification、routing decision、audit、feature-flag aware dispatch。**Slice 23 只负责对 Phase 4 DAG、Phase 5 Swarm、Phase 6 RAG/MCP/Sandbox/Skills 等已冻结能力进行分派**。对 Reflection / ToT / Debate / Research v2 的 dispatch 入口预留在 Slice 23，但不接入真实 Workflow，等待 Slice 25-28 实现后再集成。Slice 23 的测试只覆盖 routing decision、fallback、audit、policy trace ref；完整 routed E2E 放到 Slice 28 之后统一验证。
- **Phase 7B HITL / Approval（Slice 24）** 复用 Phase 6D Hooks 作为高风险 tool / sandbox / MCP / handoff / publish 的触发点
- **Phase 7C Reflection（Slice 25）** 复用 Phase 6 LLM Service / Skills / Workspace
- **Phase 7D Tree-of-Thoughts（Slice 26）** 复用 Phase 4 DAG / token budget、Phase 6 Skills、Workspace
- **Phase 7E Debate（Slice 27）** 复用 Phase 5 Swarm / Workspace / Security，并可复用 Phase 6F RAG evidence
- **Phase 7F Research-Synthesis v2（Slice 28）** 必须建立在 Phase 6F v1 主路径上，而不是新写一个绕过 v1 的 all-in-one workflow

**关键约束**：
- 所有外部 IO 必须放在 Activity 中，Workflow 保持 deterministic
- Workspace 延续 Phase 5 Append-only 原则
- 不修改 Phase 4/5/6 已冻结主架构（DAG、ReAct、Swarm、P2P、Workspace、Handoff、MCP、Skills、Hooks、RAG）
- 不塞入 Phase 8 能力（SDK、CLI、Multi-tenant、Auth/Quota、OpenAI-compatible API、配置导入导出）
- 大文本（draft、debate transcript、thought tree、research report、routing policy trace）必须通过 WorkspaceRef 传递，不进入 Workflow history
- 新增 Go 代码一律写入 Cribug 真实目录（`internal/workflows/...`、`internal/activities/...`、`internal/api/handlers/...`），不写 Shannon 的 `go/orchestrator/...`
- Shannon 路径（`go/orchestrator/...`、`python/llm-service/...`）只用于"参考实现"章节，不得作为 Cribug 新增实现目录

---

## 一、项目定位

### 1.1 当前已完成基线

| Phase | 内容 | 状态 |
|-------|------|------|
| Phase 1 | Gateway + Temporal Worker + SimpleWorkflow + Postgres + Redis | 完成后 |
| Phase 2 | AgentActivity 全链路 + Session Memory + Budget + SSE | 完成后 |
| Phase 3D Slice 7 | DAG Concurrency | 完成后 |
| Phase 3D Slice 8 | ReAct Reasoning Loop | 完成后 |
| Phase 3D Slice 9 | Real tiktoken Tokenizer | 完成后 |
| Phase 4 Slice 10 | DAG 可视化 | 完成后 |
| Phase 4 Slice 11 | ReAct 暂停/恢复 | 完成后 |
| Phase 4 Slice 12 | 两级 LRU 缓存 | 完成后 |
| Phase 5A Slice 10 | Lead Agent / SwarmWorkflow | 完成后 |
| Phase 5B Slice 11 | Agent P2P Communication | 完成后 |
| Phase 5C Slice 12 | Workspace Append-only + LLM Synthesis | 完成后 |
| Phase 5D Slice 13 | State Synchronization | 完成后 |
| Phase 5E Slice 14 | Conflict Resolution（已删除，Workspace 纯 Append-only） | 删除 |
| Phase 5F Slice 15 | Security & Access Control | 完成后 |
| Phase 5G Slice 16 | Agent Handoff Mechanism | 完成后 |
| Phase 6A Slice 17 | MCP Tool Runtime | 完成后 |
| Phase 6B Slice 18 | Sandbox / WASI Execution | 完成后 |
| Phase 6C Slice 19 | Skills System | 完成后 |
| Phase 6D Slice 20 | Hooks Event System | 完成后 |
| Phase 6E Slice 21 | RAG / Qdrant Long-term Memory | 完成后 |
| Phase 6F Slice 22 | Research-Synthesis v1 | 完成后 |

### 1.2 Phase 7 目标

| Slice | 功能 | 描述 | 默认测试 |
|-------|------|------|----------|
| Phase 7A Slice 23 | **Advanced Strategy Router** | 统一入口：query 复杂度分类、能力检测、路由策略评估、成本估算、审计、分派到 Phase 4-7 已存在 workflow | mock classifier |
| Phase 7B Slice 24 | **HITL / Approval / UI Control** | 人类审批、任务暂停恢复、Approval Signal、Dashboard / UI 控制入口 | mock |
| Phase 7C Slice 25 | **Reflection Production Mode** | generate -> reflect -> revise，多轮 reflection，rubric，audit | mock |
| Phase 7D Slice 26 | **Tree-of-Thoughts** | 多候选路径、评分、剪枝、best path selection、bounded search | mock |
| Phase 7E Slice 27 | **Debate Mode** | Pro Agent、Con Agent、Judge Agent、多轮辩论 | mock |
| Phase 7F Slice 28 | **Research-Synthesis v2** | 多来源证据、冲突证据处理、引用链路、深度研究报告 | mock LLM |

---

## 二、职责边界（强制约束 - Shannon 范式）

### 2.1 组件职责约束

| 组件 | Phase 7 职责 | 禁止 |
|------|-------------|------|
| Gateway | 接收 approval/pause/resume 请求、routing 请求（POST /api/v1/tasks/route），创建 task，返回 task_id/workflow_id/run_id/SSE，接收 Dashboard / UI 操作请求 | 直接改 Workflow 内部状态、访问 DB/Redis/Qdrant、直接调 LLM |
| Workflow | 确定性编排逻辑、调用 Activities、维护 HITL pause/reply、维护 Reflection/ToT/Debate/Research v2 状态机、维护 Router 状态机 | 直接 HTTP/DB/Redis/外部 IO、使用 `time.Now()`、使用随机数、创建 goroutine |
| Activity | 所有外部 IO：Approval DB 写入、Signal 发送、LLM generate、reflection、judge、scoring、Workspace 写入、Router 分类器调用 | 做全局业务编排（只做外部 IO）、调用 workflow.ExecuteActivity、生成业务时间戳 |
| Python LLM Service | LLM generate（reflection/evaluation/judge/scoring/routing-classifier）、embedding（mock/real） | 任务编排、状态管理、Workflow 调度 |
| Dashboard / UI | 通过 Gateway API / Signal 控制 Workflow，只能查看状态，不能直接改数据库或 Workflow 内部状态 | 直接改 DB、直接发 Signal 给 Temporal、直接访问 Workspace |

### 2.2 Advanced Router 约束

- **Router 不重写任何已存在 workflow**：分派时只调用 Phase 4 DAG、Phase 5 Swarm、Phase 6 RAG/MCP/Sandbox/Skills、Phase 7 Reflection/ToT/Debate/Research v2 的已有 Workflow / Activity 入口；Router 不复制实现，不 fork 主路径
- **Router 不替代 HITL**：Router 负责"选路径"，HITL 负责"高风险路径执行前暂停确认"。Router 决策中 `RequiresApproval=true` 时，进入 Phase 7B Approval 流程
- **Router 可调用 Phase 6D Hooks 作为 policy checkpoint**：例如 `before_tool`、`before_sandbox`、`before_publish`、`before_handoff`、`before_research_v2`，但 Phase 7 不重写 Hooks 实现
- **Router 决策必须写 Workspace / Audit**：用于后续可观测性。`PolicyTraceRef` 走 WorkspaceRef，不进入 Workflow history
- **分类器默认 heuristic + optional LLM**：CI 必须 mock / deterministic，真实 LLM 分类只做可选 smoke
- **成本与复杂度分层显式**：每个 mode 都有 complexity / risk / evidence_need / tool_need / cost_tier 描述

### 2.3 HITL / Approval 约束

- **Approval 必须通过 Signal 恢复 Workflow**
- Workflow 等待 `human-approval-{approvalID}` Signal，超时返回结构化 PartialResult
- UI 不能直接改 Workflow 内部状态，只能通过 Gateway API 操作
- approval payload 大内容必须用 PayloadRef / WorkspaceRef
- timeout 返回 `PartialResult{Reason: "approval_timeout"}`
- 每个 ApprovalRequest 必须有唯一 `approval_id`
- 业务时间戳（`requested_at`、`expires_at`、`responded_at`）由 Workflow 用 `workflow.Now(ctx)` 生成后传入 Activity，**Activity 不生成业务时间戳**
- `PostgreSQL created_at DEFAULT NOW()` 是数据库行创建时间，可保留，但业务 `requested_at/expires_at/responded_at` 由 Workflow 传入并显式落库

### 2.4 Reflection 约束

- **Reflection 不等于 ReAct**
- Reflection 不保存模型隐藏 CoT，只保存 audit summary
- draft / critique / revision 大文本必须通过 WorkspaceRef 传递
- 多轮 reflection 必须有 `max_rounds` 限制
- `confidence_threshold` 决定是否需要 reflection
- Real LLM reflection 只做可选 smoke，不进默认 CI

### 2.5 Tree-of-Thoughts 约束

- **ToT 是 bounded search，不能无限展开**
- `max_depth`、`max_branching_factor`、`max_total_nodes`、`tot_token_budget` 必须配置
- Thought 大文本用 ref，不进入 Workflow history
- `pruning_threshold` 以下的分支必须剪枝
- best path selection 后，best thought 写入 Workspace
- 默认 mock LLM 测试，Real LLM ToT 只做手动 smoke 并设置成本上限
- `ThoughtNode` 统一使用 `ThoughtRef` + `Summary` 字段，`Workflow history` 不存完整 thought 文本
- 不要定义 `ToTWorkflowActivity` 这种"Activity 包整个 workflow"的反模式；Activity 只做 IO，Workflow 编排用 `TreeOfThoughtsWorkflow` 本身

### 2.6 Debate 约束

- **Debate 复用 Phase 5 Swarm / Workspace / Security**
- Debate transcript 必须写 Workspace（append-only）
- Judge 必须读取 WorkspaceList / TranscriptRef，不直接依赖内存数组
- Debate 不能无限轮次，必须有 `max_rounds` / `debate_round_timeout_seconds`
- Pro / Con / Judge 大文本必须通过 WorkspaceRef 传递
- Real LLM debate 只做可选 smoke，不进默认 CI
- `DebateConfig` 中如需 `MockLLM` 字段，必须在结构体中显式定义，**不要隐式假设存在**
- 不要定义 `DebateWorkflowActivity` 这种反模式；编排放 `DebateWorkflow`，Activity 只做 IO

### 2.7 Research-Synthesis v2 约束

- **v2 不是简单 RAG answer**
- v2 必须建立在 Phase 6F v1 主路径上（`ResearchSynthesisV1Workflow` 已有能力），v2 是 v1 的增强而不是 all-in-one 重写
- v2 必须有 evidence provenance（来源追踪）
- v2 必须支持 contradiction metadata（冲突证据标记）
- v2 输出 report ref，不把大报告塞进 Workflow history
- 完整 evidence content 通过 `content_ref` + `summary` 存到 Workspace，Postgres 只存 `content_ref` / metadata
- 如果启用 HITL，发布前必须进入 Approval（`research_v2_require_approval_before_publish`）
- multi-source evidence 必须有 credibility scoring
- citation chain 必须可追溯

### 2.8 Phase 6 承接约束（强制）

- **Phase 7 不允许重新实现 Phase 6 的 MCP / Sandbox / Skills / Hooks / RAG / Research-Synthesis v1**
- 只能通过已有 Activity / Workflow / Workspace / Skill 接口消费这些能力
- Phase 7A Router 可以调用 Phase 6D Hooks 作为 policy checkpoint，但不能重写 Hooks 实现
- Phase 7B Approval 可以触发 Phase 6D Hooks 作为审批前置条件，但不能重写 Hooks 实现
- Phase 7C Reflection 可以调用 Phase 6 Skills，但不能重写 Skills System
- Phase 7F 只能建立在 Phase 6F v1 主路径上，不能绕过 v1 重新实现 all-in-one workflow

---

## 三、阶段范围

### Phase 7A Slice 23：Advanced Strategy Router（Mode Router / Policy Orchestrator）

#### 3.1 核心架构澄清

Advanced Router 是 Phase 7 的统一入口。它根据用户 query、上下文、可用能力、成本预算、风险等级、citation 需求等，自动选择最省钱、最高效、最安全的执行路径，然后分派到 Phase 4-7 已存在的 Workflow / Activity 入口，**而不是让上层 API 或 UI 手动选择 workflow**。

它需要做到：

1. 一个用户 query 进来后，**Router 自动判断走哪条路径**：
   - 简单问答 → `direct_answer`（小模型/直接走 `SimpleWorkflow`）
   - 需要本地长期记忆 → `rag_answer`（复用 Phase 6E RAG / Qdrant）
   - 需要调工具 → `react_tool`（复用 Phase 3/4 ReAct + Phase 6 MCP / Skills）
   - 需要跑代码 → `sandbox_execution`（复用 Phase 6B WASI Sandbox；高风险 → 触发 Approval）
   - 多步可分解 → `dag_workflow`（复用 Phase 4 DAG）
   - 多 Agent 分工 → `swarm_workflow`（复用 Phase 5 Swarm / P2P / Workspace / Handoff）
   - 高质量输出 → `reflection`（复用 Slice 25）
   - 多路径搜索 → `tree_of_thoughts`（复用 Slice 26）
   - 观点对抗 → `debate`（复用 Slice 27）
   - 普通研究 → `research_v1`（复用 Phase 6F）
   - 深度研究 → `research_v2`（复用 Slice 28）
   - 高风险 / 高预算 / 发布前 → `requires_approval=true`，进入 Phase 7B Approval 流程

2. 决策必须可审计、可回放、可解释
3. 决策必须显式考虑成本与复杂度分层
4. 默认 heuristic + mock LLM 分类器，真实 LLM 分类器只在 `REAL_ROUTER_TEST=1` 时启用
5. **不要把 Router 当成 Selector 模板**——它是 Phase 7 的中心能力，必须有 Workflow、Activity、Audit 表

**参考 Shannon 实现**（只读参考，不修改）：

| 概念 | Shannon 参考路径 |
|------|----------------|
| 策略选择（research / scientific / exploratory） | `go/orchestrator/internal/workflows/strategies/research.go`、`scientific.go`、`exploratory.go` |
| 策略公共工具（shouldReflect 等） | `go/orchestrator/internal/strategies/strategy_helpers.go` |
| Reflection 模式 | `go/orchestrator/internal/workflows/patterns/reflection.go` |
| ToT 模式 | `go/orchestrator/internal/workflows/patterns/tree_of_thoughts.go` |
| Debate 模式 | `go/orchestrator/internal/workflows/patterns/debate.go` |
| Pattern Registry | `go/orchestrator/internal/workflows/patterns/registry.go` |
| Approval 中间件 | `go/orchestrator/internal/workflows/middleware_approval.go` |
| Control Handler | `go/orchestrator/internal/workflows/control/handler.go` |
| Control Signals | `go/orchestrator/internal/workflows/control/signals.go` |

> Shannon 没有一个集中的"Router"对象，但通过 `strategies/` + `patterns/registry.go` + `middleware_approval.go` + `control/handler.go` 的组合形成"策略路由"语义。Cribug Phase 7A 在 Cribug 真实目录中显式实现一个集中式 Router，而不是把这些散落在 Workflow 中。

#### 3.2 路由分派规则表

下表给出**不同任务场景的推荐 mode**。Router 默认根据下表做 heuristic 决策；可叠加 LLM 分类器。

| 场景 | complexity | risk | evidence_need | tool_need | sandbox_need | 推荐 mode | 备注 |
|------|-----------|------|---------------|-----------|--------------|-----------|------|
| 简单问答 | low | low | none | none | none | `direct_answer` | 用小模型 / `SimpleWorkflow` |
| 项目文档问答 | low-medium | low | local | none | none | `rag_answer` | 复用 Phase 6E RAG / Qdrant |
| 需要工具调用 | medium | medium | maybe | tool | none | `react_tool` | 复用 Phase 3/4 ReAct + Phase 6 MCP / Skills |
| 需要代码运行 | medium-high | high | none | maybe | sandbox | `sandbox_execution` + `requires_approval=true` | 复用 Phase 6B WASI |
| 多步骤任务 | medium-high | medium | maybe | maybe | none | `dag_workflow` | 复用 Phase 4 DAG |
| 多角色协作 | high | medium | maybe | maybe | none | `swarm_workflow` | 复用 Phase 5 Swarm / P2P / Handoff |
| 方案比较 | high | medium | maybe | none | none | `debate` | 复用 Slice 27 |
| 多路径搜索 | high | medium | none | none | none | `tree_of_thoughts` | 复用 Slice 26，必须 bounded |
| 高质量写作 / 报告 | medium-high | low-medium | maybe | none | none | `reflection` | 复用 Slice 25 |
| 普通研究综合 | medium-high | low-medium | multi-source | maybe | none | `research_v1` | 复用 Phase 6F |
| 深度研究 | high | medium-high | multi-source + contradiction | maybe | none | `research_v2` | 复用 Slice 28，建立在 v1 上 |
| 高风险 / 高预算 / 发布前 | - | high / critical | - | - | - | `requires_approval=true`（叠加在以上任意 mode 上） | 进入 Phase 7B Approval |

复杂度 / 风险 / 工具需求如何从 query 抽取 → 见 3.5 `ClassifyTaskComplexityActivity` / `DetectTaskCapabilitiesActivity`。

#### 3.3 数据结构

```go
// RoutingMode - 路由结果模式
type RoutingMode string

const (
    RouteDirectAnswer     RoutingMode = "direct_answer"
    RouteRAGAnswer        RoutingMode = "rag_answer"
    RouteReActTool        RoutingMode = "react_tool"
    RouteSandboxExecution RoutingMode = "sandbox_execution"
    RouteDAGWorkflow      RoutingMode = "dag_workflow"
    RouteSwarmWorkflow    RoutingMode = "swarm_workflow"
    RouteReflection       RoutingMode = "reflection"
    RouteTreeOfThoughts   RoutingMode = "tree_of_thoughts"
    RouteDebate           RoutingMode = "debate"
    RouteResearchV1       RoutingMode = "research_v1"
    RouteResearchV2       RoutingMode = "research_v2"
    RouteModeDisabled     RoutingMode = "mode_disabled"     // feature flag 关闭或 Workflow 未注册时使用
    RouteNotImplemented   RoutingMode = "not_implemented"   // Slice 25-28 尚未实现的 mode，预留 dispatch 入口
)

// RouterConfigSnapshot - Router 配置快照（Workflow input 携带，不在 Workflow 内动态读取）
// 由 Gateway / Caller 在启动 Workflow 时从 config/router.yaml + env 中读取并传入。
// Workflow 内部不得使用 routerConfigFromContext(ctx) 或类似动态读取。
type RouterConfigSnapshot struct {
    ClassifierMode          string  `json:"classifier_mode"`           // "heuristic" | "mock_llm" | "real_llm"
    ComplexityDAGThreshold  float64 `json:"complexity_dag_threshold"`
    ComplexitySwarmThreshold float64 `json:"complexity_swarm_threshold"`
    ComplexityToTThreshold  float64 `json:"complexity_tot_threshold"`
    ResearchV2Threshold     float64 `json:"research_v2_threshold"`
    ApprovalRiskThreshold   string  `json:"approval_risk_threshold"`
    MaxClassificationTokens int     `json:"max_classification_tokens"`
    DecisionAuditEnabled    bool    `json:"decision_audit_enabled"`
    DefaultMode             string  `json:"default_mode"`
}

// RouteRequest - 路由请求
type RouteRequest struct {
    SessionID        string                 `json:"session_id"`
    Query            string                 `json:"query"`
    UserIntent       string                 `json:"user_intent,omitempty"`
    ContextSummary   map[string]interface{} `json:"context_summary,omitempty"`
    AvailableTools   []string               `json:"available_tools,omitempty"`
    BudgetUSD        float64                `json:"budget_usd"`
    MaxLatencyMs     int                    `json:"max_latency_ms"`
    RequireCitations bool                   `json:"require_citations"`
    AllowTools       bool                   `json:"allow_tools"`
    AllowSandbox     bool                   `json:"allow_sandbox"`
    AllowResearch    bool                   `json:"allow_research"`
    RiskLevelHint    string                 `json:"risk_level_hint,omitempty"` // "low" | "medium" | "high" | "critical" | ""
    RouterConfig     RouterConfigSnapshot   `json:"router_config"`             // 配置快照，由 Gateway 传入；Workflow 不动态读取
}

// RouteAddon - 附加能力（RouteAddon 可以是主模式的补充，如 sandbox + reflection + debate）
type RouteAddon string

const (
    AddonRAG        RouteAddon = "rag"         // 附加本地知识检索
    AddonSandbox    RouteAddon = "sandbox"      // 附加代码/沙箱执行
    AddonReflection RouteAddon = "reflection"   // 附加质量自检
    AddonDebate     RouteAddon = "debate"       // 附加观点/方案辩论
    AddonToT        RouteAddon = "tree_of_thoughts" // 附加多路径搜索
    AddonResearchV2 RouteAddon = "research_v2"  // 附加深度研究增强
    AddonApproval   RouteAddon = "approval"     // 附加人工审批
)

// RoutingDecision - 路由决策（Workflow history 只存 metadata + ref，不存长解释）
type RoutingDecision struct {
    PlannedMode        RoutingMode            `json:"planned_mode"`       // Router 最初判断的理想 mode（不受 feature flag 约束）
    Mode               RoutingMode            `json:"mode"`               // 实际执行模式（executed_mode，可能因 feature flag / fallback 而不同于 planned_mode）
    FallbackReason     string                 `json:"fallback_reason,omitempty"` // 如果 mode != planned_mode，记录 fallback 原因
    AddonCapabilities  []RouteAddon           `json:"addon_capabilities"` // 附加能力（可多个）
    WorkflowType       string                 `json:"workflow_type"`      // 例如 "DirectAnswerWorkflow" / "ReActWorkflow" / "DAGWorkflow"
    Reason             string                 `json:"reason"`             // 短解释（一句话）
    ComplexityScore    float64                `json:"complexity_score"`
    RiskLevel          string                 `json:"risk_level"`
    RequiresApproval   bool                   `json:"requires_approval"`
    RequiresRAG        bool                   `json:"requires_rag"`
    RequiresTools      bool                   `json:"requires_tools"`
    RequiresSandbox    bool                   `json:"requires_sandbox"`
    RequiresWorkspace  bool                   `json:"requires_workspace"`
    RequiresReflection bool                   `json:"requires_reflection"`
    RequiresDebate     bool                   `json:"requires_debate"`
    RequiresToT        bool                   `json:"requires_tot"`
    RequiresResearchV2 bool                   `json:"requires_research_v2"`
    ModelTier          string                 `json:"model_tier"`          // "small" | "medium" | "large"
    TokenBudget        int                    `json:"token_budget"`
    CostBudgetUSD      float64                `json:"cost_budget_usd"`
    Confidence         float64                `json:"confidence"`
    PolicyTraceRef     string                 `json:"policy_trace_ref,omitempty"` // 长解释 / 分类器原文走 Workspace
    Metadata           map[string]interface{} `json:"metadata,omitempty"`
}

// RoutedExecutionResult - 路由后执行结果（统一返回结构）
type RoutedExecutionResult struct {
    SessionID         string                 `json:"session_id"`
    WorkflowID        string                 `json:"workflow_id"`
    RunID             string                 `json:"run_id"`
    Decision          RoutingDecision        `json:"decision"`
    FinalAnswerRef    string                 `json:"final_answer_ref"`        // 完整答案 / 报告 / transcript 走 Workspace
    FinalAnswerText   string                 `json:"final_answer_text"`       // 短答案 / preview / 摘要（≤2KB；超限走 ref）
    FeedbackSummary   string                 `json:"feedback_summary,omitempty"` // 短反馈摘要（≤500 字符；Rejected / Timeout / Error 时用）
    FeedbackRef       string                 `json:"feedback_ref,omitempty"`     // 完整反馈 → Workspace
    Reason            string                 `json:"reason,omitempty"`            // 短原因（approval_timeout / mode_disabled / not_implemented / safe_fallback）
    TokensUsed        int                    `json:"tokens_used"`
    CostUSD           float64                `json:"cost_usd"`
    Status            string                 `json:"status"`                  // "ok" | "approval_required" | "rejected" | "timeout" | "partial" | "mode_disabled" | "error"
    PendingApprovalID string                 `json:"pending_approval_id,omitempty"`
    Metadata         map[string]interface{} `json:"metadata,omitempty"`
}

// RouterConfig - Router 配置
type RouterConfig struct {
    ClassifierMode             string  `json:"classifier_mode"`               // "heuristic" | "mock_llm" | "real_llm"
    ComplexityDAGThreshold     float64 `json:"complexity_dag_threshold"`      // 默认 0.45
    ComplexitySwarmThreshold   float64 `json:"complexity_swarm_threshold"`    // 默认 0.65
    ComplexityToTThreshold     float64 `json:"complexity_tot_threshold"`      // 默认 0.75
    ResearchV2Threshold        float64 `json:"research_v2_threshold"`         // 默认 0.70
    ApprovalRiskThreshold      string  `json:"approval_risk_threshold"`       // "high" | "critical"
    MaxClassificationTokens     int     `json:"max_classification_tokens"`     // 默认 800
    DecisionAuditEnabled       bool    `json:"decision_audit_enabled"`        // 默认 true
    DefaultMode                string  `json:"default_mode"`                  // 默认 "direct_answer"
    Enabled                    bool    `json:"enabled"`                       // 主开关 ENABLE_ADVANCED_ROUTER
}
```

#### 3.2.1 路由冲突优先级规则

一个任务可能同时满足多个模式条件，Router 不只用简单 switch。Router 采用 **主模式（Mode）+ 附加能力（AddonCapabilities）** 的设计：

- **Mode** 是主执行路径，决定 primary Workflow。
- **AddonCapabilities** 是执行路径中额外启用的能力。Reflection、Debate、ToT、Sandbox、RAG 都可以作为 addon 附加到主模式上，但必须有明确的预算边界。

优先级规则（从高到低）：

1. **用户显式要求优先**：如果 `user_intent` / `RouteRequest` 明确指定某 mode，优先尊重用户要求，但仍需经过风险和预算检查。
2. **高风险 / 高预算 → 叠加 Approval**：任何 mode 如果风险 ≥ high 或 cost ≥ threshold，`requires_approval=true` 叠加在 mode 上。Approval 不是主执行模式，是 gate。
3. **需要代码执行 → sandbox_execution**：可作为主模式（sandbox 是唯一路径），也可作为 research_v2 / dag / swarm 的 addon。
4. **多来源 + 引用 + 矛盾检测 → research_v2 主模式**：深度研究优先择 research_v2 为主模式。可以不附加其他 mode。
5. **多 Agent 分工 → swarm_workflow 主模式**。
6. **可分解但不需要多 Agent → dag_workflow 主模式**。
7. **需要工具交互但不复杂 → react_tool 主模式**。
8. **需要本地知识但不需工具 → rag_answer 主模式**（也可作为 addon 附加到 debate / research_v2 等）。
9. **简单低风险 → direct_answer 主模式**。
10. **Reflection / Debate / ToT 可以是主模式，也可以是 addon**：如果主任务本身是"做方案比较"→ debate 作为主模式；如果主任务是 research_v2 但"需要高质量报告"→ reflection 作为 addon。

#### 3.2.2 组合示例

**例 1：深度研究 + 代码验证 + 质量自检**
```
query: "调研某技术方案，并运行代码验证数据，最后输出报告"
主模式: research_v2
附加能力: [sandbox, reflection]
requires_approval: true (sandbox 高风险)
```

**例 2：方案对比 + 项目文档参考**
```
query: "比较 PostgreSQL 和 MongoDB 哪个适合我的项目"
主模式: debate
附加能力: [rag]  (如需参考项目文档)
requires_approval: false
```

**例 3：本地知识问答**
```
query: "根据我的项目文档回答某个模块在哪里"
主模式: rag_answer
附加能力: []
requires_approval: false
```

**例 4：未知代码执行 + 分析**
```
query: "帮我执行一段未知代码并分析结果"
主模式: sandbox_execution
附加能力: [approval]
requires_approval: true
```

**例 5：复杂研究报告 + 多 Agent 协作 + 代码验证**
```
query: "多 Agent 协作完成金融研究报告，包含数据回测"
主模式: swarm_workflow
附加能力: [sandbox, research_v2]
requires_approval: true (外部发布)
```

#### 3.2.2b planned_mode vs executed_mode（Feature-flag aware dispatch）

Router 必须区分**理想路由**（planned_mode，不受 feature flag 约束）和**实际执行模式**（executed_mode / `Mode` 字段）：

| 场景 | planned_mode | executed_mode | 行为 |
|------|-------------|---------------|------|
| Feature flag 全部开启 | as classified | as classified | 正常 dispatch |
| `enable_reflection=false` | `reflection` | `mode_disabled` | fallback 到 `direct_answer` 或返回 `RoutedExecutionResult{Status: "mode_disabled"}` |
| `enable_tot=false` | `tree_of_thoughts` | `mode_disabled` | 同上 |
| `enable_debate=false` | `debate` | `mode_disabled` | 同上 |
| `enable_research_v2=false` | `research_v2` | `mode_disabled` → `research_v1`（如果有 v1） | Priority fallback 到 Phase 6F research_v1 |
| Slice 25-28 未实现 | `reflection` / `tot` / `debate` / `research_v2` | `not_implemented` | **不调用未注册 Workflow**；返回 `RoutedExecutionResult{Status: "mode_disabled", Reason: "not_implemented"}` 或 safe fallback |
| 所有 feature flag 关闭 | any | `direct_answer` | 兜底 |

**核心原则**：

- Router **可以计划** `planned_mode`（写入 audit），但**只执行** `executed_mode`（写入 `Mode` 字段）。
- 对 Slice 25-28 未实现的 mode：dispatch 入口预留在 `dispatchByMode()` 中，但在 Workflow registry 中不注册对应 Workflow。Router 检测到 mode 对应 Workflow 未注册时，须返回 `not_implemented` 或 fallback 到可用 mode，**不允许**直接调用不存在的 Workflow。
- 对 feature flag 关闭的 mode：Router 通过 `RouterConfigSnapshot` 检查 feature flags，关闭时 fallback。
- `planned_mode` 与 `executed_mode` 不一致时：写入 `FallbackReason` + 审计 log。

**重要约束**：

- `PolicyTraceRef` 必须写 Workspace（`router:{session_id}:policy_trace` topic），Workflow history 不存长解释
- `RoutingDecision` 只保存可审计摘要（mode / score / 短 reason / 阈值命中），不保存模型隐藏 CoT
- 默认分类器是 `heuristic`（按 3.2 规则表 + 关键词/长度判断），CI 跑 mock LLM 或 heuristic；真实 LLM 分类器只在 `REAL_ROUTER_TEST=1` 时启用，且 `ROUTER_MAX_CLASSIFICATION_TOKENS=800`
- `AddonCapabilities` 不能无限叠加：单个 task 最多 3 个 addon，超出部分应降级或忽略

#### 3.2.3 长文本限制（Workflow history 保护）

为避免 Workflow history 膨胀，Router 结果和中间产物受以下限制：

| 字段 | 约束 | 超限处理 |
|------|------|----------|
| `FinalAnswerText` | ≤ 2 KB（建议 500 字符以内） | 写入 Workspace → `FinalAnswerRef` |
| `RoutingDecision.Reason` | ≤ 500 字符 | 长解释写入 `PolicyTraceRef` |
| `ClassifyTaskComplexityResult.ReasoningRef` | 短摘要 `Summary` ≤ 500 字符 | 完整分类解释走 `ReasoningRef` |
| `DetectTaskCapabilitiesResult.ReasoningRef` | 短摘要 `Summary` ≤ 500 字符 | 完整检测解释走 `ReasoningRef` |
| `WriteRoutingPolicyTraceInput.ComplexitySummary` | ≤ 500 字符 | — Activity 只接收短摘要 |
| `WriteRoutingPolicyTraceInput.CapabilitySummary` | ≤ 500 字符 | — Activity 只接收短摘要 |
| `<任何完整报告 / draft / thought / debate / evidence>` | 不进入 Workflow history | 必须走 `*Ref` → Workspace |

> **原则**：Workflow history 中不保存任何完整 LLM 输出、完整报告、完整 draft / critique / revision / thought / debate argument / evidence content。所有长文本通过 Activity 写入 Workspace。Workflow history 只保存短摘要（≤500 字符）或 Workspace ref。

#### 3.4 Workflow / Activity 设计

```go
// ClassifyTaskComplexityActivity - 任务复杂度分类
type ClassifyTaskComplexityInput struct {
    Query     string `json:"query"`
    UserIntent string `json:"user_intent,omitempty"`
}

type ClassifyTaskComplexityResult struct {
    ComplexityScore float64 `json:"complexity_score"` // 0.0 - 1.0
    RiskLevel       string  `json:"risk_level"`       // "low" | "medium" | "high" | "critical"
    Summary         string  `json:"summary"`          // 短摘要（≤500 字符，进 Workflow history 可接受）
    ReasoningRef    string  `json:"reasoning_ref"`    // 长解释走 Workspace
    TokensUsed      int     `json:"tokens_used"`
}

// DetectTaskCapabilitiesActivity - 任务能力检测
type DetectTaskCapabilitiesInput struct {
    Query          string   `json:"query"`
    AvailableTools []string `json:"available_tools"`
    AllowTools     bool     `json:"allow_tools"`
    AllowSandbox   bool     `json:"allow_sandbox"`
    AllowResearch  bool     `json:"allow_research"`
}

type DetectTaskCapabilitiesResult struct {
    RequiresTools    bool     `json:"requires_tools"`
    RequiresSandbox  bool     `json:"requires_sandbox"`
    RequiresRAG      bool     `json:"requires_rag"`
    RequiresResearch bool     `json:"requires_research"`
    DetectedTools    []string `json:"detected_tools"`
    Summary          string   `json:"summary"`       // 短摘要（≤500 字符）
    ReasoningRef     string   `json:"reasoning_ref"` // 长解释走 Workspace
}

// EvaluateRoutingPolicyActivity - 路由策略评估（基于 complexity / capability / budget 选择 mode）
type EvaluateRoutingPolicyInput struct {
    ComplexityScore float64              `json:"complexity_score"`
    RiskLevel       string               `json:"risk_level"`
    RequiresTools   bool                 `json:"requires_tools"`
    RequiresSandbox bool                 `json:"requires_sandbox"`
    RequiresRAG     bool                 `json:"requires_rag"`
    RequiresResearch bool                `json:"requires_research"`
    RequireCitations bool                `json:"require_citations"`
    BudgetUSD       float64              `json:"budget_usd"`
    RouterConfig    RouterConfigSnapshot `json:"router_config"` // 配置快照，由 Workflow 从 RouteRequest 传入
}

type EvaluateRoutingPolicyResult struct {
    Decision RoutingDecision `json:"decision"`
}

// EstimateRouteCostActivity - 估算路由成本（token / USD）
type EstimateRouteCostInput struct {
    Mode         RoutingMode `json:"mode"`
    ModelTier    string      `json:"model_tier"`
    ComplexityScore float64  `json:"complexity_score"`
}

type EstimateRouteCostResult struct {
    EstimatedTokens int     `json:"estimated_tokens"`
    EstimatedUSD    float64 `json:"estimated_usd"`
    TokenBudget     int     `json:"token_budget"`
    CostBudgetUSD   float64 `json:"cost_budget_usd"`
}

// AuditRoutingDecisionActivity - 审计路由决策（写 Postgres + Workspace）
type AuditRoutingDecisionInput struct {
    SessionID  string          `json:"session_id"`
    WorkflowID string          `json:"workflow_id"`
    RunID      string          `json:"run_id"`
    Decision   RoutingDecision `json:"decision"`
    PolicyTrace string         `json:"policy_trace"` // 短解释，写 DB
}

type AuditRoutingDecisionResult struct {
    AuditID string `json:"audit_id"`
}

// EmitRoutingEventActivity - 发送路由事件（SSE / 事件总线）
type EmitRoutingEventInput struct {
    WorkflowID string          `json:"workflow_id"`
    EventType  string          `json:"event_type"` // "ROUTING_DECIDED" | "ROUTING_DISPATCHED" | "ROUTING_APPROVAL_REQUIRED"
    Decision   RoutingDecision `json:"decision"`
    Metadata   map[string]interface{} `json:"metadata,omitempty"`
}

// WriteRoutingPolicyTraceActivity - 将长 policy trace / 分类器原文写入 Workspace
// **必须通过 Activity 调用**：Workflow 不能直接写 Workspace
type WriteRoutingPolicyTraceInput struct {
    WorkflowID        string          `json:"workflow_id"`
    Decision          RoutingDecision `json:"decision"`
    ComplexitySummary string          `json:"complexity_summary"` // 短摘要，≤500 字符
    CapabilitySummary string          `json:"capability_summary"` // 短摘要，≤500 字符
}

type WriteRoutingPolicyTraceResult struct {
    PolicyTraceRef string `json:"policy_trace_ref"` // Workspace ref
}

// EvaluateApprovalPolicyActivity - 由 Phase 7B 提供（决策是否需要 Approval；Router 复用其入口）
//   输入：RoutingDecision + TokenBudgetUSD + RiskLevel
//   输出：{Required: bool, Reason: string, RiskLevel: string}
```

**AdvancedRoutingWorkflow 设计**（Cribug 真实实现，写入 `internal/workflows/router.go`）：

> **核心约束**：
> `AdvancedRoutingWorkflow` 只负责 deterministic orchestration。
> **所有** Workspace 写入、DB 审计、Redis event、SSE event、LLM classifier、Qdrant 检索、tool/sandbox 调用都必须通过 Activity 或 Child Workflow 完成。
> **严禁** Workflow 内直接调用任何外部 IO helper 函数（如 `writePolicyTraceToWorkspace()`）。

**高风险模式（fail-closed）定义**：

```go
// isHighRiskMode 判断 mode 是否属于高风险路径
// 高风险路径在 Approval 评估失败时必须 fail-closed，不能默认放行
func isHighRiskMode(mode RoutingMode) bool {
    switch mode {
    case RouteSandboxExecution, RouteResearchV2:
        return true
    case RouteDAGWorkflow, RouteSwarmWorkflow:
        // DAG / Swarm 内部可能包含 sandbox / tool_write，属于高风险
        return true
    default:
        return false
    }
}

// 注意：以下场景也需要 fail-closed（通过附加条件判断，不通过 mode 判断）：
//   - requires_sandbox=true（任何 mode 附加 sandbox 都是高风险）
//   - requires_research_v2=true（research_v2 publish 前）
//   - tool_write=true（任何 mode 执行写操作都是高风险）
//   - risk_level="critical"
```

```go
// AdvancedRoutingWorkflow - Phase 7 统一入口
// 约束：Workflow 不直接 IO；只做编排 + 决策；长 trace 走 Workspace
func AdvancedRoutingWorkflow(ctx workflow.Context, input RouteRequest) (RoutedExecutionResult, error) {
    workflowID := workflow.GetInfo(ctx).WorkflowExecution.ID
    runID := workflow.GetInfo(ctx).WorkflowExecution.RunID

    // Step 1: 任务复杂度分类
    var complexityResult ClassifyTaskComplexityResult
    if err := workflow.ExecuteActivity(ctx, "ClassifyTaskComplexityActivity",
        ClassifyTaskComplexityInput{Query: input.Query, UserIntent: input.UserIntent},
    ).Get(ctx, &complexityResult); err != nil {
        return RoutedExecutionResult{}, err
    }

    // Step 2: 能力检测
    var capabilityResult DetectTaskCapabilitiesResult
    if err := workflow.ExecuteActivity(ctx, "DetectTaskCapabilitiesActivity",
        DetectTaskCapabilitiesInput{
            Query:          input.Query,
            AvailableTools: input.AvailableTools,
            AllowTools:     input.AllowTools,
            AllowSandbox:   input.AllowSandbox,
            AllowResearch:  input.AllowResearch,
        },
    ).Get(ctx, &capabilityResult); err != nil {
        return RoutedExecutionResult{}, err
    }

    // Step 3: 路由策略评估（基于 complexity / capability / budget / 风险）
    var policyResult EvaluateRoutingPolicyResult
    if err := workflow.ExecuteActivity(ctx, "EvaluateRoutingPolicyActivity",
        EvaluateRoutingPolicyInput{
            ComplexityScore:  complexityResult.ComplexityScore,
            RiskLevel:        complexityResult.RiskLevel,
            RequiresTools:    capabilityResult.RequiresTools,
            RequiresSandbox:  capabilityResult.RequiresSandbox,
            RequiresRAG:      capabilityResult.RequiresRAG,
            RequiresResearch: capabilityResult.RequiresResearch,
            RequireCitations: input.RequireCitations,
            BudgetUSD:        input.BudgetUSD,
            RouterConfig:     input.RouterConfig,
        },
    ).Get(ctx, &policyResult); err != nil {
        return RoutedExecutionResult{}, err
    }

    decision := policyResult.Decision

    // Step 4: 成本估算
    var costResult EstimateRouteCostResult
    _ = workflow.ExecuteActivity(ctx, "EstimateRouteCostActivity",
        EstimateRouteCostInput{
            Mode:            decision.Mode,
            ModelTier:       decision.ModelTier,
            ComplexityScore: decision.ComplexityScore,
        },
    ).Get(ctx, &costResult)
    decision.TokenBudget = costResult.TokenBudget
    decision.CostBudgetUSD = costResult.CostBudgetUSD

    // Step 5: 长 policy trace 写 Workspace（不进 history）
    // **禁止** Workflow 直接写 Workspace。必须通过 Activity。
    if input.RouterConfig.DecisionAuditEnabled {
        var policyTrace WriteRoutingPolicyTraceResult
        err := workflow.ExecuteActivity(ctx, "WriteRoutingPolicyTraceActivity",
            WriteRoutingPolicyTraceInput{
                WorkflowID:   workflowID,
                Decision:     decision,
                ComplexitySummary: complexityResult.Summary, // 只传短摘要，不传完整 LLM 输出
                CapabilitySummary: capabilityResult.Summary, // 只传短摘要
            },
        ).Get(ctx, &policyTrace)
        if err != nil {
            // non-fatal：审计写入失败不阻塞主流程
            workflow.GetLogger(ctx).Warn("WriteRoutingPolicyTrace failed", "error", err)
        } else {
            decision.PolicyTraceRef = policyTrace.PolicyTraceRef
        }
    }

    // Step 6: 评估是否需要 Approval（高风险 / 高预算 / 沙箱 / 发布前）
    // ============================================================
    // Approval 失败策略：
    //   - 低风险路径（direct_answer / rag_answer / react_tool）: eval 失败 fallback 继续
    //   - 高风险路径（sandbox_execution / tool_write / publish / research_v2_publish）: fail-closed
    //     → 必须进入 approval_required 或返回 safe_error，不允许默认放行
    // ============================================================
    var approvalEval struct {
        Required  bool
        Reason    string
        RiskLevel string
    }
    errApproval := workflow.ExecuteActivity(ctx, "EvaluateApprovalPolicyActivity",
        map[string]interface{}{
            "complexity_score":  decision.ComplexityScore,
            "token_budget_usd":  decision.CostBudgetUSD,
            "tools":             capabilityResult.DetectedTools,
            "risk_level":        decision.RiskLevel,
            "mode":              string(decision.Mode),
            "requires_sandbox":  decision.RequiresSandbox,
            "requires_publish":  decision.RequiresResearchV2,
        },
    ).Get(ctx, &approvalEval)

    if errApproval != nil {
        // Approval 评估 Activity 失败 → 按风险等级分岔
        if isHighRiskMode(decision.Mode) || decision.RequiresSandbox || decision.RequiresResearchV2 {
            // fail-closed：高风险路径不默认放行
            return RoutedExecutionResult{
                Status:    "error",
                Reason:    "approval_policy_evaluation_failed_on_high_risk",
                Decision:  decision,
                FeedbackSummary: "Approval policy evaluation failed; high-risk task blocked pending human review",
                PendingApprovalID: "policy-eval-failed-" + workflowID,
            }, nil
        }
        // 低风险路径：fallback continue
        decision.RequiresApproval = false
    } else {
        decision.RequiresApproval = approvalEval.Required
        if decision.RequiresApproval {
            decision.Reason = decision.Reason + "; " + approvalEval.Reason
        }
    }

    // Step 7: 审计路由决策（写 Postgres）
    if input.RouterConfig.DecisionAuditEnabled {
        _ = workflow.ExecuteActivity(ctx, "AuditRoutingDecisionActivity",
            AuditRoutingDecisionInput{
                SessionID:  input.SessionID,
                WorkflowID: workflowID,
                RunID:      runID,
                Decision:   decision,
                PolicyTrace: decision.Reason,
            },
        ).Get(ctx, nil)
    }

    // Step 8: 发送 routing 事件
    _ = workflow.ExecuteActivity(ctx, "EmitRoutingEventActivity",
        EmitRoutingEventInput{
            WorkflowID: workflowID,
            EventType:  "ROUTING_DECIDED",
            Decision:   decision,
        },
    ).Get(ctx, nil)

    // Step 9: 分派到对应 Workflow（Child Workflow；不改写实现）
    // 如果 approval 需要，先进入 Approval gate。Approval 不是最终执行模式，而是高风险路径的 gate。
    // 审批通过后必须继续执行 Router 原本选择的 mode。
    if decision.RequiresApproval {
        return executeWithApprovalGate(ctx, input, decision)
    }

    return dispatchByMode(ctx, input, decision)
}

// ============================================================
// Approval Gate 状态机（Phase 7B 集成）
// ============================================================
// 原则：
//  1. Approval 使用 Temporal Signal 恢复 Workflow，不许通过直接改 DB/Redis 恢复
//  2. 审批通过后 continue 到原始 RoutingDecision.Mode
//  3. 审批记录写 DB + Workspace (audit)，但 Workflow 恢复只靠 Signal
//
// 状态机流程：
//   Approved:  → dispatchByMode(ctx, input, decision)     // 继续执行原 mode
//   Rejected:  → return RoutedExecutionResult{Status: "rejected", Feedback: ...}
//   Modified:  → 更新 RoutingDecision / execution input → dispatchByMode(ctx, input, modifiedDecision)
//   Timeout:   → return RoutedExecutionResult{Status: "timeout", Reason: "approval_timeout"}
//   Invalid:   → return error（signal 内容无法解析）
// ============================================================

// executeWithApprovalGate 封装 Approval ➜ Continue 状态机
func executeWithApprovalGate(ctx workflow.Context, input RouteRequest, decision RoutingDecision) (RoutedExecutionResult, error) {
    workflowID := workflow.GetInfo(ctx).WorkflowExecution.ID
    runID := workflow.GetInfo(ctx).WorkflowExecution.RunID

    // Step A: 写入 Approval request（Activity）
    var approvalReq RequestApprovalResult
    approvalTimeout := decision.Metadata["approval_timeout_seconds"].(int)
    requestedAt := workflow.Now(ctx)
    expiresAt := requestedAt.Add(time.Duration(approvalTimeout) * time.Second)

    err := workflow.ExecuteActivity(ctx, "RequestApprovalActivity",
        RequestApprovalInput{
            WorkflowID:    workflowID,
            RunID:         runID,
            Query:         decision.Reason,
            Reason:        decision.Reason,
            RiskLevel:     decision.RiskLevel,
            TimeoutSeconds: approvalTimeout,
            RequestedAt:   requestedAt,
            ExpiresAt:     expiresAt,
        },
    ).Get(ctx, &approvalReq)
    if err != nil {
        return RoutedExecutionResult{Status: "error", Metadata: map[string]interface{}{"step": "request_approval", "error": err.Error()}}, err
    }

    // Step B: 发 SSE
    _ = workflow.ExecuteActivity(ctx, "EmitStreamEventActivity",
        EmitStreamEventInput{
            WorkflowID: workflowID,
            EventType:  "APPROVAL_REQUESTED",
            Data: map[string]interface{}{
                "approval_id": approvalReq.ApprovalID,
                "mode":        string(decision.Mode),
                "risk_level":  decision.RiskLevel,
            },
        },
    ).Get(ctx, nil)

    // Step C: 等待 Signal（Temporal standard pattern）
    signalChan := workflow.GetSignalChannel(ctx, fmt.Sprintf("human-approval-%s", approvalReq.ApprovalID))
    deadline := expiresAt

    var signalPayload ApprovalSignalPayload
    more := true
    for more {
        selector := workflow.NewSelector(ctx)
        selector.AddReceive(signalChan, func(c workflow.ReceiveChannel, more bool) {
            c.Receive(ctx, &signalPayload)
        })
        selector.AddFuture(workflow.NewTimer(ctx, deadline.Sub(workflow.Now(ctx))), func(f workflow.Future) {})
        selector.Select(ctx)

        if workflow.Now(ctx).After(deadline) {
            // Timeout：安全退出，不继续执行
            respondedAt := workflow.Now(ctx)
            _ = recordApprovalAudit(ctx, approvalReq.ApprovalID, workflowID, decision, nil, "timeout", requestedAt, respondedAt)
            return RoutedExecutionResult{
                Status:    "timeout",
                Reason:    "approval_timeout",
                Decision:  decision,
                PendingApprovalID: approvalReq.ApprovalID,
            }, nil
        }

        if signalPayload.ApprovalID == approvalReq.ApprovalID {
            more = false
        }
    }

    respondedAt := workflow.Now(ctx)

    // Step D: Rejected → 安全退出，不执行
    if !signalPayload.Approved {
        _ = recordApprovalAudit(ctx, approvalReq.ApprovalID, workflowID, decision, boolPtr(false), signalPayload.Feedback, requestedAt, respondedAt)
        return RoutedExecutionResult{
            Status:    "rejected",
            Reason:    "approval_rejected",
            Decision:  decision,
            Feedback:  signalPayload.Feedback,
            PendingApprovalID: approvalReq.ApprovalID,
        }, nil
    }

    // Step E: Approved → 记录审计，继续原始 mode
    _ = recordApprovalAudit(ctx, approvalReq.ApprovalID, workflowID, decision, boolPtr(true), signalPayload.Feedback, requestedAt, respondedAt)

    // Step F: Modified → 使用 modified action 更新 decision 或 execution input
    if signalPayload.ModifiedAction != nil {
        if newMode, ok := signalPayload.ModifiedAction["mode"].(string); ok {
            decision.Mode = RoutingMode(newMode)
        }
        if newAddons, ok := signalPayload.ModifiedAction["addon_capabilities"].([]string); ok {
            // 合并 addon capabilities（不覆盖已有）
            for _, a := range newAddons {
                decision.AddonCapabilities = append(decision.AddonCapabilities, RouteAddon(a))
            }
        }
    }

    // Step G: 审批通过后，继续执行原始路由模式
    _ = workflow.ExecuteActivity(ctx, "EmitRoutingEventActivity",
        EmitRoutingEventInput{
            WorkflowID: workflowID,
            EventType:  "ROUTING_DISPATCHED",
            Decision:   decision,
        },
    ).Get(ctx, nil)

    return dispatchByMode(ctx, input, decision)
}

// dispatchByMode 按 RoutingDecision.Mode 分派到已有 Workflow（Child Workflow）
func dispatchByMode(ctx workflow.Context, input RouteRequest, decision RoutingDecision) (RoutedExecutionResult, error) {
    // 注意：dispatchTo* 全部以 Child Workflow 形式调用 Phase 4-7 已存在 workflow 入口
    switch decision.Mode {
    case RouteDirectAnswer:
        return dispatchToDirectAnswer(ctx, input, decision)
    case RouteRAGAnswer:
        return dispatchToRAGAnswer(ctx, input, decision)
    case RouteReActTool:
        return dispatchToReAct(ctx, input, decision)
    case RouteSandboxExecution:
        return dispatchToSandbox(ctx, input, decision)
    case RouteDAGWorkflow:
        return dispatchToDAG(ctx, input, decision)
    case RouteSwarmWorkflow:
        return dispatchToSwarm(ctx, input, decision)
    case RouteReflection:
        return dispatchToReflection(ctx, input, decision)
    case RouteTreeOfThoughts:
        return dispatchToToT(ctx, input, decision)
    case RouteDebate:
        return dispatchToDebate(ctx, input, decision)
    case RouteResearchV1:
        return dispatchToResearchV1(ctx, input, decision)
    case RouteResearchV2:
        return dispatchToResearchV2(ctx, input, decision)
    default:
        return dispatchToDirectAnswer(ctx, input, decision)
    }
}

// recordApprovalAudit 记录审批审计（Helper，内部调 Activity）
func recordApprovalAudit(ctx workflow.Context, approvalID, workflowID string, decision RoutingDecision, approved *bool, feedback string, requestedAt, respondedAt time.Time) error {
    return workflow.ExecuteActivity(ctx, "AuditApprovalActivity",
        AuditApprovalInput{
            ApprovalID: approvalID,
            WorkflowID: workflowID,
            Query:      decision.Reason,
            RiskLevel:  decision.RiskLevel,
            Approved:   approved,
            Feedback:   feedback,
            DurationMs: respondedAt.Sub(requestedAt).Milliseconds(),
        },
    ).Get(ctx, nil)
}

// dispatchTo* - 全部以 Child Workflow 形式调用 Phase 4-7 已存在 workflow 入口
// 例如：
//   func dispatchToDAG(ctx workflow.Context, input RouteRequest, decision RoutingDecision) (RoutedExecutionResult, error) {
//       cwo := workflow.ChildWorkflowOptions{WorkflowID: input.SessionID + ":dag"}
//       cctx := workflow.WithChildOptions(ctx, cwo)
//       var dagResult DAGWorkflowResult
//       if err := workflow.ExecuteChildWorkflow(cctx, "DAGWorkflow", dagInput).Get(cctx, &dagResult); err != nil {
//           return RoutedExecutionResult{}, err
//       }
//       return RoutedExecutionResult{...}, nil
//   }
// 注意：Child Workflow 类型必须已在 internal/workflows/registry.go 注册
```

**反模式（必须避免）**：

- ❌ Activity 直接做 workflow 级编排（参考 Slice 25/26/27 中 "ToTWorkflowActivity / DebateWorkflowActivity" 的反模式，Router 不能引入新的 "RouterWorkflowActivity"）
- ❌ Router 在 `AdvancedRoutingWorkflow` 内复制 DAG / Swarm / Reflection / ToT / Debate / Research v2 的实现
- ❌ Router 把整段 policy trace / 分类器原文塞进 Workflow history（必须走 `PolicyTraceRef` → Workspace）
- ❌ Router 通过直接改 Redis / DB 恢复下游 workflow（必须 Child Workflow / Signal）

#### 3.5 分类器实现

`ClassifyTaskComplexityActivity` 默认是 **heuristic**：

- `complexity_score = 0.0`
- 长度 < 50 字符 → 0.1
- 含"分析/对比/方案/比较/规划"等关键词 → +0.2
- 含"研究/报告/引用/多源" → +0.3
- 含"执行/运行/沙箱/代码" → +0.25
- 含"多 Agent / 多角色 / 团队 / 分工" → +0.3
- 多步骤（句号 / 分号 / 数字列表） → +0.1
- `risk_level`：
  - 0.0 - 0.3 → "low"
  - 0.3 - 0.6 → "medium"
  - 0.6 - 0.85 → "high"
  - 0.85 - 1.0 → "critical"

`mock_llm` 模式：返回固定 score / 字段（确定性，CI 用）。
`real_llm` 模式：调用 Python LLM Service `/v1/classify`（仅 `REAL_ROUTER_TEST=1` 时启用）。

#### 3.6 API 设计

```bash
# 路由但不执行（用于 preview / UI 决策展示）
POST /api/v1/tasks/route
Content-Type: application/json

Request:
{
    "session_id": "sess-xxx",
    "query": "比较 PostgreSQL 和 MySQL 在高并发写入下的性能差异",
    "available_tools": ["bash", "search"],
    "budget_usd": 0.5,
    "max_latency_ms": 30000,
    "require_citations": true,
    "allow_tools": true,
    "allow_sandbox": false,
    "allow_research": true
}

Response:
{
    "session_id": "sess-xxx",
    "decision": {
        "mode": "debate",
        "workflow_type": "DebateWorkflow",
        "reason": "high complexity, comparative query, multi-perspective",
        "complexity_score": 0.72,
        "risk_level": "medium",
        "requires_approval": false,
        "requires_rag": false,
        "requires_tools": false,
        "requires_sandbox": false,
        "requires_workspace": true,
        "requires_reflection": false,
        "requires_debate": true,
        "requires_tot": false,
        "requires_research_v2": false,
        "model_tier": "medium",
        "token_budget": 4000,
        "cost_budget_usd": 0.20,
        "confidence": 0.81,
        "policy_trace_ref": "router:sess-xxx:policy_trace"
    }
}

# 路由并执行
POST /api/v1/tasks/execute-routed
Body: 同上
Response: RoutedExecutionResult（见数据结构）

# 获取路由决策
GET /api/v1/tasks/{workflow_id}/routing-decision
Response: { decision: RoutingDecision, audit_ref: string }

# 获取路由事件流
GET /api/v1/tasks/{workflow_id}/routing-events
Response: SSE / 事件列表
```

> **不做**：Phase 8 的 SDK / CLI / OpenAI-compatible API / Multi-tenant；只做 Phase 7 自身需要的能力。

#### 3.7 成功路径

```
AdvancedRoutingWorkflow(query)
  → ClassifyTaskComplexityActivity
  → DetectTaskCapabilitiesActivity
  → EvaluateRoutingPolicyActivity
  → EstimateRouteCostActivity
  → WorkspaceAppend(policy_trace)
  → EvaluateApprovalPolicyActivity
  → AuditRoutingDecisionActivity
  → EmitRoutingEventActivity(ROUTING_DECIDED)
  → if requires_approval: dispatchToApproval
  → else: Child Workflow 分派到对应 mode
  → return RoutedExecutionResult
```

#### 3.8 失败路径

```
Classify 失败 → 使用默认 score=0.0, mode=direct_answer（兜底）
Detect 失败 → 不视为必填，使用 Input 中的 hints
EvaluatePolicy 失败 → 返回 error
Cost 估算失败 → 使用 0 / max budget（不阻塞）
Approval 评估失败 → 不要求 approval（保守分支不影响主流程）
Audit 失败 → 记录 warning，继续（non-fatal）
Child Workflow 失败 → 透传 error 到 RoutedExecutionResult.Status="error"
```

#### 3.9 验收标准

| 验收项 | 通过条件 |
|--------|----------|
| 简单问题路由 | complexity < 0.45 → `direct_answer` |
| 文档问答路由 | 命中"项目 / 文档"关键词 → `rag_answer` |
| 工具调用路由 | 命中"搜索 / 执行 / 计算" → `react_tool` |
| 代码运行路由 | 命中"运行 / 执行 / 沙箱" → `sandbox_execution` + `requires_approval=true` |
| 多步骤路由 | complexity >= 0.45 → `dag_workflow` |
| 多 Agent 路由 | complexity >= 0.65 → `swarm_workflow` |
| 方案比较路由 | 命中"比较 / 对比 / vs" → `debate` |
| 多路径搜索路由 | complexity >= 0.75 + 命中"路径 / 证明 / 搜索" → `tree_of_thoughts` |
| 深度研究路由 | complexity >= 0.70 + `require_citations=true` → `research_v2` |
| 决策审计 | decision 写 Postgres `routing_audit_logs` |
| Policy trace | 长 trace 写 Workspace，不进 Workflow history |
| 分类器默认 mock | CI 跑 mock / heuristic，不依赖真实 LLM |
| 真实 LLM 可选 | `REAL_ROUTER_TEST=1` 启用，`REAL_ROUTER_TEST_MAX_COST_USD=1` 限制 |
| 无 API key skip | 无 API key 时真实 LLM 测试 skip，不 fail |
| Router 与 HITL 解耦 | `requires_approval=true` 走 Phase 7B Approval |
| Router 不重写下游 | Router 只调 Child Workflow 入口，不复制实现 |

#### 3.10 Shannon 参考点

| 功能 | Shannon 参考路径 | Cribug Phase 7A 实现 |
|------|----------------|---------------------|
| 策略选择 | `workflows/strategies/research.go`、`scientific.go`、`exploratory.go` | `EvaluateRoutingPolicyActivity` |
| 策略工具 | `strategies/strategy_helpers.go:shouldReflect` | heuristic 规则表 |
| Pattern Registry | `workflows/patterns/registry.go` | `internal/workflows/registry.go`（已存在；Router 通过它注册 `AdvancedRoutingWorkflow`） |
| Approval Policy | `workflows/middleware_approval.go:CheckApprovalPolicy` | 复用 `EvaluateApprovalPolicyActivity`（Slice 24 提供） |
| Control Signal | `workflows/control/signals.go` | 复用（Slice 24 提供） |
| Reflection / ToT / Debate | `workflows/patterns/*.go` | Child Workflow 分派，不复制实现 |

#### 3.11 Cribug Lite 实现边界

- **不做**：动态图神经网络 router（只做规则 + 阈值）
- **不做**：Router 的多租户隔离（Phase 8）
- **不做**：Router 的可视化 Dashboard（Phase 7B mock UI 即可）
- **不做**：Router 自动训练（分类器策略走 config / env，不在线学习）

### Phase 7B Slice 24：HITL / Approval / UI Control

#### 4.1 核心架构澄清

HITL / Approval / UI Control 提供人类介入审批能力，允许高风险操作暂停等待人工确认，UI/Dashboard 可以查看状态并发起 approve/reject/modify 操作。

**参考 Shannon 实现**（只读参考，不修改）：
- `go/orchestrator/internal/workflows/middleware_approval.go` - `RequestAndWaitApproval`、`CheckApprovalPolicy`
- `go/orchestrator/internal/workflows/control/handler.go` - `SignalHandler`（pause/resume/cancel）
- `go/orchestrator/internal/workflows/control/signals.go` - Signal 常量 + `WorkflowControlState`
- `go/orchestrator/internal/activities/human_intervention.go` - `HumanApprovalInput`、`HumanApprovalResult`、`ApprovalPolicy`
- `go/orchestrator/internal/httpapi/approval.go` - `ApprovalHandler`、`handleDecision`
- `go/orchestrator/cmd/gateway/internal/handlers/review.go` - `ReviewHandler`（HITL review）
- `protos/orchestrator/orchestrator.proto` - `ApproveTask`、`GetPendingApprovals`、`PauseTask`、`ResumeTask`
- `desktop/components/` - `SwarmTaskBoard`、`RunTimeline`、`RunConversation`

#### 4.2 数据结构

```go
// ApprovalRequest - 审批请求（Workflow 发起）
type ApprovalRequest struct {
    ApprovalID   string                 `json:"approval_id"`
    WorkflowID   string                 `json:"workflow_id"`
    RunID        string                 `json:"run_id"`
    Query        string                 `json:"query"`            // 人类可读的问题描述
    Context      map[string]interface{} `json:"context"`          // 审批上下文（摘要，不是完整 payload）
    ProposedAction map[string]interface{} `json:"proposed_action"` // 建议的操作
    Reason       string                 `json:"reason"`           // 为什么需要审批
    RiskLevel    string                 `json:"risk_level"`       // "low" | "medium" | "high" | "critical"
    RequestedAt  time.Time              `json:"requested_at"`     // Workflow 用 workflow.Now(ctx) 生成后传入
    ExpiresAt    time.Time              `json:"expires_at"`       // Workflow 用 workflow.Now(ctx).Add(timeout) 生成后传入
    Metadata     map[string]interface{} `json:"metadata,omitempty"`
}

// ApprovalResponse - 人类响应
type ApprovalResponse struct {
    ApprovalID     string                 `json:"approval_id"`
    Approved       bool                   `json:"approved"`
    Feedback       string                 `json:"feedback,omitempty"`          // 可选的反馈/修改意见
    ModifiedAction map[string]interface{} `json:"modified_action,omitempty"`   // 如果 reject+modify
    ApprovedBy     string                 `json:"approved_by,omitempty"`
    RespondedAt    time.Time             `json:"responded_at"`
}

// WorkflowControlState - 工作流控制状态
type WorkflowControlState struct {
    IsPaused      bool       `json:"is_paused"`
    IsCancelled   bool       `json:"is_cancelled"`
    PausedAt       *time.Time `json:"paused_at,omitempty"`
    PauseReason   string     `json:"pause_reason,omitempty"`
    PausedBy      string     `json:"paused_by,omitempty"`
    CancelReason  string     `json:"cancel_reason,omitempty"`
    CancelledBy   string     `json:"cancelled_by,omitempty"`
}

// ApprovalPolicy - 审批策略
type ApprovalPolicy struct {
    Enabled                   bool     `json:"enabled"`
    ComplexityThreshold       float64  `json:"complexity_threshold"`   // >= 此值触发审批
    TokenBudgetThresholdUSD   float64  `json:"token_budget_threshold"` // 超此预算触发审批
    RequireForTools           []string `json:"require_for_tools"`      // 这些 tool 必须审批
    RequireForRiskLevels      []string `json:"require_for_risk_levels"` // "high" | "critical"
    DefaultTimeoutSeconds     int      `json:"default_timeout_seconds"` // 默认 3600（1小时）
}

// ApprovalAuditLog - 审批审计
type ApprovalAuditLog struct {
    ID            string    `json:"id"`
    ApprovalID    string    `json:"approval_id"`
    WorkflowID    string    `json:"workflow_id"`
    Query         string    `json:"query"`
    RiskLevel     string    `json:"risk_level"`
    Approved      *bool     `json:"approved,omitempty"`     // nil = timeout
    Feedback      string    `json:"feedback,omitempty"`
    ApprovedBy    string    `json:"approved_by,omitempty"`
    DurationMs    int64     `json:"duration_ms"`
    CreatedAt     time.Time `json:"created_at"`
}
```

#### 4.3 Activity 设计

```go
// RequestApprovalActivity - 请求人类审批（时间戳由 Workflow 生成后传入）
type RequestApprovalInput struct {
    SessionID       string                   `json:"session_id"`
    WorkflowID      string                   `json:"workflow_id"`
    RunID           string                   `json:"run_id"`
    Query           string                   `json:"query"`
    Context         map[string]interface{}   `json:"context"`
    ProposedAction  map[string]interface{}  `json:"proposed_action"`
    Reason          string                   `json:"reason"`
    RiskLevel       string                   `json:"risk_level"`
    TimeoutSeconds  int                      `json:"timeout_seconds"`   // 默认 3600
    RequestedAt     time.Time                `json:"requested_at"`      // Workflow 生成后传入
    ExpiresAt       time.Time                `json:"expires_at"`       // Workflow 生成后传入
    Metadata        map[string]interface{}   `json:"metadata,omitempty"`
}

type RequestApprovalResult struct {
    ApprovalID  string    `json:"approval_id"`
    // RequestedAt / ExpiresAt 直接使用 Input 中 Workflow 传入的值，Activity 不生成业务时间戳
}

// GetApprovalStatusActivity - 获取审批状态
type GetApprovalStatusInput struct {
    ApprovalID string `json:"approval_id"`
}

type GetApprovalStatusResult struct {
    ApprovalID   string     `json:"approval_id"`
    Status       string     `json:"status"`  // "pending" | "approved" | "rejected" | "modified" | "timeout"
    Approved     *bool      `json:"approved,omitempty"`
    Feedback     string     `json:"feedback,omitempty"`
    RespondedAt  *time.Time `json:"responded_at,omitempty"`
}

// ProcessApprovalResponseActivity - 处理人类响应
type ProcessApprovalResponseInput struct {
    ApprovalID     string                   `json:"approval_id"`
    WorkflowID      string                   `json:"workflow_id"`
    RunID           string                   `json:"run_id"`
    Approved        bool                     `json:"approved"`
    Feedback        string                   `json:"feedback,omitempty"`
    ModifiedAction  map[string]interface{}   `json:"modified_action,omitempty"`
    ApprovedBy      string                   `json:"approved_by"`
}

type ProcessApprovalResponseResult struct {
    ProcessedAt time.Time `json:"processed_at"`
}

// EvaluateApprovalPolicyActivity - 评估是否需要审批
type EvaluateApprovalPolicyInput struct {
    ComplexityScore float64                `json:"complexity_score"`
    TokenBudgetUSD  float64                `json:"token_budget_usd"`
    Tools          []string                `json:"tools"`
    RiskLevel      string                  `json:"risk_level"`
}

type EvaluateApprovalPolicyResult struct {
    Required   bool     `json:"required"`
    Reason     string   `json:"reason"`
    RiskLevel  string   `json:"risk_level"`
}

// AuditApprovalActivity - 审计审批
type AuditApprovalInput struct {
    ApprovalID  string    `json:"approval_id"`
    WorkflowID  string    `json:"workflow_id"`
    Query       string    `json:"query"`
    RiskLevel   string    `json:"risk_level"`
    Approved    *bool     `json:"approved,omitempty"`
    Feedback    string    `json:"feedback,omitempty"`
    ApprovedBy  string    `json:"approved_by,omitempty"`
    DurationMs  int64     `json:"duration_ms"`
}

type AuditApprovalResult struct {
    AuditID string `json:"audit_id"`
}

// PauseWorkflowActivity - 暂停工作流
type PauseWorkflowInput struct {
    WorkflowID string `json:"workflow_id"`
    Reason     string `json:"reason"`
    PausedBy   string `json:"paused_by"`
}

// ResumeWorkflowActivity - 恢复工作流
type ResumeWorkflowInput struct {
    WorkflowID string `json:"workflow_id"`
    ResumedBy  string `json:"resumed_by"`
}

// CancelWorkflowActivity - 取消工作流
type CancelWorkflowInput struct {
    WorkflowID  string `json:"workflow_id"`
    Reason     string `json:"reason"`
    CancelledBy string `json:"cancelled_by"`
}

// GetControlStateActivity - 获取控制状态
type GetControlStateInput struct {
    WorkflowID string `json:"workflow_id"`
}

type GetControlStateResult struct {
    State WorkflowControlState `json:"state"`
}
```

#### 4.4 Signal 设计

```go
// Signal 常量（参考 Shannon control/signals.go）
const (
    SignalPause    = "pause_v1"
    SignalResume   = "resume_v1"
    SignalCancel   = "cancel_v1"
    SignalApproval = "human-approval-%s"  // 格式：human-approval-{approvalID}
)

// Signal payloads
type PauseSignalPayload struct {
    Reason     string `json:"reason"`
    RequestedBy string `json:"requested_by"`
}

type ResumeSignalPayload struct {
    RequestedBy string `json:"requested_by"`
}

type CancelSignalPayload struct {
    Reason      string `json:"reason"`
    CancelledBy string `json:"cancelled_by"`
}

type ApprovalSignalPayload struct {
    ApprovalID     string                   `json:"approval_id"`
    Approved       bool                     `json:"approved"`
    Feedback       string                   `json:"feedback,omitempty"`
    ModifiedAction map[string]interface{}  `json:"modified_action,omitempty"`
    ApprovedBy     string                   `json:"approved_by"`
}
```

#### 4.5 Workflow 设计（Approval + Control）

```go
// HITLApprovalWorkflow - 等待人类审批的 Workflow
func HITLApprovalWorkflow(ctx workflow.Context, input HITLWorkflowInput) (HITLResult, error) {
    workflowID := workflow.GetInfo(ctx).WorkflowExecution.ID

    // Step 1: Policy 检查（是否需要审批）
    var policyResult EvaluateApprovalPolicyResult
    err := workflow.ExecuteActivity(ctx, "EvaluateApprovalPolicyActivity",
        EvaluateApprovalPolicyInput{
            ComplexityScore: input.ComplexityScore,
            TokenBudgetUSD:  input.TokenBudgetUSD,
            Tools:          input.Tools,
            RiskLevel:      input.RiskLevel,
        },
    ).Get(ctx, &policyResult)
    if err != nil {
        return HITLResult{Success: false, Error: err.Error()}, err
    }

    if !policyResult.Required {
        // 不需要审批，直接执行
        return executeWithoutApproval(ctx, input)
    }

    // Step 1.5: 生成业务时间戳（Workflow 生成，Activity 不负责）
    requestedAt := workflow.Now(ctx)
    timeout := time.Duration(input.ApprovalTimeoutSeconds) * time.Second
    expiresAt := requestedAt.Add(timeout)

    // Step 2: 请求审批
    var approvalResult RequestApprovalResult
    approvalOpts := workflow.ActivityOptions{
        StartToCloseTimeout: 30 * time.Second,
    }
    approvalCtx := workflow.WithActivityOptions(ctx, approvalOpts)
    err = workflow.ExecuteActivity(approvalCtx, "RequestApprovalActivity",
        RequestApprovalInput{
            SessionID:      input.SessionID,
            WorkflowID:     workflowID,
            RunID:          workflow.GetInfo(ctx).WorkflowExecution.RunID,
            Query:          policyResult.Reason,
            Context:        input.ContextSummary,
            ProposedAction: input.ProposedAction,
            Reason:         policyResult.Reason,
            RiskLevel:      policyResult.RiskLevel,
            TimeoutSeconds: input.ApprovalTimeoutSeconds,
            RequestedAt:    requestedAt,  // Workflow 生成
            ExpiresAt:      expiresAt,    // Workflow 生成
        },
    ).Get(ctx, &approvalResult)
    if err != nil {
        return HITLResult{Success: false, Error: err.Error()}, err
    }

    // Step 3: 发送 SSE 事件
    _ = workflow.ExecuteActivity(ctx, "EmitStreamEventActivity",
        EmitStreamEventInput{
            WorkflowID: workflowID,
            EventType:  "APPROVAL_REQUESTED",
            Data: map[string]interface{}{
                "approval_id": approvalResult.ApprovalID,
                "query":      policyResult.Reason,
                "risk_level": policyResult.RiskLevel,
            },
        },
    )

    // Step 4: 等待 Signal（使用 workflow.GetSignalChannel + workflow.NewSelector + workflow.NewTimer）
    signalChan := workflow.GetSignalChannel(ctx, fmt.Sprintf("human-approval-%s", approvalResult.ApprovalID))

    // 超时控制（复用 Step 1.5 已计算出的 timeout / expiresAt）
    deadline := expiresAt

    var signalPayload ApprovalSignalPayload
    more := true
    for more {
        selector := workflow.NewSelector(ctx)
        selector.AddReceive(signalChan, func(c workflow.ReceiveChannel, more bool) {
            c.Receive(ctx, &signalPayload)
        })
        selector.AddReceive(workflow.NewTimer(ctx, deadline.Sub(workflow.Now(ctx))), func(c workflow.ReceiveChannel, more bool) {
            // 超时
        })
        selector.Select(ctx)
        if workflow.Now(ctx).After(deadline) {
            // 超时，返回 partial result
            return HITLResult{
                Success:    false,
                Partial:    true,
                Reason:     "approval_timeout",
                ApprovalID: approvalResult.ApprovalID,
                Error:      "approval timeout",
            }, nil
        }
        if signalPayload.ApprovalID == approvalResult.ApprovalID {
            more = false
        }
    }

    // Step 5: 处理响应
    if !signalPayload.Approved {
        // Rejected
        respondedAt := workflow.Now(ctx)
        _ = workflow.ExecuteActivity(ctx, "AuditApprovalActivity",
            AuditApprovalInput{
                ApprovalID: approvalResult.ApprovalID,
                WorkflowID:  workflowID,
                Query:      policyResult.Reason,
                RiskLevel:  policyResult.RiskLevel,
                Approved:   boolPtr(false),
                Feedback:   signalPayload.Feedback,
                ApprovedBy: signalPayload.ApprovedBy,
                DurationMs: respondedAt.Sub(requestedAt).Milliseconds(),
            },
        )
        return HITLResult{
            Success:    false,
            Partial:    true,
            Reason:     "approval_rejected",
            ApprovalID: approvalResult.ApprovalID,
            Feedback:   signalPayload.Feedback,
        }, nil
    }

    // Step 6: Approved（可能被 Modified）
    _ = workflow.ExecuteActivity(ctx, "ProcessApprovalResponseActivity",
        ProcessApprovalResponseInput{
            ApprovalID:    approvalResult.ApprovalID,
            WorkflowID:    workflowID,
            RunID:         workflow.GetInfo(ctx).WorkflowExecution.RunID,
            Approved:      signalPayload.Approved,
            Feedback:      signalPayload.Feedback,
            ModifiedAction: signalPayload.ModifiedAction,
            ApprovedBy:    signalPayload.ApprovedBy,
        },
    )

    respondedAt := workflow.Now(ctx)
    _ = workflow.ExecuteActivity(ctx, "AuditApprovalActivity",
        AuditApprovalInput{
            ApprovalID:  approvalResult.ApprovalID,
            WorkflowID:  workflowID,
            Query:      policyResult.Reason,
            RiskLevel:  policyResult.RiskLevel,
            Approved:   boolPtr(true),
            Feedback:   signalPayload.Feedback,
            ApprovedBy: signalPayload.ApprovedBy,
            DurationMs: respondedAt.Sub(requestedAt).Milliseconds(),
        },
    )

    // Step 7: 执行被批准的操作（使用 ModifiedAction 如果提供了）
    return executeApprovedAction(ctx, input, signalPayload.ModifiedAction)
}

// ControlSignalHandler - Workflow 级别的 pause/resume/cancel 集成（参考 Shannon control/handler.go）
// 注意：这不是独立 goroutine，而是集成到主 Workflow selector 中
// 在主 Workflow 的 selector 中添加 pause/resume/cancel signal channel 即可：
//   signalChan := workflow.GetSignalChannel(ctx, "control_signals")
//   selector.AddReceive(signalChan, func(c workflow.ReceiveChannel, more bool) { ... })
// 不要写出独立的无限循环函数。
```

#### 4.6 API 设计

```bash
# 获取待审批列表
GET /api/v1/approvals/pending

Response:
{
    "approvals": [
        {
            "approval_id": "approval-xxx",
            "workflow_id": "wf-xxx",
            "query": "执行高风险 tool: bash",
            "risk_level": "critical",
            "requested_at": "2026-05-25T10:00:00Z",
            "expires_at": "2026-05-25T11:00:00Z"
        }
    ]
}

# 提交审批决策
POST /api/v1/approvals/decision
Content-Type: application/json

Request:
{
    "approval_id": "approval-xxx",
    "workflow_id": "wf-xxx",
    "run_id": "run-xxx",
    "approved": true,
    "feedback": "同意执行",
    "modified_action": null,
    "approved_by": "user@example.com"
}

Response:
{
    "status": "sent",
    "workflow_id": "wf-xxx",
    "run_id": "run-xxx",
    "approval_id": "approval-xxx"
}

# 获取审批详情
GET /api/v1/approvals/{approval_id}

# 获取工作流控制状态
GET /api/v1/tasks/{workflow_id}/control-state

Response:
{
    "is_paused": false,
    "is_cancelled": false,
    "paused_at": null,
    "pause_reason": "",
    "cancel_reason": ""
}

# 暂停工作流
POST /api/v1/tasks/{workflow_id}/pause
Content-Type: application/json

Request:
{
    "reason": "用户请求暂停",
    "paused_by": "user@example.com"
}

# 恢复工作流
POST /api/v1/tasks/{workflow_id}/resume
Content-Type: application/json

Request:
{
    "resumed_by": "user@example.com"
}

# 取消工作流
POST /api/v1/tasks/{workflow_id}/cancel
Content-Type: application/json

Request:
{
    "reason": "用户请求取消",
    "cancelled_by": "user@example.com"
}
```

#### 4.7 成功路径

```
Workflow 执行到审批检查点
  → EvaluateApprovalPolicyActivity
  → 如果需要审批：
      → RequestApprovalActivity（创建 ApprovalRequest）
      → EmitStreamEvent（APPROVAL_REQUESTED）
      → workflow.GetSignalChannel + workflow.NewSelector + workflow.NewTimer 等待 Signal
  → 如果不需要审批：直接执行

人类通过 UI / API 响应：
  → POST /api/v1/approvals/decision
  → Gateway → Orchestrator gRPC → Signal 发送到 Workflow
  → Workflow 收到 Signal，继续执行（Approved/Rejected/Modified）

  → 如果 Approved：
      → ProcessApprovalResponseActivity
      → AuditApprovalActivity
      → 继续执行被批准的操作

  → 如果 Rejected：
      → AuditApprovalActivity
      → 返回 partial result

  → 如果 Modified：
      → ProcessApprovalResponseActivity
      → AuditApprovalActivity
      → 使用 ModifiedAction 执行

  → 如果 Timeout：
      → 返回 partial result（reason: "approval_timeout"）
```

#### 4.8 失败路径

```
Policy 检查失败 → 返回 error，不进入审批流程
Approval Request 创建失败 → 返回 error
Signal 等待超时 → 返回 partial result（reason: "approval_timeout"）
Rejection + Feedback → 返回 partial result + feedback
ModifiedAction 格式错误 → 拒绝执行，返回 error
Audit 写入失败 → 记录 warning，继续（non-fatal）
```

#### 4.9 验收标准

| 验收项 | 通过条件 |
|--------|----------|
| Policy 检查 | 复杂任务触发审批，低风险任务直接执行 |
| Approval Request 创建 | 生成唯一 approval_id，发 SSE |
| Signal 等待 | Workflow 正确 Await，不消耗 CPU（无轮询） |
| Approve | Workflow 收到 Signal 后继续执行 |
| Reject | Workflow 返回 partial result |
| Modified | Workflow 使用 ModifiedAction 执行 |
| Timeout | 超时后返回 partial result，不阻塞 |
| Audit | 所有审批记录写入 PostgreSQL |
| Control State | pause/resume/cancel 状态正确 |
| UI Contract | Dashboard 可以通过 API 获取状态 |

#### 4.10 Shannon 参考点

| 功能 | Shannon 实现路径 | Cribug Phase 7B 实现 |
|------|----------------|---------------------|
| Approval Request | `human_intervention.go` | `RequestApprovalActivity` |
| Policy Check | `middleware_approval.go:CheckApprovalPolicy` | `EvaluateApprovalPolicyActivity` |
| Signal Wait | `middleware_approval.go:RequestAndWaitApproval` | `workflow.GetSignalChannel + workflow.NewSelector + workflow.NewTimer` |
| Control Handler | `control/handler.go:SignalHandler` | 内嵌 Workflow 级别 |
| Pause/Resume | `control/signals.go` | `SignalPause/SignalResume` |
| Approval API | `httpapi/approval.go:handleDecision` | `POST /api/v1/approvals/decision` |
| Review Handler | `review.go:ReviewHandler` | `POST /api/v1/review/{workflowID}` |
| Desktop UI | `desktop/components/` | mock UI contract（不做完整前端） |

#### 4.11 Cribug Lite 实现边界

- **不做**：完整 Dashboard/UI 产品（只提供 API contract + mock 前端 contract）
- **不做**：复杂的 multi-level approval hierarchy（只做单级）
- **不做**：企业身份集成（LDAP/SAML）—— Phase 8
- **不做**：Approval 持久化的 all cases（只做 audit log）

---

## 四、Phase 7C Slice 25：Reflection Production Mode

### 5.1 核心架构澄清

Reflection Production Mode 提供 generate -> reflect -> revise 能力，多轮 reflection 直到 confidence threshold，生成可审计的 reflection summary。

**参考 Shannon 实现**：
- `go/orchestrator/internal/workflows/patterns/reflection.go` - `ReflectionConfig`、`ReflectOnResult`
- `go/orchestrator/internal/workflows/patterns/registry.go` - Pattern 类型
- `go/orchestrator/internal/activities/evaluate.go` - `EvaluateResult`（评分）
- `python/llm-service/llm_service/api/evaluate.py` - Evaluation API
- `go/orchestrator/internal/strategies/strategy_helpers.go` - `shouldReflect`

**重要区分**：
- **Reflection 不等于 ReAct**：ReAct 是 reasoning loop，Reflection 是质量评估 + 改进
- **Reflection 不保存模型隐藏 CoT**：只保存 audit summary（不暴露内部思考）
- **Reflection 是可选的**：由 `reflection_confidence_threshold` 控制是否触发

### 5.2 数据结构

```go
// ReflectionConfig - Reflection 配置
type ReflectionConfig struct {
    Enabled             bool     `json:"enabled"`
    MaxRounds           int      `json:"max_rounds"`            // 默认 3
    ConfidenceThreshold float64  `json:"confidence_threshold"`  // 默认 0.8
    Criteria            []string `json:"criteria"`              // 评估标准
    TimeoutMs           int      `json:"timeout_ms"`            // 默认 60000
    Rubric              string   `json:"rubric,omitempty"`       // 自定义评分标准
    MockLLM             bool     `json:"mock_llm"`              // 默认 true，由 Workflow input 控制
}

// ReflectionInput - Reflection 输入
type ReflectionInput struct {
    Query       string                  `json:"query"`
    DraftRef   string                  `json:"draft_ref"`         // 初始草稿的 Workspace ref
    Context    map[string]interface{}  `json:"context"`           // 上下文
    Config     ReflectionConfig        `json:"config"`
}

// ReflectionOutput - Reflection 输出
type ReflectionOutput struct {
    FinalResultRef string    `json:"final_result_ref"`  // 最终答案的 Workspace ref
    FinalScore     float64   `json:"final_score"`
    TotalRounds    int       `json:"total_rounds"`
    TotalTokens    int       `json:"total_tokens"`
    AuditSummary   string    `json:"audit_summary"`      // 可审计的 summary（不暴露隐藏 CoT）
}

// ReflectionRound - 单轮 Reflection（Workflow history 只保存 metadata，不保存完整 draft）
type ReflectionRound struct {
    RoundIndex      int       `json:"round_index"`
    DraftRef       string    `json:"draft_ref"`          // 本轮草稿的 Workspace ref
    CritiqueRef     string    `json:"critique_ref"`       // 本轮批评的 Workspace ref
    RevisionRef    string    `json:"revision_ref"`       // 本轮修订的 Workspace ref（如果有）
    Score           float64   `json:"score"`
    Feedback        string    `json:"feedback"`           // 短 feedback 摘要
    TokensUsed      int       `json:"tokens_used"`
    Timestamp       time.Time `json:"timestamp"`
}
```

### 5.3 Activity 设计

```go
// EvaluateDraftActivity - 评估草稿质量（返回评分 + critique ref）
type EvaluateDraftInput struct {
    Query      string   `json:"query"`
    DraftRef   string   `json:"draft_ref"`
    Criteria   []string `json:"criteria"`
    Rubric     string   `json:"rubric,omitempty"`
    MockLLM    bool     `json:"mock_llm"`
}

type EvaluateDraftResult struct {
    Score       float64 `json:"score"`        // 0.0 - 1.0
    Issues      []string `json:"issues"`      // 问题列表
    Feedback    string  `json:"feedback"`     // 短 feedback 摘要（不进 Workspace ref）
    CritiqueRef string  `json:"critique_ref"` // 完整 critiques 的 Workspace ref
    TokensUsed  int     `json:"tokens_used"`
}

// ReviseDraftActivity - 基于 critique 修订草稿
type ReviseDraftInput struct {
    Query       string `json:"query"`
    DraftRef    string `json:"draft_ref"`
    CritiqueRef string `json:"critique_ref"`
    MockLLM     bool   `json:"mock_llm"`
}

type ReviseDraftResult struct {
    RevisedDraftRef string `json:"revised_draft_ref"` // 修订后完整文本的 Workspace ref
    TokensUsed      int    `json:"tokens_used"`
}

// ReflectionProductionWorkflow - 多轮 Reflection Workflow（不是 Activity）
// 约束：Reflection 是 Workflow，**不是 Activity**。不要定义 "ReflectionWorkflowActivity"。
// 下面结构体仅作为外部触发 / Re-entry 时的契约使用；内部状态由 Workflow 维护。
type ReflectionWorkflowInput struct {
    Query     string                 `json:"query"`
    DraftRef  string                 `json:"draft_ref"`   // 初始草稿的 Workspace ref
    Context   map[string]interface{} `json:"context"`
    Config    ReflectionConfig        `json:"config"`
}

type ReflectionWorkflowResult struct {
    FinalResultRef string             `json:"final_result_ref"` // 最终答案的 Workspace ref
    FinalScore     float64            `json:"final_score"`
    TotalRounds    int               `json:"total_rounds"`
    TotalTokens    int               `json:"total_tokens"`
    AuditSummary   string            `json:"audit_summary"`
    Rounds         []ReflectionRound  `json:"rounds"` // 只保存 metadata ref，不保存完整草稿
}

// AuditReflectionActivity - 审计 Reflection
type AuditReflectionInput struct {
    WorkflowID   string               `json:"workflow_id"`
    Query        string               `json:"query"`
    TotalRounds  int                  `json:"total_rounds"`
    FinalScore   float64               `json:"final_score"`
    AuditSummary string               `json:"audit_summary"`
    TokensUsed   int                  `json:"tokens_used"`
}

type AuditReflectionResult struct {
    AuditID string `json:"audit_id"`
}
```

### 5.4 Workflow 设计（Reflection）

```go
// ReflectionProductionWorkflow - Reflection Workflow
// 约束：Workflow 只保存 ref / score / summary，不保存完整 draft / critique / revision
func ReflectionProductionWorkflow(ctx workflow.Context, input ReflectionWorkflowInput) (ReflectionWorkflowResult, error) {
    workflowID := workflow.GetInfo(ctx).WorkflowExecution.ID
    config := input.Config

    // 初始化
    currentDraftRef := input.DraftRef
    currentScore := 0.0
    totalTokens := 0
    rounds := []ReflectionRound{}

    for round := 0; round < config.MaxRounds; round++ {
        roundStart := workflow.Now(ctx)

        // Step 1: 评估当前 draft
        var evalResult EvaluateDraftResult
        evalOpts := workflow.ActivityOptions{
            StartToCloseTimeout: time.Duration(config.TimeoutMs) * time.Millisecond,
        }
        evalCtx := workflow.WithActivityOptions(ctx, evalOpts)
        err := workflow.ExecuteActivity(evalCtx, "EvaluateDraftActivity",
            EvaluateDraftInput{
                Query:    input.Query,
                DraftRef: currentDraftRef,
                Criteria: config.Criteria,
                Rubric:   config.Rubric,
                MockLLM:  config.MockLLM,
            },
        ).Get(ctx, &evalResult)
        if err != nil {
            // 评估失败，返回当前 draft ref
            break
        }

        currentScore = evalResult.Score
        totalTokens += evalResult.TokensUsed

        // Step 2: 记录 round metadata（不是完整 draft）
        roundSummary := ReflectionRound{
            RoundIndex: round,
            CritiqueRef: evalResult.CritiqueRef, // Workspace ref
            Score:      evalResult.Score,
            Feedback:   evalResult.Feedback, // 短 feedback 摘要
            TokensUsed: evalResult.TokensUsed,
            Timestamp:  roundStart,
        }
        rounds = append(rounds, roundSummary)

        // Step 3: 检查是否达标
        if evalResult.Score >= config.ConfidenceThreshold {
            // 达标，生成 audit summary（不暴露隐藏 CoT）
            auditSummary := generateAuditSummary(input.Query, rounds)

            return ReflectionWorkflowResult{
                FinalResultRef: currentDraftRef, // 最后一个 draft ref
                FinalScore:      currentScore,
                TotalRounds:     round + 1,
                TotalTokens:     totalTokens,
                AuditSummary:    auditSummary,
                Rounds:          rounds,
            }, nil
        }

        // Step 4: 未达标，修订 draft
        if round < config.MaxRounds-1 {
            var reviseResult ReviseDraftResult
            err := workflow.ExecuteActivity(ctx, "ReviseDraftActivity",
                ReviseDraftInput{
                    Query:      input.Query,
                    DraftRef:   currentDraftRef,
                    CritiqueRef: evalResult.CritiqueRef,
                    MockLLM:    config.MockLLM,
                },
            ).Get(ctx, &reviseResult)
            if err != nil {
                // 修订失败，返回当前 draft ref
                break
            }

            // 更新 round summary，加入 revision ref
            rounds[len(rounds)-1].RevisionRef = reviseResult.RevisedDraftRef

            currentDraftRef = reviseResult.RevisedDraftRef
            totalTokens += reviseResult.TokensUsed
        }
    }

    // 超出最大轮次，返回当前 draft ref
    auditSummary := generateAuditSummary(input.Query, rounds)
    return ReflectionWorkflowResult{
        FinalResultRef: currentDraftRef,
        FinalScore:     currentScore,
        TotalRounds:    len(rounds),
        TotalTokens:    totalTokens,
        AuditSummary:   auditSummary,
        Rounds:         rounds,
    }, nil
}

// generateAuditSummary - 生成可审计的 summary（不暴露隐藏 CoT）
func generateAuditSummary(query string, rounds []ReflectionRound) string {
    // 只包含：round 数、每个 round 的评分、feedback 摘要长度
    // 不包含：模型内部思考过程
    var summary strings.Builder
    summary.WriteString(fmt.Sprintf("Query: %s\n", query))
    summary.WriteString(fmt.Sprintf("Total Rounds: %d\n", len(rounds)))
    for i, r := range rounds {
        summary.WriteString(fmt.Sprintf("Round %d: score=%.2f, feedback_len=%d\n",
            i+1, r.Score, len(r.Feedback)))
    }
    return summary.String()
}
```

### 5.5 成功路径

```
ReflectionWorkflow(query, draft_ref)
  → for round in [0, max_rounds):
      → EvaluateDraftActivity（返回 critique_ref）
      → 如果 score >= confidence_threshold：
          → 生成 audit summary
          → return final_result_ref
      → 否则：
          → ReviseDraftActivity（返回 revised_draft_ref）
          → 继续下一轮
  → return final_result_ref（即使未达标）
```

### 5.6 失败路径

```
EvaluateDraftActivity 失败 → 返回当前 draft（不阻塞）
ReviseDraftActivity 失败 → 返回当前 draft
Timeout → 返回当前 draft
Audit 写入失败 → 记录 warning，继续
```

### 5.7 验收标准

| 验收项 | 通过条件 |
|--------|----------|
| 多轮 Reflection | 支持 1-N 轮 reflection |
| Confidence threshold | 达标后停止，不继续无谓迭代 |
| Audit summary | 不暴露模型隐藏 CoT |
| Mock LLM | 默认 mock，不进默认 CI |
| Real LLM（可选） | REAL_LLM_TEST=1 时可跑 |
| Max rounds | 防止无限迭代 |
| Rubric | 支持自定义评分标准 |

### 5.8 Shannon 参考点

| 功能 | Shannon 实现路径 | Cribug Phase 7B 实现 |
|------|----------------|---------------------|
| Reflection Config | `patterns/reflection.go:ReflectionConfig` | `ReflectionConfig` |
| ReflectOnResult | `patterns/reflection.go:ReflectOnResult` | `ReflectionProductionWorkflow` |
| EvaluateResult | `activities/evaluate.go:EvaluateResult` | `EvaluateDraftActivity` |
| Evaluation API | `llm_service/api/evaluate.py` | Python LLM Service |
| shouldReflect | `strategies/strategy_helpers.go:shouldReflect` | Activity 内判断 |
| Criteria | `api/evaluate.py:EvaluationRequest` | `ReflectionConfig.Criteria` |

### 5.9 Cribug Lite 实现边界

- **不做**：保存完整 CoT（只保存 audit summary）
- **不做**：与 ReAct 深度耦合（Reflection 可独立使用）
- **不做**：复杂的 rubric hierarchy（只做 flat criteria）
- Real LLM reflection 只做可选 smoke，不进默认 CI

---

## 五、Phase 7D Slice 26：Tree-of-Thoughts

### 6.1 核心架构澄清

Tree-of-Thoughts 提供多候选路径搜索、评分、剪枝、best path selection 能力。是 bounded search，不能无限展开。

**参考 Shannon 实现**：
- `go/orchestrator/internal/workflows/patterns/tree_of_thoughts.go` - `TreeOfThoughtsConfig`、`ThoughtNode`、`TreeOfThoughtsResult`、`TreeOfThoughts`

**关键约束**：
- ToT 是 bounded search，`max_depth`、`max_branching_factor`、`max_total_nodes`、`tot_token_budget` 必须配置
- Thought 大文本用 ref，不进入 Workflow history
- `pruning_threshold` 以下的分支必须剪枝
- 默认 mock LLM 测试，Real LLM ToT 只做手动 smoke 并设置成本上限

### 6.2 数据结构

```go
// TreeOfThoughtsConfig - ToT 配置
type TreeOfThoughtsConfig struct {
    MaxDepth           int      `json:"max_depth"`            // 最大深度，默认 5
    BranchingFactor    int      `json:"branching_factor"`     // 每层最大分支，默认 3
    MaxTotalNodes      int      `json:"max_total_nodes"`     // 最大总节点数，默认 50
    TokenBudget        int      `json:"token_budget"`        // 最大 token 预算，默认 5000
    PruningThreshold  float64  `json:"pruning_threshold"`   // 剪枝阈值，默认 0.3
    EvaluationMethod  string   `json:"evaluation_method"`   // "scoring" | "voting" | "llm"
    BacktrackEnabled  bool     `json:"backtrack_enabled"`   // 是否允许回溯，默认 false
    ModelTier         string   `json:"model_tier"`           // "small" | "medium"
}

// ThoughtNode - 思维节点（Workflow history 只保存 metadata ref，不保存完整 thought 文本）
type ThoughtNode struct {
    ID           string  `json:"id"`
    ParentID     string  `json:"parent_id"`          // 父节点 ID
    Depth        int     `json:"depth"`              // 深度
    Score        float64 `json:"score"`              // 评分
    Status       string  `json:"status"`            // "active" | "pruned" | "terminal"
    ThoughtRef   string  `json:"thought_ref"`        // 完整 thought 内容的 Workspace ref
    Summary      string  `json:"summary"`            // 短摘要（用于 Workflow 内传递；不存完整 thought）
    Children     []string `json:"children"`         // 子节点 IDs
    TokensUsed   int     `json:"tokens_used"`        // 本节点 token 消耗
    IsTerminal   bool    `json:"is_terminal"`       // 是否是终止节点
    Explanation  string  `json:"explanation"`      // 评分解释
}

// ToTResult - ToT 结果（Workflow history 只保存 path refs，不保存完整 thought tree）
type ToTResult struct {
    BestPath           []string `json:"best_path"`            // 最佳路径（节点 IDs）
    SolutionRef        string   `json:"solution_ref"`         // 最终答案的 Workspace ref
    TotalThoughts      int      `json:"total_thoughts"`       // 总节点数
    TreeDepth          int      `json:"tree_depth"`           // 实际树深度
    TotalTokens        int      `json:"total_tokens"`         // 总 token 消耗
    ExplorationTreeRef string   `json:"exploration_tree_ref"` // 探索树摘要的 Workspace ref
    Confidence         float64  `json:"confidence"`           // 最终置信度
    PrunedCount        int      `json:"pruned_count"`         // 被剪枝的节点数
}

// ToTWorkflowInput - ToT Workflow 输入
type ToTWorkflowInput struct {
    Query       string                 `json:"query"`
    Context     map[string]interface{} `json:"context"`
    Config      TreeOfThoughtsConfig   `json:"config"`
}
```

### 6.3 Activity 设计

```go
// GenerateThoughtsActivity - 生成候选 thoughts（分支扩展）
type GenerateThoughtsInput struct {
    ParentThoughtRef string   `json:"parent_thought_ref"` // 父节点 thought 的 Workspace ref
    ParentID         string   `json:"parent_id"`
    Depth            int     `json:"depth"`
    Count            int     `json:"count"`           // 生成多少个分支
    ModelTier        string   `json:"model_tier"`
    MockLLM          bool    `json:"mock_llm"`        // 默认 true
}

type GenerateThoughtsResult struct {
    Thoughts    []ThoughtNode `json:"thoughts"`  // 只有 metadata + ref，不含完整文本
    TokensUsed  int           `json:"tokens_used"`
}

// ScoreThoughtActivity - 评分单个 thought
type ScoreThoughtInput struct {
    ThoughtRef  string  `json:"thought_ref"`  // Workspace ref（不传完整 thought）
    ParentScore float64 `json:"parent_score"`
    Depth       int     `json:"depth"`
    Method     string  `json:"method"`   // "scoring" | "voting" | "llm"
    MockLLM    bool    `json:"mock_llm"` // 默认 true
}

type ScoreThoughtResult struct {
    Score       float64 `json:"score"`
    Explanation string  `json:"explanation"`
    TokensUsed  int     `json:"tokens_used"`
}

// PruneThoughtsActivity - 剪枝低分分支
type PruneThoughtsInput struct {
    Nodes   []ThoughtNode `json:"nodes"`  // 只有 metadata
    Threshold float64   `json:"threshold"`
}

type PruneThoughtsResult struct {
    RetainedNodes []ThoughtNode `json:"retained_nodes"`
    PrunedCount   int           `json:"pruned_count"`
}

// FindBestPathActivity - 找到最佳路径（输入 tree_id，从 Workspace 读取 node metadata）
type FindBestPathInput struct {
    TreeID string         `json:"tree_id"` // Workflow 从 WorkspaceList 获取
    Nodes  []ThoughtNode  `json:"nodes"`   // 当前 BFS 已展开的节点（Workflow 内传递）
}

type FindBestPathResult struct {
    BestPath    []string `json:"best_path"`     // 节点 IDs
    BestPathRef string   `json:"best_path_ref"`  // 最佳路径内容的 Workspace ref
    Confidence  float64  `json:"confidence"`
}

// SynthesizeToTResultActivity - 综合 ToT 结果
type SynthesizeToTResultInput struct {
    BestPathRef string   `json:"best_path_ref"` // 从 Workspace 读取
    Query       string   `json:"query"`
    ModelTier   string   `json:"model_tier"`
    MockLLM     bool     `json:"mock_llm"` // 默认 true
}

type SynthesizeToTResultResult struct {
    SolutionRef string   `json:"solution_ref"` // 最终答案的 Workspace ref
    TokensUsed  int      `json:"tokens_used"`
}
```

> **反模式已删除**：不再定义 `ToTWorkflowActivity`。Tree-of-Thoughts 的编排是 `TreeOfThoughtsWorkflow` 本身，不存在把整个 ToT 流程塞进一个 Activity 的"Activity 包 workflow"反模式。如果需要 re-entry / external trigger，使用 `ToTWorkflowInput` / `ToTResult` 即可。

### 6.4 Workflow 设计（ToT）

```go
// TreeOfThoughtsWorkflow - ToT Workflow
// 约束：
//   1. Workflow history 不存完整 thought；只存 ThoughtNode 的 summary + ref
//   2. 完整 thought 文本由 GenerateThoughtsActivity 写入 Workspace（topic=workflow:{id}:tot:thoughts:{node_id}）
//   3. 不引用未定义字段（无 Thought / WorkspaceRef；统一用 ThoughtRef / Summary）
func TreeOfThoughtsWorkflow(ctx workflow.Context, input ToTWorkflowInput) (ToTResult, error) {
    workflowID := workflow.GetInfo(ctx).WorkflowExecution.ID
    config := input.Config

    // 初始化根节点（Summary 用 query 的短摘要；完整 query 上下文走 input.Context）
    root := ThoughtNode{
        ID:         "root",
        ParentID:   "",
        Score:      1.0,
        Depth:      0,
        IsTerminal: false,
        Summary:    truncate(input.Query, 200),
    }
    allNodes := []ThoughtNode{root}
    nodeMap := map[string]*ThoughtNode{"root": &root}
    totalTokens := 0
    prunedCount := 0

    // BFS 展开
    for depth := 0; depth < config.MaxDepth; depth++ {
        // 获取当前深度的所有节点
        currentNodes := getNodesAtDepth(allNodes, depth)
        if len(currentNodes) == 0 {
            break
        }

        // 检查 token budget
        if totalTokens >= config.TokenBudget {
            break
        }

        // 生成子节点
        var nextNodes []ThoughtNode
        for _, node := range currentNodes {
            // 检查是否终止
            if node.IsTerminal {
                continue
            }

            // 检查 max_total_nodes
            if len(allNodes) >= config.MaxTotalNodes {
                break
            }

            // 生成分支：父节点用 ref 传（Activity 从 Workspace 读完整 thought）
            var genResult GenerateThoughtsResult
            err := workflow.ExecuteActivity(ctx, "GenerateThoughtsActivity",
                GenerateThoughtsInput{
                    ParentThoughtRef: node.ThoughtRef, // 父节点 thought 的 workspace ref
                    ParentID:         node.ID,
                    Depth:            depth + 1,
                    Count:            config.BranchingFactor,
                    ModelTier:        config.ModelTier,
                    MockLLM:          input.Config.MockLLM,
                },
            ).Get(ctx, &genResult)
            if err != nil {
                continue
            }

            totalTokens += genResult.TokensUsed

            for _, thought := range genResult.Thoughts {
                // 评分：传 ref，不传完整 thought
                var scoreResult ScoreThoughtResult
                err := workflow.ExecuteActivity(ctx, "ScoreThoughtActivity",
                    ScoreThoughtInput{
                        ThoughtRef:  thought.ThoughtRef,
                        ParentScore: node.Score,
                        Depth:       thought.Depth,
                        Method:      config.EvaluationMethod,
                        MockLLM:     input.Config.MockLLM,
                    },
                ).Get(ctx, &scoreResult)
                if err != nil {
                    continue
                }

                totalTokens += scoreResult.TokensUsed

                // 剪枝检查
                if scoreResult.Score < config.PruningThreshold {
                    prunedCount++
                    continue
                }

                thought.Score = scoreResult.Score
                thought.Explanation = scoreResult.Explanation

                // 写入 Workspace（完整 thought 已经在 GenerateThoughtsActivity 落库；这里只 append metadata）
                _ = workflow.ExecuteActivity(ctx, "WorkspaceAppend",
                    WorkspaceAppendInput{
                        WorkflowID: workflowID,
                        Topic:      fmt.Sprintf("tot:%s:thoughts", workflowID[:8]),
                        Entry: map[string]interface{}{
                            "type":        "thought",
                            "id":          thought.ID,
                            "parent_id":   thought.ParentID,
                            "score":       thought.Score,
                            "depth":       thought.Depth,
                            "thought_ref": thought.ThoughtRef,
                            "summary":     thought.Summary,
                        },
                        Timestamp: workflow.Now(ctx),
                    },
                )

                // 更新父子关系
                if parent, ok := nodeMap[node.ID]; ok {
                    parent.Children = append(parent.Children, thought.ID)
                }

                nextNodes = append(nextNodes, thought)
                allNodes = append(allNodes, thought)
                nodeMap[thought.ID] = &allNodes[len(allNodes)-1]
            }
        }

        if len(nextNodes) == 0 {
            break
        }
    }

    // 找最佳路径（Activity 从 Workflow 接收 nodes 列表，不依赖隐式全局状态）
    var bestPathResult FindBestPathResult
    err := workflow.ExecuteActivity(ctx, "FindBestPathActivity",
        FindBestPathInput{
            TreeID: fmt.Sprintf("tot:%s:tree", workflowID[:8]),
            Nodes:  allNodes,
        },
    ).Get(ctx, &bestPathResult)
    if err != nil {
        return ToTResult{}, err
    }

    // 综合最佳路径
    var synthResult SynthesizeToTResultResult
    err = workflow.ExecuteActivity(ctx, "SynthesizeToTResultActivity",
        SynthesizeToTResultInput{
            BestPathRef: bestPathResult.BestPathRef, // Workspace ref
            Query:       input.Query,
            ModelTier:   config.ModelTier,
            MockLLM:     input.Config.MockLLM,
        },
    ).Get(ctx, &synthResult)
    if err != nil {
        return ToTResult{}, err
    }

    return ToTResult{
        BestPath:           bestPathResult.BestPath,
        SolutionRef:        synthResult.SolutionRef,
        TotalThoughts:      len(allNodes),
        TreeDepth:          getMaxDepth(allNodes),
        TotalTokens:        totalTokens,
        ExplorationTreeRef: fmt.Sprintf("tot:%s:tree", workflowID[:8]),
        Confidence:         bestPathResult.Confidence,
        PrunedCount:        prunedCount,
    }, nil
}
```

### 6.5 成功路径

```
TreeOfThoughtsWorkflow(query)
  → 初始化根节点
  → for depth in [0, max_depth):
      → for each node at depth:
          → GenerateThoughtsActivity（生成分支）
          → ScoreThoughtActivity（评分）
          → 如果 score < pruning_threshold：剪枝
          → 否则：写入 Workspace（ref）
          → 更新父子关系
      → 如果没有更多节点：break
  → FindBestPathActivity（找最佳路径）
  → SynthesizeToTResultActivity（综合结果）
  → return ToTResult
```

### 6.6 失败路径

```
GenerateThoughtsActivity 失败 → 该分支跳过
ScoreThoughtActivity 失败 → 该分支跳过
Token budget 超限 → 停止展开
Max total nodes 超限 → 停止展开
FindBestPathActivity 失败 → 返回 error
SynthesizeToTResultActivity 失败 → 返回 error
```

### 6.7 验收标准

| 验收项 | 通过条件 |
|--------|----------|
| Bounded search | max_depth/max_total_nodes/token_budget 生效 |
| 分支生成 | branching_factor 控制每层分支数 |
| 评分 | evaluation_method 正确执行 |
| 剪枝 | pruning_threshold 以下分支被剪枝 |
| Workspace 写入 | thought 内容写入 Workspace ref |
| Best path | 正确找到并返回最佳路径 |
| Mock LLM | 默认 mock，不进默认 CI |
| Real LLM（可选） | REAL_TOT_TEST=1 时可跑（成本上限 1 美元） |

### 6.8 Shannon 参考点

| 功能 | Shannon 实现路径 | Cribug Phase 7C 实现 |
|------|----------------|---------------------|
| ToT Config | `patterns/tree_of_thoughts.go:TreeOfThoughtsConfig` | `TreeOfThoughtsConfig` |
| ThoughtNode | `patterns/tree_of_thoughts.go:ThoughtNode` | `ThoughtNode` |
| TreeOfThoughtsResult | `patterns/tree_of_thoughts.go:TreeOfThoughtsResult` | `ToTResult` |
| TreeOfThoughts() | `patterns/tree_of_thoughts.go:TreeOfThoughts` | `TreeOfThoughtsWorkflow` |
| generateBranches | `patterns/tree_of_thoughts.go:generateBranches` | `GenerateThoughtsActivity` |
| evaluateThought | `patterns/tree_of_thoughts.go:evaluateThought` | `ScoreThoughtActivity` |
| findBestPath | `patterns/tree_of_thoughts.go:findBestPath` | `FindBestPathActivity` |

### 6.9 Cribug Lite 实现边界

- **不做**：backtrack 回溯（config.BacktrackEnabled = false）
- **不做**：无限展开（必须配置 max_depth/max_total_nodes/token_budget）
- Real LLM ToT 只做可选 smoke，并设置 `REAL_TOT_TEST_MAX_COST_USD=1`

---

## 六、Phase 7E Slice 27：Debate Mode

### 6.1 核心架构澄清

Debate Mode 提供 Pro Agent / Con Agent / Judge Agent 多 Agent 辩论能力，Workspace 记录 append-only transcript，Judge 仲裁并给出最终裁决。

**参考 Shannon 实现**：
- `go/orchestrator/internal/workflows/patterns/debate.go` - `DebateConfig`、`DebateResult`、`DebatePosition`、`Debate`
- `python/llm-service/llm_service/roles/trading/bull_researcher.py` / `bear_researcher.py` - Pro/Con Agent

**关键约束**：
- Debate 复用 Phase 5 Swarm / Workspace / Security
- Debate transcript 必须写 Workspace（append-only）
- Judge 必须读取 WorkspaceList，不直接依赖内存数组
- Debate 不能无限轮次，必须有 `max_rounds` / `debate_round_timeout_seconds`

### 6.2 数据结构

```go
// DebateConfig - Debate 配置
type DebateConfig struct {
    NumDebaters       int      `json:"num_debaters"`        // 默认 2（Pro + Con）
    MaxRounds         int      `json:"max_rounds"`         // 默认 5
    Perspectives      []string `json:"perspectives"`       // 默认 ["pro", "con"]
    RequireConsensus  bool     `json:"require_consensus"`  // 是否需要共识
    ModeratorEnabled  bool     `json:"moderator_enabled"`  // 是否有 Judge，默认 true
    VotingEnabled     bool     `json:"voting_enabled"`     // 是否投票，默认 true
    ModelTier         string   `json:"model_tier"`         // "small" | "medium"
    RoundTimeoutSecs  int      `json:"round_timeout_secs"` // 每轮超时，默认 60
    MockLLM           bool     `json:"mock_llm"`           // 默认 true；CI 用 mock 分类器 / mock judge
}

// DebatePosition - Agent 立场（Workflow history 只保存 turn metadata + content ref，不保存完整 arguments）
type DebatePosition struct {
    TurnID     string   `json:"turn_id"`      // 本轮 turn ID
    AgentID    string   `json:"agent_id"`     // "pro" | "con"
    Position   string   `json:"position"`     // "pro" | "con"
    ContentRef string   `json:"content_ref"`  // 论点内容的 Workspace ref
    Score      float64  `json:"score"`        // Judge 评分
    Confidence float64   `json:"confidence"`   // 置信度
    TokensUsed int      `json:"tokens_used"`  // token 消耗
}

// DebateRound - 单轮辩论（Workflow history 只保存 metadata ref，不保存完整 transcript）
type DebateRound struct {
    RoundIndex   int      `json:"round_index"`
    ProTurnRef   string   `json:"pro_turn_ref"`   // Pro 论点的 Workspace ref
    ConTurnRef   string   `json:"con_turn_ref"`   // Con 论点的 Workspace ref
    JudgeScore  float64  `json:"judge_score"`   // Judge 评分
    TokensUsed  int      `json:"tokens_used"`
    Timestamp   time.Time `json:"timestamp"`
}

// DebateResult - 辩论结果（Workflow history 只保存 result metadata + verdict ref，不保存完整 transcript）
type DebateResult struct {
    FinalPosition    string   `json:"final_position"`    // "pro" | "con" | "tie"
    TranscriptRef    string   `json:"transcript_ref"`    // 完整 transcript 的 Workspace ref
    ConsensusReached bool     `json:"consensus_reached"` // 是否达成共识
    TotalTokens      int      `json:"total_tokens"`
    Rounds           int      `json:"rounds"`           // 实际辩论轮次
    WinningTurnRef   string   `json:"winning_turn_ref"`  // 胜出论点的 Workspace ref
    VerdictRef       string   `json:"verdict_ref"`       // Judge 最终裁决的 Workspace ref
    WorkspaceTopic   string   `json:"workspace_topic"`   // transcript topic ref
}

// DebateWorkflowInput - Debate Workflow 输入
type DebateWorkflowInput struct {
    Query       string                `json:"query"`
    Context     map[string]interface{} `json:"context"`
    Config      DebateConfig          `json:"config"`
}
```

### 6.3 Activity 设计

```go
// GenerateArgumentsActivity - Agent 生成论点
type GenerateArgumentsInput struct {
    Position     string   `json:"position"`    // "pro" | "con"
    Query        string   `json:"query"`
    RoundIndex   int      `json:"round_index"`
    PreviousTurnRef string `json:"previous_turn_ref"` // 对方上一轮论点的 Workspace ref
    ModelTier    string   `json:"model_tier"`
    MockLLM      bool     `json:"mock_llm"`   // 默认 true
}

type GenerateArgumentsResult struct {
    ContentRef  string   `json:"content_ref"`   // 论点内容的 Workspace ref
    Score       float64  `json:"score"`         // 自评分
    Confidence  float64   `json:"confidence"`     // 置信度
    TokensUsed  int      `json:"tokens_used"`
}

// JudgeDebateActivity - Judge 评分（从 Workspace 读取 transcript，不依赖内存数组）
type JudgeDebateInput struct {
    Query         string `json:"query"`
    TranscriptRef string `json:"transcript_ref"` // Workspace ref
    RoundIndex    int    `json:"round_index"`
    MockLLM       bool   `json:"mock_llm"` // 默认 true
}

type JudgeDebateResult struct {
    ProScore     float64 `json:"pro_score"`
    ConScore     float64 `json:"con_score"`
    Verdict      string  `json:"verdict"`      // "pro" | "con" | "tie"
    VerdictRef   string  `json:"verdict_ref"`  // Judge reasoning 的 Workspace ref
    TokensUsed   int     `json:"tokens_used"`
}

// VoteDebateActivity - 投票（可选）
type VoteDebateInput struct {
    TranscriptRef string `json:"transcript_ref"` // 从 WorkspaceList 读取
}

type VoteDebateResult struct {
    Votes map[string]int `json:"votes"`
}

// CheckConsensusActivity - 检查是否达成共识（从 Workspace 读取）
type CheckConsensusInput struct {
    TranscriptRef string  `json:"transcript_ref"`
    Threshold     float64  `json:"threshold"` // 置信度阈值
}

type CheckConsensusResult struct {
    Consensus bool     `json:"consensus"`
    Winner    string   `json:"winner"`
}
```

> **反模式已删除**：不再定义 `DebateWorkflowActivity` / `DebateWorkflowActivityInput` / `DebateWorkflowActivityResult`。Debate 的编排是 `DebateWorkflow` 本身，不存在把整个 Debate 流程塞进一个 Activity 的反模式。如果需要 re-entry / external trigger，使用 `DebateWorkflowInput` / `DebateResult` 即可。

### 6.4 Workflow 设计（Debate）

```go
// DebateWorkflow - Debate Workflow
// 约束：Workflow 只保存 turn metadata + content ref，不保存完整 arguments
// Judge 必须从 Workspace 读取 transcript，不直接依赖内存数组
func DebateWorkflow(ctx workflow.Context, input DebateWorkflowInput) (DebateResult, error) {
    workflowID := workflow.GetInfo(ctx).WorkflowExecution.ID
    config := input.Config

    workspaceTopic := fmt.Sprintf("debate:%s:transcript:%s", input.Query[:20], workflowID[:8])
    totalTokens := 0
    maxRounds := config.MaxRounds
    roundCount := 0

    for round := 0; round < maxRounds; round++ {
        roundStart := workflow.Now(ctx)

        // Step 1: Pro Agent 生成论点
        var proResult GenerateArgumentsResult
        proCtx := withRoundTimeout(ctx, config.RoundTimeoutSecs)
        err := workflow.ExecuteActivity(proCtx, "GenerateArgumentsActivity",
            GenerateArgumentsInput{
                Position:    "pro",
                Query:       input.Query,
                RoundIndex:  round,
                ModelTier:   config.ModelTier,
                MockLLM:     input.Config.MockLLM,
            },
        ).Get(ctx, &proResult)
        if err != nil {
            // Pro 失败，记录并继续
            _ = appendDebateRoundError(ctx, workspaceTopic, round, "pro_error", err.Error())
        }

        // Step 2: Con Agent 生成论点（使用 Pro 的 turn ref 作为 previous）
        var conResult GenerateArgumentsResult
        conCtx := withRoundTimeout(ctx, config.RoundTimeoutSecs)
        err = workflow.ExecuteActivity(conCtx, "GenerateArgumentsActivity",
            GenerateArgumentsInput{
                Position:     "con",
                Query:        input.Query,
                RoundIndex:   round,
                PreviousTurnRef: proResult.ContentRef,
                ModelTier:    config.ModelTier,
                MockLLM:      input.Config.MockLLM,
            },
        ).Get(ctx, &conResult)
        if err != nil {
            _ = appendDebateRoundError(ctx, workspaceTopic, round, "con_error", err.Error())
        }

        totalTokens += proResult.TokensUsed + conResult.TokensUsed

        // Step 3: 记录 round metadata 到 Workspace（只有 turn refs，不是完整 arguments）
        _ = workflow.ExecuteActivity(ctx, "WorkspaceAppend",
            WorkspaceAppendInput{
                WorkflowID: workflowID,
                Topic:      workspaceTopic,
                Entry: map[string]interface{}{
                    "type":        "debate_round",
                    "round_index": round,
                    "pro_turn_ref": proResult.ContentRef,
                    "con_turn_ref": conResult.ContentRef,
                    "pro_score":   proResult.Score,
                    "con_score":   conResult.Score,
                    "timestamp":   roundStart,
                },
                Timestamp: roundStart,
            },
        )

        // Step 4: Judge 评分（如果启用，从 Workspace 读取 transcript）
        if config.ModeratorEnabled {
            var judgeResult JudgeDebateResult
            err = workflow.ExecuteActivity(ctx, "JudgeDebateActivity",
                JudgeDebateInput{
                    Query:         input.Query,
                    TranscriptRef: workspaceTopic, // Workspace ref
                    RoundIndex:    round,
                    MockLLM:       input.Config.MockLLM,
                },
            ).Get(ctx, &judgeResult)
            if err == nil {
                totalTokens += judgeResult.TokensUsed
                roundCount = round + 1

                // 记录 Judge 评分到 Workspace
                _ = workflow.ExecuteActivity(ctx, "WorkspaceAppend",
                    WorkspaceAppendInput{
                        WorkflowID: workflowID,
                        Topic:      workspaceTopic,
                        Entry: map[string]interface{}{
                            "type":        "judge_score",
                            "round_index": round,
                            "pro_score":   judgeResult.ProScore,
                            "con_score":   judgeResult.ConScore,
                            "verdict":     judgeResult.Verdict,
                            "verdict_ref": judgeResult.VerdictRef,
                        },
                        Timestamp: workflow.Now(ctx),
                    },
                )

                // 检查是否需要提前结束
                if config.RequireConsensus && judgeResult.Verdict != "tie" {
                    // 共识达成，结束辩论
                    return DebateResult{
                        FinalPosition:    judgeResult.Verdict,
                        TranscriptRef:   workspaceTopic,
                        ConsensusReached: true,
                        TotalTokens:      totalTokens,
                        Rounds:          roundCount,
                        WinningTurnRef:   proResult.ContentRef,
                        VerdictRef:      judgeResult.VerdictRef,
                        WorkspaceTopic:  workspaceTopic,
                    }, nil
                }
            }
        }
    }

    // 超出最大轮次，进行最终裁决（从 Workspace 读取 transcript）
    var judgeResult JudgeDebateResult
    err := workflow.ExecuteActivity(ctx, "JudgeDebateActivity",
        JudgeDebateInput{
            Query:         input.Query,
            TranscriptRef: workspaceTopic,
            RoundIndex:    maxRounds,
            MockLLM:       input.Config.MockLLM,
        },
    ).Get(ctx, &judgeResult)
    if err != nil {
        return DebateResult{}, err
    }

    return DebateResult{
        FinalPosition:    judgeResult.Verdict,
        TranscriptRef:   workspaceTopic,
        ConsensusReached: judgeResult.Verdict != "tie",
        TotalTokens:      totalTokens,
        Rounds:           roundCount,
        WinningTurnRef:   "",
        VerdictRef:       judgeResult.VerdictRef,
        WorkspaceTopic:   workspaceTopic,
    }, nil
}
```
```

### 6.5 成功路径

```
DebateWorkflow(query)
  → for round in [0, max_rounds):
      → GenerateArgumentsActivity (Pro)
      → GenerateArgumentsActivity (Con)
      → WorkspaceAppend (transcript)
      → JudgeDebateActivity（评分）
      → WorkspaceAppend (judge score)
      → 如果 RequireConsensus && verdict != "tie"：
          → return DebateResult
  → 超出最大轮次 → 最终 Judge 裁决
  → return DebateResult
```

### 6.6 失败路径

```
Pro Agent 失败 → 记录 error，继续
Con Agent 失败 → 记录 error，继续
Judge 失败 → 返回 error
Timeout → 停止该轮，进入下一轮
WorkspaceAppend 失败 → 记录 warning，继续
```

### 6.7 验收标准

| 验收项 | 通过条件 |
|--------|----------|
| Pro/Con 生成 | Pro/Con Agent 能生成论点 |
| Workspace 记录 | debate transcript 写入 Workspace |
| Judge 评分 | Judge 能评分每轮 |
| 轮次限制 | max_rounds 生效 |
| 共识检测 | RequireConsensus 时提前结束 |
| 投票 | VotingEnabled 时统计票数 |
| Mock LLM | 默认 mock，不进默认 CI |
| Real LLM（可选） | REAL_DEBATE_TEST=1 时可跑 |

### 6.8 Shannon 参考点

| 功能 | Shannon 实现路径 | Cribug Phase 7E 实现 |
|------|----------------|---------------------|
| Debate Config | `patterns/debate.go:DebateConfig` | `DebateConfig` |
| DebateResult | `patterns/debate.go:DebateResult` | `DebateResult` |
| DebatePosition | `patterns/debate.go:DebatePosition` | `DebatePosition` |
| Debate() | `patterns/debate.go:Debate` | `DebateWorkflow` |
| generateDefaultPerspectives | `patterns/debate.go:generateDefaultPerspectives` | 默认 ["pro", "con"] |
| checkConsensus | `patterns/debate.go:checkConsensus` | `CheckConsensusActivity` |
| moderateDebate | `patterns/debate.go:moderateDebate` | `JudgeDebateActivity` |
| Bull/Bear Researcher | `roles/trading/bull_researcher.py` | Pro/Con Agent activity |

### 6.9 Cribug Lite 实现边界

- **不做**：复杂的多 Agent 辩论（只做 Pro + Con + Judge）
- **不做**：Real-time debate UI（只做最终 transcript）
- Real LLM debate 只做可选 smoke，不进默认 CI

---

## 七、Phase 7F Slice 28：Research-Synthesis v2

### 7.1 核心架构澄清

Research-Synthesis v2 在 v1 基础上升级：multi-source evidence、evidence credibility scoring、contradiction detection、citation chain、source attribution、multi-round research loop、structured research report、optional reflection/debate before final synthesis、optional approval before publish。

**参考 Shannon 实现**：
- `go/orchestrator/internal/workflows/strategies/research.go` - `ResearchWorkflow`（深度研究）
- `go/orchestrator/internal/metadata/citations.go` - `Citation`、`CitationStats`、`CredibilityConfig`
- `go/orchestrator/internal/activities/citation_agent.go` - `AddCitations`、`CitationAgentInput`
- `config/source_types.yaml` - Source type 配置
- `config/research_strategies.yaml` - Research strategy 配置
- `python/llm-service/llm_service/roles/deep_research/deep_research_agent.py` - `DEEP_RESEARCH_AGENT_PRESET`

**重要区分**：
- **v2 不是简单 RAG answer**：必须有 evidence provenance、contradiction metadata、citation chain
- **v2 输出 report ref**：大报告不塞进 Workflow history
- **如果启用 HITL**：发布前必须进入 Approval

### 7.2 数据结构

```go
// SourceCredibility - 来源可信度
type SourceCredibility struct {
    SourceType   string  `json:"source_type"`  // "official" | "news" | "academic" | "github" | "financial"
    Domain       string  `json:"domain"`       // e.g., ".edu", ".gov"
    CredibilityScore float64 `json:"credibility_score"` // 0.0 - 1.0
    QualityScore float64  `json:"quality_score"`  // 0.0 - 1.0
}

// Evidence - 证据（v2 增强）
type Evidence struct {
    ID              string                 `json:"id"`
    SubqueryID      string                 `json:"subquery_id"`
    SourceID        string                 `json:"source_id"`       // 来源 ID
    SourceType      string                 `json:"source_type"`     // 来源类型
    SourceCredibility SourceCredibility    `json:"source_credibility"`
    Summary         string                 `json:"summary"`          // 证据短摘要（≤500 字符；进入 Workflow history 可接受）
    ContentRef      string                 `json:"content_ref"`      // 完整证据内容 → Workspace
    CredibilityScore float64               `json:"credibility_score"` // 本条证据可信度
    IsContradiction bool                   `json:"is_contradiction"` // 是否与矛盾
    ContradictsWith []string               `json:"contradicts_with"` // 矛盾的证据 IDs
    CitationChain   []string               `json:"citation_chain"`  // 引用链路（source_id 列表）
    CitationChainRef string                `json:"citation_chain_ref"` // 完整引用链路 → Workspace（如果超长）
    RetrievedAt     time.Time              `json:"retrieved_at"`
    Metadata        map[string]interface{} `json:"metadata"`
}

// ResearchReportV2 - 研究报告（v2）
type ResearchReportV2 struct {
    QueryID              string            `json:"query_id"`
    Title                string            `json:"title"`            // 标题（短，直接存）
    ExecutiveSummary     string            `json:"executive_summary"` // 短摘要（≤2KB；超限走 ExecutiveSummaryRef）
    ExecutiveSummaryRef  string            `json:"executive_summary_ref"` // 完整 executive summary → Workspace
    Sections             []ReportSection   `json:"sections"`
    AllEvidence          []Evidence        `json:"all_evidence"`     // evidence metadata，不含完整 content（已走 ContentRef）
    Contradictions       []Contradiction   `json:"contradictions"` // 冲突 evidence 组
    ContradictionReportRef string          `json:"contradiction_report_ref"` // 完整矛盾分析报告 → Workspace
    CitationStats        CitationStats     `json:"citation_stats"`
    ConfidenceScore      float64           `json:"confidence_score"`
    TotalSources         int               `json:"total_sources"`
    TotalEvidenceItems   int               `json:"total_evidence_items"`
    ReportRef            string            `json:"report_ref"`     // 完整报告的 Workspace ref
    CreatedAt            time.Time         `json:"created_at"`
    Approved             *bool             `json:"approved,omitempty"` // 如果启用 HITL
}

// ReportSection - 报告章节
type ReportSection struct {
    Title          string     `json:"title"`
    ContentSummary string     `json:"content_summary"` // 章节短摘要 / preview（≤500 字符）
    ContentRef     string     `json:"content_ref"`     // 完整章节正文 → Workspace
    Evidence       []string   `json:"evidence"`        // evidence IDs（引用，不含完整内容）
}

// Contradiction - 冲突证据组
type Contradiction struct {
    ID              string   `json:"id"`
    EvidenceIDs     []string `json:"evidence_ids"`
    Resolution     string   `json:"resolution"`    // 如何解决冲突
    ResolutionType string   `json:"resolution_type"` // "preference" | "evidence_based" | "uncertain"
}

// ResearchV2Config - Research v2 配置
type ResearchV2Config struct {
    MaxSources              int      `json:"max_sources"`             // 最大来源数，默认 20
    MaxEvidenceItems        int      `json:"max_evidence_items"`       // 最大证据条目，默认 100
    EnableContradictionDetection bool `json:"enable_contradiction_detection"` // 默认 true
    RequireCitations       bool     `json:"require_citations"`        // 默认 true
    RequireApprovalBeforePublish bool `json:"require_approval_before_publish"` // 默认 false
    EnableReflection        bool     `json:"enable_reflection"`       // 是否启用 reflection
    EnableDebate            bool     `json:"enable_debate"`           // 是否启用 debate
    SourceTypes            []string `json:"source_types"`            // 允许的来源类型
    CredibilityThreshold   float64  `json:"credibility_threshold"`  // 可信度阈值，默认 0.5
}
```

### 7.3 Activity 设计

```go
// ScoreSourceCredibilityActivity - 评分来源可信度
type ScoreSourceCredibilityInput struct {
    SourceURL string `json:"source_url"`
    SourceType string `json:"source_type"`
}

type ScoreSourceCredibilityResult struct {
    Domain           string  `json:"domain"`
    CredibilityScore float64 `json:"credibility_score"`
    QualityScore     float64 `json:"quality_score"`
}

// RetrieveMultiSourceEvidenceActivity - 多来源证据检索
type RetrieveMultiSourceEvidenceInput struct {
    Subqueries   []ResearchSubquery `json:"subqueries"`
    SourceTypes  []string           `json:"source_types"`  // 允许的来源类型
    MaxPerSource int                `json:"max_per_source"` // 每种来源最多多少条
}

type RetrieveMultiSourceEvidenceResult struct {
    Evidence     []Evidence `json:"evidence"`
    TotalSources int        `json:"total_sources"`
}

// DetectContradictionsActivity - 检测冲突证据
type DetectContradictionsInput struct {
    Evidence []Evidence `json:"evidence"`
    Threshold float64   `json:"threshold"` // 相似度阈值
}

type DetectContradictionsResult struct {
    Contradictions []Contradiction `json:"contradictions"`
}

// BuildCitationChainActivity - 构建引用链路
type BuildCitationChainInput struct {
    Evidence []Evidence `json:"evidence"`
}

type BuildCitationChainResult struct {
    CitationChains [][]string `json:"citation_chains"` // 每条证据的引用链路
}

// GenerateReportV2Activity - 生成研究报告 v2
type GenerateReportV2Input struct {
    QueryID       string           `json:"query_id"`
    Query         string           `json:"query"`
    Evidence      []Evidence       `json:"evidence"`
    Contradictions []Contradiction `json:"contradictions"`
    Config        ResearchV2Config `json:"config"`
    MockLLM       bool             `json:"mock_llm"`  // 默认 true
}

type GenerateReportV2Result struct {
    Report       ResearchReportV2 `json:"report"`
    ReportRef   string           `json:"report_ref"`  // Workspace ref
    TokensUsed  int              `json:"tokens_used"`
}

// ReflectionBeforeSynthesisActivity - 综合前的 reflection（可选）
type ReflectionBeforeSynthesisInput struct {
    Query    string      `json:"query"`
    Evidence []Evidence  `json:"evidence"`
    MockLLM  bool       `json:"mock_llm"`
}

type ReflectionBeforeSynthesisResult struct {
    ImprovedEvidence []Evidence `json:"improved_evidence"`
    ConfidenceScore  float64    `json:"confidence_score"`
}

// DebateBeforeSynthesisActivity - 综合前的 debate（可选）
type DebateBeforeSynthesisInput struct {
    Query    string     `json:"query"`
    Evidence []Evidence `json:"evidence"`
    Config   DebateConfig `json:"config"`
}

type DebateBeforeSynthesisResult struct {
    ResolvedEvidence []Evidence `json:"resolved_evidence"`
    ResolutionNotes  string     `json:"resolution_notes"`
}

// ResearchV2WorkflowInput - Research v2 Workflow 输入（用于 Child Workflow 调用或外部 re-entry）
// 注意：Research v2 是一个 **Workflow**，不是 Activity。
// 不要使用 "ResearchV2WorkflowActivity" 这种反模式命名。
// Workflow 的编排逻辑在 ResearchSynthesisV2Workflow 中；Activity 只负责外部 IO：
//   ScoreEvidenceCredibilityActivity / DetectContradictionsActivity / BuildCitationChainActivity /
//   WriteResearchReportActivity / AuditResearchV2Activity / ReflectionBeforeSynthesisActivity /
//   DebateBeforeSynthesisActivity / RetrieveMultiSourceEvidenceActivity / GenerateReportV2Activity
type ResearchV2WorkflowInput struct {
    Query   string            `json:"query"`
    Context map[string]interface{} `json:"context"`
    Config  ResearchV2Config  `json:"config"`
}

type ResearchV2WorkflowResult struct {
    Report       ResearchReportV2 `json:"report"`
    ReportRef   string           `json:"report_ref"`
    TotalTokens int              `json:"total_tokens"`
}
```

**非目标**：
- 不重写 Phase 6F Research-Synthesis v1
- v2 是 v1 的增强，不是替代

### 7.4 Workflow 设计（Research v2）

```go
// ResearchSynthesisV2Workflow - Research v2 Workflow
func ResearchSynthesisV2Workflow(ctx workflow.Context, input ResearchV2WorkflowInput) (ResearchV2WorkflowResult, error) {
    workflowID := workflow.GetInfo(ctx).WorkflowExecution.ID
    config := input.Config

    // Step 0: 分解研究查询（复用 Phase 6F）
    var decompResult DecomposeResearchQueryResult
    err := workflow.ExecuteActivity(ctx, "DecomposeResearchQueryActivity",
        DecomposeResearchQueryInput{
            Query:        input.Query,
            MaxSubqueries: 6,
        },
    ).Get(ctx, &decompResult)
    if err != nil {
        return ResearchV2WorkflowResult{}, err
    }

    // Step 1: 多来源证据检索（Phase 6F v1 能力 + v2 增强）
    var evidenceResult RetrieveMultiSourceEvidenceResult
    err = workflow.ExecuteActivity(ctx, "RetrieveMultiSourceEvidenceActivity",
        RetrieveMultiSourceEvidenceInput{
            Subqueries:   decompResult.Subqueries,
            SourceTypes:  config.SourceTypes,
            MaxPerSource: config.MaxSources / len(config.SourceTypes) + 1,
        },
    ).Get(ctx, &evidenceResult)
    if err != nil {
        return ResearchV2WorkflowResult{}, err
    }

    // Step 2: 评分每个证据的来源可信度
    scoredEvidence := []Evidence{}
    for _, e := range evidenceResult.Evidence {
        var credResult ScoreSourceCredibilityResult
        err := workflow.ExecuteActivity(ctx, "ScoreSourceCredibilityActivity",
            ScoreSourceCredibilityInput{
                SourceURL: e.SourceID,
                SourceType: e.SourceType,
            },
        ).Get(ctx, &credResult)
        if err == nil {
            e.SourceCredibility = SourceCredibility{
                SourceType:        e.SourceType,
                Domain:           credResult.Domain,
                CredibilityScore: credResult.CredibilityScore,
                QualityScore:     credResult.QualityScore,
            }
            e.CredibilityScore = credResult.CredibilityScore
        }
        scoredEvidence = append(scoredEvidence, e)
    }

    // 过滤低可信度证据
    filteredEvidence := filterByCredibility(scoredEvidence, config.CredibilityThreshold)

    // Step 3: 矛盾检测（如果启用）
    var contradictions []Contradiction
    if config.EnableContradictionDetection {
        var detectResult DetectContradictionsResult
        err = workflow.ExecuteActivity(ctx, "DetectContradictionsActivity",
            DetectContradictionsInput{
                Evidence:  filteredEvidence,
                Threshold: 0.7,
            },
        ).Get(ctx, &detectResult)
        if err == nil {
            contradictions = detectResult.Contradictions
        }
    }

    // Step 4: 构建引用链路
    var citationResult BuildCitationChainResult
    err = workflow.ExecuteActivity(ctx, "BuildCitationChainActivity",
        BuildCitationChainInput{Evidence: filteredEvidence},
    ).Get(ctx, &citationResult)
    if err == nil {
        for i := range filteredEvidence {
            if i < len(citationResult.CitationChains) {
                filteredEvidence[i].CitationChain = citationResult.CitationChains[i]
            }
        }
    }

    // Step 5: Reflection before synthesis（如果启用）
    if config.EnableReflection {
        var reflResult ReflectionBeforeSynthesisResult
        err = workflow.ExecuteActivity(ctx, "ReflectionBeforeSynthesisActivity",
            ReflectionBeforeSynthesisInput{
                Query:    input.Query,
                Evidence: filteredEvidence,
                MockLLM:  input.Config.MockLLM,
            },
        ).Get(ctx, &reflResult)
        if err == nil {
            filteredEvidence = reflResult.ImprovedEvidence
        }
    }

    // Step 6: Debate before synthesis（如果启用）
    if config.EnableDebate {
        var debateResult DebateBeforeSynthesisResult
        err = workflow.ExecuteActivity(ctx, "DebateBeforeSynthesisActivity",
            DebateBeforeSynthesisInput{
                Query:    input.Query,
                Evidence: filteredEvidence,
                Config: DebateConfig{
                    MaxRounds:         3,
                    ModeratorEnabled:  true,
                    RequireConsensus:  true,
                    ModelTier:         "medium",
                },
            },
        ).Get(ctx, &debateResult)
        if err == nil {
            filteredEvidence = debateResult.ResolvedEvidence
            // 写入 debate resolution 到 Workspace
            _ = workflow.ExecuteActivity(ctx, "WorkspaceAppend",
                WorkspaceAppendInput{
                    WorkflowID: workflowID,
                    Topic:      fmt.Sprintf("research:%s:debate", workflowID[:8]),
                    Entry: map[string]interface{}{
                        "type":           "debate_resolution",
                        "resolution":     debateResult.ResolutionNotes,
                        "evidence_count": len(debateResult.ResolvedEvidence),
                    },
                    Timestamp: workflow.Now(ctx),
                },
            )
        }
    }

    // Step 7: 生成研究报告 v2
    var reportResult GenerateReportV2Result
    err = workflow.ExecuteActivity(ctx, "GenerateReportV2Activity",
        GenerateReportV2Input{
            QueryID:        fmt.Sprintf("research-%s", workflowID[:8]),
            Query:          input.Query,
            Evidence:       filteredEvidence,
            Contradictions: contradictions,
            Config:         config,
            MockLLM:        input.Config.MockLLM,
        },
    ).Get(ctx, &reportResult)
    if err != nil {
        return ResearchV2WorkflowResult{}, err
    }

    // Step 8: 如果启用 HITL approval before publish
    if config.RequireApprovalBeforePublish {
        // 进入 Approval 流程（类似 Phase 7A）
        var approvalResult RequestApprovalResult
        err := workflow.ExecuteActivity(ctx, "RequestApprovalActivity",
            RequestApprovalInput{
                SessionID:     input.SessionID,
                WorkflowID:     workflowID,
                RunID:         workflow.GetInfo(ctx).WorkflowExecution.RunID,
                Query:         "发布研究报告",
                ProposedAction: map[string]interface{}{
                    "action": "publish_report",
                    "report_ref": reportResult.ReportRef,
                },
                Reason:      "research_report_publish",
                RiskLevel:   "medium",
                TimeoutSeconds: 3600,
            },
        ).Get(ctx, &approvalResult)
        if err != nil {
            return ResearchV2WorkflowResult{}, err
        }

        // 等待 approval signal
        // ...（类似 Phase 7A）

        reportResult.Report.Approved = boolPtr(false) // 待审批
    }

    return ResearchV2WorkflowResult{
        Report:      reportResult.Report,
        ReportRef:  reportResult.ReportRef,
        TotalTokens: reportResult.TokensUsed,
    }, nil
}
```

### 7.5 成功路径

```
ResearchSynthesisV2Workflow(query)
  → RetrieveMultiSourceEvidenceActivity（多来源检索）
  → for each evidence:
      → ScoreSourceCredibilityActivity（来源可信度评分）
  → filterByCredibility（过滤低分证据）
  → DetectContradictionsActivity（矛盾检测）
  → BuildCitationChainActivity（引用链路）
  → 如果 EnableReflection：
      → ReflectionBeforeSynthesisActivity
  → 如果 EnableDebate：
      → DebateBeforeSynthesisActivity
      → WorkspaceAppend (debate resolution)
  → GenerateReportV2Activity（生成报告）
  → 如果 RequireApprovalBeforePublish：
      → RequestApprovalActivity
      → 等待 Signal
  → return Report + ReportRef
```

### 7.6 失败路径

```
Multi-source retrieval 失败 → 返回 error
Credibility scoring 失败 → 使用默认分数
Contradiction detection 失败 → 跳过矛盾检测
Citation chain 失败 → 继续（non-fatal）
Reflection 失败 → 继续（non-fatal）
Debate 失败 → 继续（non-fatal）
Generate report 失败 → 返回 error
Approval timeout → 返回 partial result
```

### 7.7 验收标准

| 验收项 | 通过条件 |
|--------|----------|
| Multi-source | 支持多种来源类型 |
| Credibility scoring | 每个证据有可信度评分 |
| Contradiction detection | 能检测并标记矛盾证据 |
| Citation chain | 引用链路可追溯 |
| Reflection（可选） | enable_reflection 时生效 |
| Debate（可选） | enable_debate 时生效 |
| Report ref | 大报告通过 WorkspaceRef |
| Approval（可选） | require_approval_before_publish 时生效 |
| Mock LLM | 默认 mock，不进默认 CI |
| Real LLM（可选） | REAL_RESEARCH_V2_TEST=1 时可跑 |

### 7.8 Shannon 参考点

| 功能 | Shannon 实现路径 | Cribug Phase 7F 实现 |
|------|----------------|---------------------|
| Research Workflow | `strategies/research.go:ResearchWorkflow` | `ResearchSynthesisV2Workflow` |
| Citation | `metadata/citations.go:Citation` | `Citation` (沿用) |
| Citation Agent | `activities/citation_agent.go` | `AddCitations` (沿用) |
| Source Types | `config/source_types.yaml` | `SourceTypes` |
| Research Strategies | `config/research_strategies.yaml` | `ResearchV2Config` |
| Coverage Evaluator | `activities/coverage_evaluator.go` | `ReflectionBeforeSynthesisActivity` |
| Deep Research Agent | `roles/deep_research/deep_research_agent.py` | Python LLM Service |
| Credibility Config | `metadata/citations.go:CredibilityConfig` | `SourceCredibility` |

### 7.9 Cribug Lite 实现边界

- **不做**：实时矛盾更新（只做一次性检测）
- **不做**：复杂 citation graph 可视化
- **不做**：企业级 citation style（APA/MLA/Chicago）
- Real LLM Research v2 只做可选 smoke，不进默认 CI

---

## 八、Redis / Postgres / Workspace / 其他数据结构总结

### 8.1 Redis 数据结构（Phase 7 新增）

```
# Advanced Router（workflow 前缀）
wf:{workflow_id}:router:policy_trace    → String（长 policy trace / 分类器原文 → workspace）
wf:{workflow_id}:router:decision        → Hash（RoutingDecision 摘要）
wf:{workflow_id}:router:events          → Stream（ROUTING_DECIDED / ROUTING_DISPATCHED / ROUTING_APPROVAL_REQUIRED）

# Approval tracking（workflow 前缀）
wf:{workflow_id}:approval:{approval_id}  → Hash（ApprovalRequest）

# HITL control state（workflow 前缀）
wf:{workflow_id}:control:state            → Hash（WorkflowControlState）

# Reflection rounds（workflow 前缀）
wf:{workflow_id}:reflection:rounds       → List（ReflectionRound 摘要；完整 draft / critique / revision 走 workspace ref）

# ToT nodes（workflow 前缀）
wf:{workflow_id}:tot:nodes               → Hash（node_id → ThoughtNode 摘要，summary + ref）

# ToT workspace refs
wf:{workflow_id}:tot:thoughts:{node_id}  → String（完整 thought 内容的 workspace ref）

# Debate transcript（workflow 前缀）
wf:{workflow_id}:debate:transcript:{round} → String（transcript workspace ref）
wf:{workflow_id}:debate:turns:{turn_id}  → String（单 turn 论点 workspace ref）

# Research v2 evidence（workflow 前缀）
wf:{workflow_id}:research:evidence       → List（summary + content_ref）
wf:{workflow_id}:research:report         → String（report workspace ref）
```

### 8.2 Postgres Schema（Phase 7 新增）

```sql
-- Approvals (Phase 7A)
CREATE TABLE approvals (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    approval_id VARCHAR(100) NOT NULL UNIQUE,
    workflow_id VARCHAR(100) NOT NULL,
    run_id VARCHAR(100),
    query TEXT NOT NULL,
    proposed_action JSONB,
    reason TEXT,
    risk_level VARCHAR(50) NOT NULL DEFAULT 'medium',
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    approved BOOLEAN,
    feedback TEXT,
    modified_action JSONB,
    approved_by VARCHAR(100),
    requested_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    expires_at TIMESTAMP WITH TIME ZONE,
    responded_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Approval Audit Logs (Phase 7A)
CREATE TABLE approval_audit_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    approval_id VARCHAR(100) NOT NULL,
    workflow_id VARCHAR(100) NOT NULL,
    query TEXT,
    risk_level VARCHAR(50),
    approved BOOLEAN,
    feedback TEXT,
    approved_by VARCHAR(100),
    duration_ms INTEGER,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Reflections (Phase 7C)
CREATE TABLE reflections (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workflow_id VARCHAR(100) NOT NULL,
    query TEXT NOT NULL,
    initial_draft_ref VARCHAR(200),   -- 完整 draft 走 Workspace
    final_result_ref VARCHAR(200),   -- 完整 final result 走 Workspace
    final_score FLOAT,
    total_rounds INTEGER,
    total_tokens INTEGER,
    audit_summary TEXT,              -- 可审计摘要（不暴露隐藏 CoT）
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Tree of Thoughts (Phase 7D)
CREATE TABLE thought_nodes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workflow_id VARCHAR(100) NOT NULL,
    node_id VARCHAR(100) NOT NULL,
    parent_id VARCHAR(100),
    summary VARCHAR(500),            -- 短摘要（用于 Workflow 内传递）
    score FLOAT,
    depth INTEGER,
    is_terminal BOOLEAN DEFAULT FALSE,
    tokens_used INTEGER,
    thought_ref VARCHAR(200),        -- 完整 thought 文本走 Workspace
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    UNIQUE(workflow_id, node_id)
);

-- Debates (Phase 7E)
CREATE TABLE debates (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workflow_id VARCHAR(100) NOT NULL,
    query TEXT NOT NULL,
    final_position VARCHAR(50),
    consensus_reached BOOLEAN DEFAULT FALSE,
    total_rounds INTEGER,
    total_tokens INTEGER,
    winning_turn_ref VARCHAR(200),   -- 胜出论点走 Workspace
    verdict VARCHAR(50),
    workspace_topic VARCHAR(200),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Debate Positions (Phase 7D)
CREATE TABLE debate_positions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    debate_id UUID NOT NULL REFERENCES debates(id),
    agent_id VARCHAR(100) NOT NULL,
    position VARCHAR(50) NOT NULL,
    arguments JSONB,
    confidence FLOAT,
    tokens_used INTEGER,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Research Reports v2 (Phase 7F)
CREATE TABLE research_reports_v2 (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    report_id VARCHAR(100) NOT NULL UNIQUE,
    query TEXT NOT NULL,
    title VARCHAR(500),              -- 标题通常较短，直接存 Postgres
    executive_summary_ref VARCHAR(200),  -- 完整 executive summary 走 Workspace
    executive_summary_text VARCHAR(2000), -- 短 summary 摘要（用于报告列表展示）
    sections JSONB,                  -- 每章节存 {title, content_summary, content_ref}
    all_evidence JSONB,              -- evidence metadata（summary + content_ref，不含完整 content）
    contradictions JSONB,
    contradiction_report_ref VARCHAR(200), -- 完整矛盾分析报告 → Workspace
    citation_stats JSONB,
    confidence_score FLOAT,
    total_sources INTEGER,
    total_evidence_items INTEGER,
    report_ref VARCHAR(200),         -- 完整报告走 Workspace
    approved BOOLEAN,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Research Evidence v2 (Phase 7F)
CREATE TABLE research_evidence_v2 (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    report_id VARCHAR(100) NOT NULL REFERENCES research_reports_v2(report_id),
    evidence_id VARCHAR(100) NOT NULL,
    subquery_id VARCHAR(100),
    source_id TEXT,
    source_type VARCHAR(50),
    credibility_score FLOAT,
    summary VARCHAR(500),            -- 证据短摘要（用于 Postgres 索引 / 报告章节引用）
    content_ref VARCHAR(200),        -- 完整 evidence content 走 Workspace
    citation_chain JSONB,
    citation_chain_ref VARCHAR(200), -- 完整 citation chain → Workspace（如果超长）
    is_contradiction BOOLEAN DEFAULT FALSE,
    contradicts_with TEXT[],
    retrieved_at TIMESTAMP WITH TIME ZONE,
    metadata JSONB,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    UNIQUE(report_id, evidence_id)
);

-- Router Audit (Phase 7A)
CREATE TABLE routing_audit_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id VARCHAR(100) NOT NULL,
    workflow_id VARCHAR(100) NOT NULL,
    run_id VARCHAR(100),
    mode VARCHAR(50) NOT NULL,            -- direct_answer / rag_answer / react_tool / ...
    workflow_type VARCHAR(100),           -- 选中的下游 workflow
    complexity_score FLOAT,
    risk_level VARCHAR(50),
    requires_approval BOOLEAN DEFAULT FALSE,
    requires_rag BOOLEAN DEFAULT FALSE,
    requires_tools BOOLEAN DEFAULT FALSE,
    requires_sandbox BOOLEAN DEFAULT FALSE,
    requires_workspace BOOLEAN DEFAULT FALSE,
    requires_reflection BOOLEAN DEFAULT FALSE,
    requires_debate BOOLEAN DEFAULT FALSE,
    requires_tot BOOLEAN DEFAULT FALSE,
    requires_research_v2 BOOLEAN DEFAULT FALSE,
    model_tier VARCHAR(50),
    token_budget INTEGER,
    cost_budget_usd FLOAT,
    confidence FLOAT,
    classifier_mode VARCHAR(50),          -- "heuristic" | "mock_llm" | "real_llm"
    short_reason VARCHAR(500),            -- 短解释
    policy_trace_ref VARCHAR(200),        -- 长解释 / 分类器原文 → Workspace
    classification_tokens INTEGER,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Router Policy Traces（写 Workspace，不写 Postgres；表 schema 仅作为元数据登记）
-- 实际长 trace / 分类器原文一律走：
--   workspace:{workflow_id}:router:policy_trace
```

### 8.3 Workspace Topics（Phase 7 新增）

```
# HITL / Approval
workflow:{workflow_id}:approval:{approval_id}         → Approval events
workflow:{workflow_id}:approval:{approval_id}:payload → Full payload (ref)

# Advanced Router (Phase 7A)
workflow:{workflow_id}:router:policy_trace           → 长 policy trace / 分类器原文（不进 history）
workflow:{workflow_id}:router:classification         → 分类器输出详情
workflow:{workflow_id}:router:events                 → Routing 事件流

# Reflection
workflow:{workflow_id}:reflection:rounds              → Round summaries
workflow:{workflow_id}:reflection:drafts             → Draft content (ref, 完整 draft / critique / revision 全部走 ref)
workflow:{workflow_id}:reflection:critiques          → Critique content (ref)
workflow:{workflow_id}:reflection:revisions          → Revision content (ref)

# ToT
workflow:{workflow_id}:tot:thoughts                  → All thought nodes
workflow:{workflow_id}:tot:thoughts:{node_id}        → Thought content (ref, 完整 thought 文本走 ref)
workflow:{workflow_id}:tot:best_path                 → Best path summary

# Debate
workflow:{workflow_id}:debate:transcript:{round}      → Round transcript (ref)
workflow:{workflow_id}:debate:turns:{turn_id}         → 单 turn 论点 (ref)
workflow:{workflow_id}:debate:judgments              → Judge scores
workflow:{workflow_id}:debate:winning_turn           → 胜出 turn (ref)
workflow:{workflow_id}:debate:final                  → Final verdict

# Research v2
workflow:{workflow_id}:research:evidence             → Evidence list (ref to content)
workflow:{workflow_id}:research:contradictions        → Contradiction groups
workflow:{workflow_id}:research:citation_chains      → Citation chains
workflow:{workflow_id}:research:report               → Final report (ref)
workflow:{workflow_id}:research:executive_summary    → Executive summary (ref)
workflow:{workflow_id}:research:debate               → Pre-synthesis debate
```

---

## 九、验收标准总结

| Slice | 验收项 | 通过条件 |
|-------|--------|----------|
| **全局** | Shannon 只读 | `/home/florian/code/cribug/go` 未修改，仅作为参考 |
| **全局** | Cribug 真实目录 | 新实现全部写入 `internal/` / `cmd/` / `migrations/` / `scripts/` / `config/` / `python_llm_service/`，不写入 `go/orchestrator/` |
| **全局** | 章节编号统一 | 一-十八章 + 附录 A-H，Slice 23-28 全篇一致，无重复编号 |
| **全局** | Workflow 确定性 | 不使用 time.Now()/random/goroutine；不直接 Workspace/DB/Redis IO |
| 7A Router | 简单问题路由 | complexity < 0.45 → `direct_answer` |
| 7A Router | 文档问答路由 | 命中"项目/文档"关键词 → `rag_answer` |
| 7A Router | 工具调用路由 | 命中"搜索/执行/计算" → `react_tool` |
| 7A Router | 代码运行路由 | 命中"运行/执行/沙箱" → `sandbox_execution` + `requires_approval=true` |
| 7A Router | 多步骤路由 | complexity >= 0.45 → `dag_workflow` |
| 7A Router | 多 Agent 路由 | complexity >= 0.65 → `swarm_workflow` |
| 7A Router | 方案比较路由 | 命中"比较/对比/vs" → `debate` |
| 7A Router | 多路径搜索 | complexity >= 0.75 + 关键词 → `tree_of_thoughts` |
| 7A Router | 深度研究路由 | complexity >= 0.70 + citations → `research_v2` |
| 7A Router | 决策审计 | decision 写 Postgres `routing_audit_logs` |
| 7A Router | Policy trace | 长 trace 写 Workspace，不进 history |
| 7A Router | 分类器 mock | CI 跑 mock / heuristic，不依赖真实 LLM |
| 7A Router | Addon 能力 | `AddonCapabilities` 字段存在，可组合 sandbox+reflection+rag 等 |
| 7A Router | Approval gate | approval 通过后 continue 原 mode；reject/timeout 不执行 |
| 7A Router | Workflow IO 隔离 | 不直接写 Workspace/DB/Redis（全部走 Activity） |
| 7A Router | FinalAnswerText | ≤2KB；超限走 FinalAnswerRef |
| 7A Router | Policy trace ref | 长 trace 走 WriteRoutingPolicyTraceActivity → Workspace |
| 7A Router | ResearchV2 命名 | 不存在 ResearchV2WorkflowActivity 反模式命名 |
| 7A Router | 简洁任务不误触发 | 简单 query 不触发 DAG/Swarm/ToT/Debate 重流程 |
| 7B Approval | Policy 检查 | 复杂任务触发审批，低风险直接执行 |
| 7B Approval | Signal 等待 | Workflow Await，不消耗 CPU |
| 7B Approval | Approve/Reject/Modify | 正确处理三种响应 |
| 7B Approval | Timeout | 超时返回 partial result |
| 7B Approval | Audit | 所有审批记录写入 PostgreSQL |
| 7B Approval | Control State | pause/resume/cancel 状态正确 |
| 7C Reflection | 多轮迭代 | 支持 1-N 轮 reflection |
| 7C Reflection | Confidence threshold | 达标后停止 |
| 7C Reflection | Audit summary | 不暴露隐藏 CoT |
| 7C Reflection | Workspace ref | 完整 draft / critique / revision 走 ref |
| 7C Reflection | Mock LLM | 默认 mock |
| 7D ToT | Bounded search | max_depth/total_nodes/budget 生效 |
| 7D ToT | 评分剪枝 | pruning_threshold 生效 |
| 7D ToT | Best path | 正确找到并返回 |
| 7D ToT | Workspace ref | thought 文本走 ref，不进 history |
| 7D ToT | Mock LLM | 默认 mock |
| 7E Debate | Pro/Con 生成 | 能生成论点 |
| 7E Debate | Workspace 记录 | transcript 写入 Workspace |
| 7E Debate | Judge | 从 WorkspaceList / TranscriptRef 读 |
| 7E Debate | 轮次限制 | max_rounds 生效 |
| 7E Debate | MockLLM | DebateConfig.MockLLM 显式定义 |
| 7E Debate | Mock LLM | 默认 mock |
| 7F Research v2 | Multi-source | 支持多种来源 |
| 7F Research v2 | Credibility | 证据有可信度评分 |
| 7F Research v2 | Contradiction | 能检测并标记矛盾 |
| 7F Research v2 | Citation chain | 引用链路可追溯 |
| 7F Research v2 | Report ref | 完整报告走 ref，Postgres 只存 summary |
| 7F Research v2 | v1 承接 | 建立在 Phase 6F v1 主路径上 |
| 7F Research v2 | Mock LLM | 默认 mock |

---

## 十、明确禁止事项

- ❌ **Workflow 直接访问 DB/Redis/Qdrant/MCP/Sandbox**（必须通过 Activity）
- ❌ **Workflow 使用 `time.Now()` / `random` / `goroutine`**（必须用 `workflow.Now(ctx)` / Activity）
- ❌ **Activity 调用 `workflow.ExecuteActivity`**（违反 Temporal deterministic）
- ❌ **Approval 通过直接改 Redis/DB 来恢复 Workflow**（必须使用 Signal）
- ❌ **UI 直接改 Workflow 内部状态**（必须通过 Gateway API）
- ❌ **大文本塞进 Workflow history**（必须用 WorkspaceRef/PayloadRef）
- ❌ **保存或展示模型隐藏 chain-of-thought**（只保存 audit summary）
- ❌ **ToT 无限展开**（必须配置 max_depth/max_total_nodes/token_budget）
- ❌ **Debate 无限轮次**（必须配置 max_rounds/round_timeout）
- ❌ **Research v2 伪造 citation**（citation chain 必须可追溯）
- ❌ **重写 Phase 6 的 MCP/RAG/Sandbox/Skills/Hooks**（Phase 7 不重写）
- ❌ **把 Phase 8 的 SDK/CLI/Multi-tenant/Auth 提前塞进 Phase 7**
- ❌ **Reflection 等于 ReAct**（Reflection 是质量评估，不是 reasoning loop）
- ❌ **Research v2 等于简单 RAG answer**（v2 必须有 evidence provenance + contradiction）
- ❌ **Workflow 直接写 Workspace**（必须通过 WriteRoutingPolicyTraceActivity / WorkspaceAppend 等 Activity）
- ❌ **Activity 做 Workflow 级编排**（如 ToTWorkflowActivity、DebateWorkflowActivity、ResearchV2WorkflowActivity 等反模式）
- ❌ **FinalAnswerText 超 2KB 不进 ref**（完整报告 / transcript / evidence 必须走 WorkspaceRef）
- ❌ **Router 选 mode 后不处理 Approval continue**（Approval 通过后必须继续原 mode，不能丢弃 RoutingDecision）
- ❌ **Cribug 新实现写在 go/orchestrator 或 python/llm-service 目录**（必须用 Cribug 真实目录）
- ❌ **修改 /home/florian/code/cribug/go 中任何文件**（Shannon 只读参考）

---

## 十一、真实 LLM / Reasoning 测试策略

### 11.1 测试分层

| 测试类型 | 环境变量 | 默认行为 | 进入 CI |
|---------|---------|---------|--------|
| Mock Regression | 默认 | mock + fixed data | 是 |
| Real LLM | `REAL_LLM_TEST=1` | 调用真实 LLM | 可选 |
| Real Router Classifier | `REAL_ROUTER_TEST=1` | 调用真实 LLM 分类器 | 可选（无 API key skip） |
| Real Reflection | `REAL_REFLECTION_TEST=1` | 调用真实 LLM reflection | 可选 |
| Real ToT | `REAL_TOT_TEST=1` | 调用真实 LLM ToT | 可选 |
| Real Debate | `REAL_DEBATE_TEST=1` | 调用真实 LLM debate | 可选 |
| Real Research v2 | `REAL_RESEARCH_V2_TEST=1` | 调用真实 LLM research | 可选 |

### 11.2 各 Slice 测试策略

**Phase 7A Advanced Router**：
- 默认：heuristic / mock_llm 分类器
- `REAL_ROUTER_TEST=1`：可跑真实 LLM 分类器（成本上限 `$REAL_ROUTER_TEST_MAX_COST_USD`，默认 1 美元）
- 无 API key 时 skip
- 路由决策必须写入 `routing_audit_logs` 表

**Phase 7B HITL / Approval**：
- 默认：mock approval signal
- `ENABLE_HITL=1`：可跑完整 HITL workflow
- 不需要真实 LLM

**Phase 7B Reflection**：
- 默认：mock reflection evaluation
- `REAL_LLM_TEST=1` 或 `REAL_REFLECTION_TEST=1`：可跑真实 LLM reflection
- 无 API key 时 skip

**Phase 7C ToT**：
- 默认：mock thought generation/scoring
- `REAL_TOT_TEST=1`：可跑真实 LLM ToT（成本上限 `$REAL_TOT_TEST_MAX_COST_USD`，默认 1 美元）
- 无 API key 时 skip

**Phase 7D Debate**：
- 默认：mock argument generation/judge
- `REAL_DEBATE_TEST=1`：可跑真实 LLM debate（成本上限 `$REAL_DEBATE_TEST_MAX_COST_USD`，默认 1 美元）
- 无 API key 时 skip

**Phase 7E Research v2**：
- 默认：mock LLM synthesis
- `REAL_RESEARCH_V2_TEST=1`：可跑真实 LLM research（成本上限 `$REAL_RESEARCH_V2_TEST_MAX_COST_USD`，默认 2 美元）
- 无 API key 时 skip

### 11.3 成本上限配置

```bash
# Phase 7 Feature Flags（默认 false）
ENABLE_HITL=false
ENABLE_APPROVAL=false
ENABLE_REFLECTION=false
ENABLE_TOT=false
ENABLE_DEBATE=false
ENABLE_RESEARCH_V2=false

# 测试 Flags（默认 0）
REAL_LLM_TEST=0
REAL_REFLECTION_TEST=0
REAL_TOT_TEST=0
REAL_DEBATE_TEST=0
REAL_RESEARCH_V2_TEST=0

# 成本上限
REAL_LLM_TEST_MAX_COST_USD=1
REAL_REFLECTION_TEST_MAX_COST_USD=1
REAL_TOT_TEST_MAX_COST_USD=1
REAL_DEBATE_TEST_MAX_COST_USD=1
REAL_RESEARCH_V2_TEST_MAX_COST_USD=2

# Approval
APPROVAL_DEFAULT_TIMEOUT_SECONDS=3600
APPROVAL_HIGH_RISK_REQUIRED=true
APPROVAL_ALLOW_MODIFY=true
APPROVAL_MAX_PENDING_PER_WORKFLOW=10

# Reflection
REFLECTION_MAX_ROUNDS=3
REFLECTION_CONFIDENCE_THRESHOLD=0.8
REFLECTION_DEFAULT_RUBRIC="coverage,evidence_quality,citation_integrity,clarity"

# ToT
TOT_MAX_DEPTH=5
TOT_MAX_BRANCHING_FACTOR=3
TOT_MAX_TOTAL_NODES=50
TOT_SCORE_THRESHOLD=0.3
TOT_TOKEN_BUDGET=5000

# Debate
DEBATE_MAX_ROUNDS=5
DEBATE_ROUND_TIMEOUT_SECONDS=60
DEBATE_REQUIRE_JUDGE=true
DEBATE_REQUIRE_EVIDENCE=true

# Research v2
RESEARCH_V2_MAX_SOURCES=20
RESEARCH_V2_MAX_EVIDENCE_ITEMS=100
RESEARCH_V2_ENABLE_CONTRADICTION_DETECTION=true
RESEARCH_V2_REQUIRE_CITATIONS=true
RESEARCH_V2_REQUIRE_APPROVAL_BEFORE_PUBLISH=false
```

---

## 十二、测试脚本说明

### 12.0 两层测试策略

Phase 7 测试分为两层，按 Slice 实现进度逐步启用：

**第一层 — Slice 23 独立测试**（Router contract 完成后立即可跑）：

| 脚本 | 覆盖 |
|------|------|
| `test_advanced_router_smoke.sh` | Routing decision、fallback（mode_disabled / not_implemented）、audit、policy trace ref |
| `test_advanced_router_e2e.sh` | Route → audit → events 端到端（仅 Phase 4-6 已存在 Workflow 的 dispatch） |

- Slice 23 测试**不依赖** Slice 25-28 未实现的 Workflow。
- 对 `reflection` / `tot` / `debate` / `research_v2` 的 dispatch 测试使用 `not_implemented` 或 `mode_disabled` 验证。

**第二层 — Slice 25-28 逐步集成**（各 Slice Workflow 完成后再接入 Router）：

- Slice 25 Reflection 完成后 → 在 Router 中启用 `enable_reflection=true`，验证 `reflection` dispatch。
- Slice 26 ToT 完成后 → 启用 `enable_tot=true`，验证 `tot` dispatch。
- Slice 27 Debate 完成后 → 启用 `enable_debate=true`，验证 `debate` dispatch。
- Slice 28 Research v2 完成后 → 启用 `enable_research_v2=true`，验证 `research_v2` dispatch。

**最终 — Slice 28 后统一验证**：

| 脚本 | 内容 |
|------|------|
| `test_phase7_routed_e2e.sh` | 完整端到端：Router → 所有已启用 mode 的 dispatch → 所有 Slice 集成 |

### 12.1 必须包含的测试脚本

| 脚本 | 内容 | 默认行为 | 进入 CI |
|------|------|---------|--------|
| `test_advanced_router_smoke.sh` | 11 类 query 路由（direct / rag / react / sandbox / dag / swarm / reflection / tot / debate / research_v2 / approval） | mock classifier | 是 |
| `test_advanced_router_e2e.sh` | route → execute → audit → events 端到端 | mock classifier | 是 |
| `test_phase7_routed_e2e.sh` | 完整端到端：Router → HITL / DAG / Swarm / ToT / Debate / Research v2 | mock | 是 |
| `test_hitl_approval_smoke.sh` | Policy check、approval request、signal wait、approve/reject/modify、timeout、audit | mock | 是 |
| `test_reflection_smoke.sh` | 多轮 reflection、confidence threshold、audit summary | mock | 是 |
| `test_tot_smoke.sh` | 分支生成、评分、剪枝、best path selection | mock | 是 |
| `test_debate_smoke.sh` | Pro/Con 生成、Workspace transcript、Judge 裁决 | mock | 是 |
| `test_research_v2_smoke.sh` | Multi-source retrieval、credibility scoring、contradiction detection、report generation | mock | 是 |
| `test_phase7_e2e.sh` | 端到端集成测试（调用所有 Slices） | mock | 是 |
| `test_phase7_real_llm_smoke.sh` | 真实 LLM smoke（需要 API key） | real LLM（可选） | 否 |

### 12.1.1 Advanced Router 测试覆盖项

`test_advanced_router_smoke.sh` 必须覆盖：

1. 简单问题 → `direct_answer`，不触发 DAG / Swarm / ToT / Debate
2. 项目文档问答 → `rag_answer`
3. 需要工具调用 → `react_tool`
4. 需要 sandbox → `sandbox_execution`，高风险时 `requires_approval=true`
5. 多步骤任务 → `dag_workflow`
6. 多 Agent 协作 → `swarm_workflow`
7. 方案比较 → `debate`
8. 多路径搜索 → `tree_of_thoughts`
9. 深度研究 → `research_v2`
10. 路由决策写入 `routing_audit_logs` + Workspace `router:policy_trace`
11. 默认使用 mock classifier（CI 不依赖真实 LLM）
12. `REAL_ROUTER_TEST=1` 时跑真实 LLM 分类器 smoke（不进默认 CI）
13. 无 API key 时真实测试 skip，不 fail
14. Router 不会"重写"下游（验证 `AdvancedRoutingWorkflow` 内不出现 DAG / Swarm / Reflection / ToT / Debate / Research v2 的具体实现代码，只能有 Child Workflow 调用）
15. **sandbox + approval 组合测试**：代码执行任务触发 `sandbox_execution`；高风险时 `requires_approval=true`；approval approved 后继续执行 sandbox；rejected 后不执行 sandbox
16. **research_v2 + sandbox + reflection 组合测试**：深度研究为主模式 `research_v2`；如需代码验证 sandbox 为 addon；如需报告质量提升 reflection 为 addon；三者不互相覆盖
17. **debate + rag 组合测试**：架构方案比较走 `debate` 主模式；如涉及项目文档则启用 RAG addon
18. **routing audit 完整测试**：每次路由必须生成 routing decision；长 policy trace 必须写 WorkspaceRef；Workflow history 不出现完整 policy trace 长文本
19. **approval resume 完整测试**：等待 approval signal → approved 后继续原 mode → rejected 后安全退出 → timeout 后返回 timeout result → modified 后更新 decision 继续
20. **real LLM classifier 测试**：默认 CI 使用 heuristic 或 mock classifier；`REAL_ROUTER_TEST=1` 时才跑真实 LLM classifier smoke；无 API key 时 skip 不 fail
21. **AddonCapabilities 字段测试**：RoutingDecision 包含非空 `addon_capabilities`；组合模式不互相覆盖主 mode
22. **FinalAnswerText 大小限制测试**：短答案直接返回 `FinalAnswerText`；长报告走 `FinalAnswerRef`；验证 Workflow history 不含完整长报告

### 12.2 测试脚本断言策略

每个测试脚本断言：
- **结构正确**：返回的 struct 字段完整、类型正确
- **状态正确**：status / score / verdict 等字段符合预期
- **Workspace 写入**：验证 Workspace 包含预期 entry
- **Audit 写入**：验证 audit log 记录正确
- **不断言**：完整自然语言文本内容（因为 mock/real 差异大）

### 12.3 无 API key 时行为

```bash
# 如果 OPENAI_API_KEY / ANTHROPIC_API_KEY 为空：
# - REAL_LLM_TEST=1 时，测试 skip（不 fail）
# - 输出：SKIP: no API key configured

if [ -z "$OPENAI_API_KEY" ] && [ -z "$ANTHROPIC_API_KEY" ]; then
    echo "SKIP: no API key configured"
    exit 0
fi
```

---

## 十三、参考实现

### 13.1 Shannon 参考文件路径

| 功能 | Shannon 参考文件路径 |
|------|---------------------|
| **HITL / Approval** | |
| Approval middleware | `go/orchestrator/internal/workflows/middleware_approval.go` |
| Control handler | `go/orchestrator/internal/workflows/control/handler.go` |
| Control signals | `go/orchestrator/internal/workflows/control/signals.go` |
| Human intervention activities | `go/orchestrator/internal/activities/human_intervention.go` |
| Approval HTTP handler | `go/orchestrator/internal/httpapi/approval.go` |
| Review handler | `go/orchestrator/cmd/gateway/internal/handlers/review.go` |
| Stream events | `go/orchestrator/internal/activities/stream_events.go` |
| Desktop UI | `desktop/components/swarm-task-board.tsx` |
| Proto definitions | `protos/orchestrator/orchestrator.proto` |
| **Reflection** | |
| Reflection pattern | `go/orchestrator/internal/workflows/patterns/reflection.go` |
| Evaluate activity | `go/orchestrator/internal/activities/evaluate.go` |
| Evaluation API | `python/llm-service/llm_service/api/evaluate.py` |
| Strategy helpers | `go/orchestrator/internal/strategies/strategy_helpers.go` |
| **Tree-of-Thoughts** | |
| ToT pattern | `go/orchestrator/internal/workflows/patterns/tree_of_thoughts.go` |
| Pattern registry | `go/orchestrator/internal/workflows/patterns/registry.go` |
| **Debate** | |
| Debate pattern | `go/orchestrator/internal/workflows/patterns/debate.go` |
| Bull/Bear researcher | `python/llm-service/llm_service/roles/trading/bull_researcher.py` |
| Investor panel synthesizer | `python/llm-service/llm_service/roles/trading/investor_panel_synthesizer.py` |
| **Research / Deep Research** | |
| Research workflow | `go/orchestrator/internal/workflows/strategies/research.go` |
| Citation metadata | `go/orchestrator/internal/metadata/citations.go` |
| Citation agent | `go/orchestrator/internal/activities/citation_agent.go` |
| Source types config | `config/source_types.yaml` |
| Research strategies | `config/research_strategies.yaml` |
| Deep research agent | `python/llm-service/llm_service/roles/deep_research/deep_research_agent.py` |
| **Scientific Workflow** | |
| Scientific workflow | `go/orchestrator/internal/workflows/strategies/scientific.go` |
| **Exploratory Workflow** | |
| Exploratory workflow | `go/orchestrator/internal/workflows/strategies/exploratory.go` |

---

## 十四、完整目录结构

### 14.1 Phase 7 章节总表

| 章节 | 内容 |
|------|------|
| 一 | 项目定位 |
| 二 | 职责边界（含 Advanced Router 约束、HITL/Approval、Reflection、ToT、Debate、Research v2、Phase 6 承接） |
| 三 | 阶段范围（Slices 23-28：Router / HITL / Reflection / ToT / Debate / Research v2） |
| 四 | Reflection Production Mode（Slice 25） |
| 五 | Tree-of-Thoughts（Slice 26） |
| 六 | Debate Mode（Slice 27） |
| 七 | Research-Synthesis v2（Slice 28） |
| 八 | Redis / Postgres / Workspace / 其他数据结构总结 |
| 九 | 验收标准总结 |
| 十 | 明确禁止事项 |
| 十一 | 真实 LLM / Reasoning 测试策略 |
| 十二 | 测试脚本说明 |
| 十三 | 参考实现（Shannon 只读参考） |
| 十四 | 完整目录结构 |
| 十五 | 成功路径 / 失败路径 |
| 十六 | 后续扩展路线图 |
| 十七 | Shannon 原生能力 vs Cribug Lite 实现对照 |
| 十八 | 开发者配置参考 |
| 附录 A-H | Slice 总表 / 不做范围 / 边界 / 测试策略 / 对照 / Temporal / 职责边界 / Shannon 参考 |

### 14.2 Cribug 新增文件结构（写入 Cribug 真实目录）

> **重要**：
> - Cribug 新实现一律写入 Cribug 真实目录（`internal/workflows/...`、`internal/activities/...`、`internal/api/...`、`cmd/gateway/...`、`migrations/`、`scripts/`、`config/`）
> - Shannon 路径（`go/orchestrator/...`、`python/llm-service/...`、`protos/...`、`desktop/...`）**只作为"参考实现"章节的引用**，**不作为 Cribug 新增实现目录**
> - `go/` 目录是只读参考，不修改、不移动、不提交其中任何文件

```
# === Cribug Go Workflows（写入 internal/workflows/，并注册到 registry.go） ===
internal/workflows/
  router.go                       # Phase 7A: AdvancedRoutingWorkflow + dispatchTo* 辅助
  approval.go                     # Phase 7B: HITLApprovalWorkflow（控制流 + signal wait）
  reflection.go                   # Phase 7C: ReflectionProductionWorkflow
  tot.go                          # Phase 7D: TreeOfThoughtsWorkflow
  debate.go                       # Phase 7E: DebateWorkflow
  research_v2.go                  # Phase 7F: ResearchSynthesisV2Workflow（建立在 Phase 6F v1 之上）
  registry.go                     # 已存在；注册 AdvancedRoutingWorkflow / HITLApprovalWorkflow / ...

# === Cribug Go Activities（写入 internal/activities/） ===
internal/activities/
  router.go                       # Phase 7A: ClassifyTaskComplexity / DetectTaskCapabilities / EvaluateRoutingPolicy / EstimateRouteCost / AuditRoutingDecision / EmitRoutingEvent
  approval.go                     # Phase 7B: RequestApproval / GetApprovalStatus / ProcessApprovalResponse / EvaluateApprovalPolicy / AuditApproval / Pause/Resume/CancelWorkflow / GetControlState
  reflection.go                   # Phase 7C: EvaluateDraft / ReviseDraft / AuditReflection
  tot.go                          # Phase 7D: GenerateThoughts / ScoreThought / PruneThoughts / FindBestPath / SynthesizeToTResult
  debate.go                       # Phase 7E: GenerateArguments / JudgeDebate / VoteDebate / CheckConsensus
  research_v2.go                  # Phase 7F: ScoreSourceCredibility / RetrieveMultiSourceEvidence / DetectContradictions / BuildCitationChain / GenerateReportV2 / ReflectionBeforeSynthesis / DebateBeforeSynthesis

# === Cribug Gateway API（写入 internal/api/handlers/） ===
internal/api/handlers/
  router_handlers.go              # Phase 7A: POST /api/v1/tasks/route, /execute-routed, GET routing-decision, GET routing-events
  approval_handlers.go            # Phase 7B: GET /api/v1/approvals/pending, POST /api/v1/approvals/decision, GET /api/v1/approvals/{id}, GET /api/v1/tasks/{wf}/control-state, POST pause/resume/cancel
  reasoning_handlers.go           # Phase 7C-7F: Reflection / ToT / Debate / Research v2 触发与查询

# === Cribug CLI 主程序入口（写入 cmd/gateway/） ===
cmd/gateway/
  main.go                         # 已存在；新增 router / approval / reasoning handler 注册

# === Cribug 配置（写入 config/，与现有 config/features.yaml 等并列） ===
config/
  features.yaml                   # 已存在；新增 router / approval / reflection / tot / debate / research_v2 配置段
  router.yaml                     # Phase 7A: Router 默认阈值、模型分级、分类器模式
  reasoning.yaml                  # Phase 7C-7F: Reflection / ToT / Debate / Research v2 默认配置

# === Cribug Postgres Migrations（续接现有 001-009 编号，从 010 开始） ===
migrations/
  010_routing_audit_logs.sql      # Phase 7A: routing_audit_logs
  011_approvals.sql               # Phase 7B: approvals
  012_approval_audit_logs.sql     # Phase 7B: approval_audit_logs
  013_reflections.sql             # Phase 7C: reflections（initial_draft_ref / final_result_ref）
  014_thought_nodes.sql           # Phase 7D: thought_nodes（thought_ref / summary）
  015_debates.sql                 # Phase 7E: debates（winning_turn_ref）+ debate_positions
  016_research_reports_v2.sql     # Phase 7F: research_reports_v2（executive_summary_ref / report_ref）
  017_research_evidence_v2.sql    # Phase 7F: research_evidence_v2（content_ref / summary）

# === Cribug 测试脚本（写入 scripts/，与现有脚本并列） ===
scripts/
  test_advanced_router_smoke.sh   # Phase 7A: 11 类 query 路由（direct/rag/react/sandbox/dag/swarm/reflection/tot/debate/research_v2 + approval）— mock classifier
  test_advanced_router_e2e.sh     # Phase 7A: 端到端：route → execute → audit → events
  test_phase7_routed_e2e.sh       # Phase 7: 完整端到端：Router → HITL / DAG / Swarm / ToT / Debate / Research v2
  test_hitl_approval_smoke.sh     # Phase 7B
  test_reflection_smoke.sh         # Phase 7C
  test_tot_smoke.sh               # Phase 7D
  test_debate_smoke.sh            # Phase 7E
  test_research_v2_smoke.sh       # Phase 7F
  test_phase7_real_llm_smoke.sh    # Phase 7: 真实 LLM smoke（无 API key 时 skip）

# === Python LLM Service（仅在需要新增 router-classifier endpoint 时） ===
# 写入 python_llm_service/llm_service/api/，但**不写 python/llm-service/**（Shannon 路径）
python_llm_service/
  llm_service/
    api/
      classify.py                 # Phase 7A: 真实 LLM 分类器 endpoint（仅 REAL_ROUTER_TEST=1 时启用）
      evaluate.py                 # Phase 7C: Reflection 评估（如果需要）
    roles/
      reasoning/
        router_classifier.py      # Phase 7A: 路由分类器
        debate_judge.py           # Phase 7E: Debate Judge
        research_synthesizer.py   # Phase 7F: Research v2 报告合成

# === Shannon 参考（只读，绝不写入 Cribug 新代码） ===
# 路径：/home/florian/code/cribug/go/orchestrator/...
#      /home/florian/code/cribug/python/llm-service/...
#      /home/florian/code/cribug/protos/...
#      /home/florian/code/cribug/desktop/...
#      /home/florian/code/cribug/config/shannon.yaml  （参考，不是 Cribug 配置）
# 只在"十三、参考实现"章节引用；不得作为 Cribug 新增实现目录
```

---

## 十五、成功路径 / 失败路径

### Slice 23：Advanced Strategy Router

**成功路径**：
```
AdvancedRoutingWorkflow(query)
  → ClassifyTaskComplexityActivity
  → DetectTaskCapabilitiesActivity
  → EvaluateRoutingPolicyActivity
  → EstimateRouteCostActivity
  → WorkspaceAppend(router:policy_trace)
  → EvaluateApprovalPolicyActivity
  → AuditRoutingDecisionActivity（写 routing_audit_logs）
  → EmitRoutingEventActivity(ROUTING_DECIDED)
  → if requires_approval：dispatchToApproval
  → else：Child Workflow 分派到对应 mode
  → return RoutedExecutionResult
```

**失败路径**：
```
Classify 失败 → 使用默认 score=0.0, mode=direct_answer
Detect 失败 → 不视为必填，使用 input hints
EvaluatePolicy 失败 → 返回 error
Cost 估算失败 → 使用 0 / max budget（不阻塞）
Approval 评估失败 → 不要求 approval（保守分支）
Audit 失败 → 记录 warning，继续
Child Workflow 失败 → 透传 error 到 Status="error"
```

### Slice 24：HITL / Approval / UI Control

**成功路径**：
```
Workflow 执行到审批检查点
  → EvaluateApprovalPolicyActivity
  → RequestApprovalActivity（创建 ApprovalRequest）
  → EmitStreamEvent（APPROVAL_REQUESTED）
  → workflow.GetSignalChannel + workflow.NewSelector + workflow.NewTimer 等待 Signal
  → 人类通过 API 响应
  → Workflow 收到 Signal 继续执行
  → ProcessApprovalResponseActivity + AuditApprovalActivity
```

**失败路径**：
```
Policy 检查失败 → 返回 error
Approval 创建失败 → 返回 error
Signal 超时 → 返回 partial result（reason: "approval_timeout"）
Rejection → 返回 partial result + feedback
Audit 失败 → 记录 warning，继续
```

### Slice 25：Reflection Production Mode

**成功路径**：
```
ReflectionProductionWorkflow(query, initial_draft_ref)
  → for round in [0, max_rounds):
      → EvaluateDraftActivity
      → 如果 score >= threshold：
          → return final_result_ref
      → 否则：
          → ReviseDraftActivity
          → 继续下一轮
  → return final_result_ref
```

**失败路径**：
```
EvaluateDraftActivity 失败 → 返回当前 draft
ReviseDraftActivity 失败 → 返回当前 draft
Audit 失败 → 记录 warning，继续
```

### Slice 26：Tree-of-Thoughts

**成功路径**：
```
TreeOfThoughtsWorkflow(query)
  → 初始化根节点
  → for depth in [0, max_depth):
      → 生成子节点 → 评分 → 剪枝 → WorkspaceAppend
  → FindBestPathActivity
  → SynthesizeToTResultActivity
  → return ToTResult
```

**失败路径**：
```
Token budget 超限 → 停止展开
Max nodes 超限 → 停止展开
GenerateThoughtsActivity 失败 → 跳过该分支
ScoreThoughtActivity 失败 → 跳过该分支
FindBestPathActivity 失败 → 返回 error
```

### Slice 27：Debate Mode

**成功路径**：
```
DebateWorkflow(query)
  → for round in [0, max_rounds):
      → GenerateArgumentsActivity (Pro)
      → GenerateArgumentsActivity (Con)
      → WorkspaceAppend (transcript)
      → JudgeDebateActivity
      → 如果 RequireConsensus && verdict != "tie"：
          → return DebateResult
  → 最终裁决 → return DebateResult
```

**失败路径**：
```
Pro/Con Agent 失败 → 记录 error，继续
Judge 失败 → 返回 error
WorkspaceAppend 失败 → 记录 warning，继续
```

### Slice 28：Research-Synthesis v2

**成功路径**：
```
ResearchSynthesisV2Workflow(query)
  → 复用 Phase 6F v1 DecomposeResearchQuery
  → RetrieveMultiSourceEvidenceActivity
  → ScoreSourceCredibilityActivity
  → DetectContradictionsActivity
  → BuildCitationChainActivity
  → ReflectionBeforeSynthesisActivity（如果启用）
  → DebateBeforeSynthesisActivity（如果启用）
  → GenerateReportV2Activity
  → RequestApprovalActivity（如果启用）
  → return ResearchReportV2（ReportRef + ExecutiveSummaryRef 走 Workspace）
```

**失败路径**：
```
Multi-source retrieval 失败 → 返回 error
Credibility scoring 失败 → 使用默认分数
Contradiction detection 失败 → 跳过
Generate report 失败 → 返回 error
Approval timeout → 返回 partial result
```

---

## 十六、后续扩展路线图

| Phase | 内容 | 关键能力 |
|-------|------|---------|
| Phase 6 | MCP / Sandbox / Skills / Hooks / RAG / Research v1 | Tool Runtime + Sandbox + Skills + Hooks + Memory |
| **Phase 7** | **Advanced Router / HITL / Approval / Reflection / ToT / Debate / Research v2** | **统一路由入口 + 人类审批 + 高级推理** |
| Phase 8 | SDK / CLI / Multi-tenant | Python/Go SDK、CLI、Auth/Quota、配置导入导出（不包括 Phase 7A `config/router.yaml` 这种自身最小配置） |

### Phase 7 后扩展方向

| 扩展方向 | Phase 8+ |
|---------|---------|
| SDK | Python/Go SDK 封装 Phase 7 能力（包括 Router 入口） |
| CLI | 命令行工具调用 Phase 7 workflow（包括 Router） |
| Multi-tenant | Phase 7 能力的租户隔离（Router 决策可按租户配置） |
| Auth/Quota | Phase 7 能力的权限控制 + 配额管理 |
| OpenAI-compatible API | Phase 7 能力的 OpenAI 兼容接口 |
| 配置导入导出 | Phase 7 workflow 配置的 YAML/JSON 导出（含 Router / Reasoning） |

---

## 十七、Shannon 原生能力 vs Cribug Lite 实现对照

| 功能 | Shannon 原生能力 | Cribug Phase 7 Lite | 差异说明 |
|------|-----------------|---------------------|---------|
| **HITL / Approval** | | | |
| Approval Request | `middleware_approval.go:RequestAndWaitApproval` | `RequestApprovalActivity` | Shannon 实现完整审批流，Cribug Lite 先做后端控制闭环 |
| Policy Check | `middleware_approval.go:CheckApprovalPolicy` | `EvaluateApprovalPolicyActivity` | Shannon 有复杂 policy engine，Cribug Lite 先做简单实现 |
| Control Handler | `control/handler.go:SignalHandler` | 内嵌 Workflow 级别 | Shannon 有独立 handler，Cribug Lite 简化集成 |
| Pause/Resume | `control/signals.go` | `SignalPause/SignalResume` | 复用 Shannon Signal 协议 |
| Approval API | `httpapi/approval.go` | `POST /api/v1/approvals/decision` | Shannon 有独立 server，Cribug Lite 通过 Gateway |
| Review Handler | `review.go:ReviewHandler` | `POST /api/v1/review/{workflowID}` | Shannon 有复杂 review round，Cribug Lite 先做单级 |
| Desktop UI | `desktop/components/` | mock UI contract | Shannon 有完整 React 组件，Cribug Lite 不做前端 |
| **Reflection** | | | |
| Reflection Pattern | `patterns/reflection.go:ReflectOnResult` | `ReflectionProductionWorkflow` | Shannon 有完整 pattern，Cribug Lite 先实现基础版 |
| EvaluateResult | `activities/evaluate.go:EvaluateResult` | `EvaluateDraftActivity` | Shannon 有 heuristic scoring，Cribug Lite 用 mock |
| Evaluation API | `llm_service/api/evaluate.py` | Python LLM Service | Shannon 有完整 evaluation criteria，Cribug Lite 简化 |
| **Tree-of-Thoughts** | | | |
| ToT Pattern | `patterns/tree_of_thoughts.go:TreeOfThoughts` | `TreeOfThoughtsWorkflow` | Shannon 有完整 ToT 实现，Cribug Lite 先做 bounded 版 |
| ThoughtNode | `patterns/tree_of_thoughts.go:ThoughtNode` | `ThoughtNode` | 复用 |
| Evaluation Method | `scoring/voting/llm` | `scoring` only | Shannon 支持三种，Cribug Lite 先做 scoring |
| **Debate** | | | |
| Debate Pattern | `patterns/debate.go:Debate` | `DebateWorkflow` | Shannon 有完整 debate 实现，Cribug Lite 先做基础版 |
| Pro/Con Agents | `bull_researcher.py` / `bear_researcher.py` | `GenerateArgumentsActivity` | Shannon 有 preset prompts，Cribug Lite 用 Activity |
| Judge | `moderateDebate()` | `JudgeDebateActivity` | Shannon 有 moderator synthesis，Cribug Lite 用评分 |
| **Research v2** | | | |
| Research Workflow | `strategies/research.go:ResearchWorkflow` | `ResearchSynthesisV2Workflow` | Shannon 有深度研究实现，Cribug Lite 先做 v2 升级版 |
| Citation | `metadata/citations.go:Citation` | `Citation` | 复用 Shannon Citation 结构 |
| Citation Agent | `activities/citation_agent.go` | `AddCitations` | 复用 Shannon Citation Agent |
| Source Types | `config/source_types.yaml` | `SourceCredibility` | Shannon 有完整 source type 定义 |
| Research Strategies | `config/research_strategies.yaml` | `ResearchV2Config` | Shannon 有 strategy 分级，Cribug Lite 统一配置 |
| Deep Research Agent | `deep_research_agent.py` | Python LLM Service | Shannon 有 OODA loop，Cribug Lite 用 Activity 封装 |
| Coverage Evaluator | `activities/coverage_evaluator.go` | `ReflectionBeforeSynthesisActivity` | 复用为 reflection before synthesis |
| **Workspace** | | | |
| Append-only | Phase 5 冻结 | `WorkspaceAppend` / `WorkspaceList` | 延续 |
| ToT Thoughts | - | `tot:thoughts` topic | Phase 7C 新增 |
| Debate Transcript | - | `debate:transcript` topic | Phase 7D 新增 |
| Research Evidence | Phase 6F 冻结 | `research:evidence` topic | Phase 7E 升级 |

---

## 十八、开发者配置参考

### 18.1 Feature Flags

| Feature Flag | 类型 | 默认值 | 说明 |
|-------------|------|--------|------|
| enable_advanced_router | bool | false | 开启 Advanced Strategy Router（Phase 7A 主开关） |
| enable_hitl | bool | false | 开启 HITL 能力 |
| enable_approval | bool | false | 开启 Approval 能力 |
| enable_reflection | bool | false | 开启 Reflection |
| enable_tot | bool | false | 开启 Tree-of-Thoughts |
| enable_debate | bool | false | 开启 Debate Mode |
| enable_research_v2 | bool | false | 开启 Research-Synthesis v2 |
| enable_reasoning_ui | bool | false | 开启 Reasoning UI（mock contract） |

### 18.1.1 Advanced Router 配置（Phase 7A）

新增配置写入 `config/router.yaml`（与现有 `config/features.yaml` / `config/budget.yaml` / `config/models.yaml` 并列），不在脚本中散落：

```yaml
# config/router.yaml
router:
  enabled: false                           # ENABLE_ADVANCED_ROUTER
  default_mode: "direct_answer"            # ROUTER_DEFAULT_MODE
  classifier_mode: "heuristic"             # "heuristic" | "mock_llm" | "real_llm"
  thresholds:
    complexity_dag: 0.45                   # ROUTER_COMPLEXITY_DAG_THRESHOLD
    complexity_swarm: 0.65                 # ROUTER_COMPLEXITY_SWARM_THRESHOLD
    complexity_tot: 0.75                   # ROUTER_COMPLEXITY_TOT_THRESHOLD
    research_v2: 0.70                      # ROUTER_RESEARCH_V2_THRESHOLD
  approval:
    risk_threshold: "high"                 # ROUTER_APPROVAL_RISK_THRESHOLD
  classification:
    max_tokens: 800                        # ROUTER_MAX_CLASSIFICATION_TOKENS
  audit:
    decision_audit_enabled: true           # ROUTER_DECISION_AUDIT_ENABLED
  real_llm_test:
    enabled: 0                             # REAL_ROUTER_TEST
    max_cost_usd: 1                        # REAL_ROUTER_TEST_MAX_COST_USD
```

> 后续会做统一 model provider YAML，但 Phase 7 不提前做完整 Phase 8 配置导入导出。Router 配置只写 `router.yaml`，与 `config/features.yaml` 平行，不强耦合。

### 18.1.2 Router 环境变量（覆盖 config/router.yaml）

```bash
ENABLE_ADVANCED_ROUTER=false
ROUTER_DEFAULT_MODE=direct_answer
ROUTER_CLASSIFIER_MODE=heuristic
ROUTER_COMPLEXITY_DAG_THRESHOLD=0.45
ROUTER_COMPLEXITY_SWARM_THRESHOLD=0.65
ROUTER_COMPLEXITY_TOT_THRESHOLD=0.75
ROUTER_RESEARCH_V2_THRESHOLD=0.70
ROUTER_APPROVAL_RISK_THRESHOLD=high
ROUTER_MAX_CLASSIFICATION_TOKENS=800
ROUTER_DECISION_AUDIT_ENABLED=true
REAL_ROUTER_TEST=0
REAL_ROUTER_TEST_MAX_COST_USD=1
```

### 18.2 Approval 配置

| Config | 类型 | 默认值 | 说明 |
|--------|------|--------|------|
| approval_default_timeout_seconds | int | 3600 | 审批超时（1小时） |
| approval_high_risk_required | bool | true | 高风险操作必须审批 |
| approval_allow_modify | bool | true | 允许 reject+modify |
| approval_max_pending_per_workflow | int | 10 | 每个 workflow 最大待审批数 |

### 18.3 Reflection 配置

| Config | 类型 | 默认值 | 说明 |
|--------|------|--------|------|
| reflection_max_rounds | int | 3 | 最大 reflection 轮次 |
| reflection_confidence_threshold | float | 0.8 | 置信度阈值 |
| reflection_default_rubric | string | "coverage,evidence_quality,citation_integrity,clarity" | 默认评分标准 |

### 18.4 ToT 配置

| Config | 类型 | 默认值 | 说明 |
|--------|------|--------|------|
| tot_max_depth | int | 5 | 最大深度 |
| tot_max_branching_factor | int | 3 | 每层最大分支 |
| tot_max_total_nodes | int | 50 | 最大总节点数 |
| tot_score_threshold | float | 0.3 | 剪枝阈值 |
| tot_token_budget | int | 5000 | Token 预算 |

### 18.5 Debate 配置

| Config | 类型 | 默认值 | 说明 |
|--------|------|--------|------|
| debate_max_rounds | int | 5 | 最大辩论轮次 |
| debate_round_timeout_seconds | int | 60 | 每轮超时（秒） |
| debate_require_judge | bool | true | 是否需要 Judge |
| debate_require_evidence | bool | true | 是否需要证据 |

### 18.6 Research v2 配置

| Config | 类型 | 默认值 | 说明 |
|--------|------|--------|------|
| research_v2_max_sources | int | 20 | 最大来源数 |
| research_v2_max_evidence_items | int | 100 | 最大证据条目数 |
| research_v2_enable_contradiction_detection | bool | true | 启用矛盾检测 |
| research_v2_require_citations | bool | true | 要求 citations |
| research_v2_require_approval_before_publish | bool | false | 发布前必须审批 |

### 18.7 Real LLM 测试配置

| Config | 类型 | 默认值 | 说明 |
|--------|------|--------|------|
| REAL_LLM_TEST | int | 0 | 真实 LLM 测试（不进 CI） |
| REAL_ROUTER_TEST | int | 0 | 真实 Router 分类器测试（无 API key skip） |
| REAL_REFLECTION_TEST | int | 0 | 真实 Reflection 测试 |
| REAL_TOT_TEST | int | 0 | 真实 ToT 测试 |
| REAL_DEBATE_TEST | int | 0 | 真实 Debate 测试 |
| REAL_RESEARCH_V2_TEST | int | 0 | 真实 Research v2 测试 |
| REAL_LLM_TEST_MAX_COST_USD | float | 1 | 成本上限（美元） |
| REAL_ROUTER_TEST_MAX_COST_USD | float | 1 | Router 成本上限 |
| REAL_REFLECTION_TEST_MAX_COST_USD | float | 1 | Reflection 成本上限 |
| REAL_TOT_TEST_MAX_COST_USD | float | 1 | ToT 成本上限 |
| REAL_DEBATE_TEST_MAX_COST_USD | float | 1 | Debate 成本上限 |
| REAL_RESEARCH_V2_TEST_MAX_COST_USD | float | 2 | Research v2 成本上限 |

---

## 附录 A：Phase 7 Slice 总表（Slices 23-28）

| Slice | 功能 | 核心目标 | 默认测试 |
|-------|------|----------|----------|
| Phase 7A Slice 23 | **Advanced Strategy Router** | 统一入口：query 复杂度分类、能力检测、路由策略评估、成本估算、审计、分派到 Phase 4-7 已存在 workflow | mock classifier |
| Phase 7B Slice 24 | **HITL / Approval / UI Control** | 人类审批、任务暂停恢复、Approval Signal、Dashboard / UI 控制入口 | mock |
| Phase 7C Slice 25 | **Reflection Production Mode** | generate -> reflect -> revise，多轮 reflection，rubric，audit | mock |
| Phase 7D Slice 26 | **Tree-of-Thoughts** | 多候选路径、评分、剪枝、best path selection、bounded search | mock |
| Phase 7E Slice 27 | **Debate Mode** | Pro Agent、Con Agent、Judge Agent、多轮辩论 | mock |
| Phase 7F Slice 28 | **Research-Synthesis v2** | 多来源证据、冲突证据处理、引用链路、深度研究报告（建立在 v1 主路径上） | mock LLM |

---

## 附录 B：Phase 7 不做范围清单

以下内容不在 Phase 7 范围内：

1. **不做 SDK / CLI**（Phase 8）
2. **不做 Multi-tenant**（Phase 8）
3. **不做 Auth / Quota**（Phase 8）
4. **不做 OpenAI-compatible API**（Phase 8）
5. **不做配置导入导出**（Phase 8；Phase 7A 只写 `config/router.yaml`，不做完整 model provider 导入导出）
6. **不做完整 Dashboard / UI 产品**（Phase 7B 只提供 API contract + mock UI contract）
7. **不做复杂 multi-level approval hierarchy**（Phase 7B 只做单级）
8. **不做企业身份集成**（LDAP/SAML）（Phase 8）
9. **不做 backtrack 回溯**（Phase 7D ToT config.BacktrackEnabled = false）
10. **不做无限展开**（ToT 必须配置 bounded 参数）
11. **不做实时矛盾更新**（Research v2 只做一次性检测）
12. **不做复杂 citation graph 可视化**（Phase 7F 只做 basic）
13. **不做企业级 citation style**（APA/MLA/Chicago）（Phase 8+）
14. **不做保存完整 CoT**（Reflection 只保存 audit summary）
15. **不做复杂 rubric hierarchy**（Reflection 只做 flat criteria）
16. **不做复杂 multi-Agent 辩论**（Debate 只做 Pro + Con + Judge）
17. **不做 Real-time debate UI**（Debate 只做最终 transcript）
18. **不做 Router 自动训练**（分类器策略走 config / env，不在线学习）
19. **不做 Router 多租户隔离**（Phase 8）
20. **不做 Router 可视化 Dashboard**（Phase 7B mock UI 即可）
21. **不做"Router 包整个下游 workflow"**（Router 通过 Child Workflow 分派，不复制下游实现）

---

## 附录 C：Phase 7 与 Phase 6 / Phase 8 的边界

### Phase 7 与 Phase 6 的边界

- **延续**：Phase 5/6 的 Workspace append-only 原则
- **延续**：Phase 6 的 MCP / RAG / Sandbox / Skills / Hooks 能力
- **不重写**：Phase 6 的任何实现
- **升级**：Research-Synthesis v1 → v2（Phase 7F 必须建立在 Phase 6F v1 主路径上，而不是新写一个绕过 v1 的 all-in-one workflow）
- **复用**：Swarm / Workspace / Security from Phase 5

### Phase 7 与 Phase 8 的边界

- **不做 Phase 8**：SDK / CLI / Multi-tenant / Auth / Quota / OpenAI-compatible API / 配置导入导出（除了 `config/router.yaml` 这种 Phase 7 自身需要的最小配置）
- **后续扩展**：Phase 8 可以在 Phase 7 基础上添加 SDK 封装、CLI 工具、企业身份集成

---

## 附录 D：真实 LLM / Reasoning 测试策略

### D.1 测试矩阵

| Feature | REAL_LLM_TEST | 进入 CI | 成本上限 |
|---------|--------------|---------|---------|
| Router Classifier | `REAL_ROUTER_TEST=1` | 否（无 API key skip） | $1 |
| Reflection | `REAL_REFLECTION_TEST=1` | 否 | $1 |
| ToT | `REAL_TOT_TEST=1` | 否 | $1 |
| Debate | `REAL_DEBATE_TEST=1` | 否 | $1 |
| Research v2 | `REAL_RESEARCH_V2_TEST=1` | 否 | $2 |

### D.2 测试断言策略

```bash
# 测试脚本只断言：
# 1. 结构正确（struct 字段完整）
# 2. 状态正确（status / score / verdict）
# 3. Workspace 写入（entry 存在）
# 4. Audit 写入（log 存在）
# 5. llm_calls 计数 > 0（真实 LLM 测试）
# 6. token usage > 0（真实 LLM 测试）

# 不断言：
# - 完整自然语言文本内容（mock/real 差异大）
```

### D.3 无 API key 行为

```bash
if [ -z "$OPENAI_API_KEY" ] && [ -z "$ANTHROPIC_API_KEY" ]; then
    echo "SKIP: no API key configured"
    exit 0
fi
```

---

## 附录 E：Shannon 原生能力 vs Cribug Lite 对照

| 功能 | Shannon 原生能力 | Cribug Phase 7 Lite | 差异说明 |
|------|-----------------|---------------------|---------|
| **Advanced Router（Slice 23）** | `strategies/research.go` + `strategies/scientific.go` + `strategies/exploratory.go` + `patterns/registry.go` + `middleware_approval.go` | `AdvancedRoutingWorkflow` + `EvaluateRoutingPolicyActivity` + `routing_audit_logs` | Shannon 把策略分散在 `strategies/` + `patterns/registry.go` + `middleware_approval.go` + `control/handler.go`；Cribug 集中为一个 `AdvancedRoutingWorkflow`，便于统一审计 / 成本控制 / HITL 协同 |
| HITL / Approval | `middleware_approval.go` | 后端控制闭环 | Shannon 完整 policy engine，Cribug Lite 简化 |
| UI / Dashboard | `desktop/components/` | API contract + mock | Shannon 完整 React UI，Cribug Lite 不做前端 |
| Reflection | `patterns/reflection.go` | `ReflectionProductionWorkflow` | Shannon 完整 pattern，Cribug Lite 基础版 |
| Tree-of-Thoughts | `patterns/tree_of_thoughts.go` | `TreeOfThoughtsWorkflow` | Shannon 完整实现，Cribug Lite bounded 版 |
| Debate | `patterns/debate.go` | `DebateWorkflow` | Shannon 完整实现，Cribug Lite Pro/Con/Judge 基础版 |
| Research v2 | `strategies/research.go` | `ResearchSynthesisV2Workflow`（建立在 Phase 6F v1 之上） | Shannon 深度研究，Cribug Lite v2 升级版 |
| Workspace | Phase 5 冻结 | `WorkspaceAppend / WorkspaceList` | 延续 |
| Tools / Sandbox / Skills | Phase 6 冻结 | 消费 Phase 6 能力 | Phase 7 不重写 |

---

## 附录 F：是否仍保持 Temporal Workflow 确定性

**是**。

Phase 7 所有 Workflow 遵守 Temporal deterministic 规则：

1. **Workflow 只使用 `workflow.Now(ctx)`** 生成时间戳，不使用 `time.Now()`
2. **Workflow 不使用 `random`**，所有随机性在 Activity 或 Python LLM Service
3. **Workflow 不创建 goroutine**，所有并发通过 `workflow.ExecuteActivity` + Future
4. **Activity 不调用 `workflow.ExecuteActivity`**，违反 deterministic
5. **Activity 不使用 `workflow.Now(ctx)`**，必须由 Workflow 传入
6. **大文本通过 WorkspaceRef / PayloadRef 传递**，不进入 Workflow history

---

## 附录 G：是否仍保持 Gateway / Workflow / Activity / Python LLM Service 职责边界

**是**。

| 组件 | Phase 7 职责 | 禁止 |
|------|-------------|------|
| Gateway | 接收 approval/pause/resume/route 请求，创建 task，返回 SSE，Dashboard / UI 操作入口 | 直接改 Workflow 内部状态；直接调 LLM |
| Workflow | 确定性编排，调用 Activities，维护状态机，等待 Signal，维护 Router 状态机 | 直接访问外部 IO |
| Activity | 所有外部 IO：DB/Redis 写入、Signal 发送、LLM call、Workspace 写入、Router 分类器调用 | 做全局业务编排、调用 workflow.ExecuteActivity、生成业务时间戳 |
| Python LLM Service | LLM generate / reflection / judge / scoring / synthesis / router-classifier | 任务编排 |

---

## 附录 H：需要在 Shannon 项目中重点参考的文件路径列表

> **重要约束**：
> - Shannon 路径（`go/orchestrator/...`、`python/llm-service/...`、`protos/...`、`desktop/...`）只作为"参考实现"章节的引用
> - **`/home/florian/code/cribug/go` 目录是只读参考，不得修改、移动、提交其中任何文件**
> - 不得在 Cribug 新代码中使用 Shannon 路径作为实现目录

### Advanced Router（Phase 7A，参考策略组合模式）
- `go/orchestrator/internal/strategies/strategy_helpers.go`（参考 `shouldReflect` / 策略公共工具）
- `go/orchestrator/internal/workflows/strategies/research.go`
- `go/orchestrator/internal/workflows/strategies/scientific.go`
- `go/orchestrator/internal/workflows/strategies/exploratory.go`
- `go/orchestrator/internal/workflows/patterns/registry.go`（参考 Pattern Registry）
- `go/orchestrator/internal/workflows/middleware_approval.go`（参考 Approval Policy）
- `go/orchestrator/internal/workflows/control/handler.go`（参考 Control Handler）
- `go/orchestrator/internal/workflows/control/signals.go`（参考 Signal 常量）

### HITL / Approval
- `go/orchestrator/internal/workflows/middleware_approval.go`
- `go/orchestrator/internal/workflows/control/handler.go`
- `go/orchestrator/internal/workflows/control/signals.go`
- `go/orchestrator/internal/activities/human_intervention.go`
- `go/orchestrator/internal/httpapi/approval.go`
- `go/orchestrator/cmd/gateway/internal/handlers/review.go`
- `protos/orchestrator/orchestrator.proto`

### Reflection / ToT / Debate
- `go/orchestrator/internal/workflows/patterns/reflection.go`
- `go/orchestrator/internal/workflows/patterns/tree_of_thoughts.go`
- `go/orchestrator/internal/workflows/patterns/debate.go`
- `go/orchestrator/internal/workflows/patterns/registry.go`
- `go/orchestrator/internal/activities/evaluate.go`
- `python/llm-service/llm_service/api/evaluate.py`

### Research / Deep Research
- `go/orchestrator/internal/workflows/strategies/research.go`
- `go/orchestrator/internal/metadata/citations.go`
- `go/orchestrator/internal/activities/citation_agent.go`
- `config/source_types.yaml`
- `config/research_strategies.yaml`
- `python/llm-service/llm_service/roles/deep_research/deep_research_agent.py`

### Scientific / Exploratory Workflows
- `go/orchestrator/internal/workflows/strategies/scientific.go`
- `go/orchestrator/internal/workflows/strategies/exploratory.go`
- `go/orchestrator/internal/strategies/strategy_helpers.go`

### Desktop UI
- `desktop/components/swarm-task-board.tsx`
- `desktop/components/run-timeline.tsx`
- `desktop/components/run-conversation.tsx`

### Stream Events
- `go/orchestrator/internal/activities/stream_events.go`

### Bull/Bear Researcher (Pro/Con Agents)
- `python/llm-service/llm_service/roles/trading/bull_researcher.py`
- `python/llm-service/llm_service/roles/trading/bear_researcher.py`

---

*Phase 7 任务书 v1.1（新增 Advanced Strategy Router Slice 23，章节统一 / 目录修正 / 大文本入库修正 / 伪代码字段一致性修正）*