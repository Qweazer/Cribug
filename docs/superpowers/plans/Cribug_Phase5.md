# Cribug Phase 5 重构任务书 — Swarm Architecture + Agent P2P + Workspace

## 版本说明

本文档为 Phase 5 **重构版**（v2.0），基于 Shannon 原生架构范式修复了以下核心缺陷：

- **5A/5B 修复**：彻底消灭 Workflow 内 Sleep 轮询，采用 Selector + Timer 响应式等待（Shannon supervisor_workflow.go 模式）
- **5E 修复**：彻底删除 Slice 14（Git-like 冲突合并），Workspace 纯 Append-only + LLM 聚合消解
- **关键约束**：Workspace 是 Append-only 事件流，不做版本控制或文件锁

---

## 一、项目定位

### 1.1 当前已完成基线

| Phase | 内容 | 状态 |
|-------|------|------|
| Phase 1 | Gateway + Temporal Worker + SimpleWorkflow + Postgres + Redis | 完成后 |
| Phase 2 | AgentActivity 全链路 + Session Memory + Budget + SSE | 完成后 |
| Phase 3D Slice 7 | DAG Concurrency（LocalDispatchOptions 并发） | 完成后 |
| Phase 3D Slice 8 | ReAct Reasoning Loop（Workflow 级别） | 完成后 |
| Phase 3D Slice 9 | Real tiktoken Tokenizer（两级缓存） | 完成后 |
| Phase 4 Slice 10 | DAG 可视化 | 完成后 |
| Phase 4 Slice 11 | ReAct 暂停/恢复（Workflow 级别 Loop） | 完成后 |
| Phase 4 Slice 12 | 两级 LRU 缓存 | 完成后 |

### 1.2 Phase 5 重构目标

| Slice | 功能 | 描述 | 真实 LLM 引入 |
|-------|------|------|---------------|
| Phase 5A Slice 10 | **Lead Agent / SwarmWorkflow（重构）** | Selector + Timer 响应式等待，彻底消灭 Workflow 内 Sleep 轮询 | 必须已有可选 E2E |
| Phase 5B Slice 11 | Agent P2P Communication | `SendAgentMessage`/`FetchAgentMessages`（Shannon p2p.go 模式） | 可选 |
| Phase 5C Slice 12 | Workspace + Real LLM Synthesis | `WorkspaceAppend`/`WorkspaceList`（Shannon p2p.go 纯追加模式）；**必须新增真实 LLM synthesis 测试** | 必须有真实 LLM synthesis |
| Phase 5D Slice 13 | State Synchronization | SignalChannel 驱动（Shannon supervisor_workflow.go 模式） | 可选 |
| **Phase 5E Slice 14** | **Conflict Resolution（删除）** | **彻底删除 Slice 14**，Workspace 不做冲突合并 | N/A |
| Phase 5F Slice 15 | Security & Access Control | Agent 身份验证 + 权限控制 | 可选 |
| Phase 5G Slice 16 | **Agent Handoff Mechanism** | Worker 显式转交任务给另一个 Worker，由 Lead 编排 | 可选 |

---

## 二、职责边界（强制约束 - Shannon 范式）

### 2.1 组件职责约束

| 组件 | 职责 | 禁止 |
|------|------|------|
| Gateway | 任务创建、返回 task_id/workflow_id/run_id、SSE 事件流推送 | 直接调用 LLM、做 Agent 推理、执行业务逻辑 |
| Workflow | 确定性编排逻辑、调用 Activities | 直接 HTTP/DB/Redis、创建 goroutine、使用 `time.Now()`、使用随机数 |
| Activity | 所有外部 IO：同步/异步 HTTP/DB/Redis 调用、goroutine | 业务编排逻辑（只做外部 IO） |
| Python LLM Service | 模型调用、tiktoken/tokenize | 任务编排、状态管理、Workflow 调度 |
| Lead Agent | 负责任务分解、Worker 调度、结果聚合 | 直接执行任务、访问共享文件、自己管理并发 |
| Worker Agent | 负责任务执行、P2P 消息传递 | 任务分配、其他 Worker 管理、创建子任务 |

### 2.2 Swarm 架构约束（新增）

**Lead Agent 约束：**
- Lead Agent 在 `SwarmWorkflow`（Workflow）中实现
- Lead Agent 通过 `workflow.ExecuteActivity` 并发调用多个 `WorkerAgentActivity`
- Lead Agent 使用 **Selector + Timer 响应式等待**（Shannon supervisor 模式）
- **禁止：Lead Agent 自己管理 goroutine/线程池**，这应由 Temporal 调度

**Worker Agent 约束：**
- Worker Agent 在 `WorkerAgentActivity` Activity 中实现
- Worker Agent 通过 `SendAgentMessage`/`FetchAgentMessages` 进行 P2P 消息传递
- Worker Agent 可读写 Workspace（通过 `WorkspaceAppend`/`WorkspaceList`）
- Worker Agent 独立 Activity，超时重试由 Temporal 处理

**Workspace 约束（关键重构）：**
- ⚠️ **Workspace 是 Append-only 事件流**，不是文件系统
- 所有数据存储在 Redis List（`wf:{workflow_id}:ws:{topic}`）
- **不做版本控制、乐观锁、文件锁**
- 语义层冲突消解交给最后的 `SynthesizeResults` LLM 聚合层

**Handoff 约束（Phase 5G）：**
- Handoff 是任务控制权转移，不等于普通 P2P 消息
- Worker Agent 可以请求 Handoff，但不能自己调度新的 Worker
- Lead Agent / SwarmWorkflow 负责：验证 Handoff 请求、调度 target worker、记录 Handoff 状态
- Handoff 必须写入 Workspace append-only event（`wf:{workflow_id}:ws:handoff`）
- Handoff 必须通过 SSE / Stream 暴露给 Gateway / Dashboard
- Activity 内禁止调用 `workflow.ExecuteActivity`
- **Phase 5G 只实现显式 handoff，不做自动 Agent 选举，不做复杂自治路由**

---

## 三、阶段范围

### Phase 5A Slice 10：Lead Agent / SwarmWorkflow（重构）

#### 3.1 核心架构澄清（关键）

⚠️ **原草案的错误设计**：
```go
// 原草案的问题代码
for {
    ExecuteActivity("WorkspaceList");
    workflow.Sleep(2s);  // ← Workflow History 爆炸！
}
```
- Workflow 内的 `for { Activity; workflow.Sleep }` 循环会在 Temporal History 中记录每个 Sleep 事件
- 高负载下导致 History 呈指数级爆炸，直接冲爆 Worker 内存

✅ **重构后的正确设计（Shannon supervisor_workflow.go 模式）**：
```go
// 正确的 Selector + Timer 响应式等待
sel := workflow.NewSelector(ctx)
sel.AddReceive(ch, func(c workflow.ReceiveChannel, more bool) {})
timer := workflow.NewTimer(ctx, backoff)
sel.AddFuture(timer, func(f workflow.Future) {})
sel.Select(ctx)
```
- Selector + Timer 避免了 Workflow 内 Sleep 轮询和反复 Activity 拉取 Redis
- Temporal Timer 和 Activity 调用仍会进入 History，但不能声称"零 History"
- 等待逻辑通过 `workflow.NewSelector` + `workflow.NewTimer` 实现
- Shannon 在 `supervisor_workflow.go` lines 675-757 已实现此模式

#### 3.2 SwarmWorkflow 实现（Shannon 标杆模式）

```go
// SwarmWorkflow - Swarm 架构 Workflow
// 参考 Shannon supervisor_workflow.go 实现
// 关键：采用 Selector + Timer 响应式等待，彻底消灭 Workflow 内 Sleep 轮询
func SwarmWorkflow(ctx workflow.Context, input SwarmTaskInput) (TaskResult, error) {
    logger := workflow.GetLogger(ctx)
    workflowID := workflow.GetInfo(ctx).WorkflowExecution.ID

    // ============================================================
    // 1. 初始化 Lead Agent 状态（通过 Activity）
    // ============================================================
    emitCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
        StartToCloseTimeout: 30 * time.Second,
        RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
    })
    _ = workflow.ExecuteActivity(emitCtx, "EmitTaskUpdate", EmitTaskUpdateInput{
        WorkflowID: workflowID,
        EventType:  StreamEventWorkflowStarted,
        AgentID:    "lead",
        Message:    "Lead Agent started",
        Timestamp:  workflow.Now(ctx),
    }).Get(ctx, nil)

    // ============================================================
    // 2. 任务分解（调用 DecomposeTaskActivity）
    // ============================================================
    var decomp DecompositionResult
    if err := workflow.ExecuteActivity(ctx, "DecomposeTask",
        DecomposeTaskInput{Query: input.Query, Context: input.Context},
    ).Get(ctx, &decomp); err != nil {
        return TaskResult{Success: false, ErrorMessage: err.Error()}, err
    }

    // ============================================================
    // 3. 配置 Activity Options（并发控制）
    // ============================================================
    activityOpts := workflow.ActivityOptions{
        StartToCloseTimeout: time.Duration(input.TaskTimeout) * time.Second,
        RetryPolicy: &temporal.RetryPolicy{
            MaximumAttempts: 3,
        },
        // 并发控制通过 LocalDispatchOptions
        WithLocalDispatchOptions: &temporal.LocalDispatchOptions{
            MaxConcurrentActors: input.WorkerPoolSize,
        },
    }
    ctx = workflow.WithActivityOptions(ctx, activityOpts)

    // ============================================================
    // 4. 初始化 P2P 协调所需的 topic channels
    // 参考 Shannon supervisor_workflow.go lines 99-117
    // ============================================================
    topicChans := make(map[string]workflow.Channel)
    for _, topic := range getAllProducedTopics(decomp.Subtasks) {
        topicChans[topic] = workflow.NewChannel(ctx)
    }

    // ============================================================
    // 5. 并发调度 Worker Activities
    // 使用 workflow.ExecuteActivity 并发调用多个 WorkerAgentActivity
    // Temporal 的 LocalDispatchOptions 处理并发限制
    // ============================================================
    var childFutures []workflow.Future
    for i, subtask := range decomp.Subtasks {
        agentName := fmt.Sprintf("worker-%s-%d", workflowID[:8], i)

        // 注入 role 信息（Shannon 模式）
        childCtx := map[string]interface{}{"role": subtask.Role}
        if subtask.Consumes != nil && len(subtask.Consumes) > 0 {
            childCtx["previous_results"] = getPreviousResults(ctx, decomp.Subtasks[:i])
            childCtx["consumes"] = subtask.Consumes
        }

        // 调用 WorkerAgentActivity（通过 Temporal 调度并发执行）
        future := workflow.ExecuteActivity(ctx, "ExecuteWorkerAgent",
            WorkerAgentInput{
                TaskID:              input.TaskID,
                SubTaskID:           subtask.ID,
                AgentID:             agentName,
                Prompt:              subtask.Description,
                Context:             childCtx,
                EnableReAct:         input.ReActEnabled,
                MaxReActIterations:  input.MaxReActIterations,
            },
        )
        childFutures = append(childFutures, future)
    }

    // ============================================================
    // 6. 收集子任务结果（使用 Selector 响应式等待）
    // 关键：彻底消灭 Workflow 内 Sleep 轮询
    // 参考 Shannon supervisor_workflow.go lines 675-757
    //
    // ⚠️ 并发 future 完成顺序不等于 subtask 顺序，必须用固定索引写入
    // ============================================================
    childResults := make([]AgentExecutionResult, len(childFutures))
    completedCount := 0
    failedCount := 0
    timedOut := false

    // 初始化 selector
    sel := workflow.NewSelector(ctx)

    // 添加每个 child future 的 receive
    for i, future := range childFutures {
        idx := i
        subtaskID := decomp.Subtasks[idx].ID
        sel.AddFuture(future, func(f workflow.Future) {
            var result WorkerAgentResult
            if err := f.Get(ctx, &result); err != nil {
                logger.Error("Worker task failed", "subtask_id", subtaskID, "error", err)
                childResults[idx] = AgentExecutionResult{
                    AgentID: fmt.Sprintf("worker-%s-%d", workflowID[:8], idx),
                    Success: false,
                    Error:   err.Error(),
                }
                failedCount++
            } else {
                childResults[idx] = AgentExecutionResult{
                    AgentID:    result.AgentID,
                    Response:   result.Output,
                    TokensUsed: result.TokensUsed,
                    Success:    result.Status == "completed",
                }
                completedCount++
            }
        })
    }

    // 添加超时 timer
    timeoutTimer := workflow.NewTimer(ctx, time.Duration(input.TaskTimeout)*time.Second)
    sel.AddFuture(timeoutTimer, func(f workflow.Future) {
        logger.Warn("SwarmWorkflow timeout", "elapsed", input.TaskTimeout)
        timedOut = true
    })

    // 响应式等待（timeout 后立即退出）
    for completedCount+failedCount < len(childFutures) && !timedOut {
        sel.Select(ctx)
    }

    // timeout 后返回 PartialResult，不无限等待未完成 worker
    // ⚠️ PartialResult 语义说明：
    // - PartialResult 不等于完全失败，已完成 worker 的结果应保留并可展示
    // - Gateway / result 展示层应能读取 partial metadata（completed、failed、timeout）
    // - 可选状态建议为 partial_completed，而非直接标记为 failed
    // - timeout 后不要丢弃已完成 worker 的结果（childResults 中保留）
    if timedOut {
        return TaskResult{
            Success:          false,
            ErrorMessage:     "SwarmWorkflow timeout",
            AgentResults:     childResults,  // 已完成 worker 的结果保留
            CompletedWorkers: completedCount,
            FailedWorkers:    failedCount,
            PartialResult: map[string]interface{}{
                "partial":       true,
                "completed":     completedCount,
                "failed":        failedCount,
                "timeout":       true,
            },
        }, nil
    }

    // ============================================================
    // 7. P2P：Produce 到 Workspace（供后续 Consumer 使用）
    // Workspace 是 Append-only，不做版本控制或锁
    // ============================================================
    for i, subtask := range decomp.Subtasks {
        if subtask.Produces != nil && len(subtask.Produces) > 0 {
            result := childResults[i]
            for _, topic := range subtask.Produces {
                _ = workflow.ExecuteActivity(ctx, "WorkspaceAppend",
                    WorkspaceAppendInput{
                        WorkflowID: workflowID,
                        Topic:      topic,
                        Entry: map[string]interface{}{
                            "subtask_id": subtask.ID,
                            "summary":    result.Response,
                        },
                        Timestamp: workflow.Now(ctx),
                    },
                )
            }
        }
    }

    // ============================================================
    // 8. 从 Workspace 读取 entries（供后续 Synthesis 使用）
    // Phase 5C 主路径：必须通过 WorkspaceList 读取，而不是直接使用 childResults
    // childResults 作为辅助 metadata，主要数据来自 Workspace append-only history
    // ============================================================
    allEntries := []WorkspaceEntry{}
    topicsProduced := map[string]bool{}
    for _, subtask := range decomp.Subtasks {
        for _, topic := range subtask.Produces {
            topicsProduced[topic] = true
        }
    }
    for topic := range topicsProduced {
        var wsResult WorkspaceListResult
        if err := workflow.ExecuteActivity(ctx, "WorkspaceList",
            WorkspaceListInput{WorkflowID: workflowID, Topic: topic, SinceSeq: 0, Limit: 200},
        ).Get(ctx, &wsResult); err == nil {
            allEntries = append(allEntries, wsResult.Entries...)
        }
    }

    // ============================================================
    // 9. 结果聚合（调用 SynthesizeResultsActivity）
    // ⚠️ 主路径从 WorkspaceList 读取 entries，不依赖 childResults
    // ============================================================
    var synth SynthesisResult
    if err := workflow.ExecuteActivity(ctx, "SynthesizeResults",
        SynthesisInput{Query: input.Query, WorkspaceEntries: allEntries, AgentResults: childResults},
    ).Get(ctx, &synth); err != nil {
        return TaskResult{Success: false, ErrorMessage: err.Error()}, err
    }

    // ============================================================
    // 9. 发送完成事件
    // ============================================================
    _ = workflow.ExecuteActivity(emitCtx, "EmitTaskUpdate", EmitTaskUpdateInput{
        WorkflowID: workflowID,
        EventType:  StreamEventWorkflowCompleted,
        AgentID:    "lead",
        Message:    "Lead Agent completed",
        Timestamp:  workflow.Now(ctx),
    }).Get(ctx, nil)

    return TaskResult{
        Result:     synth.FinalResult,
        Success:    true,
        TokensUsed: synth.TokensUsed,
        Metadata: map[string]interface{}{
            "num_workers":   len(childResults),
            "mode":          "swarm",
            "completed":     completedCount,
            "failed":        failedCount,
        },
    }, nil
}
```

#### 3.3 P2P 协调：Selector + Timer 响应式等待

关键：对于需要等待上游数据到达的场景（如 `subtask.Consumes`），采用 Shannon supervisor_workflow.go 的 Selector + SignalChannel 事件驱动模式。

**事件驱动设计（禁止伪轮询）：**

Worker 完成处理后，通过 Signal 通知 SwarmWorkflow：
- Worker 执行 `WorkspaceAppend` → 写入结果
- Worker 调用 `SignalWithStart` 或通过 Activity 发 Signal → 通知 SwarmWorkflow
- SwarmWorkflow 的 Selector 等待 SignalChannel receive → 事件触发后调用 `WorkspaceList` 读取数据

```go
// SwarmWorkflow 内：等待 Worker 响应的事件驱动 Selector
// 参考 Shannon supervisor_workflow.go lines 675-757
sel := workflow.NewSelector(ctx)

// 添加 SignalChannel receive（Worker 完成后发送 Signal）
sigCh := workflow.GetSignalChannel(ctx, "worker_response_"+subtaskID)
sel.AddReceive(sigCh, func(c workflow.ReceiveChannel, more bool) {
    // Signal 到达：Worker 已完成，触发后续 WorkspaceList 读取
})

// 添加超时 timer
timer := workflow.NewTimer(ctx, taskTimeout)
sel.AddFuture(timer, func(f workflow.Future) {
    timedOut = true
})

// 阻塞直到任一条件满足（Signal 到达 或 timeout）
sel.Select(ctx)

if !timedOut {
    // Signal 到达后：通过 WorkspaceList 读取 Worker 输出（不在 Selector 内轮询）
    entries, _ := workspaceListResult.Entries, nil
    // ... 处理 entries
}
```

**⚠️ 禁止在 Selector 循环内调用 Activity 轮询 Redis。**
**⚠️ Temporal Timer 和 Activity 调用仍会进入 History，不能声称"零 History"。**
**⚠️ 正确做法：Signal 触发 → Selector 解锁 → Workflow 发起 Activity 调用。**

**与原草案的关键区别**：

| 方面 | 原草案（错误） | 重构后（Shannon 标杆） |
|------|--------------|---------------------|
| 等待模式 | `for { Activity; workflow.Sleep(2s); }` 或循环内 WorkspaceList | `Selector + SignalChannel` 事件驱动 |
| Activity 调用 | 在循环内反复调用 | Signal 触发后调用一次 |
| History 影响 | 每次 Activity/Sleep 都在 History 中记录 | 仅正常的 Activity 调用入 History |
| 实现位置 | Workflow 内轮询 | Workflow + Signal + Activity 分离 |
| Shannon 参考 | 无 | supervisor_workflow.go lines 675-757 |

---

### Phase 5B Slice 11：Agent P2P Communication

#### 3.4 P2P 消息数据结构（复用 Shannon p2p.go）

```go
// ============================================================
// 复用 Shannon p2p.go 的 SendAgentMessage / FetchAgentMessages
// 消息 Key 模式：wf:{workflow_id}:mbox:{agent_id}:msgs
// ============================================================

type SendAgentMessageInput struct {
    WorkflowID string                 `json:"workflow_id"`
    From       string                 `json:"from"`
    To         string                 `json:"to"`
    Type       MessageType            `json:"type"` // "request" | "offer" | "accept" | "delegation" | "info"
    Payload    map[string]interface{} `json:"payload"`
    Timestamp  time.Time              `json:"timestamp"` // 必须来自 workflow.Now()
}

type SendAgentMessageResult struct {
    Seq uint64 `json:"seq"`
}

// SendAgentMessage - 发送 P2P 消息
// 完全复用 Shannon p2p.go 实现
func (a *Activities) SendAgentMessage(ctx context.Context, in SendAgentMessageInput) (SendAgentMessageResult, error) {
    if in.WorkflowID == "" || in.To == "" || in.From == "" {
        return SendAgentMessageResult{}, fmt.Errorf("invalid message args")
    }

    rc := a.sessionManager.RedisWrapper().GetClient()
    seqKey := fmt.Sprintf("wf:%s:mbox:%s:seq", in.WorkflowID, in.To)
    listKey := fmt.Sprintf("wf:%s:mbox:%s:msgs", in.WorkflowID, in.To)
    seq := rc.Incr(ctx, seqKey).Val()

    // 使用 workflow.Now() 保证确定性 replay
    ts := in.Timestamp
    if ts.IsZero() {
        return SendAgentMessageResult{}, fmt.Errorf("timestamp is required; must be provided by workflow.Now(ctx)")
    }

    msg := map[string]interface{}{
        "seq":     seq,
        "from":    in.From,
        "to":      in.To,
        "type":    string(in.Type),
        "payload": in.Payload,
        "ts":      ts.UnixNano(),
    }
    b, _ := json.Marshal(msg)
    if err := rc.RPush(ctx, listKey, b).Err(); err != nil {
        return SendAgentMessageResult{}, err
    }

    // 设置 TTL（配置驱动）
    cfg := a.config
    rc.Expire(ctx, seqKey, cfg.P2PMessageTTL)
    rc.Expire(ctx, listKey, cfg.P2PMessageTTL)

    return SendAgentMessageResult{Seq: uint64(seq)}, nil
}

type FetchAgentMessagesInput struct {
    WorkflowID string `json:"workflow_id"`
    AgentID    string `json:"agent_id"`
    SinceSeq   uint64 `json:"since_seq"`
    Limit      int64  `json:"limit"`
}

type AgentMessage struct {
    Seq     uint64                 `json:"seq"`
    From    string                 `json:"from"`
    To      string                 `json:"to"`
    Type    MessageType            `json:"type"`
    Payload map[string]interface{} `json:"payload"`
    Ts      int64                  `json:"ts"`
}

// FetchAgentMessages - 获取 P2P 消息
// 完全复用 Shannon p2p.go 实现
func (a *Activities) FetchAgentMessages(ctx context.Context, in FetchAgentMessagesInput) ([]AgentMessage, error) {
    if in.WorkflowID == "" || in.AgentID == "" {
        return nil, fmt.Errorf("invalid args")
    }

    rc := a.sessionManager.RedisWrapper().GetClient()
    listKey := fmt.Sprintf("wf:%s:mbox:%s:msgs", in.WorkflowID, in.AgentID)

    if in.Limit <= 0 {
        in.Limit = 200
    }

    llen := rc.LLen(ctx, listKey).Val()
    start := llen - in.Limit
    if start < 0 {
        start = 0
    }

    vals, err := rc.LRange(ctx, listKey, start, llen).Result()
    if err != nil && err != redis.Nil {
        return nil, err
    }

    out := make([]AgentMessage, 0, len(vals))
    for _, v := range vals {
        var m AgentMessage
        if json.Unmarshal([]byte(v), &m) == nil {
            if m.Seq > in.SinceSeq {
                out = append(out, m)
            }
        }
    }

    return out, nil
}
```

#### 3.5 P2P 协作场景

**场景 1：Worker 请求 Leader 上下文**
```
Worker-A → SendAgentMessage(to="lead", type="request", payload={topic})
Worker-A → FetchAgentMessages(since_seq=last_seq)
Lead → SendAgentMessage(to="worker-a", type="response", payload={context})
```

**场景 2：广播状态更新（通过 Workspace）**
```
Worker-A → WorkspaceAppend(topic="status", entry={worker_id, status})
其他 Worker → WorkspaceList(topic="status", since_seq=X)
```

**场景 3：心跳检测**
```
Worker-A → SendAgentMessage(to="lead", type="heartbeat", payload={worker_id, timestamp})
Lead → FetchAgentMessages(agent_id="lead", since_seq=X) 监控 Worker 健康状态
```

---

### Phase 5C Slice 12：Workspace（Shannon 纯追加模式）

#### 3.6 核心架构澄清（关键）

⚠️ **原草案的错误设计**：
- 在 Workspace 中实现 Git-like 版本控制
- 设计行级冲突合并、文件覆盖、乐观锁版本控制
- Phase 5E (Slice 14) 长篇大论地设计冲突解决

✅ **重构后的正确设计（Shannon p2p.go 纯追加模式）**：
- **Workspace 是 Append-only ordered log**
- 不做版本控制、乐观锁、文件锁
- Redis List 永远保留完整 append-only history
- 不在存储层覆盖旧 Entry；如果需要去重，只在读取/synthesis 层按 seq 选择最新 Entry
- 语义层冲突消解交给最后的 `SynthesizeResults` LLM 聚合层

#### 3.7 Workspace 数据结构（Shannon 纯追加）

```go
// ============================================================
// 复用 Shannon p2p.go 的 WorkspaceAppend / WorkspaceList
// Workspace Key 模式：wf:{workflow_id}:ws:{topic}
// ============================================================

type WorkspaceAppendInput struct {
    WorkflowID string                 `json:"workflow_id"`
    Topic      string                 `json:"topic"`
    Entry      map[string]interface{} `json:"entry"`
    Timestamp  time.Time              `json:"timestamp"` // 必须来自 workflow.Now()
}

type WorkspaceAppendResult struct {
    Seq uint64 `json:"seq"`
}

// WorkspaceAppend - 追加 Entry 到 Topic
// 完全复用 Shannon p2p.go 实现
func (a *Activities) WorkspaceAppend(ctx context.Context, in WorkspaceAppendInput) (WorkspaceAppendResult, error) {
    if in.WorkflowID == "" || in.Topic == "" {
        return WorkspaceAppendResult{}, fmt.Errorf("invalid args")
    }

    rc := a.sessionManager.RedisWrapper().GetClient()
    seqKey := fmt.Sprintf("wf:%s:ws:seq", in.WorkflowID)
    seq := rc.Incr(ctx, seqKey).Val()

    // 使用 workflow.Now() 保证确定性 replay
    ts := in.Timestamp
    if ts.IsZero() {
        return WorkspaceAppendResult{}, fmt.Errorf("timestamp is required; must be provided by workflow.Now(ctx)")
    }

    entry := map[string]interface{}{
        "seq":   seq,
        "topic": in.Topic,
        "entry": in.Entry,
        "ts":    ts.UnixNano(),
    }
    b, _ := json.Marshal(entry)

    listKey := fmt.Sprintf("wf:%s:ws:%s", in.WorkflowID, in.Topic)
    if err := rc.RPush(ctx, listKey, b).Err(); err != nil {
        return WorkspaceAppendResult{}, err
    }

    // 设置 TTL（配置驱动）
    cfg := a.config
    rc.Expire(ctx, seqKey, cfg.WorkspaceTTL)  // seq key TTL 与 workspace list key 一致
    rc.Expire(ctx, listKey, cfg.WorkspaceTTL)

    return WorkspaceAppendResult{Seq: uint64(seq)}, nil
}

type WorkspaceListInput struct {
    WorkflowID string `json:"workflow_id"`
    Topic      string `json:"topic"`
    SinceSeq   uint64 `json:"since_seq"`
    Limit      int64  `json:"limit"`
}

type WorkspaceEntry struct {
    Seq   uint64                 `json:"seq"`
    Topic string                 `json:"topic"`
    Entry map[string]interface{} `json:"entry"`
    Ts    int64                  `json:"ts"`
}

// WorkspaceList - 获取 Topic 的 Entries
// 完全复用 Shannon p2p.go 实现
func (a *Activities) WorkspaceList(ctx context.Context, in WorkspaceListInput) ([]WorkspaceEntry, error) {
    if in.WorkflowID == "" || in.Topic == "" {
        return nil, fmt.Errorf("invalid args")
    }

    rc := a.sessionManager.RedisWrapper().GetClient()
    listKey := fmt.Sprintf("wf:%s:ws:%s", in.WorkflowID, in.Topic)

    if in.Limit <= 0 {
        in.Limit = 200
    }

    llen := rc.LLen(ctx, listKey).Val()
    start := llen - in.Limit
    if start < 0 {
        start = 0
    }

    vals, err := rc.LRange(ctx, listKey, start, llen).Result()
    if err != nil && err != redis.Nil {
        return nil, err
    }

    out := make([]WorkspaceEntry, 0, len(vals))
    for _, v := range vals {
        var e WorkspaceEntry
        if json.Unmarshal([]byte(v), &e) == nil {
            if e.Seq > in.SinceSeq {
                out = append(out, e)
            }
        }
    }

    return out, nil
}
```

#### 3.8 Workspace 并发控制

**唯一约束：Append-only ordered log**
- 每个 Entry 有全局递增的 `seq`
- Redis List 永远保留完整 append-only history，**不在存储层覆盖旧 Entry**
- 不做文件锁、乐观锁、版本比较
- 如果需要去重，只允许在读取层 / synthesis 层按 `seq` 选择最新 Entry
- 语义冲突交给 `SynthesizeResults` LLM 聚合层

**语义冲突消解：交给 LLM 聚合层**
```
多个 Worker 并发写入 topic="summary"
→ Workspace 存储所有 Entry（按 seq 顺序）
→ SynthesizeResultsActivity 获取所有 Entry
→ LLM 智能判断哪个更相关，进行语义聚合
```

---

### Phase 5D Slice 13：State Synchronization

#### 3.9 SignalChannel 驱动（复用 Shannon supervisor_workflow.go）

参考 `supervisor_workflow.go` lines 675-757：

```go
// ⚠️ Workflow 内禁止使用原生 go func
// 正确做法：直接使用 workflow.GetSignalChannel + selector.AddReceive
sigCh := workflow.GetSignalChannel(ctx, "mailbox_v1")

sel := workflow.NewSelector(ctx)
sel.AddReceive(sigCh, func(c workflow.ReceiveChannel, more bool) {
    var msg MailboxMessage
    c.Receive(ctx, &msg)
    // 处理消息
})

// 添加超时 timer
timer := workflow.NewTimer(ctx, taskTimeout)
sel.AddFuture(timer, func(f workflow.Future) {
    timedOut = true
})

sel.Select(ctx)
```

**⚠️ 禁止在 Workflow 内使用 `workflow.Go` 或原生 `go func`。**

---

### Phase 5E Slice 14：删除 Conflict Resolution

#### 3.10 彻底删除 Slice 14

**原草案 Slice 14 的错误设计**：
- Git-like 行级冲突合并
- 文件覆盖与版本控制
- 乐观锁版本控制

**重构后：完全删除**
- Workspace 是 Append-only 事件流
- 不做版本控制、冲突合并、文件锁
- 语义冲突消解交给 `SynthesizeResults` LLM 聚合层

---

### Phase 5F Slice 15：Security & Access Control

#### 3.11 Agent 身份验证

```go
// AgentAuthInput - Agent 身份验证
type AgentAuthInput struct {
    WorkflowID string `json:"workflow_id"`
    AgentID    string `json:"agent_id"`
    AgentRole  string `json:"agent_role"` // "lead" | "worker"
    Timestamp  time.Time `json:"timestamp"`
}

// AuthorizeTeamAction - 策略门禁
func (a *Activities) AuthorizeTeamAction(ctx context.Context, in TeamActionInput) (TeamActionDecision, error) {
    // Lead Agent 可以：分解任务、分发任务、聚合结果
    // Worker Agent 可以：执行任务、P2P 通信、读写自己 topic 的 Workspace
    // Worker Agent 禁止：管理其他 Worker、修改 Lead 决策
}
```

---

### Phase 5G Slice 16：Agent Handoff Mechanism

#### 3.12 目标与非目标

**目标：**
- Worker Agent 显式请求将任务转交给另一个 Worker
- Lead Agent / SwarmWorkflow 编排 Handoff 流程
- Handoff 事件写入 Workspace append-only log
- Handoff 状态通过 SSE / Stream 暴露

**非目标：**
- ❌ 自动 Agent 选举（Leader Election）
- ❌ 复杂自治路由（Worker 自己调度 Worker）
- ❌ Git-like conflict resolution
- ❌ Handoff 链深度 > max_handoff_chain_depth

#### 3.13 Handoff 数据结构

```go
// HandoffRequest - Worker 请求转交任务
type HandoffRequest struct {
    WorkflowID      string                 `json:"workflow_id"`
    TaskID          string                 `json:"task_id"`
    SubTaskID       string                 `json:"subtask_id"`
    SourceAgentID   string                 `json:"source_agent_id"`
    TargetAgentID   string                 `json:"target_agent_id"`
    Reason          string                 `json:"reason"`
    RequiredSkill   string                 `json:"required_skill,omitempty"`
    ContextSnapshot map[string]interface{} `json:"context_snapshot"`
    PartialResult   map[string]interface{} `json:"partial_result,omitempty"`
    Timestamp       time.Time              `json:"timestamp"` // 必须来自 workflow.Now()
}

// HandoffStatus - Handoff 状态枚举
type HandoffStatus string

const (
    HandoffRequested HandoffStatus = "requested"
    HandoffAccepted  HandoffStatus = "accepted"
    HandoffRejected  HandoffStatus = "rejected"
    HandoffCompleted HandoffStatus = "completed"
    HandoffFailed    HandoffStatus = "failed"
    HandoffTimedOut  HandoffStatus = "timed_out"
)

// HandoffEvent - Workspace 中记录的 Handoff 事件
type HandoffEvent struct {
    Seq           uint64        `json:"seq"`
    HandoffID    string        `json:"handoff_id"`
    Status        HandoffStatus `json:"status"`
    Request       HandoffRequest `json:"request"`
    Timestamp     int64         `json:"timestamp"`
    Metadata      map[string]interface{} `json:"metadata,omitempty"`
}
```

#### 3.14 Handoff 推荐流程

```
1. WorkerAgentActivity 执行任务
2. Worker A 判断需要 handoff
3. WorkerAgentActivity 返回 HandoffRequested 结果
4. SwarmWorkflow / Lead Agent 接收 handoff request
5. Lead Agent 校验 target agent 权限
6. Lead Agent 写入 Workspace handoff event（wf:{workflow_id}:ws:handoff）
7. Lead Agent 通过 P2P 给 Worker B 发送 handoff message
   （wf:{workflow_id}:mbox:{worker_b}:msgs）
8. Lead Agent 调度 Worker B 继续执行
9. Worker B 读取 context_snapshot / partial_result
10. Worker B 产出结果并写入 Workspace
11. SynthesizeResults 最终聚合所有结果
```

**关键约束：**
- Activity 内不能调用 `workflow.ExecuteActivity`
- Handoff 必须通过 Workflow 调用 Activity（Lead Agent 编排）
- Handoff event 写入 Workspace append-only log，不做版本控制

#### 3.15 Handoff 验收标准

| 验收项 | 标准 | 验证方式 |
|--------|------|----------|
| HandoffRequest 结构 | 必须包含 source/target/context_snapshot/partial_result | 代码审查 |
| Workspace handoff log | wf:{workflow_id}:ws:handoff 存在 HandoffEvent | redis-cli LRANGE |
| Lead 编排 | Lead 校验权限、调度 Worker B | 日志验证 |
| SSE 事件 | handoff_requested / handoff_completed 事件推送 | SSE 流验证 |
| 无权限拒绝 | target 无权限时返回 rejected | mock 测试 |
| Handoff timeout | 超时返回 PartialResult | timeout 测试 |
| max_handoff_chain_depth | 链深度超过限制时拒绝新 handoff | 配置测试 |

#### 3.16 Handoff 失败路径

```
Handoff timeout → HandoffTimedOut → PartialResult → 不阻断全局
Target agent 无权限 → HandoffRejected → Lead 调度其他 agent
Handoff chain depth 超过限制 → 拒绝新 handoff 请求
Redis 写入失败 → Activity 重试 → 不丢 handoff event
```

---

## 四、API 设计 + Redis 数据结构

### 4.1 P2P Activity 接口

```go
// SendAgentMessageInput - 发送 P2P 消息
type SendAgentMessageInput struct {
    WorkflowID string                 `json:"workflow_id"`
    From       string                 `json:"from"`
    To         string                 `json:"to"`
    Type       MessageType            `json:"type"` // "request" | "offer" | "accept" | "delegation" | "info"
    Payload    map[string]interface{} `json:"payload"`
    Timestamp  time.Time              `json:"timestamp"` // 必须来自 workflow.Now()
}

// FetchAgentMessagesInput - 接收 P2P 消息
type FetchAgentMessagesInput struct {
    WorkflowID string `json:"workflow_id"`
    AgentID    string `json:"agent_id"`
    AfterSeq   uint64 `json:"after_seq"`
}
```

### 4.2 Workspace Activity 接口

```go
// WorkspaceAppendInput - 追加 Entry 到 Topic
type WorkspaceAppendInput struct {
    WorkflowID string                 `json:"workflow_id"`
    Topic      string                 `json:"topic"`
    Entry      map[string]interface{} `json:"entry"`
    Timestamp  time.Time              `json:"timestamp"` // 必须来自 workflow.Now()
}

// WorkspaceListInput - 获取 Topic 的 Entries
type WorkspaceListInput struct {
    WorkflowID string `json:"workflow_id"`
    Topic      string `json:"topic"`
    SinceSeq   uint64 `json:"since_seq"`
    Limit      int64  `json:"limit"`
}
```

### 4.3 SwarmWorkflow 接口

```go
// SwarmWorkflow - Swarm 架构 Workflow
func SwarmWorkflow(ctx workflow.Context, input SwarmTaskInput) (TaskResult, error)

// SwarmTaskInput
type SwarmTaskInput struct {
    TaskID            string
    Query             string
    Context           map[string]interface{}
    ReActEnabled      bool
    MaxReActIterations int
    WorkerPoolSize    int
    TaskTimeout       int // seconds
}
```

### 4.4 Redis 数据结构

```
# P2P 邮箱（Shannon 风格）
wf:{workflow_id}:mbox:{agent_id}:seq    → String (消息序列号)
wf:{workflow_id}:mbox:{agent_id}:msgs   → List (AgentMessage JSON)

# Workspace（Shannon 风格，Append-only）
wf:{workflow_id}:ws:seq                 → String (全局序列号，INCR)
wf:{workflow_id}:ws:{topic}            → List (WorkspaceEntry JSON)

# Lead Agent 状态
swarm:{task_id}:lead                   → Hash
swarm:{task_id}:workers                → Hash
swarm:{task_id}:tasks                  → Hash
swarm:{task_id}:results                → Hash

# DAG 节点状态
dag:{workflow_id}:meta                  → Hash
dag:{workflow_id}:nodes                → Hash（NodeState JSON）

# LRU 缓存
lru:tiktoken:{model}:{text_hash}       → String (token count)
lru:stats                              → Hash (hits/misses)
```

---

## 五、状态与错误

### 5.1 SwarmWorkflow 状态枚举

| 状态 | 说明 |
|------|------|
| `swarm_init` | SwarmWorkflow 初始化 |
| `lead_started` | Lead Agent 已启动 |
| `tasks_decomposed` | 任务已分解 |
| `workers_registered` | Worker 已注册 |
| `in_progress` | 任务执行中 |
| `partial_completed` | 部分完成（timeout 后） |
| `completed` | 全部完成 |
| `failed` | 失败 |

### 5.2 Error Taxonomy

| Error Type | 来源 | 处理方式 |
|------------|------|----------|
| `swarm_timeout` | Worker 超时未响应 | 返回 PartialResult，保留已完成 worker 结果 |
| `lead_degraded` | Lead 检测到 worker 失败 | 标记 degraded，继续其他任务 |
| `workspace_append_failed` | Redis 写入失败 | Activity 重试，不丢数据 |
| `p2p_message_failed` | P2P 消息发送失败 | Activity 重试 |
| `synthesis_failed` | LLM 聚合失败 | 返回部分结果 + error |
| `signal_timeout` | Signal 超时 | 执行默认逻辑 |

### 5.3 PartialResult metadata

```json
{
  "partial": true,
  "completed": 3,
  "failed": 1,
  "timeout": true,
  "completed_worker_ids": ["worker-0", "worker-1", "worker-2"],
  "final_result": "..."
}
```

**Gateway 处理 PartialResult：**
- 读取 `partial: true` 字段
- 展示已完成 worker 的结果
- 不阻塞 UI，显示 degraded 状态

---

## 六、修改文件清单

### Phase 5A Slice 10：Lead Agent / SwarmWorkflow（重构）

| 文件 | 操作 | 说明 |
|------|------|------|
| `go/orchestrator/internal/workflows/swarm_workflow.go` | 新增 | SwarmWorkflow + Selector + SignalChannel |
| `go/orchestrator/internal/activities/swarm.go` | 新增 | LeadAgentActivity, WorkerAgentActivity |
| `go/orchestrator/internal/workflows/control/handler.go` | 复用 | SignalChannel 模式 |

### Phase 5B Slice 11：Agent P2P Communication

| 文件 | 操作 | 说明 |
|------|------|------|
| `go/orchestrator/internal/activities/p2p.go` | 复用 | SendAgentMessage/FetchAgentMessages |
| `go/orchestrator/internal/workflows/supervisor_workflow.go` | 参考 | Shannon 标杆 |

### Phase 5C Slice 12：Workspace

| 文件 | 操作 | 说明 |
|------|------|------|
| `go/orchestrator/internal/activities/p2p.go` | 修改 | WorkspaceAppend/WorkspaceList（Append-only） |
| `go/orchestrator/internal/workflows/swarm_workflow.go` | 修改 | SynthesizeResultsActivity 调用 |

### Phase 5D Slice 13：State Synchronization

| 文件 | 操作 | 说明 |
|------|------|------|
| `go/orchestrator/internal/workflows/control/handler.go` | 复用 | SignalChannel 驱动 |

### Phase 5F Slice 15：Security & Access Control

| 文件 | 操作 | 说明 |
|------|------|------|
| `go/orchestrator/internal/activities/auth.go` | 新增 | AgentAuthActivity |

---

## 七、测试脚本

### 7.1 SwarmWorkflow Smoke Test

```bash
#!/bin/bash
# test_swarm_smoke.sh

export REAL_LLM_TEST=${REAL_LLM_TEST:-0}
export WORKFLOW_TIMEOUT=60

# Test 1: SwarmWorkflow 初始化（mock LLM）
echo "=== Test 1: SwarmWorkflow 初始化 ==="
curl -X POST http://localhost:8080/api/v1/tasks \
  -H "Content-Type: application/json" \
  -d '{"query": "test", "config": {"enable_swarm": true}}'
# 验证返回 task_id

# Test 2: P2P 消息发送/接收（mock）
echo "=== Test 2: P2P 消息 ==="
# 验证 Redis 中存在 wf:{workflow_id}:mbox:{agent_id}:msgs

# Test 3: Workspace Append-only
echo "=== Test 3: Workspace Append ==="
# 验证 Redis List 长度递增，global_seq 递增

# Test 4: SignalChannel 响应
echo "=== Test 4: SignalChannel ==="
# 验证 Selector + Timer 响应式等待

# Test 5: Real LLM E2E（可选）
if [ "$REAL_LLM_TEST" = "1" ]; then
  echo "=== Test 5: Real LLM E2E ==="
  # 验证 llm_calls > 0, token usage > 0
  # 验证 final result 非空
  # 验证 Workspace 中存在 worker 输出
fi

echo "=== All Swarm Smoke Tests Passed ==="
```

### 7.2 Workspace Synthesis Test

```bash
#!/bin/bash
# test_workspace_synthesis.sh

export REAL_LLM_TEST=${REAL_LLM_TEST:-0}

# Test 1: Workspace Append-only
echo "=== Test 1: Workspace Append ==="
# Worker 写入多个 entries
# 验证 global_seq 递增

# Test 2: WorkspaceList 按 seq 读取
echo "=== Test 2: WorkspaceList ==="
# 验证返回 entries 按 seq 排序

# Test 3: Real LLM Synthesis（可选）
if [ "$REAL_LLM_TEST" = "1" ]; then
  echo "=== Test 3: Real LLM Synthesis ==="
  # 验证 llm_calls 中存在 synthesis 调用
  # 验证 final result 非空
  # 验证 final result 覆盖多个 worker 核心信息
fi

echo "=== All Workspace Synthesis Tests Passed ==="
```

---

## 八、验收标准

| Slice | 验收项 | 通过条件 |
|-------|--------|----------|
| Slice 10 Lead Agent | Selector + Timer 响应式等待 | Workflow 内无 Sleep 轮询；SignalChannel 驱动 |
| Slice 11 P2P | SendAgentMessage/FetchAgentMessages | Redis List 记录；P2PMessageTTL 生效 |
| Slice 12 Workspace | Append-only + seq 递增 + LLM Synthesis | 无版本控制；WorkspaceList 读取；Real LLM synthesis 可选 |
| Slice 13 State Sync | SignalChannel 驱动 | 复用 supervisor_workflow.go 模式 |
| **Slice 14 Conflict** | **删除** | **无 Git-like 合并代码** |
| Slice 15 Security | Agent 身份验证 | Lead/Worker 权限分离 |

**Real LLM 验收（可选，REAL_LLM_TEST=1）：**

Phase 5A Real LLM Swarm Smoke：
- SwarmWorkflow completed 或 partial completed
- worker_count > 0, completed_workers > 0
- llm_calls > 0, token usage > 0
- Workspace 中存在 worker 输出
- SynthesizeResults 输出非空
- SSE/Stream 中有 lead / worker 事件

Phase 5C Real LLM Workspace Synthesis：
- WorkspaceAppend 写入多个 entries
- WorkspaceList 按 seq 读取 entries
- SynthesizeResults 调用真实 LLM
- llm_calls 中存在 synthesis 调用
- token usage > 0, final result 非空
- final result 覆盖多个 worker 核心信息

---

---

## 九、明确禁止事项

- ❌ **Workflow 内 Sleep 轮询**（必须用 Selector + Timer 响应式）
- ❌ **Workspace 版本控制**（Append-only，不做冲突合并）
- ❌ **Worker Agent 管理其他 Worker**
- ❌ **Worker Agent 修改 Lead 决策**
- ❌ **进程内单实例缓存**
- ❌ **在 Workflow 内使用 `time.Now()`**

---

## 真实 LLM 测试引入策略

### 引入原则

Phase 5 以 Phase 4 真实 LLM E2E smoke 为基础，继续扩展到 Swarm / Lead Agent / Workspace synthesis 场景。

真实 LLM 测试只用于验证端到端模型链路，Workspace 语义冲突消解依赖 LLM 聚合能力。

### 引入时机

| 阶段 | 是否使用真实 LLM | 原因 |
|------|------------------|------|
| Phase 5A Slice 10 Lead Agent / SwarmWorkflow | 必须已有可选 E2E | Lead Agent / Worker Agent / P2P 已涉及真实模型语义能力，必须在进入 Phase 5A 之前证明真实 LLM 链路可选跑通 |
| Phase 5B Slice 11 Agent P2P | 可选 | 主要验证消息通道和 Redis 结构，真实 LLM 仅用于手动 smoke |
| Phase 5C Slice 12 Workspace | **必须有真实 LLM synthesis** | Workspace 纯追加不做冲突合并，语义层冲突消解交给 SynthesizeResults LLM 聚合层，必须用真实 LLM 测试多 Worker 写入后 LLM 能否聚合出有效结论 |
| Phase 5D Slice 13 State Sync | 可选 | 主要验证 SignalChannel 驱动，真实 LLM 不是必需 |
| Phase 5G Slice 16 Handoff | 可选 | 主要验证 handoff 控制流、权限校验、Workspace event、SSE；真实 LLM 只用于手动 smoke，默认 mock 即可 |
| Phase 5F Slice 15 Security | 可选 | 主要验证权限控制，真实 LLM 仅用于手动 smoke |

### 真实 LLM 测试分层

#### 1. Mock Regression Test

- **默认执行**
- 不需要 API key
- 用于 CI / 本地快速回归
- 断言工程结构、Redis、P2P 消息、Workspace append、SignalChannel

#### 2. Real LLM Smoke Test

- **手动开启**
- 需要 `REAL_LLM_TEST=1`
- 需要 `OPENAI_API_KEY` 或兼容 Provider API key
- 用于验证真实 LLM 调用链路
- **不进入默认 CI**

#### 3. Real LLM Synthesis Test（Phase 5C 新增）

- **Phase 5C 验收项**
- 验证多个 Worker 写入 Workspace 后，SynthesizeResults 能否聚合出有效结论
- 断言：聚合结果结构、语义完整性、Workspace seq 递增
- 不断言完整自然语言文本

### 真实 LLM 测试默认行为

| 场景 | 行为 |
|------|------|
| `REAL_LLM_TEST=0`（默认） | 只跑 mock 测试，不请求真实 LLM |
| `REAL_LLM_TEST=1` + 无 API key | skip 真实 LLM 测试，不 fail |
| `REAL_LLM_TEST=1` + 有 API key | 执行真实 LLM smoke + synthesis test |
| API key 缺失 | `t.Skip("OPENAI_API_KEY not set")`，不 fail |

### Phase 5C Workspace Synthesis 验收条件

Workspace 完成后，真实 LLM synthesis 测试必须可选跑通：

1. **多个 Worker 并发写入**：Worker1 / Worker2 / Worker3 同时向 Workspace 写入不同 partial results
2. **Sequential Append 验证**：每次写入 global_seq 递增，**Redis List 记录完整 append-only history**
3. **SynthesizeResults LLM 聚合**：调用 LLM 聚合 Workspace 内容，验证能否生成有效聚合结论
4. **语义冲突消解验证**：如果 Worker 写入的内容存在语义冲突（部分矛盾），LLM 聚合层能否消解

### 真实 LLM 测试验收条件

1. **Phase 5A Slice 10 验收前**：Lead Agent / SwarmWorkflow 真实 LLM 链路必须可选跑通
   - 不阻塞 release，但作为 Phase 5 入口必须记录结果
   - 如果 API key 缺失，标记为 skip 并记录

   **Phase 5A Real LLM Swarm Smoke 必须断言：**
   - SwarmWorkflow completed 或 partial completed
   - worker_count > 0
   - completed_workers > 0
   - llm_calls > 0
   - token usage > 0
   - Workspace 中存在 worker 输出
   - SynthesizeResults 输出非空
   - SSE / Stream 中有 lead / worker 事件

2. **Phase 5C Slice 12 验收时**：真实 LLM synthesis test 必须可选跑通
   - 验证 Workspace append + LLM synthesis 全链路
   - 如果 API key 缺失，标记为 skip 并记录

   **Phase 5C Real LLM Workspace Synthesis 必须断言：**
   - WorkspaceAppend 写入多个 entries
   - WorkspaceList 按 seq 读取 entries（Redis List）
   - SynthesizeResults 调用真实 LLM
   - llm_calls 中存在 synthesis 调用
   - token usage > 0
   - final result 非空
   - final result 覆盖多个 worker 的核心信息
   - 不断言完整自然语言文本（只断言结构、状态、llm_calls）

---

---

## 十一、完整目录结构

Phase 5 完整 16 章结构：

| 章节 | 内容 |
|------|------|
| 一 | 项目定位 |
| 二 | 职责边界（含 Handoff 约束） |
| 三 | 阶段范围（5A-5G Slices） |
| 四 | API 设计 + Redis 数据结构 |
| 五 | 状态与错误 |
| 六 | 修改文件清单 |
| 七 | 测试脚本（含 Handoff 测试） |
| 八 | 验收标准（含 Real LLM E2E 验收） |
| 九 | 明确禁止事项 |
| 十 | 真实 LLM 测试引入策略 |
| 十一 | 完整目录结构 |
| 十二 | 成功路径 / 失败路径（含 Handoff 流程） |
| 十三 | 后续扩展路线图 |
| 十四 | Shannon 原生能力 vs Lite 实现对照 |
| 十五 | 开发者配置参考（含 Handoff 配置） |
| 十六 | 参考实现 |

*Phase 5 重构任务书 v2.0*

---

## 十二、成功路径 / 失败路径

### Slice 10：Lead Agent / SwarmWorkflow（重构）

**成功路径：**
```
SwarmWorkflow(agents=[lead, w1, w2])
  → lead 等待 SwarmMembersUpdated Signal
  → w1/w2 注册就绪
  → lead.Select(SwarmMembersUpdated) → 开始调度
  → for each task:
      → lead.SendAgentMessage(target=w1, task)
      → w1.FetchAgentMessages → 接收任务
      → w1 执行 → WorkspaceAppend 结果
      → lead.Select(AgentResponse) → 收集结果
  → return aggregated_results
```

**失败路径：**
```
Worker 超时未响应 → lead 标记 worker 为 degraded → 继续其他任务
Selector 超时 → 返回 PartialResult → 不阻塞全局
```

### Slice 11：Agent P2P Communication

**成功路径：**
```
Agent A 发送消息：
  → SendAgentMessage(A, B, payload)
  → Redis RPUSH wf:{workflow_id}:mbox:{B}:msgs [AgentMessage JSON with seq, from, payload, ts]
  → return {seq}

Agent B 接收消息：
  → FetchAgentMessages(B, after_seq)
  → Redis LRANGE wf:{workflow_id}:mbox:{B}:msgs 0 -1
  → 过滤 seq > after_seq
  → 返回 [messages with seq > after_seq]
```

**失败路径：**
```
Redis 写入失败 → Activity 重试 → 不丢消息
消息队列空 → 返回空数组 → 不阻塞
```

### Slice 12：Workspace（Shannon 纯追加）

**成功路径：**
```
Worker 写入：
  → WorkspaceAppend(workspace_id, content, metadata)
  → global_seq++（原子操作，INCR）
  → Redis RPUSH wf:{workflow_id}:ws:{topic} [WorkspaceEntry JSON with seq, content, metadata, ts]
  → return {seq}

Lead 读取：
  → WorkspaceList(workspace_id, after_seq)
  → Redis LRANGE wf:{workflow_id}:ws:{topic} 0 -1
  → 过滤 seq > after_seq
  → LLM 聚合 → return aggregated_result
```

**失败路径：**
```
Redis 写入失败 → Activity 重试 → 不丢数据
LLM 聚合超时 → 返回原始列表 → 不阻塞
```

### Slice 13：State Synchronization

**成功路径：**
```
SwarmWorkflow 等待状态：
  → selector.Select(SwarmMembersUpdated) → 阻塞直到成员就绪
  → selector.Select(TaskUpdated) → 阻塞直到任务变化
  → selector.Select(AgentResponse) → 阻塞直到响应

SignalChannel 驱动：
  → 每个 Signal 有唯一 channel name
  → Signal → channel.Send → Selector 收到 → 处理
```

**失败路径：**
```
Signal 超时 → 可配置超时时间 → 超时后执行默认逻辑
Signal channel 未注册 → 返回 error → 不阻塞
```

---

## 十三、后续扩展路线图

| Phase | 内容 | 关键能力 |
|-------|------|---------|
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

## 十四、Shannon 原生能力 vs Lite 实现对照

| 功能 | Shannon 原生能力 | Cribug Phase 5 Lite | 说明 |
|------|-----------------|---------------------|------|
| Lead Agent | Supervisor 模式 + Selector | Lead Agent + P2P | 无复杂选举 |
| Agent P2P | SendAgentMessage/FetchAgentMessages | Redis List 邮箱 | 复用 Shannon 模式 |
| Workspace | Append-only + LLM 聚合 | Redis List + seq | 无 Git-like 合并，无 Redis Hash |
| Agent Handoff | 显式 Handoff + Lead 编排 | Lead 编排 + Workspace append-only event | 无自动选举 |
| State Sync | SignalChannel + Selector | SignalChannel 驱动 | 复用 supervisor_workflow.go |
| 并发控制 | LocalDispatchOptions | LocalDispatchOptions | 一致 |

---

## 十五、开发者配置参考

### 15.1 Feature Flags

| Feature Flag | 类型 | 默认值 | 说明 |
|-------------|------|--------|------|
| enable_swarm | bool | false | 开启 Swarm 模式 |
| max_swarm_agents | int | 10 | 最大 Agent 数量 |
| workspace_ttl_hours | int | 24 | Workspace 数据保留时间（也是 seq key TTL） |
| p2p_message_ttl_seconds | int | 86400 | P2P 消息过期时间（24h） |
| enable_handoff | bool | false | 开启 Agent Handoff 机制 |
| max_handoff_chain_depth | int | 2 | Handoff 链最大深度 |
| handoff_timeout_seconds | int | 120 | Handoff 超时时间 |

### 15.2 Redis Keys

| Key Pattern | 类型 | TTL | 说明 |
|-------------|------|-----|------|
| `wf:{workflow_id}:mbox:{agent_id}:seq` | String | cfg.P2PMessageTTL | P2P 消息序列号 |
| `wf:{workflow_id}:mbox:{agent_id}:msgs` | Redis List | cfg.P2PMessageTTL | Agent P2P 邮箱 |
| `wf:{workflow_id}:ws:seq` | String | cfg.WorkspaceTTL | Workspace 全局序号 |
| `wf:{workflow_id}:ws:{topic}` | Redis List | cfg.WorkspaceTTL | Workspace 数据（append-only） |
| `wf:{workflow_id}:ws:handoff` | Redis List | cfg.WorkspaceTTL | Handoff append-only event log |
| `swarm:state:{id}` | String | 1h | Swarm 状态 |
| `agent:status:{id}` | String | 10min | Agent 心跳 |
| `wf:{workflow_id}:handoff:{handoff_id}:status` | String | 1h | Handoff 状态（可选） |

### 15.3 日志级别

| Level | 场景 |
|-------|------|
| DEBUG | P2P 消息发送/接收 |
| INFO | Agent 注册/注销、Workflow 状态转换 |
| WARN | Redis 超时、Signal 超时 |
| ERROR | Agent 失败、P2P 错误 |

### 15.4 环境变量

```bash
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

# Workspace
WORKSPACE_TTL_HOURS=24
```

---

## 十六、参考实现

### 16.1 核心文件

| 文件 | 用途 | 关键模式 |
|------|------|----------|
| `go/orchestrator/internal/workflows/supervisor_workflow.go` | Shannon Supervisor | Selector + Timer + SignalChannel |
| `go/orchestrator/internal/activities/p2p.go` | P2P 实现 | SendAgentMessage/FetchAgentMessages |
| `go/orchestrator/internal/workflows/control/handler.go` | SignalChannel | SignalHandler/SignalChannel |
| `go/orchestrator/internal/embeddings/cache.go` | 两级缓存 | LocalLRU + Redis Hash |

### 16.2 模式速查

**Selector + Timer 响应式等待：**
```go
selector := workflow.NewSelector(ctx)
timer := workflow.NewTimer(ctx, delay)
selector.AddFuture(timer, func(e workflow.NakedFunc) {
    // 定时任务逻辑
})
timerFuture := workflow.NewTimer(ctx, maxWait)
selector.AddFuture(timerFuture, func(e workflow.NakedFunc) {
    // 超时默认逻辑
})
selector.Select(ctx) // 阻塞直到任一条件满足
```

**SignalChannel 驱动：**
```go
ch := handler.GetSignalChannel("SwarmMembersUpdated")
selector := workflow.NewSelector(ctx)
selector.AddReceive(ch, func(e workflow.ReceiveInput) {
    // Signal 到达，处理成员更新
})
selector.Select(ctx)
```

**P2P 消息发送：**
```go
err := activities.SendAgentMessage(ctx, &p2p.SendAgentMessageInput{
    FromAgent: fromAgent,
    ToAgent:   toAgent,
    Payload:   payload,
})
```

**P2P 消息接收：**
```go
result, err := activities.FetchAgentMessages(ctx, &p2p.FetchAgentMessagesInput{
    AgentID:    agentID,
    AfterSeq:   afterSeq,
})
```

*Phase 5 重构任务书 v2.0 完整版*
