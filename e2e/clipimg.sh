#!/usr/bin/env bash
# clipimg.sh — B6/B18 regression check: an image on the OS clipboard becomes a
# stored attachment path in the input line when a paste chord is pressed in the
# real terminal TUI.
#
# Real clipboard, real PTY (ptydrive), no model traffic: the TUI resolves the
# paste itself, so a provider is needed only for goa to boot (the mock LLM
# supplies one). Two chords are driven, because a terminal only pastes for you
# when its own chord can carry the clipboard's *text*:
#
#   Ctrl+V              0x16, the chord goa reads the OS clipboard for.
#   Cmd+V               ESC [ 118 ; 9 u, what a Kitty-protocol terminal forwards
#                       when its own Paste menu item is disabled because the
#                       clipboard holds no text (a screenshot, i.e. the case this
#                       whole path exists for). It used to decode to a bare "v"
#                       and type that into the input line (bugs.md B18).
#
# What is asserted for each chord:
#   1. the rendered input line shows a path inside the image store
#      (…/goa/images/goa-image-<n>.png),
#   2. that path names a real file whose pixels are the clipboard image
#      (dimensions re-read from the stored PNG's IHDR),
#   3. nothing else landed in the input line — a chord must never be typed.
#
# This check OWNS the OS clipboard: it replaces whatever it held.
#
# Platforms: macOS (osascript), Linux (wl-copy on Wayland, else xclip).
# Anything else (or a platform without the tool) records SKIP.
#
# Usage: e2e/clipimg.sh
#
# CLIP_KEEP=1 validates whatever is ALREADY on the clipboard instead of putting
# the synthetic PNG there — used to check a clipboard written by a real browser
# ("copy an image in a browser"), where the source dimensions are unknown to
# this script and only path + existence are asserted.
source "$(dirname "$0")/lib.sh"

E2E_ROOT="${E2E_ROOT:-/tmp/goa-e2e/last}"
DIR="$E2E_ROOT/clipimg"
WORK="$DIR/work"
CLIP_WAIT="${CLIP_WAIT:-45s}"
TEST_ID="B6-terminal-image-paste"

mkdir -p "$WORK"

# clipboard_tool prints the tool this platform can set the clipboard with, or "".
clipboard_tool() {
  case "$(uname -s)" in
    Darwin)
      command -v osascript >/dev/null 2>&1 && echo osascript
      ;;
    Linux)
      if [ -n "${WAYLAND_DISPLAY:-}" ] && command -v wl-copy >/dev/null 2>&1; then
        echo wl-copy
      elif command -v xclip >/dev/null 2>&1; then
        echo xclip
      fi
      ;;
  esac
}

TOOL="$(clipboard_tool)"
KEEP_CLIP="${CLIP_KEEP:-}"
if [ -z "$TOOL" ] && [ -z "$KEEP_CLIP" ]; then
  note "no clipboard tool on this platform — skipping"
  record "$TEST_ID" SKIP "no clipboard backend for $(uname -s)"
  exit 0
fi

log "building goa + ptydrive"
(cd "$(dirname "$0")/.." && go build -o "$GOA_BIN" ./cmd/goa/ && go build -o "$PTYDRIVE" ./e2e/ptydrive/)

# A hermetic project: the TUI must not pick up the developer's own provider or
# memory/telegram settings.
start_mock_llm "$DIR/mock-llm.log"
trap 'stop_mock_llm' EXIT
mkdir -p "$WORK/.goa"
cat > "$WORK/.goa/config.yaml" <<YAML
active_provider: lmstudio
active_model: mock
providers:
  - id: lmstudio
    name: Mock LLM
    endpoint: $MOCK_LLM_URL
    preferred: true
models:
  - id: mock
    name: mock
    provider: lmstudio
    model: mock
memory:
  enabled: false
telegram:
  enabled: false
YAML

# The clipboard payload: a known 8x8 image, so the stored copy can be checked
# as pixels, not merely as a path.
SOURCE="$DIR/source.png"
python3 - "$SOURCE" <<'PY'
import struct, sys, zlib

def chunk(tag, data):
    body = tag + data
    return struct.pack('>I', len(data)) + body + struct.pack('>I', zlib.crc32(body) & 0xffffffff)

side = 8
raw = b''.join(b'\x00' + bytes([200, 30, 30]) * side for _ in range(side))
png = (b'\x89PNG\r\n\x1a\n'
       + chunk(b'IHDR', struct.pack('>IIBBBBB', side, side, 8, 2, 0, 0, 0))
       + chunk(b'IDAT', zlib.compress(raw))
       + chunk(b'IEND', b''))
open(sys.argv[1], 'wb').write(png)
PY

log "clipboard tool: ${TOOL:-none (CLIP_KEEP: validating the clipboard as-is)}"
if [ -z "$KEEP_CLIP" ]; then
  case "$TOOL" in
    osascript) osascript -e "set the clipboard to (read (POSIX file \"$SOURCE\") as «class PNGf»)" ;;
    wl-copy)   wl-copy --type image/png < "$SOURCE" ;;
    xclip)     xclip -selection clipboard -t image/png -i "$SOURCE" ;;
  esac
else
  note "CLIP_KEEP: using the clipboard as it stands (no synthetic PNG written)"
fi

# strip_ansi renders the raw PTY log as text (SGR/CSI/OSC removed), so the
# assertions read the screen the user would see.
strip_ansi() {
  python3 - "$1" <<'PY'
import re, sys
data = open(sys.argv[1], 'rb').read().decode('utf-8', 'replace')
# CSI (params may carry <>?=! private markers), OSC, then any residual control
# bytes (the shutdown writes e.g. ESC [ < u and ESC [ ! p).
data = re.sub(r'\x1b\[[0-9;?<=>!]*[a-zA-Z]', '', data)
data = re.sub(r'\x1b\][^\x07]*\x07', '', data)
data = re.sub(r'[\x00-\x08\x0b-\x1f\x7f]', '', data)
sys.stdout.write(data)
PY
}

# input_line prints the tail of the rendered screen: the footer ends with the
# "[∞]" indicator and the editable input line follows it, so what is printed is
# the editor's buffer (and nothing else). Empty when the footer is not found —
# the caller then skips the "nothing else was typed" assertion rather than
# failing on a footer that changed shape.
input_line() {
  strip_ansi "$1" | python3 -c '
import sys
data = sys.stdin.read()
marker = "[∞]"
idx = data.rfind(marker)
if idx < 0:
    sys.exit(0)
sys.stdout.write(data[idx + len(marker):].strip(" \t\r\n"))
'
}

dims() { # PNG IHDR of $1 as "WxH"
  python3 -c 'import struct,sys; d=open(sys.argv[1],"rb").read(); print("%dx%d" % struct.unpack(">II", d[16:24]))' "$1" 2>/dev/null || echo "unreadable"
}

# drive_chord LABEL BYTES drives one paste chord through a real PTY and asserts
# the whole chain. Returns non-zero on failure (already reported).
drive_chord() {
  local label="$1" bytes="$2"
  local raw="$DIR/raw-$(printf '%s' "$label" | tr -c 'a-zA-Z0-9' '-').log"
  local rc=0

  log "driving the TUI: $label with a clipboard image"
  "$PTYDRIVE" --bin "$GOA_BIN" --dir "$WORK" --log "$raw" \
    --send-raw "$bytes" --send-delay 6s \
    --wait-output 'goa/images/goa-image-[0-9]+\.png' --timeout "$CLIP_WAIT" || rc=$?

  local path_in_line
  path_in_line="$(strip_ansi "$raw" | grep -oE '/[^[:space:]]*goa/images/goa-image-[0-9]+\.png' | head -1 || true)"

  if [ "$rc" -ne 0 ]; then
    fail "$label produced no image path in the input line (ptydrive rc=$rc)"
    strip_ansi "$raw" | tail -c 2000 >&2
    record "$TEST_ID/$label" FAIL "no image path in the input line"
    return 1
  fi
  if [ -z "$path_in_line" ] || [ ! -s "$path_in_line" ]; then
    fail "$label: input line showed $path_in_line, which is not a stored image file"
    record "$TEST_ID/$label" FAIL "inserted path is not a file: $path_in_line"
    return 1
  fi

  # The chord must have pasted and nothing more: whatever the buffer holds must
  # be the path itself (a decoded chord once left a stray "v" there).
  local line
  line="$(input_line "$raw")"
  if [ -n "$line" ] && [ "$line" != "$path_in_line" ]; then
    fail "$label: input line holds $(printf '%q' "$line"), want just the path"
    record "$TEST_ID/$label" FAIL "input line = $(printf '%q' "$line")"
    return 1
  fi

  if [ -n "$KEEP_CLIP" ]; then
    pass "$label pasted the clipboard image: input line = $path_in_line ($(dims "$path_in_line") on disk; source left to the caller)"
    record "$TEST_ID/$label" PASS "input line shows $path_in_line ($(dims "$path_in_line")), clipboard kept as-is"
    return 0
  fi
  if [ "$(dims "$path_in_line")" != "$(dims "$SOURCE")" ]; then
    fail "$label: stored image is $(dims "$path_in_line"), clipboard image was $(dims "$SOURCE")"
    record "$TEST_ID/$label" FAIL "stored pixels $(dims "$path_in_line") != clipboard $(dims "$SOURCE")"
    return 1
  fi
  pass "$label pasted the clipboard image: input line = $path_in_line ($(dims "$path_in_line"), file on disk)"
  record "$TEST_ID/$label" PASS "input line shows $path_in_line ($(dims "$path_in_line"))"
  return 0
}

# Ctrl+V first (the chord the docs lead with), then Cmd+V (the one a Kitty
# terminal forwards). Both are run even if the first fails, so one report shows
# the state of both.
CTRL_V_RC=0
CMD_V_RC=0
drive_chord "ctrl-v" $'\x16' || CTRL_V_RC=$?
drive_chord "cmd-v"  $'\x1b[118;9u' || CMD_V_RC=$?

if [ "$CTRL_V_RC" -ne 0 ] || [ "$CMD_V_RC" -ne 0 ]; then
  exit 1
fi
