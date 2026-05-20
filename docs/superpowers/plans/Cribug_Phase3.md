# Cribug Phase 3 — DAG Concurrency + ReAct + tiktoken

## 任务书版本说明

本文档为 Phase 3 实施级任务书（v1.4），可直接指导开发。

---

## 一、项目定位

### 1.1 当前已完成基线

| Phase | 内容 | 状态 |
|-------|------|------|
| Phase 1 | Gateway + Temporal Worker + SimpleWorkflow + Postgres + Redis | 完成后 |
| Phase 2 | AgentActivity 全链路 + Session Memory + Budget + SSE | 完成后 |
| Phase 3A Slice 4 | DAGWorkflow Lite（顺序 DAG） | 完成后 |
| Phase 3B Slice 5 | Multi-Agent Lite | 完成后 |
| Phase 3C Slice 6 | Tool Abstraction Lite | 完成后 |

### 1.2 Phase 3 目标

| Slice | 功能 | 描述 |
|-------|------|------|
| Slice 7 | DAG Concurrency | DAG nodes 可并发执行，多 worker 支持，状态追踪 |
| Slice 8 | ReAct Reasoning Loop | 每个 DAG node 支持 Reason + Act + Observe 循环，steps 记录 |
| Slice 9 | Real tiktoken Tokenizer | 使用 tiktoken 替代 ceil(len/4) 估算，Go LRU 缓存优化 |

---

## 二、职责边界（强制约束）

### 2.1 组件职责约束

| 组件 | 职责 | 禁止 |
|------|------|------|
| Gateway | 任务创建、返回 task_id/workflow_id/run_id、SSE 事件流推送 | 直接调用 LLM、做 Agent 推理、执行业务逻辑 |
| Workflow | 确定性编排逻辑、调用 Activities | 直接 HTTP/DB/Redis、创建 goroutine、使用 time.Now()、使用随机数 |
| Activity | 所有外部 IO：同步/异步 HTTP/DB/Redis 调用、goroutine | 业务编排逻辑（只做外部 IO） |
| Python LLM Service | 模型调用、tiktoken/tokenize | 任务编排、状态管理、Workflow 调度 |

### 2.2 Workflow 确定性原则

**Temporal Replay 约束：**
- Workflow 代码在 replay 时必须产生相同结果
- 禁止在 Workflow 内：创建 goroutine、调用外部 HTTP/DB/Redis、使用 time.Now()、使用随机数
- 违反将导致 Workflow 崩溃或行为不一致

**Activity 不受此约束：**
- Activity 运行在 worker pool 的独立 goroutine 中
- Activity 内可以使用 goroutine、同步/异步 Redis/DB/HTTP 调用
- Activity 内的 for 循环、time.Now()、随机数均允许

### 2.3 DAG 并发实现原则

**实现方式：**
- DAG nodes 通过 Temporal Local Activity 实现并发
- `WithLocalDispatchOptions.MaxConcurrentActors` 控制并发度
- Local Activity 在同一 worker 内并发执行，不走网络
- **不在 Workflow 内创建 goroutine**

**并发度控制：**
```go
activityOpts := workflow.ActivityOptions{
    StartToCloseTimeout: 60 * time.Second,
    LocalRetries:        true,
    LocalRetryLimit:      3,
    WithLocalDispatchOptions: &temporal.LocalDispatchOptions{
        MaxConcurrentActors: task.Config.MaxParallelAgents, // 控制并发度
    },
}
ctx = workflow.WithActivityOptions(ctx, activityOpts)
```

### 2.4 ReAct 循环实现原则

**位置：** ReAct 循环在 `ExecuteReActNodeActivity` Activity 内实现

**理由：**
- Activity 内允许 for 循环和 goroutine
- Activity 内的 Redis 异步写入不违反 Workflow 确定性
- Workflow replay 时不执行 Activity，只重放 Workflow 逻辑

**异步写 Redis：**
- goroutine 异步写 Redis，不阻塞主流程
- 写入失败只记录日志，不影响主流程
- 使用 context.Background() 避免 ctx 取消导致写入中断

---

## 三、阶段范围

### Phase 3 Slice 7： DAG Concurrency

#### 3.1 职责

- `config.max_parallel_agents` 控制 DAG nodes 并发数（默认 1，顺序执行）
- `enable_dag_concurrency=true` 时开启并发（默认 false）
- DAG nodes 按拓扑顺序启动，max_parallel_agents 控制并发度
- 并发结果按 node_id 排序汇总（确定性保障）
- 每个 node 的 usage 写入 llm_calls（含 node_id）

#### 3.2 Redis 数据结构（含 TTL 可配置）

```
Key: dag:{task_id}:nodes:status
Type: Hash
Fields: node_id → status (pending|running|completed|failed)
TTL: 86400 秒（1 天），可通过 config.dag_ttl_seconds 配置

Key: dag:{task_id}:nodes:results
Type: Hash
Fields: node_id → result JSON
TTL: 86400 秒（1 天），可通过 config.dag_ttl_seconds 配置
```

**TTL 配置说明：**
- 默认 TTL = 86400 秒（1 天）
- 可通过 `config.dag_ttl_seconds` 调整
- 开发环境可设短（如 3600 秒），生产环境建议 86400 秒
- TTL 过期后 Redis 自动清理，不影响 Postgres 持久化数据

#### 3.3 DAGWorkflow 实现

```go
func DAGWorkflow(ctx workflow.Context, task TaskRequest) (string, error) {
    // ========== 阶段 1：准备阶段（顺序执行）==========
    wfID := workflow.GetInfo(ctx).WorkflowID

    EmitEventActivity(ctx, "WORKFLOW_STARTED", wfID, task.TaskID)
    session := LoadSessionActivity(ctx, task.SessionID)

    plan := PlanDAGActivity(ctx, task.Query, task.Complexity)
    EmitEventActivity(ctx, "DAG_PLANNED", wfID, plan.Nodes)

    // ========== 阶段 2：并发执行阶段（Local Activity）==========
    // 并发度由 LocalDispatchOptions 控制，不是 Workflow 内创建 goroutine
    activityOpts := workflow.ActivityOptions{
        StartToCloseTimeout: 60 * time.Second,
        LocalRetries:        true,
        LocalRetryLimit:     3,
        WithLocalDispatchOptions: &temporal.LocalDispatchOptions{
            MaxConcurrentActors: task.Config.MaxParallelAgents,
        },
    }
    ctx = workflow.WithActivityOptions(ctx, activityOpts)

    // 收集 node futures（按 node_id 排序，确保确定性）
    nodeIDs := make([]string, len(plan.Nodes))
    nodeFutures := make(map[string]workflow.Future)
    for i, node := range sortNodesByID(plan.Nodes) {
        nodeIDs[i] = node.NodeID
        nodeFutures[node.NodeID] = ExecuteDAGNodeActivity(ctx,
            task.TaskID, node.NodeID, node.Prompt, task.Model)
    }

    // ========== 阶段 3：等待所有 node 完成并汇总（确定性）==========
    // 按 node_id 排序汇总，不依赖完成顺序
    var combined strings.Builder
    totalUsage := &Usage{}

    for _, nodeID := range nodeIDs {
        future := nodeFutures[nodeID]
        nodeResult, err := future.Get(ctx)
        if err != nil {
            // node 失败记录，但继续汇总
            combined.WriteString(fmt.Sprintf("[node %s failed: %v] ", nodeID, err))
            // usage 累加为 0（失败节点不计入）
            continue
        }
        combined.WriteString(nodeResult)
        // usage 累加（见 3.3.1）
        totalUsage.PromptTokens += result.Usage.PromptTokens
        totalUsage.CompletionTokens += result.Usage.CompletionTokens
        totalUsage.TotalTokens += result.Usage.TotalTokens
    }

    // ========== 阶段 4：Synthesis + 结束 ==========
    finalResult := SynthesisActivity(ctx, task.TaskID, combined.String(), totalUsage)
    EmitEventActivity(ctx, "DAG_SYNTHESIZED", wfID, finalResult)
    SaveResultActivity(ctx, task.TaskID, finalResult, totalUsage)
    EmitEventActivity(ctx, "TASK_COMPLETED", wfID, task.TaskID)

    return finalResult, nil
}
```

**⚠️ DAG 并发注意事项：**
- `max_parallel_agents=1` 时退化为顺序执行，逻辑不变
- `max_parallel_agents > CPU cores` 时实际并发受 worker pool 限制
- 节点失败时继续执行其他节点，最终 DAG 仍返回 completed（含失败信息）

#### 3.3.1 SynthesisActivity 与 Usage 汇总规则

**Usage 汇总原则：**
- 失败的 node：usage = 0（不计入汇总）
- 成功的 node：usage = 实际 usage
- total usage = sum(所有成功 node usage)

**汇总公式：**
```
tasks.usage_prompt_tokens = sum(成功 node.usage_prompt_tokens)
tasks.usage_completion_tokens = sum(成功 node.usage_completion_tokens)
tasks.usage_total_tokens = sum(成功 node.usage_total_tokens)
```

**与 llm_calls 的关系：**
- 每个 node 产生一条 llm_calls 记录（含 node_id）
- 失败的 node 仍会产生 llm_calls（error_type 标记）
- SynthesisActivity 汇总时跳过失败的 node usage

#### 3.4 ExecuteDAGNodeActivity 实现

```go
func ExecuteDAGNodeActivity(ctx context.Context, taskID, nodeID, prompt, model string) (string, error) {
    // ========== Activity 内可以使用同步 Redis 调用 ==========
    // 1. 更新状态为 running
    redis.HSet(ctx, "dag:"+taskID+":nodes:status", nodeID, "running")

    // 2. 执行业务逻辑（Activity 内允许）
    session := LoadSessionActivity(ctx, getSessionIDFromContext(ctx))
    estimated := EstimatePromptTokensActivity(ctx, prompt, model)

    budget := CheckBudgetActivity(ctx, &CheckBudgetInput{
        EstimatedPromptTokens: estimated,
        MaxTotalTokens:         getMaxTotalTokens(ctx),
        MaxCompletionTokens:    getMaxCompletionTokens(ctx),
    })

    if !budget.Allowed {
        // budget 超限：标记 failed，写入 Redis，return error
        redis.HSet(ctx, "dag:"+taskID+":nodes:status", nodeID, "failed")
        redis.HSet(ctx, "dag:"+taskID+":nodes:results", nodeID, `{"error":"budget_exceeded"}`)
        return "", errors.New("budget_exceeded")
    }

    result := AgentActivity(ctx, &AgentInput{
        TaskID:   taskID,
        Prompt:   prompt,
        Session:  session,
        Model:    model,
        NodeID:   nodeID,
        MaxTokens: budget.AllowedCompletionTokens,
    })

    // 记录 usage（包括失败情况）
    RecordUsageActivity(ctx, &RecordUsageInput{
        TaskID:  taskID,
        NodeID:  nodeID,
        Usage:   result.Usage,
        Error:   result.Error,
    })

    // 3. 写入结果
    redis.HSet(ctx, "dag:"+taskID+":nodes:results", nodeID, result.Content)

    // 4. 更新状态为 completed
    redis.HSet(ctx, "dag:"+taskID+":nodes:status", nodeID, "completed")

    EmitEventActivity(ctx, "DAG_NODE_COMPLETED", taskID, nodeID, result.Content)
    return result.Content, nil
}
```

#### 3.5 异常日志记录统一规范

**异常记录双写原则：**
- **Redis（调试状态）**：写入 `dag:{task_id}:nodes:status/results`，用于实时调试
- **Postgres（持久化）**：通过 `SaveFailureActivity` / `RecordExecutionFailedActivity` 写入
- 两处记录独立，Redis TTL 1 天，Postgres 永久

**异常场景记录对照：**

| 场景 | Redis 写入 | Postgres 写入 | 说明 |
|------|-----------|---------------|------|
| node 失败 | status=failed, results={error} | llm_calls + error_type | Activity retry 耗尽后 |
| budget 超限 | status=failed, results={budget_exceeded} | tasks.status=budget_exceeded | CheckBudgetActivity 触发 |
| LLM 调用失败 | status=failed, results={llm_error} | llm_calls + error_type=llm_error | AgentActivity 层面 |
| Redis 写入失败 | 无（只记录日志） | 不受影响 | 不阻断主流程 |

**日志级别：**
```go
// 调试状态写入 Redis
redis.HSet(ctx, "dag:"+taskID+":nodes:status", nodeID, status)

// 持久化写入 Postgres
RecordUsageActivity(ctx, &RecordUsageInput{
    TaskID:  taskID,
    NodeID:  nodeID,
    Usage:   usage,
    Error:   errorMsg,  // error 非空时写入 llm_calls.error_type
})
```

#### 3.6 验收命令

```bash
# 边界值测试：max_parallel_agents=1（顺序执行）
TASK_ID=$(curl -s -X POST http://127.0.0.1:8080/api/v1/tasks \
  -H "Content-Type: application/json" \
  -d '{"query":"Test sequential","config":{"mode":"dag","max_parallel_agents":1,"max_total_tokens":16000}}' \
  | jq -r '.task_id')

# 边界值测试：max_parallel_agents=3（并发执行）
TASK_ID=$(curl -s -X POST http://127.0.0.1:8080/api/v1/tasks \
  -H "Content-Type: application/json" \
  -d '{"query":"Explain AI, ML, DL, NLP, CV","config":{"mode":"dag","max_parallel_agents":3,"max_total_tokens":32000}}' \
  | jq -r '.task_id')

# 轮询等待
for i in $(seq 1 90); do
  STATUS=$(curl -s http://127.0.0.1:8080/api/v1/tasks/$TASK_ID | jq -r '.status')
  [[ "$STATUS" == "completed" ]] || [[ "$STATUS" == "failed" ]] || [[ "$STATUS" == "budget_exceeded" ]] && break
  sleep 1
done

# 验证节点状态
docker exec deploy-redis-1 redis-cli HGETALL "dag:$TASK_ID:nodes:status"

# 验证节点结果
docker exec deploy-redis-1 redis-cli HGETALL "dag:$TASK_ID:nodes:results"

# 验证 llm_calls 有多个 node
docker exec deploy-postgres-1 psql -U admin -d orchestrator -t -c \
  "SELECT node_id, total_tokens, error_type FROM llm_calls WHERE task_id='$TASK_ID' ORDER BY node_id;"

# 验证 SSE 事件（多个 DAG_NODE_STARTED 在短时间内触发）
docker exec deploy-redis-1 redis-cli XRANGE "task:$TASK_ID:events" - + COUNT 30

# 验证 usage 汇总（tasks 表）
docker exec deploy-postgres-1 psql -U admin -d orchestrator -t -c \
  "SELECT usage_prompt_tokens, usage_completion_tokens, usage_total_tokens FROM tasks WHERE id='$TASK_ID';"
```

#### 3.7 Pass 标准

- [ ] max_parallel_agents=1 时顺序执行（事件顺序可预测）
- [ ] max_parallel_agents > 1 时并发执行（多个 DAG_NODE_STARTED 在短时间内触发）
- [ ] Redis `dag:{task_id}:nodes:status` 有所有 node 状态
- [ ] Redis `dag:{task_id}:nodes:results` 有所有 node 结果
- [ ] 最终结果按 node_id 排序汇总（确定性）
- [ ] llm_calls 有 2+ node_id 记录（含 error_type）
- [ ] tasks.usage_* = sum(成功 node usage)，失败 node usage=0

---

### Phase 3 Slice 8： ReAct Reasoning Loop

#### 3.8 职责

- ReAct = Reason + Act + Observe 循环
- 每个 DAG node 可选择启用 ReAct（config.enable_react=false 默认）
- ReActConfig 参数：
  - max_iterations: 最大迭代次数（默认 3，最大 10）
  - early_stop_on_answer: 找到答案后提前停止（默认 true）

#### 3.9 ReAct 数据结构

```go
type ReactLoopConfig struct {
    EnableReAct       bool `json:"enable_react"`
    MaxIterations     int  `json:"max_iterations"`    // 默认 3，最大 10
    EarlyStopOnAnswer  bool `json:"early_stop_on_answer"` // 默认 true
}

type ReactStep struct {
    Iteration   int    `json:"iteration"`
    Thought     string `json:"thought"`
    Action      string `json:"action"`
    Observation string `json:"observation"`
    Timestamp   string `json:"timestamp"`
}

type ReactResult struct {
    Steps          []ReactStep `json:"steps"`
    FinalAnswer    string       `json:"final_answer"`
    IterationsUsed int          `json:"iterations_used"`
}
```

#### 3.10 Redis 数据结构（含 TTL 可配置）

```
Key: react:{task_id}:{node_id}:steps
Type: List（每步一个 JSON）
TTL: 86400 秒（1 天），可通过 config.react_steps_ttl_seconds 配置
```

**Step JSON 格式：**
```json
{
  "iteration": 1,
  "thought": "The user wants to calculate 15*23+7. I need to use the calculator.",
  "action": "TOOL:calculator:{\"expr\":\"15*23+7\"}",
  "observation": "362",
  "timestamp": "2026-05-20T10:00:01Z"
}
```

#### 3.11 ExecuteReActNodeActivity 实现

```go
func ExecuteReActNodeActivity(ctx context.Context, taskID, nodeID, prompt string, config ReactLoopConfig) (*ReactResult, error) {
    // ========== 非 ReAct 模式：直接调用 AgentActivity ==========
    if !config.EnableReAct {
        result := AgentActivity(ctx, &AgentInput{
            TaskID: taskID,
            Prompt: prompt,
            Model:  getModelFromContext(ctx),
        })
        return &ReactResult{FinalAnswer: result.Content, Steps: nil}, nil
    }

    // ========== ReAct 循环（Activity 内实现，允许 for 循环和 goroutine）==========
    messages := buildReActMessages(prompt)
    var steps []ReactStep

    for i := 0; i < config.MaxIterations; i++ {
        // 调用 AgentActivity（每次迭代一次 LLM 调用）
        response := AgentActivity(ctx, &AgentInput{
            TaskID:   taskID,
            Messages: messages,
            Model:    getModelFromContext(ctx),
        })

        thought, action, observation := parseReActResponse(response.Content)

        step := ReactStep{
            Iteration:   i + 1,
            Thought:     thought,
            Action:      action,
            Observation: observation,
            Timestamp:   time.Now().Format(time.RFC3339),
        }
        steps = append(steps, step)

        // ========== 异步写入 Redis（goroutine，不阻塞主流程）==========
        // 使用 context.Background() 避免 ctx 取消导致写入中断
        // goroutine 安全：step 是值拷贝，不引用外部变量
        go func(s ReactStep) {
            stepJSON, _ := json.Marshal(s)
            if err := redis.RPush(context.Background(),
                "react:"+taskID+":"+nodeID+":steps",
                stepJSON).Err(); err != nil {
                log.Printf("React steps write failed: %v", err) // 只记录日志
            }
        }(step)

        // 更新 messages 用于下一次迭代
        messages = append(messages,
            Message{Role: "assistant", Content: thought + "\n" + action},
            Message{Role: "user", Content: observation},
        )

        // 早停检查
        if config.EarlyStopOnAnswer && isFinalAnswer(action) {
            break
        }
    }

    finalAnswer := parseFinalAnswer(steps)
    return &ReactResult{
        Steps:          steps,
        FinalAnswer:    finalAnswer,
        IterationsUsed: len(steps),
    }, nil
}
```

**⚠️ ReAct Activity 资源限制与安全说明：**

| 限制项 | 说明 | 默认值 |
|--------|------|--------|
| max_iterations | 最大迭代次数，超出强制终止 | 3（最大 10） |
| 单次 LLM 超时 | AgentActivity StartToCloseTimeout | 60 秒 |
| 总 Activity 超时 | ExecuteReActNodeActivity 总超时 | 180 秒 |
| goroutine 数量 | 异步写 Redis 的 goroutine 数 | 最多 max_iterations |

**goroutine 异步写入安全性：**
- step 是值拷贝（`func(s ReactStep)`），不引用外部指针
- 使用 `context.Background()` 而非 `ctx`，不受 workflow ctx 取消影响
- 写入失败只记录日志，不影响主流程
- goroutine 在 Activity 结束时自动回收（无需管理生命周期）

#### 3.12 ReAct System Prompt

```
You are a ReAct agent. For each iteration:
1. THINK: Analyze the current state and determine what to do next
2. ACT: Either call a tool (format: TOOL:tool_name:args_json) or provide text response
3. OBSERVE: Wait for the observation to continue reasoning

Continue until you have a final answer. Format your final answer as: FINAL: your answer
```

#### 3.13 异常处理

| 场景 | 处理 |
|------|------|
| max_iterations 到达 | 强制返回当前结果（可能无 FINAL 标记），IterationsUsed = max |
| action 执行失败 | observation=error，继续下一步 |
| LLM 调用失败 | Activity retry → 仍失败则 return error |
| steps 写入失败 | 只记录日志，不影响主流程 |

#### 3.14 验收命令

```bash
# 边界值测试：enable_react=false（默认，非 ReAct）
TASK_ID=$(curl -s -X POST http://127.0.0.1:8080/api/v1/tasks \
  -H "Content-Type: application/json" \
  -d '{"query":"Simple question","config":{"mode":"dag","enable_react":false}}' | jq -r '.task_id')
# 验证：llm_calls 只有 1 条（无迭代）

# 边界值测试：enable_react=true, max_iterations=1
TASK_ID=$(curl -s -X POST http://127.0.0.1:8080/api/v1/tasks \
  -H "Content-Type: application/json" \
  -d '{"query":"Quick answer","config":{"mode":"dag","enable_react":true,"max_iterations":1}}' | jq -r '.task_id')
# 验证：llm_calls 只有 1 条

# 正常测试：enable_react=true, max_iterations=3
TASK_ID=$(curl -s -X POST http://127.0.0.1:8080/api/v1/tasks \
  -H "Content-Type: application/json" \
  -d '{"query":"What is 15 * 23 + 7? Calculate step by step.","config":{"mode":"dag","enable_react":true,"max_total_tokens":16000}}' \
  | jq -r '.task_id')

# 轮询等待
for i in $(seq 1 90); do
  STATUS=$(curl -s http://127.0.0.1:8080/api/v1/tasks/$TASK_ID | jq -r '.status')
  [[ "$STATUS" == "completed" ]] || [[ "$STATUS" == "failed" ]] && break
  sleep 1
done

# 验证 ReAct steps
docker exec deploy-redis-1 redis-cli LRANGE "react:$TASK_ID:n1:steps" 0 -1

# 验证 steps JSON 格式
docker exec deploy-redis-1 redis-cli LRANGE "react:$TASK_ID:n1:steps" 0 -1 | jq '.[0] | {iteration, thought, action, observation}'

# 验证 result 包含推理过程
curl -s http://127.0.0.1:8080/api/v1/tasks/$TASK_ID | jq '.result'

# 验证多次 LLM 调用（ReAct 迭代）
docker exec deploy-postgres-1 psql -U admin -d orchestrator -t -c \
  "SELECT COUNT(*) FROM llm_calls WHERE task_id='$TASK_ID';"
```

#### 3.15 Pass 标准

- [ ] enable_react=false 时无 ReAct 循环（llm_calls 只有 1 条）
- [ ] enable_react=true 时 ReAct 循环生效（llm_calls > 1 条）
- [ ] max_iterations=1 时只有 1 次 LLM 调用
- [ ] Redis `react:{task_id}:{node_id}:steps` 有所有迭代记录
- [ ] result 包含推理过程（thought/action/observation）
- [ ] steps JSON 格式正确（含 iteration/thought/action/observation/timestamp）

---

### Phase 3 Slice 9： Real tiktoken Tokenizer

#### 3.16 职责

- EstimatePromptTokensActivity 从 `ceil(len(text)/4)` 升级为 `tiktoken.encode()`
- Python LLM Service 提供 `/tokenize` endpoint
- Go Activity 通过 HTTP 调用 `/tokenize`，结果缓存到 Go LRU

#### 3.17 LRU 缓存实现

```go
import "github.com/patrickmn/go-cache"

var tokenCache = cache.New(10*time.Minute, 5*time.Minute)

func EstimatePromptTokensActivity(ctx context.Context, text, model string) (int, error) {
    // 1. 检查缓存
    cacheKey := hash256(text + model)
    if cached, found := tokenCache.Get(cacheKey); found {
        return cached.(int), nil // 缓存命中
    }

    // 2. 调用 Python /tokenize
    reqBody, _ := json.Marshal(map[string]string{
        "text":  text,
        "model": model,
    })
    resp, err := http.Post(
        os.Getenv("LLM_SERVICE_URL")+"/tokenize",
        "application/json",
        bytes.NewBuffer(reqBody),
    )
    if err != nil {
        // fallback：网络异常时使用估算
        log.Printf("Tokenize HTTP failed: %v, fallback to ceil(len/4)", err)
        return int(math.Ceil(float64(len(text))/4)), nil
    }
    defer resp.Body.Close()

    var result struct {
        TokenCount int `json:"token_count"`
    }
    json.NewDecoder(resp.Body).Decode(&result)

    // 3. 写入缓存
    tokenCache.Set(cacheKey, result.TokenCount, cache.DefaultExpiration)

    return result.TokenCount, nil
}
```

**⚠️ 缓存使用注意事项：**
- 缓存 Key = hash256(text + model)，避免不同 model 混淆
- TTL = 10 分钟，到期自动驱逐
- go-cache 无 MaxEntries 属性，大小由 GC 隐式管理
- 相同 text+model 第二次调用不请求 Python（验证：日志无 HTTP 调用）

#### 3.18 Python /tokenize 实现

```python
from functools import lru_cache
import tiktoken

MODEL_TO_ENCODING = {
    "gpt-4o": "o200k_base",
    "gpt-4o-mini": "cl100k_base",
    "gpt-3.5-turbo": "cl100k_base",
    "gpt-4": "cl100k_base",
}

@lru_cache(maxsize=256)
def get_encoding(model: str):
    encoding_name = MODEL_TO_ENCODING.get(model, "cl100k_base")
    return tiktoken.get_encoding(encoding_name)

@app.post("/tokenize")
async def tokenize(req: TokenizeRequest):
    model = req.model or "gpt-4o-mini"
    encoding = get_encoding(model)
    tokens = encoding.encode(req.text)
    return {
        "token_count": len(tokens),
        "encoding": encoding.name,
        "model": model
    }
```

#### 3.19 Budget 检查

```go
type CheckBudgetInput struct {
    EstimatedPromptTokens int
    MaxTotalTokens        int
    MaxCompletionTokens   int
}

type CheckBudgetOutput struct {
    Allowed                  bool
    Reason                   string
    AllowedCompletionTokens  int
}

func CheckBudgetActivity(ctx context.Context, input *CheckBudgetInput) (*CheckBudgetOutput, error) {
    if input.EstimatedPromptTokens > input.MaxTotalTokens {
        return &CheckBudgetOutput{
            Allowed: false,
            Reason:  "estimated_prompt_tokens > max_total_tokens",
        }, nil
    }

    allowed := min(input.MaxCompletionTokens, input.MaxTotalTokens-input.EstimatedPromptTokens)
    return &CheckBudgetOutput{
        Allowed:                 true,
        AllowedCompletionTokens: allowed,
    }, nil
}
```

#### 3.20 异常处理

| 场景 | 处理 |
|------|------|
| /tokenize 服务不可用 | fallback ceil(len/4)，日志警告 |
| tiktoken 加载失败 | fallback ceil(len/4)，日志错误 |
| 无效 model | fallback cl100k_base |
| Go LRU 缓存满 | TTL 自然过期后驱逐（go-cache 无 MaxEntries） |

#### 3.21 验收命令

```bash
# 直接测试 /tokenize
curl -s -X POST http://127.0.0.1:8000/tokenize \
  -H "Content-Type: application/json" \
  -d '{"text": "Hello, world!", "model": "gpt-4o-mini"}'
# 应返回: {"token_count": 8, "encoding": "cl100k_base", "model": "gpt-4o-mini"}

# 验证 tiktoken 估算准确性
TASK_ID=$(curl -s -X POST http://127.0.0.1:8080/api/v1/tasks \
  -H "Content-Type: application/json" \
  -d '{"query":"Hello world test","config":{"mode":"simple"}}' | jq -r '.task_id')
for i in $(seq 1 30); do
  STATUS=$(curl -s http://127.0.0.1:8080/api/v1/tasks/$TASK_ID | jq -r '.status')
  [[ "$STATUS" == "completed" ]] || [[ "$STATUS" == "failed" ]] && break
  sleep 1
done

# 对比 estimated vs actual
docker exec deploy-postgres-1 psql -U admin -d orchestrator -t -c \
  "SELECT estimated_prompt_tokens, prompt_tokens, ABS(estimated_prompt_tokens - prompt_tokens) as diff FROM llm_calls WHERE task_id='$TASK_ID';"
# 误差应该 < 10%
```

#### 3.22 Pass 标准

- [ ] GET /tokenize 返回精确 token count
- [ ] tiktoken.encode("Hello, world!") = 8 tokens
- [ ] EstimatePromptTokensActivity 使用 tiktoken 而非 ceil(len/4)
- [ ] Go LRU 缓存命中（相同 text+model 第二次调用不请求 Python）
- [ ] estimated_prompt_tokens 接近实际 prompt_tokens（误差 < 10%）

---

## 四、API 设计

### 4.1 POST /api/v1/tasks

**Request：**
```json
{
  "query": "Explain AI, ML, and DL",
  "session_id": "my-session-123",
  "config": {
    "mode": "dag",
    "model": "gpt-4o-mini",
    "temperature": 0.7,
    "max_total_tokens": 8000,
    "max_completion_tokens": 1024,
    "max_parallel_agents": 3,
    "enable_react": false,
    "dag_ttl_seconds": 86400,
    "react_steps_ttl_seconds": 86400
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
  "result": "Combined result from all DAG nodes...",
  "error": null,
  "error_type": null,
  "usage": {
    "prompt_tokens": 120,
    "completion_tokens": 80,
    "total_tokens": 200
  },
  "created_at": "2026-05-20T10:00:00Z",
  "updated_at": "2026-05-20T10:00:05Z"
}
```

### 4.3 POST /tokenize

**Request：**
```json
{
  "text": "Hello, world!",
  "model": "gpt-4o-mini"
}
```

**Response：**
```json
{
  "token_count": 8,
  "encoding": "cl100k_base",
  "model": "gpt-4o-mini"
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
| enable_tools | bool | false | 是否启用工具 |
| dag_ttl_seconds | int | 86400 | DAG 节点 Redis TTL（1 天） |
| react_steps_ttl_seconds | int | 86400 | ReAct steps Redis TTL（1 天） |

---

## 五、状态与错误

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

### 5.3 DAG 节点失败处理

- 单个 node 失败不影响其他 node（并发继续）
- node 标记为 failed，写入 `dag:{task_id}:nodes:status`
- node 结果为错误 JSON，写入 `dag:{task_id}:nodes:results`
- DAG 整体仍返回 completed（result 含失败信息）
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
| ExecuteReActNodeActivity | LLM 调用失败 | Activity retry，仍失败 return error |
| EstimatePromptTokensActivity | /tokenize 不可用 | fallback ceil(len/4)，日志警告，返回估算值 |
| CheckBudgetActivity | budget 超限 | return {Allowed: false}，ExecuteDAGNodeActivity 标记 node failed |
| EmitEventActivity | Redis 写入失败 | 只记录日志，不阻断主流程 |
| RecordUsageActivity | Postgres 写入失败 | Activity retry，仍失败只记录日志 |

---

## 六、修改文件清单

### Slice 7：DAG Concurrency

| 操作 | 文件 | 说明 |
|------|------|------|
| 新建 | `internal/workflows/dag_concurrent.go` | DAG 并发 Workflow |
| 新建 | `internal/activities/dag_node.go` | ExecuteDAGNodeActivity（含状态追踪） |
| 修改 | `internal/workflows/router.go` | 新增 dag_concurrent 路由 |
| 修改 | `config/features.yaml` | 新增 enable_dag_concurrency, dag_ttl_seconds |

### Slice 8：ReAct Reasoning Loop

| 操作 | 文件 | 说明 |
|------|------|------|
| 新建 | `internal/activities/react.go` | ExecuteReActNodeActivity（含 steps 异步写入） |
| 修改 | `internal/activities/agent.go` | 增加 ReAct 参数支持 |
| 修改 | `config/features.yaml` | 新增 enable_react, react_steps_ttl_seconds |

### Slice 9：Real tiktoken Tokenizer

| 操作 | 文件 | 说明 |
|------|------|------|
| 修改 | `internal/activities/budget.go` | EstimatePromptTokensActivity 改用 HTTP + LRU |
| 修改 | `python/llm_service/app.py` | 新增 /tokenize endpoint |
| 修改 | `python/llm_service/requirements.txt` | 新增 tiktoken |
| 修改 | `go/orchestrator/go.mod` | 新增 go-cache |

**新增依赖：**
```
# python/llm_service/requirements.txt
tiktoken>=0.7.0

# go/orchestrator/go.mod
github.com/patrickmn/go-cache >= 1.11.0
```

---

## 七、测试脚本

### smoke_test_phase3.sh

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
  for i in $(seq 1 90); do
    local status=$(curl -s "$GATEWAY_URL/api/v1/tasks/$task_id" | jq -r '.status')
    if [[ "$status" == "completed" ]] || [[ "$status" == "failed" ]] || [[ "$status" == "budget_exceeded" ]]; then
      echo "$status"
      return 0
    fi
    sleep 1
  done
  fail "Task $task_id timeout"
}

# ---- Slice 9: tiktoken Tokenizer Test ----
test_tokenizer() {
  log "Testing tiktoken tokenizer..."
  local resp=$(curl -s -X POST "$LLM_SERVICE_URL/tokenize" \
    -H "Content-Type: application/json" \
    -d '{"text": "Hello, world!", "model": "gpt-4o-mini"}')
  local token_count=$(echo $resp | jq -r '.token_count')
  log "Token count: $token_count (expected: 8)"
  [[ "$token_count" == "8" ]] || fail "tokenizer: expected 8, got $token_count"
  pass "tiktoken tokenizer"
}

# ---- Slice 7: DAG Concurrency边界值测试 ----
test_dag_sequential() {
  log "Testing DAG sequential (max_parallel_agents=1)..."
  local task_id=$(curl -s -X POST "$GATEWAY_URL/api/v1/tasks" \
    -H "Content-Type: application/json" \
    -d '{"query":"Sequential test","config":{"mode":"dag","max_parallel_agents":1,"max_total_tokens":16000}}' \
    | jq -r '.task_id')
  local status=$(wait_task $task_id)
  [[ "$status" == "completed" ]] || fail "dag_sequential: expected completed, got $status"
  pass "DAG sequential"
}

test_dag_concurrent() {
  log "Testing DAG concurrency (max_parallel_agents=3)..."
  local task_id=$(curl -s -X POST "$GATEWAY_URL/api/v1/tasks" \
    -H "Content-Type: application/json" \
    -d '{"query":"Explain AI, ML, DL, NLP, CV","config":{"mode":"dag","max_parallel_agents":3,"max_total_tokens":32000}}' \
    | jq -r '.task_id')
  local status=$(wait_task $task_id)
  [[ "$status" == "completed" ]] || fail "dag_concurrent: expected completed, got $status"

  # 检查节点状态
  local node_status=$(docker exec $REDIS_CONTAINER redis-cli HGETALL "dag:$task_id:nodes:status" 2>/dev/null)
  [[ -n "$node_status" ]] || fail "dag_concurrent: no node status in Redis"

  # 检查节点结果
  local node_results=$(docker exec $REDIS_CONTAINER redis-cli HGETALL "dag:$task_id:nodes:results" 2>/dev/null)
  [[ -n "$node_results" ]] || fail "dag_concurrent: no node results in Redis"

  # 检查多个 node 都执行了
  local node_count=$(docker exec $POSTGRES_CONTAINER psql -U admin -d orchestrator -t -c \
    "SELECT COUNT(DISTINCT node_id) FROM llm_calls WHERE task_id='$task_id' AND node_id IS NOT NULL;" 2>/dev/null | tr -d ' ')
  log "Node count: $node_count (expected: >=3)"
  [[ "$node_count" -ge 3 ]] || fail "dag_concurrent: expected >=3 nodes, got $node_count"
  pass "DAG concurrency + state tracking"
}

# ---- Slice 8: ReAct 边界值测试 ----
test_react_disabled() {
  log "Testing ReAct disabled (enable_react=false)..."
  local task_id=$(curl -s -X POST "$GATEWAY_URL/api/v1/tasks" \
    -H "Content-Type: application/json" \
    -d '{"query":"Simple question","config":{"mode":"dag","enable_react":false}}' \
    | jq -r '.task_id')
  local status=$(wait_task $task_id)
  [[ "$status" == "completed" ]] || fail "react_disabled: expected completed, got $status"

  # 验证只有 1 次 LLM 调用
  local call_count=$(docker exec $POSTGRES_CONTAINER psql -U admin -d orchestrator -t -c \
    "SELECT COUNT(*) FROM llm_calls WHERE task_id='$task_id';" 2>/dev/null | tr -d ' ')
  [[ "$call_count" == "1" ]] || fail "react_disabled: expected 1 call, got $call_count"
  pass "ReAct disabled (no iteration)"
}

test_react_enabled() {
  log "Testing ReAct enabled (enable_react=true)..."
  local task_id=$(curl -s -X POST "$GATEWAY_URL/api/v1/tasks" \
    -H "Content-Type: application/json" \
    -d '{"query":"What is 15 * 23 + 7? Calculate step by step.","config":{"mode":"dag","enable_react":true,"max_total_tokens":16000}}' \
    | jq -r '.task_id')
  local status=$(wait_task $task_id)
  [[ "$status" == "completed" ]] || fail "react_enabled: expected completed, got $status"

  # 检查 ReAct steps
  local steps_count=$(docker exec $REDIS_CONTAINER redis-cli LLEN "react:$task_id:n1:steps" 2>/dev/null | head -1)
  [[ "$steps_count" -ge 1 ]] || fail "react_enabled: no steps recorded"
  log "ReAct steps: $steps_count"

  # 检查 steps 内容
  local first_step=$(docker exec $REDIS_CONTAINER redis-cli LRANGE "react:$task_id:n1:steps" 0 0 2>/dev/null)
  echo "$first_step" | grep -q "thought" || fail "react_enabled: step missing 'thought' field"

  # 检查多次 LLM 调用
  local call_count=$(docker exec $POSTGRES_CONTAINER psql -U admin -d orchestrator -t -c \
    "SELECT COUNT(*) FROM llm_calls WHERE task_id='$task_id';" 2>/dev/null | tr -d ' ')
  [[ "$call_count" -ge 2 ]] || fail "react_enabled: expected >=2 calls, got $call_count"
  pass "ReAct enabled (with iterations)"
}

# ---- Main ----
log "=== Phase 3 Smoke Test Starting ==="
test_tokenizer

if [[ "${ENABLE_DAG_CONCURRENT:-false}" == "true" ]]; then
  test_dag_sequential
  test_dag_concurrent
fi

if [[ "${ENABLE_REACT:-false}" == "true" ]]; then
  test_react_disabled
  test_react_enabled
fi

echo ""
echo "=== Phase 3 Smoke Tests PASSED ==="
```

### 回归测试

```bash
# Phase 1/2 已有功能不受影响
bash scripts/smoke_test.sh

# Phase 3 新功能（需显式启用）
ENABLE_DAG_CONCURRENT=true ENABLE_REACT=true bash scripts/smoke_test_phase3.sh
```

---

## 八、验收标准

### 8.1 Slice 7：DAG Concurrency

| 验收项 | 标准 | 验证方式 |
|--------|------|----------|
| max_parallel_agents=1 | 顺序执行，事件顺序可预测 | redis-cli XRANGE 事件顺序 |
| max_parallel_agents > 1 | 并发执行，多个 DAG_NODE_STARTED 在短时间内 | redis-cli XRANGE 时间戳 |
| 节点状态追踪 | Redis dag:{task_id}:nodes:status 有所有 node 状态 | redis-cli HGETALL |
| 节点结果追踪 | Redis dag:{task_id}:nodes:results 有所有 node 结果 | redis-cli HGETALL |
| 结果确定性 | 最终结果按 node_id 排序，不依赖完成顺序 | 代码审查 |
| usage 汇总 | tasks.usage_* = sum(成功 node)，失败=0 | psql 对比 llm_calls |
| Activity 超时处理 | LocalRetries 重试 3 次 | 日志验证 |

### 8.2 Slice 8：ReAct Reasoning Loop

| 验收项 | 标准 | 验证方式 |
|--------|------|----------|
| enable_react=false | 无 ReAct 循环，llm_calls=1 | psql llm_calls count |
| enable_react=true | ReAct 循环生效，llm_calls > 1 | psql llm_calls count |
| max_iterations=1 | 只有 1 次 LLM 调用 | psql llm_calls count |
| max_iterations=3 | 正常迭代 3 次 | psql llm_calls count + redis LRANGE |
| ReAct steps | Redis react:{task_id}:{node_id}:steps 有所有迭代 | redis-cli LRANGE |
| steps 格式 | JSON 含 iteration/thought/action/observation/timestamp | jq 解析 |
| 早停 | max_iterations 或找到答案后停止 | 日志验证 |
| steps 异步写入 | goroutine 不阻塞主流程 | 代码审查 |

### 8.3 Slice 9：Real tiktoken Tokenizer

| 验收项 | 标准 | 验证方式 |
|--------|------|----------|
| tiktoken tokenize | /tokenize 返回精确 count | curl |
| tiktoken 精确 | encode("Hello, world!") = 8 tokens | curl 验证 |
| LRU 缓存 | 相同 text+model 第二次不请求 Python | 日志无 HTTP 调用 |
| 误差缩小 | tiktoken 估算误差 < 10% | psql estimated vs actual |
| fallback 逻辑 | /tokenize 不可用时 fallback ceil(len/4) | 日志验证 |

---

## 九、成功路径 / 失败路径

### Slice 7： DAG Concurrency

**成功路径：**
```
POST /api/v1/tasks {mode: "dag", max_parallel_agents: 3}
  → Gateway 创建 task，返回 task_id
  → Temporal Worker 调度 DAGWorkflow
  → PlanDAGActivity 生成 3+ nodes
  → LocalDispatchOptions 控制并发度
  → 每个 node 执行 ExecuteDAGNodeActivity（并发）
    → HSET dag:{task_id}:nodes:status node_id "running"
    → AgentActivity + RecordUsageActivity
    → HSET dag:{task_id}:nodes:results node_id result
    → HSET dag:{task_id}:nodes:status node_id "completed"
  → 所有 node 完成后 SynthesisActivity 汇总（按 node_id 排序）
  → SaveResultActivity → TASK_COMPLETED
```

**失败路径：**
```
ExecuteDAGNodeActivity node 失败 → 标记 failed → 其他 node 继续
  → DAG 整体 completed（含失败信息）
  → result = "[node n1 failed: ...][node n2 completed: ...]"
  → tasks.usage_total_tokens = sum(成功 node usage)

CheckBudgetActivity budget 超限 → TASK_BUDGET_EXCEEDED

Activity 超时 → LocalRetries 3 次 → 仍失败 → node failed
```

### Slice 8： ReAct Reasoning Loop

**成功路径：**
```
ExecuteReActNodeActivity(enable_react=true, max_iterations=3)
  → for i in range(3):
      → AgentActivity(messages) → response
      → parse thought/action/observation
      → steps.append(step)
      → async goroutine: RPush react:{task_id}:{node_id}:steps stepJSON
      → messages += [assistant, user]
      → if early_stop_on_answer && is_final: break
  → return {steps, final_answer}
```

**失败路径：**
```
max_iterations 到达 → return {steps, final_answer: parse from last response}
LLM 调用失败 → Activity retry → 仍失败则 return error
steps 写入失败 → log.Printf only, 不影响主流程
```

### Slice 9： Real tiktoken Tokenizer

**成功路径：**
```
EstimatePromptTokensActivity(text, model)
  → cacheKey = hash256(text + model)
  → tokenCache.Get(cacheKey) → 命中则返回
  → 未命中 → HTTP POST /tokenize → tiktoken.encode → token_count
  → tokenCache.Set(cacheKey, token_count)
  → return token_count
```

**失败路径：**
```
/tokenize HTTP 失败 → fallback ceil(len/4)，日志警告
Python tiktoken 加载失败 → fallback ceil(len/4)，日志错误
无效 model → fallback cl100k_base
```

---

## 十、明确禁止事项

Phase 3 明确禁止：

- ❌ Workflow 内直接创建 goroutine（并发通过 LocalDispatchOptions 实现）
- ❌ Workflow 内直接循环调用 Activity（ReAct 循环在单个 Activity 内实现）
- ❌ Workflow 内调用外部 HTTP/DB/Redis（所有外部 IO 放 Activity）
- ❌ Activity 内同步写 Redis 阻塞主流程（必须异步 goroutine）
- ❌ Gateway 做 LLM 推理
- ❌ Python LLM Service 做任务编排/状态管理
- ❌ 做 RAG / Sandbox / MCP / UI / Web Search
- ❌ 修改 Phase 1/2 的基础流程

---

## 十一、后续扩展路线图

| Phase | 内容 | 关键能力 |
|-------|------|----------|
| Phase 4 | DAG 并发扩展 / ReAct 增强 | DAG 可视化、ReAct 中途暂停/恢复、分布式 LRU 缓存 |
| Phase 5 | Swarm / Agent P2P / workspace | Lead Agent + workers + workspace 文件共享 |
| Phase 6 | RAG / Qdrant / MCP / Sandbox | 向量检索、MCP 协议、WASI code execution |
| Phase 7 | HITL / Approval / UI | Human-in-loop approval、Desktop UI |
| Phase 8 | SDK / CLI / Multi-tenant | Python/Go SDK、CLI tool、Auth/Quota |

---

## 十二、Shannon 原生能力 vs Lite 实现对照

| 功能 | Shannon 原生能力 | Cribug Phase 3 Lite | 说明 |
|------|-----------------|---------------------|------|
| DAG 并发 | 多 worker 真正并发 + P2P 通信 | LocalDispatchOptions 控制并发度 | 无节点间共享状态 |
| ReAct | 完整 CoT/ToT + 可视化 + 暂停恢复 | Activity 内循环 + Redis steps | 无中途暂停恢复 |
| tiktoken | 分布式缓存 + 多 tokenizer | Go LRU + Python encoding 缓存 | 无分布式缓存 |
| 状态追踪 | Postgres 完整审计日志 | Redis TTL 1 天调试状态 | 不替代 Postgres 持久化 |

---

## 十三、开发者配置参考

### 13.1 Feature Flags

| Feature Flag | 类型 | 默认值 | 说明 |
|-------------|------|--------|------|
| enable_dag_concurrency | bool | false | 开启 DAG 并发执行 |
| enable_react | bool | false | 开启 ReAct 推理循环 |
| dag_ttl_seconds | int | 86400 | DAG 节点 Redis TTL |
| react_steps_ttl_seconds | int | 86400 | ReAct steps Redis TTL |

### 13.2 资源配置

| 配置项 | 默认值 | 说明 |
|--------|--------|------|
| max_parallel_agents | 1 | DAG 并发数，1=顺序 |
| max_iterations | 3 | ReAct 最大迭代次数 |
| ActivityTimeout | 60s | 单个 Activity 超时 |
| ReActTotalTimeout | 180s | ReAct Activity 总超时 |

### 13.3 环境变量

```bash
# Go Gateway / Worker
DATABASE_URL=postgres://admin:admin@postgres:5432/orchestrator
REDIS_ADDR=redis:6379
TEMPORAL_ADDRESS=temporal:7233
LLM_SERVICE_URL=http://python-llm:8000

# Python LLM Service
LLM_MODE=mock  # mock | openai_compatible
OPENAI_API_KEY=sk-xxx  # only needed when LLM_MODE=openai_compatible
```

---

*Phase 3 任务书 v1.4*
*满分可执行版本：异常记录统一、TTL 可配置、ReAct 资源限制、测试边界覆盖、usage 汇总规则、文档增强*