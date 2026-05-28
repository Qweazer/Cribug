# Phase 6E-4: Agent RAG Integration — Design Spec

## 版本说明

把 6E-3 RAGQueryWorkflow / retrieval activities 接入 ReAct、Swarm、Skills 执行链。不重新实现 embedding、Qdrant、chunking。

## 1. Step 1: ReAct + RAG

### Integration point
In `internal/workflows/patterns/react.go`, at the start of each agent step (before LLM call), optionally execute RAG retrieval and inject context.

### Flow
```
ReAct loop iteration:
  1. Check if task needs retrieval (detect "search:", "retrieve:", "lookup:" prefix, or task complexity)
  2. If yes: ExecuteActivity → RAGQueryWorkflow or retrieval activities inline
  3. Inject packed context into next LLM prompt as system/context message
  4. on_agent_step hook records retrieval summary (hit_count, context_size, not full chunks)
```

### Key: Don't start a child workflow for every retrieval. Use direct activity call:
```go
// In workflow execution context:
if needsRetrieval(input.Query, stepResult) {
    var searchOutput activities.SearchHitsOutput
    workflow.ExecuteActivity(ao, "EmbedAndSearchChunksActivity", ...).Get(ctx, &searchOutput)
    // Inject into prompt
    contextStr = packContext(searchOutput.Hits)
}
```

### Tests
- ReAct without retrieval trigger → no RAG call
- ReAct with "search: xxx" → retrieval called, context injected
- Empty retrieval results → fallback prompt added

## 2. Step 2: Swarm + RAG

### Integration point
Swarm agents share retrieval context via workflow-level state. Leader fetches, workers receive context_id.

### Flow
```
SwarmWorkflow:
  Leader decides retrieval needed → executes retrieval → stores result
  Workers receive retrieval_result_id in task input
  Workers fetch/use packed context, don't re-execute same retrieval
```

### Tests
- Single agent uses RAG context
- Multi-agent shares same retrieval result
- Retrieval failure doesn't crash Swarm

## 3. Step 3: Skills + RAG

### Integration point
`SkillExecutionWorkflow` can pass `retrieval_context` as input to skills like `llm_summary_skill`.

### Flow
```
SkillExecutionWorkflow:
  If skill params include "retrieve_context": true:
    Execute retrieval activity  
    Pass packed context as additional parameter to skill
```

### Python skill change
`llm_summary_skill` accepts optional `context` parameter to ground summary in retrieved documents.

### Tests
- Skill execution with retrieved context
- Skill without context still works
- Context size limit respected

## 4. Step 4: Hooks + RAG audit

### Hooks (reuse Phase 6D HookPoints, no new ones)
- `before_tool_call` / `after_tool_call`: tool_type="rag_retrieval"
- `on_agent_step`: records retrieval usage summary (hit_count, context_size, not full chunks)
- `on_error`: embedding/qdrant/fetch/pack failures

### Audit
- Hooks record IDs, scores, context size summary only — NOT full chunk content

## 5. File changes

```
internal/workflows/patterns/react.go  — add retrieval decision + injection
internal/workflows/swarm.go           — add shared retrieval context
internal/workflows/skill_execution.go  — add context injection for skills
python_llm_service/llm_service/tools/builtin/llm_summary_skill.py — accept context param
```

## 6. Non-Goals
- No new HookPoint types
- No new migration
- No modifying go/ or python/llm-service/
- No Qdrant/Python/embed direct calls in Workflow
