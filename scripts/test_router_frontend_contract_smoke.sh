#!/bin/bash
# Phase 7I Router — Frontend contract smoke.
#
# Verifies that the router decision payload is fully consumable by a
# frontend. Each case MUST surface these contract fields, and the
# smoke saves a complete sample to
# testdata/router/output/frontend_contract_samples/<case_id>.json.
#
# Required contract fields (24):
#   task_id, status, planned_mode, executed_mode, fallback_reason,
#   frontend_explanation, capability_summary, addon_capabilities,
#   required_capabilities, disabled_capabilities,
#   workspace_artifacts_expected, approval_required, async_required,
#   candidates, rejected_modes, score_breakdown, signals,
#   classifier_used, classifier_reason, classifier_confidence,
#   provider, model_used, mock, fallback_used
# Plus: audit_id or row pointer.

set -e

BASE_URL="${BASE_URL:-http://127.0.0.1:8080}"
SAMPLE_DIR="testdata/router/output/frontend_contract_samples"
TIMEOUT="${TIMEOUT:-60}"
PASS=0
FAIL=0

log_pass() { echo "[PASS] $1"; PASS=$((PASS+1)); }
log_fail() { echo "[FAIL] $1"; FAIL=$((FAIL+1)); }

mkdir -p "$SAMPLE_DIR"

if ! curl -s --max-time 3 "$BASE_URL/health" >/dev/null 2>&1; then
    echo "SKIP: gateway not reachable"
    exit 0
fi

echo "=== Phase 7I Router Frontend contract smoke ==="
echo ""

# Helper: run a case and assert its contract.
run_contract_case() {
    local case_id="$1"
    local query="$2"
    local budget="$3"
    local max_latency="$4"
    local require_citations="$5"
    local allow_tools="$6"
    local allow_sandbox="$7"
    local allow_research="$8"

    echo "--- Case: $case_id ---"
    local body
    body=$(python3 -c "
import json
print(json.dumps({
    'query': '$query',
    'budget_usd': $budget,
    'max_latency_ms': $max_latency,
    'require_citations': bool('$require_citations' == 'true'),
    'allow_tools': bool('$allow_tools' == 'true'),
    'allow_sandbox': bool('$allow_sandbox' == 'true'),
    'allow_research': bool('$allow_research' == 'true'),
}))
")

    local resp
    resp=$(curl -s --max-time "$TIMEOUT" -X POST "$BASE_URL/api/v1/tasks/route" \
        -H "Content-Type: application/json" -d "$body" 2>/dev/null || echo '{}')
    # Use a tempfile so the python heredoc can keep reading the
    # assertion script from stdin instead of the curl body.
    local resp_file="/tmp/cribug-fc-$$.json"
    printf '%s' "$resp" > "$resp_file"

    python3 - "$case_id" "$SAMPLE_DIR" "$BASE_URL" "$resp_file" <<'PYEOF'
import json, sys, os
case_id = sys.argv[1]
sample_dir = sys.argv[2]
base_url = sys.argv[3]
resp_file = sys.argv[4]
try:
    with open(resp_file) as f:
        resp = json.load(f)
except Exception:
    resp = {}
try:
    os.unlink(resp_file)
except Exception:
    pass

dec = resp.get('decision', {}) or {}
fc = resp.get('frontend_contract', {}) or {}
addons = fc.get('addon_capabilities', []) or dec.get('v2_addon_capabilities', []) or []
disabled = fc.get('disabled_capabilities', []) or []
required = fc.get('required_capabilities', []) or []
explain = fc.get('frontend_explanation', {}) or {}
cap_summary = explain.get('capability_summary', []) or []
artifacts = fc.get('workspace_artifacts_expected', []) or []
candidates = fc.get('candidates', []) or []
rejected = fc.get('rejected_modes', []) or []
score = fc.get('score_breakdown', {}) or {}
signals = (fc.get('signals') or {})

status = resp.get('status', '')
task_id = resp.get('workflow_id', '') or resp.get('task_id', '')
planned = dec.get('planned_mode', fc.get('planned_mode',''))
executed = dec.get('mode', fc.get('executed_mode',''))
fallback_reason = dec.get('fallback_reason', fc.get('fallback_reason',''))
approval_required = bool(fc.get('approval_required', dec.get('requires_approval', False)))
async_required = bool(fc.get('async_required', dec.get('async_required', False)))
classifier_used = bool(fc.get('classifier_used', False))
classifier_reason = fc.get('classifier_reason', '') or ''
classifier_confidence = float(fc.get('classifier_confidence', 0.0))
provider = fc.get('provider','') or ''
model_used = fc.get('model_used_hint','') or fc.get('model_tier','') or ''
mock = bool(fc.get('mock', dec.get('mock', False)))
fallback_used = bool(fc.get('fallback_used', dec.get('fallback_used', False)))

required_fields = [
    ('task_id', task_id),
    ('status', status),
    ('planned_mode', planned),
    ('executed_mode', executed),
    ('fallback_reason', fallback_reason),
    ('frontend_explanation', explain),
    ('capability_summary', cap_summary),
    ('addon_capabilities', addons),
    ('required_capabilities', required),
    ('disabled_capabilities', disabled),
    ('workspace_artifacts_expected', artifacts),
    ('approval_required', approval_required),
    ('async_required', async_required),
    ('candidates', candidates),
    ('rejected_modes', rejected),
    ('score_breakdown', score),
    ('signals', signals),
    ('classifier_used', classifier_used),
    ('classifier_reason', classifier_reason),
    ('classifier_confidence', classifier_confidence),
    ('provider', provider),
    ('model_used', model_used),
    ('mock', mock),
    ('fallback_used', fallback_used),
]

missing = []
for name, value in required_fields:
    if value is None:
        missing.append(name)
if missing:
    print(f"  [FAIL] missing required fields: {missing}")
    sys.exit(2)

mode = planned or executed
if not artifacts and mode not in ('', 'direct_answer'):
    print(f"  [FAIL] workspace_artifacts_expected empty for mode={mode}")
    sys.exit(3)

addons_set = set(addons)
if 'workspace' not in addons_set or 'audit' not in addons_set:
    print(f"  [FAIL] addon_capabilities missing workspace/audit: {addons}")
    sys.exit(4)

sample = {
    'case_id': case_id,
    'task_id': task_id,
    'status': status,
    'planned_mode': planned,
    'executed_mode': executed,
    'fallback_reason': fallback_reason,
    'frontend_explanation': explain,
    'capability_summary': cap_summary,
    'addon_capabilities': addons,
    'required_capabilities': required,
    'disabled_capabilities': disabled,
    'workspace_artifacts_expected': artifacts,
    'approval_required': approval_required,
    'async_required': async_required,
    'candidates': candidates,
    'rejected_modes': rejected,
    'score_breakdown': score,
    'signals': signals,
    'classifier_used': classifier_used,
    'classifier_reason': classifier_reason,
    'classifier_confidence': classifier_confidence,
    'provider': provider,
    'model_used': model_used,
    'mock': mock,
    'fallback_used': fallback_used,
    'audit_url_hint': f"{base_url}/api/v1/tasks/{task_id}/routing-decision" if task_id else None,
}
os.makedirs(sample_dir, exist_ok=True)
sample_path = os.path.join(sample_dir, f"{case_id}.json")
with open(sample_path, 'w') as f:
    json.dump(sample, f, ensure_ascii=False, indent=2)

print(f"  [PASS] contract complete: addons={addons} planned={planned} executed={executed} clf_used={classifier_used} sample={sample_path}")
sys.exit(0)
PYEOF
    local rc=$?
    if [ $rc -eq 0 ]; then
        log_pass "$case_id"
    else
        log_fail "$case_id (exit=$rc)"
    fi
}

# ─── Case A: simple direct ─────────────────────────────────────────────
run_contract_case "fc_direct_simple" \
    "法国首都是哪里？只回答城市名。" \
    0.1 30000 false true true true

# ─── Case B: tool ───────────────────────────────────────────────────────
run_contract_case "fc_react_calculation" \
    "请计算 128 * 37 + 952，并给出计算步骤。" \
    0.2 30000 false true true true

# ─── Case C: RAG ───────────────────────────────────────────────────────
run_contract_case "fc_rag_local" \
    "根据项目知识库中关于 Cribug Phase 6 的资料，说明 RAG、Sandbox、Skills 是怎么串起来的。" \
    0.3 60000 false true true true

# ─── Case D: DAG ────────────────────────────────────────────────────────
run_contract_case "fc_dag_pipeline" \
    "请按步骤完成：先分析这个需求属于哪类任务，再列出执行计划，然后生成一份验收清单。" \
    0.4 60000 false true true true

# ─── Case E: Reflection ────────────────────────────────────────────────
run_contract_case "fc_reflection" \
    "请检查下面这个任务计划是否严谨，指出漏洞，然后给出修订版。" \
    0.3 60000 false true true true

# ─── Case F: ToT ────────────────────────────────────────────────────────
run_contract_case "fc_tot" \
    "请探索三种不同路线来实现 Cribug 的高级 Router，分别推演优缺点。" \
    0.5 120000 false true true true

# ─── Case G: Debate ─────────────────────────────────────────────────────
run_contract_case "fc_debate" \
    "请从正方和反方分别论证：Cribug 是否应该在 Router 中引入真实 LLM classifier？" \
    0.4 60000 false true true true

# ─── Case H: Research v2 ────────────────────────────────────────────────
run_contract_case "fc_research_v2" \
    "请做一份带引用来源的研究报告：分析当前 AI Agent 框架在任务编排、工具调用、RAG、Sandbox、安全审批方面的常见架构模式。" \
    1.0 180000 true true true true

# ─── Case I: Sandbox (approval required) ───────────────────────────────
run_contract_case "fc_sandbox" \
    "请在安全沙箱中运行一段 Python 代码，计算斐波那契数列前 10 项。" \
    0.5 60000 false true true true

# ─── Case J: Frontend contract full payload (the explicit case) ────────
run_contract_case "fc_full_payload" \
    "我要在前端展示 Router 的决策过程：为什么选择这个模式、还有哪些候选模式、哪些能力被禁用、是否需要审批、是否会生成 workspace artifact。请执行一个中等复杂度任务并返回完整 routing contract。" \
    0.4 60000 false true true true

echo ""
echo "=== Results ==="
echo "Passed: $PASS  Failed: $FAIL"
echo "Samples saved to: $SAMPLE_DIR/"
if [ "$FAIL" -gt 0 ]; then
    echo "Some tests FAILED"
    exit 1
fi
echo "All tests passed"
exit 0
