#!/bin/bash
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.." || exit 1

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VENV_DIR="$SCRIPT_DIR/../python_llm_service/.venv"

if [ -d "$VENV_DIR" ]; then
  source "$VENV_DIR/bin/activate"
fi

cd python_llm_service

uvicorn app:app --host 0.0.0.0 --port 8000
