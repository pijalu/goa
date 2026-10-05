# Bug and feature Tracking

## Guideline
1. Create a detailed fix plan for each bug - the plan must contain test approach and validation steps - execute the plan and validate the fix when all elements are in place.
2. Any issues found must be fixed and the fix plan must be updated accordingly.
3. Issues found during testing must be fixed and the fix plan must be updated accordingly.
4. Each bug should be moved to docs/archive when tested and closed as the associated plan.
5. Use interactive shell/filmstrip to validate the output of the tool - you must verify the actual terminal output.
6. Check code quality with each tool run separately (do not chain them with `;` or `&&`):
- `go vet ./...`
- `staticcheck ./...`
- `gocognit -over 15 .`
- `gocyclo -over 12 .`
- `go test -count=1 -race -cover ./...`
Fix any issues.
! For cognitive and cyclomatic complexity, Pre-existing warnings are acceptable only if they are unrelated to the change and explicitly noted !

At the end of the session - the bug list should be empty, change committed and this file should only contain the guidelines for bug reporting.
If new items are added, restart the process.

Use goals to execute the fix plan - focus on micro tasks goals with new contextto lower context usage - use todos for micro tasks that should share context

Commit at the end of each fix with a clear and descriptive commit message

## Report format
Describe the bug or feature request under `# To fix` below. Keep one section
per item with a short title, the observed behavior, and the expected behavior.

# To fix

## B6 — Terminal copy/paste does not support images

**Observed.** Pasting an image into the terminal inserts nothing: there is no
path from the terminal clipboard to an attachment. The web page has this path
(`app.js` `paste` → `/upload` → image path inserted as input), but the terminal
does not.

**Expected.** The terminal can bring an image in the same way the web page does,
and the resulting attachment is inserted as a path in the input line — i.e. image
paste is a first-class capability of the session, not of one front end.

**Root cause (measured, not inferred).** No terminal emulator forwards image
bytes. The terminal's own paste chord (Cmd+V) can only deliver the clipboard's
*text* flavours: a file copied in Finder/Explorer is exactly that (its path
arrives as pasted text and the submit path attaches it), but a raw image — a
screenshot — has no text form, so nothing reaches the TUI and "inserts nothing"
is literally what happens. That is the one case the paste key covers: the image
can only be had by reading the OS clipboard from inside goa, which is what the
explicit paste key does. `KbPaste` (`tui/keybindings.go`: `Ctrl+V`,
`ctrl+shift+v`, raw `0x16` via `tui/keys.go`) → `Editor.pasteFromClipboard`
(`tui/editor.go`) resolves the clipboard in the precedence a file-manager copy
needs — file paths → image → text — and the platform dispatch lives in
`internal/clipboard_image.go` (`osascript`/`pbpaste` on macOS, `wl-paste`/
`xclip` on Linux, PowerShell on Windows and WSL) behind one runner,
`runClipboardCommand`.

That chain existed before this entry; what was missing was any *pin* on it — no
test covered clipboard bytes → store → path → input line, no test covered a
platform with no clipboard reader, and no real-terminal check existed. That is
how "there is no path from the terminal clipboard to an attachment" could be
reported against a binary that had one (and why the trigger is worth documenting
in `docs/HOTKEYS.md`, next to the text chords).

**Fix (structure, not a rewrite).** Nothing in the paste chain needed changing;
the gap was coverage and discoverability, and both are now closed:

* **One platform seam.** `internal/clipboard_image.go` owns every OS tool call
  and is swappable at one point (`runClipboardCommand`); the editor never calls
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
  file manager, then an image, then text".

**Tests.** `internal/clipboard_paste_chain_test.go` (new) runs
`fakeImageClipboard`: a fake runner on *every* OS answers each backend's command
shape, so clipboard bytes → `ReadClipboardImage` decode → `SaveClipboardImage`
(store redirected to `t.TempDir()`) → stored path → `IsImageFile` runs with no
real clipboard and no OS tool; plus the two failure paths (nothing on the
clipboard → `(nil,nil)`, silently; bytes that are not an image → a decode error).
`tui/editor_image_paste_chain_test.go` (new) drives the real `Ctrl+V` binding
through the *real* store to the input line
(`TestEditor_PasteFromClipboard_StoresImageAndInsertsStoredPath`) and pins both
failure paths — no image on the clipboard, and *no reader at all* on this
platform (`nil` readers: quiet no-op, nothing written to the store).
`internal/app/pasted_image_test.go` (new) pins the app-level tail:
`OnImagePaste` → `handlePastedImage` inserts the stored path into the input line,
separated from the text the cursor sits in.

**Validation (real terminal, real clipboard).** `e2e/clipimg.sh` (new) puts a
known 8×8 PNG on the OS clipboard, boots a real `goa` in a PTY (config pinned to
`e2e/mockllm`, so no model traffic) and presses `Ctrl+V`; it asserts the
*rendered input line* shows `…/goa/images/goa-image-<n>.png` **and** that the
stored file is that image (IHDR re-read from the stored PNG):

```
[PASS] Ctrl+V pasted the clipboard image: input line =
  /Users/…/Library/Caches/goa/images/goa-image-4102929851.png (8x8, file on disk)
```

`e2e/ptydrive` gained a `--wait-output <regex>` condition for this (assert on
what the TUI rendered, not on a polled file), and `e2e/README.md` documents the
check. Live-process note: the TUI was shut down by the driver (`gracefulStop`);
no `goa`/mock-LLM process was left behind.

**Validation, the reported case (image copied in a browser).** The synthetic PNG
above proves the chain; the report is about an image copied *in a browser*, so
that was driven too. A page whose image is **40×24** (deliberately unlike the
script's 8×8) writes the real OS clipboard through the browser's own clipboard
API from a real click:

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

**Regression evidence (before/after).** With `tryPasteImage` short-circuited to
"no image" (`return "", false`), the three `tui` paste tests fail
(`StoresImageAndInsertsStoredPath`, `InsertsImageReference`,
`CallsOnImagePaste`) and `e2e/clipimg.sh` reports
`FAIL paste key produced no image path in the input line (ptydrive rc=1)`;
restored, both pass. The same short-circuit also leaves the input line empty in
the script's screen dump, which is the reported symptom.

## Closed

Closed 2026-10-04 — B2, the web UI painting a long output two or three times
after `/quota`: the compositor wipes the terminal's scrollback and re-emits the
whole transcript at a new width (and on a mid-transcript edit). A terminal loses
its transcript to that wipe and the re-emitted rows replace it; the browser kept
its own list and appended them, so every already-scrolled line came back — and
B3's erase-and-suppress workaround left a seam that repeated (or dropped) the
boundary rows. `tui.TermEmulator.ScrollbackGeneration` now reports the wipe, the
grid ships the batch that follows it as a *replacement* (wire `sbr`), and
`app.js` clears its transcript before appending. Pinned by
`internal/app/webui_b2_longoutput_test.go` (three scenarios through the real
`EncodeKey` path, including the reported `/quota`-during-streaming case),
`TestClientJS_TranscriptReplace*` for the page, and a real-browser DOM dump at
the screenshot's geometry (984×692: 79 content lines, 0 duplicates). See
[`docs/archive/webui-b2-long-output-once.2026-10-04.md`](docs/archive/webui-b2-long-output-once.2026-10-04.md).
Closed 2026-10-04 — B4, the web UI's input line / bottom status bar: the bottom
band (input row, separators, status row with the right-aligned mode, model line)
now renders exactly like the terminal's at the same geometry and stays inside the
viewport. Root cause: the same startup-canvas overflow as B3 (the boxed banner
made the canvas taller than the browser's grid, so the compositor scrolled the
header into the emulator's scrollback and the transcript pushed the footer band
below the fold); B3's single-row startup info entries removed it. Pinned by
`internal/app/webui_b4_footer_test.go` (web grid == terminal screen, row for row,
at 10 geometries, plus the band's presence/order) and an `e2e/w1_webui_browser.sh`
`footer_band` check in a real browser. See
[`docs/archive/webui-b4-bottom-band.2026-10-04.md`](docs/archive/webui-b4-bottom-band.2026-10-04.md).

Closed 2026-10-04 — B1, `goa server` could not be stopped from its own console:
Ctrl+C/SIGTERM are now wired into the session's own stop path, the listener
closes after the session ends, deferred profiling flushes and the process exits 0.
See [`docs/archive/2026-10-04-webui-server-shutdown.md`](docs/archive/2026-10-04-webui-server-shutdown.md)
for the root cause, the RED/GREEN measurements and the residual risks.

Closed 2026-10-04 — web UI rendering (scroll / caret / resize / clipboard) and
the O(history) per-frame cost. See
[`docs/archive/webui-rendering-and-frame-cost.2026-10-04.md`](docs/archive/webui-rendering-and-frame-cost.2026-10-04.md)
for the root causes, the measurements and the verification, and
[`docs/webui-perf-assessment.md`](docs/webui-perf-assessment.md) for the full
performance record.
