<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# B6 — Terminal copy/paste does not support images

Closed 2026-10-05. Archived from `bugs.md`.

## Reported

Pasting an image into the terminal inserted nothing, so there was no path from
the terminal clipboard to an attachment. The web page had that path
(`app.js` `paste` → `/upload` → image path inserted as input); the terminal did
not.

Expected: the terminal brings an image in the same way the web page does, and the
resulting attachment is inserted as a path in the input line — image paste is a
capability of the session, not of one front end.

## Root cause (measured, not inferred)

No terminal emulator forwards image bytes. A terminal's own paste chord (Cmd+V)
can only deliver the clipboard's *text* flavours: a file copied in
Finder/Explorer is exactly that (its path arrives as pasted text and the submit
path attaches it), but a raw image — a screenshot — has no text form, so nothing
reaches the TUI and "inserts nothing" is literally what happens.

That is the one case the paste *key* covers: the image can only be had by reading
the OS clipboard from inside goa, which is what the explicit paste key does.
The chain already existed:

```
KbPaste            tui/keybindings.go  (Ctrl+V, ctrl+shift+v, raw 0x16 via tui/keys.go)
  → Editor.pasteFromClipboard  tui/editor.go
  → file paths → image → text  (the precedence a file-manager copy needs)
  → internal/clipboard_image.go
       runClipboardCommand: osascript/pbpaste (macOS), wl-paste/xclip (Linux),
       PowerShell (Windows, WSL)
```

What was missing was any *pin* on it: no test covered clipboard bytes → store →
path → input line, no test covered a platform with no clipboard reader, and no
real-terminal check existed. That is how "there is no path from the terminal
clipboard to an attachment" could be reported against a binary that had one —
and why the trigger also needed documenting in `docs/HOTKEYS.md`, next to the
text chords.

## Fix (structure, not a rewrite)

Nothing in the paste chain needed changing; the gap was coverage and
discoverability, and both were closed:

* **One platform seam.** `internal/clipboard_image.go` owns every OS tool call
  and is swappable at one point (`runClipboardCommand`). The editor never calls
  an OS tool — it holds three injectable readers (`readClipboardImage`,
  `readClipboardFilePaths`, `readClipboardText`, defaulting to the `internal`
  functions in `tui/editor.go:NewEditor`) plus one injectable store
  (`saveClipboardImage` = `internal.SaveClipboardImage`). The editor only ever
  learns "insert this path".
* **Shared code with the web path, not a copy.** `internal.SaveClipboardImage`
  writes through `internal.NewImageFile` — the same durable image store
  `internal/webui/upload.go` stores uploads in — and `internal.IsImageFile` is
  the same predicate the submit path (`internal/app/submithandler_input.go`) uses
  to turn a token into an attachment. Web/image parity is therefore structural.
* **Discoverability.** `docs/HOTKEYS.md` documents `Ctrl+V` as "files copied in a
  file manager, then an image, then text", and says why the terminal's own paste
  chord cannot do it.

## Tests

* `internal/clipboard_paste_chain_test.go` (new) runs `fakeImageClipboard`: a fake
  runner on *every* OS answers each backend's command shape, so clipboard bytes →
  `ReadClipboardImage` decode → `SaveClipboardImage` (store redirected to
  `t.TempDir()`) → stored path → `IsImageFile` runs with no real clipboard and no
  OS tool. Plus both failure paths: nothing on the clipboard → `(nil,nil)`,
  silently; bytes that are not an image → a decode error.
* `tui/editor_image_paste_chain_test.go` (new) drives the real `Ctrl+V` binding
  through the *real* store to the input line
  (`TestEditor_PasteFromClipboard_StoresImageAndInsertsStoredPath`, which also
  asserts the store received exactly the pasted image) and pins both failure
  paths — no image on the clipboard, and *no reader at all* on this platform
  (`nil` readers: quiet no-op, nothing written to the store).
* `internal/app/pasted_image_test.go` (new) pins the app-level tail:
  `OnImagePaste` → `handlePastedImage` inserts the stored path into the input
  line, separated from the text the cursor sits in.

## Validation — real terminal, real clipboard

`e2e/clipimg.sh` (new) puts a known 8×8 PNG on the OS clipboard, boots a real
`goa` in a PTY (config pinned to `e2e/mockllm`, so no model traffic) and presses
`Ctrl+V`; it asserts the *rendered input line* shows
`…/goa/images/goa-image-<n>.png` **and** that the stored file is that image
(IHDR re-read from the stored PNG):

```
[PASS] Ctrl+V pasted the clipboard image: input line =
  /Users/…/Library/Caches/goa/images/goa-image-4102929851.png (8x8, file on disk)
```

`e2e/ptydrive` gained a `--wait-output <regex>` condition for this (assert on
what the TUI rendered, not on a polled file), and `e2e/README.md` documents the
check. Live-process note: the TUI was shut down by the driver (`gracefulStop`).

Re-run on 2026-10-05 (this closure session, same script, unchanged code):

```
[PASS] Ctrl+V pasted the clipboard image: input line =
  /Users/muaddib/Library/Caches/goa/images/goa-image-1201303083.png (8x8, file on disk)
```

`ps` showed no surviving `goa`/`ptydrive`/mock-LLM process afterwards.

### The reported case (image copied in a browser)

The synthetic PNG proves the chain; the report is about an image copied *in a
browser*, so that was driven too. A page whose image is **40×24** (deliberately
unlike the script's 8×8) writes the real OS clipboard through the browser's own
clipboard API from a real click:

```
AGENT_BROWSER_SESSION=clipimg-headed agent-browser open --headed \
  file:///tmp/clipval/browser/copy-image.html      # page: <img> + copy button
AGENT_BROWSER_SESSION=clipimg-headed agent-browser click '#copy'
  → eval state = "copied:97"
osascript -e 'clipboard info'
  → BEFORE: «class PNGf», 74  …  AFTER: «class PNGf», 217
  and the readback PNG is 40×24 (the browser re-encoded the 97-byte source)
CLIP_KEEP=1 e2e/clipimg.sh
  → [PASS] Ctrl+V pasted the clipboard image: input line =
    /Users/…/Caches/goa/images/goa-image-20449706.png (40x24, file on disk)
```

The `40x24` on both sides is the point: the stored attachment **is** the
browser-copied image, not a leftover file. `e2e/clipimg.sh` takes `CLIP_KEEP=1`
for exactly this: validate a clipboard someone else set (it then asserts path +
existence, since it does not know the source dimensions).

**Harness finding (measured here).** `agent-browser` launches Chrome with
`--headless=new`, and in that mode `navigator.clipboard.write` **resolves and
changes nothing on the OS pasteboard** — a click reported `copied:97` while
`clipboard info` still showed the previous 74-byte PNG. So a *browser→OS
clipboard* check must run `--headed` (or read back through the page, as
`e2e/webclip` does); a headless run of this validation would look green and prove
nothing.

## Regression evidence (re-measured 2026-10-05)

With `tryPasteImage` short-circuited to "no image" (`return "", false` as its
first statement):

```
--- FAIL: TestEditor_PasteFromClipboard_StoresImageAndInsertsStoredPath
    editor_image_paste_chain_test.go:74: Ctrl+V on a clipboard image inserted nothing
--- FAIL: TestEditor_PasteFromClipboard_InsertsImageReference
    editor_image_paste_test.go:35: text = "", want /tmp/test-paste.png
--- FAIL: TestEditor_PasteFromClipboard_CallsOnImagePaste
    editor_image_paste_test.go:55: OnImagePaste path = "", want /tmp/test-paste.png
FAIL	github.com/pijalu/goa/tui
```

and, with the same short-circuit, the script itself reports

```
ptydrive: timeout waiting for condition
[FAIL] paste key produced no image path in the input line (ptydrive rc=1)
```

— its screen dump shows an empty input line, which is the reported symptom.
Restored: all three tests pass and the script reports `[PASS]` (see
"Validation" above).

## Residual risk

* The chain is validated against the clipboard tools of the *host* OS (macOS
  here). Linux/Windows coverage was, at the time this closed, only the
  fake-runner tests above — which turned out **not** to execute those platforms'
  backends at all, because backend selection keyed on `runtime.GOOS`. That gap is
  what B12 closed (`internal/clipboard_dispatch_test.go` drives every platform
  through an injected platform seam); see
  [`b12-clipboard-backend-contract.2026-10-05.md`](b12-clipboard-backend-contract.2026-10-05.md).
* The paste stored a re-encoded PNG, so a clipboard offering only WebP pasted
  nothing; image bytes are now stored verbatim (B14,
  [`b14-clipboard-image-format-parity.2026-10-05.md`](b14-clipboard-image-format-parity.2026-10-05.md)).
* A wayland/X11 session without `wl-paste`/`xclip` still pastes nothing but text;
  `readClipboardText` falls back to the terminal's own paste when the key
  delivers text, so only the image case degrades — silently, by design (a paste
  must not push errors into the input line).
