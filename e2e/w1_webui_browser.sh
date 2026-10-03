#!/usr/bin/env bash
# w1_webui_browser.sh — G7: drive `goa server` in a real Chrome via agent-browser
# and assert the live DOM at every stage of a scripted agent flow.
#
# This is the browser twin of e2e/t*.sh: those drive the TUI over a PTY against
# LM Studio, this one drives the same engine through internal/webui in a real
# browser (open/type/press/screenshot/eval). It proves the web path renders,
# streams and executes tools — the PTY harness cannot see any of that.
#
# Requires: /opt/homebrew/bin/agent-browser (or $AGENT_BROWSER_BIN), a built
# goa, and a provider for the pinned model in the user's own ~/.goa/config.yaml
# (credentials are never written into the throwaway project).
#
# Model pinning is ENV ONLY (GOA_ACTIVE_PROVIDER / GOA_ACTIVE_MODEL): the
# config cascade makes env win over every file layer, so the script cannot
# silently fall back to whatever model the developer last used.
#
# Usage:
#   e2e/w1_webui_browser.sh
#   E2E_ROOT=/tmp/my-run GOA_WEB_PORT=8199 e2e/w1_webui_browser.sh
#
# Env overrides: GOA_BIN, E2E_ROOT, GOA_WEB_PORT, AGENT_BROWSER_BIN,
# GOA_WEB_PROVIDER, GOA_WEB_MODEL.

set -euo pipefail
cd "$(dirname "$0")/.."
source e2e/lib.sh

AGENT_BROWSER_BIN="${AGENT_BROWSER_BIN:-agent-browser}"
command -v "$AGENT_BROWSER_BIN" >/dev/null || command -v /opt/homebrew/bin/agent-browser >/dev/null || {
  fail "agent-browser not found (set AGENT_BROWSER_BIN)"; exit 1; }

# The pinned model. Nothing else may answer during this scenario.
export GOA_ACTIVE_PROVIDER="${GOA_WEB_PROVIDER:-opencode-go}"
export GOA_ACTIVE_MODEL="${GOA_WEB_MODEL:-space-bunny-free}"

WEB_PORT="${GOA_WEB_PORT:-8099}"
SHOT_DIR="$E2E_ROOT/shots"
mkdir -p "$SHOT_DIR"
: > "$E2E_ROOT/results.tsv"

# ab <args...> — one agent-browser call in our own session.
SESSION="goa-webui-e2e-$$"
ab() { "$AGENT_BROWSER_BIN" "$@" >/dev/null 2>&1 || true; }
export AGENT_BROWSER_SESSION="$SESSION"

# abq <js> — eval and echo the raw JSON result.
abq() { AGENT_BROWSER_SESSION="$SESSION" "$AGENT_BROWSER_BIN" eval "$1" 2>/dev/null; }

# press_str <text> — type text as real keystrokes. agent-browser's
# `keyboard type` goes through Input.insertText, which fires no keydown, and
# internal/webui/app.js deliberately reports *key descriptors* over the socket
# (the server owns the byte table). So every character goes through `press`.
press_str() {
  local s="$1" i
  for (( i = 0; i < ${#s}; i++ )); do ab press "${s:$i:1}"; done
}

shot() { ab screenshot "$SHOT_DIR/$1.png"; log "screenshot: $SHOT_DIR/$1.png"; }

# wait_for <js-condition> <seconds> — poll a JS predicate until truthy.
wait_for() {
  local js="$1" secs="${2:-60}" deadline
  deadline=$(( $(date +%s) + secs ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    case "$(abq "$js")" in *true*) return 0;; esac
    sleep 0.5
  done
  return 1
}

cleanup() {
  ab close >/dev/null 2>&1
  [ -n "${WEB_PID:-}" ] && kill "$WEB_PID" 2>/dev/null || true
  [ -n "${WEB_PID:-}" ] && wait "$WEB_PID" 2>/dev/null || true
}
trap cleanup EXIT

# ---------------------------------------------------------------- server

log "building goa"
go build -o "$GOA_BIN" ./cmd/goa/

log "starting 'goa server' on 127.0.0.1:$WEB_PORT pinned to $GOA_ACTIVE_PROVIDER/$GOA_ACTIVE_MODEL"
"$GOA_BIN" server --server-addr "127.0.0.1:$WEB_PORT" \
  > "$E2E_ROOT/webui.log" 2>&1 &
WEB_PID=$!

SESSION_URL=""
for _ in $(seq 1 60); do
  SESSION_URL="$(curl -s -o /dev/null -w '%{redirect_url}' "http://127.0.0.1:$WEB_PORT/" 2>/dev/null || true)"
  [ -n "$SESSION_URL" ] && break
  sleep 0.5
done
[ -n "$SESSION_URL" ] || { fail "server did not answer /"; exit 1; }
log "session url: $SESSION_URL"

# ---------------------------------------------------------------- page

ab open "$SESSION_URL"
ab click "#grid"
wait_for "document.getElementById('status').dataset.state==='live'" 30 \
  || { fail "#status never reached data-state=live"; exit 1; }
record page_live PASS "status live"

# The status bar is the model's own identity line: this is the pin assertion.
if wait_for "document.getElementById('grid').innerText.indexOf('($GOA_ACTIVE_PROVIDER) $GOA_ACTIVE_MODEL')>=0" 60; then
  record pinned_model PASS "status bar shows ($GOA_ACTIVE_PROVIDER) $GOA_ACTIVE_MODEL"
else
  record pinned_model FAIL "status bar never showed the pinned model"
fi
shot 01-boot

# ---------------------------------------------------------------- agent turn

press_str "Use the bash tool to run: echo G7-E2E-OK. Then reply with exactly the word DONE."
sleep 1
shot 02-typed
ab press "Enter"

if wait_for "document.getElementById('grid').innerText.indexOf('\$ echo G7-E2E-OK')>=0" 120; then
  record tool_call PASS "tool call rendered: \$ echo G7-E2E-OK"
else
  record tool_call FAIL "no tool call rendered"
fi
wait_for "document.getElementById('grid').innerText.indexOf('DONE')>=0" 120 \
  && record agent_reply PASS "model replied DONE" \
  || record agent_reply FAIL "no DONE reply"
shot 03-response

# Streaming: the grid must grow while the turn is still in flight, not jump
# from empty to complete. Poll from inside the page so the sampling is not
# racing the shell.
press_str "reply with the words alpha beta gamma delta epsilon zeta, one per line, no other text"
ab press "Enter"
STREAM_JSON="$(abq "new Promise(function(res){var g=document.getElementById('grid');
function cnt(){var t=g.innerText.split(String.fromCharCode(10)),n=0;
for(var i=0;i<t.length;i++){if(/^(alpha|beta|gamma|delta|epsilon|zeta)\$/.test(t[i].trim())){n++}}return n}
var seen=[],t0=Date.now();
var iv=setInterval(function(){var n=cnt();
if(seen.length===0||seen[seen.length-1]!==n){seen.push(n)}
var el=Date.now()-t0;
if((n>=6&&el>3000)||el>180000){clearInterval(iv);res(JSON.stringify(seen))}},60)})")"
# `seen` is already deduped by the guard above; a genuine stream shows more
# than one non-zero level (0 -> mid -> final), never a single jump to final.
DISTINCT="$(printf '%s' "$STREAM_JSON" | tr -cd ',' | wc -c | tr -d ' ')"
if [ "${DISTINCT:-0}" -ge 2 ]; then
  record streaming PASS "grid grew in $((DISTINCT + 1)) steps: $STREAM_JSON"
else
  record streaming FAIL "no incremental growth: $STREAM_JSON"
fi
shot 04-stream

# ---------------------------------------------------------------- commands

press_str "/help"; ab press "Enter"
wait_for "document.getElementById('scrollback').innerText.indexOf('/provider')>=0" 30 \
  && record command_help PASS "/help listed commands" \
  || record command_help FAIL "/help produced no command list"
shot 05-help

press_str "/config"; ab press "Enter"
wait_for "document.getElementById('grid').innerText.indexOf('search>')>=0" 30 \
  && record config_open PASS "/config menu opened" \
  || record config_open FAIL "/config menu did not open"

# Arrow navigation moves the › cursor.
BEFORE="$(abq "Array.from(document.querySelectorAll('#grid .row')).filter(function(r){return r.innerText.indexOf('›')===0}).length" || echo 0)"
ab press "ArrowDown"; sleep 1
CUR="$(abq "(Array.from(document.querySelectorAll('#grid .row')).map(function(r){return r.innerText.trim()}).filter(function(x){return x.charAt(0)==='›'})[0]||'')")"
[ -n "$CUR" ] && record config_nav PASS "selector row: $CUR" \
             || record config_nav FAIL "no selector row after ArrowDown"
shot 06-config

# Type filter narrows the list.
press_str "mo"; sleep 1
wait_for "document.getElementById('grid').innerText.indexOf('search> mo')>=0" 15 \
  && record config_filter PASS "filter 'mo' applied" \
  || record config_filter FAIL "type filter had no effect"

ab press "Escape"; sleep 2
wait_for "document.getElementById('grid').innerText.indexOf('search>')<0" 15 \
  && record config_close PASS "Escape closed the menu" \
  || record config_close FAIL "menu still open after Escape"
shot 07-after-escape

# The pin must survive every command above.
if wait_for "document.getElementById('grid').innerText.indexOf('($GOA_ACTIVE_PROVIDER) $GOA_ACTIVE_MODEL')>=0" 15; then
  record pin_held PASS "model still pinned after commands"
else
  record pin_held FAIL "model pin lost"
fi

echo
log "results ($E2E_ROOT/results.tsv):"
cat "$E2E_ROOT/results.tsv"
grep -q FAIL "$E2E_ROOT/results.tsv" && exit 1 || exit 0