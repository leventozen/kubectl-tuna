#!/usr/bin/env bash
# Capture the console report for the broken-readiness fixture into docs/.
# Used as the source of truth for the animated demo in docs/demo.gif.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
TUNA_DEMO=1 go test ./internal/diag -run TestRenderDemo -v 2>&1 \
  | sed -n '/^Kind:/,/^--- PASS/p' \
  | sed '/^--- PASS/d' \
  | awk 'NF { for (i = 0; i < blanks; i++) print ""; blanks = 0; print; next } { blanks++ }' \
  > docs/demo-output.txt
echo "wrote docs/demo-output.txt ($(wc -l < docs/demo-output.txt) lines)"
