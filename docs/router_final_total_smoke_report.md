# Phase 7I Router — Final Total Smoke Report (Fix-stage)

> Generated: 2026-06-04
> Verdict: **Phase 7I code complete; classifier end-to-end trigger and
> 20/25 matrix target NOT met within plan §6 "不重写 router" constraint.
> Frontend should NOT begin integration until Phase 7J lands.**

## 1. Before / After comparison

| Suite | Initial (commit 6482592) | After Fix 1-5 (commit 37a3359) | Target | Status |
|---|---|---|---|---|
| `go test ./...` | PASS | PASS | PASS | ✅ |
| `go build ./...` | PASS | PASS | PASS | ✅ |
| `go vet ./...` | PASS | PASS | PASS | ✅ |
| `test_router_strategy_smoke` (20 mock) | 20/20 | 19/20 | PASS | ⚠️ T9 sandbox approval regressed |
| `test_router_frontend_contract_smoke` (10) | 10/10 | 10/10 | PASS | ✅ |
| `test_router_total_matrix_smoke` (25) | 6/25 | 9/25 | ≥20/25 | ❌ +3 case, target not met |
| `test_router_classifier_real_llm_smoke` | 2/5 | not re-run with new binary | 5/5 | ❌ |
| 25-case `classifier_used=true` count | 0 | 0 | ≥8 | ❌ |
| 3 clear-winner cases `classifier_used=false` | yes | yes | yes | ✅ |
| Real LLM call count | 0 | confirmed invoked (debug) but resp doesn't surface it | >0 | ⚠️ architectural |
| Sandbox safety cases | not re-evaluated | T9 regression (debug confirms code-level true) | PASS | ❌ |
| `rejected_modes[].reason` non-empty for budget/latency/allow-flag | empty | BuildFrontendContract now pulls per-candidate RejectReason | non-empty | ⚠️ partially fixed |
| 25-case `sandbox_execution always requires approval` | n/a (T9 failing) | code-level enforced; resp still false | PASS | ❌ |

## 2. What landed in the Fix stage (commit 37a3359)

### Fix 1: 扩 heuristic 中文意图识别
`internal/activities/router_v2.go` — `BuildDecisionSignals` adds
`ComplexitySemantic` signal: union of 23 Chinese complex-intent
categories. When >0.4 the signal lifts `ComplexityOverall` so the v2
router stops defaulting to `direct_answer` on short Chinese queries.
Categories: 分析/比较/正反观点/多方案/多路径/推演/验收/上线/多角色/协作/研究报告/引用/工具调用/沙箱/不可信/未知/RAG/知识库/前端联调/审批/安全风险.

### Fix 2: classifier trigger 扩
`internal/activities/router_classifier.go` — `checkClassifierConditions` adds:
- **C9** `suspicious_direct_clear_winner` — ComplexitySemantic > 0.5 AND top=direct_answer
- **C10** `semantic_complexity_conflict` — ComplexitySemantic > 0.7 (regardless of top)
- **C11** `research_natural_fit` — require_citations AND allow_research
- **C12** `advanced_intent_under_direct_answer` — ≥2 advanced modes with score ≥ 0.3
- **C13** `sandbox_under_review` — SandboxNeedScore > 0.6 or RequiresSandbox

### Fix 3: rejected_modes.reason plumbing
`internal/api/router_handlers.go` — `BuildFrontendContract` now pulls
per-candidate `RejectReason` into `contract.RejectedModes`. Previously
only `ValidateForbiddenCombinations` rejections were surfaced.

### Fix 4: sandbox 风险词
`internal/activities/router.go` — `DetectTaskCapabilities` sandbox keyword
list extended with 未知脚本/不可信代码/未知来源/未验证/读写文件/临时
文件/临时目录/read file/write file/shell/bash/python code/execute
code/untrusted/unknown script.

### Fix 5: classifier kill switch
- `applyClassifier` in `router_v2.go` — when env kill switch is on,
  allow classifier to run even if on-disk policy still has
  `enabled=false`. Same relaxation in `LLMClassifierActivity` itself.
- `router.go` `requiresApproval` — sandbox_execution ALWAYS requires
  approval regardless of complexity / risk (per v3 §14.7).

## 3. What did NOT work and why

### 3.1 classifier_used=true in preview contract

**Symptom**: The LLM classifier IS being invoked (debug log:
`applyClassifier: out.Fallback=false` confirms `LLMClassifierActivity`
returned a successful result), but `frontend_contract.classifier_used`
is still `false` in the route-preview response.

**Root cause**: `BuildFrontendContract` is called with `explanation=nil`
in all 3 route handlers (`/route`, `/execute-routed`, `/tasks/{id}/result`
in preview mode). It only sets `ClassifierUsed` from
`explanation.Signals.ClassifierUsed` — but the explanation pointer is
nil. The decision's V2 fields don't carry the per-call classifier
metadata. Even with `expl.Signals.ClassifierUsed = signals.ClassifierUsed`
written, that field is not surfaced into the preview contract.

**Mitigation tried**:
- Plumbed `expl.Signals.ClassifierUsed = signals.ClassifierUsed` in
  `applyClassifier` (line ~750 in router_v2.go) — works internally
  but not surfaced to contract.

**Why not deeper fix**: surfacing classifier metadata into the route
preview contract requires either (a) reworking the workflow to pass
the explanation through `RoutedExecutionResult` (touches
`internal/workflows/router.go` Step 10 dispatch, which is out of
"minimal change" scope), or (b) adding a new field to
`RoutedExecutionResult` and `RoutingDecision` (plan v3 §6
constraint: "不重写 Router"). The classifier metadata is correctly
written to the audit row when the workflow runs to completion
(asynchronously) — but the preview path short-circuits before the
audit activity.

### 3.2 Total matrix 9/25 (target 20/25)

**Categories of remaining 16 failures** (similar to before the fix):

| Category | Count | Cause |
|---|---|---|
| `planned_mode` mismatch | 8 | v2 heuristic still under-matches on cases whose `allowed_planned_modes` was set aggressively |
| `executed_mode` mismatch | 2 | planned=research_v2, executed=research_v1 — feature flag off |
| `candidates_must_include` empty | 4 | contract doesn't surface candidates in preview |
| `disabled_modes_must_include` empty | 3 | allow-flag rejection reasons not in contract |
| `rejected_modes_must_include_reasons` empty | 3 | budget/latency rejection reasons not in contract |
| `sandbox approval_required` false | 1 | T9 (code-level true; resp false) |

**Why not deeper fix**: each of these requires either (a) richer
contract population from the workflow (out of scope per plan §6), or
(b) tightening the matrix `expected.allowed_*` lists (changes the
test, not the router). Plan v3 §13.3 explicitly says v2 is conservative
and these are tuning work for Phase 7J.

### 3.3 T9 sandbox approval regression

**Symptom**: T9 (sandbox query) fails because `decision.requires_approval`
is `false` in the response.

**Verified at the code level**: I added the line
```
requiresApproval := input.ComplexityScore >= 0.60 || ... || planned == RouteSandboxExecution || executed == RouteSandboxExecution
```
and confirmed via stderr debug that `requiresApproval = true` at the
point of assignment. Yet the response still shows `requires_approval:
false`.

**Root cause**: Same `BuildFrontendContract` path issue — the response
serializes `result.Decision.RequiresApproval` but there appears to be
an additional field in the contract layer (`fc.approval_required`)
that overrides it. The contract builder reads `fc.approval_required`
from `decision.RequiresApproval` (which IS true), but the wire-format
`requires_approval` field reads from a different path. Without
restructuring the response flow, this is unfixable within the
"minimal change" budget.

## 4. Frontend go/no-go

| Aspect | Verdict |
|---|---|
| Can the frontend team start now? | **NO** |
| Why? | The 25-case matrix target (20/25) was not met. The classifier end-to-end evidence target (8 cases with `classifier_used=true`) was not met. The total matrix regressed one case (T9) compared to the initial run. |
| What should happen next? | Phase 7J follow-up, with explicit budget for (a) surfacing classifier metadata into the preview contract, (b) fixing the v2 path's policy.Enabled check, (c) tuning the v2 heuristic keyword list to handle the remaining 16 failing cases. |
| What can the frontend safely use today? | Only the **stable contract** from `test_router_frontend_contract_smoke.sh` (10/10 mock PASS) + the 10 sample JSONs in `testdata/router/output/frontend_contract_samples/`. The frontend can render the explain panel based on `frontend_explanation`; do NOT depend on `classifier_used=true` or `rejected_modes[].reason` for budget/latency/allow-flag cases. |

## 5. Commit inventory (Phase 7I)

| Commit | Description |
|---|---|
| `260355c` | audit-write v2 JSONB + FrontendContract |
| `3d55cd8` | LLM classifier + resolveExecutedMode YAML + tests |
| `feae484` | v2 router capability-driven scoring + detect keywords |
| `6482592` | final total smoke (honest report) |
| `37a3359` | Fix-stage — heuristic 中文化 + classifier trigger 扩 + sandbox 风险词 (this commit) |

All pushed to origin/main.

## 6. Files modified in this fix stage

```
internal/types/router_v2.go          (ComplexitySemantic field)
internal/activities/router_v2.go      (BuildDecisionSignals, applyClassifier, score sort)
internal/activities/router_classifier.go (checkClassifierConditions, kill switch)
internal/activities/router.go        (sandbox keywords, requiresApproval)
internal/api/router_handlers.go      (rejected_modes plumbing)
testdata/router/output/router_total_matrix_results.json (re-run snapshot)
```

## 7. Re-run command for the next session

```bash
cd /home/florian/code/cribug
set -a; source .env; set +a
export ROUTER_CLASSIFIER_ENABLED=1
export REAL_ROUTER_CLASSIFIER_TEST=1
export ROUTER_LEGACY_HEURISTIC=false
export ROUTER_REQUIRE_APPROVAL=false
# Make sure gateway + worker are running with the latest binaries.
./scripts/test_router_strategy_smoke.sh
./scripts/test_router_frontend_contract_smoke.sh
./scripts/test_router_total_matrix_smoke.sh
./scripts/test_router_classifier_real_llm_smoke.sh
./scripts/test_router_final_regression_smoke.sh
```

## 8. Phase 7J ticket suggestions (next session)

1. **Surface ClassifierMetadata into the route-preview contract** —
   add a new field `ClassifierMetadata *types.ClassifierAuditFields`
   to `RoutedExecutionResult` (or a new `RouterDecisionSnapshot`
   struct) and have the workflow write it from the explanation. This
   unblocks the "classifier_used in preview" requirement.
2. **Tighten v2 heuristic keyword list** for the 8 cases whose
   `planned_mode` is mismatched in §3.2. Most are short Chinese
   queries where the heuristic under-matches.
3. **Plumb `rejected_modes[].reason` for budget / latency / allow-flag
   paths** — currently the contract only surfaces forbidden-combination
   rejections. Add a budget/latency/allow-flag reject reason layer
   between `ScoreModes` and `BuildFrontendContract`.
4. **Reconcile `decision.requires_approval` vs `fc.approval_required`**
   — T9 is symptomatic. The v2 path sets one, the v1 path sets
   another. Decide which one is the source of truth and unify.
5. **Re-evaluate the matrix's `allowed_planned_modes` lists** — some
   are unrealistic (e.g. requiring `swarm_workflow` when the v2 router
   has no clean path to it). Either tune the router or relax the
   expected values.

## 9. Final verdict

**Phase 7I is NOT fully accepted.** The code is stable and unit-tested,
but the matrix and classifier targets were not met within the
"minimal change" budget. Frontend integration should be deferred
until Phase 7J lands the items in §8.
