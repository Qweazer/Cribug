# Phase 4 Slice 10.0 Design — DAG Visualization & ReAct Observability

## Overview

Add observability to Phase 3 DAG/ReAct workflows without modifying core logic.

**Architecture:** Workflow calls Activity → Activity writes Redis + triggers SSE

## 1. DAG Visualization

### 1.1 New Events (`internal/events/types.go`)

| Event | Description |
|-------|-------------|
| `DAG_NODE_PENDING` | Node queued, waiting for dependencies |
| `DAG_NODE_RUNNING` | Node executing (replaces `DAG_NODE_STARTED`) |
| `DAG_NODE_FAILED` | Node execution failed |

### 1.2 New Types (`internal/types/dag_visual.go`)

```go
type DAGNodeStatus struct {
    NodeID        string   `json:"node_id"`
    Status        string   `json:"status"`
    Layer         int      `json:"layer"`
    Dependencies  []string `json:"dependencies,omitempty"`
    StartedAtNs   int64    `json:"started_at_ns,omitempty"`
    CompletedAtNs int64    `json:"completed_at_ns,omitempty"`
    Error         string   `json:"error,omitempty"`
}

type DAGVisualMeta struct {
    TaskID         string `json:"task_id"`
    WorkflowID     string `json:"workflow_id"`
    TotalNodes     int    `json:"total_nodes"`
    PendingNodes   int    `json:"pending_nodes"`
    RunningNodes   int    `json:"running_nodes"`
    CompletedNodes int    `json:"completed_nodes"`
    FailedNodes    int    `json:"failed_nodes"`
    UpdatedAtNs    int64  `json:"updated_at_ns"`
}

type DAGVisualSnapshot struct {
    TaskID     string                `json:"task_id"`
    WorkflowID string                `json:"workflow_id"`
    Meta       DAGVisualMeta         `json:"meta"`
    Nodes      map[string]DAGNodeStatus `json:"nodes"`
}
```

### 1.3 New Activity (`internal/activities/dag_visual.go`)

| Activity | Function |
|----------|----------|
| `RecordDAGNodeStatusActivity` | Write node status to Redis + emit SSE |
| `BuildDAGVisualSnapshotActivity` | Read from Redis, return snapshot |

### 1.4 Redis Keys

```
dag:{workflow_id}:meta
  - task_id, workflow_id, total_nodes, pending/running/completed/failed_nodes, updated_at_ns

dag:{workflow_id}:nodes
  - node_id → DAGNodeStatus JSON
```

### 1.5 DAG Workflow Integration

In `dag.go`: insert `RecordDAGNodeStatusActivity` calls at:
- Node initialization → `DAG_NODE_PENDING`
- Before execution → `DAG_NODE_RUNNING`
- After success → `DAG_NODE_COMPLETED`
- After failure → `DAG_NODE_FAILED`

## 2. ReAct Observability

### 2.1 New Types (`internal/types/metrics.go`)

```go
type AgentMetrics struct {
    AgentRole      string `json:"agent_role"`
    CallCount      int    `json:"call_count"`
    SuccessCount   int    `json:"success_count"`
    FailureCount   int    `json:"failure_count"`
    TotalLatencyMs int64  `json:"total_latency_ms"`
    AvgLatencyMs   int64  `json:"avg_latency_ms"`
    TotalTokens    int    `json:"total_tokens"`
    ToolCallCount  int    `json:"tool_call_count"`
}

type MetricsSummary struct {
    TaskID         string         `json:"task_id"`
    WorkflowID     string         `json:"workflow_id"`
    AgentMetrics   []AgentMetrics `json:"agent_metrics"`
    TotalCallCount int            `json:"total_call_count"`
    TotalTokens    int            `json:"total_tokens"`
}
```

**Constraint:** `AvgLatencyMs = TotalLatencyMs / CallCount` (0 if CallCount==0)

### 2.2 New Activity (`internal/activities/react_observability.go`)

| Activity | Function |
|----------|----------|
| `RecordAgentMetricsActivity` | Increment per-role metrics in Redis |
| `AggregateAgentMetricsActivity` | Build MetricsSummary, write to Redis |
| `EmitMetricsSummaryActivity` | Emit SSE event with summary |

### 2.3 Redis Keys

```
react_metrics:{task_id}:{agent_role}
  - agent_role, call_count, success_count, failure_count,
    total_latency_ms, avg_latency_ms, total_tokens, tool_call_count

react_metrics:{task_id}:summary
  - task_id, workflow_id, total_call_count, total_tokens, agent_metrics[]
```

## 3. SSE Events

| Event | Payload |
|-------|---------|
| `DAG_NODE_PENDING` | task_id, node_id, status, timestamp |
| `DAG_NODE_RUNNING` | task_id, node_id, status, worker_id, timestamp |
| `DAG_NODE_COMPLETED` | task_id, node_id, status, timestamp |
| `DAG_NODE_FAILED` | task_id, node_id, status, error, timestamp |
| `AGENT_METRICS_SUMMARY` | task_id, agent_metrics[], total_call_count, total_tokens |

## 4. Testing

- `scripts/test_dag_visual.sh` — Verify DAG node status in Redis + SSE events
- `scripts/test_react_observability.sh` — Verify agent metrics aggregation + SSE events
- All Phase 3 regression tests must pass

## 5. Constraints

- **No core logic rewrite** of dag.go, dag_redis.go, react.go
- **No new main workflow** — only helper functions
- **No time.Now() in Workflow** — use `workflow.Now(ctx)`
- **No direct Redis access in Workflow** — use Activity
- **No random numbers** in any new code