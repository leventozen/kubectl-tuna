#!/usr/bin/env bash
# Capture the console report for the broken-readiness fixture into docs/.
# Used as the source of truth for the animated demo in docs/demo.svg.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
KDIAG_DEMO=1 go test ./internal/diag -run TestRenderDemo -v 2>&1 \
  | sed -n '/^Kind:/,/^--- PASS/p' \
  | sed '/^--- PASS/d' \
  > docs/demo-output.txt
echo "wrote docs/demo-output.txt ($(wc -l < docs/demo-output.txt) lines)"
