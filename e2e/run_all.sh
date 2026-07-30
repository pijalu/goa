#!/usr/bin/env bash
# run_all.sh — build goa, warm up LM Studio models, run T1..T4, print summary.
#
# Usage:
#   e2e/run_all.sh            # full series (slow: local models, expect 1h+)
#   E2E_ROOT=/tmp/my-run e2e/run_all.sh
#
# Env overrides: GOA_BIN, LMS_URL, E2E_ROOT (see lib.sh).
source "$(dirname "$0")/lib.sh"

export E2E_ROOT="${E2E_ROOT:-/tmp/goa-e2e/run-$(date +%Y%m%d-%H%M%S)}"
mkdir -p "$E2E_ROOT"
: > "$E2E_ROOT/results.tsv"
ln -sfn "$E2E_ROOT" /tmp/goa-e2e/last

log "E2E root: $E2E_ROOT"

log "building goa + ptydrive"
( cd "$(dirname "$0")/.." && go build -o "$(dirname "$GOA_BIN")/goa" ./cmd/goa/ \
  && go build -o "$PTYDRIVE" ./e2e/ptydrive/ )

log "warming up LM Studio models (JIT load; first call per model is slow)"
warmup_model "$QWEN"
warmup_model "$QWYTHOS"
warmup_model "$GEMMA"

overall=0
for t in t1_orchestration t2_companion t3_goals_companion t4_all; do
  log "=== $t ==="
  if bash "$(dirname "$0")/$t.sh"; then
    log "$t done"
  else
    log "$t FAILED (rc=$?)"
    overall=1
  fi
done

echo
echo "=== RESULTS ($E2E_ROOT/results.tsv) ==="
column -t -s "$(printf '\t')" "$E2E_ROOT/results.tsv" || cat "$E2E_ROOT/results.tsv"
exit "$overall"
