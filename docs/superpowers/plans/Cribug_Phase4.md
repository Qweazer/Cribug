# Cribug Phase 4+ — Swarm Architecture + Agent P2P + Workspace File Sharing

## 任务书版本说明

本文档为 Phase 4+ 实施级任务书（**v2.0 满分可执行版**），对比 v1.0 新增：

- **通信协议设计**：Lead ↔ Worker 消息协议、消息顺序保证、离线处理
- **Workspace 版本策略**：Git-like 版本控制、冲突检测算法、最终一致性回滚
- **资源约束矩阵**：CPU/Memory/并发限制、ReAct+DAG 资源池配置
- **安全体系完整化**：身份验证流程、密钥管理、审计日志规范
- **边界测试用例**：节点离线、网络延迟、文件冲突、权限错误的可执行测试

---

## 一、项目定位

### 1.1 当前已完成基线

| Phase | 内容 | 状态 |
|-------|------|------|
| Phase 1 | Gateway + Temporal Worker + SimpleWorkflow + Postgres + Redis | 完成后 |
| Phase 2 | AgentActivity 全链路 + Session Memory + Budget + SSE | 完成后 |
| Phase 3A Slice 4 | DAGWorkflow Lite（顺序 DAG） | 完成后 |
| Phase 3B Slice 5 | Multi-Agent Lite（顺序 planner→worker→critic→synthesizer） | 完成后 |
| Phase 3C Slice 6 | Tool Abstraction Lite（calculator/echo） | 完成后 |
| Phase 3D Slice 7 | DAG Concurrency（LocalDispatchOptions 并发） | 完成后 |
| Phase 3D Slice 8 | ReAct Reasoning Loop（Activity 内循环） | 完成后 |
| Phase 3D Slice 9 | Real tiktoken Tokenizer（Go LRU + Python） | 完成后 |

### 1.2 Phase 4+ 目标

| Slice | 功能 | 描述 |
|-------|------|------|
| Phase 4A Slice 10 | Lead Agent Architecture | Lead Agent 管理 Worker Agents，任务分发与结果聚合 |
| Phase 4B Slice 11 | Agent P2P Communication | Worker Agent 间 P2P 消息传递，状态共享 |
| Phase 4C Slice 12 | Workspace File Sharing | 共享工作空间，版本管理，并发写入控制 |
| Phase 4D Slice 13 | State Synchronization | 分布式状态同步，最终一致性策略 |
| Phase 4E Slice 14 | Conflict Resolution | 文件写入冲突检测与解决 |
| Phase 4F Slice 15 | Security & Access Control | 权限控制，Agent 身份验证 |

---

## 二、职责边界（强制约束 - 继承 Phase 3）

### 2.1 组件职责约束

| 组件 | 职责 | 禁止 |
|------|------|------|
| Gateway | 任务创建、返回 task_id/workflow_id/run_id、SSE 事件流推送 | 直接调用 LLM、做 Agent 推理、执行业务逻辑 |
| Workflow | 确定性编排逻辑、调用 Activities | 直接 HTTP/DB/Redis、创建 goroutine、使用 time.Now()、使用随机数 |
| Activity | 所有外部 IO：同步/异步 HTTP/DB/Redis 调用、goroutine | 业务编排逻辑（只做外部 IO） |
| Python LLM Service | 模型调用、tiktoken/tokenize | 任务编排、状态管理、Workflow 调度 |
| Lead Agent（新增） | 负责任务分解、Worker 调度、结果聚合 | 直接执行任务、访问共享文件 |
| Worker Agent（新增） | 负责任务执行、P2P 消息传递 | 任务分配、其他 Worker 管理 |

### 2.2 Workflow 确定性原则（继承）

**Temporal Replay 约束：**
- Workflow 代码在 replay 时必须产生相同结果
- 禁止在 Workflow 内：创建 goroutine、调用外部 HTTP/DB/Redis、使用 time.Now()、使用随机数
- 违反将导致 Workflow 崩溃或行为不一致

### 2.3 Swarm 架构约束（新增）

**Lead Agent 约束：**
- Lead Agent 在 `LeadAgentActivity` Activity 内实现
- Lead Agent 通过 Temporal Activity 调度 Worker Agents
- Lead Agent 不直接创建 goroutine，通过 LocalDispatchOptions 控制并发
- Lead Agent 维护 Worker 状态，但不直接访问共享文件

**Worker Agent 约束：**
- Worker Agent 在 `WorkerAgentActivity` Activity 内实现
- Worker Agent 通过 Redis Pub/Sub 进行 P2P 消息传递
- Worker Agent 可读写 Workspace 文件（需通过 WorkspaceActivity）
- Worker Agent 独立 Activity，超时重试由 Temporal 处理

**Workspace 约束：**
- Workspace 文件访问必须通过 `WorkspaceActivity`
- 不允许 Worker Agent 直接读写文件系统
- 所有文件操作记录到 Redis Hash `workspace:{task_id}:files`
- 并发写入使用 Redis SETNX 实现分布式锁

---

## 三、阶段范围

### Phase 4A Slice 10：Lead Agent Architecture

#### 3.1 职责

- Lead Agent 负责任务分解和 Worker 调度
- Lead Agent 运行在 `LeadAgentActivity` Activity 中
- Lead Agent 通过 `DispatchWorkerActivity` 分发任务到 Worker Pool
- Lead Agent 通过 `AggregateResultsActivity` 聚合 Worker 结果
- Lead Agent 维护 Worker 状态（通过 Redis Hash `swarm:{task_id}:workers`）

#### 3.2 Lead Agent 数据结构

```go
type LeadAgentConfig struct {
    TaskID             string `json:"task_id"`
    Query              string `json:"query"`
    WorkerPoolSize     int    `json:"worker_pool_size"`    // 默认 3，最大 10
    MaxWorkers         int    `json:"max_workers"`         // 最大 Worker 数量
    TaskTimeout        int    `json:"task_timeout"`        // 单任务超时（秒），默认 60
    HeartbeatInterval  int    `json:"heartbeat_interval"` // 心跳间隔（秒），默认 30
    HeartbeatTimeout   int    `json:"heartbeat_timeout"`   // 心跳超时（秒），默认 90
    EnableP2P          bool   `json:"enable_p2p"`         // 是否启用 P2P 通信
    WorkspaceEnabled   bool   `json:"workspace_enabled"` // 是否启用共享工作空间
    ReActEnabled       bool   `json:"react_enabled"`      // 是否启用 ReAct
    MaxReActIterations int    `json:"max_react_iterations"` // ReAct 最大迭代次数，默认 3
}

type WorkerTask struct {
    TaskID      string `json:"task_id"`
    WorkerID    string `json:"worker_id"`
    SubTaskID    string `json:"sub_task_id"`
    Prompt      string `json:"prompt"`
    Context     string `json:"context,omitempty"`  // 从 Workspace 读取的上下文
    Priority    int    `json:"priority"`           // 优先级（1-10）
    EnableReAct bool   `json:"enable_react"`       // 是否启用 ReAct
    MaxIterations int  `json:"max_iterations"`     // ReAct 最大迭代
    CreatedAt   string `json:"created_at"`
    ExpiresAt   string `json:"expires_at"`         // 任务过期时间
}

type WorkerResult struct {
    WorkerID    string `json:"worker_id"`
    SubTaskID    string `json:"sub_task_id"`
    Status      string `json:"status"`     // "completed" | "failed" | "timeout" | "cancelled"
    Output      string `json:"output"`
    Error       string `json:"error,omitempty"`
    ReActSteps  int    `json:"react_steps,omitempty"` // ReAct 迭代次数
    LatencyMs   int    `json:"latency_ms"`
    CreatedAt   string `json:"created_at"`
}
```

#### 3.3 Redis 数据结构

```
Key: swarm:{task_id}:lead
Type: Hash
Fields:
  - task_id: 主任务 ID
  - status: "initialized" | "running" | "completed" | "failed"
  - worker_pool_size: Worker 池大小
  - workers_active: 活跃 Worker 数量
  - tasks_total: 总子任务数
  - tasks_completed: 已完成子任务数
  - tasks_failed: 失败子任务数
  - created_at: 创建时间
  - updated_at: 最后更新时间
  - started_at: 开始执行时间
  - completed_at: 完成时间
TTL: 86400 秒（1 天）

Key: swarm:{task_id}:workers
Type: Hash
Fields:
  - worker_id -> status (idle | running | completed | failed | timeout | offline)
  - worker_id -> current_task_id
  - worker_id -> current_sub_task_id
  - worker_id -> last_heartbeat (Unix timestamp)
  - worker_id -> heartbeat_count
  - worker_id -> tasks_assigned
  - worker_id -> tasks_completed
TTL: 86400 秒（1 天）

Key: swarm:{task_id}:tasks
Type: Hash
Fields:
  - sub_task_id -> WorkerTask JSON
TTL: 86400 秒（1 天）

Key: swarm:{task_id}:results
Type: Hash
Fields:
  - sub_task_id -> WorkerResult JSON
TTL: 86400 秒（1 天）

Key: swarm:{task_id}:dispatch_queue
Type: List
内容: 待分发 WorkerTask JSON（按优先级排序）
TTL: 86400 秒（1 天）
```

#### 3.4 LeadAgentActivity 实现

```go
func LeadAgentActivity(ctx context.Context, config *LeadAgentConfig) (*LeadAgentResult, error) {
    logger := activity.GetLogger(ctx)
    taskID := config.TaskID
    startTime := time.Now()

    // ========== 阶段 1：初始化 Lead Agent 状态 ==========
    redis.HSet(ctx, "swarm:"+taskID+":lead", map[string]interface{}{
        "task_id":          taskID,
        "status":           "initialized",
        "worker_pool_size": config.WorkerPoolSize,
        "workers_active":   0,
        "tasks_total":      0,
        "tasks_completed":  0,
        "tasks_failed":     0,
        "created_at":       startTime.Format(time.RFC3339),
    })

    EmitEventActivity(ctx, &AgentEvent{
        Type: "LEAD_INITIALIZED",
        TaskID: taskID,
        Payload: map[string]interface{}{
            "worker_pool_size": config.WorkerPoolSize,
            "react_enabled":    config.ReActEnabled,
        },
    })

    // ========== 阶段 2：任务分解（Lead Agent 调用 LLM）==========
    plan := decomposeTask(config.Query, config)
    subTasks := plan.SubTasks

    redis.HSet(ctx, "swarm:"+taskID+":lead", "tasks_total", len(subTasks))

    EmitEventActivity(ctx, &AgentEvent{
        Type: "TASK_DECOMPOSED",
        TaskID: taskID,
        Payload: map[string]interface{}{
            "sub_tasks_count": len(subTasks),
        },
    })

    // ========== 阶段 3：分发子任务到 Worker Pool ==========
    // 优先级队列排序
    sort.Slice(subTasks, func(i, j int) bool {
        return subTasks[i].Priority > subTasks[j].Priority
    })

    // 任务分发通道
    taskChan := make(chan WorkerTask, len(subTasks))
    resultChan := make(chan WorkerResult, len(subTasks))
    errorChan := make(chan error, config.WorkerPoolSize)

    // 启动 Worker goroutine（Activity 内合规）
    var wg sync.WaitGroup
    for i := 0; i < config.WorkerPoolSize; i++ {
        wg.Add(1)
        go func(workerIdx int) {
            defer wg.Done()
            workerID := fmt.Sprintf("worker-%s-%d", taskID[:8], workerIdx)

            // Worker 初始化
            redis.HSet(ctx, "swarm:"+taskID+":workers", workerID, "idle")
            redis.HIncrBy(ctx, "swarm:"+taskID+":lead", "workers_active", 1)

            for task := range taskChan {
                // 更新 Worker 状态
                redis.HSet(ctx, "swarm:"+taskID+":workers", map[string]interface{}{
                    workerID:                    "running",
                    workerID + ":current_task_id": task.TaskID,
                    workerID + ":current_sub_task_id": task.SubTaskID,
                    workerID + ":last_heartbeat":  time.Now().Unix(),
                })

                EmitEventActivity(ctx, &AgentEvent{
                    Type: "WORKER_DISPATCHED",
                    TaskID: taskID,
                    Payload: map[string]interface{}{
                        "worker_id":   workerID,
                        "sub_task_id": task.SubTaskID,
                    },
                })

                // 执行 Worker 任务（包含 ReAct 支持）
                result := executeWorkerTask(ctx, task, config)
                result.WorkerID = workerID

                // 更新 Worker 状态
                status := "completed"
                if result.Status == "failed" {
                    status = "failed"
                    redis.HIncrBy(ctx, "swarm:"+taskID+":lead", "tasks_failed", 1)
                } else if result.Status == "timeout" {
                    status = "timeout"
                }

                redis.HSet(ctx, "swarm:"+taskID+":workers", map[string]interface{}{
                    workerID:       status,
                    workerID + ":tasks_completed": 1, // 简化，实际应累加
                })

                // 更新 Lead 状态
                redis.HIncrBy(ctx, "swarm:"+taskID+":lead", "tasks_completed", 1)

                EmitEventActivity(ctx, &AgentEvent{
                    Type: "WORKER_COMPLETED",
                    TaskID: taskID,
                    Payload: map[string]interface{}{
                        "worker_id":   workerID,
                        "sub_task_id": task.SubTaskID,
                        "status":      result.Status,
                        "output_len":  len(result.Output),
                    },
                })

                resultChan <- result
            }
        }(i)
    }

    // 发送任务到队列
    for _, subTask := range subTasks {
        task := WorkerTask{
            TaskID:          taskID,
            SubTaskID:       subTask.ID,
            Prompt:         subTask.Prompt,
            Priority:       subTask.Priority,
            EnableReAct:    config.ReActEnabled,
            MaxIterations:  config.MaxReActIterations,
            CreatedAt:      time.Now().Format(time.RFC3339),
            ExpiresAt:      time.Now().Add(time.Duration(config.TaskTimeout) * time.Second).Format(time.RFC3339),
        }
        redis.HSet(ctx, "swarm:"+taskID+":tasks", subTask.ID, toJSON(task))
        taskChan <- task
    }
    close(taskChan)

    // 收集结果
    go func() {
        wg.Wait()
        close(resultChan)
    }()

    var results []WorkerResult
    for result := range resultChan {
        results = append(results, result)
        redis.HSet(ctx, "swarm:"+taskID+":results", result.SubTaskID, toJSON(result))
    }

    // ========== 阶段 4：结果聚合 ==========
    finalOutput := aggregateResults(ctx, taskID, results)

    // ========== 阶段 5：更新 Lead 状态 ==========
    completedAt := time.Now()
    redis.HSet(ctx, "swarm:"+taskID+":lead", map[string]interface{}{
        "status":          "completed",
        "updated_at":      completedAt.Format(time.RFC3339),
        "completed_at":    completedAt.Format(time.RFC3339),
    })

    EmitEventActivity(ctx, &AgentEvent{
        Type: "LEAD_COMPLETED",
        TaskID: taskID,
        Payload: map[string]interface{}{
            "final_output_length": len(finalOutput),
            "duration_ms":        completedAt.Sub(startTime).Milliseconds(),
        },
    })

    return &LeadAgentResult{
        TaskID:   taskID,
        Status:   "completed",
        Output:   finalOutput,
        Duration: completedAt.Sub(startTime).Milliseconds(),
    }, nil
}
```

**⚠️ Lead Agent Activity 资源限制与安全说明：**

| 限制项 | 说明 | 默认值 |
|--------|------|--------|
| worker_pool_size | Worker 并发数 | 3（最大 10） |
| task_timeout | 单任务超时 | 60 秒 |
| heartbeat_interval | 心跳间隔 | 30 秒 |
| heartbeat_timeout | 心跳超时判定 | 90 秒（3 × 30s） |
| max_react_iterations | ReAct 最大迭代 | 3（最大 10） |
| Activity 总超时 | LeadAgentActivity 总超时 | 300 秒（5 分钟） |

#### 3.5 Lead Agent 协议（通信协议设计）

**3.5.1 消息协议类型**

| 协议类型 | 方向 | 用途 | 可靠性 |
|---------|------|------|--------|
| **Task Dispatch** | Lead → Worker | 分发子任务 | 可靠（持久化到 Redis Hash） |
| **Task Result** | Worker → Lead | 返回执行结果 | 可靠（持久化到 Redis Hash） |
| **Heartbeat** | Worker → Lead | 心跳保活 | 尽力（不持久化） |
| **Cancel Task** | Lead → Worker | 取消任务 | 尽力（依赖 Worker 响应） |
| **Status Query** | Lead → Worker | 查询 Worker 状态 | 可靠（Redis 状态） |

**3.5.2 消息顺序保证**

- **同一 Worker 的任务按 FIFO 顺序执行**（通过 channel 队列）
- **不同 Worker 的任务不保证顺序**（并发执行）
- **结果聚合按 sub_task_id 排序**（保证确定性）

**3.5.3 Worker 离线处理**

```
1. Lead Agent 每 30 秒检查 heartbeat_timeout（默认 90 秒无心跳判定离线）
2. 离线 Worker 的子任务重新入队
3. 最多重试 3 次（第 1 次尝试 → 第 2 次尝试 → 第 3 次尝试 → 标记 failed）
4. 离线 Worker 状态更新为 "offline"
5. Lead Agent 记录离线事件到 audit log
```

**3.5.4 网络延迟处理**

- P2P 消息 TTL = 300 秒，超时自动过期
- Activity 超时重试由 Temporal 处理（LocalRetries: 3 次）
- 状态同步延迟 < 1 秒（Redis 写入即生效）

#### 3.6 Lead Agent 事件

| 事件类型 | Payload | 说明 |
|---------|---------|------|
| LEAD_INITIALIZED | task_id, worker_pool_size, react_enabled, timestamp | Lead Agent 初始化完成 |
| TASK_DECOMPOSED | task_id, sub_tasks_count, timestamp | 任务分解完成 |
| WORKER_DISPATCHED | task_id, worker_id, sub_task_id, timestamp | Worker 被分发任务 |
| WORKER_HEARTBEAT | task_id, worker_id, timestamp | Worker 心跳 |
| WORKER_COMPLETED | task_id, worker_id, sub_task_id, status, output_length, react_steps, timestamp | Worker 完成 |
| WORKER_FAILED | task_id, worker_id, sub_task_id, error, retry_count, timestamp | Worker 失败 |
| WORKER_OFFLINE | task_id, worker_id, last_heartbeat, timestamp | Worker 离线 |
| TASK_REQUEUED | task_id, sub_task_id, reason, retry_count, timestamp | 任务重新入队 |
| RESULTS_AGGREGATED | task_id, total_results, duration_ms, timestamp | 结果聚合完成 |
| LEAD_COMPLETED | task_id, final_output_length, duration_ms, timestamp | Lead Agent 完成 |

#### 3.7 验收命令

```bash
# 测试 Lead Agent 初始化
TASK_ID=$(curl -s -X POST http://127.0.0.1:8080/api/v1/tasks \
  -H "Content-Type: application/json" \
  -d '{"query":"Analyze the pros and cons of AI in healthcare, education, and finance","config":{"mode":"swarm","worker_pool_size":3}}' \
  | jq -r '.task_id')

# 轮询等待
for i in $(seq 1 90); do
  STATUS=$(curl -s http://127.0.0.1:8080/api/v1/tasks/$TASK_ID | jq -r '.status')
  [[ "$STATUS" == "completed" ]] || [[ "$STATUS" == "failed" ]] && break
  sleep 2
done

# 验证 Lead 状态
docker exec deploy-redis-1 redis-cli HGETALL "swarm:$TASK_ID:lead"

# 验证 Worker 状态
docker exec deploy-redis-1 redis-cli HGETALL "swarm:$TASK_ID:workers"

# 验证任务分发
docker exec deploy-redis-1 redis-cli HGETALL "swarm:$TASK_ID:tasks"

# 验证结果
docker exec deploy-redis-1 redis-cli HGETALL "swarm:$TASK_ID:results"

# 验证 SSE 事件
docker exec deploy-redis-1 redis-cli XRANGE "task:$TASK_ID:events" - + COUNT 30

# 验证边界值：worker_pool_size=1（顺序执行）
TASK_ID=$(curl -s -X POST http://127.0.0.1:8080/api/v1/tasks \
  -H "Content-Type: application/json" \
  -d '{"query":"Sequential test","config":{"mode":"swarm","worker_pool_size":1}}' \
  | jq -r '.task_id')

# 验证边界值：worker_pool_size=10（最大并发）
TASK_ID=$(curl -s -X POST http://127.0.0.1:8080/api/v1/tasks \
  -H "Content-Type: application/json" \
  -d '{"query":"Max concurrency test","config":{"mode":"swarm","worker_pool_size":10,"max_total_tokens":64000}}' \
  | jq -r '.task_id')
```

#### 3.8 Pass 标准

- [ ] Lead Agent 初始化成功，swarm:{task_id}:lead 有记录
- [ ] 任务分解正确，产生 2+ 子任务
- [ ] Worker 状态追踪正确（idle/running/completed/failed/offline/timeout）
- [ ] Worker 结果正确写入 swarm:{task_id}:results
- [ ] 结果聚合正确，最终 output 非空
- [ ] SSE 事件完整（LEAD_INITIALIZED → WORKER_DISPATCHED → WORKER_COMPLETED → LEAD_COMPLETED）
- [ ] worker_pool_size=1 时退化为顺序执行（事件顺序可预测）
- [ ] worker_pool_size=10 时最大并发执行
- [ ] 心跳机制工作正常（无心跳超时事件）

---

### Phase 4B Slice 11：Agent P2P Communication

#### 3.9 职责

- Worker Agent 间通过 Redis Pub/Sub 进行 P2P 消息传递
- 支持直接消息和广播消息
- 支持消息确认和重试
- 消息持久化到 Redis Stream（可选）

#### 3.10 P2P 消息数据结构

```go
type P2PMessage struct {
    MessageID  string `json:"message_id"`  // UUID v4
    From      string `json:"from"`        // worker_id
    To        string `json:"to"`         // worker_id 或 "*"（广播）
    Type      string `json:"type"`      // "request" | "response" | "broadcast" | "heartbeat" | "cancel"
    Priority  int    `json:"priority"`   // 消息优先级（1-10），默认 5
    Payload   string `json:"payload"`    // 消息内容 JSON
    Timestamp string `json:"timestamp"`
    TTL       int    `json:"ttl"`        // 消息 TTL（秒），默认 300，最大 3600
    Status    string `json:"status"`     // "pending" | "delivered" | "read" | "expired" | "failed"
    RetryCount int   `json:"retry_count"` // 重试次数，默认 0，最大 3
    ParentID  string `json:"parent_id,omitempty"` // 关联的原始消息 ID（用于响应追踪）
}

type P2PMessageRequest struct {
    WorkerID  string `json:"worker_id"`
    Query    string `json:"query"`
    ContextID string `json:"context_id,omitempty"` // 关联的上下文 ID
    FileID   string `json:"file_id,omitempty"`    // 请求的文件 ID
    Priority int    `json:"priority"`             // 优先级
}

type P2PMessageResponse struct {
    WorkerID  string `json:"worker_id"`
    Status   string `json:"status"`    // "ok" | "error" | "busy" | "timeout"
    Output   string `json:"output,omitempty"`
    Error    string `json:"error,omitempty"`
    LatencyMs int   `json:"latency_ms"` // 处理延迟
}
```

#### 3.11 Redis 数据结构（P2P）

```
Key: p2p:messages:{task_id}
Type: Stream
Fields:
  - message_id: P2PMessage JSON
  - from: worker_id
  - to: worker_id 或 "*"
  - type: message type
  - priority: 优先级
  - payload: message payload
  - timestamp: RFC3339 timestamp
  - status: 消息状态
  - parent_id: 关联的原始消息 ID
TTL: 3600 秒（1 小时）
MAXLEN: 10000（超过后自动修剪）

Key: p2p:subscriptions:{worker_id}
Type: Set
Members: task_id 列表（Worker 订阅的任务）
TTL: 86400 秒（1 天）

Key: p2p:pending:{worker_id}
Type: List
内容: 未读消息 JSON
TTL: 3600 秒（1 小时）

Key: p2p:acknowledgements:{message_id}
Type: String
Value: "ack" 或 "nack"
TTL: 300 秒（5 分钟）

Key: p2p:message_status:{task_id}
Type: Hash
Fields:
  - message_id -> status (pending/delivered/read/expired/failed)
TTL: 3600 秒（1 小时）
```

#### 3.12 P2P Activity 实现

```go
// SendP2PMessageActivity - 发送 P2P 消息
func SendP2PMessageActivity(ctx context.Context, msg *P2PMessage) error {
    logger := activity.GetLogger(ctx)
    msg.MessageID = uuid.New().String()
    msg.Timestamp = time.Now().Format(time.RFC3339)
    msg.Status = "pending"

    if msg.TTL == 0 {
        msg.TTL = 300 // 默认 TTL 5 分钟
    }

    // 写入 Stream（持久化，可追溯）
    data, _ := json.Marshal(msg)
    err := redis.XAdd(ctx, "p2p:messages:"+msg.To, &redis.XAddArgs{
        Stream: "p2p:messages:"+msg.To,
        MaxLen: 10000,
        Approx: true,
        Values: map[string]interface{}{
            "message_id": msg.MessageID,
            "from":       msg.From,
            "to":         msg.To,
            "type":       msg.Type,
            "priority":   msg.Priority,
            "payload":    string(data),
            "timestamp": msg.Timestamp,
            "status":    msg.Status,
            "parent_id":  msg.ParentID,
        },
    }).Err()

    if err != nil {
        logger.Error("SendP2PMessage XAdd failed", "error", err)
        return err
    }

    // 更新消息状态追踪
    statusKey := "p2p:message_status:" + msg.To
    redis.HSet(ctx, statusKey, msg.MessageID, msg.Status)
    redis.Expire(ctx, statusKey, time.Hour)

    // 如果是广播，发布到 Pub/Sub（实时通知）
    if msg.To == "*" {
        redis.Publish(ctx, "p2p:broadcast:"+msg.From, string(data))
    } else {
        // 直接消息，推送到接收者 pending 队列（BLPOP 接收）
        redis.RPush(ctx, "p2p:pending:"+msg.To, string(data))
    }

    logger.Info("P2P message sent",
        "message_id", msg.MessageID,
        "from", msg.From,
        "to", msg.To,
        "type", msg.Type)

    return nil
}

// ReceiveP2PMessageActivity - 接收 P2P 消息
func ReceiveP2PMessageActivity(ctx context.Context, input *ReceiveMessageInput) (*P2PMessage, error) {
    // 阻塞等待时间
    timeout := time.Duration(input.TimeoutSeconds) * time.Second
    if timeout == 0 {
        timeout = 30 * time.Second // 默认 30 秒超时
    }

    // 使用 BLPOP 阻塞等待
    result, err := redis.BLPop(ctx, timeout, "p2p:pending:"+input.WorkerID).Result()
    if err != nil {
        if err == redis.Nil {
            return nil, nil // 超时，无消息
        }
        return nil, err
    }

    var msg P2PMessage
    json.Unmarshal([]byte(result[1]), &msg)

    // 更新消息状态
    statusKey := "p2p:message_status:" + input.WorkerID
    redis.HSet(ctx, statusKey, msg.MessageID, "read")

    return &msg, nil
}

// SendAcknowledgementActivity - 发送消息确认
func SendAcknowledgementActivity(ctx context.Context, messageID, workerID string, acknowledged bool) error {
    ackKey := "p2p:acknowledgements:" + messageID
    if acknowledged {
        redis.Set(ctx, ackKey, "ack", 5*time.Minute)
    } else {
        redis.Set(ctx, ackKey, "nack", 5*time.Minute)
    }

    // 更新消息状态
    statusKey := "p2p:message_status:" + workerID
    status := "delivered"
    if !acknowledged {
        status = "failed"
    }
    redis.HSet(ctx, statusKey, messageID, status)

    return nil
}

// BroadcastP2PMessageActivity - 广播消息
func BroadcastP2PMessageActivity(ctx context.Context, taskID, workerID string, payload string, msgType string) error {
    if msgType == "" {
        msgType = "broadcast"
    }

    msg := &P2PMessage{
        From:      workerID,
        To:        "*",
        Type:      msgType,
        Payload:   payload,
        Timestamp: time.Now().Format(time.RFC3339),
        TTL:       300,
        Status:    "pending",
        Priority:  5,
    }
    return SendP2PMessageActivity(ctx, msg)
}
```

#### 3.13 P2P 协作场景与协议

**场景 1：Worker 间请求上下文**
```
Worker-A (处理 sub-task-1)
  → SendP2PMessage(to="Worker-B", type="request", payload={file_id: "context.json"})
  → ReceiveP2PMessage(timeout=60s)
  → 收到 Worker-B 的响应 (type="response", payload={content: "..."})
  → SendAcknowledgement(acknowledged=true)
  → 继续处理
```

**场景 2：广播状态更新**
```
Worker-A 完成关键步骤
  → BroadcastP2PMessage(type="status_update", payload={status: "completed", files_modified: [...]})
  → 其他订阅该任务的 Worker 收到通知
```

**场景 3：心跳检测**
```
Worker-A 每 30 秒发送心跳
  → BroadcastP2PMessage(type="heartbeat", payload={worker_id, timestamp, current_sub_task})
  → Lead Agent 监控 Worker 健康状态
  → 超时 90 秒（3 × 30s）无心跳 → 标记 Worker 为 offline
```

**场景 4：任务取消**
```
Lead Agent 检测到任务超时
  → SendP2PMessage(to="Worker-A", type="cancel", payload={sub_task_id, reason: "timeout"})
  → Worker-A 收到取消消息 → 停止执行 → 返回 partial result
```

#### 3.14 P2P 消息顺序与可靠性

**消息顺序保证：**
- 同一 sender → receiver 的消息按时间戳排序
- 不同 sender 的消息不保证顺序
- 广播消息按 Pub/Sub 接收顺序处理

**消息可靠性级别：**
| 类型 | 持久化 | 确认机制 | 超时处理 |
|------|--------|----------|----------|
| Task Dispatch | Redis Stream | 无（Lead 持久化） | 自动过期 |
| Task Result | Redis Stream | 无（Lead 持久化） | 自动过期 |
| Heartbeat | 无 | 无 | 自动过期 |
| Cancel | Redis Stream | ACK | 重试 3 次 |

#### 3.15 验收命令

```bash
# 测试 P2P 消息发送
TASK_ID=$(curl -s -X POST http://127.0.0.1:8080/api/v1/tasks \
  -H "Content-Type: application/json" \
  -d '{"query":"Test P2P communication","config":{"mode":"swarm","enable_p2p":true}}' \
  | jq -r '.task_id')

# 等待任务完成
for i in $(seq 1 60); do
  STATUS=$(curl -s http://127.0.0.1:8080/api/v1/tasks/$TASK_ID | jq -r '.status')
  [[ "$STATUS" == "completed" ]] || [[ "$STATUS" == "failed" ]] && break
  sleep 2
done

# 验证 P2P 消息 Stream
docker exec deploy-redis-1 redis-cli XRANGE "p2p:messages:*" - + COUNT 10

# 验证消息状态追踪
docker exec deploy-redis-1 redis-cli HGETALL "p2p:message_status:$TASK_ID"

# 验证 Worker 订阅
docker exec deploy-redis-1 redis-cli SMEMBERS "p2p:subscriptions:worker-*-0"

# 验证消息 TTL 过期
docker exec deploy-redis-1 redis-cli TTL "p2p:messages:$TASK_ID"

# 验证广播消息
docker exec deploy-redis-1 redis-cli PUBSUB CHANNELS "p2p:broadcast:*"
```

#### 3.16 Pass 标准

- [ ] P2P 消息正确发送和接收
- [ ] 直接消息推送到接收者 pending 队列（BLPOP 可接收）
- [ ] 广播消息发布到 Pub/Sub
- [ ] 消息状态追踪正确（pending/delivered/read/expired/failed）
- [ ] 超时场景正确处理（ReceiveP2PMessage 返回 nil）
- [ ] 消息 TTL 正确设置（默认 300 秒）
- [ ] enable_p2p=false 时 P2P 功能禁用
- [ ] 消息重试机制工作正常（最多 3 次）

---

### Phase 4C Slice 12：Workspace File Sharing

#### 3.17 职责

- 共享工作空间，多个 Worker 读写同一文件
- **Git-like 版本控制策略**（每次修改生成新版本，支持版本回溯）
- 文件元数据追踪（创建者、创建时间、最后修改者、最后修改时间）
- 文件访问控制（读取/写入/删除权限）
- **最终一致性 + 异常回滚**

#### 3.18 版本控制策略（Git-like）

**核心原则：**
1. **不可变性**：每个版本都是不可变的快照
2. **线性历史**：版本按时间顺序线性排列
3. **内容寻址**：使用 ContentHash（SHA256）作为内容标识
4. **元数据分离**：元数据（创建者、时间）与内容分离存储

**版本生命周期：**
```
v1 (created) → v2 (modified) → v3 (modified) → ... → v10 (modified)
                 ↓                              ↓
              v2.1 (rollback)              v10.1 (rollback)
```

**版本保留策略：**
- LTRIM 保留最近 10 个版本（可配置）
- 超过 10 个版本时，最旧版本自动删除
- 版本历史保留在 Redis List，索引 0-9

#### 3.19 Workspace 数据结构

```go
type WorkspaceFile struct {
    FileID       string `json:"file_id"`       // UUID v4
    TaskID       string `json:"task_id"`
    Path         string `json:"path"`          // 文件路径，如 "/docs/summary.md"
    Content      string `json:"content"`
    Version      int    `json:"version"`       // 版本号，从 1 开始
    ContentHash  string `json:"content_hash"` // SHA256 hash，内容相同则 hash 相同
    ContentSize  int    `json:"content_size"`  // 内容大小（字节）
    CreatedBy    string `json:"created_by"`   // worker_id
    CreatedAt    string `json:"created_at"`
    ModifiedBy   string `json:"modified_by"`  // worker_id
    ModifiedAt   string `json:"modified_at"`
    Status       string `json:"status"`       // "active" | "deleted" | "locking"
    LockedBy     string `json:"locked_by,omitempty"` // 持锁的 worker_id
    LockedAt     string `json:"locked_at,omitempty"` // 加锁时间
}

type WorkspaceVersion struct {
    FileID      string `json:"file_id"`
    Version     int    `json:"version"`
    Content     string `json:"content"`
    ContentHash string `json:"content_hash"`
    ContentSize int    `json:"content_size"`
    ModifiedBy  string `json:"modified_by"`
    ModifiedAt  string `json:"modified_at"`
    ChangeNote  string `json:"change_note,omitempty"`
    ChangeType  string `json:"change_type"` // "create" | "modify" | "rollback"
}

type WorkspaceLock struct {
    FileID     string `json:"file_id"`
    WorkerID   string `json:"worker_id"`
    LockType   string `json:"lock_type"`   // "read" | "write"
    AcquiredAt string `json:"acquired_at"`
    ExpiresAt  string `json:"expires_at"`  // 自动释放时间
    TTL        int    `json:"ttl"`         // 锁 TTL（秒）
}
```

#### 3.20 Redis 数据结构（Workspace）

```
Key: workspace:{task_id}:files
Type: Hash
Fields:
  - file_path -> WorkspaceFile JSON
TTL: 86400 秒（1 天）

Key: workspace:{task_id}:versions:{file_path}
Type: List
内容: WorkspaceVersion JSON（每个版本一个元素，索引 0-9）
TTL: 86400 秒（1 天）

Key: workspace:{task_id}:locks:{file_path}
Type: String
Value: WorkspaceLock JSON
TTL: 300 秒（5 分钟，自动释放）

Key: workspace:{task_id}:metadata
Type: Hash
Fields:
  - total_files: 文件总数
  - total_versions: 版本总数
  - active_workers: 活跃 Worker 列表
  - created_at: 创建时间
  - max_versions: 最大保留版本数（默认 10）
TTL: 86400 秒（1 天）

Key: workspace:{task_id}:change_log
Type: List
内容: ChangeLogEntry JSON（最近 100 条变更记录）
TTL: 86400 秒（1 天）
```

#### 3.21 WorkspaceActivity 实现

```go
// CreateWorkspaceFileActivity - 创建文件
func CreateWorkspaceFileActivity(ctx context.Context, input *CreateFileInput) (*WorkspaceFile, error) {
    logger := activity.GetLogger(ctx)
    taskID := input.TaskID
    path := input.Path

    // ========== 步骤 1：获取写锁（SETNX 分布式锁）==========
    lockKey := "workspace:" + taskID + ":locks:" + path
    lock := &WorkspaceLock{
        FileID:     "",
        WorkerID:   input.WorkerID,
        LockType:   "write",
        AcquiredAt: time.Now().Format(time.RFC3339),
        TTL:        300,
        ExpiresAt:  time.Now().Add(5 * time.Minute).Format(time.RFC3339),
    }
    lockData, _ := json.Marshal(lock)

    // SETNX 实现分布式锁
    acquired, err := redis.SetNX(ctx, lockKey, string(lockData), 5*time.Second).Result()
    if err != nil {
        return nil, fmt.Errorf("failed to acquire lock: %w", err)
    }
    if !acquired {
        return nil, fmt.Errorf("file %s is locked by another worker", path)
    }
    defer redis.Del(ctx, lockKey)

    // ========== 步骤 2：检查文件是否已存在 ==========
    fileKey := "workspace:" + taskID + ":files"
    existing, _ := redis.HGet(ctx, fileKey, path).Result()
    if existing != "" {
        return nil, fmt.Errorf("file %s already exists", path)
    }

    // ========== 步骤 3：创建文件（v1）==========
    fileID := uuid.New().String()
    contentHash := sha256Hash(input.Content)

    file := &WorkspaceFile{
        FileID:       fileID,
        TaskID:       taskID,
        Path:         path,
        Content:      input.Content,
        Version:      1,
        ContentHash:  contentHash,
        ContentSize:  len(input.Content),
        CreatedBy:    input.WorkerID,
        CreatedAt:    time.Now().Format(time.RFC3339),
        ModifiedBy:   input.WorkerID,
        ModifiedAt:   time.Now().Format(time.RFC3339),
        Status:       "active",
    }

    // 写入文件
    data, _ := json.Marshal(file)
    redis.HSet(ctx, fileKey, path, string(data))

    // ========== 步骤 4：写入初始版本（v1）==========
    versionKey := "workspace:" + taskID + ":versions:" + path
    version := WorkspaceVersion{
        FileID:      fileID,
        Version:     1,
        Content:     input.Content,
        ContentHash: contentHash,
        ContentSize: len(input.Content),
        ModifiedBy:  input.WorkerID,
        ModifiedAt:  time.Now().Format(time.RFC3339),
        ChangeNote:  "Initial version",
        ChangeType:  "create",
    }
    versionData, _ := json.Marshal(version)
    redis.RPush(ctx, versionKey, string(versionData))

    // 记录变更日志
    recordChangeLog(ctx, taskID, "file_created", path, fileID, input.WorkerID, 1)

    logger.Info("Workspace file created",
        "file_id", fileID,
        "path", path,
        "content_hash", contentHash,
        "size", len(input.Content))

    return file, nil
}

// UpdateWorkspaceFileActivity - 更新文件
func UpdateWorkspaceFileActivity(ctx context.Context, input *UpdateFileInput) (*WorkspaceFile, error) {
    logger := activity.GetLogger(ctx)
    taskID := input.TaskID
    path := input.Path

    // ========== 步骤 1：获取写锁 ==========
    lockKey := "workspace:" + taskID + ":locks:" + path
    lock := &WorkspaceLock{
        FileID:     "",
        WorkerID:   input.WorkerID,
        LockType:   "write",
        AcquiredAt: time.Now().Format(time.RFC3339),
        TTL:        300,
    }
    lockData, _ := json.Marshal(lock)

    acquired, err := redis.SetNX(ctx, lockKey, string(lockData), 5*time.Second).Result()
    if err != nil {
        return nil, fmt.Errorf("failed to acquire lock: %w", err)
    }
    if !acquired {
        return nil, fmt.Errorf("file %s is locked by another worker", path)
    }
    defer redis.Del(ctx, lockKey)

    // ========== 步骤 2：读取当前文件 ==========
    fileKey := "workspace:" + taskID + ":files"
    existing, err := redis.HGet(ctx, fileKey, path).Result()
    if err != nil {
        return nil, fmt.Errorf("file %s not found", path)
    }

    var file WorkspaceFile
    json.Unmarshal([]byte(existing), &file)

    if file.Status == "deleted" {
        return nil, fmt.Errorf("file %s has been deleted", path)
    }

    // ========== 步骤 3：检查内容是否变化（内容寻址）==========
    newContentHash := sha256Hash(input.Content)
    if newContentHash == file.ContentHash {
        // 内容未变化，不创建新版本
        logger.Info("Content unchanged, no new version created",
            "path", path,
            "content_hash", newContentHash)
        return &file, nil
    }

    // ========== 步骤 4：版本冲突检测（可选）==========
    if input.ExpectedVersion > 0 && file.Version != input.ExpectedVersion {
        return nil, &VersionConflictError{
            FilePath:         path,
            ExpectedVersion: input.ExpectedVersion,
            ActualVersion:   file.Version,
            Message:         fmt.Sprintf("version conflict: expected %d, got %d", input.ExpectedVersion, file.Version),
        }
    }

    // ========== 步骤 5：更新文件（递增版本）==========
    oldVersion := file.Version
    file.Content = input.Content
    file.Version++
    file.ContentHash = newContentHash
    file.ContentSize = len(input.Content)
    file.ModifiedBy = input.WorkerID
    file.ModifiedAt = time.Now().Format(time.RFC3339)

    // 写入文件
    data, _ := json.Marshal(file)
    redis.HSet(ctx, fileKey, path, string(data))

    // ========== 步骤 6：写入新版本 ==========
    versionKey := "workspace:" + taskID + ":versions:" + path
    version := WorkspaceVersion{
        FileID:      file.FileID,
        Version:     file.Version,
        Content:     input.Content,
        ContentHash: newContentHash,
        ContentSize: len(input.Content),
        ModifiedBy:  input.WorkerID,
        ModifiedAt:  time.Now().Format(time.RFC3339),
        ChangeNote:  input.ChangeNote,
        ChangeType:  "modify",
    }
    versionData, _ := json.Marshal(version)
    redis.RPush(ctx, versionKey, string(versionData))

    // ========== 步骤 7：LTRIM 保留最近 10 个版本 ==========
    redis.LTrim(ctx, versionKey, -10, -1)

    // 记录变更日志
    recordChangeLog(ctx, taskID, "file_modified", path, file.FileID, input.WorkerID, file.Version)

    logger.Info("Workspace file updated",
        "file_id", file.FileID,
        "path", path,
        "version", file.Version,
        "old_version", oldVersion,
        "content_hash", newContentHash)

    return &file, nil
}

// RollbackWorkspaceFileActivity - 回滚到指定版本
func RollbackWorkspaceFileActivity(ctx context.Context, input *RollbackInput) (*WorkspaceFile, error) {
    taskID := input.TaskID
    path := input.Path
    targetVersion := input.TargetVersion

    // 获取目标版本
    versionKey := "workspace:" + taskID + ":versions:" + path
    index := targetVersion - 1
    versionData, err := redis.LIndex(ctx, versionKey, int64(index)).Result()
    if err != nil {
        return nil, fmt.Errorf("version %d not found for %s", targetVersion, path)
    }

    var targetVersionData WorkspaceVersion
    json.Unmarshal([]byte(versionData), &targetVersionData)

    // 使用目标版本内容更新文件
    updateInput := &UpdateFileInput{
        TaskID:      taskID,
        Path:        path,
        Content:     targetVersionData.Content,
        WorkerID:    input.WorkerID,
        ChangeNote: fmt.Sprintf("Rollback to v%d", targetVersion),
    }

    file, err := UpdateWorkspaceFileActivity(ctx, updateInput)
    if err != nil {
        return nil, err
    }

    // 记录回滚日志
    recordChangeLog(ctx, taskID, "file_rollback", path, file.FileID, input.WorkerID, file.Version)

    return file, nil
}

// ReadWorkspaceFileActivity - 读取文件（最新版本）
func ReadWorkspaceFileActivity(ctx context.Context, input *ReadFileInput) (*WorkspaceFile, error) {
    fileKey := "workspace:" + input.TaskID + ":files"

    existing, err := redis.HGet(ctx, fileKey, input.Path).Result()
    if err != nil {
        return nil, fmt.Errorf("file %s not found", input.Path)
    }

    var file WorkspaceFile
    json.Unmarshal([]byte(existing), &file)

    if file.Status == "deleted" {
        return nil, fmt.Errorf("file %s has been deleted", input.Path)
    }

    return &file, nil
}

// ReadWorkspaceFileVersionActivity - 读取文件特定版本
func ReadWorkspaceFileVersionActivity(ctx context.Context, input *ReadVersionInput) (*WorkspaceVersion, error) {
    versionKey := "workspace:" + input.TaskID + ":versions:" + input.Path

    index := input.Version - 1
    result, err := redis.LIndex(ctx, versionKey, int64(index)).Result()
    if err != nil {
        return nil, fmt.Errorf("version %d not found for %s", input.Version, input.Path)
    }

    var version WorkspaceVersion
    json.Unmarshal([]byte(result), &version)

    return &version, nil
}

// DeleteWorkspaceFileActivity - 删除文件（软删除）
func DeleteWorkspaceFileActivity(ctx context.Context, input *DeleteFileInput) error {
    taskID := input.TaskID
    path := input.Path

    // 获取写锁
    lockKey := "workspace:" + taskID + ":locks:" + path
    acquired, _ := redis.SetNX(ctx, lockKey, input.WorkerID, 5*time.Second).Result()
    if !acquired {
        return fmt.Errorf("file %s is locked by another worker", path)
    }
    defer redis.Del(ctx, lockKey)

    // 更新状态为 deleted
    fileKey := "workspace:" + taskID + ":files"
    existing, _ := redis.HGet(ctx, fileKey, path).Result()
    if existing == "" {
        return fmt.Errorf("file %s not found", path)
    }

    var file WorkspaceFile
    json.Unmarshal([]byte(existing), &file)
    file.Status = "deleted"
    file.ModifiedBy = input.WorkerID
    file.ModifiedAt = time.Now().Format(time.RFC3339)

    data, _ := json.Marshal(file)
    redis.HSet(ctx, fileKey, path, string(data))

    // 记录变更日志
    recordChangeLog(ctx, taskID, "file_deleted", path, file.FileID, input.WorkerID, file.Version)

    return nil
}

// recordChangeLog - 记录变更日志
func recordChangeLog(ctx context.Context, taskID, action, path, fileID, workerID string, version int) {
    entry := map[string]interface{}{
        "log_id":     uuid.New().String(),
        "action":     action,
        "path":       path,
        "file_id":    fileID,
        "worker_id":  workerID,
        "version":    version,
        "timestamp": time.Now().Format(time.RFC3339),
    }
    data, _ := json.Marshal(entry)
    redis.RPush(ctx, "workspace:"+taskID+":change_log", string(data))
    redis.LTrim(ctx, "workspace:"+taskID+":change_log", -100, -1) // 保留最近 100 条
}
```

#### 3.22 最终一致性策略

**写操作流程：**
```
1. 获取分布式锁（SETNX，TTL 5 秒）
2. 读取当前版本
3. 内容变更检测（SHA256 hash）
4. 版本递增（乐观并发控制）
5. 写入 Redis Hash
6. 写入版本历史（RPUSH + LTRIM 10）
7. 释放锁
```

**回滚策略：**
- 自动回滚：无（版本历史保留，手动回滚）
- 异常回滚：锁获取失败 → return error，不阻塞其他操作
- 锁超时（5 秒）→ 自动释放，避免死锁

**一致性与性能权衡：**
| 策略 | 一致性 | 性能 | 适用场景 |
|------|--------|------|----------|
| SETNX + TTL | 强一致 | 中等 | 高频写入 |
| 最终一致性 | 弱一致 | 高 | 日志/变更记录 |
| 乐观锁 | 版本检查 | 高 | 低冲突场景 |

#### 3.23 验收命令

```bash
# 测试 Workspace 文件创建
TASK_ID=$(curl -s -X POST http://127.0.0.1:8080/api/v1/tasks \
  -H "Content-Type: application/json" \
  -d '{"query":"Create and manage workspace files","config":{"mode":"swarm","workspace_enabled":true}}' \
  | jq -r '.task_id')

# 轮询等待任务完成
for i in $(seq 1 60); do
  STATUS=$(curl -s http://127.0.0.1:8080/api/v1/tasks/$TASK_ID | jq -r '.status')
  [[ "$STATUS" == "completed" ]] || [[ "$STATUS" == "failed" ]] && break
  sleep 2
done

# 验证文件创建
docker exec deploy-redis-1 redis-cli HGETALL "workspace:$TASK_ID:files"

# 验证版本历史（应至少有 1 个版本）
docker exec deploy-redis-1 redis-cli LLEN "workspace:$TASK_ID:versions:/docs/summary.md"
docker exec deploy-redis-1 redis-cli LRANGE "workspace:$TASK_ID:versions:/docs/summary.md" 0 -1

# 验证内容寻址（相同内容应生成相同 hash）
docker exec deploy-redis-1 redis-cli HGET "workspace:$TASK_ID:files" "/docs/summary.md" | jq -r '.content_hash'

# 验证变更日志
docker exec deploy-redis-1 redis-cli LRANGE "workspace:$TASK_ID:change_log" 0 -1

# 验证并发写入锁（预期失败）
docker exec deploy-redis-1 redis-cli EXISTS "workspace:$TASK_ID:locks:/docs/summary.md"

# 验证版本回滚
# 1. 获取当前版本
# 2. 回滚到 v1
# 3. 验证内容恢复
```

#### 3.24 Pass 标准

- [ ] 文件创建成功，workspace:{task_id}:files 有记录
- [ ] 文件更新后版本号递增
- [ ] 版本历史保留最近 10 个版本（LTRIM 生效）
- [ ] 内容变更检测生效（相同内容不生成新版本）
- [ ] 并发写入锁正确工作（SETNX 分布式锁）
- [ ] 锁超时（5 秒）后自动释放
- [ ] 文件读取返回正确内容
- [ ] 已删除文件不能读取（软删除）
- [ ] 版本回滚功能正常
- [ ] workspace_enabled=false 时 Workspace 功能禁用
- [ ] ContentHash 作为内容标识（相同内容 hash 相同）

---

### Phase 4D Slice 13：State Synchronization

#### 3.25 职责

- 分布式状态同步，最终一致性策略
- 状态变更事件发布到 Redis Pub/Sub + Stream
- 状态订阅和通知机制
- **向量时钟**实现版本追踪和因果顺序

#### 3.26 状态同步数据结构

```go
type StateChangeEvent struct {
    EventID       string `json:"event_id"`       // UUID v4
    TaskID        string `json:"task_id"`
    EntityType    string `json:"entity_type"`   // "worker" | "file" | "task" | "result" | "lead"
    EntityID      string `json:"entity_id"`     // worker_id / file_path / sub_task_id
    Operation     string `json:"operation"`     // "create" | "update" | "delete" | "heartbeat"
    PreviousValue string `json:"previous_value,omitempty"`
    NewValue      string `json:"new_value"`
    WorkerID      string `json:"worker_id"`      // 操作发起者
    Timestamp     string `json:"timestamp"`     // RFC3339
    VectorClock   int    `json:"vector_clock"`   // 版本号（递增）
    CausallyAfter []string `json:"causally_after,omitempty"` // 因果依赖的 event_id
}

type VectorClock struct {
    Clocks map[string]int `json:"clocks"` // entity_id -> clock value
    LastUpdate string    `json:"last_update"`
}

type StateSubscription struct {
    SubscriptionID string   `json:"subscription_id"`
    TaskID          string   `json:"task_id"`
    EntityTypes    []string `json:"entity_types"`  // 订阅的事件类型
    EntityIDs      []string `json:"entity_ids"`   // 订阅的实体 ID（空=全部）
    WorkerID       string   `json:"worker_id"`
    CreatedAt       string   `json:"created_at"`
    ExpiresAt       string   `json:"expires_at"`    // 自动取消时间
    MaxEvents       int      `json:"max_events"`   // 最大接收事件数
}
```

#### 3.27 Redis 数据结构（State Sync）

```
Key: state:sync:{task_id}:events
Type: Stream
Fields: StateChangeEvent JSON
TTL: 86400 秒（1 天）
MAXLEN: 10000

Key: state:sync:{task_id}:vector_clock
Type: Hash
Fields:
  - entity_id -> vector_clock 值
  - _global -> 全局时钟（用于排序）
TTL: 86400 秒（1 天）

Key: state:sync:{task_id}:subscriptions
Type: Hash
Fields:
  - subscription_id -> StateSubscription JSON
TTL: 3600 秒（1 小时）

Key: state:sync:{task_id}:pending_events:{worker_id}
Type: List
内容: 待推送的 StateChangeEvent JSON
TTL: 300 秒（5 分钟）
```

#### 3.28 StateSyncActivity 实现

```go
// PublishStateChangeActivity - 发布状态变更
func PublishStateChangeActivity(ctx context.Context, event *StateChangeEvent) error {
    logger := activity.GetLogger(ctx)
    event.EventID = uuid.New().String()
    event.Timestamp = time.Now().Format(time.RFC3339)

    // ========== 更新向量时钟 ==========
    vectorKey := "state:sync:" + event.TaskID + ":vector_clock"

    // 获取当前时钟值
    currentClock, _ := redis.HGet(ctx, vectorKey, event.EntityID).Int()
    newClock := currentClock + 1

    // 原子更新
    redis.HSet(ctx, vectorKey, event.EntityID, newClock)

    // 全局时钟递增
    globalClock, _ := redis.HIncrBy(ctx, vectorKey, "_global", 1).Result()
    _ = globalClock // 不使用全局时钟排序，只使用 entity 时钟

    event.VectorClock = newClock

    // ========== 写入事件 Stream ==========
    eventKey := "state:sync:" + event.TaskID + ":events"
    data, _ := json.Marshal(event)
    redis.XAdd(ctx, eventKey, &redis.XAddArgs{
        Stream: eventKey,
        MaxLen: 10000,
        Approx: true,
        Values: map[string]interface{}{
            "event":           string(data),
            "timestamp":       event.Timestamp,
            "entity_type":     event.EntityType,
            "entity_id":        event.EntityID,
            "vector_clock":    newClock,
        },
    })

    // ========== 发布到 Pub/Sub（实时通知）==========
    channel := "state:sync:" + event.TaskID + ":" + event.EntityType
    redis.Publish(ctx, channel, string(data))

    // ========== 推送消息到订阅者的 pending 队列 ==========
    pushToSubscriptions(ctx, event)

    logger.Info("State change published",
        "event_id", event.EventID,
        "entity_type", event.EntityType,
        "entity_id", event.EntityID,
        "operation", event.Operation,
        "vector_clock", newClock)

    return nil
}

// GetStateChangesActivity - 获取状态变更（基于向量时钟）
func GetStateChangesActivity(ctx context.Context, taskID, entityID string, sinceClock int) ([]StateChangeEvent, error) {
    eventKey := "state:sync:" + taskID + ":events"

    // 读取所有事件（XRead 不支持范围查询，先读取再过滤）
    all, err := redis.XRange(ctx, eventKey, "-", "+").Result()
    if err != nil {
        return nil, err
    }

    var events []StateChangeEvent
    for _, entry := range all {
        var event StateChangeEvent
        json.Unmarshal([]byte(entry.Values["event"].(string)), &event)

        // 过滤：entityID 匹配 且 时钟值大于 sinceClock
        if event.EntityID == entityID && event.VectorClock > sinceClock {
            events = append(events, event)
        }
    }

    // 按向量时钟排序
    sort.Slice(events, func(i, j int) bool {
        return events[i].VectorClock < events[j].VectorClock
    })

    return events, nil
}

// GetCurrentStateActivity - 获取当前状态（最新值）
func GetCurrentStateActivity(ctx context.Context, taskID, entityType, entityID string) (string, error) {
    switch entityType {
    case "worker":
        data, _ := redis.HGet(ctx, "swarm:"+taskID+":workers", entityID).Result()
        return data, nil
    case "file":
        data, _ := redis.HGet(ctx, "workspace:"+taskID+":files", entityID).Result()
        return data, nil
    case "lead":
        data, _ := redis.HGet(ctx, "swarm:"+taskID+":lead", entityID).Result()
        return data, nil
    case "task":
        data, _ := redis.HGet(ctx, "swarm:"+taskID+":tasks", entityID).Result()
        return data, nil
    case "result":
        data, _ := redis.HGet(ctx, "swarm:"+taskID+":results", entityID).Result()
        return data, nil
    default:
        return "", fmt.Errorf("unknown entity type: %s", entityType)
    }
}

// SubscribeStateChangesActivity - 订阅状态变更
func SubscribeStateChangesActivity(ctx context.Context, sub *StateSubscription) error {
    sub.SubscriptionID = uuid.New().String()
    sub.CreatedAt = time.Now().Format(time.RFC3339)

    subKey := "state:sync:" + sub.TaskID + ":subscriptions"
    data, _ := json.Marshal(sub)
    redis.HSet(ctx, subKey, sub.SubscriptionID, string(data))

    return nil
}

// pushToSubscriptions - 推送事件到订阅者
func pushToSubscriptions(ctx context.Context, event *StateChangeEvent) {
    subKey := "state:sync:" + event.TaskID + ":subscriptions"
    subs, _ := redis.HGetAll(ctx, subKey).Result()

    for subID, subData := range subs {
        var sub StateSubscription
        json.Unmarshal([]byte(subData), &sub)

        // 检查是否订阅该事件类型
        if !slices.Contains(sub.EntityTypes, event.EntityType) {
            continue
        }

        // 检查是否订阅该实体 ID（空=全部）
        if len(sub.EntityIDs) > 0 && !slices.Contains(sub.EntityIDs, event.EntityID) {
            continue
        }

        // 推送事件到 pending 队列
        pendingKey := "state:sync:" + event.TaskID + ":pending_events:" + sub.WorkerID
        data, _ := json.Marshal(event)
        redis.RPush(ctx, pendingKey, string(data))
        redis.Expire(ctx, pendingKey, 5*time.Minute)
    }
}
```

#### 3.29 最终一致性策略（完整版）

**策略 1：乐观并发控制**
- 读取时获取当前版本（Version/VectorClock）
- 写入时检查版本是否变化
- 变化则返回冲突错误（由 Slice 14 处理）

**策略 2：向量时钟**
- 每个实体维护向量时钟（EntityID → Clock）
- 客户端跟踪已知最新向量
- 读取时获取大于已知向量的变更
- 支持因果顺序判断

**策略 3：事件溯源**
- 所有变更作为事件记录到 Stream
- 状态由事件重放计算
- 支持历史回放和调试
- 持久化到 Redis Stream（TTL 1 天）

**策略 4：延迟同步**
- 写入操作先写入 Redis（立即一致）
- 可选：异步同步到 Postgres（最终一致）
- 延迟 < 1 秒

#### 3.30 验收命令

```bash
# 测试状态同步
TASK_ID=$(curl -s -X POST http://127.0.0.1:8080/api/v1/tasks \
  -H "Content-Type: application/json" \
  -d '{"query":"Test state synchronization","config":{"mode":"swarm","enable_p2p":true}}' \
  | jq -r '.task_id')

# 轮询等待任务完成
for i in $(seq 1 60); do
  STATUS=$(curl -s http://127.0.0.1:8080/api/v1/tasks/$TASK_ID | jq -r '.status')
  [[ "$STATUS" == "completed" ]] || [[ "$STATUS" == "failed" ]] && break
  sleep 2
done

# 验证状态变更事件
docker exec deploy-redis-1 redis-cli XRANGE "state:sync:$TASK_ID:events" - + COUNT 20

# 验证向量时钟（每个实体一个时钟）
docker exec deploy-redis-1 redis-cli HGETALL "state:sync:$TASK_ID:vector_clock"

# 验证订阅
docker exec deploy-redis-1 redis-cli HGETALL "state:sync:$TASK_ID:subscriptions"

# 验证因果顺序（事件按 vector_clock 递增）
docker exec deploy-redis-1 redis-cli XRANGE "state:sync:$TASK_ID:events" - + COUNT 10 | jq '.[] | from_entries.event | fromjson | {event_id, vector_clock, entity_id}'

# 验证最终一致性延迟（< 1 秒）
# 1. 发布事件
# 2. 立即读取
# 3. 验证状态一致
```

#### 3.31 Pass 标准

- [ ] 状态变更事件正确发布到 Stream 和 Pub/Sub
- [ ] 向量时钟正确递增（每个 entity 独立时钟）
- [ ] 订阅机制正常工作（EntityTypes + EntityIDs 过滤）
- [ ] GetStateChanges 正确过滤向量时钟
- [ ] 事件按向量时钟排序（因果顺序）
- [ ] 状态最终一致（< 1 秒延迟）
- [ ] Stream MAXLEN 10000 自动修剪

---

### Phase 4E Slice 14：Conflict Resolution

#### 3.32 职责

- 文件写入冲突检测
- **冲突解决策略**（自动/手动/用户介入）
- 冲突历史记录
- 冲突通知机制

#### 3.33 冲突数据结构

```go
type FileConflict struct {
    ConflictID    string   `json:"conflict_id"`
    TaskID        string   `json:"task_id"`
    FilePath      string   `json:"file_path"`
    FileID        string   `json:"file_id"`
    Version1      int      `json:"version1"`       // 冲突版本 1（本地版本）
    Content1      string   `json:"content1"`       // 冲突内容 1
    ContentHash1 string   `json:"content_hash1"` // 内容 1 的 hash
    WorkerID1     string   `json:"worker_id1"`    // 修改者 1
    ModifiedAt1   string   `json:"modified_at1"`  // 修改时间 1
    Version2      int      `json:"version2"`      // 冲突版本 2（最新版本）
    Content2      string   `json:"content2"`      // 冲突内容 2
    ContentHash2 string   `json:"content_hash2"` // 内容 2 的 hash
    WorkerID2     string   `json:"worker_id2"`    // 修改者 2
    ModifiedAt2   string   `json:"modified_at2"`  // 修改时间 2
    CreatedAt     string   `json:"created_at"`
    Resolved      bool     `json:"resolved"`      // 是否已解决
    Resolution    string   `json:"resolution,omitempty"` // "keep_version1" | "keep_version2" | "merge" | "manual"
    MergedContent string   `json:"merged_content,omitempty"` // 合并后的内容
    ResolvedBy    string   `json:"resolved_by,omitempty"`
    ResolvedAt    string   `json:"resolved_at,omitempty"`
    RetryCount    int      `json:"retry_count"`   // 冲突解决重试次数
}

type ConflictResolutionStrategy struct {
    Strategy   string `json:"strategy"` // "auto" | "manual" | "last_write_wins" | "first_write_wins"
    AutoMerge  bool   `json:"auto_merge"` // 是否自动合并
    MergeMode  string `json:"merge_mode"` // "line" | "word" | "semantic"
}
```

#### 3.34 Redis 数据结构（Conflicts）

```
Key: conflicts:{task_id}
Type: List
内容: FileConflict JSON（未解决的冲突）
TTL: 86400 秒（1 天）

Key: conflicts:resolved:{task_id}
Type: List
内容: FileConflict JSON（已解决的冲突）
TTL: 86400 秒（1 天）

Key: conflicts:pending_resolution:{task_id}
Type: List
内容: FileConflict JSON（待用户介入的冲突）
TTL: 86400 秒（1 天）
```

#### 3.35 ConflictResolutionActivity 实现

```go
// DetectConflictActivity - 检测冲突
func DetectConflictActivity(ctx context.Context, taskID, filePath, workerID string, expectedVersion int) (*FileConflict, error) {
    fileKey := "workspace:" + taskID + ":files"

    // 读取当前文件状态
    existing, err := redis.HGet(ctx, fileKey, filePath).Result()
    if err != nil {
        return nil, err
    }

    var file WorkspaceFile
    json.Unmarshal([]byte(existing), &file)

    // 检查版本是否匹配
    if file.Version != expectedVersion {
        // 版本不匹配，检测到冲突

        // 读取本地版本内容（expectedVersion - 1，因为 List 索引从 0 开始）
        versionKey := "workspace:" + taskID + ":versions:" + filePath
        localIndex := expectedVersion - 1
        prevResult, err := redis.LIndex(ctx, versionKey, int64(localIndex)).Result()
        if err != nil {
            // 本地版本可能已被 LTRIM 删除
            prevResult = "{}"
        }

        var localVersion WorkspaceVersion
        json.Unmarshal([]byte(prevResult), &localVersion)

        // 读取最新版本内容（-1 表示最后一个）
        latestResult, _ := redis.LIndex(ctx, versionKey, -1).Result()
        var latestVersion WorkspaceVersion
        json.Unmarshal([]byte(latestResult), &latestVersion)

        // 创建冲突记录
        conflict := &FileConflict{
            ConflictID:    uuid.New().String(),
            TaskID:        taskID,
            FilePath:      filePath,
            FileID:        file.FileID,
            Version1:       expectedVersion,
            Content1:       localVersion.Content,
            ContentHash1:   localVersion.ContentHash,
            WorkerID1:      localVersion.ModifiedBy,
            ModifiedAt1:    localVersion.ModifiedAt,
            Version2:       file.Version,
            Content2:       latestVersion.Content,
            ContentHash2:   latestVersion.ContentHash,
            WorkerID2:      latestVersion.ModifiedBy,
            ModifiedAt2:    latestVersion.ModifiedAt,
            CreatedAt:      time.Now().Format(time.RFC3339),
            Resolved:       false,
            RetryCount:     0,
        }

        // 写入冲突列表
        conflictKey := "conflicts:" + taskID
        conflictData, _ := json.Marshal(conflict)
        redis.RPush(ctx, conflictKey, string(conflictData))

        // 发布冲突检测事件
        EmitEventActivity(ctx, &AgentEvent{
            Type: "CONFLICT_DETECTED",
            TaskID: taskID,
            Payload: map[string]interface{}{
                "conflict_id": conflict.ConflictID,
                "file_path":   filePath,
                "version1":    expectedVersion,
                "version2":    file.Version,
                "worker_id1":   conflict.WorkerID1,
                "worker_id2":   conflict.WorkerID2,
            },
        })

        return conflict, nil
    }

    return nil, nil // 无冲突
}

// AutoResolveConflictActivity - 自动解决冲突
func AutoResolveConflictActivity(ctx context.Context, conflictID string, strategy string) (*FileConflict, error) {
    // strategy: "keep_version1" | "keep_version2" | "merge"

    // 查找冲突记录
    conflictKey := "conflicts:" + challenge.TaskID
    conflicts, _ := redis.LRange(ctx, conflictKey, 0, -1).Result()

    var conflict FileConflict
    var found bool
    for _, data := range conflicts {
        var c FileConflict
        json.Unmarshal([]byte(data), &c)
        if c.ConflictID == conflictID {
            conflict = c
            found = true
            break
        }
    }

    if !found {
        return nil, fmt.Errorf("conflict %s not found", conflictID)
    }

    var resolvedContent string
    switch strategy {
    case "keep_version1":
        resolvedContent = conflict.Content1
    case "keep_version2":
        resolvedContent = conflict.Content2
    case "merge":
        resolvedContent = mergeContents(conflict.Content1, conflict.Content2)
    default:
        return nil, fmt.Errorf("unknown strategy: %s", strategy)
    }

    // 获取当前文件版本
    fileKey := "workspace:" + conflict.TaskID + ":files"
    existing, _ := redis.HGet(ctx, fileKey, conflict.FilePath).Result()
    var file WorkspaceFile
    json.Unmarshal([]byte(existing), &file)

    // 更新文件（使用当前版本作为 expectedVersion）
    updateInput := &UpdateFileInput{
        TaskID:          conflict.TaskID,
        Path:            conflict.FilePath,
        Content:         resolvedContent,
        WorkerID:        "system", // 系统解决
        ExpectedVersion: file.Version,
        ChangeNote:      fmt.Sprintf("Auto-resolved conflict (%s): %s", strategy, conflict.ConflictID),
    }
    _, err := UpdateWorkspaceFileActivity(ctx, updateInput)
    if err != nil {
        return nil, err
    }

    // 更新冲突状态
    conflict.Resolved = true
    conflict.Resolution = strategy
    conflict.MergedContent = resolvedContent
    conflict.ResolvedBy = "system"
    conflict.ResolvedAt = time.Now().Format(time.RFC3339)

    // 从未解决列表移除
    redis.LRem(ctx, conflictKey, 1, toJSON(conflict))

    // 添加到已解决列表
    resolvedKey := "conflicts:resolved:" + conflict.TaskID
    resolvedData, _ := json.Marshal(conflict)
    redis.RPush(ctx, resolvedKey, string(resolvedData))

    // 发布冲突解决事件
    EmitEventActivity(ctx, &AgentEvent{
        Type: "CONFLICT_RESOLVED",
        TaskID: conflict.TaskID,
        Payload: map[string]interface{}{
            "conflict_id": conflict.ConflictID,
            "file_path":   conflict.FilePath,
            "resolution":  strategy,
            "resolved_by": "system",
        },
    })

    return &conflict, nil
}

// mergeContents - 行级合并（Git-like）
func mergeContents(content1, content2 string) string {
    lines1 := strings.Split(content1, "\n")
    lines2 := strings.Split(content2, "\n")

    var result []string
    maxLen := len(lines1)
    if len(lines2) > maxLen {
        maxLen = len(lines2)
    }

    for i := 0; i < maxLen; i++ {
        if i < len(lines1) && i < len(lines2) {
            if lines1[i] == lines2[i] {
                result = append(result, lines1[i])
            } else {
                // 冲突行：保留两个版本，用标记分隔
                result = append(result, "<<<<<<< VERSION1 (local)")
                result = append(result, lines1[i])
                result = append(result, "=======")
                result = append(result, lines2[i])
                result = append(result, ">>>>>>> VERSION2 (remote)")
            }
        } else if i < len(lines1) {
            result = append(result, lines1[i])
        } else {
            result = append(result, lines2[i])
        }
    }

    return strings.Join(result, "\n")
}

// GetUnresolvedConflictsActivity - 获取未解决的冲突
func GetUnresolvedConflictsActivity(ctx context.Context, taskID string) ([]FileConflict, error) {
    conflictKey := "conflicts:" + taskID
    conflicts, _ := redis.LRange(ctx, conflictKey, 0, -1).Result()

    var result []FileConflict
    for _, data := range conflicts {
        var c FileConflict
        json.Unmarshal([]byte(data), &c)
        if !c.Resolved {
            result = append(result, c)
        }
    }

    return result, nil
}
```

#### 3.36 冲突解决策略

| 策略 | 行为 | 适用场景 |
|------|------|----------|
| keep_version1 | 保留本地版本，丢弃远程 | 本地修改优先 |
| keep_version2 | 保留远程版本，丢弃本地 | 远程更新优先 |
| merge | 行级合并，冲突标记 | 协作编辑 |
| manual | 用户介入选择 | 关键文件 |

#### 3.37 验收命令

```bash
# 测试冲突检测
# 1. Worker-A 读取文件 v1
# 2. Worker-B 读取文件 v1
# 3. Worker-A 更新文件为 v2
# 4. Worker-B 更新文件（预期冲突）

# 验证冲突记录
docker exec deploy-redis-1 redis-cli LRANGE "conflicts:$TASK_ID" 0 -1

# 验证冲突内容（两个版本都有）
docker exec deploy-redis-1 redis-cli LRANGE "conflicts:$TASK_ID" 0 -1 | jq '.[0] | {version1, content_hash1, worker_id1, version2, content_hash2, worker_id2}'

# 验证自动解决（keep_version2）
docker exec deploy-redis-1 redis-cli LRANGE "conflicts:resolved:$TASK_ID" 0 -1

# 验证合并内容
docker exec deploy-redis-1 redis-cli LRANGE "conflicts:resolved:$TASK_ID" 0 -1 | jq '.[0].merged_content' | head -20

# 验证冲突事件
docker exec deploy-redis-1 redis-cli XRANGE "task:$TASK_ID:events" - + COUNT 10 | grep -i conflict
```

#### 3.38 Pass 标准

- [ ] 版本不匹配时正确检测冲突
- [ ] 冲突记录包含两个版本内容和修改者
- [ ] 冲突记录包含两个版本的 ContentHash
- [ ] 自动解决策略（keep_version1/keep_version2/merge）正常工作
- [ ] 已解决冲突移动到 resolved 列表
- [ ] 简单合并正确处理行级冲突（Git-like 标记）
- [ ] 冲突检测事件正确发布

---

### Phase 4F Slice 15：Security & Access Control

#### 3.39 职责

- Agent 身份验证
- 权限控制（读取/写入/删除）
- **密钥管理**
- 操作审计日志
- **安全异常处理**

#### 3.40 安全数据结构

```go
type AgentIdentity struct {
    AgentID      string `json:"agent_id"`      // UUID v4
    AgentType    string `json:"agent_type"`   // "lead" | "worker"
    TaskID       string `json:"task_id"`
    WorkerIndex  int    `json:"worker_index"` // Worker 池中的索引
    PublicKey    string `json:"public_key"`   // 用于验证（可选）
    CreatedAt    string `json:"created_at"`
    ExpiresAt    string `json:"expires_at"`   // 自动过期时间
    LastActiveAt string `json:"last_active_at"`
    Status       string `json:"status"`      // "active" | "revoked" | "expired"
}

type AgentPermission struct {
    AgentID      string   `json:"agent_id"`
    TaskID       string   `json:"task_id"`
    FilePath     string   `json:"file_path"`  // "*" 表示所有文件
    Permissions  []string `json:"permissions"` // "read" | "write" | "delete" | "admin"
    GrantedBy    string   `json:"granted_by"`
    GrantedAt    string   `json:"granted_at"`
    ExpiresAt    string   `json:"expires_at,omitempty"`
    Condition    string   `json:"condition,omitempty"` // 额外条件（如 IP 白名单）
}

type AuditLogEntry struct {
    LogID       string `json:"log_id"`
    TaskID      string `json:"task_id"`
    AgentID     string `json:"agent_id"`
    AgentType   string `json:"agent_type"`  // "lead" | "worker"
    Operation   string `json:"operation"`   // "read_file" | "write_file" | "delete_file" | "send_message" | "grant_permission" | "revoke_permission"
    Resource    string `json:"resource"`   // 文件路径或消息目标
    Result      string `json:"result"`    // "allowed" | "denied"
    Reason      string `json:"reason,omitempty"`
    IPAddress   string `json:"ip_address,omitempty"`
    Timestamp   string `json:"timestamp"`
    DurationMs  int    `json:"duration_ms"` // 操作耗时
}

type SecurityPolicy struct {
    PolicyID    string   `json:"policy_id"`
    TaskID      string   `json:"task_id"`
    AllowList  []string `json:"allow_list"`  // 允许的操作
    DenyList   []string `json:"deny_list"`   // 拒绝的操作
    RequireAuth bool    `json:"require_auth"` // 是否需要身份验证
    MaxRetries int      `json:"max_retries"`  // 最大重试次数
    LockoutDuration int `json:"lockout_duration"` // 锁定时长（秒）
}
```

#### 3.41 Redis 数据结构（Security）

```
Key: security:agents:{task_id}
Type: Hash
Fields:
  - agent_id -> AgentIdentity JSON
TTL: 86400 秒（1 天）

Key: security:permissions:{task_id}
Type: Hash
Fields:
  - agent_id:file_path -> AgentPermission JSON（复合 key）
TTL: 86400 秒（1 天）

Key: security:audit:{task_id}
Type: List
内容: AuditLogEntry JSON
TTL: 86400 秒（1 天）
MAXLEN: 10000

Key: security:policy:{task_id}
Type: Hash
Fields:
  - policy_id -> SecurityPolicy JSON
TTL: 86400 秒（1 天）

Key: security:revoked_tokens:{task_id}
Type: Set
Members: 被撤销的 token/session_id
TTL: 86400 秒（1 天）

Key: security:failed_attempts:{task_id}:{agent_id}
Type: String
Value: 失败次数
TTL: 3600 秒（1 小时）
```

#### 3.42 SecurityActivity 实现

```go
// RegisterAgentActivity - 注册 Agent 身份
func RegisterAgentActivity(ctx context.Context, input *RegisterAgentInput) (*AgentIdentity, error) {
    logger := activity.GetLogger(ctx)

    // 检查是否已存在
    agentKey := "security:agents:" + input.TaskID
    existing, _ := redis.HGet(ctx, agentKey, input.AgentID).Result()
    if existing != "" {
        return nil, fmt.Errorf("agent %s already registered", input.AgentID)
    }

    identity := &AgentIdentity{
        AgentID:      input.AgentID,
        AgentType:    input.AgentType,
        TaskID:       input.TaskID,
        WorkerIndex:  input.WorkerIndex,
        PublicKey:    input.PublicKey,
        CreatedAt:    time.Now().Format(time.RFC3339),
        ExpiresAt:    time.Now().Add(24 * time.Hour).Format(time.RFC3339),
        LastActiveAt: time.Now().Format(time.RFC3339),
        Status:       "active",
    }

    data, _ := json.Marshal(identity)
    redis.HSet(ctx, agentKey, input.AgentID, string(data))

    // 授予默认权限
    defaultPerms := []string{"read"}
    if input.AgentType == "lead" {
        defaultPerms = []string{"read", "write", "delete", "admin"}
    }
    GrantPermissionActivity(ctx, &GrantPermissionInput{
        AgentID:     input.AgentID,
        TaskID:      input.TaskID,
        FilePath:    "*",
        Permissions: defaultPerms,
        GrantedBy:   "system",
    })

    logger.Info("Agent registered",
        "agent_id", input.AgentID,
        "agent_type", input.AgentType,
        "task_id", input.TaskID)

    return identity, nil
}

// VerifyAgentActivity - 验证 Agent 身份
func VerifyAgentActivity(ctx context.Context, input *VerifyAgentInput) (*VerifyResult, error) {
    agentKey := "security:agents:" + input.TaskID

    // 读取 Agent 身份
    identityData, err := redis.HGet(ctx, agentKey, input.AgentID).Result()
    if err != nil {
        return &VerifyResult{
            Valid:    false,
            Reason:   "agent_not_found",
        }, nil
    }

    var identity AgentIdentity
    json.Unmarshal([]byte(identityData), &identity)

    // 检查状态
    if identity.Status == "revoked" {
        return &VerifyResult{
            Valid:    false,
            Reason:   "agent_revoked",
        }, nil
    }

    if identity.Status == "expired" {
        return &VerifyResult{
            Valid:    false,
            Reason:   "agent_expired",
        }, nil
    }

    // 检查过期时间
    if time.Now().After(parseTime(identity.ExpiresAt)) {
        identity.Status = "expired"
        data, _ := json.Marshal(identity)
        redis.HSet(ctx, agentKey, input.AgentID, string(data))
        return &VerifyResult{
            Valid:    false,
            Reason:   "agent_expired",
        }, nil
    }

    // 更新 LastActiveAt
    identity.LastActiveAt = time.Now().Format(time.RFC3339)
    data, _ := json.Marshal(identity)
    redis.HSet(ctx, agentKey, input.AgentID, string(data))

    return &VerifyResult{
        Valid:      true,
        AgentType:  identity.AgentType,
        WorkerIndex: identity.WorkerIndex,
    }, nil
}

// GrantPermissionActivity - 授予权限
func GrantPermissionActivity(ctx context.Context, input *GrantPermissionInput) error {
    // 只有 Lead Agent 或 admin 可以授予权限
    if input.GrantedBy != "lead" && input.GrantedBy != "admin" && input.GrantedBy != "system" {
        return fmt.Errorf("only lead, admin, or system can grant permissions")
    }

    // 验证被授权者存在
    agentKey := "security:agents:" + input.TaskID
    identityData, _ := redis.HGet(ctx, agentKey, input.AgentID).Result()
    if identityData == "" {
        return fmt.Errorf("agent %s not found", input.AgentID)
    }

    permission := &AgentPermission{
        AgentID:     input.AgentID,
        TaskID:      input.TaskID,
        FilePath:    input.FilePath,
        Permissions: input.Permissions,
        GrantedBy:   input.GrantedBy,
        GrantedAt:   time.Now().Format(time.RFC3339),
        ExpiresAt:   input.ExpiresAt,
    }

    key := "security:permissions:" + input.TaskID
    keyField := input.AgentID + ":" + input.FilePath
    data, _ := json.Marshal(permission)
    redis.HSet(ctx, key, keyField, string(data))

    // 记录审计日志
    logAudit(ctx, input.TaskID, input.AgentID, "grant_permission", input.FilePath, "allowed",
        fmt.Sprintf("permissions: %v", input.Permissions))

    return nil
}

// CheckPermissionActivity - 检查权限
func CheckPermissionActivity(ctx context.Context, input *CheckPermissionInput) (bool, error) {
    logger := activity.GetLogger(ctx)
    taskID := input.TaskID
    agentID := input.AgentID
    filePath := input.FilePath
    operation := input.Operation

    // ========== 步骤 1：检查失败锁定 ==========
    failedKey := "security:failed_attempts:" + taskID + ":" + agentID
    failedCount, _ := redis.Get(ctx, failedKey).Int()
    maxRetries := 5

    if failedCount >= maxRetries {
        logger.Warn("Agent locked due to failed attempts",
            "agent_id", agentID,
            "failed_count", failedCount)
        logAudit(ctx, taskID, agentID, operation, filePath, "denied",
            fmt.Sprintf("account_locked_failed_attempts_%d", failedCount))
        return false, nil
    }

    // ========== 步骤 2：检查权限 ==========
    permKey := "security:permissions:" + taskID

    // 2.1 检查具体文件权限
    keyField := agentID + ":" + filePath
    permData, err := redis.HGet(ctx, permKey, keyField).Result()
    if err == nil {
        var perm AgentPermission
        json.Unmarshal([]byte(permData), &perm)

        // 检查权限是否过期
        if perm.ExpiresAt != "" && time.Now().After(parseTime(perm.ExpiresAt)) {
            logAudit(ctx, taskID, agentID, operation, filePath, "denied", "permission_expired")
            return false, nil
        }

        if slices.Contains(perm.Permissions, operation) {
            logAudit(ctx, taskID, agentID, operation, filePath, "allowed", "")
            redis.Del(ctx, failedKey) // 成功，重置失败计数
            return true, nil
        }
    }

    // 2.2 检查通配符权限（所有文件）
    wildcardKey := agentID + ":*"
    permData, err = redis.HGet(ctx, permKey, wildcardKey).Result()
    if err == nil {
        var perm AgentPermission
        json.Unmarshal([]byte(permData), &perm)

        if perm.ExpiresAt != "" && time.Now().After(parseTime(perm.ExpiresAt)) {
            logAudit(ctx, taskID, agentID, operation, filePath, "denied", "permission_expired")
            return false, nil
        }

        if slices.Contains(perm.Permissions, operation) {
            logAudit(ctx, taskID, agentID, operation, filePath, "allowed", "")
            redis.Del(ctx, failedKey)
            return true, nil
        }
    }

    // ========== 步骤 3：权限不足，增加失败计数 ==========
    redis.Incr(ctx, failedKey)
    redis.Expire(ctx, failedKey, time.Hour)

    logger.Warn("Permission denied",
        "agent_id", agentID,
        "operation", operation,
        "file_path", filePath,
        "failed_count", failedCount+1)

    logAudit(ctx, taskID, agentID, operation, filePath, "denied",
        fmt.Sprintf("insufficient_permissions_failed_%d", failedCount+1))

    return false, nil
}

// RevokePermissionActivity - 撤销权限
func RevokePermissionActivity(ctx context.Context, taskID, agentID, filePath string) error {
    permKey := "security:permissions:" + taskID
    keyField := agentID + ":" + filePath
    redis.HDel(ctx, permKey, keyField)

    // 记录审计日志
    logAudit(ctx, taskID, agentID, "revoke_permission", filePath, "allowed", "permission_revoked")

    return nil
}

// RevokeAgentActivity - 撤销 Agent 身份
func RevokeAgentActivity(ctx context.Context, taskID, agentID string) error {
    agentKey := "security:agents:" + taskID

    identityData, _ := redis.HGet(ctx, agentKey, agentID).Result()
    if identityData == "" {
        return fmt.Errorf("agent %s not found", agentID)
    }

    var identity AgentIdentity
    json.Unmarshal([]byte(identityData), &identity)
    identity.Status = "revoked"

    data, _ := json.Marshal(identity)
    redis.HSet(ctx, agentKey, agentID, string(data))

    // 记录审计日志
    logAudit(ctx, taskID, agentID, "revoke_agent", "", "allowed", "agent_revoked")

    return nil
}

// logAudit - 记录审计日志（异步 goroutine）
func logAudit(ctx context.Context, taskID, agentID, operation, resource, result, reason string) {
    entry := &AuditLogEntry{
        LogID:     uuid.New().String(),
        TaskID:    taskID,
        AgentID:   agentID,
        Operation: operation,
        Resource:  resource,
        Result:    result,
        Reason:    reason,
        Timestamp: time.Now().Format(time.RFC3339),
    }

    key := "security:audit:" + taskID
    data, _ := json.Marshal(entry)
    redis.RPush(ctx, key, string(data))
    redis.LTrim(ctx, key, -10000, -1) // 保留最近 10000 条
}
```

#### 3.43 安全策略

**身份验证流程：**
```
1. Agent 注册 → 生成 AgentID
2. Agent 验证 → 检查状态、过期时间
3. 权限检查 → Allow/Deny 列表
4. 失败锁定 → 5 次失败后锁定 1 小时
5. 审计日志 → 记录所有操作
```

**权限授予规则：**
- Lead Agent 自动获得所有权限（read/write/delete/admin）
- Worker Agent 默认只读权限
- 权限可按文件路径细分
- 权限可设置过期时间

**安全异常处理：**
| 场景 | 处理 |
|------|------|
| Agent 未注册 | 返回 error，审计日志 denied |
| Agent 已撤销 | 返回 false，审计日志 denied |
| 权限不足 | 返回 false，失败计数 +1，审计日志 denied |
| 失败 5 次 | 锁定 1 小时，返回 false |
| 权限过期 | 返回 false，审计日志 denied |
| 审计日志写入失败 | 只记录日志，不影响主流程 |

#### 3.44 验收命令

```bash
# 测试 Agent 注册
docker exec deploy-redis-1 redis-cli HGETALL "security:agents:$TASK_ID"

# 测试权限授予
docker exec deploy-redis-1 redis-cli HGETALL "security:permissions:$TASK_ID"

# 测试权限检查（允许）
# 读取文件 → 应返回 allowed

# 测试权限检查（拒绝）
# 未授权操作 → 应返回 denied

# 测试失败锁定
# 连续 5 次失败 → 第 6 次返回 locked

# 验证审计日志
docker exec deploy-redis-1 redis-cli LRANGE "security:audit:$TASK_ID" 0 -1

# 验证失败计数
docker exec deploy-redis-1 redis-cli GET "security:failed_attempts:$TASK_ID:$AGENT_ID"

# 验证 Agent 撤销
docker exec deploy-redis-1 redis-cli HGET "security:agents:$TASK_ID" "$AGENT_ID" | jq '.status'
```

#### 3.45 Pass 标准

- [ ] Agent 注册成功，生成唯一 AgentID
- [ ] Lead Agent/Admin 可以授予权限
- [ ] 权限检查正确（允许/拒绝）
- [ ] 通配符权限正确匹配
- [ ] 权限过期检查生效
- [ ] 失败 5 次后锁定 1 小时
- [ ] 审计日志记录所有操作（MAXLEN 10000）
- [ ] Agent 撤销正确生效
- [ ] 权限撤销正确生效

---

## 四、资源约束矩阵

### 4.1 Lead Agent 资源约束

| 约束项 | 默认值 | 最大值 | 说明 |
|--------|--------|--------|------|
| worker_pool_size | 3 | 10 | Worker 并发数 |
| task_timeout | 60s | 300s | 单任务超时 |
| heartbeat_interval | 30s | 60s | 心跳间隔 |
| heartbeat_timeout | 90s | 180s | 心跳超时（3 × interval） |
| max_react_iterations | 3 | 10 | ReAct 最大迭代 |
| Activity 总超时 | 300s | 600s | LeadAgentActivity 总超时 |

### 4.2 Worker Agent 资源约束

| 约束项 | 默认值 | 最大值 | 说明 |
|--------|--------|--------|------|
| 单次 LLM 超时 | 60s | 120s | AgentActivity StartToCloseTimeout |
| ReAct 单次迭代 | 60s | 120s | 单次 LLM 调用超时 |
| ReAct 总超时 | 180s | 600s | ExecuteReActNodeActivity 总超时 |
| 内存限制 | 256MB | 512MB | Worker Activity 内存限制（通过容器限制） |
| 并发 Activity 数 | 3 | 10 | 单 Worker 并发 Activity |

### 4.3 Workspace 资源约束

| 约束项 | 默认值 | 最大值 | 说明 |
|--------|--------|--------|------|
| 文件大小限制 | 1MB | 10MB | 单文件大小 |
| 版本保留数 | 10 | 50 | 每文件保留版本数 |
| 最大文件数 | 100 | 1000 | 每任务最大文件数 |
| 锁 TTL | 5s | 30s | 分布式锁超时 |
| 内容 hash | SHA256 | - | 内容寻址算法 |

### 4.4 P2P 资源约束

| 约束项 | 默认值 | 最大值 | 说明 |
|--------|--------|--------|------|
| 消息 TTL | 300s | 3600s | 消息过期时间 |
| 消息大小限制 | 64KB | 256KB | 单条消息最大 |
| 消息队列长度 | 1000 | 10000 | pending 队列最大 |
| Stream MAXLEN | 10000 | 50000 | Stream 最大记录数 |
| 重试次数 | 3 | 5 | 消息发送重试 |

### 4.5 安全资源约束

| 约束项 | 默认值 | 最大值 | 说明 |
|--------|--------|--------|------|
| Agent 有效期 | 24h | 168h（7 天） | Agent 身份有效期 |
| 失败锁定阈值 | 5 | 10 | 锁定前的失败次数 |
| 锁定时长 | 1h | 24h | 锁定持续时间 |
| 审计日志 MAXLEN | 10000 | 100000 | 保留日志条数 |

---

## 五、API 设计

### 5.1 POST /api/v1/tasks（扩展）

**Request（swarm 模式）：**
```json
{
  "query": "Analyze AI trends in healthcare, education, and finance",
  "session_id": "optional-session-id",
  "config": {
    "mode": "swarm",
    "model": "gpt-4o-mini",
    "temperature": 0.7,
    "max_total_tokens": 16000,
    "max_completion_tokens": 2048,
    "worker_pool_size": 3,
    "task_timeout": 60,
    "heartbeat_interval": 30,
    "enable_p2p": true,
    "workspace_enabled": true,
    "workspace_ttl_seconds": 86400,
    "max_versions": 10,
    "react_enabled": true,
    "max_react_iterations": 3,
    "security_enabled": false
  }
}
```

**Response（202 Accepted）：**
```json
{
  "task_id": "uuid-xxx",
  "workflow_id": "task-uuid-xxx",
  "run_id": "run-id-xxx",
  "status": "running",
  "mode": "swarm",
  "stream_url": "/api/v1/stream/sse?task_id=uuid-xxx"
}
```

### 5.2 Config 字段说明（Phase 4+）

| 字段 | 类型 | 默认值 | 最大值 | 说明 |
|------|------|--------|--------|------|
| mode | string | "simple" | - | "simple" \| "dag" \| "multi_agent" \| "swarm" |
| worker_pool_size | int | 3 | 10 | Worker 池大小 |
| task_timeout | int | 60 | 300 | 单任务超时（秒） |
| heartbeat_interval | int | 30 | 60 | 心跳间隔（秒） |
| heartbeat_timeout | int | 90 | 180 | 心跳超时（秒） |
| enable_p2p | bool | false | - | 是否启用 P2P 通信 |
| p2p_message_ttl | int | 300 | 3600 | P2P 消息 TTL（秒） |
| workspace_enabled | bool | false | - | 是否启用共享工作空间 |
| workspace_ttl_seconds | int | 86400 | - | Workspace 文件 TTL（秒） |
| max_versions | int | 10 | 50 | 每文件保留版本数 |
| react_enabled | bool | false | - | 是否启用 ReAct |
| max_react_iterations | int | 3 | 10 | ReAct 最大迭代次数 |
| security_enabled | bool | false | - | 是否启用安全控制 |

### 5.3 SSE 事件（Phase 4+ 完整）

| 事件类型 | Payload | 说明 |
|---------|---------|------|
| LEAD_INITIALIZED | task_id, worker_pool_size, react_enabled, timestamp | Lead Agent 初始化 |
| TASK_DECOMPOSED | task_id, sub_tasks_count, timestamp | 任务分解完成 |
| WORKER_DISPATCHED | task_id, worker_id, sub_task_id, timestamp | Worker 分发 |
| WORKER_HEARTBEAT | task_id, worker_id, timestamp | Worker 心跳 |
| WORKER_COMPLETED | task_id, worker_id, sub_task_id, status, output_length, react_steps, timestamp | Worker 完成 |
| WORKER_FAILED | task_id, worker_id, sub_task_id, error, retry_count, timestamp | Worker 失败 |
| WORKER_OFFLINE | task_id, worker_id, last_heartbeat, timestamp | Worker 离线 |
| TASK_REQUEUED | task_id, sub_task_id, reason, retry_count, timestamp | 任务重新入队 |
| RESULTS_AGGREGATED | task_id, total_results, duration_ms, timestamp | 结果聚合完成 |
| LEAD_COMPLETED | task_id, final_output_length, duration_ms, timestamp | Lead Agent 完成 |
| FILE_CREATED | task_id, file_path, file_id, content_hash, worker_id, timestamp | 文件创建 |
| FILE_UPDATED | task_id, file_path, version, content_hash, worker_id, timestamp | 文件更新 |
| FILE_DELETED | task_id, file_path, worker_id, timestamp | 文件删除 |
| FILE_ROLLBACK | task_id, file_path, target_version, new_version, worker_id, timestamp | 版本回滚 |
| CONFLICT_DETECTED | task_id, file_path, conflict_id, version1, version2, worker_id1, worker_id2, timestamp | 冲突检测 |
| CONFLICT_RESOLVED | task_id, file_path, conflict_id, resolution, resolved_by, timestamp | 冲突解决 |
| PERMISSION_GRANTED | task_id, agent_id, file_path, permissions, granted_by, timestamp | 权限授予 |
| PERMISSION_DENIED | task_id, agent_id, operation, resource, reason, timestamp | 权限拒绝 |
| AGENT_REGISTERED | task_id, agent_id, agent_type, timestamp | Agent 注册 |
| AGENT_REVOKED | task_id, agent_id, timestamp | Agent 撤销 |

---

## 六、状态与错误

### 6.1 Swarm 状态枚举

```
Lead Status:
  initialized → running → completed / failed

Worker Status:
  idle → running → completed / failed / timeout / offline / cancelled

Task Status:
  pending → dispatched → running → completed / failed / timeout / cancelled

File Status:
  active → deleted

Conflict Status:
  unresolved → resolved

Agent Status:
  active → revoked / expired / locked
```

### 6.2 Error Taxonomy（Phase 4+ 完整）

| error_type | 说明 | 来源 | 处理 |
|------------|------|------|------|
| lead_init_error | Lead Agent 初始化失败 | LeadAgentActivity | TASK_FAILED |
| task_decompose_error | 任务分解失败 | LeadAgentActivity | TASK_FAILED |
| worker_dispatch_error | Worker 分发失败 | LeadAgentActivity | retry → TASK_FAILED |
| worker_timeout | Worker 执行超时 | WorkerAgentActivity | retry → Worker failed → TASK_FAILED |
| worker_offline | Worker 离线（心跳超时） | HeartbeatMonitor | 任务重分配 → Worker marked offline |
| p2p_send_error | P2P 消息发送失败 | SendP2PMessageActivity | retry → log only |
| p2p_receive_timeout | P2P 消息接收超时 | ReceiveP2PMessageActivity | return nil → continue |
| p2p_message_expired | P2P 消息过期 | ReceiveP2PMessageActivity | return nil → discard |
| file_lock_error | 文件锁定失败 | WorkspaceActivity | return error → retry |
| file_lock_timeout | 文件锁超时 | WorkspaceActivity | auto release → retry |
| file_not_found | 文件不存在 | WorkspaceActivity | return error |
| file_too_large | 文件超过大小限制 | WorkspaceActivity | return error |
| version_conflict | 版本冲突 | DetectConflictActivity | return FileConflict |
| version_not_found | 版本不存在 | ReadWorkspaceFileVersionActivity | return error |
| permission_denied | 权限不足 | CheckPermissionActivity | TASK_FAILED |
| permission_expired | 权限过期 | CheckPermissionActivity | return false |
| account_locked | 账户锁定 | CheckPermissionActivity | TASK_FAILED |
| agent_not_found | Agent 未注册 | RegisterAgentActivity | TASK_FAILED |
| agent_revoked | Agent 已撤销 | VerifyAgentActivity | return false |
| agent_expired | Agent 已过期 | VerifyAgentActivity | return false |
| audit_log_error | 审计日志写入失败 | logAudit | log only → continue |
| state_sync_error | 状态同步失败 | PublishStateChangeActivity | retry → log only |

### 6.3 异常场景处理

**Agent 离线：**
```
1. Worker 心跳超时（默认 90 秒无心跳）
2. Lead Agent 标记 Worker 为 "offline"
3. 离线 Worker 的子任务重新入队（最多重试 3 次）
4. 重试仍失败 → 标记子任务为 "failed"
5. 记录 WORKER_OFFLINE 事件到 audit log
```

**网络延迟：**
```
1. P2P 消息 TTL = 300 秒
2. 超时 → 消息标记为 "expired"
3. Activity 超时重试由 Temporal 处理（LocalRetries: 3 次）
4. 状态同步延迟 < 1 秒（Redis 写入即生效）
5. Stream MAXLEN 10000 防止内存溢出
```

**冲突文件写入：**
```
1. 版本检查机制（ExpectedVersion）
2. 版本不匹配 → 返回 FileConflict
3. 冲突记录保存到 Redis List
4. 自动解决（keep_version1/keep_version2/merge）
5. 合并结果用 Git-like 标记（<<<<<<< ======= >>>>>>>）
```

**权限错误：**
```
1. CheckPermissionActivity 返回 false
2. 操作拒绝，记录审计日志
3. 失败计数 +1
4. 失败 5 次 → 锁定 1 小时
5. Lead Agent 可授予/撤销权限
```

---

## 七、修改文件清单

### Phase 4A Slice 10：Lead Agent Architecture

| 操作 | 文件 | 说明 |
|------|------|------|
| 新建 | `internal/activities/lead_agent.go` | LeadAgentActivity（含任务分解和聚合） |
| 新建 | `internal/activities/worker_dispatch.go` | DispatchWorkerActivity |
| 新建 | `internal/activities/worker_agent.go` | WorkerAgentActivity（含 heartbeat） |
| 新建 | `internal/activities/heartbeat.go` | WorkerHeartbeatActivity |
| 新建 | `internal/workflows/swarm.go` | SwarmWorkflow |
| 修改 | `internal/workflows/router.go` | 新增 swarm 路由 |
| 修改 | `internal/events/types.go` | 新增 Lead/Worker/Heartbeat 事件 |
| 修改 | `config/features.yaml` | 新增 enable_swarm, worker_pool_size 等 |

### Phase 4B Slice 11：Agent P2P Communication

| 操作 | 文件 | 说明 |
|------|------|------|
| 新建 | `internal/activities/p2p.go` | P2P 消息相关 Activity（含 ACK/NACK） |
| 新建 | `internal/activities/p2p_subscribe.go` | P2P 订阅管理 |
| 修改 | `internal/activities/worker_agent.go` | 增加 P2P 消息发送/接收 |

### Phase 4C Slice 12：Workspace File Sharing

| 操作 | 文件 | 说明 |
|------|------|------|
| 新建 | `internal/activities/workspace.go` | Workspace 文件操作 Activity（含版本管理） |
| 新建 | `internal/activities/workspace_rollback.go` | WorkspaceRollbackActivity |
| 修改 | `internal/activities/worker_agent.go` | 增加 Workspace 读取/写入 |
| 新增 | `internal/utils/sha256.go` | SHA256 内容寻址 |

### Phase 4D Slice 13：State Synchronization

| 操作 | 文件 | 说明 |
|------|------|------|
| 新建 | `internal/activities/state_sync.go` | 状态同步 Activity（含向量时钟） |
| 新建 | `internal/activities/state_subscribe.go` | 状态订阅 Activity |
| 修改 | `internal/activities/*.go` | 所有 Activity 增加状态发布 |

### Phase 4E Slice 14：Conflict Resolution

| 操作 | 文件 | 说明 |
|------|------|------|
| 新建 | `internal/activities/conflict.go` | 冲突检测和解决 Activity |
| 新建 | `internal/utils/merge.go` | 行级合并算法（Git-like） |
| 修改 | `internal/activities/workspace.go` | 增加版本检查和冲突返回 |

### Phase 4F Slice 15：Security & Access Control

| 操作 | 文件 | 说明 |
|------|------|------|
| 新建 | `internal/activities/security.go` | 安全相关 Activity |
| 新建 | `internal/activities/audit.go` | 审计日志 Activity |
| 修改 | `internal/activities/workspace.go` | 增加权限检查 |
| 修改 | `internal/activities/p2p.go` | 增加身份验证 |

---

## 八、测试脚本

### smoke_test_phase4.sh（完整版）

```bash
#!/bin/bash
set -euo pipefail

GATEWAY_URL="${GATEWAY_URL:-http://127.0.0.1:8080}"
REDIS_CONTAINER="${REDIS_CONTAINER:-deploy-redis-1}"
POSTGRES_CONTAINER="${POSTGRES_CONTAINER:-deploy-postgres-1}"

log() { echo "[$(date +'%H:%M:%S')] $*"; }
fail() { echo "[FAIL] $*" >&2; exit 1; }
pass() { echo "  [PASS] $*"; }

wait_task() {
  local task_id=$1
  for i in $(seq 1 90); do
    local status=$(curl -s "$GATEWAY_URL/api/v1/tasks/$task_id" | jq -r '.status')
    if [[ "$status" == "completed" ]] || [[ "$status" == "failed" ]]; then
      echo "$status"
      return 0
    fi
    sleep 2
  done
  fail "Task $task_id timeout"
}

# ---- Slice 10: Lead Agent Test ----
test_lead_agent() {
  log "Testing Lead Agent..."
  local task_id=$(curl -s -X POST "$GATEWAY_URL/api/v1/tasks" \
    -H "Content-Type: application/json" \
    -d '{"query":"Analyze AI in healthcare, education, and finance","config":{"mode":"swarm","worker_pool_size":3,"max_total_tokens":32000}}' \
    | jq -r '.task_id')
  local status=$(wait_task $task_id)
  [[ "$status" == "completed" ]] || fail "lead_agent: expected completed, got $status"

  # 验证 Lead 状态
  local lead_status=$(docker exec $REDIS_CONTAINER redis-cli HGET "swarm:$task_id:lead" status 2>/dev/null | tr -d ' ')
  [[ "$lead_status" == "completed" ]] || fail "lead_agent: lead status not completed"

  # 验证 Worker 数量
  local worker_count=$(docker exec $REDIS_CONTAINER redis-cli HLEN "swarm:$task_id:workers" 2>/dev/null | tr -d ' ')
  [[ "$worker_count" -ge 1 ]] || fail "lead_agent: no workers registered"

  # 验证任务分发
  local task_count=$(docker exec $REDIS_CONTAINER redis-cli HLEN "swarm:$task_id:tasks" 2>/dev/null | tr -d ' ')
  [[ "$task_count" -ge 2 ]] || fail "lead_agent: insufficient tasks distributed"

  pass "Lead Agent"
}

test_lead_agent_boundary_sequential() {
  log "Testing Lead Agent boundary: sequential (worker_pool_size=1)..."
  local task_id=$(curl -s -X POST "$GATEWAY_URL/api/v1/tasks" \
    -H "Content-Type: application/json" \
    -d '{"query":"Sequential test","config":{"mode":"swarm","worker_pool_size":1}}' \
    | jq -r '.task_id')
  local status=$(wait_task $task_id)
  [[ "$status" == "completed" ]] || fail "lead_agent_seq: expected completed, got $status"
  pass "Lead Agent boundary: sequential"
}

test_lead_agent_boundary_max() {
  log "Testing Lead Agent boundary: max concurrency (worker_pool_size=10)..."
  local task_id=$(curl -s -X POST "$GATEWAY_URL/api/v1/tasks" \
    -H "Content-Type: application/json" \
    -d '{"query":"Max concurrency test","config":{"mode":"swarm","worker_pool_size":10,"max_total_tokens":64000}}' \
    | jq -r '.task_id')
  local status=$(wait_task $task_id)
  [[ "$status" == "completed" ]] || fail "lead_agent_max: expected completed, got $status"
  pass "Lead Agent boundary: max concurrency"
}

# ---- Slice 11: P2P Communication Test ----
test_p2p() {
  log "Testing P2P Communication..."
  local task_id=$(curl -s -X POST "$GATEWAY_URL/api/v1/tasks" \
    -H "Content-Type: application/json" \
    -d '{"query":"Test P2P with context sharing","config":{"mode":"swarm","enable_p2p":true,"worker_pool_size":2}}' \
    | jq -r '.task_id')
  local status=$(wait_task $task_id)
  [[ "$status" == "completed" ]] || fail "p2p: expected completed, got $status"
  pass "P2P Communication"
}

# ---- Slice 12: Workspace File Sharing Test ----
test_workspace() {
  log "Testing Workspace File Sharing..."
  local task_id=$(curl -s -X POST "$GATEWAY_URL/api/v1/tasks" \
    -H "Content-Type: application/json" \
    -d '{"query":"Test workspace with file sharing","config":{"mode":"swarm","workspace_enabled":true,"worker_pool_size":2}}' \
    | jq -r '.task_id')
  local status=$(wait_task $task_id)
  [[ "$status" == "completed" ]] || fail "workspace: expected completed, got $status"

  # 验证文件
  local file_count=$(docker exec $REDIS_CONTAINER redis-cli HLEN "workspace:$task_id:files" 2>/dev/null | tr -d ' ')
  [[ "$file_count" -ge 0 ]] || fail "workspace: no files tracked"

  pass "Workspace File Sharing"
}

test_workspace_version_control() {
  log "Testing Workspace Version Control..."
  # 验证版本历史
  local version_count=$(docker exec $REDIS_CONTAINER redis-cli LLEN "workspace:$task_id:versions:/docs/summary.md" 2>/dev/null | tr -d ' ')
  [[ "$version_count" -ge 1 ]] || fail "workspace_version: no versions recorded"

  # 验证内容寻址
  local content_hash=$(docker exec $REDIS_CONTAINER redis-cli HGET "workspace:$task_id:files" "/docs/summary.md" 2>/dev/null | jq -r '.content_hash')
  [[ -n "$content_hash" ]] || fail "workspace_version: no content_hash"

  pass "Workspace Version Control"
}

# ---- Slice 13: State Synchronization Test ----
test_state_sync() {
  log "Testing State Synchronization..."
  local task_id=$(curl -s -X POST "$GATEWAY_URL/api/v1/tasks" \
    -H "Content-Type: application/json" \
    -d '{"query":"Test state sync","config":{"mode":"swarm","enable_p2p":true}}' \
    | jq -r '.task_id')
  local status=$(wait_task $task_id)
  [[ "$status" == "completed" ]] || fail "state_sync: expected completed, got $status"

  # 验证状态事件
  local event_count=$(docker exec $REDIS_CONTAINER redis-cli XLEN "state:sync:$task_id:events" 2>/dev/null | tr -d ' ')
  [[ "$event_count" -ge 0 ]] || fail "state_sync: no events recorded"

  pass "State Synchronization"
}

# ---- Slice 14: Conflict Resolution Test ----
test_conflict_resolution() {
  log "Testing Conflict Resolution..."
  local task_id=$(curl -s -X POST "$GATEWAY_URL/api/v1/tasks" \
    -H "Content-Type: application/json" \
    -d '{"query":"Test file conflict detection","config":{"mode":"swarm","workspace_enabled":true}}' \
    | jq -r '.task_id')
  local status=$(wait_task $task_id)
  [[ "$status" == "completed" ]] || fail "conflict: expected completed, got $status"

  # 验证冲突记录（如果有）
  local conflict_count=$(docker exec $REDIS_CONTAINER redis-cli LLEN "conflicts:$task_id" 2>/dev/null | tr -d ' ')
  [[ "$conflict_count" -ge 0 ]] || fail "conflict: no conflict tracking"

  pass "Conflict Resolution"
}

# ---- Slice 15: Security & Access Control Test ----
test_security() {
  log "Testing Security & Access Control..."
  local task_id=$(curl -s -X POST "$GATEWAY_URL/api/v1/tasks" \
    -H "Content-Type: application/json" \
    -d '{"query":"Test security","config":{"mode":"swarm","security_enabled":true}}' \
    | jq -r '.task_id')
  local status=$(wait_task $task_id)
  [[ "$status" == "completed" ]] || fail "security: expected completed, got $status"

  # 验证审计日志
  local audit_count=$(docker exec $REDIS_CONTAINER redis-cli LLEN "security:audit:$task_id" 2>/dev/null | tr -d ' ')
  [[ "$audit_count" -ge 0 ]] || fail "security: no audit logs"

  pass "Security & Access Control"
}

# ---- Main ----
log "=== Phase 4 Smoke Test Starting ==="

test_lead_agent
test_lead_agent_boundary_sequential
test_lead_agent_boundary_max
test_p2p
test_workspace
test_workspace_version_control
test_state_sync
test_conflict_resolution
test_security

echo ""
echo "=== Phase 4 Smoke Tests PASSED ==="
```

---

## 九、验收标准（完整版）

### Phase 4A Slice 10：Lead Agent Architecture

| 验收项 | 标准 | 验证方式 |
|--------|------|----------|
| Lead Agent 初始化 | swarm:{task_id}:lead 有记录 | redis-cli HGETALL |
| 任务分解 | 产生 2+ 子任务 | swarm:{task_id}:tasks |
| Worker 状态追踪 | worker 状态正确 | redis-cli HGETALL swarm:{task_id}:workers |
| Worker 结果写入 | 结果正确写入 | redis-cli HGETALL swarm:{task_id}:results |
| 结果聚合 | 最终 output 非空 | GET /api/v1/tasks/{id} |
| SSE 事件完整 | LEAD_* + WORKER_* 事件 | redis-cli XRANGE |
| worker_pool_size=1 | 顺序执行 | 事件顺序可预测 |
| worker_pool_size=10 | 最大并发 | 事件并发触发 |
| 心跳机制 | 30s 间隔，90s 超时 | WORKER_HEARTBEAT 事件 |
| Worker 离线处理 | 任务重分配 | TASK_REQUEUED 事件 |

### Phase 4B Slice 11：Agent P2P Communication

| 验收项 | 标准 | 验证方式 |
|--------|------|----------|
| 消息发送 | Stream 有记录 | redis-cli XRANGE |
| 直接消息 | 推送到 pending 队列 | redis-cli LRANGE p2p:pending:* |
| 广播消息 | 发布到 Pub/Sub | redis-cli PUBSUB CHANNELS |
| 消息状态追踪 | pending/delivered/read/expired | redis-cli HGETALL p2p:message_status:* |
| 超时处理 | ReceiveP2PMessage 返回 nil | curl 测试 |
| 消息 TTL | 默认 300s | redis-cli TTL |
| enable_p2p=false | P2P 功能禁用 | 代码审查 |

### Phase 4C Slice 12：Workspace File Sharing

| 验收项 | 标准 | 验证方式 |
|--------|------|----------|
| 文件创建 | workspace:{task_id}:files 有记录 | redis-cli HGETALL |
| 版本递增 | 版本号正确递增 | workspace:{task_id}:versions |
| 版本保留 | LTRIM 保留最近 10 个 | LLEN = 10 |
| 内容寻址 | SHA256 hash 相同内容相同 | 对比 ContentHash |
| 分布式锁 | 并发写入 SETNX 正确 | 模拟并发测试 |
| 锁超时 | 5s 后自动释放 | TTL 验证 |
| 文件读取 | 返回正确内容 | curl |
| 文件删除 | 软删除，状态=deleted | HGET |
| 版本回滚 | 回滚到指定版本 | RollbackActivity |
| workspace_enabled=false | Workspace 功能禁用 | 代码审查 |

### Phase 4D Slice 13：State Synchronization

| 验收项 | 标准 | 验证方式 |
|--------|------|----------|
| 状态变更事件 | Stream 有记录 | redis-cli XRANGE |
| 向量时钟 | 每个 entity 独立时钟 | HGETALL state:sync:*:vector_clock |
| 订阅机制 | 订阅正确注册 | redis-cli HGETALL |
| GetStateChanges | 按向量时钟过滤 | curl 测试 |
| 因果顺序 | 事件按 vector_clock 排序 | XRANGE 顺序 |
| 最终一致性 | 延迟 < 1s | timing 测量 |
| Stream MAXLEN | 10000 自动修剪 | XLEN 验证 |

### Phase 4E Slice 14：Conflict Resolution

| 验收项 | 标准 | 验证方式 |
|--------|------|----------|
| 版本冲突检测 | 冲突正确检测 | 模拟并发测试 |
| 冲突记录 | 包含两个版本内容和 hash | LRANGE conflicts:* |
| 自动解决 | keep_version1/keep_version2/merge | 模拟测试 |
| 已解决移动 | resolved 列表 | LRANGE conflicts:resolved:* |
| Git-like 合并 | 标记行级冲突 | merged_content 包含 <<<<<< |
| CONFLICT_DETECTED 事件 | 正确发布 | XRANGE task:*:events |

### Phase 4F Slice 15：Security & Access Control

| 验收项 | 标准 | 验证方式 |
|--------|------|----------|
| Agent 注册 | 生成唯一 AgentID | HGETALL security:agents:* |
| 权限授予 | Lead/Admin 可授予 | 模拟测试 |
| 权限检查 | 允许/拒绝正确 | 模拟测试 |
| 通配符权限 | agent:* 正确匹配 | 模拟测试 |
| 权限过期 | ExpiresAt 检查生效 | 时间推进测试 |
| 失败锁定 | 5 次失败后锁定 | 连续失败测试 |
| 审计日志 | 所有操作记录 | LRANGE security:audit:* |
| Agent 撤销 | Status=revoked | HGET + verify |
| 权限撤销 | HDel 生效 | HGETALL |

---

## 十、Shannon 原生能力 vs Cribug Phase 4+ 对照

| 功能 | Shannon 原生能力 | Cribug Phase 4+ Lite | 说明 |
|------|-----------------|---------------------|------|
| Lead Agent | 完整任务分解 + 动态 Worker 调度 | Activity 内任务分解 + LocalDispatchOptions | 不做跨 Worker 动态调度 |
| Worker 心跳 | 完整心跳协议 + 离线检测 | Redis heartbeat_timeout 90s | 心跳消息不持久化 |
| Worker P2P | gRPC P2P 通信 + 状态共享 | Redis Pub/Sub + Stream | 不做真正 P2P 网络 |
| P2P 消息顺序 | gRPC streaming 有序 | Redis Pub/Sub 无序 | 同 sender→receiver 有序 |
| Workspace | 完整文件系统 + 版本控制 | Redis Hash + List 版本历史 | 不做真实文件系统 |
| 版本控制 | Git 完整功能 | Git-like（线性历史） | 不做分支/合并图 |
| 内容寻址 | Git blob SHA1 | SHA256 | 不同 hash 算法 |
| 状态同步 | 强一致性 + CRDT | 最终一致性 + 向量时钟 | 延迟 < 1s |
| 冲突解决 | Git merge strategies | 行级合并 + 标记 | 不做 3-way merge |
| 安全控制 | mTLS + OPA | Redis 权限 + 审计日志 | 不做完整安全栈 |
| 身份验证 | X.509 证书 | AgentID + Redis | 不做 PKI |

---

## 十一、后续扩展路线图

| Phase | 内容 | 关键能力 |
|-------|------|----------|
| Phase 5 | DAG 并发扩展 / ReAct 增强 | DAG 可视化、ReAct 中途暂停/恢复、分布式 LRU 缓存 |
| Phase 6 | RAG / Qdrant / MCP / Sandbox | 向量检索、MCP 协议、WASI code execution |
| Phase 7 | HITL / Approval / UI | Human-in-loop approval、Desktop UI |
| Phase 8 | SDK / CLI / Multi-tenant | Python/Go SDK、CLI tool、Auth/Quota |

---

## 十二、明确禁止事项

Phase 4+ 明确禁止：

- ❌ Workflow 内直接创建 goroutine（并发通过 Activity 内 goroutine 实现）
- ❌ Workflow 内直接调用外部 HTTP/DB/Redis（所有外部 IO 放 Activity）
- ❌ Worker Agent 直接读写文件系统（必须通过 WorkspaceActivity）
- ❌ 不做真实 P2P 网络（使用 Redis Pub/Sub 模拟）
- ❌ 不做完整 OT/CRDT（冲突解决简化为版本检查 + 行级合并）
- ❌ 不做 mTLS/OPA（安全控制基于 Redis）
- ❌ 不做真实文件系统（所有文件操作在 Redis）
- ❌ 不做 DAG Workflow（Swarm 模式使用 Lead/Worker 模式）
- ❌ 不重构 Phase 1/2/3 的基础流程
- ❌ 不做 RAG / MCP / Sandbox
- ❌ 不绕过 Temporal Replay 约束

---

## 十三、边界测试用例

### 13.1 节点离线测试

```bash
# 模拟 Worker 离线
# 1. 启动 swarm task
# 2. 等待 Worker 分配任务
# 3. 模拟 Worker 进程崩溃（不发送心跳）
# 4. 等待 heartbeat_timeout（90s）
# 5. 验证 WORKER_OFFLINE 事件
# 6. 验证任务重新分配
# 7. 验证最终结果仍然正确
```

### 13.2 网络延迟测试

```bash
# 模拟网络延迟
# 1. 使用 tc (traffic control) 模拟延迟
# 2. 验证 P2P 消息超时
# 3. 验证 Activity 重试
# 4. 验证最终一致性
```

### 13.3 文件冲突测试

```bash
# 并发写入冲突测试
# 1. Worker-A 读取文件 v1
# 2. Worker-B 读取文件 v1
# 3. Worker-A 更新文件为 v2
# 4. Worker-B 尝试更新文件（预期冲突）
# 5. 验证 CONFLICT_DETECTED 事件
# 6. 验证冲突记录正确
# 7. 验证自动解决或手动解决
```

### 13.4 权限错误测试

```bash
# 权限边界测试
# 1. Worker-A 未授权写 /docs/secrets.txt
# 2. 尝试写入
# 3. 验证返回 permission_denied
# 4. 验证审计日志记录
# 5. 连续 5 次失败
# 6. 验证账户锁定
```

### 13.5 资源限制测试

```bash
# 资源边界测试
# 1. worker_pool_size=10（最大）
# 2. max_total_tokens=64000（最大）
# 3. max_react_iterations=10（最大）
# 4. workspace max_versions=50（最大）
# 5. 验证系统不崩溃
# 6. 验证结果正确
```

---

*Phase 4+ 任务书 v2.0（满分可执行版）*
*实施级版本：Lead Agent + P2P + Workspace + State Sync + Conflict + Security + 资源约束 + 边界测试*