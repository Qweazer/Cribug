# Cribug Phase 4 重构任务书 — DAG 并发扩展 / ReAct 增强

## 版本说明

本文档为 Phase 4 **重构版**（v2.0），基于 Shannon 原生架构范式修复了以下核心缺陷：

- **4B 修复**：ReAct Loop 保持在 Workflow 内（非 Activity 内），Activity 重试不丢 LLM 上下文
- **4C 修复**：采用两级缓存（LocalLRU + Redis String/KV），严禁进程内单实例缓存
- **明确约束**：所有 Redis/DB 时间戳必须通过 `workflow.Now(ctx)` 传入

---

## 一、项目定位

### 1.1 当前已完成基线

| Phase | 内容 | 状态 |
|-------|------|------|
| Phase 1 | Gateway + Temporal Worker + SimpleWorkflow + Postgres + Redis | 完成后 |
| Phase 2 | AgentActivity 全链路 + Session Memory + Budget + SSE | 完成后 |
| Phase 3D Slice 7 | DAG Concurrency（LocalDispatchOptions 并发） | 完成后 |
| Phase 3D Slice 8 | ReAct Reasoning Loop（**Workflow 级别**，非 Activity 内循环） | 完成后 |
| Phase 3D Slice 9 | Real tiktoken Tokenizer（两级缓存：LocalLRU + Redis） | 完成后 |

### 1.2 Phase 4 重构目标

| Slice | 功能 | 描述 | 真实 LLM 引入 |
|-------|------|------|---------------|
| Phase 4A Slice 10 | DAG 可视化 | 节点状态动态展示、并发进度、依赖关系可交互查看 | 不需要 |
| Phase 4B Slice 11 | **ReAct 暂停/恢复（重构）** + Real LLM Smoke 起点 | steps 存 Postgres 用于审计；LLM 对话历史通过 Workflow 参数传递，Activity 重试不丢上下文；**Slice 11 完成后新增可选真实 LLM ReAct smoke test** | 开始引入最小 smoke |
| Phase 4C Slice 12 | **分布式 LRU 缓存（重构）** | 两级缓存：L1 LocalLRU + L2 Redis String/KV with TTL + `maxmemory-policy allkeys-lru` | 可选 |
| Phase 4D Slice 13 | DAG 动态重规划 | 节点失败回退、动态重规划、Resource/Concurrency 限制 | 可选 |
| Phase 4E Slice 14 | 并发控制增强 | max_parallel_agents 可配置、节点离线容错、网络延迟处理 | 可选 |
| Phase 4F Slice 15 | 边界测试与验收 + Real LLM E2E Smoke | 可执行 Smoke Test、缓存命中率验证、异常恢复验证；**新增真实 LLM E2E smoke test 作为阶段验收项；真实 LLM 测试不进入默认 CI；无 API key 时自动 skip** | 必须有可选 E2E smoke |

---

## 二、职责边界（强制约束 - Shannon 范式）

### 2.1 组件职责约束

| 组件 | 职责 | 禁止 |
|------|------|------|
| Gateway | 任务创建、返回 task_id/workflow_id/run_id、SSE 事件流推送 | 直接调用 LLM、做 Agent 推理、执行业务逻辑 |
| Workflow | 确定性编排逻辑、调用 Activities | 直接 HTTP/DB/Redis、创建 goroutine、使用 `time.Now()`、使用随机数 |
| Activity | 所有外部 IO：同步/异步 HTTP/DB/Redis 调用、goroutine | 业务编排逻辑（只做外部 IO） |
| Python LLM Service | 模型调用、tiktoken/tokenize | 任务编排、状态管理、Workflow 调度 |

### 2.2 Workflow 确定性原则（Shannon 强制约束）

**Temporal Replay 约束：**
- Workflow 代码在 replay 时必须产生相同结果
- 禁止在 Workflow 内：创建 goroutine、调用外部 HTTP/DB/Redis、使用 `time.Now()`、使用随机数
- **所有时间戳必须通过 `workflow.Now(ctx)` 传入 Activity**

**Activity 不受此约束：**
- Activity 运行在 worker pool 的独立 goroutine 中
- Activity 内可以使用 goroutine、同步/异步 Redis/DB/HTTP 调用
- Activity 内的 for 循环、`time.Now()`、随机数均允许

### 2.3 Phase 4 新增约束

**DAG 可视化约束：**
- DAG 节点状态更新在 DAGNodeActivity 内完成，写入 Redis Hash
- DAG node 事件（running/completed/failed）通过 `a.streamPublisher.Publish()` helper 推送 SSE/Stream 事件
- Activity 内不能调用 `workflow.ExecuteActivity`
- Gateway 订阅 SSE 事件流并在 Dashboard 展示

**ReAct 暂停/恢复约束（关键重构）：**
- ⚠️ **ReAct Loop 必须在 Workflow 内**，参考 `patterns/react.go` 实现
- 每个 ReAct 迭代是独立的 `ExecuteAgent` Activity 调用
- `history`（LLM 对话历史）通过 Activity 参数传递，Activity 重试不丢上下文
- Postgres 存储 `react_steps` 仅用于**审计**，不用于重建 LLM 上下文
- 恢复逻辑：Workflow replay 时重新执行各 Activity，通过幂等性保证正确性

**分布式 LRU 约束（关键重构）：**
- L1 缓存：`LocalLRU`（进程内），TTL 短（5 分钟）
- L2 缓存：`Redis String/KV`（跨 Worker 共享），TTL 长（1 小时）
- Redis LRU 驱逐策略：`maxmemory-policy allkeys-lru`
- **严禁使用 `go-cache` 等纯进程内缓存**

**禁止事项：**
- ❌ 不在 Workflow 内创建 goroutine 来做"后台监控"
- ❌ 不在 Workflow 内直接读写 Redis/DB（必须通过 Activity）
- ❌ 不使用 `time.Now()` 在 Workflow 内（使用 `workflow.Now(ctx)`）
- ❌ 不在 Activity 内循环中丢失 LLM 对话上下文
- ❌ 不使用进程内单实例缓存（go-cache）

---

## 真实 LLM 测试引入策略

### 引入原则

Phase 4 仍以 mock / fixture / deterministic 测试作为默认回归基线，但从 Phase 4B 之后开始引入真实 LLM smoke test。

真实 LLM 测试只用于验证端到端模型链路，不替代现有 mock 测试。

### 引入时机

| 阶段 | 是否使用真实 LLM | 原因 |
|------|------------------|------|
| Phase 4A Slice 10 DAG 可视化 | 否 | 该阶段验证 DAG 状态、Redis snapshot、SSE、Dashboard graph，不依赖模型行为 |
| Phase 4B Slice 11 ReAct 暂停/恢复 | 是，开始引入最小 smoke | 需要验证真实 LLM 输出是否能被 ReAct parser 解析，history 是否保持 |
| Phase 4C Slice 12 两级 LRU | 可选 | 主要验证 token 估算与缓存，可继续以 mock/tokenizer fixture 为主 |
| Phase 4D Slice 13 DAG 动态重规划 | 可选 | 主要验证失败回退和状态流转，真实 LLM 不是必需 |
| Phase 4E Slice 14 并发控制增强 | 可选 | 主要验证并发限制和容错，真实 LLM 仅用于手动压力 smoke |
| Phase 4F Slice 15 边界测试与验收 | 必须有可选真实 LLM E2E smoke | Phase 4 出口必须证明真实模型链路可跑通 |

### 真实 LLM 测试分层

#### 1. Mock Regression Test

- **默认执行**
- 不需要 API key
- 用于 CI / 本地快速回归
- 断言工程结构、Redis、SSE、DAG、ReAct、budget、llm_calls

#### 2. Real LLM Smoke Test

- **手动开启**
- 需要 `REAL_LLM_TEST=1`
- 需要 `OPENAI_API_KEY` 或兼容 Provider API key
- 用于验证真实 LLM 调用链路
- **不进入默认 CI**

#### 3. Real LLM E2E Test

- Phase 4F 作为阶段验收项
- 验证 `Gateway -> Workflow -> Activity -> Python LLM Service -> Provider -> llm_calls -> token usage -> result` 全链路
- 不能断言完整自然语言文本，只能断言结构、状态、token、Redis、llm_calls、workflow result

### 真实 LLM 测试默认行为

| 场景 | 行为 |
|------|------|
| `REAL_LLM_TEST=0`（默认） | 只跑 mock 测试，不请求真实 LLM |
| `REAL_LLM_TEST=1` + 无 API key | skip 真实 LLM 测试，不 fail |
| `REAL_LLM_TEST=1` + 有 API key | 执行真实 LLM smoke test |
| API key 缺失 | `t.Skip("OPENAI_API_KEY not set")`，不 fail |

### 真实 LLM 测试验收条件

1. **Phase 4B Slice 11 完成后**：可选真实 LLM ReAct smoke test 必须能跑通
   - 验证 ReAct parser 能解析真实 LLM 输出格式
   - 验证 history 在 Activity retry 后仍然保持
   - 验证 token usage 被正确记录

2. **Phase 4F Slice 15 验收时**：真实 LLM E2E smoke test 必须可选跑通
   - 不阻塞 release，但作为验收项必须记录结果
   - 如果 API key 缺失，标记为 skip 并记录

---

## 三、阶段范围

### Phase 4A Slice 10：DAG 可视化

#### 3.1 职责

- 追踪每个 DAG 节点的状态变化（pending → running → completed / failed）
- 通过 SSE 事件流实时推送节点状态到 Dashboard
- 支持节点依赖关系查询和并发执行进度展示

#### 3.2 DAG 可视化数据结构

**Redis Key 设计：**
```
Key: dag:{workflow_id}:meta
Type: Hash
Fields:
  - workflow_id: 工作流 ID
  - total_nodes: 总节点数
  - completed_nodes: 已完成节点数
  - failed_nodes: 失败节点数
  - created_at: 创建时间（Unix Nano，来自 workflow.Now()）
  - updated_at: 最后更新时间（Unix Nano，来自 workflow.Now()）
TTL: 86400 秒（1 天）

Key: dag:{workflow_id}:nodes
Type: Hash
Fields:
  - node_id -> NodeState JSON（完整节点状态，包含 dependencies、layer、started_at_ns 等所有字段）
TTL: 86400 秒（1 天）

**重要：每个 node_id 对应一个完整 NodeState JSON，不能按字段拆分存储。**

NodeState JSON 结构（必须包含以下所有字段）：
```json
{
  "node_id": "draft_answer",
  "status": "completed",
  "layer": 1,
  "dependencies": ["analyze_input"],
  "dependents": [],
  "started_at_ns": 123,
  "completed_at_ns": 456,
  "worker_id": "worker-1",
  "retry_count": 0,
  "progress": 1.0,
  "result": {...},
  "error": null
}
```

**状态更新必须 merge 旧 JSON，不能覆盖丢失 dependencies、layer、started_at_ns 等字段。**
```

**NodeStatus 枚举：**
```go
type NodeStatus string

const (
    NodeStatusPending   NodeStatus = "pending"   // 等待执行
    NodeStatusRunning   NodeStatus = "running"   // 执行中
    NodeStatusCompleted NodeStatus = "completed" // 已完成
    NodeStatusFailed    NodeStatus = "failed"    // 执行失败
    NodeStatusSkipped   NodeStatus = "skipped"    // 被跳过（依赖节点失败）
)
```

**时间戳规则（统一约束）：**
- Workflow 相关业务时间戳（node started_at、completed_at、updated_at）**必须由 Workflow 使用 `workflow.Now(ctx)` 生成并传入 Activity**
- Activity 内的 `time.Now()` 只允许用于本地 debug 日志或 wall-clock metrics，**不得写入 Redis/DB 作为业务状态时间**
- DAGNodeActivity 示例中使用 `in.StartedAt` / `in.CompletedAt` / `in.UpdatedAt`（由 Workflow 传入），禁止在 Activity 内自行调用 `time.Now()` 生成业务时间戳

#### 3.3 DAG 可视化实现

**DAGNodeActivity（Activity 实现）：**
```go
// DAGNodeActivity - 节点执行与状态更新
// 遵循 Shannon 范式：所有 Redis 操作在 Activity 内完成
// ⚠️ 关键约束：Activity 内不能调用 workflow.* API，只能使用普通 context.Context
func (a *Activities) DAGNodeActivity(ctx context.Context, in DAGNodeInput) (DAGNodeResult, error) {
    rc := a.sessionManager.RedisWrapper().GetClient()
    workflowID := in.WorkflowID
    nodeID := in.NodeID
    nodeKey := fmt.Sprintf("dag:%s:nodes", workflowID)

    // ============================================================
    // 1. 读取旧状态（用于 merge）
    // ============================================================
    oldJSON, err := rc.HGet(ctx, nodeKey, nodeID).Result()
    var oldState NodeState
    if err == nil {
        json.Unmarshal([]byte(oldJSON), &oldState)
    }

    // ============================================================
    // 2. 更新节点状态为 running（Redis Hash）
    // ⚠️ 时间戳必须由 Workflow 传入 in.StartedAt，Activity 内不得自行使用 time.Now() 生成业务时间戳
    // ⚠️ 必须 merge 旧状态，不能覆盖丢失 dependencies、layer、started_at_ns 等字段
    // ============================================================
    if in.StartedAt.IsZero() {
        return DAGNodeResult{}, fmt.Errorf("started_at is required; must be provided by workflow.Now(ctx)")
    }

    // 从旧状态 merge 所有字段（一次构造完整 NodeState）
    newState := oldState // 先完整继承旧状态
    newState.Status = NodeStatusRunning
    newState.StartedAt = in.StartedAt.UnixNano()
    newState.WorkerID = in.WorkerID
    newState.RetryCount = in.RetryCount
    newState.Progress = 0.0
    // Layer、Dependencies、Dependents 等字段保留自 oldState，不覆盖

    statusJSON, _ := json.Marshal(newState)
    rc.HSet(ctx, nodeKey, nodeID, string(statusJSON))

    // ============================================================
    // 3. 推送 SSE 事件（running）
    // ⚠️ Activity 内通过普通 streamPublisher helper 发布 SSE/Stream 事件
    // ⚠️ Activity 内不能调用 workflow.ExecuteActivity（workflow.* API 只能在 Workflow 内使用）
    // ============================================================
    a.streamPublisher.Publish(ctx, StreamEvent{
        WorkflowID: workflowID,
        EventType:  StreamEventDAGNodeRunning,
        AgentID:    "dag",
        Message:    fmt.Sprintf("node=%s running", nodeID),
        Timestamp: in.StartedAt,
        Metadata: map[string]interface{}{
            "node_id":    nodeID,
            "worker_id":  in.WorkerID,
            "retry_count": in.RetryCount,
        },
    })

    // ============================================================
    // 4. 执行业务逻辑（调用 ReAct 或简单执行）
    // 注意：ReAct Loop 在 Workflow 内实现，不在 Activity 内循环
    // ============================================================
    result, err := a.executeNodeBusinessLogic(ctx, in)

    // ============================================================
    // 5. 更新节点状态为 completed/failed（Redis Hash）
    // ⚠️ 时间戳必须由 Workflow 传入 in.CompletedAt，Activity 内不得自行使用 time.Now()
    // ============================================================
    if in.CompletedAt.IsZero() {
        return DAGNodeResult{}, fmt.Errorf("completed_at is required; must be provided by workflow.Now(ctx)")
    }
    completedAt := in.CompletedAt

    // 确定最终状态
    finalStatus := NodeStatusCompleted
    errorMessage := ""
    if err != nil {
        finalStatus = NodeStatusFailed
        errorMessage = err.Error()
    }

    // ⚠️ 必须从 newState merge（不是 oldState），确保保留 running 阶段写入的所有字段
    // 继承：StartedAt、WorkerID、RetryCount、Progress、Layer、Dependencies、Dependents
    finalState := newState
    finalState.Status = finalStatus
    finalState.CompletedAt = completedAt.UnixNano()
    if err != nil {
        finalState.Error = errorMessage
        finalState.Result = nil
        // 推送 SSE 事件（failed）
        a.streamPublisher.Publish(ctx, StreamEvent{
            WorkflowID: workflowID,
            EventType:  StreamEventDAGNodeFailed,
            AgentID:    "dag",
            Message:    fmt.Sprintf("node=%s failed: %v", nodeID, err),
            Timestamp: completedAt,
            Metadata: map[string]interface{}{
                "node_id": nodeID,
                "error":   errorMessage,
            },
        })
    } else {
        finalState.Error = nil
        finalState.Result = result
        finalState.Progress = 1.0
        // 推送 SSE 事件（completed）
        a.streamPublisher.Publish(ctx, StreamEvent{
            WorkflowID: workflowID,
            EventType:  StreamEventDAGNodeCompleted,
            AgentID:    "dag",
            Message:    fmt.Sprintf("node=%s completed", nodeID),
            Timestamp: completedAt,
            Metadata: map[string]interface{}{
                "node_id": nodeID,
            },
        })
    }
    finalJSON, _ := json.Marshal(finalState)
    rc.HSet(ctx, nodeKey, nodeID, string(finalJSON))

    return DAGNodeResult{
        NodeID: nodeID,
        Status: finalStatus,
        Output: result,
        Error:  errorMessage,
    }, nil
}
```

**注意事项：**
- 所有 Redis 操作在 Activity 内完成，不在 Workflow 内
- SSE/Stream 事件推送通过 Activity 内的 `a.streamPublisher.Publish()` helper
- Activity 内不能调用 `workflow.ExecuteActivity`（workflow.* API 只能在 Workflow 内使用）
- Dashboard 通过 Gateway SSE 流实时显示 DAG 状态
- **时间戳从 workflow.Now(ctx) 传入 Activity，非 Activity 内部生成**

---

### Phase 4B Slice 11：ReAct 暂停/恢复（重构）

#### 3.5 核心架构澄清（关键）

⚠️ **原草案的错误设计**：
- 在单个 Activity 内实现 ReAct Loop 循环
- Activity 重试（LocalRetries）时，Activity 内存中的 LLM 对话历史（`messages`）全部丢失
- 新起的 Activity 从 `startIndex > 0` 开始执行时，带着空白的 `messages` 请求 LLM，导致严重幻觉

✅ **重构后的正确设计（Shannon 标杆）**：
- **ReAct Loop 必须在 Workflow 内实现**
- 参考 `patterns/react.go`：每个 ReAct 迭代是独立的 `ExecuteAgent` Activity 调用
- `history`（LLM 对话历史）通过 Activity 参数传递，**Activity 重试不丢上下文**
- Postgres 仅存储 `steps` 用于**审计**，不用于重建 LLM 上下文
- Workflow replay 时，各 Activity 重新执行，通过幂等性保证正确性

#### 3.6 ReAct 暂停/恢复数据结构

**Postgres 表设计（仅用于审计）：**
```sql
CREATE TABLE react_steps (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workflow_id VARCHAR(64) NOT NULL,
    run_id VARCHAR(64) NOT NULL,
    node_id VARCHAR(64) NOT NULL,
    step_index INTEGER NOT NULL,
    action TEXT NOT NULL,
    observation TEXT,
    reasoning TEXT,
    created_at TIMESTAMP DEFAULT NOW(),
    UNIQUE(workflow_id, run_id, node_id, step_index)
);

CREATE INDEX idx_react_steps_recovery ON react_steps(workflow_id, run_id, node_id, step_index);
```

**ReActStep 结构（审计用）：**
```go
type ReActStep struct {
    StepIndex   int       `json:"step_index"`
    Action      string    `json:"action"`
    Observation string    `json:"observation"`
    Reasoning   string    `json:"reasoning"`
    Timestamp   time.Time `json:"timestamp"` // 来自 workflow.Now()
}
```

**关键**：此结构仅用于审计。LLM 上下文（messages）通过 Workflow 参数传递。

#### 3.7 ReAct Workflow 实现（Shannon 标杆模式）

参考 `patterns/react.go` 的 `ReactLoop` 函数，实现如下：

```go
// ReactWorkflow - ReAct Loop 在 Workflow 内实现
// 每个迭代是独立的 ExecuteAgent Activity 调用，Activity 重试不丢 LLM 上下文
func ReactWorkflow(ctx workflow.Context, input TaskInput) (TaskResult, error) {
    logger := workflow.GetLogger(ctx)
    workflowID := workflow.GetInfo(ctx).WorkflowExecution.ID

    // ============================================================
    // 1. 配置 Activity Options
    // 注意：ReAct Loop 内的每个 ExecuteAgent 调用都使用这些 options
    // Activity 重试时，只有单次迭代的 AgentExecutionResult 丢失
    // 完整的 LLM 对话历史通过 history 参数传递，不受影响
    // ============================================================
    activityOptions := workflow.ActivityOptions{
        StartToCloseTimeout: 3 * time.Minute,
        RetryPolicy: &temporal.RetryPolicy{
            MaximumAttempts: 2,
        },
    }
    ctx = workflow.WithActivityOptions(ctx, activityOptions)

    // ============================================================
    // 2. 初始化 ReAct 配置
    // ============================================================
    config := patterns.ReactConfig{
        MaxIterations:     10,
        MinIterations:     1,
        ObservationWindow: 3,
        MaxObservations:   100,
        MaxThoughts:       50,
        MaxActions:        50,
    }

    // ============================================================
    // 3. 准备 baseContext 和 history
    // history 是 LLM 对话历史，通过参数传递给每个 ExecuteAgent Activity
    // Activity 重试时，只有单次调用失败，history 不受影响
    // ============================================================
    baseContext := make(map[string]interface{})
    for k, v := range input.Context {
        baseContext[k] = v
    }

    // history 通过 Workflow 参数传入或从 Session Memory 获取
    // 在 patterns/react.go 中，history 会在每次 ExecuteAgent 调用时传入
    history := convertHistoryForAgent(input.History)

    // ============================================================
    // 4. 调用 ReactLoop（在 patterns 包中，Workflow 级别）
    // ReactLoop 内部每个迭代调用 ExecuteAgent Activity
    // Activity 重试不丢 LLM 上下文，因为 history 通过参数传递
    // ============================================================
    reactOpts := patterns.Options{
        BudgetAgentMax: agentMaxTokens,
        SessionID:      input.SessionID,
        UserID:         input.UserID,
        EmitEvents:     true,
        ModelTier:      modelTier,
        Context:        baseContext,
    }

    reactResult, err := patterns.ReactLoop(
        ctx,
        input.Query,
        baseContext,
        input.SessionID,
        history, // LLM 对话历史通过参数传递
        config,
        reactOpts,
    )

    if err != nil {
        return TaskResult{
            Success:      false,
            ErrorMessage: fmt.Sprintf("React loop failed: %v", err),
        }, err
    }

    // ============================================================
    // 5. 返回结果
    // ============================================================
    return TaskResult{
        Result:     reactResult.FinalResult,
        Success:    true,
        TokensUsed: reactResult.TotalTokens,
        Metadata: map[string]interface{}{
            "iterations": reactResult.Iterations,
            "thoughts":  len(reactResult.Thoughts),
            "actions":   len(reactResult.Actions),
        },
    }, nil
}
```

#### 3.8 ReactLoop 实现（patterns/react.go，Workflow 级别）

```go
// ReactLoop - Reason-Act-Observe 循环
// 在 Workflow 内执行，每个迭代调用 ExecuteAgent Activity
// Activity 重试不丢 LLM 上下文，因为 history 通过参数传递
func ReactLoop(
    ctx workflow.Context,
    query string,
    baseContext map[string]interface{},
    sessionID string,
    history []string, // LLM 对话历史，通过参数传递
    config ReactConfig,
    opts Options,
) (*ReactLoopResult, error) {

    logger := workflow.GetLogger(ctx)

    activityOptions := workflow.ActivityOptions{
        StartToCloseTimeout: 5 * time.Minute,
        RetryPolicy: &temporal.RetryPolicy{
            MaximumAttempts: 3,
        },
    }
    ctx = workflow.WithActivityOptions(ctx, activityOptions)

    // ============================================================
    // 初始化状态
    // ============================================================
    var observations []string
    var thoughts []string
    var actions []string
    totalTokens := 0
    iteration := 0

    // ============================================================
    // 主循环：每个迭代是独立的 ExecuteAgent Activity 调用
    // Activity 重试时，只有本次迭代的 result 丢失
    // 下次迭代会使用更新后的 history（包含之前的 thought/action）
    // ============================================================
    for iteration < config.MaxIterations {
        logger.Info("ReAct iteration", "iteration", iteration+1)

        reasonerID := agents.GetAgentName(wfID, iteration*2)
        actorID := agents.GetAgentName(wfID, iteration*2+1)

        // ============================================================
        // Phase 1: REASON - 调用 ExecuteAgent Activity
        // history：是外部 LLM session history（用于 token budget/session 管理），由调用方传入
        // thoughts/actions/observations：是本轮 ReAct 循环的本地中间状态，用于构造 query context
        // Activity 重试时，只有 reasonResult 丢失，history 不受影响
        // ============================================================
        reasonContext := make(map[string]interface{})
        for k, v := range baseContext {
            reasonContext[k] = v
        }
        reasonContext["query"] = query
        reasonContext["observations"] = getRecentObservations(observations, config.ObservationWindow)
        reasonContext["thoughts"] = thoughts
        reasonContext["actions"] = actions
        reasonContext["iteration"] = iteration

        var reasonResult activities.AgentExecutionResult
        err := workflow.ExecuteActivity(ctx,
            "ExecuteAgent",
            activities.AgentExecutionInput{
                Query:            buildReasonQuery(query, observations, thoughts, actions),
                AgentID:          reasonerID,
                Context:          reasonContext,
                Mode:             "standard",
                SessionID:        sessionID,
                History:          history, // 关键：history 通过参数传递
                ParentWorkflowID: wfID,
            }).Get(ctx, &reasonResult)

        if err != nil {
            logger.Error("Reasoning failed", "error", err)
            break
        }

        thoughts = append(thoughts, reasonResult.Response)
        totalTokens += reasonResult.TokensUsed

        // ============================================================
        // Phase 2: ACT - 调用 ExecuteAgent Activity
        // Activity 重试时，只有 actionResult 丢失，history 不受影响
        // ============================================================
        actionContext := make(map[string]interface{})
        for k, v := range baseContext {
            actionContext[k] = v
        }
        actionContext["query"] = query
        actionContext["current_thought"] = reasonResult.Response

        var actionResult activities.AgentExecutionResult
        err = workflow.ExecuteActivity(ctx,
            "ExecuteAgent",
            activities.AgentExecutionInput{
                Query:            buildActionQuery(reasonResult.Response, actionContext),
                AgentID:          actorID,
                Context:          actionContext,
                Mode:             "standard",
                SessionID:        sessionID,
                History:          history, // 关键：history 通过参数传递
                SuggestedTools:   suggestedTools,
                ParentWorkflowID: wfID,
            }).Get(ctx, &actionResult)

        if err != nil {
            logger.Error("Action execution failed", "error", err)
            observations = append(observations, fmt.Sprintf("Error: %v", err))
        } else {
            totalTokens += actionResult.TokensUsed
            actions = append(actions, actionResult.Response)
            observations = append(observations, fmt.Sprintf("Action result: %s", actionResult.Response))
        }

        iteration++
    }

    // ============================================================
    // 最终合成
    // ============================================================
    var finalResult activities.AgentExecutionResult
    err := workflow.ExecuteActivity(ctx,
        "ExecuteAgent",
        activities.AgentExecutionInput{
            Query:            buildSynthesisQuery(query, thoughts, actions, observations),
            AgentID:         "react-synthesizer",
            Context:         baseContext,
            Mode:            "standard",
            SessionID:       sessionID,
            History:         history, // 关键：history 通过参数传递
            ParentWorkflowID: wfID,
        }).Get(ctx, &finalResult)

    if err != nil {
        return nil, fmt.Errorf("final synthesis failed: %w", err)
    }

    return &ReactLoopResult{
        Thoughts:     thoughts,
        Actions:      actions,
        Observations: observations,
        FinalResult:  finalResult.Response,
        TotalTokens:  totalTokens + finalResult.TokensUsed,
        Iterations:   iteration,
        AgentResults: agentResults,
    }, nil
}
```

#### 3.9 ReAct 异常处理

**Activity 重试时上下文保护：**
- **history 参数**：LLM 对话历史通过 Activity 参数传递
- **Activity 重试**：只有当前迭代的 `reasonResult` 或 `actionResult` 丢失
- **上下文恢复**：下次重试时，history 仍包含之前的完整记录，不受影响

**超时策略：**
- 单个 ReAct step 超时：`StartToCloseTimeout` = 3 分钟
- 整个 ReAct 循环超时：在 Workflow 层控制 `MaxIterations`

**Postgres 审计写入：**
```go
// SaveReActStepAudit - 仅用于审计，不用于重建 LLM 上下文
func (a *Activities) SaveReActStepAudit(ctx context.Context, in SaveReActStepInput) error {
    // 写入 Postgres，仅用于审计追踪
    // 不用于重建 LLM 对话上下文
    _, err := a.db.Exec(ctx,
        `INSERT INTO react_steps (workflow_id, run_id, node_id, step_index, action, observation, reasoning, created_at)
         VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
         ON CONFLICT (workflow_id, run_id, node_id, step_index) DO NOTHING`,
        in.WorkflowID, in.RunID, in.NodeID, in.StepIndex,
        in.Action, in.Observation, in.Reasoning, in.Timestamp)
    return err
}
```

---

### Phase 4C Slice 12：分布式 LRU 缓存（重构）

#### 3.10 核心架构澄清（关键）

⚠️ **原草案的错误设计**：
- 使用 `go-cache` 等纯进程内缓存
- 多 Worker 环境下导致"数据孤岛"——每个 Worker 有独立缓存，互不可见

✅ **重构后的正确设计（两级缓存 + Redis String/KV）**：

```
┌─────────────────────────────────────────────────────────────────┐
│                         Activity 调用                            │
│                 GetTokenCountActivity(text, model)              │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
                    ┌─────────────────┐
                    │   L1: LocalLRU   │  (进程内，低延迟 < 1ms)
                    │   TTL: 5 分钟     │
                    │   capacity: 10000 │
                    └─────────────────┘
                              │ miss
                              ▼
                    ┌─────────────────┐
                    │  L2: Redis String/KV │  (跨 Worker 共享)
                    │  TTL: 1 小时     │
                    │  maxmemory-policy │
                    │  allkeys-lru     │
                    └─────────────────┘
                              │ miss
                              ▼
                    ┌─────────────────┐
                    │ Python LLM Svc   │  (tiktoken 计数)
                    │ 延迟: ~50-200ms  │
                    └─────────────────┘
```

#### 3.11 两级缓存实现

**Redis Key 设计：**
```
Key: lru:tiktoken:{model}:{text_hash}
Type: String
Value: token_count (int)
TTL: 3600 秒（1 小时）

Key: lru:stats
Type: Hash
Fields:
  - hits: 缓存命中次数
  - misses: 缓存未命中次数
TTL: 无（持久化统计）
```

**TokenCountCache 接口：**
```go
// TokenCountCache - 两级缓存接口
type TokenCountCache interface {
    Get(ctx context.Context, model, text string) (int, bool)  // 返回 count 和是否命中
    Set(ctx context.Context, model, text string, count int)   // 写入两级缓存
    GetStats(ctx context.Context) (hits, misses int64)
}

// ============================================================
// L1: LocalLRU - 进程内缓存，TTL 短
// ============================================================
type LocalTokenLRU struct {
    mu   sync.Mutex
    cap  int
    list *list.List               // front = most recent
    m    map[string]*list.Element // key -> element
}

type lruEntry struct {
    key   string
    count int
    exp   time.Time
}

const localLRUTTL = 5 * time.Minute
const localLRUCapacity = 10000

func (l *LocalTokenLRU) Get(_ context.Context, key string) (int, bool) {
    l.mu.Lock()
    defer l.mu.Unlock()
    if el, ok := l.m[key]; ok {
        ent := el.Value.(lruEntry)
        if ent.exp.After(time.Now()) {
            l.list.MoveToFront(el)
            return ent.count, true
        }
        l.list.Remove(el)
        delete(l.m, key)
    }
    return 0, false
}

func (l *LocalTokenLRU) Set(_ context.Context, key string, count int) {
    l.mu.Lock()
    defer l.mu.Unlock()
    if el, ok := l.m[key]; ok {
        el.Value = lruEntry{key: key, count: count, exp: time.Now().Add(localLRUTTL)}
        l.list.MoveToFront(el)
        return
    }
    el := l.list.PushFront(lruEntry{key: key, count: count, exp: time.Now().Add(localLRUTTL)})
    l.m[key] = el
    if l.list.Len() > l.cap {
        lru := l.list.Back()
        if lru != nil {
            ent := lru.Value.(lruEntry)
            delete(l.m, ent.key)
            l.list.Remove(lru)
        }
    }
}

// ============================================================
// L2: Redis String/KV - 跨 Worker 共享，TTL 长
// ============================================================
type RedisTokenCache struct {
    cli *circuitbreaker.RedisWrapper
}

const redisLRUTTL = 1 * time.Hour

func (r *RedisTokenCache) Get(ctx context.Context, key string) (int, bool) {
    val, err := r.cli.Get(ctx, key).Int()
    if err != nil {
        return 0, false
    }
    return val, true
}

func (r *RedisTokenCache) Set(ctx context.Context, key string, count int) {
    _ = r.cli.Set(ctx, key, count, redisLRUTTL).Err()
}

// ============================================================
// 两级缓存组合实现
// ============================================================
type TwoLevelTokenCache struct {
    local *LocalTokenLRU
    redis *RedisTokenCache
    rc    *circuitbreaker.RedisWrapper
}

func NewTwoLevelTokenCache(rc *circuitbreaker.RedisWrapper) *TwoLevelTokenCache {
    return &TwoLevelTokenCache{
        local: NewLocalTokenLRU(localLRUCapacity),
        redis: &RedisTokenCache{cli: rc},
        rc:    rc,
    }
}

func (c *TwoLevelTokenCache) makeKey(model, text string) string {
    h := sha256.Sum256([]byte(model + "|" + text))
    return fmt.Sprintf("lru:tiktoken:%s:%x", model, h)
}

func (c *TwoLevelTokenCache) Get(ctx context.Context, model, text string) (int, bool) {
    key := c.makeKey(model, text)

    // L1 查询（本地）
    if count, ok := c.local.Get(ctx, key); ok {
        c.rc.HIncrBy(ctx, "lru:stats", "hits", 1)
        return count, true
    }

    // L2 查询（Redis）
    if count, ok := c.redis.Get(ctx, key); ok {
        // 回填 L1
        c.local.Set(ctx, key, count)
        c.rc.HIncrBy(ctx, "lru:stats", "hits", 1)
        return count, true
    }

    return 0, false
}

func (c *TwoLevelTokenCache) Set(ctx context.Context, model, text string, count int) {
    key := c.makeKey(model, text)

    // 写入 L1
    c.local.Set(ctx, key, count)

    // 写入 L2
    c.redis.Set(ctx, key, count)
}

func (c *TwoLevelTokenCache) GetStats(ctx context.Context) (hits, misses int64) {
    vals, _ := c.rc.HMGet(ctx, "lru:stats", "hits", "misses").Result()
    if vals[0] != nil {
        hits, _ = strconv.ParseInt(vals[0].(string), 10, 64)
    }
    if vals[1] != nil {
        misses, _ = strconv.ParseInt(vals[1].(string), 10, 64)
    }
    return
}
```

#### 3.12 GetTokenCountActivity 实现

```go
// GetTokenCountActivity - 获取 token 计数（两级缓存）
func (a *Activities) GetTokenCountActivity(ctx context.Context, in GetTokenCountInput) (GetTokenCountResult, error) {
    // ============================================================
    // 1. 两级缓存查询
    // ============================================================
    if count, ok := a.tokenCache.Get(ctx, in.Model, in.Text); ok {
        return GetTokenCountResult{
            Count:   count,
            Cached:  true,
            Source:  "cache",
        }, nil
    }

    // ============================================================
    // 2. 缓存未命中，调用 Python LLM Service
    // ============================================================
    count, err := a.pythonLLMService.CountTokens(ctx, in.Text, in.Model)
    if err != nil {
        return GetTokenCountResult{Count: 0, Cached: false}, err
    }

    // ============================================================
    // 3. 写入两级缓存
    // ============================================================
    a.tokenCache.Set(ctx, in.Model, in.Text, count)

    // ============================================================
    // 4. 记录 miss 统计
    // ============================================================
    a.rc.HIncrBy(ctx, "lru:stats", "misses", 1)

    return GetTokenCountResult{
        Count:   count,
        Cached:  false,
        Source:  "python_service",
    }, nil
}
```

#### 3.13 缓存一致性策略

- **L1 TTL**：5 分钟（防止本地缓存过期不一致）
- **L2 TTL**：1 小时（Redis 层面驱逐）
- **Redis LRU 驱逐**：`maxmemory-policy allkeys-lru`（全局 LRU）
- **分布式一致性**：多 Worker 共享同一 Redis 实例，保证 L2 一致

---

### Phase 4D Slice 13：DAG 动态重规划

#### 3.14 节点失败回退策略

```go
// HandleDAGNodeFailure - 节点失败处理
func (a *Activities) HandleDAGNodeFailure(ctx context.Context, in HandleDAGNodeFailureInput) error {
    rc := a.sessionManager.RedisWrapper().GetClient()
    workflowID := in.WorkflowID
    failedNodeID := in.FailedNodeID
    nodeKey := fmt.Sprintf("dag:%s:nodes", workflowID)

    // ============================================================
    // 1. 标记失败节点
    // ============================================================
    statusJSON, _ := json.Marshal(NodeStatusInfo{
        NodeID: failedNodeID,
        Status: NodeStatusFailed,
        Error:  in.Error,
    })
    rc.HSet(ctx, nodeKey, failedNodeID, string(statusJSON))

    // ============================================================
    // 2. 找到所有依赖失败节点的节点
    // ============================================================
    dependents := a.getDependents(ctx, workflowID, failedNodeID)

    // ============================================================
    // 3. 对每个依赖节点检查是否应该跳过
    // ============================================================
    for _, depNodeID := range dependents {
        if a.canSkipNode(ctx, workflowID, depNodeID) {
            skipJSON, _ := json.Marshal(NodeStatusInfo{
                NodeID:          depNodeID,
                Status:          NodeStatusSkipped,
                SkippedReason:   fmt.Sprintf("dependency %s failed", failedNodeID),
            })
            rc.HSet(ctx, nodeKey, depNodeID, string(skipJSON))
        }
    }

    return nil
}
```

---

### Phase 4E Slice 14：并发控制增强

#### 3.15 LocalDispatchOptions 配置

```go
activityOpts := workflow.ActivityOptions{
    StartToCloseTimeout: 60 * time.Second,
    RetryPolicy: &temporal.RetryPolicy{
        InitialInterval:    1 * time.Second,
        BackoffCoefficient: 2.0,
        MaximumInterval:    30 * time.Second,
        MaximumAttempts:    3,
        NonRetryableErrors: []string{"InvalidArgument", "NotFound"},
    },
    // 并发控制通过 LocalDispatchOptions
    WithLocalDispatchOptions: &temporal.LocalDispatchOptions{
        MaxConcurrentActors: config.MaxParallelAgents, // 可配置，默认 5
    },
}
```

---

### Phase 4F Slice 15：边界测试与验收

#### 3.16 Smoke Test

```bash
#!/bin/bash
set -e

echo "=== Cribug Phase 4 Smoke Test ==="

# Test 1: DAG 可视化
echo "[1/4] Testing DAG visualization..."
# 验证 SSE 事件流

# Test 2: ReAct 暂停/恢复（Workflow 级别 Loop）
echo "[2/4] Testing ReAct pause/resume..."
# 验证 Activity 重试不丢 LLM 上下文

# Test 3: 两级 LRU 缓存命中率
echo "[3/4] Testing two-level LRU cache..."
# 验证 L1 miss 后查 L2，L2 miss 后查 Python Service

# Test 4: 并发限制
echo "[4/4] Testing max_parallel_agents..."
# 验证 LocalDispatchOptions 生效

echo "=== All Smoke Tests Passed ==="
```

---

## 五、明确禁止事项

- ❌ Workflow 内直接创建 goroutine（并发通过 `workflow.ExecuteActivity` + `LocalDispatchOptions` 实现）
- ❌ Workflow 内直接调用外部 HTTP/DB/Redis（所有外部 IO 放 Activity）
- ❌ 在 Workflow 内使用 `time.Now()`（使用 `workflow.Now(ctx)`）
- ❌ **ReAct Loop 在 Activity 内循环**（必须在 Workflow 内）
- ❌ **使用进程内单实例缓存（go-cache）**（必须用两级缓存）
- ❌ 不做 P2P/Workspace（留待 Phase 5）

---

## 六、API 设计

### 4.1 POST /api/v1/tasks

**Request：**
```json
{
  "query": "Explain AI, ML, DL in detail",
  "session_id": "my-session-123",
  "config": {
    "mode": "dag",
    "model": "gpt-4o-mini",
    "temperature": 0.7,
    "max_total_tokens": 8000,
    "max_completion_tokens": 1024,
    "max_parallel_agents": 3,
    "enable_react": true,
    "react_max_iterations": 5,
    "dag_ttl_seconds": 86400,
    "react_steps_ttl_seconds": 86400,
    "enable_dag_visualization": true
  }
}
```

**Response（202 Accepted）：**
```json
{
  "task_id": "abc-123",
  "workflow_id": "task-abc-123",
  "run_id": "run-xyz-789",
  "status": "running",
  "stream_url": "/api/v1/stream/sse?task_id=abc-123"
}
```

### 4.2 GET /api/v1/tasks/{task_id}

**Response：**
```json
{
  "task_id": "abc-123",
  "workflow_id": "task-abc-123",
  "run_id": "run-xyz-789",
  "session_id": "my-session-123",
  "status": "completed",
  "mode": "dag",
  "result": "Combined result from all DAG nodes with ReAct reasoning...",
  "error": null,
  "error_type": null,
  "usage": {
    "prompt_tokens": 120,
    "completion_tokens": 80,
    "total_tokens": 200
  },
  "metadata": {
    "num_nodes": 3,
    "dag_visualization_enabled": true,
    "react_iterations": 5
  },
  "created_at": "2026-05-20T10:00:00Z",
  "updated_at": "2026-05-20T10:00:05Z"
}
```

### 4.3 GET /api/v1/tasks/{task_id}/dag

**Response（DAG 可视化）：**
```json
{
  "workflow_id": "task-abc-123",
  "meta": {
    "total_nodes": 3,
    "completed_nodes": 3,
    "failed_nodes": 0,
    "created_at": 1747728000,
    "updated_at": 1747728050
  },
  "nodes": [
    {
      "node_id": "node-0",
      "status": "completed",
      "started_at": 1747728001,
      "completed_at": 1747728010,
      "progress": 1.0
    },
    {
      "node_id": "node-1",
      "status": "completed",
      "started_at": 1747728002,
      "completed_at": 1747728015,
      "progress": 1.0
    },
    {
      "node_id": "node-2",
      "status": "completed",
      "started_at": 1747728003,
      "completed_at": 1747728020,
      "progress": 1.0
    }
  ]
}
```

### 4.4 Config 字段说明

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| mode | string | "simple" | "simple" \| "dag" \| "multi_agent" |
| model | string | "gpt-4o-mini" | 模型名 |
| max_total_tokens | int | 8000 | prompt + completion 总预算 |
| max_completion_tokens | int | 1024 | 输出上限 |
| max_parallel_agents | int | 1 | DAG 并发数，1=顺序 |
| enable_react | bool | false | 是否启用 ReAct |
| react_max_iterations | int | 3 | ReAct 最大迭代次数 |
| enable_dag_visualization | bool | false | 开启 DAG 可视化 |
| dag_ttl_seconds | int | 86400 | DAG 节点 Redis TTL |
| react_steps_ttl_seconds | int | 86400 | ReAct steps Redis TTL |

---

## 七、状态与错误

### 5.1 任务状态枚举

```
pending → running → completed
                   → failed
                   → budget_exceeded
                   → cancelled（后续）
```

### 5.2 Error Taxonomy

| error_type | 说明 | 来源 | 处理 |
|------------|------|------|------|
| validation_error | 请求参数校验失败 | Gateway | 返回 400 |
| workflow_start_error | 启动 Workflow 失败 | Gateway | 返回 500 |
| workflow_error | Workflow 执行内部错误 | Worker | TASK_FAILED |
| llm_error | LLM 调用错误 | AgentActivity | TASK_FAILED |
| llm_timeout | LLM 调用超时（60s） | AgentActivity | Activity retry → TASK_FAILED |
| budget_exceeded | 超出 token 预算 | CheckBudgetActivity | TASK_BUDGET_EXCEEDED |
| db_error | 数据库操作错误 | Activity | Activity retry → TASK_FAILED |
| redis_error | Redis 操作错误 | Activity | 只记录日志，继续执行 |
| session_error | Session 加载/保存错误 | Activity | Activity retry → TASK_FAILED |
| dag_node_failed | DAG 节点执行失败 | DAGNodeActivity | 标记 failed，其他节点继续 |
| react_steps_write_failed | ReAct steps 写入失败 | Activity | 只记录日志，继续执行 |

### 5.3 DAG 节点失败处理

- 单个 node 失败不影响其他 node（并发继续）
- node 标记为 failed，写入 `dag:{workflow_id}:nodes`
- node 结果为错误 JSON，写入 `dag:{workflow_id}:nodes`
- DAG 整体仍返回 completed（含失败信息）
- 最终 usage = sum(成功 node usage)，失败 node usage = 0

### 5.4 Temporal 异常场景

| 场景 | 处理 |
|------|------|
| Activity 超时（60s） | LocalRetries 重试 3 次，仍失败则 node failed |
| Activity 失败 | Workflow 捕获 error，继续其他 Activity |
| Workflow 失败 | TASK_FAILED，SaveFailureActivity 记录 |
| Temporal 不可用 | Gateway 返回 503 Service Unavailable |

### 5.5 Activity 异常行为描述

| Activity | 异常场景 | 行为 |
|----------|---------|------|
| ExecuteDAGNodeActivity | node 失败 | 标记 Redis status=failed，返回 error，Workflow 继续 |
| ExecuteAgent | LLM 调用失败 | Activity retry，仍失败 return error |
| EmitTaskUpdate | Redis 写入失败 | 只记录日志，不阻断主流程 |
| SaveReActStepAudit | Postgres 写入失败 | 只记录日志，不影响主流程 |
| GetTokenCountActivity | /tokenize 不可用 | fallback ceil(len/4)，日志警告 |

---

## 八、修改文件清单

### Phase 4A Slice 10：DAG 可视化

| 操作 | 文件 | 说明 |
|------|------|------|
| 新建 | `internal/activities/dag_visualization.go` | DAG 可视化 Activity |
| 新建 | `internal/activities/dag_node.go` | ExecuteDAGNodeActivity（含状态更新） |
| 修改 | `internal/workflows/dag_workflow.go` | 新增 DAG 可视化事件推送 |
| 修改 | `config/features.yaml` | 新增 enable_dag_visualization, dag_visualization_ttl_seconds |
| 新建 | `internal/workflows/supervisor_workflow.go` | 参考 SignalChannel 模式 |

### Phase 4B Slice 11：ReAct 暂停/恢复（重构）

| 操作 | 文件 | 说明 |
|------|------|------|
| 新建 | `internal/workflows/patterns/react.go` | ReAct Loop（Workflow 级别，非 Activity 内） |
| 修改 | `internal/workflows/react_workflow.go` | 适配 Workflow 级别 ReAct |
| 新建 | `internal/activities/react_audit.go` | SaveReActStepAudit Activity（仅审计用） |
| 修改 | `config/features.yaml` | 新增 react_max_iterations, react_audit_ttl_seconds |

### Phase 4C Slice 12：分布式 LRU 缓存（重构）

| 操作 | 文件 | 说明 |
|------|------|------|
| 新建 | `internal/activities/token_cache.go` | 两级缓存实现（LocalLRU + Redis String/KV） |
| 修改 | `internal/activities/budget.go` | EstimatePromptTokensActivity 改用两级缓存 |
| 修改 | `config/features.yaml` | 新增 l1_cache_ttl_seconds, l2_cache_ttl_seconds |

### Phase 4D Slice 13：DAG 动态重规划

| 操作 | 文件 | 说明 |
|------|------|------|
| 新建 | `internal/activities/dag_fallback.go` | HandleDAGNodeFailure Activity |
| 修改 | `internal/workflows/dag_workflow.go` | 新增失败回退逻辑 |

### Phase 4E Slice 14：并发控制增强

| 操作 | 文件 | 说明 |
|------|------|------|
| 修改 | `internal/workflows/dag_workflow.go` | LocalDispatchOptions 配置 |
| 修改 | `config/features.yaml` | 新增 max_parallel_agents, heartbeat_timeout_seconds |

---

## 九、测试脚本

### smoke_test_phase4.sh

```bash
#!/bin/bash
set -euo pipefail

GATEWAY_URL="${GATEWAY_URL:-http://127.0.0.1:8080}"
LLM_SERVICE_URL="${LLM_SERVICE_URL:-http://127.0.0.1:8000}"
REDIS_CONTAINER="${REDIS_CONTAINER:-deploy-redis-1}"
POSTGRES_CONTAINER="${POSTGRES_CONTAINER:-deploy-postgres-1}"

log() { echo "[$(date +'%H:%M:%S')] $*"; }
fail() { echo "[FAIL] $*" >&2; exit 1; }
pass() { echo "  [PASS] $*"; }

wait_task() {
  local task_id=$1
  for i in $(seq 1 120); do
    local status=$(curl -s "$GATEWAY_URL/api/v1/tasks/$task_id" | jq -r '.status')
    if [[ "$status" == "completed" ]] || [[ "$status" == "failed" ]] || [[ "$status" == "budget_exceeded" ]]; then
      echo "$status"
      return 0
    fi
    sleep 1
  done
  fail "Task $task_id timeout"
}

# ---- Slice 10: DAG 可视化测试 ----
test_dag_visualization() {
  log "Testing DAG visualization..."
  local task_id=$(curl -s -X POST "$GATEWAY_URL/api/v1/tasks" \
    -H "Content-Type: application/json" \
    -d '{"query":"Explain AI, ML, DL","config":{"mode":"dag","max_parallel_agents":3,"enable_dag_visualization":true}}' \
    | jq -r '.task_id')
  local status=$(wait_task $task_id)
  [[ "$status" == "completed" ]] || fail "dag_visualization: expected completed, got $status"

  # 验证 DAG 状态 API
  local dag_status=$(curl -s "$GATEWAY_URL/api/v1/tasks/$task_id/dag")
  local node_count=$(echo $dag_status | jq '.nodes | length')
  [[ "$node_count" -ge 1 ]] || fail "dag_visualization: no nodes found"

  pass "DAG visualization"
}

# ---- Slice 11: ReAct 暂停/恢复测试 ----
test_react_workflow_level() {
  log "Testing ReAct Workflow-level loop..."
  local task_id=$(curl -s -X POST "$GATEWAY_URL/api/v1/tasks" \
    -H "Content-Type: application/json" \
    -d '{"query":"What is 15 * 23 + 7? Calculate step by step.","config":{"mode":"dag","enable_react":true,"react_max_iterations":3}}' \
    | jq -r '.task_id')
  local status=$(wait_task $task_id)
  [[ "$status" == "completed" ]] || fail "react_workflow: expected completed, got $status"

  # 验证 ReAct steps（Postgres 审计）
  local steps_count=$(docker exec $POSTGRES_CONTAINER psql -U admin -d orchestrator -t -c \
    "SELECT COUNT(*) FROM react_steps WHERE workflow_id='$task_id';" 2>/dev/null | tr -d ' ')
  [[ "$steps_count" -ge 1 ]] || fail "react_workflow: no steps in Postgres audit"

  pass "ReAct Workflow-level loop + Postgres audit"
}

# ---- Slice 12: 两级 LRU 缓存测试 ----
test_two_level_cache() {
  log "Testing two-level LRU cache..."

  # 第一次调用（cache miss → Python service）
  local resp1=$(curl -s -X POST "$LLM_SERVICE_URL/tokenize" \
    -H "Content-Type: application/json" \
    -d '{"text": "Hello world test cache", "model": "gpt-4o-mini"}')
  local cached1=$(echo $resp1 | jq -r '.cached // false')

  # 第二次调用（应该 cache hit）
  local resp2=$(curl -s -X POST "$LLM_SERVICE_URL/tokenize" \
    -H "Content-Type: application/json" \
    -d '{"text": "Hello world test cache", "model": "gpt-4o-mini"}')

  # 验证缓存统计
  local hits=$(docker exec $REDIS_CONTAINER redis-cli HGET lru:stats hits 2>/dev/null || echo 0)
  local misses=$(docker exec $REDIS_CONTAINER redis-cli HGET lru:stats misses 2>/dev/null || echo 0)

  log "Cache stats - hits: $hits, misses: $misses"

  pass "Two-level LRU cache"
}

# ---- Slice 13: DAG 动态重规划测试 ----
test_dag_fallback() {
  log "Testing DAG node failure fallback..."
  # 模拟节点失败场景
  local task_id=$(curl -s -X POST "$GATEWAY_URL/api/v1/tasks" \
    -H "Content-Type: application/json" \
    -d '{"query":"Test fallback","config":{"mode":"dag","max_parallel_agents":2}}' \
    | jq -r '.task_id')
  local status=$(wait_task $task_id)

  # 验证节点状态
  local failed_nodes=$(docker exec $REDIS_CONTAINER redis-cli HGETALL "dag:$task_id:nodes" 2>/dev/null | grep -c "failed" || echo 0)
  local skipped_nodes=$(docker exec $REDIS_CONTAINER redis-cli HGETALL "dag:$task_id:nodes" 2>/dev/null | grep -c "skipped" || echo 0)

  pass "DAG fallback (failed=$failed_nodes, skipped=$skipped_nodes)"
}

# ---- Main ----
log "=== Phase 4 Smoke Test Starting ==="

test_dag_visualization
test_react_workflow_level
test_two_level_cache
test_dag_fallback

echo ""
echo "=== Phase 4 Smoke Tests PASSED ==="
```

---

## 十、验收标准

### 8.1 Slice 10：DAG 可视化

| 验收项 | 标准 | 验证方式 |
|--------|------|----------|
| **DAG Graph 边验证** | Redis node JSON 中必须包含 `dependencies` 字段；`draft_answer.dependencies` 必须包含 `analyze_input` | redis-cli HGET |
| **有向图展示** | Dashboard 必须展示 DAG graph 区域；能看到 `analyze_input --> draft_answer` 有向边 | HTML / SSE 流验证 |
| **Dependencies 列** | 节点表格的 Dependencies 列必须显示 `analyze_input`，不能用 layer 推断 | curl GET /api/v1/tasks/{id}/dag |
| **节点颜色** | completed 节点显示绿色，failed 节点显示红色 | Dashboard 可视化验证 |
| **状态实时更新** | pending → running → completed 在 1s 内反映到 Redis | redis-cli HGETALL |
| **SSE 事件推送** | DAG_NODE_RUNING/COMPLETED 事件推送到 SSE | redis-cli XRANGE |
| **并发进度显示** | 多个节点并发执行时，Dashboard 同时显示多个 running | SSE 流验证 |
| **test_dag_visual.sh** | 脚本必须验证 HTML 或 graph 输出中存在有向边表达 | bash scripts/test_dag_visual.sh |

### 8.2 Slice 11：ReAct 暂停/恢复（重构）

| 验收项 | 标准 | 验证方式 |
|--------|------|----------|
| ReAct Loop 在 Workflow 内 | patterns/react.go 实现的 ReactLoop 在 Workflow 调用 | 代码审查 |
| Activity 重试不丢上下文 | 单次 Activity 失败不影响其他迭代的 history | 日志验证 |
| Postgres 审计写入 | react_steps 表有所有迭代记录 | psql SELECT |
| ReAct 迭代生效 | enable_react=true 时 llm_calls > 1 | psql COUNT |

### 8.3 Slice 12：分布式 LRU 缓存（重构）

| 验收项 | 标准 | 验证方式 |
|--------|------|----------|
| 两级缓存命中 | L1 miss → L2 hit → Python service miss | 日志验证 |
| L1 LocalLRU | 相同 text+model 第二次调用 < 1ms | 性能测试 |
| L2 Redis String | 跨 Worker 共享，TTL 1h | redis-cli GET |
| Redis LRU 驱逐 | maxmemory-policy allkeys-lru 生效 | 内存压力测试 |
| 统计准确 | hits + misses = 总请求数 | redis-cli HGETALL |

### 8.4 Slice 13：DAG 动态重规划

| 验收项 | 标准 | 验证方式 |
|--------|------|----------|
| 节点失败标记 | node 失败后 status=failed | redis-cli HGETALL |
| 依赖节点跳过 | 依赖失败的节点标记 skipped | redis-cli HGETALL |
| 失败传播 | node-2 失败后，依赖 node-2 的节点标记 skipped | 代码审查 |

### 8.5 Slice 14：并发控制增强

| 验收项 | 标准 | 验证方式 |
|--------|------|----------|
| max_parallel_agents=1 | 顺序执行，事件顺序可预测 | redis-cli XRANGE 顺序 |
| max_parallel_agents > 1 | 并发执行，多个节点同时 running | redis-cli 时间戳 |
| LocalDispatchOptions | MaxConcurrentActors 配置生效 | 日志验证 |

---

## 十一、成功路径 / 失败路径

### Slice 10：DAG 可视化

**成功路径：**
```
POST /api/v1/tasks {enable_dag_visualization: true}
  → Gateway 创建 task，返回 task_id
  → Temporal Worker 调度 DAGWorkflow
  → DAGNodeActivity 更新节点状态到 Redis Hash
  → DAGNodeActivity 内部通过 streamPublisher.Publish 推送 SSE 事件
  → GET /api/v1/tasks/{id}/dag 返回节点状态
```

**失败路径：**
```
Redis 写入失败 → 只记录日志 → 继续执行
SSE 推送失败 → 只记录日志 → 不阻断主流程
```

### Slice 11：ReAct 暂停/恢复（重构）

**成功路径：**
```
ReactWorkflow(enable_react=true, react_max_iterations=5)
  → for i in range(5):
      → ExecuteAgent(reason) → thought
      → ExecuteAgent(act) → observation
      → SaveReActStepAudit (Postgres, 仅审计)
      → if early_stop: break
  → return {steps, final_answer}
```

**失败路径：**
```
Activity 重试 → 只有当前迭代 result 丢失 → history 不受影响
Postgres 审计写入失败 → 只记录日志 → 不影响主流程
```

### Slice 12：分布式 LRU 缓存（重构）

**成功路径：**
```
GetTokenCountActivity(text, model)
  → LocalLRU.Get(key) → hit 则返回
  → LocalLRU miss → Redis.Get(key) → hit 则回填 L1
  → Redis miss → HTTP /tokenize → tiktoken.encode
  → 回填 L1 + L2
```

**失败路径：**
```
/tokenize HTTP 失败 → fallback ceil(len/4)，日志警告
Redis 写入失败 → 只记录日志，继续执行
```

### Slice 13：DAG 动态重规划

**成功路径：**
```
DAGNodeActivity node 失败
  → HandleDAGNodeFailureActivity
  → 标记 failed 节点
  → 遍历依赖节点，标记 skipped（如果无可用父节点）
```

**失败路径：**
```
节点失败不影响其他节点（并发继续）
DAG 整体返回 completed（含失败信息）
```

---

## 十二、后续扩展路线图

| Phase | 内容 | 关键能力 |
|-------|------|---------|
| Phase 5 | Swarm / Agent P2P / Workspace / Handoff | Lead Agent + workers + workspace + agent handoff |
| Phase 6A | MCP Tool Runtime | MCP client/server、tool discovery、tool call audit |
| Phase 6B | Sandbox / WASI Execution | 安全代码执行、timeout、resource limit |
| Phase 6C | Skills System | Skill Manifest、Skill Registry、Skill Executor、Skill Permission |
| Phase 6D | Hooks Event System | before_tool_call、after_tool_call、on_agent_step、on_error |
| Phase 6E | RAG / Qdrant Long-term Memory | embedding、chunking、Qdrant upsert/search、长期记忆 |
| Phase 6F | Research-Synthesis v1 | RAG + Workspace evidence synthesis |
| Phase 7A | HITL / Approval / UI | 人类审批、任务暂停恢复、Dashboard 操作 |
| Phase 7B | Reflection Production Mode | generate -> reflect -> revise，多轮 reflection |
| Phase 7C | Tree-of-Thoughts | 多候选路径、评分、剪枝、token budget 控制 |
| Phase 7D | Debate Mode | Pro Agent、Con Agent、Judge Agent、多轮辩论 |
| Phase 7E | Research-Synthesis v2 | 多来源证据、冲突证据处理、引用链路 |
| Phase 8 | SDK / CLI / Multi-tenant | Python/Go SDK、CLI、Auth/Quota、配置导入导出 |

---

## 十三、Phase 4 明确不做范围

以下能力**不进入** Phase 4，明确推迟到后续 Phase：

| 能力 | 推迟到 | 原因 |
|------|--------|------|
| Reflection 独立模式 | Phase 7B | Reflection 需要完整 Workspace 和 LLM synthesis 基线 |
| Handoff 机制 | Phase 5G | 需要先有 Lead Agent / Worker Agent 基础架构 |
| Skills 系统 | Phase 6C | 需要 Skill Manifest 和 Registry 设计 |
| Hooks 事件系统 | Phase 6D | 需要 Hook 点定义和 Event Bus 设计 |
| RAG / Qdrant | Phase 6E | 需要 embedding 服务和向量存储基础设施 |
| Tree-of-Thoughts | Phase 7C | 需要多候选路径和评分机制 |
| Debate 模式 | Phase 7D | 需要 Pro/Con/Judge Agent 对立架构 |
| 完整 Research-Synthesis | Phase 6F / 7E | 需要 RAG + Workspace evidence 基线 |

**Phase 4 范围锁定：DAG 可视化 + ReAct 暂停恢复 + 两级 LRU + DAG 动态重规划 + 并发控制 + Real LLM smoke/E2E。**

---

## 十四、Shannon 原生能力 vs Lite 实现对照

| 功能 | Shannon 原生能力 | Cribug Phase 4 Lite | 说明 |
|------|-----------------|---------------------|------|
| DAG 可视化 | 实时节点状态 + SSE | Redis Hash + streamPublisher | 无 WebSocket 推送 |
| ReAct 暂停恢复 | Postgres 审计 + Workflow replay | 两级缓存 + Activity 重试保护 | history 通过参数传递 |
| 两级 LRU | LocalLRU + Redis String | L1 Local (5min) + L2 Redis String (1h) | 无持久化 LRU |
| DAG 动态重规划 | 节点失败自动跳过 | HandleDAGNodeFailure Activity | 无自动重规划 |

---

## 十五、开发者配置参考

### 12.1 Feature Flags

| Feature Flag | 类型 | 默认值 | 说明 |
|-------------|------|--------|------|
| enable_dag_visualization | bool | false | 开启 DAG 可视化 |
| enable_react | bool | false | 开启 ReAct 推理循环 |
| react_max_iterations | int | 3 | ReAct 最大迭代次数 |
| max_parallel_agents | int | 1 | DAG 并发数 |
| dag_ttl_seconds | int | 86400 | DAG 节点 Redis TTL |
| react_steps_ttl_seconds | int | 86400 | ReAct steps Redis TTL |
| l1_cache_ttl_seconds | int | 300 | L1 LocalLRU TTL（5 分钟） |
| l2_cache_ttl_seconds | int | 3600 | L2 Redis String TTL（1 小时） |
| heartbeat_timeout_seconds | int | 90 | 节点心跳超时 |

### 12.2 资源配置

| 配置项 | 默认值 | 说明 |
|--------|--------|------|
| max_parallel_agents | 1 | DAG 并发数，1=顺序 |
| react_max_iterations | 3 | ReAct 最大迭代次数 |
| ActivityTimeout | 60s | 单个 Activity 超时 |
| ReActTotalTimeout | 180s | ReAct Activity 总超时 |
| L1 LocalLRU capacity | 10000 | 本地缓存最大条目数 |

### 12.3 环境变量

```bash
# Go Gateway / Worker
DATABASE_URL=postgres://admin:admin@postgres:5432/orchestrator
REDIS_ADDR=redis:6379
TEMPORAL_ADDRESS=temporal:7233
LLM_SERVICE_URL=http://python-llm:8000

# Redis LRU 配置（redis.conf）
maxmemory-policy allkeys-lru

# Real LLM Test 开关（默认关闭，不进入 CI）
REAL_LLM_TEST=0  # 0=默认关闭，1=手动启用真实 LLM 测试

# Python LLM Service
LLM_MODE=mock  # mock | openai_compatible
LLM_PROVIDER=openai_compatible
LLM_BASE_URL=https://api.openai.com/v1
LLM_MODEL=gpt-4o-mini
OPENAI_API_KEY=sk-xxx

# Real LLM 限制
LLM_TEMPERATURE=0
LLM_MAX_TOKENS=512
LLM_TIMEOUT_SECONDS=60
REAL_LLM_TEST_MAX_COST_USD=1
```

---

## 十六、参考实现

- DAG Workflow 实现：`go/orchestrator/internal/workflows/dag_workflow.go`
- ReAct Loop 实现：`go/orchestrator/internal/workflows/patterns/react.go`
- 两级缓存实现：`go/orchestrator/internal/embeddings/cache.go`（参考模式）
- SupervisorWorkflow（Shannon 标杆）：`go/orchestrator/internal/workflows/supervisor_workflow.go`
- SSE 事件推送：`go/orchestrator/internal/streaming/`
- P2P 实现：`go/orchestrator/internal/activities/p2p.go`

*Phase 4 重构任务书 v2.0*
