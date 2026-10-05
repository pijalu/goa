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

## B9 — Goal exit mutates the cached prompt (tools dropped on the text-only collapse) → full-prompt cache miss

**Observed.** Export `goa-export-20261005-135830.zip` (kimi-code `k3-256k`). Two
provider cache misses, both `tool_policy_transition`. Raw provider frames:
request 18 (52 msgs, tools present) `prompt_tokens=42,811 cached=41,728`;
request 19 (54 msgs — a byte-exact message append) `prompt_tokens=41,086
cached=1,792` with `tools` removed and `tool_choice:"none"`. The 39,294-token
tail was recomputed and re-billed at full price; the same pair earlier in the
session went 115,200 → 0. The warning lands at the goal *switch*, which is why
it read as reset-related, but the reset is clean: the cache identity rotated
(`goa_3333e5f8…` → `goa_cd2c6252…`) and the fresh chain's first request still
hit 4,608 cached tokens.

**Root cause.** `tools/goal/goal.go:881` (`Goal marked complete.`) returns
`StopTurn: true` → `agent_turn_lifecycle.go:55` arms `toolCollapseNextRound` →
`agent_streaming.go:203` sets `NoTools` → the protocol builders dropped the
`tools` array for that one round (`openai_completions_messages.go`
`applyToolChoice`, plus the anthropic/mistral/google/responses equivalents).
The tool schemas are part of the provider's cached prefix, so removing them
moved the divergence point from the tail of the conversation to right after the
system prompt. Same class on the Codex WebSocket path, whose reuse fingerprint
compares `tools`, `tool_choice` and `parallel_tool_calls`
(`openai_responses/ws_incremental.go`) — the collapse broke all three.

**Expected.** A goal exit stays on the same context: the request is an
append-only continuation of its predecessor (messages appended, prompt-bearing
fields byte-identical). The text-only intent may move only `tool_choice`, a
prompt-neutral control field.

**Fix plan.**
1. Invariant test first
   (`protocol/no_tools_test.go:TestCollapse_PreservesCachedPromptSurface`):
   per flavor, build the normal and the collapse body from one context and fail
   if any field differs except the allowed control key
   (`tool_choice`/`toolConfig`); assert `tools` is byte-identical.
2. Keep the tool surface in every protocol builder; express the collapse on
   `tool_choice` where the upstream accepts `none` (verified live against
   `api.kimi.com`: tools kept + `tool_choice:"none"` → HTTP 200, prefix cache
   retained, and still no tool call even when the prompt demanded one).
3. Per-model knob `schema.CompatFlags.SupportsToolChoiceNone` (default true;
   **false on the Responses flavors**, where strict upstreams 400 on anything
   but `auto`) suppresses even that, leaving the round byte-identical.
   Resolved by `protocol/tool_choice.go`.
4. Stop dropping `parallel_tool_calls` (fingerprint-compared, cannot yield a
   tool call on its own).
5. Cache forensics: new `tool_choice_collapse` classification for the
   cache-neutral shape; `tool_policy_transition` now means the buggy/legacy
   shape only, so a reappearance is flagged.
6. Update the tests that pinned the old contract (`no_tools_test.go`,
   `openai_responses_collapse_test.go`, `openai_codex_responses_test.go`,
   `zai_tool_stream_test.go`) and `docs/PROVIDER-CACHE.md`.

**Validation.** Unit: `go test -count=1 -race -cover ./internal/agentic/...`.
Live: four-request A/B against kimi-code with a ~3k-token prompt — tools kept +
`tool_choice:none` on an appended pair `cached=2,816/3,111` vs tools removed
`cached=0/3,034`, both HTTP 200 and text-only. Then the five quality gates below.

## B8 — Web UI: a screen-taller *view* command drops the transcript above it

**Observed.** At the reported window size (984×692), `/help` on a fresh page puts
all 64 command-list lines on the page (transcript + grid, each line once).
Running `/tools` — whose table is taller than the screen — then drops most of
them: the page keeps 22–31 list lines and its transcript starts mid-list (`•
/setup`, `• /provider`). Rows are lost, not duplicated. Identical with and
without the B2 fix, with and without a window resize in between, and *not*
reproducible in the app harness at the same geometry (there the real registry's
`/help` stays complete through a width change), so the browser/socket path holds
the trigger.

**Expected.** Every line stays on the page exactly once: appending a tall output
must not remove rows the browser already holds.

**Plan.**
- Reproduce in a harness that includes the *socket* path (the real `goa server` +
a browser, or a websocket client replaying the frames) so the difference from
the in-process harness is visible.
- Suspect the compositor's scrollback watermark: a wipe (CSI 3J) that the app
performs implicitly makes rows the compositor culls as "already in scrollback"
unreachable to a client that has just replaced its transcript. Instrument the
frames around `/tools` (seq, batch size, replace flag, emulator base/length).
- Validation: the browser DOM dump at 984×692 shows the `/help` list complete
after `/tools`, and the harness passes the same sequence.

## B9 — e2e footer_band fails at 1280×900: the grid is one row too tall

**Observed.** `e2e/w1_webui_browser.sh`'s `footer_band` check fails with
`rows:53, inside:false, overlap:true`: the grid is 16 + 53×16 = 864 px, the
status bar is 24 px, so the grid's bottom crosses into `#status` at that
viewport (the page sized the grid one row taller than fits). Reproduced on the
pre-fix build, so it is not a B2 regression; the app-level B4 pinning test
(`webui_b4_footer_test.go`, 10 geometries) passes, so this is the browser's own
row-count arithmetic at this viewport.

**Expected.** The grid fits the viewport at every size, with the band inside it.

**Plan.**
- Reproduce headlessly: compare the page's reported `rows` with the viewport's
usable height at 1280×900 (and the sizes in the B4 test).
- Fix the row-count arithmetic and add the failing viewport to the B4 geometry
list so the app harness covers it too.

## B10 — Web UI: scrollback colours are wrong

**Observed.** Reported while B5 was being fixed: "scroll color aren't correct" —
colours in the scrolled-off transcript do not match the live grid.

**Expected.** A row rendered in the transcript looks the same as the same row did
in the grid: the same run classes, the same palette, no theme-default fallback.

**Plan.**
- Reproduce in the browser: emit a coloured screen (`/help` has styled rows), let
  it scroll into `#scrollback`, then compare each transcript row's computed
  colours against the same row's colours while it was in the grid (same run text,
  same class list, same `color`/`background-color`).
- Suspect the shared run builder: transcript rows are built by `buildRow` from
  `RowPatch` runs, while live rows go through `applyRow`; if one path drops the
  style fields (`fg`/`bg`) or the class flags, the transcript loses colour.
- Also check the server side: scrollback batches are encoded separately
  (`EncodeScrollback`), so a run field lost in that encoder would only show up
  after a row scrolls off.
- Validation: the browser harness compares grid colours and transcript colours for
  the same content, and a goja test asserts a transcript row keeps its runs.

## B5 — Web UI: selecting text works, copy/paste does not

**Observed.** Text can be selected in the page, but copying it does not reach the
system clipboard and pasting does not insert anything.

**Root cause (measured, not inferred).** The page left Cmd/Ctrl+C/V/X to the
browser and relied on the implicit `copy`/`paste` events. Chrome only wires those
chords to **editable** targets, and the terminal's grid is not one: with `#grid`
focused, a real Cmd+C over a live selection produced **no `copy` event and left
the clipboard unchanged**, and a real Cmd+V produced **no `paste` event at all**
(so `document.addEventListener("paste", …)` never ran). Reproduced by driving
headless Chrome over CDP with real chords (`Input.dispatchKeyEvent`) and reading
`navigator.clipboard.readText()` back with the clipboard permission granted and
focus emulated (`Emulation.setFocusEmulationEnabled`) — without the permission
and the emulated focus Chrome auto-denies the reads, which is why the failure had
looked environmental.

Two further measurements shaped the fix:

* CDP-injected chords never trigger the *browser's* clipboard actions — in
  headless and headed alike (a focused hidden textarea received no `paste`
  either). The page's own path is therefore the only testable one.
* In a real (headed) Chrome, `navigator.clipboard.readText()` sits behind a
  **permission prompt** even inside a trusted keydown (the promise never settles
  until it is answered). So a paste must first be handed to the browser's own
  paste, which needs no permission; the async read is only the fallback.

**Expected.** Cmd+C with a selection puts the selected text on the system
clipboard; Cmd+V pastes clipboard text into the input line; Cmd+X cuts; the
engine's own Ctrl chords keep working.

**Fix.** `internal/webui/assets/app.js`:

* `clipboardChord` decides the chord's fate: `CHORD_DONE` (the page did it),
  `CHORD_BROWSER` (the browser keeps it — nothing sent, nothing prevented),
  `CHORD_KEY` (an ordinary keystroke for the engine).
* Copy: `writeClipboard` uses `navigator.clipboard.writeText`, falling back to
  `document.execCommand("copy")` through a detached textarea (both work from a
  keydown, where the user gesture comes from).
* Paste: the chord focuses the hidden `#paste-target` textarea and does **not**
  `preventDefault`, so the browser pastes into an editable element and delivers a
  `paste` event carrying the clipboard — no permission involved. The chord's
  **keyup** closes the paste window (`finishPaste`); if no paste event arrived (a
  script-dispatched chord performs no browser paste) the page reads the clipboard
  itself from there, which is still a user-gesture context. The 500 ms timer is
  only a backstop. `pasteHandled` keeps the two paths from double-inserting.
* Cut: copies the selection and removes it from the input line by driving the
  engine's own keys — Backspace when the selection ends at the cursor, Delete
  when it starts there, and (for a selection neither reaches) the cursor is walked
  to the selection's end first, using pixel-measured cell columns. Text the
  terminal **output** cannot delete is copied and left alone; the walk is skipped
  on a line holding two-cell glyphs, where columns and buffer characters disagree.
* Ctrl+C with **no** selection is still sent to the engine as the interrupt
  (`\x03`), and a viewer (read-only) can still copy but never paste or cut.

**Tests.**

* `internal/webui/browserclient_test.go` (real `app.js` under goja): the chord
  contract, the paste target/focus hand-off, the keyup fallback, the backstop, the
  no-clipboard-API fallback, the execCommand copy fallback, the read-only viewer,
  and all three cut shapes (adjacent, mid-line walk, wide-glyph guard, output left
  alone). The stub gained `document.activeElement`, a clipboard mock, range
  geometry and a faithful `textContent` getter (which had hidden a row's text).
* `e2e/webclip` (new Go CDP driver): real chords, real clipboard — `copy`, `paste`
  and `cut` are asserted end to end against a live `goa server` page, with the
  permissions granted, focus emulated and the tab foregrounded.
* `e2e/w1_webui_browser.sh`: the synthetic chord check now pins the NEW ownership
  contract, and a new `clipboard_real` check runs `e2e/webclip`.

**Validation (real browser, real clipboard).** copy: Cmd+C and Ctrl+C put the
selection on the clipboard; paste: Cmd+V inserted the clipboard text **exactly
once**; cut: Cmd+X copied the selection AND removed it from the input line (both
the adjacent and mid-line cases); Ctrl+C with no selection was claimed by the page
(`defaultPrevented`, focus on the grid) so the engine still gets the interrupt.
`e2e/webclip` reports `copy PASS; paste PASS; cut PASS`.

**Regression evidence (before/after).** The new client tests fail against the
previous `app.js` and pass against the fixed one: with `git show HEAD~1:
internal/webui/assets/app.js` in place, the clipboard tests report
`page let Ctrl+C through with a live selection`, `clipboard = "" after Ctrl+C`,
`focus after Ctrl+V = ""`, `paste inserted 0 times`, `cut sent ""` and
`paste scheduled no backstop timer` — seven failures that the fix removes.

**End-to-end interrupt.** `goa server` pinned to the mock LLM started a turn
(the mock logged the completion request, the footer showed the streaming
percentage), and a real Ctrl+C chord with **no selection** then ended the session
(`exit=0`, no process left, mid-turn): the byte reached the engine and the engine
acted on it, which is what "still reaches the engine as the interrupt" means — the
engine's own Ctrl+C binding (clear the input line, or stop the TUI when it is
empty) decides the effect, and the page no longer swallows it.

**Harness rules learned here** (in `AGENTS.md`): drive Chromium **headless**, keep
injected keys inside the page's vocabulary, grant the clipboard permission and
emulate focus explicitly, foreground the tab (background tabs throttle timers),
and never dispatch Ctrl+C-with-no-selection against a session whose input line is
empty — the engine reads it as "stop the TUI" and the server exits.

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

## B7 — Web UI: keystrokes typed while the session is still starting are not acted on

**Observed.** A browser that connects the moment the listener answers — i.e.
before the session has finished wiring itself — can type without effect. Measured
while fixing B1 with a real `goa server` over a websocket client: sending the
`/quit` keys immediately after `goa web UI ready: ...` is ignored (the process
stays up), while the same keys sent 350 ms+ after startup always quit it. The
listener binds and accepts clients before `App.RunContext` builds the engine, so
the window is the session's startup time (tens of milliseconds on this machine).

**Expected.** Typing always has an effect: bytes that arrive before the engine
exists are held and delivered when it does, and once they are delivered the
submit path is there to act on them.

**Partial fix already in tree.** `webui.VirtualTerminal` now holds pre-`Start`
bytes (bounded, 4 KiB) and replays them in `Start`, instead of dropping them
silently (`TestVirtualTerminal_InputBeforeStartIsReplayedOnStart`). The remaining
half is the submit path: `inp.SetOnSubmit` is wired in `setupEventHandlers`,
*after* `buildTUI` has already started the engine — so an Enter replayed at
`Start` is consumed with no handler attached (the typed text stays in the editor,
it is not lost). Making the first keystrokes fully effective needs either the
submit wiring moved before `engine.Start` or the listener to start serving only
once the session is ready; both touch session startup, so they are left here.

**Plan.**
- Decide the gate: either (a) wire `inp.SetOnSubmit` inside `buildTUI` before
  `engine.Start`, proving no replayed key can reach unwired app state, or (b) keep
  the listener bound but start `srv.Serve` only after the session signals ready.
- Test approach: the existing e2e (`cmd/goa/e2e_server_signal_test.go`
  `TestGoaE2E_ServerBrowserQuitStopsProcessWithStatusZero`) sends `/quit` exactly
  at connect time; drop its re-send loop once the gate exists, which is the
  deterministic assertion this needs.
- Validation: repeated runs of that test with no re-send, plus a browser test that
  types immediately on load.

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
