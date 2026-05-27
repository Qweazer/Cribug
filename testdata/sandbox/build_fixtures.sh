#!/bin/bash
# Build WASI fixture .wasm files from local .go sources.
# Requires Go 1.21+ with wasip1 support.
set -euo pipefail
cd "$(dirname "$0")"

echo "=== Building WASI fixtures from .go sources ==="

for src in hello_stdout stderr_output exit_nonzero infinite_loop large_stdout memory_grow fs_access_attempt formula_compare; do
    echo "  Building $src.go -> $src.wasm..."
    GOOS=wasip1 GOARCH=wasm go build -o "$src.wasm" "$src.go"
done

echo "=== Done ==="
ls -la *.wasm
