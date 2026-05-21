#!/bin/bash
set -euo pipefail

# Phase 4 Slice 10.0: DAG Visualization Test
# Verifies DAG node status tracking in Redis

GATEWAY_URL="${GATEWAY_URL:-http://127.0.0.1:8080}"
REDIS_CONTAINER="${REDIS_CONTAINER:-deploy-redis-1}"

log() { echo "[$(date +'%H:%M:%S')] $*"; }
fail() { echo "[FAIL] $*" >&2; exit 1; }
pass() { echo "  [PASS] $*"; }

wait_task() {
  local task_id=$1
  for i in $(seq 1 90); do
    local status=$(curl -s "$GATEWAY_URL/api/v1/tasks/$task_id" | jq -r '.status')
    if [[ "$status" == "completed" ]] || [[ "$status" == "failed" ]] || [[ "$status" == "budget_exceeded" ]]; then
      echo "$status"
      return 0
    fi
    sleep 1
  done
  fail "Task $task_id timeout"
}

# Test: Create DAG task and verify node status in Redis
test_dag_visual() {
  log "Testing DAG Visualization..."

  # Create a DAG task
  local task_id=$(curl -s -X POST "$GATEWAY_URL/api/v1/tasks" \
    -H "Content-Type: application/json" \
    -d '{"query":"Test DAG visualization status tracking","config":{"mode":"dag","max_total_tokens":16000}}' \
    | jq -r '.task_id')

  log "Task created: $task_id"

  # Wait for task to complete
  local status=$(wait_task $task_id)
  log "Task status: $status"

  # Get workflow_id from task response
  local workflow_id=$(curl -s "$GATEWAY_URL/api/v1/tasks/$task_id" | jq -r '.workflow_id')

  # Check DAG meta exists
  local meta_key="dag:$workflow_id:meta"
  log "Checking meta key: $meta_key"
  local meta_exists=$(docker exec $REDIS_CONTAINER redis-cli EXISTS "$meta_key" 2>/dev/null | tr -d '\r\n')
  [[ "$meta_exists" == "1" ]] || fail "DAG meta key not found in Redis"

  # Check DAG nodes exists
  local nodes_key="dag:$workflow_id:nodes"
  log "Checking nodes key: $nodes_key"
  local nodes_exists=$(docker exec $REDIS_CONTAINER redis-cli EXISTS "$nodes_key" 2>/dev/null | tr -d '\r\n')
  [[ "$nodes_exists" == "1" ]] || fail "DAG nodes key not found in Redis"

  # Check node status fields
  local node_status=$(docker exec $REDIS_CONTAINER redis-cli HGETALL "$nodes_key" 2>/dev/null)
  [[ -n "$node_status" ]] || fail "No node status data in Redis"

  # Verify status values (pending/running/completed/failed)
  echo "$node_status" | grep -qE "pending|running|completed|failed" || fail "Node status not in expected states"

  # Check meta counters
  local total_nodes=$(docker exec $REDIS_CONTAINER redis-cli HGET "$meta_key" "total_nodes" 2>/dev/null | tr -d '\r\n')
  local completed_nodes=$(docker exec $REDIS_CONTAINER redis-cli HGET "$meta_key" "completed_nodes" 2>/dev/null | tr -d '\r\n')

  log "Meta: total_nodes=$total_nodes, completed_nodes=$completed_nodes"

  # At least one node should be tracked
  [[ "$total_nodes" =~ ^[0-9]+$ ]] || fail "total_nodes not a number"
  [[ "$total_nodes" -gt 0 ]] || fail "No nodes tracked"

  # ===== Phase 4.1: Verify dependencies field =====
  log "Verifying dependencies field in nodes..."

  # Check draft_answer node has dependencies
  local draft_answer_json=$(docker exec $REDIS_CONTAINER redis-cli HGET "$nodes_key" "draft_answer" 2>/dev/null | tr -d '\r\n')
  [[ -n "$draft_answer_json" ]] || fail "draft_answer node not found in Redis"

  log "draft_answer JSON: $draft_answer_json"

  # Verify dependencies field exists
  echo "$draft_answer_json" | grep -q "dependencies" || fail "draft_answer missing 'dependencies' field"

  # Verify draft_answer depends on analyze_input
  echo "$draft_answer_json" | grep -q "analyze_input" || fail "draft_answer.dependencies does not contain analyze_input"

  # Check analyze_input node
  local analyze_input_json=$(docker exec $REDIS_CONTAINER redis-cli HGET "$nodes_key" "analyze_input" 2>/dev/null | tr -d '\r\n')
  [[ -n "$analyze_input_json" ]] || fail "analyze_input node not found in Redis"

  log "analyze_input JSON: $analyze_input_json"

  # Verify analyze_input has dependencies field (empty array is ok)
  echo "$analyze_input_json" | grep -q "dependencies" || fail "analyze_input missing 'dependencies' field"

  pass "DAG dependencies verification passed"
  pass "DAG Visualization test passed"
  log "  task_id=$task_id"
  log "  workflow_id=$workflow_id"
  log "  total_nodes=$total_nodes"
  log "  completed_nodes=$completed_nodes"
}

# Run test
log "=== Phase 4 Slice 10.0: DAG Visualization Test ==="
test_dag_visual
echo ""
echo "=== DAG Visualization Tests PASSED ==="