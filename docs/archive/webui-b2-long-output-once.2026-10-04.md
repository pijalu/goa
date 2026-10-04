# B2 — Web UI: long output painted two or three times

**Closed** 2026-10-04. Fix commit: `webui: a transcript wipe replaces the
browser's transcript instead of extending it (bugs.md B2)`.

Reported evidence: `docs/bugs/2026-10-04-webui-quota-corruption.png` (1968×1384 =
a **984×692** viewport at devicePixelRatio 2 — the geometry every reproduction
below uses), "after `/quota` the whole screen is corrupted with output appearing
two or three times over".

## What the browser actually receives

The page is one scroll container: a **transcript** (the rows that scrolled off
the live screen) followed by the **live grid**. The server ships the transcript
in its own wire message (`t:"scrollback"`), from `CellGrid.TakeScrollback`, for
every row the emulator pushes off the top of its screen.

Two mechanisms in that path each put a line on the page twice:

1. **A wipe the browser could not see.** When the terminal's width changes (and
   on a mid-transcript edit) the compositor emits `\x1b[2J\x1b[H\x1b[3J`
   followed by a **re-emit of the whole transcript** at the new width. On a real
   terminal that is a replacement — the wipe empty the terminal's scrollback, so
   the re-emitted rows *are* the transcript. The browser kept its own list and
   appended the re-emit to it, so every already-scrolled line appeared again, and
   again for every further reset ("two or three times over").

2. **The B3 seam.** B3's fix erased the emulator's scrollback on a geometry
   change and dropped the repaint's overflow, precisely so the browser's copy
   would *not* be duplicated. The browser's copy stayed — but the rows the next
   output pushed off were the *new* geometry's rows, starting at an arbitrary
   index: the seam then repeated the tail of the old transcript (and could just
   as well drop rows). Reproduced at the reported geometry in the app harness:
   `history row 048` shipped twice.

## Fix — one source of truth for the transcript

The terminal's own `\x1b[3J` semantics are now propagated instead of being
worked around:

* `tui.TermEmulator` counts the times its transcript is dropped
  (`ScrollbackGeneration()`), incremented by the CSI 3J handler — the stream's
  own statement that a terminal's scrollback was wiped.
* `CellGrid.Process` notices the drop and marks the next batch as a
  **replacement**; `TakeScrollback` then ships every row it retains
  (`TranscriptBatch{Rows, Replace}`), so the browser rebuilds the transcript
  instead of extending it. `Clear()` (a programmatic reset) says the same thing
  with no rows.
* The flag rides its own wire message (`t:"scrollback", "sbr":true`), encoded
  even when the batch is empty — "the transcript you hold is gone" is a complete
  statement. `app.js` clears the transcript list before appending when it sees
  it (`clearTranscript`).
* B3's erase-and-suppress seam is gone (`EndGeometryChange`, `geometryChange`):
  a geometry change no longer drops the re-emitted history, it ships it as the
  replacement it is.

Rows are never re-sent by an ordinary frame, so the incremental cost is
unchanged: `TakeScrollback` still reads only the rows past the shipped mark, and
only a wipe resets that mark.

## Verification

**Harness (internal/app, real `webui.VirtualTerminal`, real engine, real
`webui.EncodeKey` input path)** — `internal/app/webui_b2_longoutput_test.go`
models the page from the wire (patches → grid, scrollback batches → transcript,
honoring the replacement):

| scenario | pre-fix | fixed |
| --- | --- | --- |
| long output after the browser's geometry report | `history row 048` twice | every line once, in order |
| long output typed mid-stream (the reported `/quota` case) | `quota row 00..07` twice (scratch probe in a worktree at the pre-fix commit) | every line once, in order |
| two real `/help` runs with a geometry change between | — | every list line exactly twice |

**Client (`app.js` under goja)** — `TestClientJS_TranscriptReplaceDropsWhatTheClientAlreadyHad`
and `TestClientJS_TranscriptReplaceWithoutRowsClearsTheList` fail without the
`sbr` handling (11 rows instead of 6). The DOM stub now models `textContent` as
the spec's accessor (assigning it replaces children), which is what makes a
"clear" observable at all.

**Real browser, reported geometry (984×692)** — `goa server`, a fresh page,
`/help` typed as keys: transcript 57 rows + grid 40 rows, 79 content lines,
**0 duplicated lines**, the transcript tail (`• /prompt`, `• /provider`)
continuing seamlessly into the grid head (`• /pty`, `• /quit`) — chronological,
each line once. Fresh screenshot:
`docs/bugs/2026-10-04-webui-b2-after-fix.png`.

**Gate** — `go vet ./...`, `staticcheck` on the touched packages, `gocognit -over
15` / `gocyclo -over 12` on the touched files, and `go test -count=1 -race
./...` all clean.

**e2e `e2e/w1_webui_browser.sh`** — every page-mechanics row PASS
(`scroll_container`, `caret_visible`, `scroll_follow_tail`, `scroll_detach`,
`transcript_bounded`, `resize_trims`, `grid_height`, `clipboard_chords`), and
`command_help`/`config_*`/`pin_held` PASS. Two rows FAIL, both **identically on
the pre-fix build** (checked by running the same script from a worktree at the
pre-fix commit), so neither is a regression: `streaming` (the provider answered
in one chunk, so no incremental growth was observed) and `footer_band` at
1280×900 — filed as B8/B9 below.

## Found while testing (not B2, filed separately)

* **B8** — a *view* command whose output is taller than the screen (`/tools`)
  drops the earlier transcript from the page (observed: `/help`'s 64 list lines
  fall to 22–31 once `/tools` runs). Identical with and without this fix, with
  and without a resize, and *not* reproducible in the app harness at the same
  geometry (the real registry's `/help` stays complete there), so the trigger is
  still unidentified. Pre-existing; needs its own investigation.
* **B9** — `footer_band` in `e2e/w1_webui_browser.sh` fails at 1280×900: 53 grid
  rows (16 + 53×16 = 864 px) overlap the 24 px status bar, i.e. the page sizes
  the grid one row too tall for that viewport. Pre-existing.

## Residual risks

* A replacement batch can be up to the emulator's retention cap (2 000 rows):
  one JSON message of the whole retained transcript on a width change. On a real
  terminal the same event re-emits the same rows as bytes, so the payload shape
  matches the event's nature; it is bounded by `MaxScrollbackRows`, not by the
  session's length.
* A client that misses a replacement batch (a dropped frame) still self-heals
  through the existing seq-gap resync, which sends a full frame; the resync
  carries the pending batch only, so a reconnecting page can miss history it
  never had — the pre-existing joiner behaviour (B7-adjacent), untouched here.
* `internal/webui`'s DOM stub changed: `textContent` now replaces children, so
  any future test that relied on a row accumulating runs across repaints will
  fail loudly rather than silently pass.
