# Phase 7I Router Strategy — Current State & Follow-up Plan

> **本文档是"当前工程状态 + 下一步执行计划"，不是架构论文。**
> 历史设计（v1 / v2 / v3 草案）已移至 `archive/phase7i_design_history.md`，本文仅保留当前 v3 真实状态与后续行动。

---

## 0. 当前结论

### 0.1 状态快照

- **Phase 7I 主体实现已做过一版**（commit `9aac7e4` ~ `8657855`），但**未 fully accepted**。
- 单测、smoke harness、final router matrix 已建立。
- Final smoke 给出过 NO-GO；Fix-stage 做了部分修复（`37a3359`），但**关键 P0 未通过**。
- **当前不进入正式前端联调**；前端 MVP 可并行推进，但**仅用 sample / mock contract**，不依赖未验收的后端。

### 0.2 已完成

| 类别 | 内容 |
|------|------|
| Router v2 主体 | 6 步多信号打分（`router_v2.go`） |
| Capability 分类 | 5 类（`router_classifier.go`） |
| LLM Arbiter 集成 | `LLMClassifierActivity` + YAML 配置 |
| Frontend Contract | `BuildFrontendContract` 10 个样本 JSON |
| 单测 | `router_v2_test.go` 833 行 |
| Smoke 脚本 | `test_phase6e3_rag_smoke.sh` / `test_dag_concurrency.sh` 等 |
| Final matrix | 25 case 已定义 |
| Fix-stage 修复 | 中文化 heuristics + 风险词扩展 |

### 0.3 未通过

| 类别 | 失败原因 |
|------|----------|
| **P0-1: classifier metadata 透传** | LLMArbiter 调用后，`classifier_used=true / reason / confidence` **没有透出到 frontend contract** |
| **P0-2: sandbox approval 回归** | T9 case（sandbox+low-risk）的 `approval_required` 语义与计划书"选中即必须审批"不符 |
| P1-1: total matrix | 9/25 通过；多个 mode 切换边界 case 失败 |
| P1-2: rejected_modes.reason | budget / latency / allow flag 触发拒绝时，原因字段为空 |
| P1-3: real classifier smoke | 真实 LLM call count = 0，未达到"至少 8 case 真正调用 classifier" |
| P1-4: classifier_expected=true 校验 | 当期望调 LLM 但未调，测试未失败（缺少断言） |

### 0.4 当前不做

- ✗ 不进入 Phase 8
- ✗ 不改 Shannon
- ✗ 不绕过 Approval Gate
- ✗ 不重写 Router 主流程
- ✗ 不大规模调 heuristic
- ✗ 暂不正式联调前端

---

## 1. 高级路由总流程

7 层流水线，**严格按顺序**：

```
Request
  ↓
[1] Early Route Layer          ← 显式强制路由（template / force_* / skip_* / agent / browser）
  ↓ (没命中)
[2] Router v2 Signal Layer      ← 多维信号提取
  ↓
[3] Mode Scoring Layer          ← 10 个 mode 打分
  ↓
[4] Capability Composition     ← 输出 addons / required / disabled
  ↓
[5] Policy Filtering / Safety  ← 过滤候选 + safety 约束
  ↓
[6] LLM-assisted Router Arbiter ← 仅在模糊/冲突/高风险时二级裁决
  ↓
[7] Frontend Contract Layer    ← 稳定输出给前端
```

### 1.1 Early Route Layer（显式强制路由）

**唯一可以不进入 v2 流水线的入口**。命中即短路返回。

**注意**：Early Route 命中的目标不一定是当前 Cribug 已实现的完整 `RoutingMode`。不在已实现 mode 列表内的项，作为"early handled path / future extension"标记，**不进入 v2 也不强行映射**，避免伪造已存在的 workflow。P0 阶段**不新增 mode**。

| 触发条件 | 路径类型 | 处理 |
|----------|----------|------|
| `template` / `template_name` in context | early handled path | 标记 `template=requested`；当前若无 TemplateWorkflow 则走 dag_workflow fallback |
| `force_research=true` | 已有 mode | → `RouteResearchV2` |
| `force_swarm=true` | 已有 mode | → `RouteSwarmWorkflow` |
| `force_sandbox=true` 且 `allow_sandbox=true` | 已有 mode | → `RouteSandboxExecution` |
| `force_sandbox=true` 且 `allow_sandbox=false` | 已实现但被拒 | rejected + `sandbox_disallowed_by_user` |
| `skip_synthesis=true` | 已有 mode | → `RouteDirectAnswer` |
| `agent=...` / `role=browser_use` | future extension | 标记 `role=browser_capability_hint`；当前不实现 BrowserUseWorkflow，记入 future extension |
| 浏览器意图自动检测（"go to URL / click / login / fill form"） | future extension | 同上，**不纳入 Phase 7I P0** |
| `role=sandbox` | 已有 mode | → `RouteSandboxExecution` |

**Early Route 的 priority 高于 v2 全部打分**。

### 1.2 Router v2 Signal Layer（信号提取）

从 `query` / `input.*` / `user_intent` 提取以下信号：

| 信号 | 类型 | 用途 |
|------|------|------|
| `ComplexityOverall` | float 0-1 | 主分数 |
| `ComplexityAnalyze` | float | 分析需求强度 |
| `ComplexityResearch` | float | 研究需求强度 |
| `ComplexityExecution` | float | 执行/代码需求强度 |
| `ComplexityDebate` | float | 辩论/对比强度 |
| `ComplexityExploration` | float | 多路径探索强度 |
| `ComplexityMultiAgent` | float | 多 agent 协作强度 |
| `RequiresTools` | bool | 工具需求 |
| `RequiresRAG` | bool | 知识库需求 |
| `RequiresSandbox` | bool | 沙箱需求 |
| `RequiresWorkspace` | bool | 工作区需求 |
| `RequiresCitations` | bool | 引用需求 |
| `BudgetUSD` | float | 预算 |
| `MaxLatencyMs` | int | 延迟约束 |
| `RiskLevel` | string | 风险等级（low/medium/high） |
| `AllowTools` / `AllowResearch` / `AllowSandbox` | bool | 用户开关 |
| `QueryCharCount` / `HasNumberedList` | int/bool | 长度/结构信号 |
| `SandboxRiskKeywords` | bool | 高风险词（rm, sudo, curl \| sh 等） |
| `ApprovalKeywords` | bool | 审批词（"审批" / "approve"） |

### 1.3 Mode Scoring Layer（10 个 mode 打分）

每个 mode 计算一个 base score + capability 增量 + safety 折扣：

| Mode | 触发条件 |
|------|----------|
| `direct_answer` | c < 0.15，无 capability 需求 |
| `react_tool` | 0.15 ≤ c < 0.40 + RequiresTools |
| `rag_answer` | RequiresRAG + c < 0.50 |
| `dag_workflow` | 0.15 ≤ c < 0.55 + 需多步 |
| `reflection` | 0.25 ≤ c < 0.45（分析/写作） |
| `tree_of_thoughts` | 0.50 ≤ c < 0.70（多路径） |
| `debate` | 0.15 ≤ c < 0.55 + 关键词"对比/比较/debate" |
| `research_v2` | RequiresResearch + Citations + c ≥ 0.40 |
| `swarm_workflow` | c ≥ 0.60 + 多 agent 关键词 |
| `sandbox_execution` | RequiresSandbox + allow_sandbox=true |

### 1.4 Capability Composition Layer

Router **不执行**能力，只声明：

```yaml
addon_capabilities:        # 需附加的（如选了 DAG 但也需要 RAG）
  - rag
  - workspace
required_capabilities:     # 选中 mode 必需的
  - dag
  - audit
disabled_capabilities:     # 用户禁用
  - sandbox
  - web_search
workspace_artifacts_expected:
  - dag:{workflow_id}:final
  - workspace:{workflow_id}:report
```

### 1.5 Policy Filtering / Safety Layer

**强约束（不可绕过）**：

| 条件 | 后果 |
|------|------|
| `allow_sandbox=false` | `sandbox_execution` **必须 rejected**，记录 `sandbox_disallowed_by_user` |
| `allow_tools=false` | `react_tool` **必须 rejected**；若 `dag_workflow` 的 `required_capabilities` 包含 `tools` / `mcp_tools`，该 dag_workflow 也 **必须 rejected** 或降级到 `reflection` / `direct_answer`。**不允许静默剥离工具步骤后继续执行不完整 DAG** |
| `allow_research=false` | `research_v2` rejected + 用 `dag_workflow` 替代 |
| `budget_usd < estimated_cost` | mode **降级**（cost-aware fallback），记 `budget_insufficient` |
| `max_latency_ms < estimated_latency_ms` | mode 降级，记 `latency_exceeded` |
| 选中 `sandbox_execution` | `approval_required` **必须 = true**（safety 不可绕过） |
| `risk_level=high` 且 选中非 sandbox | `approval_required=true`，触发审批 gate |
| LLM arbiter 试图把 sandbox 降级为 react_tool | **拒绝**（记 `classifier_tried_to_downgrade_sandbox_rejected`） |

### 1.6 LLM-assisted Router Arbiter（二级裁决）

**仅在以下情况触发**，不是默认主路径：

| 触发条件 |
|----------|
| 多个高级 mode（debate / ToT / research_v2）分数差距 < 0.10 |
| `RiskLevel=high` + 任何非 sandbox mode 胜出 |
| 启发式选了 `direct_answer` 但 `ComplexitySemantic > 0.5`（suspicious_direct_clear_winner） |
| `ComplexitySemantic > 0.7`（语义复杂冲突） |
| `RequiresCitations=true + AllowResearch=true`（research_v2 候选） |

**硬约束**：

- LLM 只能**重排**候选，不能新增 mode
- LLM **不能降级** sandbox / untrusted code 路径
- LLM 失败时**必须 fallback** 到 heuristic，不得导致任务失败
- LLM 调用的 metadata（`classifier_used=true` + reason/confidence/provider/model_used）**必须透传**到 frontend contract

### 1.7 Frontend Contract Layer

稳定输出（前端可以无条件消费）：

```yaml
planned_mode:            string
executed_mode:           string  # 可能与 planned 不同（capability 降级后）
fallback_reason:         string  # "" 表示无降级
classifier_used:         bool
classifier_reason:       string
classifier_confidence:   float
provider:                string
model_used:              string
mock:                    bool
candidates:              [{mode, score, reason}]
rejected_modes:          [{mode, reason}]   # reason 必须非空
score_breakdown:         {mode: score}
signals:                 {完整 signal 字典}
approval_required:       bool
async_required:          bool
addon_capabilities:      [string]
required_capabilities:   [string]
disabled_capabilities:   [string]
workspace_artifacts_expected: [string]
frontend_explanation:    {selected_mode_human, why, capability_summary, cost_estimate, approval_needed}
raw_debug:               {内部审计用}
```

---

## 2. 当前已经完成的内容

### 2.1 Router v2 基础实现

- `internal/activities/router.go` — V1 if/else 链（仅 LegacyHeuristicEnabled 时使用）
- `internal/activities/router_v2.go` — V2 多信号打分（主路径）
- `internal/activities/router_classifier.go` — capability 分类
- `internal/workflows/router.go` — `AdvancedRoutingWorkflow` 入口
- `internal/activities/dag_visual.go` — DAG 节点 metadata

### 2.2 测试

- `internal/activities/router_v2_test.go` — 833 行
- `test_phase6e3_rag_smoke.sh` — RAG smoke
- `test_dag_concurrency.sh` — DAG 并发 smoke
- `test_advanced_router_smoke.sh` / `test_advanced_router_e2e.sh` — router smoke
- `test_dag_dynamic_replan.sh` — 动态重规划

### 2.3 Frontend Contract 样本

- `testdata/router/output/frontend_contract_samples/` 10 个样本（fc_dag_pipeline / fc_debate / fc_direct_simple / fc_full_payload / fc_rag_local / fc_react_calculation / fc_reflection / fc_research_v2 / fc_sandbox / fc_tot）

### 2.4 Final Smoke 报告

- `8657855 docs: update Phase 7I final smoke report with Fix-stage before/after` — NO-GO

### 2.5 Fix-stage 部分修复

- `37a3359 phase7I: Fix-stage — heuristic 中文化 + classifier trigger 扩 + sandbox 风险词`

### 2.6 Frontend MVP（已落地，未联调）

- `cribug-agent-web/` 完整 React 18 + Vite + TS 前端
- 三栏 GPT-like 布局
- SSE 流式输出 + 取消 + IME 处理
- Code highlight + 引用徽章 + 复制按钮
- 宝藏地图 DAG 可视化
- session 重命名/删除
- 10 个 router contract samples 注入前端

---

## 3. 当前未完成 / 阻塞问题

### 3.1 P0 — 硬阻塞

#### 3.1.1 Sandbox Approval 回归

**症状**：T9 case（query 含 sandbox 关键词 + low risk）选中 `sandbox_execution` 但 `approval_required=false`。

**期望**：

- `allow_sandbox=false` → `sandbox_execution` rejected，记 `sandbox_disallowed_by_user`
- `allow_sandbox=true` 且选中 `sandbox_execution` → `approval_required=true`（不可绕过）
- LLM arbiter 试图把 sandbox 降级为 react_tool → **拒绝**

**根因**：当前 v2 在 `c=low + sandbox keyword` 时不一定强制 approval。

#### 3.1.2 Classifier Metadata 透传

**症状**：`classifier_used=true` 时，`classifier_reason` / `classifier_confidence` / `provider` / `model_used` / `mock=false` **没有出现在 frontend contract**。

**根因**：`BuildFrontendContract` 只接 `Decision`，不接 `RouterDecisionExplanation` 的 classifier metadata。

### 3.2 P1 — 路由调优

- total matrix 9/25 → 20/25+
- 中文复杂意图（"为什么" / "怎么" / "如何"）未覆盖全
- rejected_modes.reason 字段在 budget / latency / allow flag case 为空
- DAG / Reflection / ToT / Debate / Research / Swarm 边界 case 分数仍偏
- real LLM classifier smoke 未达最终验收

### 3.3 Frontend — 已落地，无需调整

- 完整 MVP 在 `cribug-agent-web/`
- 等 P0 通过后再做正式联调

---

## 4. P0 后端修复计划

**只做两件事**。

### 4.1 Sandbox Approval Regression Fix

**目标**：T9 case 通过 + sandbox safety 不可绕过

**改动**：

1. `internal/activities/router_v2.go` `ValidateForbiddenCombinations` / `ComposeAddons` / mode selection：
   - `allow_sandbox=false` → 强制 reject `sandbox_execution` 候选，记 `sandbox_disallowed_by_user`
   - 选中 `sandbox_execution` → `approval_required=true`（写到 `Expl.ApprovalRequired`）
   - 加 safety 兜底：LLM arbiter 试图 sandbox → react_tool 降级，**拒绝并保留 sandbox + approval**
2. 修正测试断言：
   - `router_v2_test.go`：T9 case 期望 `approval_required=true`
   - 删除"allow_sandbox=false → 强制 approval"反向断言（如有）

### 4.2 Classifier Metadata Plumbing

**目标**：LLM 调用过的 case，frontend contract 能看到 `classifier_used=true / reason / confidence / provider / model_used / mock=false`

**改动**：

1. `internal/activities/router.go` `BuildFrontendContract` 签名扩展：
   - 增加 `explanation *types.RouterDecisionExplanation` 参数
   - 把 `expl.Signals.ClassifierUsed / ClassifierReason / ClassifierConfidence` 写入 contract
   - 把 `expl.ClassifierProvider / ClassifierModel / ClassifierMock` 写入
2. `internal/workflows/router.go` `AdvancedRoutingWorkflow` 拿到 `explanation` 后传给 `BuildFrontendContract`
3. 加断言（test）：
   - `classifier_expected=true` 的 case，必须 `classifier_used=true` 才通过
   - LLM call count 必须 ≥ 1（real smoke）
4. 修正文档：`allow_sandbox=false → 强制 approval` 这类错误语义全部删除，替换为正确语义

### 4.3 P0 明确不做

- ✗ 不大范围调 heuristic
- ✗ 不重写 Router 主流程
- ✗ 不进入 Phase 8
- ✗ 不改 Shannon
- ✗ 不绕过 Approval Gate
- ✗ 不动 Reflection / ToT / Debate / Research v2 实现
- ✗ 不增加新 mode

---

## 5. P1 路由调优计划

P0 通过后再启动。

### 5.1 total matrix 提升（9/25 → 20/25+）

- 梳理 16 个未通过 case 的根因
- 按 mode 调整 base score 权重
- 增强 capability boost 系数
- 修复 7 套 keyword 命中冲突

### 5.2 中文复杂意图增强

- "为什么" / "怎么" / "如何" / "怎么选" / "哪个更好"
- 复杂多步任务（"先 X，再 Y，最后 Z"）
- 暗含对比（"A 和 B 区别"）

### 5.3 rejected_modes.reason 全量补齐

- budget 拒绝 → 写明预算/估算
- latency 拒绝 → 写明延迟/限额
- allow flag 拒绝 → 写明哪个 allow=false
- capability 拒绝 → 写明缺哪个 capability

### 5.4 cost / latency / allow flag 调优

- estimated_cost_usd 精度
- estimated_latency_ms 精度
- allow flag 优先级

### 5.5 P1 明确不做

- ✗ 增删 mode
- ✗ 改 capability 分类
- ✗ 动 Shannon
- ✗ 改 frontend contract schema

---

## 6. Frontend MVP 并行计划

### 6.1 目标

GPT-like Router-aware Console，可独立演示。

### 6.2 已完成

- 三栏布局（session / chat / router panel）
- SSE 流式 + 取消 + IME
- 代码高亮 + 引用徽章 + 复制
- 宝藏地图 DAG 可视化
- session 重命名/删除
- sample/mock contract fallback

### 6.3 后续可加

- 多 Router 候选模式可视化（圆环图）
- 决策理由悬浮卡
- cost / latency / risk 仪表盘
- DAG 节点状态实时刷新（已有）

### 6.4 不做

- ✗ 后端联调（等 P0 通过）
- ✗ 用户系统 / 权限 / 鉴权
- ✗ 实时 LLM 路由预览 UI
- ✗ 多模态 / Tauri 桌面版

---

## 7. 最终验收标准

### 7.1 P0 验收

- [ ] T9 case（sandbox + low risk）`approval_required=true`
- [ ] `allow_sandbox=false` 的 case `sandbox_execution` rejected
- [ ] LLM arbiter 试图 sandbox → react_tool 降级的 case 拒绝
- [ ] `allow_tools=false` 情况下 react_tool rejected；dag_workflow 含 tools capability 时也 rejected 或降级
- [ ] `classifier_expected=true` 的 case，`classifier_used=true` 透传到 frontend contract
- [ ] `classifier_reason` / `classifier_confidence` 透传
- [ ] `provider` / `model_used` / `mock=false` 透传
- [ ] **P0 阶段 real classifier smoke 最低目标**：至少 1~2 个强制 classifier case 真实调用 LLM，frontend contract 中 `classifier_used=true` 能被看到（**不是**至少 8 个；高目标放到 P1 / Final Gate）
- [ ] 单元测试 + smoke 全部通过

### 7.2 P1 验收

- [ ] total matrix ≥ 20/25
- [ ] 至少 8 个 `classifier_expected=true` case 真实触发 `classifier_used=true`
- [ ] 至少 3 个 simple clear winner case `classifier_used=false`
- [ ] `rejected_modes.reason` 在 budget / latency / allow flag case 非空
- [ ] 中文复杂意图（"为什么 / 怎么 / 如何"）正确路由

### 7.3 Frontend MVP 验收

- [x] 页面能启动
- [x] 简单任务能提交并展示回复
- [x] 后端不可用时能 fallback 到 sample
- [x] 右侧能展示 router contract
- [x] JSON debug 可查看完整响应
- [ ] （P0 后）正式联调

---

## 8. 附录

- `archive/phase7i_design_history.md` — v1 / v2 / v3 历史设计全文
- `testdata/router/output/frontend_contract_samples/` — 10 个 router contract 样本
- `internal/activities/router_v2_test.go` — 当前单测
- `docs/superpowers/plans/HighLevelRouter.md` — 本文档
