#!/bin/bash
# Common test helpers for DAG and regular tests
# All functions filter by task_id before counting events

GATEWAY_URL="${GATEWAY_URL:-http://127.0.0.1:8080}"
REDIS_HOST="${REDIS_HOST:-127.0.0.1}"
REDIS_PORT="${REDIS_PORT:-6379}"

# Helper to run redis-cli
redis_cmd() {
  if command -v redis-cli &>/dev/null; then
    redis-cli -h "$REDIS_HOST" -p "$REDIS_PORT" "$@"
  else
    docker.exe exec deploy-redis-1 redis-cli "$@"
  fi
}

# Helper to run psql
psql_cmd() {
  docker.exe exec deploy-postgres-1 psql -U admin -d orchestrator -t -c "$1"
}

# Wait for task to reach terminal state
wait_for_task_terminal() {
  local task_id=$1
  local timeout=${2:-60}
  for i in $(seq 1 "$timeout"); do
    local status=$(curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$task_id" | jq -r '.status')
    if [[ "$status" == "completed" ]] || [[ "$status" == "failed" ]] || [[ "$status" == "budget_exceeded" ]]; then
      echo "$status"
      return 0
    fi
    sleep 1
  done
  echo "TIMEOUT"
  return 1
}

# Get task result
get_task_result() {
  local task_id=$1
  curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$task_id" | jq -r '.result'
}

# Get task status
get_task_status() {
  local task_id=$1
  curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$task_id" | jq -r '.status'
}

# Get task error_type
get_task_error_type() {
  local task_id=$1
  curl --noproxy '*' -s "$GATEWAY_URL/api/v1/tasks/$task_id" | jq -r '.error_type // empty'
}

# Get all events for a task_id from Redis stream
get_task_events() {
  local task_id=$1
  redis_cmd XRANGE "task:$task_id:events" - +
}

# Count events of a specific type for a task_id
# Usage: count_task_event "$task_id" "WORKFLOW_STARTED"
count_task_event() {
  local task_id=$1
  local event_type=$2
  local events=$(get_task_events "$task_id")
  local count=$(echo "$events" | grep -E '"event_type":"'"$event_type"'"' | wc -l)
  echo "${count:-0}"
}

# Assert event count equals expected value
# Usage: assert_task_event_count "$task_id" "WORKFLOW_STARTED" 1
assert_task_event_count() {
  local task_id=$1
  local event_type=$2
  local expected=$3
  local count=$(count_task_event "$task_id" "$event_type")
  if [[ "$count" -ne "$expected" ]]; then
    echo "[FAIL] Expected $expected $event_type events for task $task_id, found $count"
    return 1
  fi
  return 0
}

# Assert event exists (count >= 1)
# Usage: assert_task_event_exists "$task_id" "DAG_PLANNED"
assert_task_event_exists() {
  local task_id=$1
  local event_type=$2
  local count=$(count_task_event "$task_id" "$event_type")
  if [[ "$count" -lt 1 ]]; then
    echo "[FAIL] Missing $event_type event for task $task_id"
    return 1
  fi
  return 0
}

# Assert event absent (count == 0)
# Usage: assert_task_event_absent "$task_id" "LLM_STARTED"
assert_task_event_absent() {
  local task_id=$1
  local event_type=$2
  local count=$(count_task_event "$task_id" "$event_type")
  if [[ "$count" -ne 0 ]]; then
    echo "[FAIL] Unexpected $event_type event found for task $task_id (count=$count)"
    return 1
  fi
  return 0
}

# Count total llm_calls for a task
count_llm_calls() {
  local task_id=$1
  psql_cmd "SELECT count(*) FROM llm_calls WHERE task_id='$task_id';" | tr -d ' '
}

# Count llm_calls for a task and specific node
count_llm_calls_by_node() {
  local task_id=$1
  local node_id=$2
  psql_cmd "SELECT count(*) FROM llm_calls WHERE task_id='$task_id' AND node_id='$node_id';" | tr -d ' '
}

# Check event ordering - returns 0 if ordering is correct
# Usage: check_event_ordering "$task_id" "TASK_CREATED" "WORKFLOW_STARTED"
check_event_ordering() {
  local task_id=$1
  local first_event=$2
  local second_event=$3
  local events=$(get_task_events "$task_id")

  local first_idx=$(echo "$events" | grep -n -E '"event_type":"'"$first_event"'"' | head -1 | cut -d: -f1)
  local second_idx=$(echo "$events" | grep -n -E '"event_type":"'"$second_event"'"' | head -1 | cut -d: -f1)

  if [[ -z "$first_idx" ]] || [[ -z "$second_idx" ]]; then
    echo "[WARN] Cannot check ordering: event(s) not found"
    return 1
  fi

  if [[ "$first_idx" -lt "$second_idx" ]]; then
    return 0
  else
    echo "[FAIL] Event ordering violated: $first_event (line $first_idx) should come before $second_event (line $second_idx)"
    return 1
  fi
}