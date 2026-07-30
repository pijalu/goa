#!/usr/bin/env bash
# T1 — Orchestration: qwen orchestrates, qwythos reviews, gemma codes (hub).
# Launch: seeded run + headless resume (`goa --orchestrate <run-id>`).
# Validation: events.jsonl roles/models/conversation + task artifact.
source "$(dirname "$0")/lib.sh"

T=T1
PROJ="$E2E_ROOT/proj-t1"
LOG="$E2E_ROOT/t1-headless.log"
RUN_ID="run-e2e-t1"

log "T1 setup: $PROJ"
mk_fake_project "$PROJ"
write_base_config "$PROJ" "$QWEN"
enable_orchestrator_roles "$PROJ"

OBJECTIVE="Two delegations are REQUIRED, in this order: (1) use the delegate tool with role=coder to create answer.txt in the project root containing exactly the word BLUE via the write tool; (2) then use the delegate tool with role=reviewer to verify answer.txt contains BLUE. Do not write the file yourself. Keep all messages very short."
seed_orch_run "$PROJ" "$RUN_ID" hub "$OBJECTIVE"

log "T1 run: goa --orchestrate $RUN_ID (timeout 20m)"
( cd "$PROJ" && "$GOA_BIN" --orchestrate "$RUN_ID" --yes --timeout 20m \
    --no-memory --no-plugins --plain ) > "$LOG" 2>&1 || true

EV="$PROJ/.goa/orchestrator/$RUN_ID/events.jsonl"
fails=0

assert_file_exists "T1.1 events.jsonl exists" "$EV" || fails=$((fails+1))

if [ -f "$EV" ]; then
  orch_qwen=$(jq -c 'select(.type=="agent_started" and .role=="orchestrator" and .model=="qwen/qwen3.5-9b")' "$EV" | wc -l | tr -d ' ')
  [ "$orch_qwen" -ge 1 ] && pass "T1.2 orchestrator agent runs on qwen" || { fail "T1.2 orchestrator agent runs on qwen (count=$orch_qwen)"; fails=$((fails+1)); }

  mismatches=$(jq -c 'select(.type=="agent_started") | select((.role=="orchestrator" and .model!="qwen/qwen3.5-9b") or (.role=="reviewer" and .model!="qwythos-9b-v2") or (.role=="coder" and .model!="google/gemma-4-e4b"))' "$EV" | wc -l | tr -d ' ')
  [ "$mismatches" -eq 0 ] && pass "T1.3 every agent_started role→model mapping correct" || { fail "T1.3 role→model mismatches: $mismatches"; jq -c 'select(.type=="agent_started")|{role,model}' "$EV" | sort -u; fails=$((fails+1)); }

  speakers=$(jq -r 'select(.type=="agent_message") | .agent_id' "$EV" | sort -u | wc -l | tr -d ' ')
  [ "$speakers" -ge 2 ] && pass "T1.4 conversation: $speakers distinct agents produced messages" || { fail "T1.4 conversation: only $speakers distinct agents spoke"; fails=$((fails+1)); }

  delegates=$(jq -c 'select(.type=="agent_tool_call" and .role=="orchestrator" and .payload.tool=="delegate" and .payload.is_delta==false)' "$EV" | wc -l | tr -d ' ')
  [ "$delegates" -ge 1 ] && pass "T1.5 orchestrator issued $delegates delegate call(s)" || { fail "T1.5 orchestrator issued no delegate calls"; fails=$((fails+1)); }

  rev_qwythos=$(jq -c 'select(.type=="agent_started" and .role=="reviewer" and .model=="qwythos-9b-v2")' "$EV" | wc -l | tr -d ' ')
  [ "$rev_qwythos" -ge 1 ] && pass "T1.9 reviewer agent ran on qwythos" || { fail "T1.9 reviewer agent never ran (orchestrator skipped review delegation)"; fails=$((fails+1)); }

  rundirs=$(find "$PROJ/.goa/orchestrator" -mindepth 1 -maxdepth 1 -type d | wc -l | tr -d ' ')
  [ "$rundirs" -eq 1 ] && pass "T1.10 resume continued the same run (no fork)" || { fail "T1.10 resume forked: $rundirs run dirs"; fails=$((fails+1)); }

  if jq -e 'select(.type=="run_finished")' "$EV" >/dev/null 2>&1; then
    pass "T1.6 run_finished emitted"
  else
    fail "T1.6 run_finished missing (timeout/crash?)"; fails=$((fails+1))
  fi

  goal_id=$(jq -r 'select(.type=="run_started") | .payload.goal_id // empty' "$EV" | head -1)
  if [ -n "$goal_id" ]; then
    pass "T1.7 run bound to goal $goal_id"
  else
    note "T1.7 headless run has NO bound goal (goal binder is wired in the TUI /orchestrate:new path only) — documented finding F1"
  fi
fi

if [ -f "$PROJ/answer.txt" ] && grep -qi BLUE "$PROJ/answer.txt"; then
  pass "T1.8 artifact answer.txt contains BLUE"
else
  fail "T1.8 artifact answer.txt missing or wrong content"; fails=$((fails+1))
fi

if [ "$fails" -eq 0 ]; then record "$T" PASS "orchestration roles/models/conversation verified"; else record "$T" FAIL "$fails assertion(s) failed — see $LOG and $EV"; fi
exit "$fails"
