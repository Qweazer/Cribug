# Phase 6D Hooks Event System — Completion Report

## 1. Files Created / Modified

### New Files (10)

| File | Purpose |
|------|---------|
| `internal/hooks/types.go` | HookPoint enum, HookEvent, HookResult (split-field), HookDecision, HookRegistration, HookAuditLog, HandlerResponse |
| `internal/hooks/validation.go` | HookRegistration validation, handler URL validation, internal/HTTP handler checks |
| `internal/hooks/filter.go` | HookFilter with known filter keys (tool_type, tool_name, etc.), handler config key passthrough |
| `internal/hooks/runtime.go` | HookRuntime — ExecuteHandlers (internal + HTTP), blocking semantics, audit logging |
| `internal/hooks/activity.go` | HookActivities — EmitHookEventActivity (single Temporal Activity, no nested activities) |
| `internal/hooks/hooks_test.go` | 34 unit/integration tests covering all hook types, blocking semantics, HTTP handlers |
| `internal/db/hooks.go` | HookRepository — CRUD, ListEnabledHooksByPoint, InsertHookAuditLog/InsertHookAuditLogs |
| `internal/api/hooks_handlers.go` | 7 REST endpoints for hook registration, listing, enable/disable, audit log query |
| `migrations/007_hooks.sql` | hooks table (14 columns) + hook_audit_logs table (22 columns) + indexes |
| `scripts/test_phase6d_hooks_e2e.sh` | E2E smoke test covering registration, blocking allow/deny, API validation, cleanup |

### Modified Files (6)

| File | Changes |
|------|---------|
| `internal/config/config.go` | Added 8 hooks config fields (EnableHooks, HooksBlockingEnabled, HookBlockingFailClosed, etc.) + HOOKS_ENABLED fallback alias |
| `cmd/worker/main.go` | Created HookRuntime + HookActivities, registered EmitHookEventActivity + SkillExecutionWorkflow, injected HookRuntime into LLM Activities |
| `internal/workflows/mcp.go` | Added before_tool_call + after_tool_call + on_error hooks in MCPToolCallWorkflow |
| `internal/workflows/sandbox.go` | Added before_tool_call + after_tool_call + on_error hooks in SandboxWorkflow |
| `internal/workflows/skill_execution.go` | Added before_tool_call + after_tool_call + on_error hooks in SkillExecutionWorkflow |
| `internal/activities/agent.go` | Added inline before_llm_call + after_llm_call hooks in CallLLM Activity |
| `internal/api/router.go` | Registered 7 hooks API routes |

## 2. Architecture

### Execution Model (Single Activity, No Nested Activities)

```
Workflow (deterministic)
  → EmitHookEventActivity (single Temporal Activity)
      → HookRepository.ListEnabledHooksByPoint (DB read)
      → HookFilter.Match (pure Go)
      → HookRuntime.ExecuteHandlers (Go service)
          → internal:* handlers (inline)
          → http/https handlers (POST with timeout/size limits)
      → HookRepository.InsertHookAuditLog (DB write)
      → return HookDecision
  → Workflow checks HookDecision.Denied / .CanContinue
```

**Key constraint**: No `workflow.ExecuteActivity` inside EmitHookEventActivity. HookRuntime is plain Go, called inline.

### Blocking Semantics (Split-Field)

| blocking_configured | HooksBlockingEnabled | handler continue=false | Result |
|---------------------|---------------------|------------------------|--------|
| false | any | any | Non-blocking, main flow continues |
| true | false | false | `suppressed_reason="blocking_disabled_by_config"`, main flow continues |
| true | true | false | `decision_enforced=true`, main flow DENIED |
| true | true | timeout/error | Default fail-open; fail-closed if `HOOK_BLOCKING_FAIL_CLOSED=true` |

### Workflow Contract

Workflows ONLY check `decision.Denied` and `decision.CanContinue`. They never check `HasBlockingHooks` or raw `Blocking` flags.

## 3. Hook Points Coverage

| HookPoint | Blocking | MCP | Sandbox | Skill | LLM | Error Path |
|-----------|----------|-----|---------|-------|-----|------------|
| `before_tool_call` | Yes | ✅ | ✅ | ✅ | N/A | N/A |
| `after_tool_call` | No | ✅ | ✅ | ✅ | N/A | N/A |
| `before_llm_call` | Yes | N/A | N/A | N/A | ✅ (inline) | N/A |
| `after_llm_call` | No | N/A | N/A | N/A | ✅ (inline) | N/A |
| `on_error` | Yes | ✅ | ✅ | ✅ | N/A | ✅ |
| `on_agent_step` | No | Not yet | Not yet | Not yet | Not yet | N/A |
| `on_workspace_append` | No | Recursion guard ready | Recursion guard ready | Recursion guard ready | N/A | N/A |
| `on_handoff` | No | Not yet | Not yet | Not yet | N/A | N/A |

## 4. API Examples

```bash
# Register blocking permission hook
POST /api/v1/hooks
{
    "name": "tool_permission_check",
    "hook_point": "before_tool_call",
    "handler_url": "internal:permission_check",
    "blocking": true,
    "filter": {"allowed_tools": ["echo", "get_time"]}
}

# Register non-blocking audit hook
POST /api/v1/hooks
{
    "name": "usage_audit",
    "hook_point": "after_tool_call",
    "handler_url": "internal:audit_logger"
}

# List hooks
GET /api/v1/hooks?hook_point=before_tool_call

# Query audit logs
GET /api/v1/hooks/audit?workflow_id=wf-xxx
```

## 5. Test Report

### Unit Tests: 34/34 PASS

```
TestHookPointIsValid                           PASS
TestHookPointAllowsBlocking                    PASS
TestValidateHookRegistration_Valid             PASS
TestValidateHookRegistration_BlockingOnNonBlockingPoint PASS
TestValidateHookRegistration_UnknownInternalHandler PASS
TestValidateHookRegistration_HTTPHostNotAllowed PASS
TestValidateHookRegistration_HTTPHostAllowed   PASS
TestValidateHookRegistration_InvalidScheme     PASS
TestFilter_EmptyFilterMatchesAll               PASS
TestFilter_ToolNameMatch                       PASS
TestFilter_ToolNameNoMatch                     PASS
TestFilter_ToolTypesMatch                      PASS
TestFilter_ToolTypesNoMatch                    PASS
TestFilter_MultipleKeysAND                     PASS
TestFilter_MultipleKeysAND_Partial             PASS
TestRecursionGuard_DepthExceeded               PASS
TestRecursionGuard_HookOriginOnWorkspaceAppend PASS
TestNonBlockingHandler_Success                 PASS
TestNonBlockingHandler_FailureDoesNotBlock     PASS
TestBlockingHandler_DenyWhenEnabled            PASS
TestBlockingHandler_SuppressedWhenBlockingDisabled PASS
TestHTTPHandler_Success                        PASS
TestHTTPHandler_500DoesNotBlockNonBlocking     PASS
TestBlockingHTTPHandler_TimeoutFailOpen        PASS (10s timeout test)
TestBlockingHTTPHandler_TimeoutFailClosed      PASS (10s timeout test)
TestHooksDisabled_FastReturn                   PASS
TestAuditLog_ContainsBlockingFields            PASS
TestAuditLog_SuppressedReasonWhenDisabled      PASS
TestAuditLog_HasPayloadHash                    PASS
TestNoMatchingHooks_CanContinue                PASS
TestFilterNoMatch_CanContinue                  PASS
TestPermissionCheck_Allowed                    PASS
TestPermissionCheck_Denied                     PASS
TestHookDecision_WarningsCollected             PASS
```

### Spec-Required Tests Coverage

| Requirement | Test |
|-------------|------|
| After_tool_call registered blocking=true → API rejects | `TestValidateHookRegistration_BlockingOnNonBlockingPoint` |
| blocking_configured=true + BlockingEnabled=false → main flow continues, suppressed_reason written | `TestBlockingHandler_SuppressedWhenBlockingDisabled` |
| blocking_configured=true + BlockingEnabled=true + continue=false → permission_denied | `TestBlockingHandler_DenyWhenEnabled` |
| Non-blocking HTTP 500 → main flow continues | `TestHTTPHandler_500DoesNotBlockNonBlocking` |
| Blocking HTTP timeout → default fail-open | `TestBlockingHTTPHandler_TimeoutFailOpen` |
| Blocking HTTP timeout + fail-closed → fail-closed | `TestBlockingHTTPHandler_TimeoutFailClosed` |
| Audit log contains blocking_configured, blocking_effective, decision_enforced, suppressed_reason | `TestAuditLog_ContainsBlockingFields`, `TestAuditLog_SuppressedReasonWhenDisabled` |
| Workspace append hook doesn't recurse (hook_origin) | `TestRecursionGuard_HookOriginOnWorkspaceAppend` |
| Workspace append hook doesn't recurse (recursion_depth) | `TestRecursionGuard_DepthExceeded` |
| Permission check allows allowed tool | `TestPermissionCheck_Allowed` |
| Permission check denies disallowed tool | `TestPermissionCheck_Denied` |

## 6. Configuration

```bash
ENABLE_HOOKS=true                          # Master switch (HOOKS_ENABLED also accepted)
HOOKS_BLOCKING_ENABLED=false               # Global blocking sub-switch
HOOK_BLOCKING_FAIL_CLOSED=false           # Fail-closed for blocking handler errors
HOOK_HANDLER_TIMEOUT_SECONDS=5
HOOK_HANDLER_MAX_RESULT_BYTES=65536
HOOK_ALLOWED_INTERNAL_HANDLERS=audit_logger,log_only,permission_check
HOOK_ALLOWED_HTTP_HOSTS=localhost,127.0.0.1
```

## 7. Deterministic Purity

- `EmitHookEventActivity` is a Temporal Activity (all external IO in Activity)
- Workflows call only `EmitHookEventActivity`, never direct DB/HTTP/Redis
- LLM hooks are inline in existing Activities (no nested Activity calls)
- `workflow.Now(ctx)` used in Workflows for timestamps; `time.Now().UTC()` only in Activities
- No goroutines, rand, or uuid.New in Workflows

## 8. Risks and Next Steps

### Left for Phase 6E/6F/7
- `on_agent_step` integration into ReAct loop
- `on_handoff` integration into handoff workflow
- Full `on_workspace_append` integration with recursion guard
- DAG/Swarm/ReAct LLM Activity hook injection beyond `CallLLM`
- Async hook delivery (message queue, durable retry)
- Real LLM smoke test for before/after_llm_call hooks (opt-in with `REAL_LLM_TEST=1`)

### Known Limitations
- HTTP handler allowlist is host-based, no path-level filtering
- No hook handler chaining or pipeline
- No real-time SSE push of hook events to external consumers
- Test internal handlers default-disabled in production config
