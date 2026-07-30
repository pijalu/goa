#!/usr/bin/env bash
# T4 — Orchestration + goals + companion (all combined).
# Path: ptydrive TUI — /orchestrate:new binds a goal (TUI-only wiring) and
# companion state is seeded before launch. Validates run evidence (roles,
# models, conversation), the orchestrator-bound goal lifecycle, and companion
# coexistence.
source "$(dirname "$0")/lib.sh"

T=T4
PROJ="$E2E_ROOT/proj-t4"
RAW="$E2E_ROOT/t4-tui.raw"

log "T4 setup: $PROJ"
mk_fake_project "$PROJ"
write_base_config "$PROJ" "$QWEN"
enable_orchestrator_roles "$PROJ"
enable_companion "$PROJ"
seed_agent_driven_companion "$PROJ"

OBJECTIVE="Create file orbit.txt in the project root containing exactly the word ORBIT. The coder writes it with the write tool; the reviewer confirms. Keep all messages very short."

log "T4 run: TUI /orchestrate:new (ptydrive, timeout 25m)"
"$PTYDRIVE" --bin "$GOA_BIN" --dir "$PROJ" --log "$RAW" \
  --send "/orchestrate:new:topology=hub,objective=$OBJECTIVE" --send-delay 12s \
  --wait-file "$PROJ/.goa/orchestrator/*/events.jsonl" --wait-pattern '"run_finished"' \
  --timeout 25m || true

EV=$(ls -t "$PROJ"/.goa/orchestrator/*/events.jsonl 2>/dev/null | head -1 || true)
fails=0

if [ -z "$EV" ]; then
  fail "T4.1 no orchestration events.jsonl produced"
  record "$T" FAIL "no run produced — see $RAW"
  exit 1
fi
pass "T4.1 events.jsonl exists: $EV"

orch_qwen=$(jq -c 'select(.type=="agent_started" and .role=="orchestrator" and .model=="qwen/qwen3.5-9b")' "$EV" | wc -l | tr -d ' ')
[ "$orch_qwen" -ge 1 ] && pass "T4.2 orchestrator runs on qwen" || { fail "T4.2 orchestrator not on qwen (count=$orch_qwen)"; fails=$((fails+1)); }

mismatches=$(jq -c 'select(.type=="agent_started") | select((.role=="orchestrator" and .model!="qwen/qwen3.5-9b") or (.role=="reviewer" and .model!="qwythos-9b-v2") or (.role=="coder" and .model!="google/gemma-4-e4b"))' "$EV" | wc -l | tr -d ' ')
[ "$mismatches" -eq 0 ] && pass "T4.3 role→model mapping correct" || { fail "T4.3 role→model mismatches: $mismatches"; fails=$((fails+1)); }

speakers=$(jq -r 'select(.type=="agent_message") | .agent_id' "$EV" | sort -u | wc -l | tr -d ' ')
[ "$speakers" -ge 2 ] && pass "T4.4 conversation: $speakers distinct agents" || { fail "T4.4 only $speakers distinct agents spoke"; fails=$((fails+1)); }

jq -e 'select(.type=="run_finished")' "$EV" >/dev/null 2>&1 \
  && pass "T4.5 run_finished emitted" || { fail "T4.5 run_finished missing"; fails=$((fails+1)); }

# goal binding: TUI /orchestrate:new wires a GoalBinder -> goal_id in
# run_started + goal lifecycle in .goa/goals/goal-events.jsonl
goal_id=$(jq -r 'select(.type=="run_started") | .payload.goal_id // empty' "$EV" | head -1)
GEV="$PROJ/.goa/goals/goal-events.jsonl"
if [ -n "$goal_id" ]; then
  pass "T4.6 run bound to goal $goal_id"
  if [ -f "$GEV" ] && grep -q "$goal_id" "$GEV"; then
    pass "T4.7 bound goal lifecycle present in goal-events.jsonl"
    grep -E '"goal\.(complete|clear|block)"' "$GEV" >/dev/null \
      && pass "T4.8 bound goal reached terminal event" \
      || { fail "T4.8 bound goal has no terminal event"; fails=$((fails+1)); }
  else
    fail "T4.7 bound goal $goal_id not found in goal-events.jsonl"; fails=$((fails+1))
  fi
else
  fail "T4.6 run_started has no goal_id (TUI path should bind a goal)"; fails=$((fails+1))
fi

if [ -f "$PROJ/orbit.txt" ] && grep -qi ORBIT "$PROJ/orbit.txt"; then
  pass "T4.9 artifact orbit.txt contains ORBIT"
else
  fail "T4.9 artifact orbit.txt missing/wrong"; fails=$((fails+1))
fi

# companion coexistence: seeded companion must not break the run; any
# companion activity is recorded as an observation.
if jq -e '.companion_history | length > 0' "$PROJ/.goa/state.json" >/dev/null 2>&1; then
  note "T4.10 companion history non-empty during orchestration (see bugs.md observation)"
else
  note "T4.10 companion idle during orchestration run (expected: companion hooks main-agent turns, not orchestration agents)"
fi

if [ "$fails" -eq 0 ]; then record "$T" PASS "orchestration+goal+companion coexistence verified"; else record "$T" FAIL "$fails assertion(s) failed — see $RAW and $EV"; fi
exit "$fails"
