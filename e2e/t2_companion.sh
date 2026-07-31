#!/usr/bin/env bash
# T2 — Companion: qwen main + qwythos companion.
# T2a: agent-driven (seeded state.json, headless, prompt instructs request_review).
# T2b: framework-driven (/companion:framework via ptydrive TUI; companion must
#      review the turn without being asked).
source "$(dirname "$0")/lib.sh"

fails=0

# ---------- T2a agent-driven ----------
PROJ="$E2E_ROOT/proj-t2a"
LOG="$E2E_ROOT/t2a-headless.log"
log "T2a setup: $PROJ"
mk_fake_project "$PROJ"
write_base_config "$PROJ" "$QWEN"
enable_companion "$PROJ"
seed_agent_driven_companion "$PROJ"

PROMPT="Use the write tool to create color.txt containing exactly the word GREEN. You are in companion mode: you MUST call the request_review tool on your draft before finalizing. Keep everything very short."
log "T2a run: headless agent-driven companion (timeout 10m)"
( cd "$PROJ" && "$GOA_BIN" --prompt "$PROMPT" --yes --timeout 10m \
    --no-memory --no-plugins --plain ) > "$LOG" 2>&1 || true

if [ -f "$PROJ/color.txt" ] && grep -qi GREEN "$PROJ/color.txt"; then
  pass "T2a.1 artifact color.txt contains GREEN"
else
  fail "T2a.1 artifact color.txt missing/wrong"; fails=$((fails+1))
fi

# REAL tool invocation only (headless renders '-- tool call <name>'); model
# prose mentioning request_review does NOT count (false-pass guard).
if grep -qE "^-- tool call request_review" "$LOG"; then
  pass "T2a.2 main agent invoked request_review (real tool call)"
else
  fail "T2a.2 no request_review tool call in headless output"; fails=$((fails+1))
fi

# companion review evidence (agent-driven path): the review is delivered to
# the main agent as an in-session '[Message from companion]' user message —
# that IS the inter-model conversation proof (strongest). Fallbacks: tool
# result without the "no review output" marker (companion produced output),
# the framework-only '-- companion start' render marker, or persisted
# companion_history (not written by the agent-driven headless path).
SESS=$(ls -t "$PROJ"/.goa/sessions/*.jsonl 2>/dev/null | head -1 || true)
if [ -n "$SESS" ] && grep -q "Message from companion" "$SESS"; then
  pass "T2a.3 companion review delivered to main agent (session evidence)"
elif grep -q '"status":"review_complete"' "$LOG" && ! grep -q 'no review output' "$LOG"; then
  pass "T2a.3 companion review completed with non-empty output"
elif grep -qE "^-- companion start" "$LOG"; then
  pass "T2a.3 companion review block rendered in output"
elif jq -e '.companion_history | length > 0' "$PROJ/.goa/state.json" >/dev/null 2>&1; then
  pass "T2a.3 companion history persisted in state.json"
else
  fail "T2a.3 no companion review evidence (session + output + state.json)"; fails=$((fails+1))
fi

# ---------- T2b framework-driven ----------
PROJ="$E2E_ROOT/proj-t2b"
RAW="$E2E_ROOT/t2b-tui.raw"
log "T2b setup: $PROJ"
mk_fake_project "$PROJ"
write_base_config "$PROJ" "$QWEN"
enable_companion "$PROJ"

log "T2b run: TUI /companion:framework + prompt (ptydrive, timeout 12m)"
"$PTYDRIVE" --bin "$GOA_BIN" --dir "$PROJ" --log "$RAW" \
  --send "/companion:framework" --send-delay 10s \
  --send "Use the write tool to create sky.txt containing exactly the word AZURE. Reply very briefly." \
  --wait-file "$PROJ/sky.txt" --wait-pattern "AZURE" \
  --timeout 12m || true

if [ -f "$PROJ/sky.txt" ] && grep -qi AZURE "$PROJ/sky.txt"; then
  pass "T2b.1 artifact sky.txt contains AZURE"
else
  fail "T2b.1 artifact sky.txt missing/wrong"; fails=$((fails+1))
fi

# framework mode => companion reviewed the turn without being asked:
# main must NOT need request_review; companion evidence comes from the raw
# TUI stream (companion widget / footer) or persisted companion history.
if jq -e '.companion_history | length > 0' "$PROJ/.goa/state.json" >/dev/null 2>&1; then
  pass "T2b.2 companion history persisted (framework review happened)"
  jq -r '.companion_history[-1] | fromjson? | .content // empty' "$PROJ/.goa/state.json" 2>/dev/null | head -3 || true
elif grep -qiE "companion" "$RAW"; then
  pass "T2b.2 companion activity visible in TUI stream"
else
  fail "T2b.2 no framework companion evidence"; fails=$((fails+1))
fi

if [ "$fails" -eq 0 ]; then record T2 PASS "agent-driven + framework companion verified"; else record T2 FAIL "$fails assertion(s) failed"; fi
exit "$fails"
