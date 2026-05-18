# Week 3 Phase 1: Python LLM Service + AgentActivity

## Overview

Implement mock Python LLM Service with AgentActivity to call it, enabling the Workflow to return LLM responses instead of hardcoded "empty workflow completed".

## Architecture

```
┌──────────┐     ┌─────────────────────────────────────────────────────────┐
│ Gateway  │────>│ Worker                                                   │
└──────────┘     │  ┌─────────────────────────────────────────────────────┐│
                 │  │ SimpleWorkflow                                       ││
                 │  │  1. EmitEvent(WORKFLOW_STARTED)                      ││
                 │  │  2. EmitEvent(LLM_STARTED)                           ││
                 │  │  3. AgentActivity ────────> Python LLM Service       ││
                 │  │  4. EmitEvent(LLM_COMPLETED)                         ││
                 │  │  5. SaveResultActivity                              ││
                 │  │  6. RecordExecutionCompletedActivity                 ││
                 │  │  7. EmitEvent(TASK_COMPLETED)                        ││
                 │  └─────────────────────────────────────────────────────┘│
                 └─────────────────────────────────────────────────────────┘
```

## Components

### 1. Python LLM Service (`python_llm_service/app.py`)

**Endpoints:**
- `GET /health` → `{"status": "healthy"}`
- `POST /chat` → accepts `LLMRequest`, returns `LLMResponse`

**Validation:**
- provider must be "openai_compatible" (400 otherwise)
- messages must be non-empty (400 otherwise)
- at least one message with role="user" (400 otherwise)

**Mock behavior:**
- Extract last user message content
- Return `{"content": "mock answer: " + last_user_content, ...}`

### 2. Go Types (`internal/types/types.go`)

```go
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

### 3. AgentActivity (`internal/activities/agent.go`)

**Input:**
```go
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
```

**Output:**
```go
type AgentActivityOutput struct {
    Answer    string
    Usage     types.Usage
    Model     string
    Provider  string
    LatencyMS int64
}
```

**Behavior:**
1. Build LLMRequest with messages=[{"role":"user", "content":input.Query}]
2. POST to LLM_SERVICE_URL + "/chat"
3. Return error if HTTP non-2xx
4. Return error if response.error != nil
5. Return output with answer content

### 4. Config (`internal/config/config.go`)

Add `LLMServiceURL` field. Load from `LLM_SERVICE_URL` env var, default `http://127.0.0.1:8000`.

### 5. Events (`internal/events/types.go`)

Add:
```go
const EventTypeLLMStarted = "LLM_STARTED"
const EventTypeLLMCompleted = "LLM_COMPLETED"

func NewLLMStartedEvent(taskID, model string) AgentEvent { ... }
func NewLLMCompletedEvent(taskID, model, finishReason string, latencyMS int64) AgentEvent { ... }
```

### 6. Workflow (`internal/workflows/simple.go`)

Replace hardcoded "empty workflow completed" flow:
1. Emit WORKFLOW_STARTED
2. Emit LLM_STARTED
3. Execute AgentActivity
4. Emit LLM_COMPLETED
5. SaveResultActivity(agentOutput.Answer)
6. RecordExecutionCompletedActivity
7. Emit TASK_COMPLETED
8. Return TaskResult{Status:"completed", Answer:agentOutput.Answer}

### 7. Worker Registration (`cmd/worker/main.go`)

Register AgentActivity alongside existing activities.

## Files to Modify

1. `python_llm_service/app.py` - FastAPI app with /health, /chat
2. `python_llm_service/requirements.txt` - fastapi, uvicorn, pydantic
3. `internal/types/types.go` - Add LLM types
4. `internal/events/types.go` - Add LLM_STARTED, LLM_COMPLETED events
5. `internal/config/config.go` - Add LLMServiceURL
6. `internal/activities/agent.go` - Implement AgentActivity
7. `internal/workflows/simple.go` - Use AgentActivity
8. `cmd/worker/main.go` - Register AgentActivity

## Verification

1. Python /health returns `{"status": "healthy"}`
2. Python /chat returns mock answer
3. `go build ./cmd/gateway` passes
4. `go build ./cmd/worker` passes
5. Gateway /health passes
6. POST /api/v1/tasks creates task
7. GET /api/v1/tasks/{id} returns status=completed, result="mock answer: <query>"
8. Redis Stream contains TASK_CREATED, WORKFLOW_STARTED, LLM_STARTED, LLM_COMPLETED, TASK_COMPLETED
9. SSE outputs same events
10. Postgres tasks.status=completed, tasks.result="mock answer: <query>"
11. Postgres executions.status=completed

## Forbidden

- No real OpenAI API
- No OPENAI_API_KEY
- No Budget feature
- No Session memory
- No llm_calls table writes
- No Gateway direct LLM calls
- No Workflow direct HTTP