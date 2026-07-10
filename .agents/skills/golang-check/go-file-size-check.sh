#!/bin/bash

# go-file-size-check: enforce Go source file size limits
set -euo pipefail

ROOT="$(cd "$(dirname "$(go env GOMOD)")" 2>/dev/null && pwd || pwd)"
HARD_LIMIT=1000
SOFT_LIMIT=500
HARD_VIOLATIONS=0
SOFT_VIOLATIONS=0

while IFS= read -r -d '' file; do
    lines=$(wc -l < "$file")
    if [ "$lines" -gt "$HARD_LIMIT" ]; then
        echo "HARD LIMIT VIOLATION: $file has $lines lines (max $HARD_LIMIT)"
        HARD_VIOLATIONS=$((HARD_VIOLATIONS + 1))
    elif [ "$lines" -gt "$SOFT_LIMIT" ]; then
        echo "SOFT LIMIT VIOLATION: $file has $lines lines (target $SOFT_LIMIT)"
        SOFT_VIOLATIONS=$((SOFT_VIOLATIONS + 1))
    fi
done < <(find "$ROOT" -type f -name '*.go' -not -path '*/vendor/*' -not -path '*/.git/*' -print0)

if [ "$HARD_VIOLATIONS" -gt 0 ]; then
    echo "✗ $HARD_VIOLATIONS file(s) exceed the hard limit of $HARD_LIMIT lines"
    exit 1
fi

if [ "$SOFT_VIOLATIONS" -gt 0 ]; then
    echo "⚠ $SOFT_VIOLATIONS file(s) exceed the soft target of $SOFT_LIMIT lines"
fi

echo "✓ All Go files are within size limits"
exit 0
