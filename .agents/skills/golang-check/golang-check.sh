#!/bin/bash

# golang-check: run all Go static analysis and file-size checks
set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "$(go env GOMOD)")" 2>/dev/null && pwd || pwd)"
HAS_ERRORS=0
# Complexity and size checks intentionally cover every repository Go file,
# including files that have not changed in the current working tree.
GO_FILES=()
while IFS= read -r file; do
  [[ -f "$file" ]] && GO_FILES+=("$file")
done < <(find "$PROJECT_DIR" -type f -name '*.go' -not -path '*/.git/*' | sort)
if ((${#GO_FILES[@]} == 0)); then
  echo "No Go files found"
  exit 0
fi

echo "=== gocognit (cognitive complexity > 15) ==="
gocognit -over 15 "${GO_FILES[@]}" || { echo "FAIL: cognitive complexity exceeded"; HAS_ERRORS=1; }

echo ""
echo "=== gocyclo (cyclomatic complexity > 12) ==="
gocyclo -over 12 "${GO_FILES[@]}" || { echo "FAIL: cyclomatic complexity exceeded"; HAS_ERRORS=1; }

echo ""
echo "=== staticcheck ==="
staticcheck "$PROJECT_DIR/..." || { echo "FAIL: staticcheck issues found"; HAS_ERRORS=1; }

echo ""
echo "=== go file size (hard max 1000, soft target 500) ==="
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
"$SCRIPT_DIR/go-file-size-check.sh" "${GO_FILES[@]}" || { echo "FAIL: file size limits exceeded"; HAS_ERRORS=1; }

echo ""
if [ "$HAS_ERRORS" -eq 0 ]; then
  echo "✓ All checks passed"
else
  echo "✗ Some checks failed"
fi
exit $HAS_ERRORS