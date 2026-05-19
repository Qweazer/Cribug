# Cribug - Multi-Agent LLM Orchestration MVP

An intelligent agent orchestrating LLM calls with Temporal workflows.

## Current Status

MVP complete: Single-agent SimpleWorkflow with Gateway, Temporal, Worker, Python LLM Service (mock), Postgres, Redis, SSE, Session Memory, Usage Recording, and Budget Tracking.

## Architecture

```
┌──────────┐
│  Client  │
└────┬─────┘
     │ HTTP
     ▼
┌──────────┐     ┌──────────────────────────────────────────────────────────┐
│ Gateway  │────>│ Temporal                                                  │
└──────────┘     │  ┌────────────────────────────────────────────────────┐  │
                  │  │ SimpleWorkflow                                      │  │
                  │  │  LoadSession → Estimate → CheckBudget → Agent → ... │  │
                  │  └────────────────────────────────────────────────────┘  │
                  │         │                                                  │
                  │         ▼                                                  │
                  │  ┌────────────┐                                           │
                  │  │   Worker   │                                           │
                  │  └────────────┘                                           │
                  └──────────────────────────────────────────────────────────┘
                           │                    │
                           ▼                    ▼
                  ┌────────────────┐    ┌──────────────────┐
                  │  Python LLM   │    │     Postgres     │
                  │   (Mock)       │    │ tasks/executions │
                  └────────────────┘    │ llm_calls        │
                                         └──────────────────┘
                           │
                           ▼
                  ┌────────────────┐
                  │     Redis      │
                  │ task:status    │
                  │ task:*:events  │
                  │ session:*:msgs │
                  └────────────────┘
```

## Components

| Component | Description |
|-----------|-------------|
| Gateway | REST API: POST /tasks, GET /tasks/{id}, GET /stream/sse |
| Temporal | Workflow orchestration engine |
| Worker | Executes SimpleWorkflow activities |
| Python LLM Service | Mock LLM with /health and /chat endpoints |
| Postgres | Persistent storage: tasks, executions, llm_calls |
| Redis | Caching: task status, events stream, session memory |

## Local Startup

### Prerequisites

- Docker Desktop (Windows) or Docker daemon (Linux/WSL)
- Go 1.21+
- Python 3.10+

### Step 1: Start Infrastructure

```bash
docker.exe compose -f deploy/docker-compose.yaml up -d postgres redis temporal temporal-ui
```

Verify:
```bash
docker.exe ps --format "table {{.Names}}\t{{.Ports}}"
# deploy-postgres-1     0.0.0.0:5432->5432/tcp
# deploy-redis-1        0.0.0.0:6379->6379/tcp
# deploy-temporal-1     0.0.0.0:17233->7233/tcp
# deploy-temporal-ui-1  0.0.0.0:18088->8080/tcp
```

### Step 2: Setup Environment

```bash
cp .env.example .env
```

### Step 3: Build

```bash
bash scripts/build.sh
```

### Step 4: Start Services

Terminal 1 - Python LLM Service:
```bash
bash scripts/run-llm-service.sh
```

Terminal 2 - Gateway:
```bash
bash scripts/run-gateway.sh
```

Terminal 3 - Worker:
```bash
bash scripts/run-worker.sh
```

## WSL/Windows Notes

**Important:** Temporal port mapping:
- Container internal port: `7233`
- Host mapped port: `17233`
- **Use:** `TEMPORAL_ADDRESS=127.0.0.1:17233`
- **NOT:** `127.0.0.1:7233`

Temporal UI: http://127.0.0.1:18088

## API Examples

### Health Check

```bash
curl --noproxy '*' -s http://127.0.0.1:8080/health | jq
```

### Create Task with Mode

```bash
curl --noproxy '*' -s -X POST http://127.0.0.1:8080/api/v1/tasks \
  -H "Content-Type: application/json" \
  -d '{
    "query": "Hello world",
    "session_id": "my-session",
    "config": {
      "mode": "simple",
      "model": "gpt-4o-mini",
      "temperature": 0.7,
      "max_total_tokens": 8000,
      "max_completion_tokens": 128,
      "enable_tools": false
    }
  }' | jq
```

**Mode options:**
- `simple` (default): Single-agent execution via SimpleWorkflow
- `dag`: DAG workflow with multiple nodes (requires `ENABLE_DAG_WORKFLOW=true`)
- `multi_agent`: Multi-agent chain (planner → worker → critic → synthesizer, requires `ENABLE_MULTI_AGENT=true`)

**Feature flags:**
- `ENABLE_DAG_WORKFLOW=true`: Enable DAG mode
- `ENABLE_MULTI_AGENT=true`: Enable multi-agent mode
- `ENABLE_TOOLS=true`: Enable tool calling (calculator, echo)

When a feature is disabled and the corresponding config is set, the API returns HTTP 400 with `validation_error`.

### Multi-Agent Lite (Phase 3B Slice 5.1-5.2)

Multi-agent workflow is implemented with the following status:

- **mode=multi_agent** controlled by `ENABLE_MULTI_AGENT` feature flag
- **Roles:** planner → researcher → critic → synthesizer (sequential, no concurrency)
- **Agent execution:**
  - planner: mock, returns "planned approach for task <task_id>"
  - researcher: mock, returns "researched context for task <task_id>"
  - critic: mock, returns "reviewed draft for task <task_id>"
  - synthesizer: **LLM-backed**, calls Python LLM Service with combined prompt
- **Synthesizer LLM call:**
  - Combines original query + planner output + researcher output + critic output
  - Records usage to llm_calls with agent_role='synthesizer'
  - Budget check before LLM call (prevents LLM_STARTED if budget exceeded)
- **No llm_calls for planner/researcher/critic:** Only synthesizer writes llm_calls
- **Events emitted:**
  - WORKFLOW_STARTED (1)
  - SESSION_LOADED (1)
  - AGENT_STARTED planner (1)
  - AGENT_COMPLETED planner (1)
  - AGENT_STARTED researcher (1)
  - AGENT_COMPLETED researcher (1)
  - AGENT_STARTED critic (1)
  - AGENT_COMPLETED critic (1)
  - AGENT_STARTED synthesizer (1)
  - LLM_STARTED synthesizer (1)
  - LLM_COMPLETED synthesizer (1)
  - USAGE_RECORDED (1)
  - AGENT_COMPLETED synthesizer (1)
  - MULTI_AGENT_SYNTHESIZED (1)
  - TASK_COMPLETED (1)
- **Failure path:** Query containing `__force_multi_agent_failure__` triggers failure
- **Budget path:** max_total_tokens too low triggers TASK_BUDGET_EXCEEDED
- **Current state:** Uses mock Python LLM Service (not real OpenAI)
- **Future:** Slice 5.3+ will add real OpenAI integration

```bash
# Test multi-agent skeleton (requires ENABLE_MULTI_AGENT=true on Gateway and Worker)
ENABLE_MULTI_AGENT=true bash scripts/test_multi_agent_skeleton.sh

# Test multi-agent LLM execution
ENABLE_MULTI_AGENT=true bash scripts/test_multi_agent_llm_execution.sh

# Test budget protection
ENABLE_MULTI_AGENT=true bash scripts/test_multi_agent_llm_budget.sh

# Test failure path
ENABLE_MULTI_AGENT=true bash scripts/test_multi_agent_failure.sh

# Run full multi-agent lite test suite
ENABLE_MULTI_AGENT=true bash scripts/test_multi_agent_lite_full.sh
```

## DAG Workflow Planning (Slice 4.3)

With `ENABLE_DAG_WORKFLOW=true`, setting `config.mode="dag"` routes to DAGWorkflow with planning. DAG planning:
- Emits WORKFLOW_STARTED, SESSION_LOADED, TASK_CLASSIFIED, DAG_PLANNED, TASK_COMPLETED events
- Loads session memory
- ClassifyTaskActivity classifies task (simple/analysis/creative)
- PlanDAGActivity generates minimal 2-node DAG (analyze_input → draft_answer)
- Returns result: `"dag plan created: category=X, complexity=Y, nodes=N, edges=M"`
- **Does NOT** call LLM or write llm_calls records

```bash
# Test DAG planning (requires ENABLE_DAG_WORKFLOW=true on Gateway and Worker)
ENABLE_DAG_WORKFLOW=true bash scripts/test_dag_plan.sh
```

Note: DAG node execution and synthesis are in Slice 4.4+.

### Get Task Status

```bash
curl --noproxy '*' -s http://127.0.0.1:8080/api/v1/tasks/{task_id} | jq
```

### SSE Stream

```bash
curl --noproxy '*' -N "http://127.0.0.1:8080/api/v1/stream/sse?task_id={task_id}"
```

## Redis Verification

### Task Events Stream

```bash
redis-cli -h 127.0.0.1 -p 6379 XRANGE "task:{task_id}:events" - +
```

Expected events: TASK_CREATED, WORKFLOW_STARTED, SESSION_LOADED, LLM_STARTED, LLM_COMPLETED, USAGE_RECORDED, TASK_COMPLETED

### Session Memory

```bash
redis-cli -h 127.0.0.1 -p 6379 LRANGE "session:{session_id}:messages" 0 -1
redis-cli -h 127.0.0.1 -p 6379 TTL "session:{session_id}:messages"
```

Session TTL: ~7 days (604800 seconds)
Max messages: 50

### Session Messages Format

```json
{"role":"user","content":"your query"}
{"role":"assistant","content":"mock answer: your query"}
```

## Budget Tracking

Create task with low `max_total_tokens`:

```bash
curl --noproxy '*' -s -X POST http://127.0.0.1:8080/api/v1/tasks \
  -H "Content-Type: application/json" \
  -d '{
    "query": "This is a long prompt that will exceed budget",
    "max_total_tokens": 1
  }' | jq
```

Expected response:
- `status`: "budget_exceeded"
- `error_type`: "budget_exceeded"
- No LLM call made

## Smoke Test

```bash
bash scripts/smoke_test.sh
```

This validates:
1. Gateway health
2. Normal task completion
3. Usage recording
4. Session memory
5. Budget exceeded path
6. SSE streaming

## Scripts

| Script | Description |
|--------|-------------|
| `scripts/build.sh` | Build gateway and worker |
| `scripts/run-gateway.sh` | Start Gateway |
| `scripts/run-worker.sh` | Start Worker |
| `scripts/run-llm-service.sh` | Start Python LLM Service |
| `scripts/test_sse.sh` | Test SSE endpoint |
| `scripts/smoke_test.sh` | Full MVP smoke test |
| `scripts/test_dag_plan.sh` | Test DAG planning (requires ENABLE_DAG_WORKFLOW=true) |

## Troubleshooting

### "No workers polling" in Temporal UI

Check:
1. Worker is running: `pgrep -f worker`
2. TEMPORAL_ADDRESS is correct: `echo $TEMPORAL_ADDRESS` (should be `127.0.0.1:17233`)
3. TEMPORAL_TASK_QUEUE matches: `echo $TEMPORAL_TASK_QUEUE` (should be `orchestrator-task-queue`)

### "workflow_start_error" when creating task

Gateway cannot reach Temporal:
- Check TEMPORAL_ADDRESS=127.0.0.1:17233
- Check Temporal container is running: `docker.exe ps | grep temporal`
- Restart Temporal: `docker.exe restart deploy-temporal-1`

### Task stuck in "running" status

1. Check Worker logs: `tail -100 /tmp/worker.log`
2. Check Temporal UI: http://127.0.0.1:18088
3. Verify Worker is polling the correct task queue

### Python LLM Service errors

- Verify it's running: `curl http://127.0.0.1:8000/health`
- Check logs in the terminal running the service
- Restart if needed

### SSE stream hangs

1. Check Redis has terminal event (TASK_COMPLETED/TASK_FAILED/TASK_BUDGET_EXCEEDED)
2. Verify Gateway is reading from Redis Stream
3. Check client didn't disconnect

### Usage/result missing from completed task

1. Check SaveResultActivity executed
2. Check RecordUsageActivity executed
3. Check Postgres has data: `docker.exe exec deploy-postgres-1 psql ...`

## What We DON'T Do (Yet)

This MVP does NOT currently include:
- Real DAG workflows (Phase 3A - DAG planning complete, execution in progress)
- Real multi-agent chains (Phase 3B - skeleton complete, LLM integration upcoming)
- Tool calling (Phase 3C upcoming)
- RAG (Retrieval Augmented Generation)
- MCP (Model Context Protocol)
- OPA (Open Policy Agent)
- Sandbox execution
- Web UI
- Real OpenAI API calls
- Cancel API

## License

MIT
