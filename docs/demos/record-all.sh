#!/usr/bin/env bash
#
# Record all Goa demos: Workflows, Orchestrator, and Companion.
#
# Usage:
#   bash docs/demos/record-all.sh
#
# Results are written to docs/demos/*.cast, *.gif, *.html
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

echo "⟡ Recording all Goa demos..."
echo ""

echo "1/3 Workflows Demo..."
bash "$SCRIPT_DIR/record-workflows-demo.sh"
echo ""

echo "2/3 Orchestrator Demo..."
bash "$SCRIPT_DIR/record-orchestrator-demo.sh"
echo ""

echo "3/3 Companion Demo..."
bash "$SCRIPT_DIR/record-companion-demo.sh"
echo ""

echo "⟡ All demos recorded!"
echo "  Files in: $SCRIPT_DIR/"
ls -lh "$SCRIPT_DIR"/*.gif "$SCRIPT_DIR"/*.html 2>/dev/null
