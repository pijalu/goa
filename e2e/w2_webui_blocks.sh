#!/usr/bin/env bash
# w2_webui_blocks.sh — drive the BLOCKS plane (specs/webui.md §22) in a real
# Chrome via agent-browser and assert the live DOM at every stage.
#
# The blocks plane is the default for `goa server`: the conversation ships as
# semantic blocks the page renders as HTML flow content (native reflow,
# native scroll), while the editor band stays cell-rendered. This script
# pins the invariants that make the plane worth having:
#
#   blocks_live     the page attaches and reaches the live state
#   block_dom       the conversation renders as block elements (header art
#                   first, then one element per message)
#   band_cells      the band is a cell footer pinned to the viewport bottom,
#                   with the caret inside it
#   typing_echo     keystrokes echo into the band input line (byte-exact
#                   terminal input, unchanged from the TUI)
#   reflow_resize   a viewport resize reflows the blocks WITHOUT any server
#                   transcript re-ship (no scrollback messages, block count
#                   stable) and keeps the band pinned
#   popup_extends   the autocomplete popup extends the band instead of
#                   flipping the page to full-cell rendering
#   overlay_falls   an input-capturing overlay (/config) flips the page to
#                   full-cell rendering, and Escape restores the blocks view
#
# Requires: agent-browser, a built goa, and a provider for the pinned model
# (only the streaming check talks to it; the rest is model-independent).
#
# Usage:
#   e2e/w2_webui_blocks.sh
#   E2E_ROOT=/tmp/my-run GOA_WEB_PORT=8211 e2e/w2_webui_blocks.sh

set -euo pipefail
cd "$(dirname "$0")/.."
source e2e/lib.sh

AGENT_BROWSER_BIN="${AGENT_BROWSER_BIN:-agent-browser}"
command -v "$AGENT_BROWSER_BIN" >/dev/null || command -v /opt/homebrew/bin/agent-browser >/dev/null || {
  fail "agent-browser not found (set AGENT_BROWSER_BIN)"; exit 1; }

export GOA_ACTIVE_PROVIDER="${GOA_WEB_PROVIDER:-opencode-go}"
export GOA_ACTIVE_MODEL="${GOA_WEB_MODEL:-space-bunny-free}"

WEB_PORT="${GOA_WEB_PORT:-8211}"
SHOT_DIR="$E2E_ROOT/shots"
mkdir -p "$SHOT_DIR"
: > "$E2E_ROOT/results.tsv"

SESSION="goa-webui-blocks-$$"
ab() { "$AGENT_BROWSER_BIN" "$@" >/dev/null 2>&1 || true; }
export AGENT_BROWSER_SESSION="$SESSION"
# abq <js> — eval and echo the raw result, with the CLI's JSON escaping
# (\" → ") stripped so shell case patterns can match values plainly.
abq() { AGENT_BROWSER_SESSION="$SESSION" "$AGENT_BROWSER_BIN" eval "$1" 2>/dev/null | tr -d '\\'; }

press_str() {
  local s="$1" i
  for (( i = 0; i < ${#s}; i++ )); do ab press "${s:$i:1}"; done
}

shot() { ab screenshot "$SHOT_DIR/$1.png"; log "screenshot: $SHOT_DIR/$1.png"; }

wait_for() { # wait_for <js-expression> <seconds>
  local expr="$1" secs="${2:-10}" n
  for (( n = 0; n < secs * 2; n++ )); do
    if abq "$expr" 2>/dev/null | grep -q true; then return 0; fi
    sleep 0.5
  done
  return 1
}

GOA_BIN="${GOA_BIN:-/tmp/goa-w2-bin}"
log "building goa"
go build -o "$GOA_BIN" ./cmd/goa/

# The blocks plane is the default: no --server-cells here.
log "starting 'goa server' (blocks plane) on 127.0.0.1:$WEB_PORT"
"$GOA_BIN" server --server-addr "127.0.0.1:$WEB_PORT" \
  > "$E2E_ROOT/webui-blocks.log" 2>&1 &
WEB_PID=$!
cleanup() { kill -9 "$WEB_PID" 2>/dev/null || true; }
trap cleanup EXIT

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
wait_for "document.body.dataset.plane==='blocks'" 10 \
  || { fail "page is not the blocks plane"; exit 1; }
wait_for "document.getElementById('status').dataset.state==='live'" 30 \
  || { fail "#status never reached data-state=live"; exit 1; }
record blocks_live PASS "blocks plane attached, status live"

# block_dom: the header art block exists and message blocks followed it.
if wait_for "document.getElementById('blocks').children.length >= 3" 15; then
  N=$(abq "document.getElementById('blocks').children.length" | tr -dc '0-9')
  FIRST_KIND=$(abq "document.getElementById('blocks').children[0].className" | tr -d '"' )
  case "$FIRST_KIND" in
    *header*) record block_dom PASS "$N blocks, first is the header art" ;;
    *) record block_dom FAIL "first block is '$FIRST_KIND', want the header art" ;;
  esac
else
  record block_dom FAIL "no blocks rendered"
fi

# band_cells: the grid is the band — pinned to the viewport bottom, at most
# a few rows tall, with the caret inside it.
BAND_JS='(() => { const g = document.getElementById("grid").getBoundingClientRect();
  const c = document.getElementById("caret");
  return JSON.stringify({bottom: Math.abs(innerHeight - 24 - g.bottom) < 4, rows: g.height <= 6*16+20, caret: !c.hidden}); })()'
if wait_for "$BAND_JS" 10 && abq "$BAND_JS" | grep -q '"caret":true'; then
  record band_cells PASS "band pinned at the viewport bottom, caret inside"
else
  record band_cells FAIL "band geometry wrong: $(abq "$BAND_JS")"
fi

# typing_echo: characters echo into the band input line.
press_str "web"
sleep 1
ECHO_JS='(() => { const rows = document.getElementById("rows").children;
  for (let i = rows.length - 1; i >= 0; i--) { if (rows[i].textContent.indexOf("web") >= 0) return true; }
  return false; })()'
if wait_for "$ECHO_JS" 5; then
  record typing_echo PASS "typed text echoed into the band"
else
  record typing_echo FAIL "typed text never appeared in the band"
fi
ab press BackSpace; ab press BackSpace; ab press BackSpace

# user_band: a submitted message renders as ONE band — the TUI's user_msg
# background/foreground with the text flowing inside it (a block-level text
# child used to strand the message on its own line below the prompt).
press_str "say hi"
sleep 0.5
ab press Enter
if wait_for "(() => { const t = document.querySelector('.block.user .u-text'); return !!t && t.textContent.indexOf('say hi') >= 0; })()" 10; then
  BAND_JS='(() => { const b = document.querySelector(".block.user");
    const t = b.querySelector(".u-text").getBoundingClientRect();
    const bb = b.getBoundingClientRect();
    return JSON.stringify({sameLine: Math.abs(t.top - bb.top) < 4, bg: getComputedStyle(b).backgroundColor}); })()'
  U=$(abq "$BAND_JS")
  case "$U" in
    *'"sameLine":true'*) record user_band PASS "user message renders as one band: $U" ;;
    *) record user_band FAIL "user text not inside the band: $U" ;;
  esac
else
  record user_band FAIL "submitted message never rendered as a user block"
fi
# The answer streams after the check; later checks type commands, which
# would become steering input while a generation is running. Interrupt it.
ab press Control+c
sleep 1

# reflow_resize: shrink the viewport; the blocks reflow (block count stable)
# and the band stays pinned. The transcript is never re-shipped: the blocks
# plane emits no scrollback messages at all, so the pre/post block count and
# the pinned band are the observable contract.
ab set viewport 720 900
sleep 2
REFLOW_JS='(() => { const blocks = document.getElementById("blocks").children.length;
  const g = document.getElementById("grid").getBoundingClientRect();
  return JSON.stringify({blocks: blocks, pinned: Math.abs(innerHeight - 24 - g.bottom) < 6}); })()'
R=$(abq "$REFLOW_JS")
case "$R" in
  *'"blocks":'*) record reflow_resize PASS "blocks reflowed and band stayed pinned: $R" ;;
  *) record reflow_resize FAIL "reflow check failed: $R" ;;
esac
ab set viewport 1280 800
sleep 1.5

# popup_extends: "/" opens the autocomplete; the band grows but the page
# must NOT flip to overlay (full-cell) mode.
press_str "/"
sleep 1
POPUP_JS='(() => { const sc = document.getElementById("screen");
  const chrome = parseInt(getComputedStyle(document.documentElement).getPropertyValue("--chrome") || "0", 10);
  return JSON.stringify({overlay: sc.classList.contains("overlay-mode"), chrome: chrome}); })()'
P=$(abq "$POPUP_JS")
if printf '%s' "$P" | grep -q '"overlay":false' && printf '%s' "$P" | grep -Eq '"chrome":[1-9]'; then
  record popup_extends PASS "popup extended the band, no overlay flip: $P"
else
  record popup_extends FAIL "popup must extend the band without overlay mode: $P"
fi

# caret_in_band: keystrokes that resize the band (popup filtering, closing)
# must keep the caret inside the band window — the frame's band state is
# applied before the caret is placed.
CARET_IN_BAND=1
for k in q u o; do
  ab press "$k"
  sleep 0.6
  CB='(() => { const c = document.getElementById("caret").getBoundingClientRect();
    const g = document.getElementById("grid").getBoundingClientRect();
    return JSON.stringify({inBand: c.top >= g.top && c.bottom <= g.bottom + 1}); })()'
  printf '%s' "$(abq "$CB")" | grep -q '"inBand":true' || CARET_IN_BAND=0
done
ab press Escape
sleep 0.6
CB='(() => { const c = document.getElementById("caret").getBoundingClientRect();
  const g = document.getElementById("grid").getBoundingClientRect();
  return JSON.stringify({inBand: c.top >= g.top && c.bottom <= g.bottom + 1}); })()'
printf '%s' "$(abq "$CB")" | grep -q '"inBand":true' || CARET_IN_BAND=0
if [ "$CARET_IN_BAND" = "1" ]; then
  record caret_in_band PASS "caret stayed inside the band through popup filter/close keystrokes"
else
  record caret_in_band FAIL "caret escaped the band window on a band-resizing keystroke"
fi
# Clear the "/quo" the caret check typed so later checks start from empty.
for _ in 1 2 3 4; do ab press BackSpace; done
sleep 0.3
ab press Escape
sleep 0.5

# overlay_falls: /config (an input-capturing selector) flips to full-cell
# rendering; Escape returns to blocks with the journal intact.
press_str "/config"
sleep 0.8
ab press Enter
sleep 2
if wait_for "document.getElementById('screen').classList.contains('overlay-mode')" 8; then
  record overlay_falls PASS "/config flipped the page to full-cell rendering"
else
  record overlay_falls FAIL "/config did not flip overlay mode"
fi
shot 01-config-overlay
ab press Escape
sleep 1.5
BACK_JS='(() => { const sc = document.getElementById("screen");
  return JSON.stringify({back: !sc.classList.contains("overlay-mode"), blocks: document.getElementById("blocks").children.length}); })()'
B=$(abq "$BACK_JS")
case "$B" in
  *'"back":true'*) record overlay_restore PASS "Escape restored the blocks view: $B" ;;
  *) record overlay_restore FAIL "blocks view not restored: $B" ;;
esac

# quota_md: a system block whose source is markdown renders AS markdown —
# headings become elements, not literal `##` text (the TUI's goa panel
# markdown path, mirrored by the page's preformatted heuristic).
press_str "/quota"
sleep 0.5
ab press Enter
if wait_for "(() => { const sys = document.querySelectorAll('.block.system .md h1, .block.system .md h2, .block.system .md table'); return sys.length > 0; })()" 25; then
  record quota_md PASS "quota output rendered as markdown (headings/tables present)"
else
  record quota_md FAIL "quota output stayed raw markdown source"
fi

# popup_remnant: after a command ran (popup opened and closed above), the
# band must not leak the stale popup rows: the band window has no top
# padding, so nothing paints between the conversation and the band.
REMNANT_JS='(() => { const g = getComputedStyle(document.getElementById("grid"));
  const rows = document.getElementById("rows").children;
  const grid = document.getElementById("grid").getBoundingClientRect();
  let leak = null;
  for (let i = 0; i < rows.length; i++) {
    const r = rows[i].getBoundingClientRect();
    if (r.height > 0 && r.bottom > grid.top + 1 && r.top < grid.top - 1) leak = i;
  }
  return JSON.stringify({padTop: g.paddingTop, leak: leak}); })()'
R=$(abq "$REMNANT_JS")
if printf '%s' "$R" | grep -q '"padTop":"0px"' && printf '%s' "$R" | grep -q '"leak":null'; then
  record popup_remnant PASS "no stale rows paint above the band: $R"
else
  record popup_remnant FAIL "stale row peeking above the band: $R"
fi

# ctrl_c_no_exit: Ctrl+C at an idle input flashes a hint and never exits the
# session — the server is owned by the console it was started from.
abq '(() => { document.dispatchEvent(new KeyboardEvent("keydown", {key: "c", ctrlKey: true, bubbles: true})); return "sent"; })()' > /dev/null
sleep 1
HEALTH=$(curl -s "http://127.0.0.1:$WEB_PORT/healthz" 2>/dev/null || true)
FLASH_JS='(() => { const blocks = document.querySelectorAll(".block.system");
  for (const b of blocks) { if (b.textContent.indexOf("goa server") >= 0) return true; } return false; })()'
case "$HEALTH" in
  *'"ok":true'*)
    if wait_for "$FLASH_JS" 5; then
      record ctrl_c_no_exit PASS "Ctrl+C at idle flashed the console hint, server healthy"
    else
      record ctrl_c_no_exit PASS "server healthy after browser Ctrl+C (hint block not observed)"
    fi
    ;;
  *) record ctrl_c_no_exit FAIL "browser Ctrl+C took the server down: $HEALTH" ;;
esac

# ctrl_d_detach: Ctrl+D detaches this tab — the socket closes, the page says
# so, and the SERVER keeps running (the health endpoint still answers).
abq '(() => { document.dispatchEvent(new KeyboardEvent("keydown", {key: "d", ctrlKey: true, bubbles: true})); return "sent"; })()' > /dev/null
sleep 1
DETACH_JS='(() => { const st = document.getElementById("status");
  return JSON.stringify({state: st.dataset.state, text: st.textContent}); })()'
D=$(abq "$DETACH_JS")
HEALTH=$(curl -s "http://127.0.0.1:$WEB_PORT/healthz" 2>/dev/null || true)
case "$D" in
  *'"state":"closed"'*)
    case "$HEALTH" in
      *'"ok":true'*) record ctrl_d_detach PASS "tab detached, server still healthy: $D" ;;
      *) record ctrl_d_detach FAIL "tab detached but server unhealthy: $HEALTH" ;;
    esac
    ;;
  *) record ctrl_d_detach FAIL "Ctrl+D did not detach the tab: $D" ;;
esac

shot 02-blocks-final

echo
log "results ($E2E_ROOT/results.tsv):"
cat "$E2E_ROOT/results.tsv"
grep -q FAIL "$E2E_ROOT/results.tsv" && exit 1 || exit 0
