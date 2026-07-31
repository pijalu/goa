#!/usr/bin/env bash
# T3 — Goals + companion: headless --goal with agent-driven companion seeded.
# Validates goal lifecycle events AND companion evidence AND the artifact.
source "$(dirname "$0")/lib.sh"

T=T3
PROJ="$E2E_ROOT/proj-t3"
LOG="$E2E_ROOT/t3-headless.log"

log "T3 setup: $PROJ"
mk_fake_project "$PROJ"
write_base_config "$PROJ" "$QWEN"
enable_companion "$PROJ"
seed_agent_driven_companion "$PROJ"

OBJECTIVE="Create the file done.txt in the project root containing exactly the word DONE using the write tool. Completion is verified when done.txt exists with that content. Call request_review on your result before completing the goal. Keep everything very short."

log "T3 run: goa --goal (timeout 12m)"
( cd "$PROJ" && "$GOA_BIN" --goal --prompt "$OBJECTIVE" --yes --timeout 12m \
    --no-memory --no-plugins --plain ) > "$LOG" 2>&1 || true
rc=$?
note "T3 exit code: $rc (0 expected when goal completes; artifacts decide)"

fails=0

if [ -f "$PROJ/done.txt" ] && grep -qi DONE "$PROJ/done.txt"; then
  pass "T3.1 artifact done.txt contains DONE"
else
  fail "T3.1 artifact done.txt missing/wrong"; fails=$((fails+1))
fi

GEV="$PROJ/.goa/goals/goal-events.jsonl"
if [ -f "$GEV" ]; then
  pass "T3.2 goal events log exists"
  if grep -q '"goal.create"' "$GEV"; then
    pass "T3.3 goal created"
  else
    fail "T3.3 no goal.create event"; fails=$((fails+1))
  fi
  # Terminal state = goal.clear event, or a goal.update patch carrying a
  # terminal status (complete/blocked/paused). There is no goal.complete
  # event type (core/goal/model.go: only create/update/clear exist).
  if jq -e 'select(.type=="goal.clear")' "$GEV" >/dev/null 2>&1 \
    || jq -e 'select(.type=="goal.update" and (.status=="complete" or .status=="blocked" or .status=="paused"))' "$GEV" >/dev/null 2>&1; then
    term=$(jq -r 'select(.type=="goal.update") | .status // empty' "$GEV" | tail -1)
    [ -n "$term" ] || term="goal.clear"
    pass "T3.4 goal reached terminal event: $term"
  else
    fail "T3.4 goal never reached a terminal event"; fails=$((fails+1))
  fi
else
  fail "T3.2 goal events log missing ($GEV)"; fails=$((fails+1))
fi

# companion evidence: real request_review call, framework render marker, an
# in-session '[Message from companion]' delivery, or persisted history.
SESS=$(ls -t "$PROJ"/.goa/sessions/*.jsonl 2>/dev/null | head -1 || true)
if grep -qE "^-- tool call request_review" "$LOG" || grep -qE "^-- companion start" "$LOG" \
  || { [ -n "$SESS" ] && grep -q "Message from companion" "$SESS"; } \
  || jq -e '.companion_history | length > 0' "$PROJ/.goa/state.json" >/dev/null 2>&1; then
  pass "T3.5 companion engaged during goal run"
else
  fail "T3.5 no companion evidence during goal run"; fails=$((fails+1))
fi

if [ "$fails" -eq 0 ]; then record "$T" PASS "goal lifecycle + companion verified"; else record "$T" FAIL "$fails assertion(s) failed — see $LOG"; fi
exit "$fails"
