# B39 + B49 — @ completion: bounded async search, ~ expansion, encoded paths

Closed: 2026-11-01

## Report

**B49.** The `@` path completion ran a filesystem search synchronously on
every keystroke, on the UI goroutine. The engine (internal/filefind) ran the
`fd` deep search FIRST — unbounded (no depth cap, `--follow` enabled, no
timeout) — with `os.ReadDir` only as a fallback, so a prefix over a huge tree
could dive the whole filesystem per keystroke and stall editing. It also had
no `~` handling (bare `@~` tracked separately as B39) and no decoding: a
pasted `file://` URL or a percent-encoded path (`my%20file`) never matched
anything on disk.

The user's follow-up sharpened the diagnosis: "the point is a user had issue
editing a path due to constant rerun of completion: completion must be
bounded, async to avoid UI issue — possibly slowed down on edition." The
root cause is the synchronous per-keystroke rerun, not the fd-vs-fallback
choice.

**B39.** A bare `@~` completion request resolved nothing — tilde was never
expanded, so the engine searched a literal `~` directory under the workdir.

## Resolution

**internal/filefind — one bounded resolver pipeline** (shared by every file
completer):

- `decodePrefix`: strips `file://` (bare or `file://localhost` authority)
  and percent-decodes (`%20` → space) before matching; an invalid escape
  keeps the raw text.
- `expandTilde`: `~` and `~/…` resolve to `$HOME` (bare `~` gains a trailing
  `/` so it lists home); with HOME unset, tilde prefixes yield no candidates
  instead of searching a literal `~` directory.
- `Complete` runs a decoded pass and, when it matched nothing (and decoding
  changed the prefix), retries the raw form so literal `%xx` file names
  still complete. Exact-file suppression stays terminal: a decoded token
  naming an existing file yields no popup and no raw retry.
- **Shallow-first ordering (inverted):** one `os.ReadDir` of the directory
  the user is typing in answers first; `fd` only rescues queries the
  shallow pass cannot answer. A keystroke-triggered completion never walks
  the whole tree.
- **Bounded fd rescue:** `--max-depth 3+len("/")` in the query (depth grows
  with typed path segments, never tree size), no `--follow` (one symlink
  could pull the whole filesystem in), and a 250 ms context timeout; the
  50-result cap unchanged (`--max-results 50`).

**tui Editor — async debounced completion:**

- `scheduleAutoComp` distinguishes cheap from expensive: history-search and
  slash-command completions stay synchronous for immediate feedback;
  `@` prefixes debounce (150 ms, each keystroke restarts the timer so rapid
  typing coalesces into one search for the final prefix) and run off the UI
  goroutine (`compSearch` on the timer goroutine).
- Delivery is actor-model safe: with the engine loops running, results are
  installed via `ApplySync` on the command loop (sole owner of compState);
  generation counter (`compGen`) stale-drops results when the user kept
  typing while the search ran; `compApplied` channel signals finished
  searches (after compState settles) for race-free test synchronization.
- `backspace` now routes through `scheduleAutoComp` (it ran
  `updateAutoComp` synchronously — same per-keystroke stall); leaving the
  completion context closes the popup and bumps the generation so a
  dismissed popup cannot resurrect from an in-flight search; async applies
  request a render frame.
- Dead `compAbort` channel replaced by `compGen`/`compApplied`.
- Scope: `*tui.Editor` (the production input); `LineEditor`/`Input` remain
  overlay/wizard surfaces without file completers and stay synchronous.

## Validation

- RED observed (assertion-level, pre-fix):
  - filefind: fd invoked for a shallow query (argv logged);
    `--max-depth` missing and `--follow` present in fd argv; `~` prefixes
    returned nil; `f%20g` / `file://…%20` returned nil.
  - tui: `@~`/`@~/` returned nothing; `@f%20g`, `@file://…` returned
    nothing; three keystrokes ran the completion synchronously 3×
    (`TestEditor_AutoComp_FilePrefixAsyncDebounced` reported "3 call(s),
    want 0"); backspace ran it synchronously.
- GREEN: internal/filefind and tui suites (incl. new B49 tests:
  shallow-skip, fd-bounded argv, tilde expansion, encoded decode with raw
  retry, literal `%20` and invalid-escape guards, async debounce + coalescing,
  stale-drop, command-prefix-stays-sync guards) — `go test -count=1 -race`
  green for both packages.
- Production-path filmstrip (real engine + Editor + FileCompleter, Actor
  loops running, keys injected via Apply — `tui/editor_b49_filmstrip_test.go`):
  - `@` → no popup on the keystroke, popup appears async:
    `── Files ── / › @plans/`
  - `@p` → drill-down filter (`› @plans/`, README gone)
  - Tab → buffer `@plans/`
  - `@~` → `› @~/todo.txt` (HOME-abbreviated display)
  - `@f%20g` → `› @f g.md` (decoded)
- Two data races found and fixed while wiring the async path: result signal
  fired before compState settled (moved to defer), and `clearCompletion`
  touched the timer field from the timer goroutine (timer lifecycle kept
  owner-goroutine only; stale results dropped by generation).
- Full gates run separately: `go vet ./...`, `staticcheck ./...`,
  `gocognit -over 15 .` and `gocyclo -over 12 .` (only the documented
  pre-existing hotspots, all unrelated), and the full race suite
  `go test -count=1 -race -timeout 600s ./...` green repository-wide.

Known, documented caveat (pre-existing): a completion `Value` containing a
decoded space (`@f g.md`) can break the next completion tokenization when
inserted in token mode; accepted — re-triggering completion on the inserted
path is the escape hatch.
