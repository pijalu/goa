#!/usr/bin/env bash
# clipimg.sh — B6 regression check: an image on the OS clipboard becomes a
# stored attachment path in the input line when the paste key is pressed in the
# real terminal TUI.
#
# Real clipboard, real PTY (ptydrive), no model traffic: the TUI resolves the
# paste itself, so a provider is needed only for goa to boot (the mock LLM
# supplies one). What is asserted:
#   1. the rendered input line shows a path inside the image store
#      (…/goa/images/goa-image-<n>.png),
#   2. that path names a real file whose pixels are the clipboard image
#      (dimensions re-read from the stored PNG's IHDR).
#
# This check OWNS the OS clipboard: it replaces whatever it held.
#
# Platforms: macOS (osascript), Linux (wl-copy on Wayland, else xclip).
# Anything else (or a platform without the tool) records SKIP.
#
# Usage: e2e/clipimg.sh
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
if [ -z "$TOOL" ]; then
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

log "clipboard tool: $TOOL"
case "$TOOL" in
  osascript) osascript -e "set the clipboard to (read (POSIX file \"$SOURCE\") as «class PNGf»)" ;;
  wl-copy)   wl-copy --type image/png < "$SOURCE" ;;
  xclip)     xclip -selection clipboard -t image/png -i "$SOURCE" ;;
esac

log "driving the TUI: Ctrl+V with a clipboard image"
rc=0
"$PTYDRIVE" --bin "$GOA_BIN" --dir "$WORK" --log "$DIR/raw.log" \
  --send-raw $'\x16' --send-delay 6s \
  --wait-output 'goa/images/goa-image-[0-9]+\.png' --timeout "$CLIP_WAIT" || rc=$?

# The input line's path, read back from the rendered screen.
PATH_IN_LINE="$(python3 - "$DIR/raw.log" <<'PY'
import re, sys
data = open(sys.argv[1], 'rb').read().decode('utf-8', 'replace')
data = re.sub(r'\x1b\[[0-9;?]*[a-zA-Z]|\x1b\][^\x07]*\x07', '', data)
m = re.search(r'/[^\s\x1b]*goa/images/goa-image-\d+\.png', data)
print(m.group(0) if m else '')
PY
)"

dims() { # PNG IHDR of $1 as "WxH"
  python3 -c 'import struct,sys; d=open(sys.argv[1],"rb").read(); print("%dx%d" % struct.unpack(">II", d[16:24]))' "$1" 2>/dev/null || echo "unreadable"
}

if [ "$rc" -ne 0 ]; then
  fail "paste key produced no image path in the input line (ptydrive rc=$rc)"
  record "$TEST_ID" FAIL "no image path in the input line"
  exit 1
fi
if [ -z "$PATH_IN_LINE" ] || [ ! -s "$PATH_IN_LINE" ]; then
  fail "input line showed $PATH_IN_LINE, which is not a stored image file"
  record "$TEST_ID" FAIL "inserted path is not a file: $PATH_IN_LINE"
  exit 1
fi

want_dims="$(dims "$SOURCE")"
got_dims="$(dims "$PATH_IN_LINE")"
if [ "$got_dims" != "$want_dims" ]; then
  fail "stored image is $got_dims, clipboard image was $want_dims"
  record "$TEST_ID" FAIL "stored pixels $got_dims != clipboard $want_dims"
  exit 1
fi

pass "Ctrl+V pasted the clipboard image: input line = $PATH_IN_LINE ($got_dims, file on disk)"
record "$TEST_ID" PASS "input line shows $PATH_IN_LINE ($got_dims)"
