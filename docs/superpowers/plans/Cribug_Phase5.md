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

| Slice | 功能 | 描述 |
|-------|------|------|
| Phase 5A Slice 10 | **Lead Agent / SwarmWorkflow（重构）** | Selector + Timer 响应式等待，彻底消灭 Workflow 内 Sleep 轮询 |
| Phase 5B Slice 11 | Agent P2P Communication | `SendAgentMessage`/`FetchAgentMessages`（Shannon p2p.go 模式） |
| Phase 5C Slice 12 | Workspace | `WorkspaceAppend`/`WorkspaceList`（Shannon p2p.go 纯追加模式） |
| Phase 5D Slice 13 | State Synchronization | SignalChannel 驱动（Shannon supervisor_workflow.go 模式） |
| **Phase 5E Slice 14** | **Conflict Resolution（删除）** | **彻底删除 Slice 14**，Workspace 不做冲突合并 |
| Phase 5F Slice 15 | Security & Access Control | Agent 身份验证 + 权限控制 |

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
- Workflow 保持**绝对静止**（Zero-History 睡眠）
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
    // ============================================================
    var childResults []AgentExecutionResult
    completedCount := 0
    failedCount := 0

    // 初始化 selector
    sel := workflow.NewSelector(ctx)

    // 添加每个 child future 的 receive
    for i, future := range childFutures {
        subtaskID := decomp.Subtasks[i].ID
        sel.AddFuture(future, func(f workflow.Future) {
            var result WorkerAgentResult
            if err := f.Get(ctx, &result); err != nil {
                logger.Error("Worker task failed", "subtask_id", subtaskID, "error", err)
                childResults = append(childResults, AgentExecutionResult{
                    AgentID: fmt.Sprintf("worker-%s-%d", workflowID[:8], i),
                    Success: false,
                    Error:   err.Error(),
                })
                failedCount++
            } else {
                childResults = append(childResults, AgentExecutionResult{
                    AgentID:    result.AgentID,
                    Response:   result.Output,
                    TokensUsed: result.TokensUsed,
                    Success:    result.Status == "completed",
                })
                completedCount++
            }
        })
    }

    // 添加超时 timer
    timeoutTimer := workflow.NewTimer(ctx, time.Duration(input.TaskTimeout)*time.Second)
    sel.AddFuture(timeoutTimer, func(f workflow.Future) {
        logger.Warn("SwarmWorkflow timeout", "elapsed", input.TaskTimeout)
    })

    // 响应式等待
    for completedCount+failedCount < len(childFutures) {
        sel.Select(ctx)
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
    // 8. 结果聚合（调用 SynthesizeResultsActivity）
    // ============================================================
    var synth SynthesisResult
    if err := workflow.ExecuteActivity(ctx, "SynthesizeResults",
        SynthesisInput{Query: input.Query, AgentResults: childResults},
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

关键：对于需要等待上游数据到达的场景（如 `subtask.Consumes`），采用 Shannon supervisor_workflow.go 的 Selector + Timer 模式：

```go
// waitForTopic - 响应式等待 P2P Topic 数据
// 参考 Shannon supervisor_workflow.go lines 675-757
// 关键：彻底消灭 Workflow 内 Sleep 轮询
func waitForTopic(ctx workflow.Context, workflowID, topic string, timeout time.Duration) error {
    // 初始化 topic channel（如果尚未初始化）
    topicChans := getTopicChans() // 从 context 或 closure 获取
    ch, ok := topicChans[topic]
    if !ok {
        ch = workflow.NewChannel(ctx)
        topicChans[topic] = ch
    }

    // 指数退避 timer
    backoff := 1 * time.Second
    maxBackoff := 30 * time.Second
    startTime := workflow.Now(ctx)

    for workflow.Now(ctx).Sub(startTime) < timeout {
        // ============================================================
        // 关键：Selector + Timer 响应式等待
        // Workflow 保持绝对静止，不在 History 中留下 Sleep 记录
        // ============================================================
        sel := workflow.NewSelector(ctx)
        sel.AddReceive(ch, func(c workflow.ReceiveChannel, more bool) {
            // 数据到达，退出等待
        })
        timer := workflow.NewTimer(ctx, backoff)
        sel.AddFuture(timer, func(f workflow.Future) {
            // Timer 到期，增加 backoff 继续等待
            backoff = backoff * 2
            if backoff > maxBackoff {
                backoff = maxBackoff
            }
        })
        sel.Select(ctx)

        // 检查是否已有数据
        var entries []WorkspaceEntry
        if err := workflow.ExecuteActivity(ctx, "WorkspaceList",
            WorkspaceListInput{WorkflowID: workflowID, Topic: topic, SinceSeq: 0, Limit: 1},
        ).Get(ctx, &entries); err == nil && len(entries) > 0 {
            return nil // 数据已到达
        }

        // 超时判定
        if workflow.Now(ctx).Sub(startTime) >= timeout {
            return fmt.Errorf("timeout waiting for topic: %s", topic)
        }
    }

    return fmt.Errorf("timeout waiting for topic: %s", topic)
}
```

**与原草案的关键区别**：

| 方面 | 原草案（错误） | 重构后（Shannon 标杆） |
|------|--------------|---------------------|
| 等待模式 | `for { Activity; workflow.Sleep(2s); }` | `Selector + Timer` 响应式 |
| History 影响 | 每次 Sleep 都在 History 中记录 → 指数爆炸 | Workflow 静止，零额外 History |
| 实现位置 | Workflow 内循环 | Workflow + Activity 分离 |
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
        ts = time.Now()
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

    // 设置 TTL
    rc.Expire(ctx, seqKey, 48*time.Hour)
    rc.Expire(ctx, listKey, 48*time.Hour)

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
- **Workspace 是 Append-only 事件流**
- 不做版本控制、乐观锁、文件锁
- 并发控制简化为：**后写胜出（Last-Writer-Wins）**
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
        ts = time.Now()
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

    // 设置 TTL
    rc.Expire(ctx, seqKey, 48*time.Hour)
    rc.Expire(ctx, listKey, 48*time.Hour)

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

**唯一约束：Append-only，Last-Writer-Wins**
- 每个 Entry 有一个全局递增的 `seq`
- `seq` 大的 Entry 覆盖 `seq` 小的 Entry（如果需要去重）
- 不做文件锁、乐观锁、版本比较

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

参考 `supervisor_workflow.go` lines 99-117：

```go
// SignalChannel 接收来自外部的信号
sig := workflow.GetSignalChannel(ctx, "mailbox_v1")
msgChan := workflow.NewChannel(ctx)

// goroutine 转发 signal 到 channel
workflow.Go(ctx, func(ctx workflow.Context) {
    for {
        var msg MailboxMessage
        sig.Receive(ctx, &msg)
        msgChan.Send(ctx, msg)
    }
})

// Workflow 通过 selector 响应
sel := workflow.NewSelector(ctx)
sel.AddReceive(msgChan, func(c workflow.ReceiveChannel, more bool) {
    var msg MailboxMessage
    c.Receive(ctx, &msg)
    // 处理消息
})
sel.Select(ctx)
```

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

## 四、Redis 数据结构总结

```
# P2P 邮箱（Shannon 风格）
wf:{workflow_id}:mbox:{agent_id}:seq    → String (消息序列号)
wf:{workflow_id}:mbox:{agent_id}:msgs   → List (AgentMessage JSON)

# Workspace（Shannon 风格，Append-only）
wf:{workflow_id}:ws:seq                 → String (全局序列号)
wf:{workflow_id}:ws:{topic}            → List (WorkspaceEntry JSON)

# Lead Agent 状态
swarm:{task_id}:lead                   → Hash
swarm:{task_id}:workers                → Hash
swarm:{task_id}:tasks                  → Hash
swarm:{task_id}:results                → Hash

# DAG 节点状态
dag:{workflow_id}:meta                  → Hash
dag:{workflow_id}:nodes                → Hash

# LRU 缓存
lru:tiktoken:{model}:{text_hash}       → String (token count)
lru:stats                              → Hash (hits/misses)
```

---

## 五、验收标准总结

| Slice | 验收项 | 通过条件 |
|-------|--------|----------|
| Slice 10 Lead Agent | Selector + Timer 响应式等待 | Workflow 内无 Sleep 轮询，History 不爆炸 |
| Slice 11 P2P | SendAgentMessage/FetchAgentMessages | Shannon p2p.go 模式 |
| Slice 12 Workspace | Append-only + seq 递增 | 无版本控制、无锁 |
| Slice 13 State Sync | SignalChannel 驱动 | 复用 supervisor_workflow.go 模式 |
| **Slice 14 Conflict** | **删除** | **无 Git-like 合并代码** |
| Slice 15 Security | Agent 身份验证 | Lead/Worker 权限分离 |

---

## 六、明确禁止事项

- ❌ **Workflow 内 Sleep 轮询**（必须用 Selector + Timer 响应式）
- ❌ **Workspace 版本控制**（Append-only，不做冲突合并）
- ❌ **Worker Agent 管理其他 Worker**
- ❌ **Worker Agent 修改 Lead 决策**
- ❌ **进程内单实例缓存**
- ❌ **在 Workflow 内使用 `time.Now()`**

---

## 七、参考实现

- SupervisorWorkflow（Shannon 标杆）：`go/orchestrator/internal/workflows/supervisor_workflow.go`
- P2P 实现（Shannon 风格）：`go/orchestrator/internal/activities/p2p.go`
- ReAct Loop（Workflow 级别）：`go/orchestrator/internal/workflows/patterns/react.go`
- 两级 LRU 缓存：`go/orchestrator/internal/embeddings/cache.go`
- SignalChannel 模式：`go/orchestrator/internal/workflows/control/handler.go`

---

## 八、完整目录结构

Phase 5 完整 13 章结构：

| 章节 | 内容 |
|------|------|
| 一 | 项目定位 |
| 二 | 职责边界 |
| 三 | 阶段范围（5A-5F Slices） |
| **四** | **API 设计（新增）** |
| **五** | **状态与错误（新增）** |
| **六** | **修改文件清单（新增）** |
| **七** | **测试脚本（新增）** |
| **八** | **验收标准（新增）** |
| **九** | **成功路径/失败路径（新增）** |
| 十 | 后续扩展路线图 |
| 十一 | Shannon 原生能力 vs Lite 实现对照 |
| 十二 | 开发者配置参考 |
| 十三 | 参考实现 |

*Phase 5 重构任务书 v2.0*

---

## 九、成功路径 / 失败路径

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
  → Redis HSET messages:{B} [{seq, from, payload, ts}]
  → return {seq}

Agent B 接收消息：
  → FetchAgentMessages(B, after_seq)
  → Redis HGETALL messages:{B}
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
  → global_seq++（原子操作）
  → Redis HSET workspace:{id} {global_seq: {content, metadata, ts}}
  → return {seq}

Lead 读取：
  → WorkspaceList(workspace_id, after_seq)
  → Redis HGETALL workspace:{id}
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

## 十、后续扩展路线图

| Phase | 内容 | 关键能力 |
|-------|------|---------|
| Phase 4 | DAG 并发扩展 / ReAct 增强 | DAG 可视化、ReAct 暂停恢复、两级 LRU |
| Phase 6 | RAG / Qdrant / MCP / Sandbox | 向量检索、MCP 协议、WASI code execution |
| Phase 7 | HITL / Approval / UI | Human-in-loop approval、Desktop UI |
| Phase 8 | SDK / CLI / Multi-tenant | Python/Go SDK、CLI tool、Auth/Quota |

---

## 十一、Shannon 原生能力 vs Lite 实现对照

| 功能 | Shannon 原生能力 | Cribug Phase 5 Lite | 说明 |
|------|-----------------|---------------------|------|
| Lead Agent | Supervisor 模式 + Selector | Lead Agent + P2P | 无复杂选举 |
| Agent P2P | SendAgentMessage/FetchAgentMessages | Redis Hash 邮箱 | 复用 Shannon 模式 |
| Workspace | Append-only + LLM 聚合 | Redis Hash + seq | 无 Git-like 合并 |
| State Sync | SignalChannel + Selector | SignalChannel 驱动 | 复用 supervisor_workflow.go |
| 并发控制 | LocalDispatchOptions | LocalDispatchOptions | 一致 |

---

## 十二、开发者配置参考

### 12.1 Feature Flags

| Feature Flag | 类型 | 默认值 | 说明 |
|-------------|------|--------|------|
| enable_swarm | bool | false | 开启 Swarm 模式 |
| max_swarm_agents | int | 10 | 最大 Agent 数量 |
| workspace_ttl_hours | int | 24 | Workspace 数据保留时间 |
| p2p_message_ttl_seconds | int | 300 | P2P 消息过期时间 |

### 12.2 Redis Keys

| Key Pattern | 类型 | TTL | 说明 |
|-------------|------|-----|------|
| `messages:{agent_id}` | Redis Hash | 5min | Agent P2P 邮箱 |
| `workspace:{id}` | Redis Hash | 24h | Workspace 数据 |
| `swarm:state:{id}` | String | 1h | Swarm 状态 |
| `agent:status:{id}` | String | 10min | Agent 心跳 |

### 12.3 日志级别

| Level | 场景 |
|-------|------|
| DEBUG | P2P 消息发送/接收 |
| INFO | Agent 注册/注销、Workflow 状态转换 |
| WARN | Redis 超时、Signal 超时 |
| ERROR | Agent 失败、P2P 错误 |

---

## 十三、参考实现

### 13.1 核心文件

| 文件 | 用途 | 关键模式 |
|------|------|----------|
| `go/orchestrator/internal/workflows/supervisor_workflow.go` | Shannon Supervisor | Selector + Timer + SignalChannel |
| `go/orchestrator/internal/activities/p2p.go` | P2P 实现 | SendAgentMessage/FetchAgentMessages |
| `go/orchestrator/internal/workflows/control/handler.go` | SignalChannel | SignalHandler/SignalChannel |
| `go/orchestrator/internal/embeddings/cache.go` | 两级缓存 | LocalLRU + Redis Hash |

### 13.2 模式速查

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
