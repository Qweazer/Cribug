#!/bin/bash
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.." || exit 1

echo "Building gateway..."
go build -o gateway ./cmd/gateway

echo "Building worker..."
go build -o worker ./cmd/worker

echo "Build OK"
