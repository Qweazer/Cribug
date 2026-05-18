# Python LLM Service + AgentActivity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement mock Python LLM Service and AgentActivity so Workflow returns LLM mock answers instead of "empty workflow completed"

**Architecture:** FastAPI Python service provides /health and /chat endpoints. Go AgentActivity calls /chat in a Temporal Activity. Workflow orchestrates LLM_STARTED/LLM_COMPLETED events via EmitEventActivity and uses AgentActivity output as task result.

**Tech Stack:** Python FastAPI, Go Temporal SDK, Redis Stream, PostgreSQL

---

## File Structure

```
python_llm_service/
  app.py              # FastAPI app with /health and /chat
  requirements.txt   # fastapi, uvicorn, pydantic

internal/
  types/types.go     # Add LLM types (LLMMessage, LLMRequest, Usage, LLMResponse)
  events/types.go    # Add LLM_STARTED, LLM_COMPLETED events
  config/config.go   # Add LLMServiceURL field
  activities/agent.go # Implement AgentActivity

internal/workflows/
  simple.go          # Use AgentActivity instead of hardcoded result

cmd/worker/
  main.go            # Register AgentActivity
```

---

## Task 1: Python LLM Service

**Files:**
- Create: `python_llm_service/app.py`
- Create: `python_llm_service/requirements.txt`

- [ ] **Step 1: Create requirements.txt**

```txt
fastapi
uvicorn
pydantic
```

- [ ] **Step 2: Run syntax check**

Run: `python3 -m py_compile -c "from pathlib import Path; Path('python_llm_service/requirements.txt').touch()" 2>&1 || echo "ignore"`

- [ ] **Step 3: Create FastAPI app**

```python
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel
import time

app = FastAPI()

class LLMMessage(BaseModel):
    role: str
    content: str

class LLMRequest(BaseModel):
    trace_id: str
    task_id: str
    session_id: str | None = None
    provider: str
    model: str
    messages: list[LLMMessage]
    temperature: float = 0.7
    max_completion_tokens: int = 1024
    response_format: str | None = None
    metadata: dict = {}

class Usage(BaseModel):
    prompt_tokens: int
    completion_tokens: int
    total_tokens: int

class LLMResponse(BaseModel):
    content: str
    usage: Usage
    model: str
    provider: str
    finish_reason: str
    provider_response_id: str | None = None
    latency_ms: int
    error: str | None = None

@app.get("/health")
def health():
    return {"status": "healthy"}

@app.post("/chat")
def chat(req: LLMRequest):
    if req.provider != "openai_compatible":
        raise HTTPException(status_code=400, detail="provider must be openai_compatible")
    if not req.messages:
        raise HTTPException(status_code=400, detail="messages cannot be empty")
    
    last_user_content = None
    for msg in reversed(req.messages):
        if msg.role == "user":
            last_user_content = msg.content
            break
    
    if last_user_content is None:
        raise HTTPException(status_code=400, detail="no user message found")
    
    start = time.time()
    content = f"mock answer: {last_user_content}"
    
    prompt_text = " ".join(m.content for m in req.messages)
    prompt_tokens = max(1, len(prompt_text) // 4)
    completion_tokens = max(1, len(content) // 4)
    
    latency_ms = int((time.time() - start) * 1000)
    
    return LLMResponse(
        content=content,
        usage=Usage(
            prompt_tokens=prompt_tokens,
            completion_tokens=completion_tokens,
            total_tokens=prompt_tokens + completion_tokens
        ),
        model=req.model,
        provider=req.provider,
        finish_reason="stop",
        provider_response_id=f"mock-{req.task_id}",
        latency_ms=latency_ms,
        error=None
    )
```

- [ ] **Step 4: Install and test locally**

```bash
cd /home/florian/code/cribug/python_llm_service
python3 -m venv .venv
source .venv/bin/activate
pip install -r requirements.txt
uvicorn app:app --host 0.0.0.0 --port 8000 &
sleep 2
curl --noproxy '*' -s http://127.0.0.1:8000/health | jq
curl --noproxy '*' -s -X POST http://127.0.0.1:8000/chat \
  -H "Content-Type: application/json" \
  -d '{"trace_id":"test","task_id":"t1","provider":"openai_compatible","model":"gpt-4o-mini","messages":[{"role":"user","content":"hello"}]}' | jq
```

Expected: `/health` returns `{"status":"healthy"}`, `/chat` returns `{"content":"mock answer: hello",...}`

- [ ] **Step 5: Commit**

```bash
git add python_llm_service/app.py python_llm_service/requirements.txt
git commit -m "feat: add Python LLM Service with mock /health and /chat"
```

---

## Task 2: Go LLM Types

**Files:**
- Modify: `internal/types/types.go:141-143` (add after WorkflowTaskResult)

- [ ] **Step 1: Add LLM types**

Add after `WorkflowTaskResult` struct in `internal/types/types.go`:

```go
// LLM types for AgentActivity

type LLMMessage struct {
    Role    string `json:"role"`
    Content string `json:"content"`
}

type LLMRequest struct {
    TraceID             string         `json:"trace_id"`
    TaskID              string         `json:"task_id"`
    SessionID           *string        `json:"session_id,omitempty"`
    Provider            string         `json:"provider"`
    Model               string         `json:"model"`
    Messages            []LLMMessage   `json:"messages"`
    Temperature         float64        `json:"temperature"`
    MaxCompletionTokens int            `json:"max_completion_tokens"`
    ResponseFormat      *string        `json:"response_format,omitempty"`
    Metadata            map[string]any `json:"metadata,omitempty"`
}

type Usage struct {
    PromptTokens     int `json:"prompt_tokens"`
    CompletionTokens int `json:"completion_tokens"`
    TotalTokens      int `json:"total_tokens"`
}

type LLMResponse struct {
    Content            string  `json:"content"`
    Usage              Usage   `json:"usage"`
    Model              string  `json:"model"`
    Provider           string  `json:"provider"`
    FinishReason       string  `json:"finish_reason"`
    ProviderResponseID *string `json:"provider_response_id,omitempty"`
    LatencyMS          int64   `json:"latency_ms"`
    Error              *string `json:"error,omitempty"`
}
```

- [ ] **Step 2: Verify build**

Run: `go build ./internal/types/...`
Expected: PASS (no errors)

- [ ] **Step 3: Commit**

```bash
git add internal/types/types.go
git commit -m "feat: add LLM types for AgentActivity"
```

---

## Task 3: LLM Events

**Files:**
- Modify: `internal/events/types.go:1-58` (add LLM event constants and constructors)

- [ ] **Step 1: Add event constants**

Add after line 10 (after `EventTypeTaskFailed`):

```go
const (
    EventTypeTaskCreated      = "TASK_CREATED"
    EventTypeWorkflowStarted  = "WORKFLOW_STARTED"
    EventTypeTaskCompleted    = "TASK_COMPLETED"
    EventTypeTaskFailed       = "TASK_FAILED"
    EventTypeLLMStarted       = "LLM_STARTED"
    EventTypeLLMCompleted     = "LLM_COMPLETED"
)
```

- [ ] **Step 2: Add event constructors**

Add after `NewTaskFailedEvent` function (around line 58):

```go
func NewLLMStartedEvent(taskID, model string) AgentEvent {
    return NewAgentEvent(EventTypeLLMStarted, map[string]interface{}{
        "task_id":   taskID,
        "model":     model,
        "timestamp": time.Now().UTC().Format(time.RFC3339),
    })
}

func NewLLMCompletedEvent(taskID, model, finishReason string, latencyMS int64) AgentEvent {
    return NewAgentEvent(EventTypeLLMCompleted, map[string]interface{}{
        "task_id":      taskID,
        "model":        model,
        "finish_reason": finishReason,
        "latency_ms":   latencyMS,
        "timestamp":    time.Now().UTC().Format(time.RFC3339),
    })
}
```

- [ ] **Step 3: Verify build**

Run: `go build ./internal/events/...`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/events/types.go
git commit -m "feat: add LLM_STARTED and LLM_COMPLETED events"
```

---

## Task 4: Config - LLM Service URL

**Files:**
- Modify: `internal/config/config.go:9-17` (add LLMPort)

- [ ] **Step 1: Add LLMServiceURL field**

In `Config` struct, add after `TemporalTaskQueue`:

```go
LLMServiceURL   string
```

- [ ] **Step 2: Load from environment**

In `Load()` function, after line 27 (`TemporalTaskQueue: getEnv(...)`):

```go
LLMServiceURL:  getEnv("LLM_SERVICE_URL", "http://127.0.0.1:8000"),
```

- [ ] **Step 3: Verify build**

Run: `go build ./internal/config/...`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/config/config.go
git commit -m "feat: add LLM_SERVICE_URL to config"
```

---

## Task 5: AgentActivity Implementation

**Files:**
- Modify: `internal/activities/agent.go` (replace empty file)

- [ ] **Step 1: Implement AgentActivity**

Replace entire `internal/activities/agent.go` content:

```go
package activities

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "time"

    "cribug/internal/types"

    "go.temporal.io/sdk/activity"
)

type AgentActivities struct {
    llmServiceURL string
    httpClient     *http.Client
}

func NewAgentActivities(llmServiceURL string) *AgentActivities {
    return &AgentActivities{
        llmServiceURL: llmServiceURL,
        httpClient: &http.Client{
            Timeout: 60 * time.Second,
        },
    }
}

type AgentActivityInput struct {
    TaskID              string
    WorkflowID          string
    RunID               string
    Query               string
    SessionID           string
    Model               string
    Temperature         float64
    MaxCompletionTokens int
}

type AgentActivityOutput struct {
    Answer    string
    Usage     types.Usage
    Model     string
    Provider  string
    LatencyMS int64
}

func (a *AgentActivities) CallLLM(ctx context.Context, input AgentActivityInput) (*AgentActivityOutput, error) {
    logger := activity.GetLogger(ctx)
    logger.Info("AgentActivity started", "task_id", input.TaskID, "model", input.Model)

    reqBody := types.LLMRequest{
        TraceID:             input.TaskID,
        TaskID:              input.TaskID,
        Provider:           "openai_compatible",
        Model:               input.Model,
        Messages:           []types.LLMMessage{{Role: "user", Content: input.Query}},
        Temperature:         input.Temperature,
        MaxCompletionTokens: input.MaxCompletionTokens,
    }
    if input.SessionID != "" {
        reqBody.SessionID = &input.SessionID
    }
    reqBody.Metadata = map[string]any{
        "workflow_id": input.WorkflowID,
        "run_id":      input.RunID,
    }

    jsonBody, err := json.Marshal(reqBody)
    if err != nil {
        return nil, fmt.Errorf("marshal request: %w", err)
    }

    resp, err := a.httpClient.Post(
        a.llmServiceURL+"/chat",
        "application/json",
        bytes.NewBuffer(jsonBody),
    )
    if err != nil {
        log.Printf("[ERROR] AgentActivity: HTTP call failed: %v", err)
        return nil, fmt.Errorf("http call: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode < 200 || resp.StatusCode >= 300 {
        return nil, fmt.Errorf("llm service returned status %d", resp.StatusCode)
    }

    var llmResp types.LLMResponse
    if err := json.NewDecoder(resp.Body).Decode(&llmResp); err != nil {
        return nil, fmt.Errorf("decode response: %w", err)
    }

    if llmResp.Error != nil && *llmResp.Error != "" {
        return nil, fmt.Errorf("llm error: %s", *llmResp.Error)
    }

    if llmResp.Content == "" {
        return nil, fmt.Errorf("empty content from llm")
    }

    logger.Info("AgentActivity completed", "task_id", input.TaskID, "latency_ms", llmResp.LatencyMS)

    return &AgentActivityOutput{
        Answer:    llmResp.Content,
        Usage:     llmResp.Usage,
        Model:     llmResp.Model,
        Provider:  llmResp.Provider,
        LatencyMS: llmResp.LatencyMS,
    }, nil
}
```

- [ ] **Step 2: Verify build**

Run: `go build ./internal/activities/...`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/activities/agent.go
git commit -m "feat: implement AgentActivity with HTTP call to LLM service"
```

---

## Task 6: Worker Registration

**Files:**
- Modify: `cmd/worker/main.go:41-54` (register AgentActivity)

- [ ] **Step 1: Import config for LLMServiceURL**

After line 9 (after `redisclient "cribug/internal/redis"`):

```go
    _ "embed"
```

- [ ] **Step 2: Create AgentActivities after other activities**

After line 43 (after `emitEventActivity := activities.NewEmitEventActivity(redisClient)`):

```go
    agentActivities := activities.NewAgentActivities(cfg.LLMServiceURL)
```

- [ ] **Step 3: Register AgentActivity**

After line 54 (after `w.RegisterActivityWithOptions(execActivities.RecordFailed, ...)`):

```go
    w.RegisterActivityWithOptions(agentActivities.CallLLM, activity.RegisterOptions{Name: "AgentActivity"})
```

- [ ] **Step 4: Verify build**

Run: `go build ./cmd/worker`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/worker/main.go
git commit -m "feat: register AgentActivity in worker"
```

---

## Task 7: Workflow Modification

**Files:**
- Modify: `internal/workflows/simple.go:1-93` (replace workflow logic)

- [ ] **Step 1: Replace Execute function**

Replace the `Execute` function in `simple.go`. Replace lines 21-93 with:

```go
func (sw *SimpleWorkflow) Execute(ctx workflow.Context, req types.WorkflowTaskRequest) (*types.WorkflowTaskResult, error) {
    logger := workflow.GetLogger(ctx)
    logger.Info("SimpleWorkflow started", "task_id", req.TaskID, "workflow_id", req.WorkflowID)

    opts := workflow.ActivityOptions{
        StartToCloseTimeout: 90 * time.Second,
        RetryPolicy: &temporal.RetryPolicy{
            InitialInterval:    1 * time.Second,
            BackoffCoefficient: 2.0,
            MaximumInterval:    10 * time.Second,
            MaximumAttempts:    3,
        },
    }
    ctx = workflow.WithActivityOptions(ctx, opts)

    // 1. Emit WORKFLOW_STARTED
    err := workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
        TaskID: req.TaskID,
        Event:  events.NewWorkflowStartedEvent(req.TaskID, req.WorkflowID),
    }).Get(ctx, nil)
    if err != nil {
        logger.Error("EmitEventActivity (WORKFLOW_STARTED) failed", "error", err)
    }

    // 2. Emit LLM_STARTED
    err = workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
        TaskID: req.TaskID,
        Event:  events.NewLLMStartedEvent(req.TaskID, req.Model),
    }).Get(ctx, nil)
    if err != nil {
        logger.Error("EmitEventActivity (LLM_STARTED) failed", "error", err)
    }

    // 3. Execute AgentActivity
    var agentOutput *activities.AgentActivityOutput
    err = workflow.ExecuteActivity(ctx, "AgentActivity", activities.AgentActivityInput{
        TaskID:              req.TaskID,
        WorkflowID:          req.WorkflowID,
        RunID:               req.RunID,
        Query:               req.Query,
        SessionID:           req.SessionID,
        Model:               req.Model,
        Temperature:         req.Temperature,
        MaxCompletionTokens: req.MaxCompletionTokens,
    }).Get(ctx, &agentOutput)
    if err != nil {
        logger.Error("AgentActivity failed", "error", err)

        workflow.ExecuteActivity(ctx, "SaveFailureActivity", activities.SaveFailureInput{
            TaskID:    req.TaskID,
            ErrorType: types.ErrorTypeWorkflow,
            ErrorMsg:  err.Error(),
        }).Get(ctx, nil)

        workflow.ExecuteActivity(ctx, "RecordExecutionFailedActivity", activities.RecordExecutionFailedInput{
            TaskID:     req.TaskID,
            WorkflowID: req.WorkflowID,
            RunID:      req.RunID,
            ErrorType:  types.ErrorTypeWorkflow,
            ErrorMsg:   err.Error(),
        }).Get(ctx, nil)

        workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
            TaskID: req.TaskID,
            Event:  events.NewTaskFailedEvent(req.TaskID, req.WorkflowID, err.Error()),
        }).Get(ctx, nil)

        return nil, err
    }

    // 4. Emit LLM_COMPLETED
    err = workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
        TaskID: req.TaskID,
        Event:  events.NewLLMCompletedEvent(req.TaskID, agentOutput.Model, "stop", agentOutput.LatencyMS),
    }).Get(ctx, nil)
    if err != nil {
        logger.Error("EmitEventActivity (LLM_COMPLETED) failed", "error", err)
    }

    // 5. SaveResultActivity with LLM answer
    err = workflow.ExecuteActivity(ctx, "SaveResultActivity", activities.SaveResultInput{
        TaskID: req.TaskID,
        Result: agentOutput.Answer,
    }).Get(ctx, nil)
    if err != nil {
        logger.Error("SaveResultActivity failed", "error", err)

        workflow.ExecuteActivity(ctx, "SaveFailureActivity", activities.SaveFailureInput{
            TaskID:    req.TaskID,
            ErrorType: types.ErrorTypeWorkflow,
            ErrorMsg:  err.Error(),
        }).Get(ctx, nil)

        workflow.ExecuteActivity(ctx, "RecordExecutionFailedActivity", activities.RecordExecutionFailedInput{
            TaskID:     req.TaskID,
            WorkflowID: req.WorkflowID,
            RunID:      req.RunID,
            ErrorType:  types.ErrorTypeWorkflow,
            ErrorMsg:   err.Error(),
        }).Get(ctx, nil)

        workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
            TaskID: req.TaskID,
            Event:  events.NewTaskFailedEvent(req.TaskID, req.WorkflowID, err.Error()),
        }).Get(ctx, nil)

        return nil, err
    }

    // 6. RecordExecutionCompletedActivity
    err = workflow.ExecuteActivity(ctx, "RecordExecutionCompletedActivity", activities.RecordExecutionInput{
        TaskID:     req.TaskID,
        WorkflowID: req.WorkflowID,
        RunID:      req.RunID,
    }).Get(ctx, nil)
    if err != nil {
        logger.Error("RecordExecutionCompletedActivity failed", "error", err)
    }

    // 7. Emit TASK_COMPLETED
    workflow.ExecuteActivity(ctx, "EmitEventActivity", activities.EmitEventInput{
        TaskID: req.TaskID,
        Event:  events.NewTaskCompletedEvent(req.TaskID, req.WorkflowID),
    }).Get(ctx, nil)

    logger.Info("SimpleWorkflow completed", "task_id", req.TaskID, "answer", agentOutput.Answer)
    return &types.WorkflowTaskResult{
        TaskID: req.TaskID,
        Status: types.TaskStatusCompleted,
        Answer: agentOutput.Answer,
    }, nil
}
```

Also remove the `const EmptyWorkflowResult = "empty workflow completed"` line at the top since it's no longer used.

- [ ] **Step 2: Verify build**

Run: `go build ./cmd/gateway && go build ./cmd/worker`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/workflows/simple.go
git commit -m "feat: modify SimpleWorkflow to use AgentActivity for LLM calls"
```

---

## Task 8: Verification

**Prerequisites:** Docker compose up (postgres, redis, temporal), Python LLM Service running on port 8000

- [ ] **Step 1: Start Python LLM Service**

```bash
cd /home/florian/code/cribug/python_llm_service
source .venv/bin/activate
uvicorn app:app --host 0.0.0.0 --port 8000 &
sleep 2
```

- [ ] **Step 2: Verify Python health**

```bash
curl --noproxy '*' -s http://127.0.0.1:8000/health | jq
```
Expected: `{"status":"healthy"}`

- [ ] **Step 3: Verify Python chat**

```bash
curl --noproxy '*' -s -X POST http://127.0.0.1:8000/chat \
  -H "Content-Type: application/json" \
  -d '{"trace_id":"t1","task_id":"t1","provider":"openai_compatible","model":"gpt-4o-mini","messages":[{"role":"user","content":"hello mock"}]}' | jq
```
Expected: `content` = "mock answer: hello mock"

- [ ] **Step 4: Build and start Gateway**

```bash
cd /home/florian/code/cribug
go build ./cmd/gateway
export DATABASE_URL='postgres://admin:admin@localhost:5432/orchestrator?sslmode=disable'
export REDIS_ADDR='127.0.0.1:6379'
export LLM_SERVICE_URL='http://127.0.0.1:8000'
./gateway &
sleep 3
curl --noproxy '*' -s http://127.0.0.1:8080/health | jq
```

- [ ] **Step 5: Build and start Worker**

```bash
export DATABASE_URL='postgres://admin:admin@localhost:5432/orchestrator?sslmode=disable'
export REDIS_ADDR='127.0.0.1:6379'
export LLM_SERVICE_URL='http://127.0.0.1:8000'
./worker &
sleep 2
```

- [ ] **Step 6: Create task and poll**

```bash
RESP=$(curl --noproxy '*' -s -X POST http://127.0.0.1:8080/api/v1/tasks \
  -H "Content-Type: application/json" \
  -d '{"query":"hello from mock llm"}')
echo "$RESP" | jq
TASK_ID=$(echo "$RESP" | jq -r '.task_id')

for i in $(seq 1 30); do
  RESULT=$(curl --noproxy '*' -s http://127.0.0.1:8080/api/v1/tasks/$TASK_ID | jq -r '.status,.result')
  echo "=== $i: $RESULT ==="
  if echo "$RESULT" | grep -q "completed"; then
    break
  fi
  sleep 1
done
```

Expected: `status` = "completed", `result` = "mock answer: hello from mock llm"

- [ ] **Step 7: Verify Redis Stream**

```bash
redis-cli -h 127.0.0.1 -p 6379 XRANGE "task:$TASK_ID:events" - +
```

Expected: Contains TASK_CREATED, WORKFLOW_STARTED, LLM_STARTED, LLM_COMPLETED, TASK_COMPLETED

- [ ] **Step 8: Verify SSE**

```bash
curl --noproxy '*' -N "http://127.0.0.1:8080/api/v1/stream/sse?task_id=$TASK_ID"
```

Expected: Streams all events and closes after TASK_COMPLETED

---

## Summary

| Task | Description | Files |
|------|-------------|-------|
| 1 | Python LLM Service | python_llm_service/app.py, requirements.txt |
| 2 | Go LLM Types | internal/types/types.go |
| 3 | LLM Events | internal/events/types.go |
| 4 | Config | internal/config/config.go |
| 5 | AgentActivity | internal/activities/agent.go |
| 6 | Worker Registration | cmd/worker/main.go |
| 7 | Workflow Modification | internal/workflows/simple.go |
| 8 | Verification | End-to-end test |