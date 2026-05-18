#!/bin/bash
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.." || exit 1

if [ -f .env ]; then
  set -a
  source .env
  set +a
fi

export DATABASE_URL="${DATABASE_URL:-postgres://admin:admin@localhost:5432/orchestrator?sslmode=disable}"
export REDIS_ADDR="${REDIS_ADDR:-127.0.0.1:6379}"
export REDIS_PASSWORD="${REDIS_PASSWORD:-}"
export REDIS_DB="${REDIS_DB:-0}"
export LLM_SERVICE_URL="${LLM_SERVICE_URL:-http://127.0.0.1:8000}"
export TEMPORAL_ADDRESS="${TEMPORAL_ADDRESS:-127.0.0.1:17233}"
export TEMPORAL_TASK_QUEUE="${TEMPORAL_TASK_QUEUE:-orchestrator-task-queue}"
export HTTP_ADDR="${HTTP_ADDR:-:8080}"

./gateway
