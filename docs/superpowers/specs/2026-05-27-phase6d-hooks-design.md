# Phase 6D Hooks Event System — Design Spec (v2, post 6D-0.5 corrections)

## 1. Architecture

### 1.1 Execution Model (Single Activity, No Nested Activities)

```
Workflow (deterministic, no external IO)
  │
  ├─ EmitHookEventActivity (single Temporal Activity)
  │   ├─ HookRepository.ListEnabledHooksByPoint(hookPoint, tenantID)  ← DB read
  │   ├─ HookFilter.Match(event, registration)                        ← pure Go
  │   ├─ HookRuntime.ExecuteHandlers(event, matchedHandlers)           ← Go service
  │   │   ├─ internal:* handlers: execute inline
  │   │   └─ http/https handlers: POST with timeout, max response size
  │   ├─ HookRepository.InsertHookAuditLogs(auditLogs)                ← DB write
  │   └─ return HookDecision                                        ← pure Go
  │
  └─ Workflow reads HookDecision.CanContinue / .Denied
     └─ if Denied: return structured error (permission_denied / policy_denied)
     └─ if CanContinue: proceed with main logic
```

**Hard constraint**: No `workflow.ExecuteActivity` inside an Activity. `EmitHookEventActivity` is the single entry point. `HookRuntime` and `HookFilter` are plain Go services called inline within the Activity.

### 1.2 Component Boundaries

| Component | Type | What it does | Forbidden |
|-----------|------|-------------|-----------|
| Workflow | Temporal Workflow | Calls EmitHookEventActivity, reads HookDecision | Direct DB/HTTP/Redis |
| EmitHookEventActivity | Temporal Activity | Orchestrates hook execution within Go process | Calling other Activities |
| HookRepository | DB repository | CRUD on hooks + hook_audit_logs tables | Business logic |
| HookFilter | Pure Go service | Matches events against registration filters | Any IO |
| HookRuntime | Go service | Executes internal/HTTP handlers, enforces timeout/size limits | Calling Activities |

---

## 2. Data Structures

### 2.1 HookPoint Enum

```go
type HookPoint string

const (
    HookPointBeforeToolCall     HookPoint = "before_tool_call"
    HookPointAfterToolCall      HookPoint = "after_tool_call"
    HookPointBeforeLLMCall      HookPoint = "before_llm_call"
    HookPointAfterLLMCall       HookPoint = "after_llm_call"
    HookPointOnAgentStep        HookPoint = "on_agent_step"
    HookPointOnWorkspaceAppend  HookPoint = "on_workspace_append"
    HookPointOnHandoff          HookPoint = "on_handoff"
    HookPointOnError            HookPoint = "on_error"
)

// BlockingAllowed returns which hook points may have blocking=true registrations.
var blockingAllowed = map[HookPoint]bool{
    HookPointBeforeToolCall: true,
    HookPointBeforeLLMCall:  true,
    HookPointOnError:        true,
    // All others: blocking registrations rejected at API layer
}

func (hp HookPoint) IsValid() bool {
    switch hp {
    case HookPointBeforeToolCall, HookPointAfterToolCall,
         HookPointBeforeLLMCall, HookPointAfterLLMCall,
         HookPointOnAgentStep, HookPointOnWorkspaceAppend,
         HookPointOnHandoff, HookPointOnError:
        return true
    }
    return false
}

func (hp HookPoint) AllowsBlocking() bool {
    return blockingAllowed[hp]
}
```

### 2.2 HookEvent

```go
type HookEvent struct {
    EventID         string                 `json:"event_id"`
    HookPoint       HookPoint              `json:"hook_point"`
    AgentID         string                 `json:"agent_id"`
    WorkflowID      string                 `json:"workflow_id"`
    TenantID        string                 `json:"tenant_id"`
    CorrelationID   string                 `json:"correlation_id,omitempty"`
    Timestamp       time.Time              `json:"timestamp"`
    SourceComponent string                 `json:"source_component"` // "mcp", "sandbox", "skill", "llm", "workspace", "handoff", "react"
    HookOrigin      bool                   `json:"hook_origin"`      // true if emitted by a hook handler itself
    RecursionDepth  int                    `json:"recursion_depth"`
    Payload         map[string]interface{} `json:"payload"`
}
```

### 2.3 HookResult (per-handler, "split field" semantics)

```go
type HookResult struct {
    EventID            string `json:"event_id"`
    HookPoint          string `json:"hook_point"`
    HandlerName        string `json:"handler_name"`
    Success            bool   `json:"success"`             // handler executed without error
    BlockingConfigured bool   `json:"blocking_configured"`  // HookRegistration.Blocking
    BlockingEffective  bool   `json:"blocking_effective"`   // blocking_configured && HooksBlockingEnabled
    DecisionEnforced   bool   `json:"decision_enforced"`    // blocking_effective && continue=false && actually blocked
    SuppressedReason   string `json:"suppressed_reason,omitempty"` // e.g. "blocking_disabled_by_config"
    ContinueDecision   bool   `json:"continue_decision"`    // handler's continue response
    RejectCode         string `json:"reject_code,omitempty"` // "permission_denied" | "policy_denied"
    RejectReason       string `json:"reject_reason,omitempty"`
    DurationMs         int64  `json:"duration_ms"`
    Error              string `json:"error,omitempty"`
    Warning            string `json:"warning,omitempty"`
}
```

### 2.4 HookDecision (aggregated, returned to Workflow)

```go
type HookDecision struct {
    EventID              string       `json:"event_id"`
    Results              []HookResult `json:"results"`
    HasBlockingHooks     bool         `json:"has_blocking_hooks"`     // audit only
    HasEffectiveBlocking bool         `json:"has_effective_blocking"` // audit only
    Denied               bool         `json:"denied"`                 // authoritative: Workflow checks this
    CanContinue          bool         `json:"can_continue"`           // authoritative: !Denied
    RejectCode           string       `json:"reject_code,omitempty"`
    RejectReason         string       `json:"reject_reason,omitempty"`
    DeniedByHandler      string       `json:"denied_by_handler,omitempty"`
    Warnings             []string     `json:"warnings,omitempty"`
}
```

### 2.5 HookRegistration (DB + API)

```go
type HookRegistration struct {
    ID          string                 `json:"id"`
    TenantID    string                 `json:"tenant_id"`
    Name        string                 `json:"name"`
    HookPoint   HookPoint              `json:"hook_point"`
    HandlerURL  string                 `json:"handler_url"`  // "internal:xxx" or "https://..."
    Blocking    bool                   `json:"blocking"`      // registration intent
    Filter      map[string]interface{} `json:"filter,omitempty"`
    Enabled     bool                   `json:"enabled"`
    CreatedAt   time.Time              `json:"created_at"`
    UpdatedAt   time.Time              `json:"updated_at"`
}
```

### 2.6 HookAuditLog

```go
type HookAuditLog struct {
    ID                 string    `json:"id"`
    EventID            string    `json:"event_id"`
    HookPoint          string    `json:"hook_point"`
    HandlerName        string    `json:"handler_name"`
    TenantID           string    `json:"tenant_id"`
    WorkflowID         string    `json:"workflow_id"`
    AgentID            string    `json:"agent_id"`
    CorrelationID      string    `json:"correlation_id,omitempty"`
    HandlerType        string    `json:"handler_type"` // "internal" | "http"
    BlockingConfigured bool      `json:"blocking_configured"`
    BlockingEffective  bool      `json:"blocking_effective"`
    DecisionEnforced   bool      `json:"decision_enforced"`
    SuppressedReason   string    `json:"suppressed_reason,omitempty"`
    ContinueDecision   bool      `json:"continue_decision"`
    RejectCode         string    `json:"reject_code,omitempty"`
    RejectReason       string    `json:"reject_reason,omitempty"`
    PayloadHash        string    `json:"payload_hash,omitempty"`
    Success            bool      `json:"success"`
    DurationMs         int64     `json:"duration_ms"`
    Error              string    `json:"error,omitempty"`
    Warning            string    `json:"warning,omitempty"`
    CreatedAt          time.Time `json:"created_at"`
}
```

---

## 3. Blocking Semantics (Split-Field)

### 3.1 Decision Matrix

| blocking_configured | HooksBlockingEnabled | handler returns continue=false | Result |
|---------------------|---------------------|-------------------------------|--------|
| false | any | any | non-blocking, main flow continues |
| true | false | continue=false | blocking_effective=false, suppressed_reason="blocking_disabled_by_config", main flow continues |
| true | true | continue=false | blocking_effective=true, decision_enforced=true, main flow DENIED |
| true | true | continue=true | blocking_effective=true, decision_enforced=false, main flow continues |
| true | true | handler timeout/error | fail-open by default (continue), or fail-closed if HOOK_BLOCKING_FAIL_CLOSED=true |

### 3.2 Workflow Contract

Workflows must NOT check `HasBlockingHooks` or `HasEffectiveBlocking` to decide whether to proceed. They MUST only check `Denied` / `CanContinue`:

```go
if decision.Denied {
    return fmt.Errorf("%s: %s", decision.RejectCode, decision.RejectReason)
}
// proceed with main logic
```

---

## 4. Internal Handlers

### 4.1 Production Handlers (always allowed)

| Handler | Blocking | Behavior |
|---------|----------|----------|
| `internal:audit_logger` | No | Logs event details to audit log, always succeeds |
| `internal:log_only` | No | Structured logging only, no state change |
| `internal:permission_check` | Yes | Checks tool_name against allowlist in filter config, returns continue=true/false |

### 4.2 Test Handlers (allowed only when explicitly configured or in test mode)

| Handler | Blocking | Behavior |
|---------|----------|----------|
| `internal:deny_tool_for_test` | Yes | Always returns continue=false with reject_code="test_denied" |
| `internal:fail_for_test` | No | Always returns success=false with error="test_failure" |
| `internal:timeout_for_test` | No | Sleeps 30s (longer than handler timeout), triggers timeout path |

### 4.3 Allowed Internal Handlers Config

```bash
HOOK_ALLOWED_INTERNAL_HANDLERS=audit_logger,log_only,permission_check
# test handlers NOT listed here in production
```

---

## 5. HTTP Handler Runtime

### 5.1 Execution Rules

1. Only `http://` and `https://` schemes allowed
2. Host must be in `HOOK_ALLOWED_HTTP_HOSTS` (default: `localhost,127.0.0.1`)
3. POST HookEvent as JSON body
4. Timeout: `HOOK_HANDLER_TIMEOUT_SECONDS` (default 5s)
5. Response body capped at `HOOK_HANDLER_MAX_RESULT_BYTES` (default 64KB)
6. Response size exceeded → handler failure
7. Non-2xx status → handler failure
8. Invalid JSON response → handler failure
9. Non-blocking failure → audit + warning, main flow continues
10. Blocking failure → fail-open by default; fail-closed only if `HOOK_BLOCKING_FAIL_CLOSED=true`

### 5.2 Response Format Expected

```json
{
    "continue": true,
    "reject_code": "",
    "reject_reason": ""
}
```

---

## 6. Filter Matching

```go
// HookFilter matches HookEvent against HookRegistration.Filter
// Supported filter keys:
//   tool_types:   []string  — "mcp", "sandbox", "skill"
//   tool_name:    string    — exact match
//   tool_id:      string    — exact match
//   agent_id:     string    — exact match
//   skill_id:     string    — exact match
//   error_type:   string    — for on_error hooks
// Filter is AND: all provided keys must match.
// Empty filter matches everything.
```

---

## 7. Recursion Guard (WorkspaceAppend)

HookEvent fields:
- `source_component`: set by emitter (e.g. "workspace", "mcp", "hook")
- `hook_origin`: true when a hook handler itself triggers an action
- `recursion_depth`: incremented when hook triggers another hook

Rules in EmitHookEventActivity:
1. If `hook_origin == true` && `hook_point == on_workspace_append` → skip, write debug audit
2. If `recursion_depth >= 2` → skip, write debug audit
3. When a hook handler calls WorkspaceAppend, set `hook_origin=true` and `recursion_depth = parent.recursion_depth + 1`

---

## 8. Configuration

### 8.1 Config struct additions

```go
// Hooks Event System (Phase 6D Slice 20)
EnableHooks              bool     // ENABLE_HOOKS — master switch
HooksBlockingEnabled     bool     // HOOKS_BLOCKING_ENABLED
HookBlockingFailClosed   bool     // HOOK_BLOCKING_FAIL_CLOSED
HookHandlerTimeoutSec    int      // HOOK_HANDLER_TIMEOUT_SECONDS (default 5)
HookHandlerMaxResultBytes int    // HOOK_HANDLER_MAX_RESULT_BYTES (default 65536)
HookAllowedInternalHandlers []string // HOOK_ALLOWED_INTERNAL_HANDLERS
HookAllowedHTTPHosts     []string // HOOK_ALLOWED_HTTP_HOSTS
```

### 8.2 Environment Variables (unified with Phase 6 plan)

```bash
ENABLE_HOOKS=true                         # master switch (was "HOOKS_ENABLED" in earlier drafts)
HOOKS_BLOCKING_ENABLED=false              # blocking sub-switch
HOOK_BLOCKING_FAIL_CLOSED=false           # fail-closed for blocking handler errors
HOOK_HANDLER_TIMEOUT_SECONDS=5
HOOK_HANDLER_MAX_RESULT_BYTES=65536
HOOK_ALLOWED_INTERNAL_HANDLERS=audit_logger,log_only,permission_check
HOOK_ALLOWED_HTTP_HOSTS=localhost,127.0.0.1
```

`ENABLE_HOOKS` is the canonical master switch. `HOOKS_ENABLED` is accepted as a fallback alias. Internal code normalizes to `Config.Hooks.Enabled`.

---

## 9. Database Schema

### 9.1 hooks table

```sql
CREATE TABLE hooks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    name VARCHAR(100) NOT NULL,
    hook_point VARCHAR(50) NOT NULL,
    handler_url VARCHAR(500) NOT NULL,
    blocking BOOLEAN DEFAULT FALSE,
    filter JSONB,
    enabled BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    UNIQUE(tenant_id, name)
);
```

### 9.2 hook_audit_logs table (expanded fields)

```sql
CREATE TABLE hook_audit_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id VARCHAR(100) NOT NULL,
    hook_point VARCHAR(50) NOT NULL,
    handler_name VARCHAR(100) NOT NULL,
    tenant_id UUID NOT NULL,
    workflow_id VARCHAR(100) NOT NULL DEFAULT '',
    agent_id VARCHAR(100) NOT NULL DEFAULT '',
    correlation_id VARCHAR(100) NOT NULL DEFAULT '',
    handler_type VARCHAR(20) NOT NULL DEFAULT 'internal',
    blocking_configured BOOLEAN NOT NULL DEFAULT FALSE,
    blocking_effective BOOLEAN NOT NULL DEFAULT FALSE,
    decision_enforced BOOLEAN NOT NULL DEFAULT FALSE,
    suppressed_reason VARCHAR(200) NOT NULL DEFAULT '',
    continue_decision BOOLEAN NOT NULL DEFAULT TRUE,
    reject_code VARCHAR(50) NOT NULL DEFAULT '',
    reject_reason TEXT NOT NULL DEFAULT '',
    payload_hash VARCHAR(64) NOT NULL DEFAULT '',
    success BOOLEAN NOT NULL DEFAULT FALSE,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT '',
    warning TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);
```

---

## 10. Workflow Integration Points

### 10.1 MCP Tool Call (before/after_tool_call)

File: `internal/workflows/mcp.go` — `MCPToolCallWorkflow`

- Before `CallMCPToolActivity`: emit `before_tool_call` with `tool_type=mcp`
- After `CallMCPToolActivity`: emit `after_tool_call` with result summary
- On error: emit `on_error`

### 10.2 Sandbox Execution (before/after_tool_call)

File: `internal/workflows/sandbox.go` — `SandboxWorkflow`

- Before `ExecuteSandboxActivity`: emit `before_tool_call` with `tool_type=sandbox`
- After `ExecuteSandboxActivity`: emit `after_tool_call` with result summary
- On error: emit `on_error`

### 10.3 Skill Execution (before/after_tool_call)

File: `internal/workflows/skill_execution.go` — `SkillExecutionWorkflow`

- Before `ExecuteSkillActivity`: emit `before_tool_call` with `tool_type=skill`
- After completion: emit `after_tool_call` with result summary
- On error: emit `on_error`

### 10.4 LLM Call (before/after_llm_call)

Files: `internal/activities/agent.go` (CallLLM), `internal/activities/react.go`, `internal/activities/dag.go`, `internal/activities/swarm.go`

The LLM call happens inside Activities (not Workflows). Hooks need to be called **before** the Activity makes the HTTP call. This is done inside the Activity method, not as a separate Temporal Activity call (since Activities can't call Activities):

```go
func (a *AgentActivities) CallLLM(ctx context.Context, input AgentActivityInput) (*AgentActivityResult, error) {
    // Hook: before_llm_call (inline, not a separate activity call)
    if a.config.EnableHooks {
        hookDecision := a.hookRuntime.EmitAndExecute(ctx, HookEvent{...HookPointBeforeLLMCall...})
        if hookDecision.Denied {
            return nil, fmt.Errorf("%s: %s", hookDecision.RejectCode, hookDecision.RejectReason)
        }
    }

    // ... actual LLM HTTP call ...

    // Hook: after_llm_call (fire-and-forget for non-blocking)
    if a.config.EnableHooks {
        a.hookRuntime.EmitAndExecuteAsync(ctx, HookEvent{...HookPointAfterLLMCall...})
    }

    return result, nil
}
```

### 10.5 ReAct Agent Step (on_agent_step)

File: `internal/workflows/patterns/react.go`

Emit `on_agent_step` after each step completes, via Workflow-level `EmitHookEventActivity`.

### 10.6 WorkspaceAppend (on_workspace_append)

Where WorkspaceAppend Activity runs, emit `on_workspace_append` after successful append. Use `hook_origin` and `recursion_depth` guards.

### 10.7 Handoff (on_handoff)

Where Handoff completes, emit `on_handoff`.

### 10.8 Error Paths (on_error)

Emit `on_error` from MCP, Sandbox, Skill, LLM error paths, with `error_type`, `message` summary, `component`, `recoverable` flags.

---

## 11. API Design

### 11.1 Routes

| Method | Path | Purpose |
|--------|------|---------|
| POST | `/api/v1/hooks` | Register hook |
| GET | `/api/v1/hooks` | List hooks (by tenant, hook_point, enabled) |
| GET | `/api/v1/hooks/{hook_id}` | Get hook by ID |
| DELETE | `/api/v1/hooks/{hook_id}` | Delete hook |
| PATCH | `/api/v1/hooks/{hook_id}/enable` | Enable hook |
| PATCH | `/api/v1/hooks/{hook_id}/disable` | Disable hook |
| GET | `/api/v1/hooks/audit` | Query audit logs |

### 11.2 Validation Rules

1. `hook_point` must be a valid HookPoint
2. `blocking=true` only allowed for `before_tool_call`, `before_llm_call`, `on_error`
3. `handler_url` must be `internal:<name>` or `http(s)://<host>/...`
4. Internal handler name must be in `HOOK_ALLOWED_INTERNAL_HANDLERS`
5. HTTP host must be in `HOOK_ALLOWED_HTTP_HOSTS`
6. `name` must be unique per tenant
7. `filter` must be a valid JSON object

### 11.3 Example Requests

```bash
# Register blocking permission hook
POST /api/v1/hooks
{
    "name": "tool_permission_check",
    "hook_point": "before_tool_call",
    "handler_url": "internal:permission_check",
    "blocking": true,
    "filter": {"tool_types": ["mcp", "sandbox"]}
}

# Register non-blocking audit hook
POST /api/v1/hooks
{
    "name": "usage_audit",
    "hook_point": "after_tool_call",
    "handler_url": "internal:audit_logger",
    "blocking": false,
    "filter": {}
}

# Register HTTP webhook
POST /api/v1/hooks
{
    "name": "notify_handoff",
    "hook_point": "on_handoff",
    "handler_url": "http://localhost:9999/webhook",
    "blocking": false,
    "filter": {}
}
```

---

## 12. File Layout (all new/modified)

### New Files

```
internal/hooks/
  types.go              — HookPoint, HookEvent, HookResult, HookDecision, HookRegistration, HookAuditLog
  validation.go         — IsValid, AllowsBlocking, ValidateHookRegistration
  filter.go             — HookFilter.Match(event, registration)
  runtime.go            — HookRuntime: ExecuteHandlers, EmitAndExecute
  internal_handlers.go  — internal:* handler implementations
  activity.go           — EmitHookEventActivity (Temporal Activity)

internal/db/
  hooks.go              — HookRepository (CRUD hooks, InsertHookAuditLogs, ListEnabledHooksByPoint)

internal/api/
  hooks_handlers.go     — Hooks API HTTP handlers

migrations/
  007_hooks.sql         — hooks + hook_audit_logs tables
```

### Modified Files

```
internal/config/config.go          — Add hooks config fields
internal/workflows/mcp.go          — Add before/after_tool_call hook emission
internal/workflows/sandbox.go      — Add before/after_tool_call hook emission
internal/workflows/skill_execution.go — Add before/after_tool_call hook emission
internal/workflows/patterns/react.go — Add on_agent_step hook emission
internal/activities/agent.go       — Add before/after_llm_call (inline, not activity call)
internal/activities/react.go       — Add LLM hooks (inline)
internal/activities/dag.go         — Add LLM hooks (inline)
internal/activities/swarm.go       — Add LLM hooks (inline)
internal/activities/execution.go   — Add on_workspace_append hook emission
internal/api/router.go             — Register /api/v1/hooks routes
```

---

## 13. Test Requirements (Phase 6D-5)

### 13.1 Unit Tests
- HookPoint validation (valid/invalid)
- blocking allowed/disallowed per hook point
- HookRegistration validation (all rules from §11.2)
- filter matching (empty, single key, multiple keys, no match)
- payload sanitization / truncation
- handler result parsing (success, failure, invalid JSON)
- HookDecision construction from HookResults
- blocking_configured vs blocking_effective split
- suppressed_reason when HOOKS_BLOCKING_ENABLED=false
- recursion depth guard

### 13.2 Integration Tests
- Activity does NOT call other Activities (structural check)
- blocking_configured=true + HOOKS_BLOCKING_ENABLED=false → main flow continues, suppressed_reason written
- blocking_configured=true + HOOKS_BLOCKING_ENABLED=true + handler continue=false → permission_denied returned
- after_tool_call registered blocking=true → API rejects
- non-blocking HTTP handler 500 → main flow continues
- blocking HTTP handler timeout → default fail-open
- HOOK_BLOCKING_FAIL_CLOSED=true + blocking timeout → fail-closed
- on_workspace_append does not recurse (hook_origin guard)
- audit log contains all split-field columns
- before_tool_call covers MCP / Sandbox / Skill
- before_llm_call / after_llm_call covers mock LLM path
- REAL_LLM_TEST=1 real LLM smoke; skip without API key

### 13.3 E2E Script
- `scripts/test_phase6d_hooks_e2e.sh`
- Covers: registration, before_tool_call allow/reject, after_tool_call audit, non-blocking failure, on_error, skill+hook integration, workspace append hook

---

## 14. Non-Goals (explicitly excluded)

- Async hook delivery (message queue, durable retry) — Phase 7
- Hook handler chaining / pipeline
- Hook marketplace / plugin system
- Real-time SSE push of hook events (Phase 6A's stream events may be reused)
- Phase 6E RAG or Phase 6F Research-Synthesis integration
