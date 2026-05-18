# Week 3 Phase 2: Session Memory + Usage Recording + Budget Tracking

## Current Status

**Already exists:**
- TaskStatusBudgetExceeded in types.go
- LLM_STARTED, LLM_COMPLETED events
- llm_calls table in Postgres
- AgentActivity with CallLLM
- SaveResultActivity / SaveFailureActivity

**Missing:**
- SESSION_LOADED, USAGE_RECORDED, TASK_BUDGET_EXCEEDED events
- LoadSessionActivity / SaveSessionActivity (Redis-based)
- EstimatePromptTokensActivity / CheckBudgetActivity
- RecordUsageActivity
- Redis session methods
- Workflow integration of all above

## Architecture

```
Workflow Execution Flow:
1. WORKFLOW_STARTED
2. LoadSessionActivity → SESSION_LOADED (Redis)
3. EstimatePromptTokensActivity (session + query)
4. CheckBudgetActivity
   - If budget exceeded → TASK_BUDGET_EXCEEDED → return budget_exceeded
   - If ok → continue
5. LLM_STARTED
6. AgentActivity (with session messages + allowed_completion_tokens)
7. LLM_COMPLETED
8. RecordUsageActivity → USAGE_RECORDED (Postgres llm_calls)
   - If actual_total > max_total → budget_exceeded path
9. SaveSessionActivity (Redis)
10. SaveResultActivity (with usage)
11. RecordExecutionCompletedActivity
12. TASK_COMPLETED
```

## Implementation Tasks

### Task 2: Events
Add SESSION_LOADED, USAGE_RECORDED, TASK_BUDGET_EXCEEDED to events/types.go

### Task 3: Redis Session Methods
Add LoadSessionMessages / SaveSessionMessages to redis/client.go

### Task 4: Session Activities  
Implement LoadSessionActivity / SaveSessionActivity in activities/session.go

### Task 5: Budget Activities
Implement EstimatePromptTokensActivity / CheckBudgetActivity in activities/budget.go

### Task 6: RecordUsageActivity
Implement in activities/usage.go

### Task 7: AgentActivity Enhancement
Add SessionMessages and AllowedCompletionTokens to input

### Task 8: Workflow Update
Full orchestration with all new activities

### Task 9: Gateway API Updates
Ensure usage is returned in GET /tasks/{id}

### Task 10: E2E Verification
Full test suite

## Files to Modify

1. internal/events/types.go - Add 3 events
2. internal/redis/client.go - Add session methods  
3. internal/activities/session.go - Implement session activities
4. internal/activities/budget.go - Implement budget activities
5. internal/activities/usage.go - Implement usage recording
6. internal/activities/agent.go - Enhance with session messages
7. internal/activities/task.go - Update SaveResultInput with usage
8. internal/workflows/simple.go - Full orchestration
9. internal/api/handler.go - Check GET /tasks returns usage
10. cmd/worker/main.go - Register new activities

## Redis Keys

- Session messages: `session:{session_id}:messages` (List, JSON encoded LLMMessage)
- TTL: 7 days (604800 seconds)
- Keep: last 50 messages
- Order: oldest first for LLM injection

## Postgres Tables

- llm_calls: already exists with all needed fields
- tasks: has usage_prompt_tokens/completion_tokens/total_tokens fields

## Validation Checklist

- [ ] Normal task: completed, result with mock answer, usage populated
- [ ] llm_calls record created
- [ ] Redis session contains user+assistant messages
- [ ] Second task in same session loads history
- [ ] Budget exceeded: no LLM call, no llm_calls record, TASK_BUDGET_EXCEEDED event
- [ ] GET /tasks/{id} returns usage fields