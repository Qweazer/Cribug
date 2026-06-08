# Phase 7I Router Strategy Upgrade — 实施计划书

> 计划阶段文档，不实现任何代码。
> 用户确认后再开始实现。

## 0. 文档目的

把当前 Cribug Router 从 **keyword + rules + 单复杂度阈值** 的粗糙版本，**增量改造**为多信号 policy router，**不重写 Router、不绕过 Approval Gate、不重写 Reflection/ToT/Debate/Research v2、不动 Shannon、不进入 Phase 8**。

---

## 1. 当前 Cribug Router 现状分析

### 1.1 路由信号源（仅 2 个）

`internal/activities/router.go` 当前所有路由决策只依赖两个东西：

| 信号                                                                                 | 来源                                      | 说明                                                                                      |
| ------------------------------------------------------------------------------------ | ----------------------------------------- | ----------------------------------------------------------------------------------------- |
| `ComplexityScore`                                                                    | `classifyHeuristic(query, intent)` 启发式 | 0.0~1.0 标量，由 `len(query)`、7 套 keyword 命中、句号数、numbered list、user_intent 决定 |
| `CapabilityNeeds` (RequiresTools / RequiresRAG / RequiresResearch / RequiresSandbox) | `DetectTaskCapabilitiesActivity` 关键词   | 4 个布尔字段，决定能否用 tools / RAG / sandbox / research                                 |

### 1.2 当前选择逻辑

`selectPlannedMode(input)` 是一个长 `if/else if` 链（router.go:330-389），按下列**顺序**短路返回：

```
RequiresSandbox                          → RouteSandboxExecution
c >= 0.50 + Citations + Research        → RouteResearchV2
c >= 0.65 + Research                    → RouteResearchV2
c >= 0.60                                → RouteTreeOfThoughts
0.20 <= c < 0.60 + ComplexitySummary∈"debate" → RouteDebate
c >= 0.70                                → RouteSwarmWorkflow
RequiresTools + c >= 0.45                 → RouteDAGWorkflow
c >= 0.35                                → RouteReflection
RequiresTools + c >= 0.20                 → RouteReActTool
RequiresRAG                                → RouteRAGAnswer
default                                  → RouteDirectAnswer
```

### 1.3 当前 planned_mode / executed_mode / fallback

- `planner` 选定 → `ResolveExecutedMode` 检查 `RouterConfigSnapshot.EnableXxx` 字段；disable 时回退 `mode_disabled` 或 ToT（仅 reflection）。
- 已有 `FallbackReason` 字段但**只有 reflection 关闭**一条带原因，其它都返 `""`。
- `RoutedExecutionResult.FallbackReason` 字段已存在。

### 1.4 当前 signals 对路由的实际影响

| Signal                                      | 是否真的影响路由                                                                          |
| ------------------------------------------- | ----------------------------------------------------------------------------------------- |
| `BudgetUSD`                                 | **否**（只填入 decision，路由不查）                                                       |
| `MaxLatencyMs`                              | **否**（未读取）                                                                          |
| `RiskLevelHint`                             | **否**（由 complexity 算 risk，hint 字段被忽略）                                          |
| `AllowTools / AllowSandbox / AllowResearch` | **部分**（Capabilities 阶段不读这些，只有 router 自己检测；用户明确禁用的工具仍可能误派） |
| `RequireCitations`                          | **是**（partial — 只在 c>=0.50 触发 research_v2）                                         |
| `UserIntent`                                | **是**（`deep_research` intent 触发 +0.30 complexity）                                    |
| `CapabilityNeeds`                           | **是**（SelectPlannedMode 显式判断 RequiresTools / RequiresRAG / RequiresSandbox）        |

### 1.5 当前明显问题

1. **单值 complexity 压扁所有信号**：7 套关键词叠加成一个 0~1 标量后，所有 mode 选择变成"c >= X 阈值"的硬线段——无法表达"用户预算低 + 任务中等 → 选 reflection 不选 ToT"这种**多维条件**。
2. **关键词死板**：debate 关键词只包含中文 "比较/对比" + "vs / versus / debate / pros and cons" + "postgresql/mongodb/which is better/compare"。英文长 query 很容易"探索 / explore / 看法 / 观点" 被错过，反而 ToT 抢走。
3. **planned → executed 的 fallback 不完整**：ResolveExecutedMode 开关 case 写得很死，e.g. ToT 关闭 → mode_disabled（**没有 fallback**），但 Reflection 关闭有 fallback 到 ToT。其它 mode 全是 mode_disabled。
4. **完全没有解释**：decision.Reason 是 "complexity=0.55 risk=medium planned=X executed=X" 拼字符串，没有 `selected_mode / alternatives / rejected_modes / score_breakdown` 字段。
5. **无 telemetry**：routing_audit_logs 表里只存 row，不存 signals / scores / policy_version。
6. **LLM classifier 形同虚设**：`config/router_policy.yaml` 写了 `classifier.enabled: false`，但代码里也根本没有调用任何 LLM classifier 路径。
7. **Sandbox 选了不一定高风险**：RequiresSandbox 命中直接 RouteSandboxExecution，但 budget / risk / citation 等其他信号完全没参与权衡。
8. **Disabled mode 没有"degrade"路径**：production 把 ENABLE_TOT 关掉后，一个"复杂度 0.6"的任务直接 mode_disabled 失败，没有 fallback 到 reflection / dag。

---

## 2. Shannon 参考分析

### 2.1 值得借鉴的设计

| Shannon 思路                                                                         | 文件                                                                       | 借鉴点                                                                                      |
| ------------------------------------------------------------------------------------ | -------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------- |
| `determineModelTier` 基于 context 多信号（complexity + research_strategy）           | `go/orchestrator/internal/workflows/strategies/strategy_helpers.go:24-104` | Cribug 同样应该把"strategy / mode 候选"作为 context 多个独立信号分别评分                    |
| `shouldReflect(complexity, config)` 用 config 提供 threshold                         | `strategy_helpers.go:178-187`                                              | Cribug Router 也应该把阈值放 `RouterConfigSnapshot` 而非硬编码                              |
| Strategy 命名（quick / standard / deep / academic）→ 强制映射到 model tier           | `strategy_helpers.go:62-95`                                                | 启发：Cribug 可以用"模式名 → mode"做更高层语义映射，但本阶段**不引入**新模式名（避免重写）  |
| Patterns Registry (`registry.go`) 把 reflection / debate / ToT / CoT 注册为 patterns | `go/orchestrator/internal/workflows/patterns/registry.go`                  | Cribug 已有 `internal/workflows/registry.go`，模式注册模式可借鉴，但本阶段**不动注册表**    |
| EmitTaskUpdate 事件                                                                  | `strategy_helpers.go:198-225`                                              | Cribug router 决策**可以 emit 一条 audit event**（router_decision_emitted）以增强 telemetry |

### 2.2 只适合作为设计原则（不能直接复制）

- Shannon 的 `WorkflowConfig`（DB-driven 配置）：Cribug 当前用 `RouterConfigSnapshot` + env，已经足够；**不引入** DB-backed config
- Shannon 的 `pricing.GetModelCost`（动态 token 估价）：超出本阶段
- Shannon 的 `circuitbreaker` / `ratecontrol` / `degradation` 模块：与 router 关注点不同，**不混用**

### 2.3 Shannon 与 Cribug 当前架构的差异

| 维度          | Shannon                                 | Cribug                                             |
| ------------- | --------------------------------------- | -------------------------------------------------- |
| Router 触发点 | Strategy 选定（`DetermineStrategy`）    | `EvaluateRoutingPolicyActivity`                    |
| 模式声明      | patterns/registry.go 注册               | `types.RouteXxx` 常量 + `dispatchByMode` switch    |
| 决策持久化    | `routing_logs`（DB）                    | `routing_audit_logs`（DB）+ `tasks.metadata` JSONB |
| Approval      | `middleware_approval.go` (Signal-based) | `executeWithApprovalGate` (Temporal Signal)        |
| 异步结果      | 无                                      | 已有 Phase 7E.5 async polling                      |

### 2.4 适合 / 不适合 Phase 7I

**适合（增量改造）：**
- 多信号评分作为 internal helper（不影响 Router 入口签名）
- `selectPlannedMode` 内部用 signals 替代硬编码
- `Decision.Reason` 改为结构化 `DecisionExplanation`
- 加载 `config/router_policy.yaml` 替代硬编码阈值
- 写一条 `router_decision_emitted` 审计事件（复用 `WriteAuditEventActivity` 不行 — 那是 Phase 7H；router decision audit 通过 `AuditRoutingDecisionActivity` 已经存在，**通过新增 migration 例如 `016_routing_signals.sql` 给 `routing_audit_logs` 增加 `signals_json` / `explanation_json` / `policy_version` / `score_breakdown_json` 等字段，不要修改旧 010 migration**）
- `ROUTER_LEGACY_HEURISTIC=1` env：切回旧 selectPlannedMode

**不适合（明确不做）：**
- 引入 LLM classifier 默认调用（只预留接口与 config 入口）
- 引入 DB-driven policy
- 引入 user feedback 闭环
- 重写 Router workflow / activity 入口签名
- 修改 Approval Gate
- 修改任何 mode 现有的 Workflow 入口

---

## 3. Phase 7I 改造目标

把当前 "ComplexityScore + CapabilityNeeds 二维信号" 升级为**多信号评分**，每个信号独立计算，最终聚合到 **ModeCandidate 列表**，按 policy 排序挑选。

### 3.1 改造目标清单

1. ✅ 引入 `RouterDecisionSignals` 结构（独立可观测）
2. ✅ 引入 `RouterDecisionExplanation`（替代/补充 `Reason` 字符串）
3. ✅ 引入 `ModeCandidate`（每个 mode 一个候选 + 分数 + 拒绝原因）
4. ✅ 引入 `RejectedModeReason`（为什么某个 mode 被排除）
5. ✅ 引入 `PolicyConfig`（从 `config/router_policy.yaml` 加载）
6. ✅ 引入 `BudgetLatencySignals`（预算/延迟信号采集）
7. ✅ 多信号复杂度评估（拆 complexity 为 4 个 sub-signal）
8. ✅ Capability matching（用真正的 allow_* 限制 + capability needs 双向校验）
9. ✅ planned_mode / executed_mode / fallback_reason 全链路
10. ✅ selected / rejected modes 在 decision 里可解释
11. ✅ telemetry：routing_audit_logs 增加 signals + explanation 字段（用新 migration `016_routing_signals.sql`）
12. ✅ heuristic fast path 保留
13. ✅ LLM classifier 只预留接口（不默认启用）
14. ✅ 不破坏 async result API
15. ✅ 不破坏现有 Slice 25/26/27/28 / Debate / ToT / Research v2 / Approval 任何代码

---

## 4. 不做的事情（强制约束）

| 编号 | 约束                                                                                                                                       |
| ---- | ------------------------------------------------------------------------------------------------------------------------------------------ |
| 1    | **不重写 Router**（AdvancedRoutingWorkflow + dispatchByMode 函数体只做最小补充）                                                           |
| 2    | **不绕过 Approval Gate**（生产默认 `ROUTER_REQUIRE_APPROVAL=true` 不变）                                                                   |
| 3    | **不重写 Reflection / ToT / Debate / Research v2** 的 workflow 或 activity                                                                 |
| 4    | **不修改 Shannon 任何只读目录**                                                                                                            |
| 5    | **不强依赖 LLM classifier**（代码路径存在但默认 false；开启也只在 `REAL_ROUTER_CLASSIFIER_TEST=1` 时生效）                                 |
| 6    | **不让 Router 每次都多调用一个昂贵模型**（heuristic 必须 < 5ms，LLM classifier 调用频率受 config 限制）                                    |
| 7    | **不破坏 async result API**（执行路径不变；只增加 metadata 字段）                                                                          |
| 8    | **不进入 Phase 8**（不碰 SDK / CLI / Multi-tenant / Auth / OpenAI-compatible）                                                             |
| 9    | **不改 Workspace / Audit 既有 Activity**（router decision audit 复用现有 `AuditRoutingDecisionActivity`；通过新增 `016_routing_signals.sql` migration 给 `routing_audit_logs` 加列，**不要修改旧 010 migration**） |
| 10   | **Router 不直接 HTTP / DB / LLM**（保持 deterministic；LLM classifier 路径必须是 Activity 包装）                                           |
| 11   | **不破坏 Slice 23-28 任何 Go test**                                                                                                        |
| 12   | **新代码必须有 telemetry 信号字段写入 audit trail**（不能默默上线）                                                                        |

---

## 5. 数据结构设计

> 设计阶段文档。**不实现代码**。所有新 struct 放入 `internal/types/router_v2.go`（新文件）以与原 `router.go` 隔离。

### 5.1 `RouterDecisionSignals`（输入层）

```go
type RouterDecisionSignals struct {
    // --- 文本信号（来自 query / intent）---
    QueryLength       int     // 字符数
    QueryCharCount    int     // 去除空白后的字符数
    HasNumberedList   bool    // 含 "1. 2." 列表
    HasMultiSentence  bool    // 多句（>=2 个句号）
    UserIntent        string  // "deep_research" | "compare" | "evaluate" | "" 等

    // --- 复杂度 sub-signals（取代单值 complexity_score）---
    ComplexityAnalyze    float64  // 分析/对比/优化类
    ComplexityResearch   float64  // 研究/证据/引用类
    ComplexityExecution  float64  // 沙箱/代码执行类
    ComplexityMultiAgent float64  // 多 agent / 团队 / swarm
    ComplexityDebate     float64  // 正反观点/比较
    ComplexityExploration float64 // 多路径 / search tree / ToT 类
    ComplexityOverall   float64  // 加权合成

    // --- 能力需求（已被 DetectTaskCapabilitiesActivity 计算）---
    RequiresTools    bool
    RequiresRAG      bool
    RequiresResearch bool
    RequiresSandbox  bool

    // --- 用户约束（从 RouteRequest）---
    AllowTools     bool
    AllowSandbox   bool
    AllowResearch  bool
    RequireCitations bool
    BudgetUSD      float64
    MaxLatencyMs   int

    // --- 风险信号（从 tokens / cost / 风险关键词）---
    RiskKeywords   []string  // "execute untrusted code" / "delete" / "production" 等
    RiskLevel      string    // "low" | "medium" | "high" | "critical"（从 policy 映射）

    // --- 政策版本（用于 telemetry）---
    PolicyVersion  string
    ClassifierUsed bool    // LLM classifier 是否被调用
}
```

### 5.2 `RouterDecisionExplanation`（输出层，替代/补充 `Reason` 字符串）

```go
type RouterDecisionExplanation struct {
    SelectedMode   types.RoutingMode
    SelectedReason string            // 1~2 句自然语言
    ScoreBreakdown map[types.RoutingMode]float64  // 每个 mode 的最终分
    Candidates     []ModeCandidate   // 排序后的候选（含拒绝原因）
    Signals        RouterDecisionSignals  // 完整信号快照
    PolicyVersion  string
}
```

### 5.3 `ModeCandidate`（中间层）

```go
type ModeCandidate struct {
    Mode          types.RoutingMode
    Score         float64        // 0~1 聚合分
    CostEstimate  float64        // 估算 token cost
    LatencyTier   string         // "fast" | "medium" | "slow"
    SignalsHit    []string       // 命中的信号名（["research.requires_citations", "tool.has_sandbox"]）
    Rejected      bool
    RejectReason  RejectedModeReason
}
```

### 5.4 `RejectedModeReason`（拒绝原因枚举 + 字符串）

```go
type RejectedModeReason string

const (
    RejectDisabledByConfig   RejectedModeReason = "disabled_by_config"
    RejectBlockedByAllow      RejectedModeReason = "blocked_by_user_allow_flag"
    RejectCostOverBudget      RejectedModeReason = "cost_exceeds_budget"
    RejectLatencyOverMax      RejectedModeReason = "latency_exceeds_max"
    RejectNoCapability        RejectedModeReason = "missing_capability"
    RejectNoSignalMatch       RejectedModeReason = "no_signal_match"
    RejectRiskTooLow          RejectedModeReason = "risk_below_threshold"
    RejectAddonConflict       RejectedModeReason = "addon_conflict"
    RejectFallbackFrom        RejectedModeReason = "fallback_from_higher_priority"
)
```

### 5.5 `CapabilityNeeds`（规范化，替代 Capabilities 散落字段）

```go
type CapabilityNeeds struct {
    NeedsTools    bool
    NeedsRAG      bool
    NeedsSandbox  bool
    NeedsResearch bool  // 多源/引用/证据
    NeedsCitation bool
    NeedsWebSearch bool
}
```

### 5.6 `BudgetLatencySignals`（独立信号）

```go
type BudgetLatencySignals struct {
    BudgetUSD          float64
    EstimatedCostUSD   float64
    BudgetHeadroom     float64  // budget - estimated_cost
    MaxLatencyMs       int
    EstimatedLatencyMs int
    LatencyHeadroomMs  int
    LatencyTight       bool    // estimated_latency > 0.7 * max_latency
}
```

### 5.7 `PolicyConfig`（policy 文件解析结果）

```go
type PolicyConfig struct {
    Version      string
    ModeThresholds   ModeThresholds
    RiskThresholds   RiskThresholds
    BudgetThresholds BudgetThresholds
    KeywordWeights   map[string][]string  // mode_name -> keywords
    SignalWeights    SignalWeights
    ModeOrder        []types.RoutingMode   // 优先级（fallback 顺序）
    ModeFeatureFlags map[types.RoutingMode]bool  // 默认 enabled
    FallbackMode     types.RoutingMode
    ClassifierConfig ClassifierConfig
    Telemetry        TelemetryConfig
}
```

### 5.8 `PolicyWeights`（每个 mode 一组分信号权重）

```go
type PolicyWeights struct {
    DirectAnswer  ModeWeights
    ReActTool     ModeWeights
    RAGAnswer     ModeWeights
    DAGWorkflow   ModeWeights
    Reflection    ModeWeights
    TreeOfThoughts ModeWeights
    Debate        ModeWeights
    ResearchV2    ModeWeights
    SwarmWorkflow ModeWeights
    SandboxExecution ModeWeights
}

type ModeWeights struct {
    Base float64
    Analyze        float64
    Research       float64
    Execution      float64
    MultiAgent     float64
    Debate         float64
    Exploration    float64
    NeedsTools     float64
    NeedsRAG       float64
    NeedsSandbox   float64
    NeedsResearch  float64
    NeedsCitation  float64
    ComplexityOverall float64
    CostMultiplier float64  // 成本惩罚（cost/budget 比例）
    LatencyMultiplier float64
    RiskPenalty   float64
}
```

### 5.9 信号→分数 聚合公式

```
score(mode) = Base(mode)
            + W_analyze * ComplexityAnalyze
            + W_research * ComplexityResearch
            + ...
            + W_needsSandbox * (RequiresSandbox ? 1 : 0)
            ...
            - cost_multiplier * (estimated_cost / budget)   // 预算惩罚
            - latency_multiplier * (estimated_latency / max_latency)  // 延迟惩罚
            - risk_penalty * (mode == SandboxExecution ? risk_level_numeric : 0)
```

`score` 限制在 `[0, 1]`，候选按 score 排序，禁用 / 用户禁用 / 预算爆 / 延迟爆 的 mode 直接 reject 不参与排序。

---

## 6. policy 配置设计

> 配置文件 `config/router_policy.yaml` 已存在但是**占位骨架**。本阶段做最小补齐：增加 `signal_weights / mode_thresholds / capability_requirements / fallback_order / disabled_behavior` 等关键块。

### 6.1 顶层结构

```yaml
version: "1.0"   # 增加，每次 policy 改变必须 +minor 递增；用于 audit trail

# ── 模式阈值（complexity） ──
thresholds:
  direct_answer_max:   0.15
  rag_min:             0.10
  react_min:           0.20
  dag_min:             0.35
  reflection_min:      0.30
  tree_of_thoughts_min: 0.55
  debate_min:          0.35
  debate_max:          0.60
  research_v2_min:     0.50
  swarm_min:           0.65
  sandbox_min_risk:    "high"   # sandbox 至少需要 high 才推荐

# ── 风险分类 ──
risk:
  low_max:      0.30
  medium_max:   0.60
  high_max:     0.85
  critical_min: 0.85
  require_approval_min: "high"

# ── 预算 ──
budget:
  cheap_max_usd:    0.05
  medium_max_usd:   0.25
  expensive_min_usd: 0.50
  # 每个 mode 的估算 USD（heuristic 初值；Phase 8 可改 model-aware）
  estimated_cost_usd:
    direct_answer:    0.005
    rag_answer:       0.02
    react_tool:       0.03
    dag_workflow:     0.08
    reflection:       0.06
    tree_of_thoughts: 0.18
    debate:           0.12
    research_v2:      0.20
    swarm_workflow:   0.30
    sandbox_execution: 0.05

# ── 延迟 ──
latency:
  fast_max_ms:     5000
  medium_max_ms:   30000
  slow_min_ms:     60000
  # 每个 mode 的估算延迟
  estimated_latency_ms:
    direct_answer:    2000
    rag_answer:       5000
    react_tool:       8000
    dag_workflow:     20000
    reflection:       30000
    tree_of_thoughts: 90000
    debate:           60000
    research_v2:      120000
    swarm_workflow:   180000
    sandbox_execution: 30000

# ── 关键词权重（每个 mode） ──
keywords:
  debate:
    weight_per_match: 0.20
    keywords: ["比较", "对比", "vs ", "versus", "debate", "pros and cons",
               "trade-off", "which is better", "compare", "view", "opinion",
               "argument", "for and against"]
  tree_of_thoughts:
    weight_per_match: 0.25
    keywords: ["explore multiple", "multi-path", "branch", "best path",
               "search tree", "explore paths", "find best approach",
               "analyze paths", "multiple perspectives", "branching",
               "tree of thought", "explore both", "explore alternatives"]
  research_v2:
    weight_per_match: 0.25
    keywords: ["研究", "分析", "报告", "evidence", "citation", "sources",
               "literature", "review", "investigate", "study", "comprehensive",
               "with citations", "according to"]
  sandbox_execution:
    weight_per_match: 0.30
    keywords: ["执行", "运行", "沙箱", "代码", "编译", "测试", "execute", "run ",
               "sandbox", "python code", "script", "compile", "wasi",
               "untrusted code", "execute code"]
  swarm_workflow:
    weight_per_match: 0.30
    keywords: ["多agent", "多角色", "团队", "分工", "协作", "swarm",
               "multi-agent", "team of agents", "collaborat", "multiple agents"]
  reflection:
    weight_per_match: 0.15
    keywords: ["分析", "评估", "改进", "analyze", "evaluate", "improve",
               "refine", "critique", "polish"]
  dag_workflow:
    weight_per_match: 0.15
    keywords: ["步骤", "step", "sequence", "workflow", "pipeline", "first then"]

# ── 信号权重（per mode） ──
signal_weights:
  direct_answer:    { base: 0.50 }
  react_tool:       { base: 0.10, needs_tools: 0.30, complexity_overall: 0.30 }
  rag_answer:       { base: 0.10, needs_rag: 0.40, complexity_overall: 0.20 }
  dag_workflow:     { base: 0.05, needs_tools: 0.20, complexity_overall: 0.30 }
  reflection:       { base: 0.10, analyze: 0.30, complexity_overall: 0.30 }
  tree_of_thoughts: { base: 0.05, exploration: 0.35, complexity_overall: 0.40 }
  debate:           { base: 0.10, debate: 0.40, complexity_overall: 0.20 }
  research_v2:      { base: 0.10, research: 0.35, needs_citation: 0.25, complexity_overall: 0.20 }
  swarm_workflow:   { base: 0.05, multi_agent: 0.40, complexity_overall: 0.30 }
  sandbox_execution: { base: 0.05, execution: 0.50, needs_sandbox: 0.40, risk_penalty: 0.30 }

# ── 能力需求门槛 ──
capability_requirements:
  react_tool:        requires_tools
  rag_answer:        requires_rag
  dag_workflow:      requires_tools
  sandbox_execution: requires_sandbox
  research_v2:       requires_research
  # (没有 requires_* 的 mode = 默认假设; reflection/tot/debate 不需要硬 capability)

# ── Fallback 顺序（当 preferred mode 不可用） ──
fallback_order:
  - sandbox_execution → []              # sandbox 无 fallback（强安全）
  - research_v2:      [dag_workflow, reflection]
  - tree_of_thoughts: [debate, reflection]
  - debate:           [reflection, tree_of_thoughts]
  - swarm_workflow:   [dag_workflow, reflection]
  - dag_workflow:     [react_tool, reflection]
  - reflection:       [tree_of_thoughts, react_tool, direct_answer]
  - react_tool:       [direct_answer]
  - rag_answer:       [direct_answer]
  - direct_answer:    []

# ── Disabled mode 行为 ──
disabled_behavior:
  sandbox_execution:
    when_disabled:    mode_disabled       # 安全优先，无 fallback
    when_user_blocks: mode_disabled       # 同样
  research_v2:
    when_disabled:    research_v1         # 实际 fallback 到 Phase 6F
    when_user_blocks: direct_answer
  tree_of_thoughts:
    when_disabled:    reflection
    when_user_blocks: direct_answer
  debate:
    when_disabled:    reflection
    when_user_blocks: direct_answer
  swarm_workflow:
    when_disabled:    dag_workflow
    when_user_blocks: direct_answer
  reflection:
    when_disabled:    tree_of_thoughts    # 已有逻辑保留
    when_user_blocks: direct_answer
  dag_workflow:
    when_disabled:    react_tool
    when_user_blocks: direct_answer
  react_tool:
    when_disabled:    direct_answer
    when_user_blocks: direct_answer
  rag_answer:
    when_disabled:    direct_answer
    when_user_blocks: direct_answer
  direct_answer:
    when_disabled:    direct_answer       # always available
    when_user_blocks: direct_answer

# ── 模式默认开关（feature flags） ──
mode_feature_flags:
  reflection:       true
  tree_of_thoughts: true
  debate:           true
  research_v2:      true
  sandbox_execution: true   # 实际由 require_approval 共同管理

# ── Approval hints ──
approval_hints:
  # 哪些 mode 在生产默认下会触发 approval gate
  auto_approval_modes: [sandbox_execution]   # sandbox 总是要 approval
  # 哪些 mode 在低风险 / 低预算时可以跳过 approval
  skip_approval_when:
    - mode: direct_answer
      max_complexity: 0.30
    - mode: rag_answer
      max_complexity: 0.30

# ── LLM classifier（默认关闭） ──
classifier:
  enabled: false
  real_test_only: true     # 只在 REAL_ROUTER_CLASSIFIER_TEST=1 时启用
  model_tier: small
  max_tokens: 400
  # 命中下列条件时跳过 LLM classifier（heuristic 已足够确定）
  skip_when:
    - heuristic_score_gap > 0.30   # 最高分与次高分差距大时直接选
    - budget_usd < 0.01           # 预算太小时不调用 classifier
  invoke_only_when:           # 满足这些才调用
    - complexity_in: [0.20, 0.60]  # 复杂度中段才不确定
    - no_clear_winner: true
  prompt_template: |
    Score each mode 0-1 for the query: {query}
    Modes: {mode_list}
    Output STRICT JSON: {scores: {mode: score, ...}}

# ── Telemetry ──
telemetry:
  log_routing_decisions: true
  record_signals:        true     # 完整 signals 写入 audit
  record_explanation:    true     # 完整 candidates + reasons 写入 audit
  audit_policy_version:  true
  emit_event:            router_decision_emitted   # SSE 事件名

# ── Legacy 兼容 ──
legacy:
  # 当 ROUTER_LEGACY_HEURISTIC=true 时使用旧 selectPlannedMode if/else 链
  # 用于紧急回滚或 A/B 对比
  enabled: false
  env_override: ROUTER_LEGACY_HEURISTIC
```

---

## 7. 路由规则设计（per mode）

| Mode                | 适合什么任务           | 需要哪些信号                                | 什么时候不应该选                  | Fallback                        | Approval     |
| ------------------- | ---------------------- | ------------------------------------------- | --------------------------------- | ------------------------------- | ------------ |
| `direct_answer`     | 单句事实问答           | 短 query + 低 complexity                    | 需要 tool / RAG / 深度推理 / 引用 | （永远可用）                    | 永不需要     |
| `react_tool`        | 工具调用 / 计算 / 搜索 | RequiresTools + 短-中 complexity            | 多步 / 多 agent / 引用            | `direct_answer`                 | 中等以下不   |
| `rag_answer`        | 知识库问答             | RequiresRAG                                 | 复杂多步 / 沙箱                   | `direct_answer`                 | 否           |
| `dag_workflow`      | 明确步骤 / pipeline    | RequiresTools + 中高 complexity             | 单句 / 简单问答                   | `react_tool` `reflection`       | 中等可能     |
| `reflection`        | 分析/改进/草稿/评稿    | analyze 信号 + 中 complexity                | 短 query / 不需要反思             | `tree_of_thoughts` `react_tool` | 视复杂度     |
| `tree_of_thoughts`  | 多路径 / 搜索解空间    | exploration 信号 + 高 complexity            | 简单任务 / 短 query               | `debate` `reflection`           | 视复杂度     |
| `debate`            | 正反观点 / 比较 / vs   | debate 信号 + 中 complexity                 | 简单 / 高 risk                    | `reflection` `tree_of_thoughts` | 视复杂度     |
| `research_v2`       | 多源资料 / 引用 / 证据 | research / citation 信号 + 中-高 complexity | 单句 / 短                         | `dag_workflow` `reflection`     | 视复杂度     |
| `swarm_workflow`    | 多 agent 协作          | multi_agent 信号 + 很高 complexity          | 简单 / 短                         | `dag_workflow` `reflection`     | 高可能       |
| `sandbox_execution` | 沙箱代码执行           | RequiresSandbox + 中-高 complexity          | 普通问答                          | **无 fallback**（安全优先）     | **总是需要** |

---

## 8. 实施步骤

> 每步独立可测、可回滚、可灰度。

### Step 1：新增 `internal/types/router_v2.go`（只 types，无 logic）

**改动：**
- 新增 `RouterDecisionSignals` / `RouterDecisionExplanation` / `ModeCandidate` / `RejectedModeReason` / `CapabilityNeeds` / `BudgetLatencySignals` / `PolicyConfig` / `PolicyWeights` / `ModeWeights` / `SignalWeights` / `ClassifierConfig` / `TelemetryConfig` / `ModeThresholds` / `RiskThresholds` / `BudgetThresholds` / `LatencyThresholds` / `CapabilityRequirement` / `DisabledBehavior` / `ApprovalHint` 等
- 新增 `LegacyConfig` 用于 legacy 回滚
- **不改任何 workflow / activity**

**新增/修改文件：**
- `internal/types/router_v2.go`（新文件）

**风险：** 无（纯类型）

**验收标准：**
- `go build ./...` 通过
- `go vet ./...` 0
- 不影响现有任何类型

---

### Step 2：policy loader（从 yaml → PolicyConfig）

**改动：**
- 新增 `internal/config/router_policy.go`（已存在 `config/router_policy.yaml` 骨架，扩展）
- `LoadRouterPolicy(path string) (*PolicyConfig, error)`
- `DefaultRouterPolicy() *PolicyConfig`（fallback：policy 加载失败时）
- `ValidatePolicyConfig(cfg *PolicyConfig) error`（拒绝 crazy values）

**新增/修改文件：**
- `internal/config/router_policy.go`（新文件）
- `config/router_policy.yaml`（补齐 6.1 的全部字段）

**风险：**
- YAML 解析失败导致 gateway panic — 必须用 DefaultRouterPolicy() 兜底
- Policy 字段写错导致所有 mode 不可用 — ValidatePolicyConfig 限制阈值范围

**验收标准：**
- `LoadRouterPolicy` 在文件不存在 / 解析失败时返回 DefaultRouterPolicy
- 单元测试覆盖 happy path / 缺字段 / 错误值

---

### Step 3：在 `EvaluateRoutingPolicyActivity` 内构造 `RouterDecisionSignals`

**改动：**
- 新增 `BuildDecisionSignals(input EvaluateRoutingPolicyInput) RouterDecisionSignals`（pure function，**不调用** activity，不读 DB）
- `BuildDecisionSignals` 内部把当前 `classifyHeuristic` 拆成 6 个 sub-signals（analyze / research / execution / multi_agent / debate / exploration）
- `BuildBudgetLatencySignals(input) BudgetLatencySignals`（估算 latency / cost，引用 policy 表格）

**修改文件：**
- `internal/activities/router.go`（新增 helper 函数）

**风险：**
- Signals 计算逻辑 bug 导致分数偏差 → 必须保留旧 `ComplexityScore` 不变，signals 是**额外**输出

**验收标准：**
- `BuildDecisionSignals` 单测覆盖各种 query
- 旧 selectPlannedMode 仍用旧 `ComplexityScore`，行为不变

---

### Step 4：引入 Policy scoring（多 mode 候选评分）

**改动：**
- 新增 `ScoreModes(signals, policy, capabilities, budget) []ModeCandidate`（pure function）
- 每个 mode 算 base + sum(signal_weight * signal_value) - cost_penalty - latency_penalty - risk_penalty
- 拒绝 disabled / user-blocked / over-budget / over-latency 的 mode（RejectReason 记录）
- 排序：分数 desc；top-1 设为 selected，其余放入 candidates

**修改文件：**
- `internal/activities/router.go`

**风险：**
- 评分权重失衡导致 Router 偏袒某个 mode → A/B 测试 + 回退到 `ROUTER_LEGACY_HEURISTIC=true`
- 任何 mode 在所有场景下都得 0 分 → DefaultPolicy 保证 base > 0

**验收标准：**
- 单测覆盖 20+ query 样本
- 所有 mode 至少在某个样本中被 selected
- `disabled_behavior` 完整生效

---

### Step 5：替换 selectPlannedMode 内部实现（保留入口）

**改动：**
- `selectPlannedMode(input)` **函数签名不变**；内部从 if/else 链改为 `ScoreModes → 排序 → top-1`
- `selectPlannedMode` 仍返回 `types.RoutingMode`（不改）
- 内部是否走 LLM classifier 由 `policy.classifier.enabled` 或 `RouterConfigSnapshot.EnableRouterClassifier` 控制（**与 Approval Gate 严格分离**；Approval Gate 由 `ROUTER_REQUIRE_APPROVAL` / `RouterConfigSnapshot.RequireApproval` 控制，**绝不共用**）
- 末尾 emit `RouterDecisionExplanation` 到 `RoutedExecutionResult.Reason`（**新增**字段，详见 Step 7）

**修改文件：**
- `internal/activities/router.go`

**风险：**
- 行为偏差导致 Debate / ToT / Research v2 smoke 失败 → 单元测试 + 端到端 smoke
- 任何 mode 选错 → fallback 到 legacy 模式

**验收标准：**
- `go test ./...` 全过（含所有原 Slice 25-28 测试）
- 全部 mock smoke 不破坏
- Real LLM smoke 不破坏

---

### Step 6：改进 ResolveExecutedMode（fallback_reason 完整）

**改动：**
- 当前 `resolveExecutedMode` 只在 reflection→tot 写 fallback_reason；补齐所有 mode：
  - ToT 关闭 → fallback 到 debate 或 reflection + reason
  - debate 关闭 → fallback 到 reflection
  - research_v2 关闭 → fallback 到 dag_workflow / reflection
  - swarm 关闭 → fallback 到 dag_workflow
  - sandbox 关闭 → mode_disabled（无 fallback，安全优先）
- 每个 case 返回 reason 字符串进入 `decision.FallbackReason`

**修改文件：**
- `internal/activities/router.go`

**风险：**
- 默认 fallback 路径变化影响 Debate / ToT 路由 → smoke 必须通过

**验收标准：**
- 单测覆盖 9 个 mode × 关闭/启用 组合
- Smoke 不破

---

### Step 7：扩展 `RoutedExecutionResult.Reason` 为结构化 explanation

**改动：**
- 在 `types.RoutedExecutionResult` 加 `Explanation *RouterDecisionExplanation`（omitempty）
- `PersistRoutedExecutionResultActivity` 写 `tasks.metadata.explanation` JSON
- `GetTaskResult` 读 explanation 回 API
- `ExecuteRoutedResponse` / `GetTaskResult` 不变（兼容旧 client 忽略 explanation）

**修改文件：**
- `internal/types/router.go`
- `internal/activities/router.go`
- `internal/api/router_handlers.go`（仅 GetTaskResult 加上 explanation 字段）

**风险：**
- tasks.metadata 已有结构变化 → 既有 smoke 仍能 PASS（只看 result.mode 等基础字段）
- 序列化的 RouterDecisionExplanation 太大 → 仅 summary 进 metadata，详细进 Workspace ref（v2 enhancement）

**验收标准：**
- 解释字段非 nil，且 selected_mode / candidates 完整
- 不破坏 async result API

---

### Step 8：telemetry / audit 接入

**改动：**
- `migrations/016_routing_signals.sql`（**只**给 `routing_audit_logs` 加 `signals JSONB` + `explanation JSONB` + `policy_version VARCHAR(20)` + `selected_mode VARCHAR(50)` + `candidates JSONB`）
- `AuditRoutingDecisionActivity` 输入加上述字段
- 真实 LLM classifier 路径（**默认 false**）：新增 `LLMClassifierActivity` 包装 LLM 调用（**复用** `AgentActivity.CallLLM`），不新写 LLM client
- 端到端：router workflow 在 selectPlannedMode 之后写 audit；audit row 现在含 signals + explanation

**修改文件：**
- `migrations/016_routing_signals.sql`（新文件，IF NOT EXISTS 安全）
- `internal/activities/router.go`（AuditRoutingDecisionActivity 增字段）
- `internal/workflows/router.go`（透传 signals 到 audit call）
- `internal/api/router_handlers.go`（`GET /api/v1/tasks/{id}/audit` 新接口，可读 signals + explanation；**只在 audit data 存在时输出**）

**风险：**
- 现有 audit 表大 → JSONB 列不破坏旧 schema
- 真实 LLM classifier 路径如果启用 → cost / latency 增加 → 默认 false 即可

**验收标准：**
- audit row 包含 signals + explanation
- `GET /api/v1/tasks/{id}/audit` 返回 signals + explanation（如果存在）
- 不破坏 Debate / ToT / Research v2 既有 audit

---

### Step 9：测试 + smoke

**改动：**
- 新增 `internal/activities/router_v2_test.go`（覆盖 7 个 step 的所有函数 + signal/case matrix）
- 新增 `scripts/test_router_strategy_smoke.sh`（10+ 场景）
- 新增 `scripts/test_router_strategy_regression.sh`（旧 mock smoke 兼容）
- 更新 `scripts/test_advanced_router_smoke.sh`（如果存在）兼容新 behavior

**风险：**
- 新测试过严导致现有 smoke false positive → 严格按 Phase 7 任务书 §十一 列表写

**验收标准：**
- 单测覆盖 20+ query × 8 mode 矩阵
- mock smoke ≥ 10 PASS
- 不破旧 mock smoke

---

## 9. 测试计划

### 9.1 单元测试

- `internal/activities/router_v2_test.go`:
  - `TestBuildDecisionSignals` 6+ query 覆盖
  - `TestScoreModes` 各种 signal 组合 → 验证 selected_mode 符合预期
  - `TestRejectWhenAllowFalse` allow_tools=false → react_tool/dag_workflow 被 reject
  - `TestRejectWhenBudgetTooLow` budget=0.001 → 排除 research_v2/tot
  - `TestRejectWhenLatencyTight` max_latency=1000ms → 排除 tot/debate/research_v2
  - `TestRequireCitationsBoostsResearchV2` require_citations=true → research_v2 分数显著高
  - `TestDisabledModeFallsBack` enable_research_v2=false → research_v2 → dag_workflow
  - `TestLegacyEnvOverride` ROUTER_LEGACY_HEURISTIC=true → 旧 if/else 链
  - `TestSandboxAlwaysRequiresApproval` sandbox_execution → requires_approval=true
  - `TestPolicyVersionBumpedInTelemetry` policy.version 写入 audit
- `internal/config/router_policy_test.go`:
  - `TestLoadRouterPolicyValid` / `TestLoadRouterPolicyMissing` / `TestLoadRouterPolicyInvalid`
  - `TestValidatePolicyConfig` reject crazy values
- `internal/workflows/router_v2_test.go`:
  - selectPlannedMode 行为兼容（existing 测试无修改）
  - ResolveExecutedMode 完整 fallback 链路
- `internal/api/router_v2_test.go`:
  - GetTaskResult 返回 explanation

### 9.2 Smoke 测试

`scripts/test_router_strategy_smoke.sh`（新增）：

1. 简单问题 → direct_answer
2. 工具调用 → react_tool
3. 多步执行 → dag_workflow
4. 需要反思 → reflection
5. 多路径比较 → tree_of_thoughts
6. 正反观点 → debate
7. 引用/研究 → research_v2
8. 沙箱执行 → sandbox_execution + requires_approval=true
9. allow_research=false → 任何 query 都不能选 research_v2
10. allow_sandbox=false → 任何 query 都不能选 sandbox
11. require_citations=true + 复杂 → research_v2
12. disabled mode 行为（ENABLE_RESEARCH_V2=false → fallback）
13. 预算极低（0.001）→ 降级到 direct_answer
14. latency 极紧（500ms）→ 跳过 tot/debate/research_v2
15. Audit trail 含 signals + explanation

`scripts/test_router_strategy_regression.sh`（新增）：

- 跑 `test_debate_smoke.sh` / `test_tot_smoke.sh` / `test_research_v2_smoke.sh` / `test_advanced_router_smoke.sh`（如存在），确保不破坏

### 9.3 Real LLM 测试

- `REAL_LLM_TEST=1 REAL_ROUTER_CLASSIFIER_TEST=1 ./scripts/test_router_classifier_real_llm_smoke.sh`（如实现 classifier 路径；如未实现只跑 mock 部分 + 报告说明）

### 9.4 关键回归

| 测试                                                                                                                                                      | 必须通过                       |
| --------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------ |
| `go test ./internal/api ./internal/activities ./internal/types ./internal/workflows`                                                                      | PASS                           |
| `go build ./cmd/gateway ./cmd/worker`                                                                                                                     | 0                              |
| `go vet ./...`                                                                                                                                            | 0                              |
| `./scripts/test_debate_smoke.sh`                                                                                                                          | 4/4 PASS                       |
| `./scripts/test_tot_smoke.sh`                                                                                                                             | 3/3 PASS                       |
| `./scripts/test_research_v2_smoke.sh`                                                                                                                     | 4/4 PASS                       |
| `ROUTER_REQUIRE_APPROVAL=false REAL_LLM_TEST=1 REAL_DEBATE_TEST=1 REAL_DEBATE_TEST_MAX_COST_USD=1 ./scripts/test_debate_real_llm_smoke.sh`                | 10/10 PASS                     |
| `ROUTER_REQUIRE_APPROVAL=false REAL_LLM_TEST=1 REAL_RESEARCH_V2_TEST=1 REAL_RESEARCH_V2_TEST_MAX_COST_USD=1 ./scripts/test_research_v2_real_llm_smoke.sh` | 7/7 PASS                       |
| `ROUTER_REQUIRE_APPROVAL=false REAL_LLM_TEST=1 REAL_TOT_TEST=1 REAL_TOT_TEST_MAX_COST_USD=1 ./scripts/test_tot_real_llm_smoke.sh`                         | 8/8 PASS                       |
| `./scripts/test_approval_async_smoke.sh`                                                                                                                  | 包含 waiting_for_approval 路径 |

---

## 10. 风险和回滚策略

### 10.1 新 Router 误分流风险

- **现象**：query 应该选 debate 但新 policy 选了 reflection
- **检测**：Step 9 的 20+ 矩阵单测 + Router strategy smoke
- **回滚**：在测试环境运行 `ROUTER_LEGACY_HEURISTIC=true`，旧 `selectPlannedMode` if/else 链立即生效
- **生产回滚**：env 切换 + gateway 重启（约 1 分钟），无状态丢失（task_id 仍然在 tasks row）

### 10.2 成本升高风险（LLM classifier 默认 false 已经规避，但需监控）

- 监控：每次 router decision audit 都记录 `classifier_used` 字段
- 关闭：env `ROUTER_CLASSIFIER_DISABLED=1`（如需要硬关）
- 限流：classifier invoke_only_when 严格条件

### 10.3 影响生产 Approval

- 新 Router **不修改** Approval Gate 行为
- `ExecuteApprovalPolicy` 的 RequireApproval 默认 true 不变
- 误触发 approval 的风险 → smoke test 覆盖

### 10.4 数据迁移风险（migration 016）

- 016 只加列（不删除/重命名），IF NOT EXISTS 保护
- 旧 row 的 signals / explanation 列是 NULL，GetTaskResult 必须容忍 nil
- 不需要 backfill

### 10.5 不破坏 async result API

- Router workflow 内部行为变化，但 routuexecutionresult JSON shape 兼容（只增字段）
- ExecuteRoutedResponse / GetTaskResult response 增加 explanation 字段（omitempty）
- 旧 client 忽略新字段，不破

---

## 11. 最终验收标准

### 11.1 必须运行

```bash
# 1. 单元 + Go test
go test ./internal/api ./internal/activities ./internal/types ./internal/workflows ./internal/config -count=1
go test ./... -count=1

# 2. Build + vet
go build ./cmd/gateway ./cmd/worker
go vet ./...

# 3. 全部 mock smoke
./scripts/test_debate_smoke.sh
./scripts/test_tot_smoke.sh
./scripts/test_research_v2_smoke.sh
./scripts/test_approval_async_smoke.sh
./scripts/test_router_strategy_smoke.sh
./scripts/test_router_strategy_regression.sh

# 4. 全部真实 LLM smoke (async polling, 已有)
ROUTER_REQUIRE_APPROVAL=false REAL_LLM_TEST=1 REAL_DEBATE_TEST=1 REAL_DEBATE_TEST_MAX_COST_USD=1 ./scripts/test_debate_real_llm_smoke.sh
ROUTER_REQUIRE_APPROVAL=false REAL_LLM_TEST=1 REAL_RESEARCH_V2_TEST=1 REAL_RESEARCH_V2_TEST_MAX_COST_USD=1 ./scripts/test_research_v2_real_llm_smoke.sh
ROUTER_REQUIRE_APPROVAL=false REAL_LLM_TEST=1 REAL_TOT_TEST=1 REAL_TOT_TEST_MAX_COST_USD=1 ./scripts/test_tot_real_llm_smoke.sh
```

### 11.2 判定 Phase 7I 完成的标准

1. ✅ 全部 Step 1-9 完成
2. ✅ go test / go build / go vet 全通过
3. ✅ 全部 mock smoke（debate 4/4、tot 3/3、research_v2 4/4、approval async、router strategy 10+、router regression）通过
4. ✅ 全部真实 LLM smoke（Debate 10/10、Research v2 7/7、ToT 8/8）通过
5. ✅ router audit row 含 signals + explanation 字段
6. ✅ `ROUTER_LEGACY_HEURISTIC=true` 紧急回滚路径可用
7. ✅ 至少 5 个不同 mode 在新 Router 下被选中（在测试矩阵中验证）
8. ✅ disabled mode 行为按 policy 配置正确降级
9. ✅ 不动 Shannon / 不进入 Phase 8 / 不绕过 Approval Gate

### 11.3 不算 Phase 7I 完成

- 仅修改 policy yaml 但 Router 没真的用 signals 评分
- 只跑通 1-2 个 mock smoke
- 真实 LLM smoke 没全过
- LLM classifier 默认开启（违反约束 5）

---

**等待用户确认后再开始实现。**
---

# Phase 7I Router Strategy Upgrade — 修订版计划书（v2）

> 本计划书是 v1 的修订版。重点补充 **Capability Composition（既有能力串联）**、**Frontend API Contract**、**修订版政策配置** 三章。
> 计划阶段文档，不实现任何代码。
> 用户确认后再开始实现。

---

## 0. v2 主要变化

| 章节 | v1 → v2 变化 |
|------|------------|
| §1.1 现状分析 | 新增 "Capability 串联" 维度（v1 只看 mode 选择，v2 看能力组合） |
| §2 Shannon 参考 | 明确 "Tool/RAG/Sandbox/Skills/MCP 是 capability，不是 mode 实体" |
| §3 改造目标 | 新增 **AddonCapabilities + RequiredCapabilities + WorkspaceArtifactsExpected** 输出 |
| **§4 能力组合** | **v2 新增整章** — 详细说明 RAG/Sandbox/Skills/MCP/Workspace/Approval/Async/Audit/Provider/Web 9 个能力如何被 Router 串起来 |
| **§5 Frontend Contract** | **v2 新增整章** — Router 输出稳定 JSON contract，前端可直接消费 |
| §6 不做 | 新增 "不把 RAG/Sandbox/Skills/MCP 执行逻辑塞进 Router" |
| §7 数据结构 | 新增 AddonCapabilities / FrontendRouterContract |
| §8 policy yaml | 增 addons / forbidden_combinations / classifier 控制分离 |
| §9 路由规则 | 每个 mode 新增 "可组合 addon / 前端如何解释" |
| §10 实施步骤 | Step 5 = Capability Composition（v2 重点） |
| §11 测试 | 新增 addon capabilities + forbidden combinations + frontend contract |

---

## 1. 当前 Cribug Router 现状分析（v2 补充）

### 1.0 Router 的真实角色定位

**Router 应该是 "能力组合编排器"，不是 "mode 选择器"。**

它必须回答 4 个问题：
1. **主执行路径**是哪个 mode？
2. **附加能力**（RAG / Sandbox / Skills / MCP / Citations / Audit / Workspace / Approval）需要哪些？
3. **执行计划**是否需要 async？是否需要 approval？需要哪些 workspace artifacts？
4. **前端 / 调试如何解释**？为什么选这个 mode？哪些 mode 被拒绝？

### 1.1~1.5（沿用 v1）

> 详见 v1 §1.1~1.5
> **v2 补充：当前 RAG / Sandbox / Skills / MCP / Workspace / Audit 全部是孤立功能**

| 能力 | 当前是否被 Router 组合？ | 实际行为 |
|------|----------------------|---------|
| RAG / Qdrant | **否** — Router 只能选 `RouteRAGAnswer` 整 mode，无法在 debate/research_v2 上叠加 | `Phase 6E RAG retrieval` 只在 `RAGAnswer` 单 mode 下被触发 |
| Sandbox / WASI | **否** — 只有 `RouteSandboxExecution` 单 mode | `sandbox_execution` 与其他能力互斥 |
| Skills System | **完全未组合** — Router 完全不感知 | `Skills` 通过 `SkillsHandlers` 手动调用，无 Router 参与 |
| MCP / Tool Calling | **部分** — `RequiresTools` 标记，但 Router 不选具体 tool，只选 `react_tool` | `Phase 6A MCP` 在 react_tool 路径下被触发 |
| Workspace | **隐式** — 各 mode 自己写 ref，Router 不报告 artifacts | ref 在 `RoutedExecutionResult.Metadata` 散落 |
| Approval | **部分** — Approval Gate 触发但不进 Router decision | `Phase 7F` 已加 `waiting_for_approval` 状态 |
| Async Result | **是** — Phase 7E.5 已加 | `?async=true` + poll |
| Audit | **是** — `routing_audit_logs` 表 | signals 字段缺失（v2 解决） |
| Web Search | **完全未组合** | 未来 capability |

**结论**：Router 当前是"关键词 → mode 单选器"，完全没做 capability composition。

---

## 2. Shannon 参考分析（v2 补充）

### 2.1~2.4（沿用 v1）

### 2.5 Shannon 的 Capability Composition 思路

| Shannon 思路 | 借鉴点 | 不借鉴 |
|------------|--------|--------|
| Tool / RAG / Sandbox / Skills / MCP 全部注册为**capability** 而非 mode 主体 | ✅ 必须学：capability 是"能力"，mode 是"主执行路径" | ❌ 不照抄其注册表结构 |
| Router 选完 mode 后附加 capability addon（reflection addon = 启用 reflection pass） | ✅ 借鉴"主路径 + addon"模型 | ❌ 不复制其 addon 命名 |
| Workflow 负责能力编排，Activity 负责执行 | ✅ 借鉴边界划分 | 保持 Cribug 现有 workflow / activity 边界 |
| 中间件 (middleware_approval) 是横切关注，独立于 mode | ✅ 借鉴横切层 | 已实现 `executeWithApprovalGate` |

### 2.6 Shannon 不适合直接复制的部分

- Shannon 的 `CapabilityRegistry` 是 Go interface + 全局注册 — Cribug 已经有 `internal/workflows/registry.go`，**不引入第二套注册机制**
- Shannon 的 `AgentCapabilities` 配置有成本/延迟阈值 — **能力组合的成本 / 延迟计算放在 Router 内部**，不分散到 capability
- Shannon 的 `ToolSelector` 是单独服务 — Cribug 工具选择是 React 内部行为，**不外提到 Router**

---

## 3. Phase 7I 改造目标（v2 补充）

> 沿用 v1 §3 全部，新增：

### 3.1 Router 必须输出完整执行计划

不只是：
```json
{"mode": "research_v2"}
```

而是：
```json
{
  "selected_mode": "research_v2",
  "planned_mode": "research_v2",
  "executed_mode": "research_v2",
  "fallback_reason": "",
  
  "addon_capabilities": ["rag", "citations", "workspace", "audit"],
  "required_capabilities": ["rag", "evidence_search"],
  "disabled_capabilities": [],
  
  "approval_required": false,
  "approval_reason": "",
  "async_required": true,
  "workspace_artifacts_expected": [
    "research:{wf}:report",
    "research:{wf}:executive_summary",
    "research:{wf}:evidence_table"
  ],
  "audit_required": true,
  
  "estimated_cost_usd": 0.20,
  "estimated_latency_ms": 120000,
  "risk_level": "medium",
  
  "reason_codes": ["mode_research_v2_high_complexity", "addon_citations_required", "addon_rag_for_evidence"],
  "candidates": [...],
  "rejected_modes": [
    {"mode": "tree_of_thoughts", "reason": "no_signal_match"},
    {"mode": "sandbox_execution", "reason": "missing_capability"}
  ],
  "score_breakdown": {"research_v2": 0.85, "debate": 0.60, "tree_of_thoughts": 0.50, ...},
  "fallback_reason": "",
  "policy_version": "1.0",
  
  "frontend_explanation": {
    "selected_mode_human": "研究综合模式",
    "why": "查询复杂度高、要求引用、需要证据",
    "capability_summary": ["将使用本地知识库检索", "将生成证据表", "结果将保存到 workspace"],
    "cost_estimate": "约 $0.20，预计 2 分钟",
    "approval_needed": false
  }
}
```

---

## 4. Capability Composition / 既有能力串联设计（v2 重点）

> **本章是 v2 计划书的核心新增。** Router 必须在选 mode 之后，把 Phase 5/6/7 的所有 capability 组合成完整的执行计划。

### 4.0 Capability Composition 执行边界（v2 强制约束）

**Phase 7I 的 Router 责任范围：**

- ✅ **识别** capability need（`RequiresTools` / `RequiresRAG` / `RequiresSandbox` / `RequireCitations` / `RequireWebSearch` 等）
- ✅ **输出** `addon_capabilities` 列表
- ✅ **做 forbidden combination 校验**（`sandbox + reflection` / `sandbox + allow_sandbox=false` 等）
- ✅ **把 addons 传给下游 workflow / activity**（透传到 `xxxWorkflow` 的 input 字段）
- ✅ **为前端 / audit 提供解释字段**（`frontend_explanation` / `candidates` / `rejected_modes` / `score_breakdown`）

**Phase 7I Router 不做的事情（强制）：**

- ❌ **不直接执行 RAG**（不调 `EmbedAndSearchChunksActivity` / 不读 Qdrant）
- ❌ **不直接执行 Sandbox**（不调 sandbox / WASI）
- ❌ **不直接执行 Skills**（不调 `SkillExecution` / 不解析 skill metadata）
- ❌ **不直接执行 MCP / Tool Calling**（不调 `callMCPTool` / `ToolRegistry`）
- ❌ **不重写这些能力**（Phase 5/6 既有代码完全不动）
- ❌ **不把外部 IO 塞进 Router**（Router 保持 deterministic，capability 编排是 metadata 传递，**实际执行由 workflow / activity 负责**）

**接口约束：** `AddonCapabilities []Capability` 字段是 **hint / metadata**，不是强约束。workflow 内部根据其自身 capability 路径实际执行，Router 给出"该叠加哪些 capability"的推荐；workflow 可以独立决定是否真的调用（例如 `reflection addon` 仅当 `model_tier != "small"` 才真启用 reflection pass）。

### 4.1 RAG / Qdrant / Embeddings

**已有能力：** `internal/activities/rag_retrieval.go` 的 `EmbedAndSearchChunksActivity`，Qdrant 向量库，本地 embedding 服务。

**Router 如何组合：**
- `RAG` 作为 capability addon，**任何 mode 都可以叠加**
- 信号：`rag_need_score = (RequiresRAG || research 信号命中) ? 1.0 : 0.0`
- `evidence_need_score` 提升 `research_v2` 优先级
- `require_citations=true` 强制 `addon_citations` 至少叠加

**Mode × Addon 矩阵：**
| Mode | rag addon 可叠加 | 何时叠加 |
|------|-----------------|---------|
| `direct_answer` | ✅ | 简单事实问答、本地知识可回答 |
| `rag_answer` | ✅（默认） | 需要本地知识 |
| `react_tool` | ✅ | 工具调用需要先检索 |
| `reflection` | ✅ | 反思需要参考资料 |
| `tree_of_thoughts` | ✅ | 多路径可能引用证据 |
| `debate` | ✅ | 正反观点都需要证据 |
| `research_v2` | ✅（默认） | 多源资料是核心 |
| `swarm_workflow` | ✅ | 多 agent 各自检索 |
| `sandbox_execution` | ❌ | 沙箱代码不查 RAG |

**信号字段（RouterDecisionSignals）：**
- `rag_need_score float64` — 0~1，requires_rag / 关键词命中 / 用户 flag
- `evidence_need_score float64` — 0~1，研究/引用关键词 + require_citations

### 4.2 Sandbox / WASI execution

**已有能力：** `internal/activities/sandbox.go` 的 sandbox 活动 + Phase 6B WASI。

**Router 如何组合：**
- `sandbox` 作为独立 mode（`RouteSandboxExecution`），不是 addon
- `allow_sandbox=false` → 任何 query 都不能选 sandbox，标记为 `disabled_capabilities: ["sandbox"]`
- `risk_score >= high` 时 sandbox 自动触发 approval
- `requires_sandbox=true` → `sandbox_execution` 必须配 `approval + workspace + audit` 三个 addon

**禁止组合：**
- `sandbox + allow_sandbox=false` → reject
- `sandbox + risk=low + tool_execution_untrusted_code` → reject（低风险不能跑沙箱）
- `sandbox + require_approval=false` → reject（不能绕开 approval）

**信号字段：**
- `sandbox_need_score float64` — 0~1，沙箱/代码执行关键词
- `tool_execution_need_score float64` — 0~1，工具执行需求

### 4.3 Skills System

**已有能力：** `internal/activities/skills.go` 的 `SkillExecution` 活动 + `SkillsHandlers` HTTP。

**Router 如何组合：**
- `skills` 作为 capability addon，**不是 mode**
- `skill_need_score` 反映任务是否需要调用预设 skill
- Router **不直接选择 skill 名**，只输出 `required_capabilities: ["skill:safe_math", "skill:summary"]` 等元数据
- 实际 skill 调用由 workflow / activity 决定（保留 Phase 6C 边界）

**信号字段：**
- `skill_need_score float64` — 0~1，关键词（"summarize" / "calculate" / "translate" 等）
- `required_skills []string` — 候选 skill 名（**仅元数据，不强制执行**）

**前端展示：** `addon_capabilities: ["skills"]` + `workspace_artifacts_expected` 含 skill 输出 ref

### 4.4 MCP / Tool Calling

**已有能力：** `internal/activities/mcp.go` + `MCPHandlers` + Phase 6A Tool Runtime。

**Router 如何组合：**
- `mcp_tools` 作为 capability addon（区别于 `react_tool` mode）
- `tool_need_score` 触发 `react_tool` mode，**不直接选 tool**
- `allow_tools=false` → `react_tool` 被 reject；其他 mode 不受影响
- MCP/tool selection 在 `react_tool` workflow 内部完成（保留 Phase 6A 边界）

**信号字段：**
- `tool_need_score float64` — 0~1
- `required_mcp_tools []string` — 候选 tool 名（**仅元数据**）

### 4.5 Workspace

**已有能力：** `internal/db/workspace.go` + `WorkspacePutActivity` + Phase 7G。

**Router 如何组合：**
- **Workspace 始终启用**（默认 addon）
- Router 预测每个 mode 会产生哪些 workspace artifacts，作为 `workspace_artifacts_expected []string` 字段
- 这些 refs 是**预期的**（predicted），不是写入后的。实际写入由 workflow 完成

**Mode × Workspace artifacts：**
| Mode | expected artifacts |
|------|-------------------|
| `direct_answer` | `direct:{wf}:final` |
| `rag_answer` | `rag:{wf}:final`, `rag:{wf}:citations` |
| `react_tool` | `react:{wf}:final`, `react:{wf}:tool_calls` |
| `dag_workflow` | `dag:{wf}:final`, `dag:{wf}:step:{i}` |
| `reflection` | `reflect:{wf}:draft`, `reflect:{wf}:critique`, `reflect:{wf}:revision` |
| `tree_of_thoughts` | `tot:{wf}:solution`, `tot:{wf}:best_path`, `tot:{wf}:thought:{id}` |
| `debate` | `debate:{wf}:turn:pro-r{i}`, `debate:{wf}:turn:con-r{i}`, `debate:{wf}:verdict:r{i}` |
| `research_v2` | `research:{wf}:report`, `research:{wf}:executive_summary`, `research:{wf}:evidence_table` |
| `sandbox_execution` | `sandbox:{wf}:output`, `sandbox:{wf}:logs` |

**信号字段：**
- `workspace_artifact_need bool` — 是否需要写 workspace
- `workspace_artifacts_expected []string` — 预测的 ref 模板

### 4.6 Approval / HITL

**已有能力：** `executeWithApprovalGate` + `EvaluateApprovalPolicy` + Phase 7F async approve/reject。

**Router 如何组合：**
- Approval **不是 addon，是横切关注**
- Router 决策后调用 `EvaluateApprovalPolicy` 决定 `requires_approval`
- `approval_required=true` 写入 `RoutedExecutionResult.Decision`
- async result API 在 approval gate 触发时返回 `waiting_for_approval` 状态

**触发条件（与 v1 一致）：**
- sandbox_execution 总是要 approval
- complexity >= 0.70 + cost > 0.50 → approval
- risk_level >= "high" + tool_need → approval
- allow_sandbox=false → 强制 approval

**信号字段：**
- `approval_need_score float64` — 0~1
- `risk_score float64` — 0~1

### 4.7 Async Result API

**已有能力：** Phase 7E.5 async polling + `PersistRoutedExecutionResult`。

**Router 如何组合：**
- **长耗时 mode 默认 async**
- `async_required bool` 字段

**Async required 条件：**
- 任何 `complexity >= 0.30` 的 mode → `async_required=true`
- `sandbox_execution` 总是 async
- `reflection / tot / debate / research_v2 / swarm` 总是 async
- `direct_answer / rag_answer / react_tool` 短任务可 sync

### 4.8 Audit / Telemetry

**已有能力：** `AuditRoutingDecisionActivity` + `routing_audit_logs` 表 + Phase 7H `audit_events`。

**Router 如何组合：**
- `audit` 作为 capability addon（默认始终启用）
- 决策数据写入 `routing_audit_logs.signals + explanation + candidates`
- Phase 7H 的 `audit_events` 记录 router decision 事件

**信号字段：**
- `audit_need bool` — 默认 true
- `audit_signals []string` — 哪些事件触发 audit

### 4.9 LLM Provider / Cost / Token Budget

**已有能力：** `RouterConfigSnapshot` + `EstimateRouteCost` activity + Phase 6D `models` 目录。

**Router 如何组合：**
- `model_tier` 作为 capability（"small" / "medium" / "large"）
- `estimated_cost_usd` 和 `estimated_latency_ms` 来自 policy 表格
- `budget_fit` / `latency_fit` 影响 mode 候选排序

**禁止组合：**
- `budget_usd < 0.01` 时禁止 research_v2 / swarm（高成本）
- `max_latency_ms < 30000` 时禁止 tot / debate / research_v2（高延迟）

**信号字段：**
- `model_tier string` — "small" / "medium" / "large"
- `budget_fit_score float64` — 0~1
- `latency_fit_score float64` — 0~1

### 4.10 Web / Research Capability

**已有能力：** `research_v2` mode 内部已调用本地 RAG（Phase 6E）。**未来**接入 web search。

**Router 如何组合：**
- `web_search` 作为 capability addon（默认 false，policy 控制）
- `web_search_need_score` 反映任务需要外部资料
- `allow_research=false` → 任何 mode 都不能叠加 `web_search` addon

**信号字段：**
- `web_search_need_score float64` — 0~1
- `web_search_enabled bool` — policy 控制

### 4.11 能力组合禁止规则（forbidden combinations）

| 禁止组合 | 原因 |
|---------|------|
| `sandbox + allow_sandbox=false` | 用户明确禁止 |
| `sandbox_execution + (reflection / tot / debate / research_v2)` | 不能既跑沙箱又跑高级推理（成本） |
| `research_v2 + allow_research=false` | 用户明确禁止 |
| `react_tool + allow_tools=false` | 用户明确禁止 |
| `mcp_tools + allow_tools=false` | 同上 |
| `high_risk_sandbox + require_approval=false` | 不能绕开 approval |
| `sandbox + rag` | 沙箱代码不查 RAG（语义不符） |
| `swarm + sandbox` | 多 agent 沙箱过载 |

### 4.12 能力如何传给下游 workflow / activity

Router 决策结果 `RoutedExecutionResult.Decision.AddonCapabilities` 列表传给：
- `AdvancedRoutingWorkflow` 的 `dispatchXxx` 函数
- `xxxWorkflow` 入口作为 input 字段 `AddonCapabilities []string`
- Workflow 内部根据 `AddonCapabilities` 决定是否调用 `WorkspacePut` / `AuditRoutingDecision` / `RequestApproval` 等 activity

---

## 5. 前端衔接 / Router API Contract（v2 重点新增）

> 因为 Phase 7I 后面就要进入前端阶段，所以 Router 输出必须**前端可消费**。
> 设计**稳定 JSON contract**，不要只返回自然语言 reason 字符串。

### 5.1 POST /api/v1/tasks/route 返回字段

完整 schema：

```json
{
  "session_id": "...",
  "workflow_id": "route-...",
  "run_id": "019e...",
  "decision": {
    "selected_mode": "research_v2",
    "planned_mode": "research_v2",
    "executed_mode": "research_v2",
    "fallback_reason": "",
    
    "addon_capabilities": ["rag", "citations", "workspace", "audit"],
    "required_capabilities": ["rag", "evidence_search", "citation_format"],
    "disabled_capabilities": [],
    
    "approval_required": false,
    "approval_reason": "",
    
    "async_required": true,
    
    "workspace_artifacts_expected": [
      "research:wf:report",
      "research:wf:executive_summary",
      "research:wf:evidence_table"
    ],
    
    "estimated_cost_usd": 0.20,
    "estimated_latency_ms": 120000,
    "risk_level": "medium",
    
    "model_tier": "medium",
    "model_used_hint": "gpt-4o-mini",
    "provider": "openai_compatible",
    
    "reason_codes": ["mode_research_v2_high_complexity", "addon_citations_required"],
    "candidates": [
      {"mode": "research_v2", "score": 0.85, "rejected": false, "signals_hit": ["research", "needs_citation"]},
      {"mode": "debate",       "score": 0.60, "rejected": false, "signals_hit": ["analyze"]},
      {"mode": "sandbox",      "score": 0.0,  "rejected": true,  "reject_reason": "missing_capability"}
    ],
    "rejected_modes": [
      {"mode": "tree_of_thoughts", "reason": "cost_exceeds_budget"},
      {"mode": "sandbox_execution", "reason": "missing_capability"}
    ],
    "score_breakdown": {"research_v2": 0.85, "debate": 0.60, "reflection": 0.45, ...},
    
    "policy_version": "1.0",
    "classifier_used": false,
    
    "frontend_explanation": {
      "selected_mode_human": "研究综合模式",
      "why": "查询要求多源资料 + 引用支持，复杂度高，需要证据表",
      "capability_summary": [
        "使用本地知识库检索",
        "生成证据表",
        "结果保存到 workspace"
      ],
      "cost_estimate": "约 $0.20，预计 2 分钟",
      "approval_needed": false
    }
  },
  "status": "preview"
}
```

**字段约束：**
- 所有字段必须始终存在（即使为空也要返回 `""` / `[]` / `0`），前端可以无检查消费
- 数值字段不能是 `null`（用 `0` / `0.0`）
- 数组字段不能是 `null`（用 `[]`）
- 字符串字段不能是 `null`（用 `""`）

### 5.2 POST /api/v1/tasks/execute-routed?async=true 返回

```json
{
  "task_id": "uuid",
  "workflow_id": "route-exec-...",
  "run_id": "...",
  "session_id": "...",
  "status": "running",
  "async": true,
  "selected_mode": "research_v2",
  "planned_mode": "research_v2",
  "executed_mode": "research_v2",
  "approval_required": false,
  "async_required": true,
  "addon_capabilities": ["rag", "citations", "workspace", "audit"],
  "result_url": "/api/v1/tasks/{id}/result",
  "status_url": "/api/v1/tasks/{id}",
  "message": "workflow started; poll ResultURL for completion"
}
```

### 5.3 GET /api/v1/tasks/{id}/result 状态机

```
pending → running → completed
                  → failed
                  → waiting_for_approval → approved → running → completed
                  → waiting_for_approval → rejected → rejected
                  → waiting_for_approval → timeout   → failed (approval_timeout)
```

| 状态 | HTTP | 何时 |
|------|------|------|
| `running` | 202 | workflow 还在跑 |
| `waiting_for_approval` | 202 | Approval gate 阻塞中 |
| `completed` | 200 | 成功 |
| `failed` | 200 | workflow 失败 |
| `rejected` | 200 | 用户 reject |
| `timeout` | 200 | approval timeout |

### 5.4 waiting_for_approval 时前端必须展示的字段

```json
{
  "task_id": "uuid",
  "status": "waiting_for_approval",
  "result": {
    "pending_approval_id": "approval-...",
    "status": "waiting_for_approval",
    "metadata": {
      "approval_id": "approval-...",
      "approval_url": "/api/v1/tasks/{id}/approve",
      "approval_risk": "high",
      "approval_mode": "sandbox_execution",
      "approval_reason": "sandbox execution requires approval",
      "estimated_cost_usd": 0.05
    }
  },
  "decision": {
    "selected_mode": "sandbox_execution",
    "approval_required": true,
    "approval_reason": "sandbox_execution + risk=high",
    "estimated_cost_usd": 0.05
  },
  "message": "Workflow is waiting for human approval. POST /api/v1/tasks/{id}/approve to continue."
}
```

前端展示：
- "Workflow 等待人工审批"
- "Mode: 沙箱执行"
- "Risk: high"
- "Cost: $0.05"
- [Approve] [Reject] 按钮

### 5.5 completed 时前端必须展示的字段

```json
{
  "task_id": "uuid",
  "status": "completed",
  "result": {
    "status": "ok",
    "decision": {...},
    "final_answer_text": "...",
    "final_answer_ref": "research:wf:report",
    "provider": "openai_compatible",
    "model_used": "gpt-4o-mini",
    "mode": "real",
    "mock": false,
    "tokens_used": 4817,
    "cost_usd": 0.075,
    "metadata": {
      "research_v2_source_count": 1,
      "research_v2_evidence_count": 1,
      "research_v2_evidence_ref": "...",
      "research_v2_synthesis_ref": "...",
      "research_v2_report_ref": "...",
      "research_v2_final_answer_ref": "...",
      "policy_version": "1.0",
      "audit_url": "/api/v1/tasks/{id}/audit"
    },
    "selected_mode": "research_v2",
    "selected_mode_explanation": "查询要求多源资料 + 引用支持，复杂度高"
  }
}
```

### 5.6 Router Explain 面板

前端可以展示：
- 为什么选这个 mode（`frontend_explanation.why`）
- 哪些 mode 被拒绝（`rejected_modes` + 拒绝原因）
- 因为什么拒绝：`cost_exceeds_budget` / `latency_exceeds_max` / `disabled_by_config` / `missing_capability` / `no_signal_match`
- 是否 fallback（`fallback_reason`）
- 是否需要 approval（`approval_required` + `approval_reason`）
- policy 版本（`policy_version`）

### 5.7 前端基于这些字段可以开发

| 页面 | 数据来源 |
|------|---------|
| 任务提交页 | `POST /api/v1/tasks/route` preview → 用户看到 selected_mode / cost / approval_required 决定是否提交 |
| 任务执行页 | `GET /api/v1/tasks/{id}/result` → 状态机 + 进度 |
| Approval 页面 | `waiting_for_approval` → [Approve][Reject] 按钮 |
| 结果页 | `completed` → final_answer_text + workspace_artifacts_expected 列表 |
| Router Explain 面板 | `decision.candidates + rejected_modes + score_breakdown` → 解释 UI |

---

## 6. 不做的事情（v2 补充）

> 沿用 v1 §6 全部，新增：

| 编号 | 约束 |
|------|------|
| 1 | **不重写 Router**（AdvancedRoutingWorkflow 函数体只做最小补充） |
| 2 | **不绕过 Approval Gate**（生产默认 `ROUTER_REQUIRE_APPROVAL=true` 不变） |
| 3 | **不重写 Reflection / ToT / Debate / Research v2** |
| 4 | **不修改 Shannon** |
| 5 | **不强依赖 LLM classifier** |
| 6 | **不让 Router 每次调用昂贵模型** |
| 7 | **不破坏 async result API** |
| 8 | **不进入 Phase 8** |
| 9 | **不改 Workspace / Audit 既有 Activity**（router decision audit 复用现有 `AuditRoutingDecisionActivity`） |
| 10 | **不把 RAG / Sandbox / Skills / MCP 的执行逻辑塞进 Router**（Router 只输出 capability metadata） |
| 11 | **不把前端 UI 直接写在本阶段**（只设计 API contract） |
| 12 | **Router 不直接 HTTP / DB / LLM**（保持 deterministic；LLM classifier 路径必须是 Activity 包装） |
| 13 | **不破坏 Slice 23-28 任何 Go test** |

---

## 7. 数据结构设计（v2 补充）

> 沿用 v1 §5，新增 / 修改：

### 7.1 RouterDecisionSignals（v2 扩充 capability signals）

```go
type RouterDecisionSignals struct {
    // --- 文本信号（v1）---
    QueryLength       int
    QueryCharCount    int
    HasNumberedList   bool
    HasMultiSentence  bool
    UserIntent        string

    // --- 复杂度 sub-signals（v1）---
    ComplexityAnalyze       float64
    ComplexityResearch      float64
    ComplexityExecution     float64
    ComplexityMultiAgent    float64
    ComplexityDebate        float64
    ComplexityExploration   float64
    ComplexityOverall       float64

    // --- 能力需求（v1 已有，v2 扩充）---
    RequiresTools       bool
    RequiresRAG         bool
    RequiresResearch    bool
    RequiresSandbox     bool
    RequiresCitation    bool
    RequiresWebSearch   bool   // v2 新增

    // --- v2 新增：capability signals（独立评分）---
    RagNeedScore            float64
    EvidenceNeedScore       float64
    ToolNeedScore           float64
    SkillNeedScore          float64
    SandboxNeedScore        float64
    ToolExecutionNeedScore  float64
    WebSearchNeedScore      float64
    WorkspaceArtifactNeed   bool
    AuditNeed               bool
    ApprovalNeedScore       float64

    // --- 用户约束（v1）---
    AllowTools      bool
    AllowSandbox    bool
    AllowResearch   bool
    AllowWebSearch  bool   // v2 新增
    RequireCitations bool
    BudgetUSD       float64
    MaxLatencyMs    int

    // --- 风险信号（v1）---
    RiskKeywords []string
    RiskLevel     string

    // --- v2 新增：成本/延迟 fit ---
    BudgetFitScore    float64
    LatencyFitScore   float64

    // --- 政策版本 ---
    PolicyVersion  string
    ClassifierUsed bool
}
```

### 7.2 RouterDecisionExplanation（v2 扩充）

```go
type RouterDecisionExplanation struct {
    SelectedMode   types.RoutingMode
    SelectedReason string
    
    // --- v2 新增：完整执行计划 ---
    AddonCapabilities       []Capability  // 主路径上叠加的能力
    RequiredCapabilities    []Capability  // 任务实际需要的能力
    DisabledCapabilities    []Capability  // 哪些能力被用户 flag / budget 禁用
    WorkspaceArtifactsExpected []string   // 预测的 workspace refs
    EstimatedCostUSD        float64
    EstimatedLatencyMs      int
    AsyncRequired           bool
    AuditRequired           bool
    
    // --- v1 已有 ---
    ScoreBreakdown map[types.RoutingMode]float64
    Candidates     []ModeCandidate
    Signals        RouterDecisionSignals
    PolicyVersion  string
}
```

### 7.3 AddonCapabilities（v2 新增）

```go
type Capability string

const (
    CapRAG            Capability = "rag"
    CapSandbox        Capability = "sandbox"
    CapSkills         Capability = "skills"
    CapMCPTools       Capability = "mcp_tools"
    CapCitations      Capability = "citations"
    CapWebSearch      Capability = "web_search"
    CapWorkspace      Capability = "workspace"
    CapAudit          Capability = "audit"
    CapApproval       Capability = "approval"
    CapReflection     Capability = "reflection"  // 反思 pass 作为 addon
    CapDebate         Capability = "debate"      // 反驳 pass 作为 addon
)
```

### 7.4 ModeCandidate（v2 扩充）

```go
type ModeCandidate struct {
    Mode          types.RoutingMode
    Score         float64
    CostEstimate  float64
    LatencyTier   string
    SignalsHit    []string
    Rejected      bool
    RejectReason  RejectedModeReason
    // v2 新增：mode 候选对应的 addon 推荐
    SuggestedAddons []Capability
}
```

### 7.5 FrontendRouterContract（v2 新增）

```go
type FrontendRouterContract struct {
    SelectedMode           types.RoutingMode    `json:"selected_mode"`
    PlannedMode            types.RoutingMode    `json:"planned_mode"`
    ExecutedMode           types.RoutingMode    `json:"executed_mode"`
    FallbackReason         string                `json:"fallback_reason"`
    AddonCapabilities     []Capability          `json:"addon_capabilities"`
    RequiredCapabilities  []Capability          `json:"required_capabilities"`
    DisabledCapabilities  []Capability          `json:"disabled_capabilities"`
    ApprovalRequired      bool                  `json:"approval_required"`
    ApprovalReason        string                `json:"approval_reason"`
    AsyncRequired         bool                  `json:"async_required"`
    WorkspaceArtifactsExpected []string          `json:"workspace_artifacts_expected"`
    AuditRequired         bool                  `json:"audit_required"`
    EstimatedCostUSD      float64               `json:"estimated_cost_usd"`
    EstimatedLatencyMs    int                   `json:"estimated_latency_ms"`
    RiskLevel             string                `json:"risk_level"`
    ModelTier             string                `json:"model_tier"`
    ModelUsedHint         string                `json:"model_used_hint"`
    Provider              string                `json:"provider"`
    ReasonCodes           []string              `json:"reason_codes"`
    Candidates            []ModeCandidate       `json:"candidates"`
    RejectedModes         []RejectedMode        `json:"rejected_modes"`
    ScoreBreakdown        map[string]float64    `json:"score_breakdown"`
    PolicyVersion         string                `json:"policy_version"`
    ClassifierUsed        bool                  `json:"classifier_used"`
    FrontendExplanation   *FrontendExplanation  `json:"frontend_explanation"`
}

type FrontendExplanation struct {
    SelectedModeHuman    string   `json:"selected_mode_human"`
    Why                  string   `json:"why"`
    CapabilitySummary    []string `json:"capability_summary"`
    CostEstimate         string   `json:"cost_estimate"`
    ApprovalNeeded       bool     `json:"approval_needed"`
}

type RejectedMode struct {
    Mode   types.RoutingMode   `json:"mode"`
    Reason RejectedModeReason  `json:"reason"`
}
```

### 7.6 RejectedModeReason（v1 已有）

### 7.7 CapabilityNeeds（v2 新增规范化）

```go
type CapabilityNeeds struct {
    NeedsTools        bool
    NeedsRAG          bool
    NeedsSandbox      bool
    NeedsResearch     bool
    NeedsCitation     bool
    NeedsWebSearch    bool
    NeedsSkills       bool
    NeedsMCPTools     bool
    NeedsReflection   bool  // 作为 addon 的反思
    NeedsDebate       bool  // 作为 addon 的反驳
}
```

### 7.8 BudgetLatencySignals（v1 已有，v2 不变）

### 7.9 PolicyConfig / PolicyWeights（v1 已有，v2 在 §8 扩充）

---

## 8. policy 配置设计（v2 修订）

> 沿用 v1 §6，新增 capability composition 字段 + classifier 控制分离。

### 8.1 顶层结构

```yaml
version: "1.0"

# ── 模式阈值（complexity） ──
thresholds:
  direct_answer_max:   0.15
  rag_min:             0.10
  react_min:           0.20
  dag_min:             0.35
  reflection_min:      0.30
  tree_of_thoughts_min: 0.55
  debate_min:          0.35
  debate_max:          0.60
  research_v2_min:     0.50
  swarm_min:           0.65
  sandbox_min_risk:    "high"

# ── 风险分类 ──
risk:
  low_max:      0.30
  medium_max:   0.60
  high_max:     0.85
  critical_min: 0.85
  require_approval_min: "high"

# ── 预算 ──
budget:
  cheap_max_usd:    0.05
  medium_max_usd:   0.25
  expensive_min_usd: 0.50
  estimated_cost_usd:
    direct_answer:    0.005
    rag_answer:       0.02
    react_tool:       0.03
    dag_workflow:     0.08
    reflection:       0.06
    tree_of_thoughts: 0.18
    debate:           0.12
    research_v2:      0.20
    swarm_workflow:   0.30
    sandbox_execution: 0.05

# ── 延迟 ──
latency:
  fast_max_ms:   5000
  medium_max_ms: 30000
  slow_min_ms:   60000
  estimated_latency_ms:
    direct_answer:    2000
    rag_answer:       5000
    react_tool:       8000
    dag_workflow:     20000
    reflection:       30000
    tree_of_thoughts: 90000
    debate:           60000
    research_v2:      120000
    swarm_workflow:   180000
    sandbox_execution: 30000

# ── 关键词权重（每个 mode） ──
keywords:
  debate:
    weight_per_match: 0.20
    keywords: ["比较", "对比", "vs ", "versus", "debate", "pros and cons",
               "trade-off", "which is better", "compare", "view", "opinion",
               "argument", "for and against"]
  tree_of_thoughts:
    weight_per_match: 0.25
    keywords: ["explore multiple", "multi-path", "branch", "best path",
               "search tree", "explore paths", "find best approach",
               "analyze paths", "multiple perspectives", "branching",
               "tree of thought", "explore both", "explore alternatives"]
  research_v2:
    weight_per_match: 0.25
    keywords: ["研究", "分析", "报告", "evidence", "citation", "sources",
               "literature", "review", "investigate", "study", "comprehensive",
               "with citations", "according to"]
  sandbox_execution:
    weight_per_match: 0.30
    keywords: ["执行", "运行", "沙箱", "代码", "编译", "测试", "execute", "run ",
               "sandbox", "python code", "script", "compile", "wasi",
               "untrusted code", "execute code"]
  swarm_workflow:
    weight_per_match: 0.30
    keywords: ["多agent", "多角色", "团队", "分工", "协作", "swarm",
               "multi-agent", "team of agents", "collaborat", "multiple agents"]
  reflection:
    weight_per_match: 0.15
    keywords: ["分析", "评估", "改进", "analyze", "evaluate", "improve",
               "refine", "critique", "polish"]
  dag_workflow:
    weight_per_match: 0.15
    keywords: ["步骤", "step", "sequence", "workflow", "pipeline", "first then"]
  skills:
    weight_per_match: 0.10
    keywords: ["summarize", "calculate", "translate", "skill", "safe_math"]
  mcp_tools:
    weight_per_match: 0.20
    keywords: ["mcp", "tool call", "external tool", "bash", "python",
               "compile", "search"]

# ── 信号权重（per mode） ──
signal_weights:
  direct_answer:    { base: 0.50 }
  react_tool:       { base: 0.10, tool_need: 0.30, complexity_overall: 0.30 }
  rag_answer:       { base: 0.10, rag_need: 0.40, complexity_overall: 0.20 }
  dag_workflow:     { base: 0.05, tool_need: 0.20, complexity_overall: 0.30 }
  reflection:       { base: 0.10, analyze: 0.30, complexity_overall: 0.30 }
  tree_of_thoughts: { base: 0.05, exploration: 0.35, complexity_overall: 0.40 }
  debate:           { base: 0.10, debate: 0.40, complexity_overall: 0.20 }
  research_v2:      { base: 0.10, research: 0.35, evidence_need: 0.25, complexity_overall: 0.20 }
  swarm_workflow:   { base: 0.05, multi_agent: 0.40, complexity_overall: 0.30 }
  sandbox_execution: { base: 0.05, execution: 0.50, sandbox_need: 0.40, risk_penalty: 0.30 }

# ── 能力需求门槛 ──
capability_requirements:
  react_tool:        requires_tools
  rag_answer:        requires_rag
  dag_workflow:      requires_tools
  sandbox_execution: requires_sandbox
  research_v2:       requires_research
  # (reflection/tot/debate 不需要硬 capability)

# ── 能力组合规则（v2 新增） ──
# 每个 mode 默认叠加的 addon
default_addons:
  direct_answer:    [workspace, audit]
  rag_answer:       [rag, citations, workspace, audit]
  react_tool:       [mcp_tools, workspace, audit]
  dag_workflow:     [mcp_tools, workspace, audit]
  reflection:       [reflection, workspace, audit]
  tree_of_thoughts: [workspace, audit]
  debate:           [rag, workspace, audit]
  research_v2:      [rag, citations, workspace, audit]
  swarm_workflow:   [workspace, audit]
  sandbox_execution: [sandbox, approval, workspace, audit]

# 强制叠加的 addon（基于信号）
forced_addons:
  require_citations: [citations]                # 全局
  sandbox_need_score > 0.5: [sandbox, approval]
  rag_need_score > 0.5: [rag]
  evidence_need_score > 0.5: [citations]
  approval_need_score > 0.5: [approval]
  high_complexity > 0.6: [workspace, audit]

# ── 禁止组合（forbidden combinations） ──
forbidden_combinations:
  - { modes: [sandbox_execution], requires_user_flag_disabled: [allow_sandbox] }
  - { modes: [sandbox_execution], requires_allow_flag_false: [allow_sandbox] }
  - { modes: [sandbox_execution], blocked_by: [sandbox + reflection] }
  - { modes: [sandbox_execution], blocked_by: [sandbox + tot] }
  - { modes: [sandbox_execution], blocked_by: [sandbox + debate] }
  - { modes: [sandbox_execution], blocked_by: [sandbox + research_v2] }
  - { modes: [research_v2], requires_allow_flag_false: [allow_research] }
  - { modes: [react_tool, dag_workflow, mcp_tools], requires_allow_flag_false: [allow_tools] }
  - { modes: [sandbox_execution], blocked_by: [high_risk + require_approval_disabled] }

# ── Fallback 顺序 ──
fallback_order:
  - sandbox_execution: []              # 无 fallback（安全优先）
  - research_v2:      [dag_workflow, reflection]
  - tree_of_thoughts: [debate, reflection]
  - debate:           [reflection, tree_of_thoughts]
  - swarm_workflow:   [dag_workflow, reflection]
  - dag_workflow:     [react_tool, reflection]
  - reflection:       [tree_of_thoughts, react_tool, direct_answer]
  - react_tool:       [direct_answer]
  - rag_answer:       [direct_answer]
  - direct_answer:    []

# ── Disabled mode 行为 ──
disabled_behavior:
  sandbox_execution:
    when_disabled:    mode_disabled
    when_user_blocks: mode_disabled
  research_v2:
    when_disabled:    research_v1
    when_user_blocks: direct_answer
  tree_of_thoughts:
    when_disabled:    reflection
    when_user_blocks: direct_answer
  debate:
    when_disabled:    reflection
    when_user_blocks: direct_answer
  swarm_workflow:
    when_disabled:    dag_workflow
    when_user_blocks: direct_answer
  reflection:
    when_disabled:    tree_of_thoughts
    when_user_blocks: direct_answer
  dag_workflow:
    when_disabled:    react_tool
    when_user_blocks: direct_answer
  react_tool:
    when_disabled:    direct_answer
    when_user_blocks: direct_answer
  rag_answer:
    when_disabled:    direct_answer
    when_user_blocks: direct_answer
  direct_answer:
    when_disabled:    direct_answer
    when_user_blocks: direct_answer

# ── 模式默认开关（feature flags） ──
mode_feature_flags:
  reflection:       true
  tree_of_thoughts: true
  debate:           true
  research_v2:      true
  sandbox_execution: true

# ── Approval hints ──
approval_hints:
  auto_approval_modes: [sandbox_execution]
  skip_approval_when:
    - { mode: direct_answer, max_complexity: 0.30 }
    - { mode: rag_answer,    max_complexity: 0.30 }

# ── v2 LLM classifier 配置（与 Approval 完全分离） ──
classifier:
  enabled: false                        # 默认关闭
  enabled_field: enable_router_classifier # RouterConfigSnapshot 字段名
  real_test_only: true                  # 只在 REAL_ROUTER_CLASSIFIER_TEST=1 启用
  model_tier: small
  max_tokens: 400
  skip_when:
    - heuristic_score_gap > 0.30
    - budget_usd < 0.01
  invoke_only_when:
    - complexity_in: [0.20, 0.60]
    - no_clear_winner: true
  prompt_template: |
    Score each mode 0-1 for the query: {query}
    Modes: {mode_list}
    Output STRICT JSON: {scores: {mode: score, ...}}
  # v2 明确：与 ROUTER_REQUIRE_APPROVAL 完全独立（详情见 §8.2）
  # Approval Gate kill switch  :  ROUTER_REQUIRE_APPROVAL  (env)  /  RouterConfigSnapshot.RequireApproval
  # LLM classifier kill switch:  ROUTER_CLASSIFIER_ENABLED  (env)  /  RouterConfigSnapshot.EnableRouterClassifier
  #                                上面两个 kill switch 互不影响、各自独立，默认：
  #                                - Approval Gate 默认 true（生产安全门保持开启）
  #                                - LLM classifier 默认 false（Router 不会默认每次调用 LLM）

# ── Telemetry ──
telemetry:
  log_routing_decisions: true
  record_signals:        true
  record_explanation:    true
  audit_policy_version:  true
  emit_event:            router_decision_emitted

# ── Legacy 兼容 ──
legacy:
  enabled: false
  env_override: ROUTER_LEGACY_HEURISTIC
```

### 8.2 LLM classifier 与 Approval Gate 严格分离

| 维度 | Approval Gate | LLM Classifier |
|------|--------------|---------------|
| 控制变量 | `RouterConfigSnapshot.RequireApproval` + `ROUTER_REQUIRE_APPROVAL` | `RouterConfigSnapshot.EnableRouterClassifier` + `policy.classifier.enabled` |
| 默认 | **true**（生产） | **false** |
| 用途 | 触发 `executeWithApprovalGate` | 调用 `LLMClassifierActivity` 重排 mode 候选 |
| 失败 | workflow 进入 `waiting_for_approval` | fallback 到 heuristic 排序 |
| 风险 | 跳过 = 绕过生产安全门 | 跳过 = 失去 LLM 排序 |
| 测试 | `test_approval_async_smoke.sh` | `test_router_classifier_real_llm_smoke.sh`（可选） |

**v2 明确（强制）**：两个 kill switch **完全独立、互不影响**。具体关系：

- **Approval Gate** 由 `ROUTER_REQUIRE_APPROVAL` env 或 `RouterConfigSnapshot.RequireApproval` 字段控制，**与 LLM classifier 无任何代码路径依赖**
- **LLM classifier** 由 `ROUTER_CLASSIFIER_ENABLED` env 或 `RouterConfigSnapshot.EnableRouterClassifier` 字段控制，**与 Approval Gate 无任何代码路径依赖**
- Approval Gate 默认 `true`（生产安全门保持开启）
- LLM classifier 默认 `false`（Router 不会默认每次调用 LLM 重新排序）
- LLM classifier 只在 `ROUTER_CLASSIFIER_ENABLED=1` 或 `policy.classifier.enabled=true`（且 `REAL_ROUTER_CLASSIFIER_TEST=1`）时调用
- **任何情况下，禁止用 `RouterConfigSnapshot.RequireApproval` 控制 LLM classifier 路径；禁止用 `EnableRouterClassifier` 控制 Approval Gate**

---

## 9. 路由规则设计（v2 扩充 per mode）

> 沿用 v1 §7，扩充每个 mode 的"addon / 解释"。

### 9.1 direct_answer

- 适合：单句事实问答
- 需要信号：低 complexity + 短 query
- 可组合 addon：workspace, audit
- 不应选：需 tool / RAG / 深度推理 / 引用
- Fallback：（永远可用）
- Approval：永不需要
- 前端解释：`"查询简短，本地直接回答"`

### 9.2 react_tool

- 适合：工具调用 / 计算 / 搜索
- 需要信号：RequiresTools + 短-中 complexity
- 可组合 addon：mcp_tools, workspace, audit
- 不应选：多步 / 多 agent / 引用
- Fallback：`direct_answer`
- Approval：中-高 risk 可能
- 前端解释：`"需要外部工具"` + capability_summary: `["调用 MCP 工具"]`

### 9.3 rag_answer

- 适合：知识库问答
- 需要信号：RequiresRAG
- 可组合 addon：rag, citations, workspace, audit
- 不应选：复杂多步 / 沙箱
- Fallback：`direct_answer`
- Approval：否
- 前端解释：`"从本地知识库检索"` + capability_summary: `["RAG 检索", "引用支持"]`

### 9.4 dag_workflow

- 适合：明确步骤 / pipeline
- 需要信号：RequiresTools + 中-高 complexity
- 可组合 addon：mcp_tools, workspace, audit
- 不应选：单句
- Fallback：`react_tool` `reflection`
- Approval：中可能
- 前端解释：`"多步骤任务"` + capability_summary: `["DAG 编排", "工具调用"]`

### 9.5 reflection

- 适合：分析 / 改进 / 草稿 / 评稿
- 需要信号：analyze 信号 + 中 complexity
- 可组合 addon：reflection, workspace, audit, rag（如果 needs_rag）
- 不应选：短 query
- Fallback：`tree_of_thoughts` `react_tool` `direct_answer`
- Approval：视复杂度
- 前端解释：`"任务需要反思改进"` + capability_summary: `["反思 pass", "草稿评审"]`

### 9.6 tree_of_thoughts

- 适合：多路径 / 搜索解空间
- 需要信号：exploration + 高 complexity
- 可组合 addon：workspace, audit, rag（可选）
- 不应选：简单
- Fallback：`debate` `reflection`
- Approval：视复杂度
- 前端解释：`"任务需要探索多条推理路径"` + capability_summary: `["多路径搜索", "最优路径选择"]`

### 9.7 debate

- 适合：正反观点 / 比较 / vs
- 需要信号：debate 信号 + 中 complexity
- 可组合 addon：rag, workspace, audit
- 不应选：简单 / 高 risk
- Fallback：`reflection` `tree_of_thoughts`
- Approval：视复杂度
- 前端解释：`"任务需要正反观点对比"` + capability_summary: `["Pro 论述", "Con 论述", "Judge 裁决"]`

### 9.8 research_v2

- 适合：多源资料 / 引用 / 证据
- 需要信号：research / citation + 中-高 complexity
- 可组合 addon：rag, citations, workspace, audit, web_search（如果 enabled）
- 不应选：单句
- Fallback：`dag_workflow` `reflection`
- Approval：视复杂度
- 前端解释：`"任务需要多源研究"` + capability_summary: `["本地知识库检索", "证据提取", "引用支持", "Workspace 保存"]`

### 9.9 sandbox_execution

- 适合：沙箱代码执行
- 需要信号：RequiresSandbox
- 可组合 addon：sandbox, approval（强制）, workspace, audit
- **绝对不**叠加：reflection / tot / debate / research_v2
- Fallback：**无**（安全优先）
- Approval：**总是需要**
- 前端解释：`"任务需要在沙箱中执行代码"` + capability_summary: `["沙箱执行", "等待审批"]` + warning: `["需要人工审批"]`

---

## 10. 实施步骤（v2 扩充）

> 沿用 v1 §8，**新增 Step 5 = Capability Composition**

### Step 1：新增 `internal/types/router_v2.go`（只 types）

- 同 v1 Step 1
- **v2 新增**：`Capability` 常量 / `CapabilityNeeds` / `CapabilityRequirement` / `AddonCapabilities` / `FrontendRouterContract` / `FrontendExplanation` / `RejectedMode`（v1 已有 ModeCandidate.RejectReason 字段，但需要独立 struct 传给 API）

**风险：** 无

**验收：** go build / go vet 0

---

### Step 2：policy loader

- 同 v1 Step 2
- **v2 新增**：解析 `default_addons` / `forced_addons` / `forbidden_combinations` / `classifier.enabled` 字段
- 验证：required_field_check（`classifier` 块必须独立，不与 `approval_hints` 混淆）

**风险：** 字段读取错导致 addon 漏算

**回滚：** `LOADER_FALLBACK_TO_DEFAULT=1` env

**验收：** 单测覆盖 5 个 yaml 路径

---

### Step 3：在 `EvaluateRoutingPolicyActivity` 内构造 `RouterDecisionSignals`

- 同 v1 Step 3
- **v2 新增**：在 `BuildDecisionSignals` 中增加 capability sub-signals（`RagNeedScore` / `ToolNeedScore` / `SkillNeedScore` / `SandboxNeedScore` / `WebSearchNeedScore` / `WorkspaceArtifactNeed` / `AuditNeed` / `ApprovalNeedScore` / `BudgetFitScore` / `LatencyFitScore`）
- 旧 `ComplexityScore` **不变**（向后兼容）

**风险：** 新 signal 错算导致 mode 偏袒

**验收：** 单测覆盖 capability signal 矩阵

---

### Step 4：引入 Policy scoring（多 mode 候选评分）

- 同 v1 Step 4
- **v2 新增**：`ScoreModes` 在算分时同时算每个 mode 候选的 `SuggestedAddons`（基于 `default_addons + forced_addons` + 信号）

**风险：** addon 推荐错误 → workflow 缺 capability

**回滚：** `ROUTER_LEGACY_HEURISTIC=true` 跳到旧路径

**验收：** 矩阵单测

---

### Step 5：Capability Composition（v2 重点新增）

**改动：**
- 新增 `ComposeAddons(signals, policy, mode) []Capability`（pure function）
- 新增 `ValidateForbiddenCombinations(mode, addons, capabilities) []RejectedMode`（pure function）
- 新增 `PredictWorkspaceArtifacts(mode, workflow_id) []string`（pure function）
- `selectPlannedMode` 内部：在选定 mode 之后调用 `ComposeAddons` 和 `ValidateForbiddenCombinations`
- `dispatchXxx` 函数在 `RoutedExecutionResult.Decision.AddonCapabilities` 填入 addon 列表
- `dispatchXxx` 函数在 `RoutedExecutionResult.WorkspaceArtifactsExpected` 填入预期 ref 列表

**修改文件：**
- `internal/activities/router.go`（新增 3 个 helper + 修改 selectPlannedMode）
- `internal/workflows/router.go`（dispatchSimple / dispatchRAG / dispatchDAG / dispatchSwarm / dispatchToT / dispatchDebate / dispatchResearchV2 全部接 AddonCapabilities 字段，透传到 child workflow input）
- `internal/types/router.go`（RoutedExecutionResult 加 `AddonCapabilities` / `WorkspaceArtifactsExpected` / `DisabledCapabilities` 字段）

**风险：**
- workflow input 增字段 → 既有 workflow 可能忽略（不破坏，因为 omitempty）
- Capability 列表错 → workflow 调用错 capability → 行为偏差

**回滚：** env `ROUTER_LEGACY_HEURISTIC=true` + 不调用 ComposeAddons

**验收：**
- 单测覆盖每个 mode 的默认 addon 列表
- 单测覆盖 forbidden combinations
- 既有 Slice 25-28 workflow 测试 PASS（workflow input 增字段不破）
- 既有 real LLM smoke PASS

---

### Step 6：替换 selectPlannedMode 内部实现

- 同 v1 Step 5
- **v2**：selectPlannedMode 内部同时填入 `AddonCapabilities` / `WorkspaceArtifactsExpected` / `RequiredCapabilities` / `DisabledCapabilities` 到 `Decision`

---

### Step 7：补 API contract 字段

- 同 v1 Step 7
- **v2 新增**：`api/router_handlers.go` 的 `Route` / `ExecuteRouted` / `GetTaskResult` 三个 handler 全部增加 §5.1/5.2/5.5 的 contract 字段
- 字段缺省值策略：空值用 `""` / `0` / `[]`，不返回 `null`

**修改文件：**
- `internal/api/router_handlers.go`（三个 handler）

**风险：** 旧 client 解析新字段失败（但 JSON 解析是 forward-compatible，新增字段不影响）

**验收：**
- `curl /api/v1/tasks/route` 返回完整 §5.1 schema
- 字段全部非 null

---

### Step 8：telemetry / audit 接入

- 同 v1 Step 8
- **v2 新增**：`router_audit_logs` 加 `addon_capabilities JSONB` + `workspace_artifacts JSONB` + `frontend_explanation JSONB` 列
- `GET /api/v1/tasks/{id}/audit` 暴露这些字段

**修改文件：**
- `migrations/016_routing_signals.sql`（v2 扩展）
- `internal/activities/router.go`（AuditRoutingDecisionActivity）
- `internal/api/router_handlers.go`（GET /tasks/{id}/audit）

**风险：** JSONB 列 → IF NOT EXISTS 保护

**验收：** audit row 包含 capability / workspace artifacts 字段

---

### Step 9：测试 + smoke

- 同 v1 Step 9
- **v2 新增**：
  - 单测：`TestAddonsForMode` / `TestForbiddenCombinations` / `TestCapabilitySignals`
  - smoke：`test_router_strategy_smoke.sh` 增加 capability composition 场景
  - regression：跑全部既有 smoke + 真实 LLM smoke

**验收：**
- 单测覆盖 8 mode × 5 addon 组合
- 既有 mock smoke 全过
- 真实 LLM smoke 全过

---

## 11. 测试计划（v2 扩充）

> 沿用 v1 §9，新增 capability composition 测试。

### 11.1 单元测试

`internal/activities/router_v2_test.go`:
- `TestBuildDecisionSignals` 6+ query 覆盖 capability signals
- `TestScoreModes` 各种 signal 组合
- `TestRejectWhenAllowFalse` 3 个 allow flag 各自测试
- `TestRejectWhenBudgetTooLow`
- `TestRejectWhenLatencyTight`
- `TestRequireCitationsBoostsResearchV2`
- **`TestComposeAddons` 每个 mode 默认 addon 正确**
- **`TestForcedAddons` require_citations 强制叠加 citations**
- **`TestForbiddenCombinations` sandbox + reflection 被 reject**
- **`TestWorkspaceArtifactsExpected` 每个 mode 预测的 ref 列表**
- **`TestDisabledCapabilities` allow_sandbox=false → sandbox 在 disabled list**
- `TestDisabledModeFallsBack`
- `TestLegacyEnvOverride`
- `TestSandboxAlwaysRequiresApproval`
- `TestPolicyVersionBumpedInTelemetry`
- `TestLLMClassifierIndependentFromApproval` ← v2 新增（验证 classifier 路径不影响 approval）

`internal/config/router_policy_test.go`:
- `TestLoadRouterPolicyValid` / `TestLoadRouterPolicyMissing` / `TestLoadRouterPolicyInvalid`
- `TestValidatePolicyConfig` reject crazy values
- **`TestPolicyLoadAddonRules` 验证 default_addons / forced_addons / forbidden_combinations 解析**

`internal/workflows/router_v2_test.go`:
- selectPlannedMode 行为兼容
- dispatchXxx 透传 AddonCapabilities 到 child workflow input
- ResolveExecutedMode 完整 fallback

`internal/api/router_v2_test.go`:
- `TestRouteResponseHasFrontendContract` 验证 §5.1 字段非 null
- `TestExecuteRoutedAsyncHasFrontendContract` 验证 §5.2
- `TestGetTaskResultStatusMachine` 验证 §5.3

### 11.2 Smoke 测试

`scripts/test_router_strategy_smoke.sh`（v2 扩充）：

1. 简单问题 → direct_answer + addons=[workspace, audit]
2. 工具调用 → react_tool + addons=[mcp_tools, workspace, audit]
3. RAG 检索 → rag_answer + addons=[rag, citations, workspace, audit]
4. 多步执行 → dag_workflow + addons=[mcp_tools, workspace, audit]
5. 反思 → reflection + addons=[reflection, workspace, audit]
6. 多路径 → tree_of_thoughts + addons=[workspace, audit]
7. 正反观点 → debate + addons=[rag, workspace, audit]
8. 多源研究 → research_v2 + addons=[rag, citations, workspace, audit]
9. 沙箱执行 → sandbox_execution + addons=[sandbox, approval, workspace, audit] + approval_required=true
10. **allow_sandbox=false → sandbox 在 disabled_capabilities**
11. **allow_research=false → research_v2 在 disabled_capabilities**
12. **allow_tools=false → react_tool / dag_workflow 在 disabled_capabilities**
13. **require_citations=true + 中-高 complexity → research_v2 (citations addon 强制叠加)**
14. **disabled mode 行为（research_v2 关闭 → fallback 到 dag_workflow）**
15. **forbidden combination 生效（sandbox + reflection 被 reject）**
16. **预算极低 → 高成本 mode 降级**
17. **latency 极紧 → 跳过 tot/debate/research_v2**
18. **workspace_artifacts_expected 列表正确**
19. **frontend_explanation 字段非空**

`scripts/test_router_strategy_regression.sh`（v2 扩充）：
- 跑全部既有 mock smoke 不破坏
- 跑全部既有真实 LLM smoke 不破坏

### 11.3 Real LLM 测试（v2 可选）

- `REAL_LLM_TEST=1 REAL_ROUTER_CLASSIFIER_TEST=1 ./scripts/test_router_classifier_real_llm_smoke.sh`（如实现）
- 默认 SKIP（不强制）

### 11.4 关键回归（同 v1）

| 测试 | 必须通过 |
|------|---------|
| go test ./internal/... | PASS |
| go build / go vet | 0 |
| 全部 mock smoke | PASS |
| Debate / ToT / Research v2 真实 LLM smoke | PASS |
| Approval async smoke | PASS |

---

## 12. 风险和回滚策略（v2 补充）

> 沿用 v1 §10，新增：

| 风险 | 缓解 / 回滚 |
|------|------------|
| 新 Router 误分流 | 单测矩阵 + router strategy smoke + `ROUTER_LEGACY_HEURISTIC=true` 回滚 |
| 成本升高 | LLM classifier 默认 false；classifier invoke_only_when 严格条件 |
| 影响生产 Approval | 两个 kill switch 互不干扰；Approval 默认 true；classifier 默认 false |
| **Addon 传错导致 workflow 异常** | workspace_artifacts_expected / addon_capabilities 字段仅用于前端展示 + 元数据；workflow 内部仍然按其自身 capability 路径执行，**addon 是 hint 不是强制** |
| **前端 contract 频繁变化** | §5 schema 一旦 Phase 7I 完成后定版，**任何字段改名 / 删除需要 deprecation 周期**；新字段用 `omitempty` 让旧 client 不破 |
| 数据迁移 | migration 016 只加列（IF NOT EXISTS 保护） |
| Workspace artifact 预测与实际不一致 | workflow 实际写入的 ref 包含模式标识 + workflow_id，ref 命名空间由 workflow 决定；预测只用于前端展示 |

---

## 13. 最终验收标准（v2 扩充）

### 13.1 必须运行

```bash
# 1. 单元 + Go test
go test ./internal/api ./internal/activities ./internal/types ./internal/workflows ./internal/config -count=1
go test ./... -count=1

# 2. Build + vet
go build ./cmd/gateway ./cmd/worker
go vet ./...

# 3. 全部 mock smoke
./scripts/test_debate_smoke.sh
./scripts/test_tot_smoke.sh
./scripts/test_research_v2_smoke.sh
./scripts/test_approval_async_smoke.sh
./scripts/test_router_strategy_smoke.sh
./scripts/test_router_strategy_regression.sh

# 4. 全部真实 LLM smoke (async polling)
ROUTER_REQUIRE_APPROVAL=false REAL_LLM_TEST=1 REAL_DEBATE_TEST=1 REAL_DEBATE_TEST_MAX_COST_USD=1 ./scripts/test_debate_real_llm_smoke.sh
ROUTER_REQUIRE_APPROVAL=false REAL_LLM_TEST=1 REAL_RESEARCH_V2_TEST=1 REAL_RESEARCH_V2_TEST_MAX_COST_USD=1 ./scripts/test_research_v2_real_llm_smoke.sh
ROUTER_REQUIRE_APPROVAL=false REAL_LLM_TEST=1 REAL_TOT_TEST=1 REAL_TOT_TEST_MAX_COST_USD=1 ./scripts/test_tot_real_llm_smoke.sh
```

### 13.2 判定 Phase 7I 完成的标准

1. ✅ 全部 Step 1-9（含 Step 5 Capability Composition）完成
2. ✅ go test / go build / go vet 全通过
3. ✅ 全部 mock smoke 通过（含 router strategy 19 场景）
4. ✅ 全部真实 LLM smoke 通过（Debate 10/10、Research v2 7/7、ToT 8/8）
5. ✅ Router 输出 §5.1 完整 schema（addon_capabilities / workspace_artifacts_expected / candidates / rejected_modes 全有）
6. ✅ Forbidden combinations 生效（sandbox + reflection 被 reject）
7. ✅ `ROUTER_LEGACY_HEURISTIC=true` 紧急回滚路径可用
8. ✅ LLM classifier 默认关闭，与 Approval Gate 独立
9. ✅ 不动 Shannon / 不进入 Phase 8 / 不绕过 Approval Gate
10. ✅ Capability composition 不把执行逻辑塞进 Router

### 13.3 不算 Phase 7I 完成

- 仅修改 policy yaml 但 Router 没真的用 signals 评分
- 仅加 types 没真正接入 selectPlannedMode
- 没补 capability composition（仅 v1 baseline）
- 没补 frontend contract（前端无法消费）
- 真实 LLM smoke 没全过
- LLM classifier 默认开启（违反约束）
- 既有 Slice 25-28 测试破

---

**等待用户确认后再开始实现。**
---

# Phase 7I Router Strategy Upgrade — 修订版计划书（v3）

> 本计划书在 v2 基础上修订。**v3 是唯一执行版本。**
> v1 / v2 仅作为历史背景和设计演进记录，不作为实现依据。
> 所有实施步骤、数据结构、测试计划、验收标准均以 v3 为准。

## v3 相对 v2 的关键变化

| 变化 | 详情 |
|------|------|
| v3 作为唯一执行版本 | v1 / v2 仅历史参考 |
| 新增 Section 14: LLM-assisted Router Arbiter | LLM 辅助二级裁决机制 |
| 新增 14.1-14.8 | 触发条件、policy 字段、安全语义、测试 |
| Section 15 测试补充 | LLM classifier 相关 + sandbox/approval 安全语义 |
| v2 其余章节不变 | Sections 0-13 全部保留 |

---

## 14. LLM-assisted Router Arbiter / LLM 辅助二级裁决机制（v3 新增）

### 14.0 设计原则

LLM classifier 不是替代启发式路由，而是在极端、不确定、冲突、高风险场景下作为二级裁决。

核心原则：
1. **默认关闭**：policy.classifier.enabled=false。Router 90%+ 的请求走 fast heuristic path。
2. **不影响普通路径**：只有满足触发条件之一时才尝试调用。
3. **只在 policy 和 env 同时允许时启用**：policy.classifier.enabled=true 且 ROUTER_CLASSIFIER_ENABLED=1。
4. **Router workflow 不直接调用 LLM**：必须通过 LLMClassifierActivity (Activity)，保持 Router 本体 deterministic。
5. **调用失败时必须 fallback 到 heuristic scoring**，任务不能因 classifier 不可用而失败。
6. **LLM classifier 不能绕过 Approval Gate**。
7. **输出严格 JSON 并 schema validate**：必须通过 validateClassifierOutput() 校验。
8. **审计可追溯**：classifier_used、classifier_reason、classifier_confidence、classifier_raw_score_summary、classifier_model_used、classifier_tokens_used、classifier_latency_ms、classifier_fallback 必须写入 audit/explanation。

### 14.1 触发条件

以下任一条件满足时，Router 考虑调用 LLM classifier（还需 14.2 配置允许）：

| 编号 | 条件 | 示例 |
|------|------|------|
| C1 | top1 与 top2 mode 分差过小 | gap < min_score_gap_to_skip (默认 0.10) |
| C2 | heuristic 无 clear winner | 多 mode 分数集中在窄区间 |
| C3 | capability 信号冲突 | RequiresResearch=true 但 budget/latency 极低 |
| C4 | 用户约束冲突 | require_citations=true 但 allow_research=false |
| C5 | 高风险或高成本任务 | sandbox/swarm/research_v2 任一被评分 >= 0.3 |
| C6 | planned_mode disabled 后 fallback 链路不唯一 | ToT disabled -> [debate, reflection] 两候选分差小 |
| C7 | query 同时命中多个高级模式 | research_v2 + debate + tree_of_thoughts 三者都有信号 |
| C8 | legacy heuristic 与 policy scoring 明显不同 | 仅 smoke / real test 时启用 |

明确不会触发的场景：简单问答(complexity<0.20)、clear winner(gap>=0.30)、budget 极低、policy/env 未开启。

### 14.2 LLM classifier 的 policy 配置字段

在 config/router_policy.yaml 的 classifier 块中新增：

```yaml
classifier:
  enabled:        false
  enabled_field:  enable_router_classifier
  real_test_only:  true
  model_tier:     small
  max_tokens:     400
  env_override:   ROUTER_CLASSIFIER_ENABLED

  # === v3 新增字段 ===
  min_score_gap_to_skip:      0.10
  max_cost_usd_per_call:      0.005
  timeout_ms:                 3000
  invoke_on_high_risk:        true
  invoke_on_conflict:         true
  invoke_on_no_clear_winner:  true
  invoke_on_disabled_fallback: true
  invoke_on_multi_advanced:   true
  invoke_on_legacy_divergence: false
  output_schema_version:     "1.0"
  fail_open_to_heuristic:    true
  required_fields:           ["scores", "reasoning", "confidence"]
  audit:
    record_raw_scores:       true
    record_tokens_used:      true
    record_latency_ms:       true
    record_trigger_reason:   true
```

Go 类型映射：PolicyClassifier 扩充以下字段：
min_score_gap_to_skip, max_cost_usd_per_call, timeout_ms, invoke_on_high_risk,
invoke_on_conflict, invoke_on_no_clear_winner, invoke_on_disabled_fallback,
invoke_on_multi_advanced, invoke_on_legacy_divergence, output_schema_version,
fail_open_to_heuristic, required_fields, audit.{record_raw_scores,
record_tokens_used, record_latency_ms, record_trigger_reason}

### 14.3 LLM classifier 执行流程

```
selectPlannedModeV2(input)
  -> BuildDecisionSignals -> ScoreModes -> ComposeAddons -> Sort
  -> checkClassifierConditions(signals, candidates, policy)
    |-- 条件满足 && policy.enabled && env enabled
    |     -> LLMClassifierActivity(ctx, classifierInput)
    |        |-- 成功+合法JSON -> mergeLLMScores(candidates, llmScores)
    |        |                   -> re-select top-1
    |        |                   -> 写入 classifier_used=true + audit
    |        |-- 超时/失败/无效JSON -> classifier_fallback=true
    |        |                   -> 继续使用 heuristic 排序
    |        -- return selected mode + explanation w/ classifier metadata
    -- 条件不满足 -> classifier_used=false, return heuristic result
```

### 14.4 LLM classifier 的 Activity 设计

LLMClassifierInput / LLMClassifierOutput 类型。
LLMClassifierActivity 通过 AgentActivity.CallLLM 调用 LLM，
构造 prompt 含 query + top-N candidates + signals summary。
失败时返回 Fallback=true 且不 panic。

### 14.5 LLM classifier 输出校验规则

validateClassifierOutput() 检查:
1. required_fields 全部存在
2. Scores map key 都在 validModes 中
3. 每个 score in [0, 1]
4. Confidence in [0, 1]
5. SelectedMode in validModes
6. Reasoning 非空
任何失败 -> error -> Activity 返回 fallback

### 14.6 LLM classifier 与 Approval Gate 严格分离

| 维度 | Approval Gate | LLM Classifier |
|------|--------------|---------------|
| 控制变量 | ROUTER_REQUIRE_APPROVAL / RequireApproval | ROUTER_CLASSIFIER_ENABLED / policy.classifier.enabled |
| 默认 | true | false |
| 作用 | 暂停 workflow 等待 human signal | 重排 mode 候选 |
| 失败行为 | workflow 进入 waiting_for_approval | fallback 到 heuristic |
| 互相干扰 | 否 - 代码路径完全独立 | |

强制约束：
- 禁止 LLMClassifier 修改 RequireApproval 字段
- 禁止 EvaluateApprovalPolicy 查看 classifier_used 字段
- 禁止 RequireApproval 控制 LLM classifier 路径
- LLM classifier 重排后的 selected mode 仍须经过 EvaluateApprovalPolicy

### 14.7 sandbox / approval 安全语义修正

1. allow_sandbox=false 时必须拒绝 sandbox，不接受 approval 覆盖
2. sandbox_execution 选中后 always approval_required=true
3. LLM classifier 不能把 sandbox 降级成普通工具任务绕过审批
4. untrusted code / execute code 关键词 -> risk_score 自动提升

### 14.8 LLM classifier 在代码中的接入点

```
internal/activities/router_v2.go
  |- BuildDecisionSignals (已有)
  |- ScoreModes (已有)
  |- ComposeAddons (已有)
  |- checkClassifierConditions (v3 新增: pure func)
  |- mergeLLMScores (v3 新增: pure func)
  |- validateClassifierOutput (v3 新增: pure func)
  -- selectPlannedModeV2 (修改: 调用 checkClassifierConditions)

internal/activities/router.go
  -- LLMClassifierActivity (v3 新增: Activity)

internal/config/router_policy.go
  -- PolicyClassifier (v3 扩充字段)

internal/types/router_v2.go
  |- LLMClassifierInput (v3 新增)
  |- LLMClassifierOutput (v3 新增)
  -- ClassifierAuditFields (v3 新增)
```

---

## 15. 测试计划补充（v3 新增）

所有 v1/v2 测试仍须全部通过。v3 新增以下：

### 15.1 LLM classifier 逻辑测试

| 测试 | 说明 |
|------|------|
| TestLLMClassifierOnlyWhenAmbiguous | score gap < 0.10 + enabled => 调用 |
| TestLLMClassifierNotCalledOnClearWinner | gap >= 0.30 => 不调用 |
| TestLLMClassifierNotCalledWhenDisabled | policy disabled => 不调用 |
| TestLLMClassifierNotCalledWhenEnvDisabled | env 未置 true => 不调用 |
| TestLLMClassifierSkipsOnBudgetZero | budget < max_cost => 不调用 |
| TestLLMClassifierFallbackToHeuristicOnError | Activity fail => fallback |
| TestLLMClassifierRejectsInvalidJSON | 非法 JSON => fallback |
| TestLLMClassifierRejectsMissingFields | 缺字段 => fallback |
| TestLLMClassifierCannotBypassApproval | sandbox->direct 被拒 |
| TestLLMClassifierAuditFieldsWritten | 所有 classifier audit 字段写入 |
| TestLLMClassifierMergeScoresPreservesOrder | 合并排序正确 |
| TestLLMClassifierOnHighRisk | C5 触发 |
| TestLLMClassifierOnConflict | C3/C4 触发 |
| TestLLMClassifierOnMultiAdvanced | C7 触发 |
| TestLLMClassifierOnDisabledFallback | C6 触发 |
| TestLLMClassifierNotOnLegacyDivergenceDefault | C8 默认关闭 |

### 15.2 Sandbox / Approval 安全语义测试

| 测试 | 说明 |
|------|------|
| TestAllowSandboxFalseRejectsSandboxNotApproval | allow_sandbox=false => reject |
| TestSandboxExecutionAlwaysRequiresApproval | sandbox => requires_approval=true |
| TestUntrustedCodeRaisesRiskLevel | untrusted keyword => risk_score >= 0.70 |
| TestLLMClassifierCannotDowngradeSandboxToBypassApproval | LLM 降级 => Router 拒绝 |

### 15.3 Real smoke（可选）

REAL_ROUTER_CLASSIFIER_TEST=1 REAL_LLM_TEST=1 ./scripts/test_router_classifier_real_llm_smoke.sh

默认 SKIP。断言：classifier_used=true, classifier_fallback=false, classifier_confidence>0, audit 写入。

---

## 16. v3 验收标准

沿用 v2 Section 13 全部。

### 16.1 新增必须运行

```
go test ./internal/activities/ -run TestLLMClassifier -v
go test ./internal/api ./internal/activities ./internal/types ./internal/workflows -count=1
go build ./cmd/gateway ./cmd/worker && go vet ./...
# 全部 mock smoke 不破
./scripts/test_debate_smoke.sh && ./scripts/test_tot_smoke.sh
./scripts/test_research_v2_smoke.sh && ./scripts/test_approval_async_smoke.sh
# 全部 real LLM smoke 不破 (Debate 10/10, Research v2 7/7, ToT 8/8)
```

### 16.2 v3 完成标准

1. 全部 v2 标准仍满足
2. LLM classifier 默认关闭
3. 所有 LLM classifier 单测通过
4. LLM classifier 调用路径接入但默认不触发
5. LLM classifier 失败 fallback 到 heuristic
6. sandbox + approval 安全语义全部通过
7. allow_sandbox=false => sandbox 直接拒绝
8. untrusted code => risk_score 自动提升
9. Classifier != Approval Gate
10. 不破任何 v2 测试和 smoke

### 16.3 v3 不算完成

- LLM classifier 默认开启
- LLM classifier 绕过 Approval Gate
- LLM classifier 错误导致任务失败
- allow_sandbox=false 仍路由到 sandbox
- untrusted code 识别为 low risk
- v2 任何 mock/real LLM smoke 破了

---

等待用户确认后再开始实现。
