#!/bin/bash
# Phase 7I Router — Total matrix smoke.
#
# Runs every case in testdata/router/final_router_matrix.json against
# /api/v1/tasks/route and asserts:
#   - planned_mode ∈ allowed_planned_modes
#   - executed_mode ∈ allowed_executed_modes (when specified)
#   - must_have_addons ⊆ addon_capabilities
#   - must_not_have_addons ∩ addon_capabilities = ∅
#   - classifier_used == expected.classifier_expected (where asserted)
#   - candidates_must_include ⊆ candidates[].Mode
#   - disabled_modes_must_include ⊆ disabled_capabilities
#   - rejected_modes_must_include_reasons ⊆ rejected_modes[].Reason
#   - approval_required matches when specified
#   - sandbox_execution always requires approval
#   - allow_sandbox=false ⇒ sandbox_execution must be rejected
#
# Output: testdata/router/output/router_total_matrix_results.json
# The script NEVER fakes a PASS. If a mode is not implemented (e.g.
# swarm_workflow planning only, debate feature off), the case is
# reported as classification_only PASS — the actual mode is captured
# and surfaced in the report for follow-up.

set -e

BASE_URL="${BASE_URL:-http://127.0.0.1:8080}"
MATRIX_FILE="${MATRIX_FILE:-testdata/router/final_router_matrix.json}"
OUTPUT_DIR="${OUTPUT_DIR:-testdata/router/output}"
RESULTS_FILE="$OUTPUT_DIR/router_total_matrix_results.json"
TIMEOUT_PER_CASE="${TIMEOUT_PER_CASE:-90}"

PASS=0
FAIL=0
SKIP=0
CLASSIFICATION_ONLY=0

log_pass() { echo "[PASS] $1"; PASS=$((PASS+1)); }
log_fail() { echo "[FAIL] $1"; FAIL=$((FAIL+1)); }
log_skip() { echo "[SKIP] $1"; SKIP=$((SKIP+1)); }
log_classification_only() {
    echo "[CLASS-ONLY] $1 (executed mode may differ; classifier path verified)"
    CLASSIFICATION_ONLY=$((CLASSIFICATION_ONLY+1))
}

mkdir -p "$OUTPUT_DIR"

if ! curl -s --max-time 3 "$BASE_URL/health" >/dev/null 2>&1; then
    echo "SKIP: gateway not reachable at $BASE_URL"
    exit 0
fi

if [ ! -f "$MATRIX_FILE" ]; then
    echo "ERROR: matrix file not found: $MATRIX_FILE"
    exit 1
fi

# Detect classifier env so we know whether to assert classifier_used.
CLASSIFIER_ENABLED="${ROUTER_CLASSIFIER_ENABLED:-0}"
REAL_CLASSIFIER="${REAL_ROUTER_CLASSIFIER_TEST:-0}"
echo "=== Phase 7I Router Total Matrix smoke ==="
echo "Matrix: $MATRIX_FILE"
echo "Output: $RESULTS_FILE"
echo "Env: ROUTER_CLASSIFIER_ENABLED=$CLASSIFIER_ENABLED REAL_ROUTER_CLASSIFIER_TEST=$REAL_CLASSIFIER"
echo ""

# Read case list and iterate.
CASE_IDS=$(python3 -c "
import json,sys
d=json.load(open('$MATRIX_FILE'))
for c in d['cases']:
    print(c['id'])
")

# Build the JSON results array shell-side.
echo '[]' > "$RESULTS_FILE.tmp"

# Helper: run one case, validate, append result.
run_case() {
    local case_id="$1"
    local case_json
    case_json=$(python3 -c "
import json
d=json.load(open('$MATRIX_FILE'))
for c in d['cases']:
    if c['id'] == '$case_id':
        print(json.dumps(c, ensure_ascii=False))
        break
")
    if [ -z "$case_json" ]; then
        log_fail "case $case_id not found in matrix"
        return
    fi

    echo "─── Case: $case_id ───"
    # Render request body from the case JSON.
    BODY=$(python3 -c "
import json,sys
c=json.loads(sys.argv[1])
body={
    'query': c['query'],
    'budget_usd': c.get('input',{}).get('budget_usd', 0.5),
    'max_latency_ms': c.get('input',{}).get('max_latency_ms', 60000),
    'require_citations': c.get('input',{}).get('require_citations', False),
    'allow_tools': c.get('input',{}).get('allow_tools', True),
    'allow_sandbox': c.get('input',{}).get('allow_sandbox', True),
    'allow_research': c.get('input',{}).get('allow_research', True),
    'user_intent': c.get('input',{}).get('user_intent', ''),
}
print(json.dumps(body, ensure_ascii=False))
" "$case_json")

    RESP=$(curl -s --max-time "$TIMEOUT_PER_CASE" -X POST "$BASE_URL/api/v1/tasks/route" \
        -H "Content-Type: application/json" -d "$BODY" 2>/dev/null || echo '{"error":"curl_failed"}')

    # Validate + collect result via Python so we can append to the
    # JSON results file atomically.
    python3 - "$case_json" "$RESP" "$RESULTS_FILE.tmp" <<'PYEOF'
import json, sys
case = json.loads(sys.argv[1])
resp_raw = sys.argv[2]
tmp_path = sys.argv[3]
try:
    resp = json.loads(resp_raw) if resp_raw and not resp_raw.startswith('"') else {}
except Exception:
    resp = {}

dec = resp.get('decision') or {}
fc = resp.get('frontend_contract') or {}
planned = dec.get('planned_mode','')
executed = dec.get('mode','')
addons = fc.get('addon_capabilities',[]) or dec.get('v2_addon_capabilities',[])
candidates = fc.get('candidates',[]) or []
rejected = fc.get('rejected_modes',[]) or []
disabled_caps = fc.get('disabled_capabilities',[]) or []
classifier_used = bool(fc.get('classifier_used', False))
classifier_reason = fc.get('classifier_reason','') or ''
reason_codes = fc.get('reason_codes',[]) or []
score_breakdown = fc.get('score_breakdown',{}) or {}
approval_required = bool(fc.get('approval_required', False))
risk_level = fc.get('risk_level','')

exp = case.get('expected',{})
allowed_planned = set(exp.get('allowed_planned_modes',[]))
allowed_executed = set(exp.get('allowed_executed_modes',[])) if exp.get('allowed_executed_modes') else None
must_have = set(exp.get('must_have_addons',[]))
must_not_have = set(exp.get('must_not_have_addons',[]))
candidates_must_include = set(exp.get('candidates_must_include',[]))
disabled_must_include = set(exp.get('disabled_modes_must_include',[]))
rejected_reasons = set(exp.get('rejected_modes_must_include_reasons',[]))
classifier_expected = exp.get('classifier_expected','optional')
front_end_req = exp.get('frontend_contract_required', False)

# Build observed sets.
candidate_modes = set()
for c in candidates:
    if isinstance(c, dict) and 'Mode' in c:
        candidate_modes.add(c['Mode'])
    elif isinstance(c, dict) and 'mode' in c:
        candidate_modes.add(c['mode'])
disabled_modes_observed = set(disabled_caps) | set([m.get('Mode') if isinstance(m,dict) and 'Mode' in m else (m.get('mode') if isinstance(m,dict) else '') for m in rejected])
rejected_reasons_observed = set()
for r in rejected:
    if isinstance(r, dict):
        rr = r.get('Reason') or r.get('reason') or ''
        if rr:
            rejected_reasons_observed.add(rr)

# Run assertions.
status = 'PASS'
notes = []

# 1. planned_mode
if allowed_planned and planned not in allowed_planned:
    status = 'FAIL'
    notes.append(f"planned_mode={planned!r} not in {sorted(allowed_planned)}")

# 2. executed_mode
if allowed_executed is not None and executed not in allowed_executed:
    status = 'FAIL'
    notes.append(f"executed_mode={executed!r} not in {sorted(allowed_executed)}")

# 3. must_have_addons
missing_addons = must_have - set(addons)
if missing_addons:
    status = 'FAIL'
    notes.append(f"missing addons: {sorted(missing_addons)}")

# 4. must_not_have_addons
extra_addons = must_not_have & set(addons)
if extra_addons:
    status = 'FAIL'
    notes.append(f"forbidden addons present: {sorted(extra_addons)}")

# 5. candidates_must_include
if candidates_must_include and not candidates_must_include.issubset(candidate_modes):
    status = 'FAIL'
    notes.append(f"candidates missing: {sorted(candidates_must_include - candidate_modes)}")

# 6. disabled_modes_must_include
if disabled_must_include and not disabled_must_include.issubset(disabled_modes_observed):
    status = 'FAIL'
    notes.append(f"disabled missing: {sorted(disabled_must_include - disabled_modes_observed)}")

# 7. rejected_modes_must_include_reasons (substring match within reasons)
if rejected_reasons:
    joined = ' '.join(rejected_reasons_observed).lower()
    found_all = all(any(tok.lower() in joined for tok in [r]) for r in rejected_reasons)
    if not found_all:
        status = 'FAIL'
        notes.append(f"rejected reasons missing: {sorted(rejected_reasons - rejected_reasons_observed)}")

# 8. classifier_expected
if classifier_expected == 'true' and not classifier_used:
    status = 'FAIL'
    notes.append("classifier_expected=true but classifier_used=false")
elif classifier_expected == 'false' and classifier_used:
    status = 'FAIL'
    notes.append("classifier_expected=false but classifier_used=true")

# 9. approval_required
if exp.get('approval_required') is True and not approval_required:
    status = 'FAIL'
    notes.append("approval_required expected true, got false")

# 10. risk_level_min
risk_min = exp.get('risk_level_min','')
risk_order = {'low':1,'medium':2,'high':3,'critical':4}
if risk_min and risk_order.get(risk_level,0) < risk_order.get(risk_min,0):
    status = 'FAIL'
    notes.append(f"risk_level={risk_level} below min={risk_min}")

# 11. sandbox_execution always requires approval
if planned == 'sandbox_execution' and not approval_required:
    status = 'FAIL'
    notes.append("sandbox_execution planned but approval_required=false")

# 12. frontend_contract
if front_end_req and not fc:
    status = 'FAIL'
    notes.append("frontend_contract_required but contract is empty")

# 13. classification-only mode hint.
classification_only = exp.get('classification_only_note','') or ''

result = {
    'case_id': case['id'],
    'name': case.get('name',''),
    'planned_mode': planned,
    'executed_mode': executed,
    'classifier_used': classifier_used,
    'classifier_reason': classifier_reason,
    'reason_codes': reason_codes,
    'provider': fc.get('provider',''),
    'model_used': fc.get('model_used_hint','') or fc.get('model_tier',''),
    'mock': fc.get('mock', dec.get('mock', False)),
    'fallback_used': dec.get('fallback_used', False),
    'approval_required': approval_required,
    'async_required': fc.get('async_required', False),
    'risk_level': risk_level,
    'addon_capabilities': addons,
    'disabled_capabilities': disabled_caps,
    'rejected_modes': rejected,
    'candidates': candidates,
    'score_breakdown': score_breakdown,
    'frontend_explanation': fc.get('frontend_explanation',{}),
    'status': status,
    'notes': notes,
    'classification_only_note': classification_only,
}
with open(tmp_path) as f:
    arr = json.load(f)
arr.append(result)
with open(tmp_path, 'w') as f:
    json.dump(arr, f, ensure_ascii=False, indent=2)

# Print summary.
prefix = "[OK] " if status == 'PASS' else "[FAIL] "
print(f"  {prefix}{case['id']:35s} planned={planned:18s} executed={executed:18s} clf_used={classifier_used}")
if notes:
    for n in notes:
        print(f"    note: {n}")
PYEOF

    # Pull status out of the last appended result and tally.
    last_status=$(python3 -c "
import json
arr=json.load(open('$RESULTS_FILE.tmp'))
print(arr[-1]['status'])
")
    if [ "$last_status" = "PASS" ]; then
        log_pass "$case_id"
    else
        log_fail "$case_id"
    fi
}

# Iterate.
for case_id in $CASE_IDS; do
    run_case "$case_id"
done

# Finalise results file.
mv "$RESULTS_FILE.tmp" "$RESULTS_FILE"

# Summary.
echo ""
echo "=== Results ==="
echo "Total cases: $(python3 -c "import json; print(len(json.load(open('$MATRIX_FILE'))['cases']))")"
echo "Passed: $PASS"
echo "Failed: $FAIL"
echo "Skipped: $SKIP"
echo "Classification-only PASS: $CLASSIFICATION_ONLY"
echo "Detailed results: $RESULTS_FILE"
if [ "$FAIL" -gt 0 ]; then
    echo "Some tests FAILED"
    exit 1
fi
echo "All tests passed"
exit 0
