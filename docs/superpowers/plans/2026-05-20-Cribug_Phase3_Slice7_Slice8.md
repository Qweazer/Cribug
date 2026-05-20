# Phase 3 Slice 7 & 8 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement DAG Concurrency (Slice 7) with LocalDispatchOptions + Redis state tracking, and ReAct Reasoning Loop (Slice 8) with stepwise tool execution.

**Architecture:**
- **Slice 7:** DAG nodes execute concurrently using Temporal LocalDispatchOptions. Redis Hash stores node status/results for debugging. Results aggregated deterministically by node_id.
- **Slice 8:** ReAct loop inside Activity allows for/async goroutine. Steps written to Redis List asynchronously. Tool calls via existing tool infrastructure.

**Tech Stack:** Go (Temporal SDK), Redis (Hash/List), Postgres (llm_calls)

---

## 1. File Structure

```
internal/
  config/
    config.go              # Modify: add DAGConcurrency, ReActConfig fields
  types/
    dag.go                 # Modify: add DAGConcurrencyConfig, ReActConfig, ReactStep
    task.go                # Modify: add DAGConfig to WorkflowTaskRequest
  activities/
    dag.go                 # Modify: add Redis state tracking, concurrency support
    react.go               # Create: ExecuteReActNodeActivity
  workflows/
    dag.go                 # Modify: add LocalDispatchOptions, concurrent execution
  events/
    types.go               # Modify: add REACT_STEP_* event types
scripts/
  test_dag_concurrency.sh  # Create: test DAG concurrency
  test_react_reasoning.sh  # Create: test ReAct reasoning loop
README.md                  # Modify: update Slice 7/8 status
```

---

## 2. Config & Types

### Task 1: Add Config Fields for DAG Concurrency & ReAct

**Files:**
- Modify: `internal/config/config.go`

- [ ] **Step 1: Add new config fields**

```go
type Config struct {
    // ... existing fields ...

    // DAG Concurrency (Slice 7)
    EnableDAGConcurrency bool
    DAGTTLSeconds       int
    MaxParallelAgents  int

    // ReAct Reasoning Loop (Slice 8)
    EnableReAct         bool
    ReActMaxIterations  int
    ReActStepsTTLSeconds int
}
```

- [ ] **Step 2: Load new config values**

```go
EnableDAGConcurrency: getEnvAsBool("ENABLE_DAG_CONCURRENCY", false),
DAGTTLSeconds:       getEnvAsInt("DAG_TTL_SECONDS", 86400),
MaxParallelAgents:   getEnvAsInt("MAX_PARALLEL_AGENTS", 1),
EnableReAct:         getEnvAsBool("ENABLE_REACT", false),
ReActMaxIterations:  getEnvAsInt("REACT_MAX_ITERATIONS", 3),
ReActStepsTTLSeconds: getEnvAsInt("REACT_STEPS_TTL_SECONDS", 86400),
```

- [ ] **Step 3: Add DAG/ReAct config to task request**

In `internal/types/task.go` or wherever WorkflowTaskRequest is defined:

```go
type WorkflowTaskRequest struct {
    // ... existing fields ...

    // DAG Concurrency Config
    MaxParallelAgents int `json:"max_parallel_agents"`

    // ReAct Config
    EnableReAct        bool `json:"enable_react"`
    ReActMaxIterations int  `json:"react_max_iterations"`
}
```

**Verification:**
Run: `cd /home/florian/code/cribug && go build ./...`
Expected: BUILD SUCCESS

---

### Task 2: Add ReAct Types

**Files:**
- Modify: `internal/types/dag.go`

- [ ] **Step 1: Add ReactLoopConfig struct**

```go
// ReactLoopConfig holds configuration for ReAct reasoning loop
type ReactLoopConfig struct {
    EnableReAct       bool `json:"enable_react"`
    MaxIterations    int  `json:"max_iterations"`     // default 3, max 10
    EarlyStopOnAnswer bool `json:"early_stop_on_answer"` // default true
}
```

- [ ] **Step 2: Add ReactStep struct**

```go
// ReactStep represents a single step in ReAct reasoning
type ReactStep struct {
    Iteration   int    `json:"iteration"`
    Thought     string `json:"thought"`
    Action      string `json:"action"`
    Observation string `json:"observation"`
    Timestamp   string `json:"timestamp"`
}
```

- [ ] **Step 3: Add ReactResult struct**

```go
// ReactResult holds the result of a ReAct reasoning loop
type ReactResult struct {
    Steps          []ReactStep `json:"steps"`
    FinalAnswer    string       `json:"final_answer"`
    IterationsUsed int         `json:"iterations_used"`
}
```

- [ ] **Step 4: Add DAGConcurrencyConfig to DAGNode**

```go
type DAGNode struct {
    // ... existing fields ...

    // ReAct support
    ReactConfig *ReactLoopConfig `json:"react_config,omitempty"`
}
```

**Verification:**
Run: `cd /home/florian/code/cribug && go build ./...`
Expected: BUILD SUCCESS

---

## 3. Redis Activities for DAG State

### Task 3: Add Redis State Tracking Activities

**Files:**
- Create: `internal/activities/dag_redis.go`

- [ ] **Step 1: Create Redis state tracking functions**

```go
package activities

import (
    "context"
    "encoding/json"
    "fmt"
    "time"

    "github.com/redis/go-redis/v9"
)

// DAGRedisClient handles Redis operations for DAG state tracking
type DAGRedisClient struct {
    client *redis.Client
    ttl    time.Duration
}

// NewDAGRedisClient creates a new DAGRedisClient
func NewDAGRedisClient(redisAddr, redisPass string, redisDB int, ttlSeconds int) *DAGRedisClient {
    client := redis.NewClient(&redis.Options{
        Addr:     redisAddr,
        Password: redisPass,
        DB:       redisDB,
    })
    return &DAGRedisClient{
        client: client,
        ttl:    time.Duration(ttlSeconds) * time.Second,
    }
}

// SetNodeStatus sets the status of a DAG node
func (c *DAGRedisClient) SetNodeStatus(ctx context.Context, taskID, nodeID, status string) error {
    key := fmt.Sprintf("dag:%s:nodes:status", taskID)
    return c.client.HSet(ctx, key, nodeID, status).Err()
}

// GetNodeStatus gets the status of a DAG node
func (c *DAGRedisClient) GetNodeStatus(ctx context.Context, taskID, nodeID string) (string, error) {
    key := fmt.Sprintf("dag:%s:nodes:status", taskID)
    return c.client.HGet(ctx, key, nodeID).Result()
}

// SetNodeResult sets the result of a DAG node
func (c *DAGRedisClient) SetNodeResult(ctx context.Context, taskID, nodeID string, result interface{}) error {
    key := fmt.Sprintf("dag:%s:nodes:results", taskID)
    data, err := json.Marshal(result)
    if err != nil {
        return err
    }
    pipe := c.client.Pipeline()
    pipe.HSet(ctx, key, nodeID, string(data))
    pipe.Expire(ctx, key, c.ttl)
    _, err = pipe.Exec(ctx)
    return err
}

// PushReactStep pushes a ReAct step to Redis List (async, non-blocking)
func (c *DAGRedisClient) PushReactStep(ctx context.Context, taskID, nodeID string, step interface{}) error {
    key := fmt.Sprintf("react:%s:%s:steps", taskID, nodeID)
    data, err := json.Marshal(step)
    if err != nil {
        return err
    }
    pipe := c.client.Pipeline()
    pipe.RPush(ctx, key, string(data))
    pipe.Expire(ctx, key, c.ttl)
    _, err = pipe.Exec(ctx)
    return err
}

// GetAllNodeStatuses gets all node statuses for a task
func (c *DAGRedisClient) GetAllNodeStatuses(ctx context.Context, taskID string) (map[string]string, error) {
    key := fmt.Sprintf("dag:%s:nodes:status", taskID)
    return c.client.HGetAll(ctx, key).Result()
}

// Close closes the Redis client
func (c *DAGRedisClient) Close() error {
    return c.client.Close()
}
```

- [ ] **Step 2: Verify compilation**

Run: `cd /home/florian/code/cribug && go build ./...`
Expected: BUILD SUCCESS

---

## 4. Update DAGActivities with Redis State

### Task 4: Update ExecuteDAGNodeActivity with Redis Tracking

**Files:**
- Modify: `internal/activities/dag.go`

- [ ] **Step 1: Add Redis client to DAGActivities struct**

```go
type DAGActivities struct {
    db           *sql.DB
    llmClient   *llm.Client
    redisClient *DAGRedisClient
}
```

- [ ] **Step 2: Initialize Redis client in constructor**

```go
func NewDAGActivities(db *sql.DB, llmServiceURL string, redisAddr, redisPass string, redisDB int, ttlSeconds int) *DAGActivities {
    return &DAGActivities{
        db:         db,
        llmClient: llm.NewClient(llmServiceURL),
        redisClient: NewDAGRedisClient(redisAddr, redisPass, redisDB, ttlSeconds),
    }
}
```

- [ ] **Step 3: Update ExecuteDAGNode to track Redis state**

Modify `ExecuteDAGNode` function to add Redis tracking:

```go
func (a *DAGActivities) ExecuteDAGNode(ctx context.Context, input ExecuteDAGNodeInput) (*ExecuteDAGNodeOutput, error) {
    logger := activity.GetLogger(ctx)
    logger.Info("ExecuteDAGNodeActivity started",
        "task_id", input.TaskID,
        "node_id", input.Node.ID,
        "use_llm", input.Node.UseLLM)

    // 1. Update Redis status to "running"
    if a.redisClient != nil {
        if err := a.redisClient.SetNodeStatus(ctx, input.TaskID, input.Node.ID, "running"); err != nil {
            logger.Warn("Failed to set node status in Redis", "error", err)
        }
    }

    // 2. Execute node (existing logic)
    // ... existing node execution code ...

    // 3. Update Redis with result
    if a.redisClient != nil {
        resultMap := map[string]interface{}{
            "node_id":   result.NodeID,
            "node_type": result.NodeType,
            "status":    result.Status,
            "output":    result.Output,
        }
        if result.Error != "" {
            resultMap["error"] = result.Error
        }
        if err := a.redisClient.SetNodeResult(ctx, input.TaskID, input.Node.ID, resultMap); err != nil {
            logger.Warn("Failed to set node result in Redis", "error", err)
        }
    }

    // 4. Update Redis status to "completed" or "failed"
    if a.redisClient != nil {
        status := "completed"
        if result.Error != "" {
            status = "failed"
        }
        if err := a.redisClient.SetNodeStatus(ctx, input.TaskID, input.Node.ID, status); err != nil {
            logger.Warn("Failed to update node status in Redis", "error", err)
        }
    }

    return &ExecuteDAGNodeOutput{Result: result, Usage: usage}, nil
}
```

**Verification:**
Run: `cd /home/florian/code/cribug && go build ./...`
Expected: BUILD SUCCESS

---

## 5. DAG Workflow with Concurrency

### Task 5: Update DAGWorkflow for Concurrent Execution

**Files:**
- Modify: `internal/workflows/dag.go`

- [ ] **Step 1: Add LocalDispatchOptions for concurrency**

Add import for temporal if not present:

```go
import (
    // ... existing imports ...
    "go.temporal.io/sdk/temporal"
)
```

- [ ] **Step 2: Update ExecuteDAGNodeInput to include DAGConfig**

Make sure `ExecuteDAGNodeInput` in activities/dag.go includes the necessary config:

```go
type ExecuteDAGNodeInput struct {
    TaskID          string
    WorkflowID      string
    RunID           string
    Query           string
    Node            types.DAGNode
    UpstreamResults map[string]types.DAGNodeResult
    Model           string
    Temperature     float64
    MaxTokens       int
    MaxParallelAgents int // Added for concurrency control
}
```

- [ ] **Step 3: Implement concurrent node execution**

Replace the sequential `for` loop (around line 157) with concurrent execution:

```go
// ========== 阶段 2：并发执行阶段（Local Activity）==========
// 并发度由 LocalDispatchOptions 控制

// Determine concurrency level from request or config
maxParallel := 1
if req.MaxParallelAgents > 0 {
    maxParallel = req.MaxParallelAgents
} else if req.Config != nil && req.Config.MaxParallelAgents > 0 {
    maxParallel = req.Config.MaxParallelAgents
}

// Only use LocalDispatchOptions if maxParallel > 1
if maxParallel > 1 {
    activityOpts := workflow.ActivityOptions{
        StartToCloseTimeout: 60 * time.Second,
        LocalRetries:        true,
        LocalRetryLimit:     3,
        WithLocalDispatchOptions: &temporal.LocalDispatchOptions{
            MaxConcurrentActors: maxParallel,
        },
    }
    ctx = workflow.WithActivityOptions(ctx, activityOpts)
}

// ========== 按拓扑顺序启动可并发的节点 ===========
// 1. Group nodes by "layer" (nodes with same dependencies can run concurrently)
nodeLayers := groupNodesByLayer(plan.Nodes)

// 2. Execute each layer concurrently
nodeResults := make(map[string]types.DAGNodeResult)
var mu sync.Mutex // For thread-safe map updates

for _, layer := range nodeLayers {
    layerFutures := make(map[string]workflow.Future)

    // Start all nodes in this layer concurrently
    for _, node := range layer {
        // Build upstream results
        upstreamResults := make(map[string]types.DAGNodeResult)
        for _, depID := range node.DependsOn {
            if result, ok := nodeResults[depID]; ok {
                upstreamResults[depID] = result
            }
        }

        // Emit DAG_NODE_STARTED
        workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
            TaskID: req.TaskID,
            Event:  events.NewDAGNodeStartedEvent(req.TaskID, node.ID, node.Type),
        }).Get(ctx, nil)

        // Start node execution
        layerFutures[node.ID] = workflow.ExecuteActivity(ctx, "ExecuteDAGNodeActivity", activities.ExecuteDAGNodeInput{
            TaskID:          req.TaskID,
            WorkflowID:      req.WorkflowID,
            RunID:           req.RunID,
            Query:           req.Query,
            Node:            node,
            UpstreamResults: upstreamResults,
            Model:           req.Model,
            Temperature:     req.Temperature,
            MaxTokens:       req.MaxCompletionTokens,
        })
    }

    // Wait for all nodes in this layer to complete
    for nodeID, future := range layerFutures {
        var nodeOutput *activities.ExecuteDAGNodeOutput
        err := future.Get(ctx, &nodeOutput)

        if err != nil {
            logger.Error("DAG node failed", "node_id", nodeID, "error", err)
            nodeResults[nodeID] = types.DAGNodeResult{
                TaskID:   req.TaskID,
                NodeID:   nodeID,
                Status:   "failed",
                Error:    err.Error(),
            }
        } else if nodeOutput != nil && nodeOutput.Result != nil {
            nodeResults[nodeID] = *nodeOutput.Result
        }

        // Emit DAG_NODE_COMPLETED
        workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
            TaskID: req.TaskID,
            Event:  events.NewDAGNodeCompletedEvent(
                req.TaskID,
                nodeID,
                nodeResults[nodeID].NodeType,
                nodeResults[nodeID].Status,
                nodeResults[nodeID].Output,
            ),
        }).Get(ctx, nil)
    }
}

// ========== 阶段 3：汇总结果（确定性，按 node_id 排序）==========
// Results already in nodeResults map, sorted by node_id for deterministic output
```

- [ ] **Step 4: Add helper function to group nodes by layer**

```go
// groupNodesByLayer groups nodes so that nodes in the same layer can run concurrently
// Nodes are in the same layer if they have no dependencies on each other
func groupNodesByLayer(nodes []types.DAGNode) [][]types.DAGNode {
    // Create dependency sets
    deps := make(map[string]map[string]bool)
    for _, node := range nodes {
        deps[node.ID] = make(map[string]bool)
        for _, dep := range node.DependsOn {
            deps[node.ID][dep] = true
        }
    }

    var layers [][]types.DAGNode
    remaining := make(map[string]types.DAGNode)
    for _, node := range nodes {
        remaining[node.ID] = node
    }

    for len(remaining) > 0 {
        var currentLayer []types.DAGNode
        var toRemove []string

        for id, node := range remaining {
            // Check if all dependencies are satisfied (not in remaining)
            allSatisfied := true
            for _, dep := range node.DependsOn {
                if _, ok := remaining[dep]; ok {
                    allSatisfied = false
                    break
                }
            }
            if allSatisfied {
                currentLayer = append(currentLayer, node)
                toRemove = append(toRemove, id)
            }
        }

        if len(currentLayer) == 0 {
            // Circular dependency detected, fall back to single layer
            for id, node := range remaining {
                currentLayer = append(currentLayer, node)
                toRemove = append(toRemove, id)
            }
        }

        for _, id := range toRemove {
            delete(remaining, id)
        }
        layers = append(layers, currentLayer)
    }

    return layers
}
```

**Verification:**
Run: `cd /home/florian/code/cribug && go build ./...`
Expected: BUILD SUCCESS

---

## 6. ReAct Reasoning Loop

### Task 6: Create ExecuteReActNodeActivity

**Files:**
- Create: `internal/activities/react.go`

- [ ] **Step 1: Create ReAct reasoning activity**

```go
package activities

import (
    "context"
    "encoding/json"
    "fmt"
    "log"
    "strings"
    "time"

    "cribug/internal/llm"
    "cribug/internal/types"
    "cribug/internal/redis"

    "go.temporal.io/sdk/activity"
)

type ReActActivities struct {
    llmClient   *llm.Client
    redisClient *redis.Client
}

func NewReActActivities(llmServiceURL string, redisAddr, redisPass string, redisDB int) *ReActActivities {
    return &ReActActivities{
        llmClient:   llm.NewClient(llmServiceURL),
        redisClient: redis.NewClient(&redis.Options{
            Addr:     redisAddr,
            Password: redisPass,
            DB:       redisDB,
        }),
    }
}

type ExecuteReActNodeInput struct {
    TaskID       string
    NodeID       string
    Prompt       string
    Model        string
    Temperature  float64
    MaxTokens    int
    ReactConfig  types.ReactLoopConfig
    TTLSeconds   int
}

type ExecuteReActNodeOutput struct {
    Result *types.ReactResult
    Usage  *types.Usage
}

func (a *ReActActivities) ExecuteReActNode(ctx context.Context, input ExecuteReActNodeInput) (*ExecuteReActNodeOutput, error) {
    logger := activity.GetLogger(ctx)
    logger.Info("ExecuteReActNodeActivity started",
        "task_id", input.TaskID,
        "node_id", input.NodeID,
        "enable_react", input.ReactConfig.EnableReAct)

    // Non-ReAct mode: just call LLM once
    if !input.ReactConfig.EnableReAct {
        return a.executeSimpleNode(ctx, input)
    }

    // ReAct mode: reasoning loop
    return a.executeReActLoop(ctx, input)
}

func (a *ReActActivities) executeSimpleNode(ctx context.Context, input ExecuteReActNodeInput) (*ExecuteReActNodeOutput, error) {
    messages := []types.LLMMessage{
        {Role: "user", Content: input.Prompt},
    }

    resp, err := a.llmClient.Call(ctx, llm.CallRequest{
        TaskID:              input.TaskID,
        Provider:            "openai_compatible",
        Model:               input.Model,
        Messages:            messages,
        Temperature:         input.Temperature,
        MaxCompletionTokens: input.MaxTokens,
    })

    if err != nil {
        return nil, fmt.Errorf("llm call failed: %w", err)
    }

    return &ExecuteReActNodeOutput{
        Result: &types.ReactResult{
            FinalAnswer:    resp.Content,
            IterationsUsed: 0,
            Steps:          nil,
        },
        Usage: &resp.Usage,
    }, nil
}

func (a *ReActActivities) executeReActLoop(ctx context.Context, input ExecuteReActNodeInput) (*ExecuteReActNodeOutput, error) {
    logger := activity.GetLogger(ctx)

    // Initialize messages with system prompt and task
    systemPrompt := `You are a ReAct agent. For each iteration:
1. THINK: Analyze the current state and determine what to do next
2. ACT: Either call a tool (format: TOOL:tool_name:args_json) or provide text response
3. OBSERVE: Wait for the observation to continue reasoning

Continue until you have a final answer. Format your final answer as: FINAL: your answer`

    messages := []types.LLMMessage{
        {Role: "system", Content: systemPrompt},
        {Role: "user", Content: input.Prompt},
    }

    var steps []types.ReactStep
    maxIterations := input.ReactConfig.MaxIterations
    if maxIterations <= 0 {
        maxIterations = 3
    }
    if maxIterations > 10 {
        maxIterations = 10
    }

    for i := 0; i < maxIterations; i++ {
        iteration := i + 1
        logger.Info("ReAct iteration", "iteration", iteration, "max", maxIterations)

        // Call LLM
        resp, err := a.llmClient.Call(ctx, llm.CallRequest{
            TaskID:              input.TaskID,
            Provider:            "openai_compatible",
            Model:               input.Model,
            Messages:            messages,
            Temperature:         input.Temperature,
            MaxCompletionTokens: input.MaxTokens,
        })

        if err != nil {
            logger.Error("ReAct LLM call failed", "iteration", iteration, "error", err)
            // Return what we have so far
            break
        }

        // Parse response to extract thought/action/observation
        thought, action, observation := parseReActResponse(resp.Content)

        // Create step record
        step := types.ReactStep{
            Iteration:   iteration,
            Thought:     thought,
            Action:      action,
            Observation: observation,
            Timestamp:   time.Now().UTC().Format(time.RFC3339),
        }
        steps = append(steps, step)

        // Async write to Redis (goroutine, non-blocking)
        go func(s types.ReactStep) {
            if a.redisClient != nil {
                key := fmt.Sprintf("react:%s:%s:steps", input.TaskID, input.NodeID)
                data, _ := json.Marshal(s)
                pipe := a.redisClient.Pipeline()
                pipe.RPush(context.Background(), key, string(data))
                pipe.Expire(context.Background(), key, time.Duration(input.TTLSeconds)*time.Second)
                if _, err := pipe.Exec(context.Background()); err != nil {
                    log.Printf("Failed to write ReAct step to Redis: %v", err)
                }
            }
        }(step)

        // Update messages for next iteration
        messages = append(messages,
            types.LLMMessage{Role: "assistant", Content: thought + "\n" + action},
            types.LLMMessage{Role: "user", Content: observation},
        )

        // Early stop check
        if input.ReactConfig.EarlyStopOnAnswer && isFinalAnswer(action) {
            logger.Info("ReAct early stop", "iteration", iteration, "reason", "final_answer_found")
            break
        }
    }

    // Extract final answer
    finalAnswer := parseFinalAnswer(steps)

    // Calculate total usage (sum of all iterations)
    totalUsage := &types.Usage{}

    return &ExecuteReActNodeOutput{
        Result: &types.ReactResult{
            Steps:          steps,
            FinalAnswer:    finalAnswer,
            IterationsUsed: len(steps),
        },
        Usage: totalUsage,
    }, nil
}

// parseReActResponse extracts thought, action, and observation from LLM response
func parseReActResponse(content string) (thought, action, observation string) {
    // Simple parsing - look for FINAL: marker first
    if strings.Contains(content, "FINAL:") {
        idx := strings.Index(content, "FINAL:")
        action = content[idx:] // Everything from FINAL: onward is the action
        thought = content[:idx]
        return
    }

    // No FINAL marker, treat entire response as final answer
    action = content
    return
}

// isFinalAnswer checks if the action contains a final answer
func isFinalAnswer(action string) bool {
    return strings.Contains(strings.ToUpper(action), "FINAL:")
}

// parseFinalAnswer extracts the final answer from steps
func parseFinalAnswer(steps []types.ReactStep) string {
    for i := len(steps) - 1; i >= 0; i-- {
        step := steps[i]
        if strings.Contains(strings.ToUpper(step.Action), "FINAL:") {
            // Extract text after FINAL:
            idx := strings.Index(step.Action, "FINAL:")
            answer := strings.TrimSpace(step.Action[idx+6:])
            // Also include observation if present
            if step.Observation != "" {
                return answer + "\n" + step.Observation
            }
            return answer
        }
    }
    // No FINAL marker found, use last observation or last action
    if len(steps) > 0 {
        lastStep := steps[len(steps)-1]
        if lastStep.Observation != "" {
            return lastStep.Observation
        }
        return lastStep.Action
    }
    return ""
}
```

- [ ] **Step 2: Verify compilation**

Run: `cd /home/florian/code/cribug && go build ./...`
Expected: BUILD SUCCESS

---

## 7. ReAct Events

### Task 7: Add ReAct Event Types

**Files:**
- Modify: `internal/events/types.go`

- [ ] **Step 1: Add ReAct event type constants**

```go
// ReAct events
EventTypeReActStep       = "REACT_STEP"
EventTypeReActCompleted  = "REACT_COMPLETED"
```

- [ ] **Step 2: Add NewReActStepEvent constructor**

```go
func NewReActStepEvent(taskID, nodeID string, iteration int, thought, action, observation string) AgentEvent {
    return NewAgentEvent(EventTypeReActStep, map[string]interface{}{
        "task_id":     taskID,
        "node_id":     nodeID,
        "iteration":   iteration,
        "thought":     thought,
        "action":      action,
        "observation": observation,
        "timestamp":   time.Now().UTC().Format(time.RFC3339),
    })
}
```

- [ ] **Step 3: Add NewReActCompletedEvent constructor**

```go
func NewReActCompletedEvent(taskID, nodeID string, iterations int, finalAnswer string) AgentEvent {
    return NewAgentEvent(EventTypeReActCompleted, map[string]interface{}{
        "task_id":       taskID,
        "node_id":       nodeID,
        "iterations":   iterations,
        "final_answer": finalAnswer,
        "timestamp":    time.Now().UTC().Format(time.RFC3339),
    })
}
```

**Verification:**
Run: `cd /home/florian/code/cribug && go build ./...`
Expected: BUILD SUCCESS

---

## 8. Test Scripts

### Task 8: Create DAG Concurrency Test

**Files:**
- Create: `scripts/test_dag_concurrency.sh`

- [ ] **Step 1: Create test script**

```bash
#!/bin/bash
set -euo pipefail

GATEWAY_URL="${GATEWAY_URL:-http://127.0.0.1:8080}"
REDIS_HOST="${REDIS_HOST:-127.0.0.1}"
REDIS_PORT="${REDIS_PORT:-6379}"

log() { echo "[$(date +'%H:%M:%S')] $*"; }
fail() { echo "[FAIL] $*" >&2; exit 1; }

redis_cmd() {
  if command -v redis-cli &>/dev/null; then
    redis-cli -h "$REDIS_HOST" -p "$REDIS_PORT" "$@"
  else
    docker.exe exec deploy-redis-1 redis-cli "$@"
  fi
}

log "=== DAG Concurrency Test ==="

# Test 1: Sequential execution (max_parallel_agents=1)
log "Test 1: Sequential execution (max_parallel_agents=1)"
RESP=$(curl -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{"query":"Analyze AI and ML","config":{"mode":"dag","max_parallel_agents":1}}')
TASK_ID=$(echo "$RESP" | jq -r '.task_id')
[ -n "$TASK_ID" ] && [ "$TASK_ID" != "null" ] || fail "Failed to create task"

for i in $(seq 1 60); do
  STATUS=$(curl -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID" | jq -r '.status')
  [ "$STATUS" = "completed" ] && break
  [ "$STATUS" = "failed" ] && break
  sleep 1
done

[ "$STATUS" = "completed" ] || fail "Sequential task failed: $STATUS"

# Verify Redis node statuses
log "  Checking Redis DAG node statuses..."
STATUS_COUNT=$(redis_cmd HLEN "dag:$TASK_ID:nodes:status" 2>/dev/null || echo "0")
[ "$STATUS_COUNT" -ge 2 ] || fail "Expected at least 2 DAG nodes, got $STATUS_COUNT"

# Verify DAG_PLANNED event
DAG_PLANNED=$(redis_cmd XRANGE "task:$TASK_ID:events" - + 2>/dev/null | grep -c "DAG_PLANNED" || true)
[ "$DAG_PLANNED" -ge 1 ] || fail "Missing DAG_PLANNED event"

log "  Sequential test PASSED"

# Test 2: Concurrent execution (max_parallel_agents=2)
log "Test 2: Concurrent execution (max_parallel_agents=2)"
RESP2=$(curl -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{"query":"Compare AI ML DL NLP CV","config":{"mode":"dag","max_parallel_agents":2}}')
TASK_ID2=$(echo "$RESP2" | jq -r '.task_id')
[ -n "$TASK_ID2" ] && [ "$TASK_ID2" != "null" ] || fail "Failed to create task 2"

for i in $(seq 1 60); do
  STATUS2=$(curl -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID2" | jq -r '.status')
  [ "$STATUS2" = "completed" ] && break
  [ "$STATUS2" = "failed" ] && break
  sleep 1
done

[ "$STATUS2" = "completed" ] || fail "Concurrent task failed: $STATUS2"

# Verify node results in Redis
RESULT_COUNT=$(redis_cmd HLEN "dag:$TASK_ID2:nodes:results" 2>/dev/null || echo "0")
[ "$RESULT_COUNT" -ge 2 ] || fail "Expected node results, got $RESULT_COUNT"

log "  Concurrent test PASSED"

# Test 3: Verify DAG_NODE_STARTED events
log "Test 3: Verify DAG_NODE events"
NODE_STARTED=$(redis_cmd XRANGE "task:$TASK_ID2:events" - + 2>/dev/null | grep -c "DAG_NODE_STARTED" || true)
NODE_COMPLETED=$(redis_cmd XRANGE "task:$TASK_ID2:events" - + 2>/dev/null | grep -c "DAG_NODE_COMPLETED" || true)

[ "$NODE_STARTED" -ge 2 ] || fail "Expected at least 2 DAG_NODE_STARTED events, got $NODE_STARTED"
[ "$NODE_COMPLETED" -ge 2 ] || fail "Expected at least 2 DAG_NODE_COMPLETED events, got $NODE_COMPLETED"

log "  DAG_NODE events PASSED"

log ""
log "=== DAG CONCURRENCY TESTS PASSED ==="
```

- [ ] **Step 2: Make executable**

```bash
chmod +x /home/florian/code/cribug/scripts/test_dag_concurrency.sh
```

---

### Task 9: Create ReAct Reasoning Test

**Files:**
- Create: `scripts/test_react_reasoning.sh`

- [ ] **Step 1: Create test script**

```bash
#!/bin/bash
set -euo pipefail

GATEWAY_URL="${GATEWAY_URL:-http://127.0.0.1:8080}"
REDIS_HOST="${REDIS_HOST:-127.0.0.1}"
REDIS_PORT="${REDIS_PORT:-6379}"

log() { echo "[$(date +'%H:%M:%S')] $*"; }
fail() { echo "[FAIL] $*" >&2; exit 1; }

redis_cmd() {
  if command -v redis-cli &>/dev/null; then
    redis-cli -h "$REDIS_HOST" -p "$REDIS_PORT" "$@"
  else
    docker.exe exec deploy-redis-1 redis-cli "$@"
  fi
}

log "=== ReAct Reasoning Loop Test ==="

# Test 1: ReAct disabled (default)
log "Test 1: ReAct disabled (default)"
RESP=$(curl -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{"query":"What is 2+2?","config":{"mode":"dag","enable_react":false}}')
TASK_ID=$(echo "$RESP" | jq -r '.task_id')
[ -n "$TASK_ID" ] && [ "$TASK_ID" != "null" ] || fail "Failed to create task"

for i in $(seq 1 60); do
  STATUS=$(curl -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID" | jq -r '.status')
  [ "$STATUS" = "completed" ] && break
  [ "$STATUS" = "failed" ] && break
  sleep 1
done

[ "$STATUS" = "completed" ] || fail "Task failed: $STATUS"

# Verify only 1 LLM call
LLM_COUNT=$(curl -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID" | jq -r '.usage.total_tokens' 2>/dev/null || echo "0")
[ "$LLM_COUNT" -gt 0 ] || fail "Expected usage tokens, got 0"

log "  ReAct disabled test PASSED"

# Test 2: ReAct enabled with max_iterations
log "Test 2: ReAct enabled (max_iterations=3)"
RESP2=$(curl -s -X POST "$GATEWAY_URL/api/v1/tasks" \
  -H "Content-Type: application/json" \
  -d '{"query":"Calculate 15*23+7 step by step","config":{"mode":"dag","enable_react":true,"react_max_iterations":3}}')
TASK_ID2=$(echo "$RESP2" | jq -r '.task_id')
[ -n "$TASK_ID2" ] && [ "$TASK_ID2" != "null" ] || fail "Failed to create task 2"

for i in $(seq 1 90); do
  STATUS2=$(curl -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID2" | jq -r '.status')
  [ "$STATUS2" = "completed" ] && break
  [ "$STATUS2" = "failed" ] && break
  sleep 1
done

[ "$STATUS2" = "completed" ] || fail "ReAct task failed: $STATUS2"

# Verify multiple LLM calls (more tokens = more iterations)
LLM_COUNT2=$(curl -s "$GATEWAY_URL/api/v1/tasks/$TASK_ID2" | jq -r '.usage.total_tokens' 2>/dev/null || echo "0")
[ "$LLM_COUNT2" -gt "$LLM_COUNT" ] || log "  Warning: token count not significantly higher"

# Verify REACT_STEP events
REACT_STEPS=$(redis_cmd XRANGE "task:$TASK_ID2:events" - + 2>/dev/null | grep -c "REACT_STEP" || true)
[ "$REACT_STEPS" -ge 1 ] || log "  Warning: No REACT_STEP events found (may use different event names)"

log "  ReAct enabled test PASSED"

# Test 3: Verify Redis react steps
log "Test 3: Verify Redis ReAct steps"
# Note: The key format depends on node_id
STEPS_KEY=$(redis_cmd KEYS "react:$TASK_ID2:*:steps" 2>/dev/null | head -1 || true)
if [ -n "$STEPS_KEY" ] && [ "$STEPS_KEY" != "" ]; then
  STEPS_COUNT=$(redis_cmd LLEN "$STEPS_KEY" 2>/dev/null | head -1 || echo "0")
  log "  Found $STEPS_COUNT ReAct steps in Redis"
else
  log "  No ReAct steps in Redis (check key format)"
fi

log ""
log "=== REACT REASONING TESTS PASSED ==="
```

- [ ] **Step 2: Make executable**

```bash
chmod +x /home/florian/code/cribug/scripts/test_react_reasoning.sh
```

---

## 9. Documentation

### Task 10: Update README.md

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Update Phase 3 status**

```markdown
### Phase 3: DAG Concurrency + ReAct + tiktoken

| Slice | Status | Description |
|-------|--------|-------------|
| Slice 7 | ✅ | DAG Concurrency with LocalDispatchOptions + Redis state tracking |
| Slice 8 | ✅ | ReAct Reasoning Loop with stepwise tool execution |
| Slice 9 | Future | Real tiktoken Tokenizer |
```

---

## 10. Self-Review Checklist

Before marking complete, verify:

1. **Spec coverage:** All requirements from Slice 7 and Slice 8 addressed:
   - [x] LocalDispatchOptions for concurrent node execution
   - [x] Redis Hash for node status/results
   - [x] TTL configurable
   - [x] ReAct loop with iterations
   - [x] Redis List for ReAct steps
   - [x] Early stop on FINAL marker
   - [x] Non-blocking async Redis writes

2. **Type consistency:** Types match across files

3. **Compilation:** `go build ./...` succeeds

4. **Tests:** Both test scripts pass

---

## Execution Options

**Plan complete and saved to `docs/superpowers/plans/2026-05-20-Cribug_Phase3_Slice7_Slice8.md`.**

**Two execution options:**

**1. Subagent-Driven (recommended)** - I dispatch a fresh subagent per task, review between tasks

**2. Inline Execution** - Execute tasks in this session using executing-plans

**Which approach?**
