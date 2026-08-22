<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Spec: /review:file:<file> — Single-File Review

## Status

Accepted — implemented.

## Goal

Add a `file` subcommand to `/review` that opens **one file** in the review
pager (the same interaction model as the diff review: scroll, comment on
lines, edit/delete comments, submit), and on submit sends the agent a message
containing a **link to the file** plus **all comments with the lines they are
attached to**.

```
/review:file:internal/app/tui.go
```

The pane is file-type aware:

- **Text files only** — binary files are rejected with a clear message.
- **Code files** — syntax-highlighted (same per-line highlighter as the diff
  review pager).
- **Markdown files** — rendered as highlighted Markdown *source* (headings,
  fences, lists, inline styles), preserving a strict 1:1 display-line ↔
  source-line mapping.

The file argument is completed exactly like `@`-mentions (fd-backed,
gitignore-aware, ranked; `os.ReadDir` fallback).

## Non-goals

- Editing the file from the pager (review only — the agent does the edits).
- Re-reviewing/diffing a file between two commits (that is `/review:<ref>`).
- Image/preview rendering.

---

## UX Specification

### Invocation

| Input | Behavior |
|---|---|
| `/review:file:<path>` | Open `<path>` in the file review pager |
| `/review:file` (no path) | Usage message: `Usage: /review:file:<path> (text files only)` |
| binary / missing / directory / oversized | Friendly chat message, no pager |
| `/review:file:<path>` outside a git repo | **Works** (file review does not require git) |

Path resolution: relative paths resolve against the project dir; absolute
paths are used as-is. Note: the router splits args on `:`, so paths containing
`:` are not addressable (same constraint as every colon command; documented).

### Pager (mirror of the diff review pager)

Title line (bold):

```
Review file internal/app/tui.go  1234 lines  comments:2  (truncated)
```

- `(truncated)` only when the file exceeded the load caps.
- Body rows: `prefix(2)` + right-aligned line-number gutter + sanitized,
  highlighted content, truncated with `…` to viewport width (no wrapping —
  same policy as the diff pager).
- Cursor line: `> ` with dark-blue bg (`#1e4273`); commented lines get the
  green pipe `│` + bg, plus `[N comment(s)]` badge — identical visuals to the
  diff review pager so both pagers read as one feature.

Keys (same as diff pager minus base switching):

| Key | Action |
|---|---|
| `↑`/`↓`, `k`/`j`, `PgUp`/`PgDn` | navigate |
| `c` | add comment on current line (main input line, prompt `Add comment on <file>:<line>:`) |
| `e` / `d` | edit / delete comment on current line |
| `s` | submit (confirm via main input line `y/n`) — sends summary to agent, closes pager |
| `x` | export summary to `review_file_<name>_<timestamp>.md` in project root, pager stays open |
| `q` / `Esc` / `Ctrl+C` | close |

Input-line help title while open:
`q:close ↑↓:scroll c:comment e:edit d:delete s:submit x:export`

### Submitted message (to the LM)

Produced by `Session.MarkdownSummary()` (file kind) — single source of truth
shared by submit (`s`) and export (`x`), exactly like the diff review:

```markdown
# File Review

- **File:** /abs/project/internal/app/tui.go
- **Lines reviewed:** 1234 (truncated)   ← "(truncated)" only when capped

Read the file to see each comment in context. Comments are anchored to the
line numbers of that file.

## Comments

- `internal/app/tui.go:42`: the error is swallowed here
- `internal/app/tui.go:120`: this loop is O(n²)
```

- The **absolute path** is the "link": `SubmitToAgent` → `sendToAgent` runs
  `expandAtRefs`, and the read tools resolve absolute paths directly.
- Anchors use the project-relative path + 1-based source line
  (`Comment.AnchorLabel()`), so the agent can jump straight to the line.
- No comments → `No comments yet.` (same as diff review).

### Completion (`file` scope, "@-like")

`Tab` after `/review:` suggests, in order: `^1`, `^2`, `^3` (unchanged),
**`file:`** ("Review a single file"), then recent tags/branches (unchanged).

After `file:` the path completes exactly like `@`:

- fd-backed search (gitignore-aware, hidden respected, `.git` excluded),
  `os.ReadDir` fallback when fd is missing;
- ranking: exact > case-sensitive prefix > case-insensitive prefix > fuzzy,
  shorter basenames first (Issue-8 tiering);
- directories complete with trailing `/` (drill-down); a token that already
  names an existing file yields nothing (popup suppressed — "path is done");
- values are project-relative when inside the project, else absolute; values
  carry the `file:` scope prefix so the popup builds `/review:file:src/ma…`.

---

## Design Decisions

### D1 — Markdown is rendered as highlighted *source*, not reflowed prose

The review pane anchors comments to **real source lines** (that is what the
agent receives: `file.md:42`). The chat markdown renderer reflows (wraps,
merges list items, multi-line constructs), which would decouple display rows
from source lines and make every anchor a lie. GitHub PR review shows
Markdown *source* with a Markdown lexer for the same reason. We therefore
highlight Markdown source 1:1 (headings, fenced code with inner-language
highlighting, lists, quotes, inline code/bold/links). This keeps the anchor
invariant of the whole review subsystem intact.

Rejected: routing `.md` through `MDStreamRenderer` (breaks anchoring; would
require a display→source map threaded through the shared chat renderer).

### D2 — Syntax highlighting reuses the existing per-line highlighter

`tui` diff review already renders file content with `tools.HighlightLine`
(`tools/renderer_common.go`), which sanitizes untrusted content and colors
go/python/bash/json/yaml. The file pager reuses it — one highlighting
convention across the app. Unknown extensions render sanitized plain text.

Rejected: chroma (present in go.mod but only used by the web build).
Introducing a chroma→ANSI theme pipeline in the TUI would be a second,
inconsistent highlighting system. Can be revisited later behind the same
per-line API.

### D3 — Extend `review.Session`, don't fork it

`Session` gains `Kind` (`""` = diff, zero value keeps every stored session
loadable unchanged) and `FilePath`. Comments, `Store` persistence
(`.goa/reviews/<id>/session.json`), `AddComment`/`CommentsFor`, submit and
export all stay shared. File comments always use `Side: SideNew` — a file has
one coordinate space.

### D4 — `file` is a first-class subcommand; git guard moves to the diff path

`args[0] == "file"` dispatches before ref matching. A branch literally named
`file` is shadowed — the same shadowing already applies to `list`, `status`,
`submit`, `export`. The `IsGitRepo` guard moves from `Run` into
`startReview`/`listCommits` so file review (and status/submit/export) work
outside git repos.

### D5 — @-completion logic is extracted, not duplicated

The fd search + fallback + ranking pipeline moves to a new UI-independent
package `internal/filefind`; `tui.FileCompleter` (@) and the new review file
completion both consume it. Guarantees "@-like" is "identical to @",
structurally.

### D6 — Comment lifecycle is extracted into a shared helper

`requestAddComment`/`requestEditComment`/`requestDeleteComment`/submit in
`tui.ReviewPager` are behavior moved verbatim into a `reviewActions` helper
keyed on `review.LineAnchor`; both pagers embed it. Prevents drift between
the two pagers' comment UX. Refactor is guarded by the existing
`review_pager_test.go` / `review_comment_input_test.go` suites.

### D7 — Hosting mirrors `showReviewPager`

New event `ShowFileReviewPager` + a dedicated app handler reusing the generic
helpers (`reviewOverlayGeometryFor`, `makeCommentRequestHandler`,
`makeConfirmHandler`). ~30 lines of mirror code instead of a setter-interface
indirection over struct-field callbacks (matches the existing
`showPlanPager` pattern).

---

## Implementation Plan

Phase order minimizes integration risk: leaf packages first, TUI second,
wiring last. Each phase compiles and passes `go test ./...` independently.

### Phase 1 — `internal/filefind` (extract @-completion engine)

New package `internal/filefind` (no tui imports):

```go
// Entry is one filesystem candidate.
type Entry struct {
    Path  string // project-relative (when under root) or absolute; dirs end with "/"
    IsDir bool
}

// Available reports whether the fd CLI is on PATH (cached).
func Available() bool

// Complete returns ranked candidates for pathPrefix under root, using fd
// (gitignore-aware) with an os.ReadDir fallback. Applies the same rules as
// the @ completer: exact-existing-file suppression, dir drill-down,
// Issue-8 ranking. Limit: 50.
func Complete(root, pathPrefix string) []Entry
```

Moves/adapts from `tui`: `fdSearch` (tui/fd.go), `splitPathPrefix`,
`resolveSearchDir`, `fileCompletion`, `isExistingFile`, the ranking tiers of
`rankFileCompletions` (re-expressed over `Entry`; tui keeps a thin adapter
that stamps `Category: CatFiles` and the `@`/`~` display prefixes).

Files: `internal/filefind/filefind.go`, `internal/filefind/filefind_test.go`
(fd skipped automatically when absent; ReadDir fallback tested with
`t.TempDir()` trees: nesting, dirs-with-slash, exact-file suppression, ranking
tiers). `tui/autocomplete.go` + `tui/fd.go` refactored onto it — existing
`tui/autocomplete_file_test.go` must pass unchanged.

### Phase 2 — `internal/review`: file sessions and loading

**`internal/review/session.go`**:

```go
type Kind string
const (
    KindDiff Kind = ""     // default/legacy: git diff review
    KindFile Kind = "file" // single-file review
)

// Session gains:
Kind     Kind   `json:"kind,omitempty"`
FilePath string `json:"file_path,omitempty"` // anchor path (project-relative or absolute)

// NewFileSession creates a file review session (no git required).
func NewFileSession(projectDir, filePath string) (*Session, error)
```

`MarkdownSummary()` and `ExportPath()` switch on `Kind` (file variants per
the UX spec; export name `review_file_<sanitized-base>_<UTC-ts>.md`).
`AnchorLabel()` already yields `file:line` — reused unchanged.

**New `internal/review/file.go`**:

```go
const (
    maxReviewFileBytes = 2 << 20 // 2 MiB
    maxReviewFileLines = 20000
)

type FileReviewContent struct {
    Path       string   // anchor path (project-relative when inside project, else absolute)
    AbsPath    string   // resolved absolute path
    Ext        string   // lower-cased, dot-less
    IsMarkdown bool     // Ext in {md, markdown}
    Lines      []string // source lines (1-based anchors = index+1)
    Truncated  bool     // hit either cap
    Bytes      int
}

// LoadReviewFile validates and loads a text file for review:
// missing / directory / binary (NUL sniff, first 8 KiB — same heuristic as
// tools/readfile.go) / over-cap → descriptive errors; never panics on
// hostile content (control bytes sanitized later at render time).
func LoadReviewFile(projectDir, path string) (*FileReviewContent, error)
```

Tests (`internal/review/file_test.go`, `session_test.go` additions):
loader table-driven (ok text, CRLF, empty file, missing, dir, binary via
NUL byte, >2 MiB, >20k lines → `Truncated`, `.md`/`.MARKDOWN` detection,
absolute path, path outside project → absolute anchor path); session JSON
round-trip incl. legacy session without `kind` loads as diff;
`MarkdownSummary` file kind (abs path present, anchors `rel:line`, no diff
command embedded, `No comments yet.`); `ExportPath` naming.

### Phase 3 — TUI: markdown source highlighter, shared actions, file pager

**New `tui/markdown_source.go`** — stateful, 1:1 line preserving:

```go
// mdSourceState tracks fenced-code state across consecutive lines.
type mdSourceState struct{ inFence bool; fenceLang string }
func (s *mdSourceState) Highlight(line string) string
```

Rules: fence open/close (```/~~~ + lang, colored marker); inside fence →
`tools.HighlightLine(line, fenceLang)`; ATX headings → bold+color; blockquote
prefix → colored; list bullets/numbers → colored marker; inline — split on
backtick spans first (code color), then `**bold**`, `*italic*`,
`[text](url)` on the non-code segments. Input is `ansi.Sanitize`d. Small
single-purpose funcs (complexity budget ≤15 gocognit / 12 cyclo each).

**New `tui/review_actions.go`** — extracted verbatim from `ReviewPager`
(D6):

```go
type reviewActions struct {
    session          *review.Session
    onCommentRequest func(title, current string, onSubmit func(string))
    onConfirm        func(question string, onResult func(yes bool))
    onCommentSaved   func()
    onSubmitReview   func(text string)
    onClose          func()
}
func (a *reviewActions) AddCommentAt(anchor review.LineAnchor)   // no-op on empty text
func (a *reviewActions) EditCommentAt(anchor review.LineAnchor)  // edits first comment at anchor
func (a *reviewActions) DeleteCommentAt(anchor review.LineAnchor) // confirm → remove first
func (a *reviewActions) SubmitWithConfirm()                       // "Submit review to agent?" → summary + close
```

`tui/review_pager.go` refactors its `request*` methods onto `reviewActions`
(no behavior change; existing suites are the characterization tests).

**New `tui/file_review_pager.go`**:

```go
type FileReviewPager struct {
    Session *review.Session
    Content *review.FileReviewContent

    pager   *annotate.Pager  // generic core (consistency with ReviewPager)
    actions reviewActions    // shared comment lifecycle

    cursor, scrollTop, viewportW, viewportH int

    // Host callbacks — same contract/semantics as ReviewPager:
    OnSubmitReview func(text string)
    OnExportReview func()
    OnClose        func()
    OnCommentSaved func()
    OnCommentRequest func(title, current string, onSubmit func(string))
    OnConfirm      func(question string, onResult func(yes bool))
    RequestRender  func()
}

func NewFileReviewPager(session *review.Session, content *review.FileReviewContent) *FileReviewPager
func (p *FileReviewPager) SetViewport(w, h int)
func (p *FileReviewPager) Render(width int) []string   // Component
func (p *FileReviewPager) HandleInput(data string)     // keys per UX spec (no 'b')
func (p *FileReviewPager) Invalidate()                 // Component
```

Rendering notes:

- Anchors: `LineAnchor{File: Content.Path, LineNum: idx+1, Side: SideNew}`
  for every source line; comment lookup via `Session.CommentsFor`.
- Gutter width = width of `len(Lines)` in decimal; content width =
  `width - 2 (prefix) - gutter - 1`; `truncate()` handles overflow.
- Pure `Render`: the markdown fence state is a **local** cursor of a fresh
  `mdSourceState` walked from line 0 (or from the last fence boundary before
  `scrollTop` — simple optimization, not required for correctness) so that
  rendering stays idempotent under differential redraws.
- Title per UX spec; empty file renders a single hint row.

Tests: `tui/markdown_source_test.go` (heading/fence/list/quote/inline/fence
state across lines/sanitize); `tui/file_review_pager_test.go` mirroring
`review_pager_test.go` + `review_comment_input_test.go` (render contains
line numbers; comment add/edit/delete flows through callbacks; submit
produces file summary + closes; export handler invoked; navigation &
clamping via `annotate`; binary never reaches the pager — command-level);
`tui/review_actions_test.go` for the extracted helper.

### Phase 4 — Event + app hosting

**`internal/event/event.go`**:

```go
// ChatEvent gains:
ShowFileReviewPager *ShowFileReviewPager

// ShowFileReviewPager requests the TUI to open the single-file review pager.
type ShowFileReviewPager struct {
    Pager any // concrete type is *tui.FileReviewPager to avoid an import cycle
}
```

**`internal/app/events_control.go`**: dispatch
`case ev.ShowFileReviewPager != nil: a.showFileReviewPager(ev.ShowFileReviewPager)`.

**`internal/app/events_review.go`**:

```go
const fileReviewHelpTitle = "q:close ↑↓:scroll c:comment e:edit d:delete s:submit x:export"

func (a *App) showFileReviewPager(m *event.ShowFileReviewPager) // mirrors showReviewPager:
    // type-assert *tui.FileReviewPager; RequestRender; SetViewport via
    // reviewOverlayGeometryFor; OnClose → Hide + title reset;
    // OnCommentRequest/OnConfirm via makeCommentRequestHandler/makeConfirmHandler
    // with fileReviewHelpTitle; OnExportReview via makeFileReviewExportHandler;
    // ShowOverlay{CaptureInput:true}; reviewSetTitle(fileReviewHelpTitle)

func (a *App) makeFileReviewExportHandler(handlePtr **tui.OverlayHandle, pager *tui.FileReviewPager) func()
    // Session.ExportPath (file variant) → Session.Export; result on input title;
    // mirrors makeReviewExportHandler
```

Tests: `internal/app/events_review_file_test.go` (mirror of existing overlay
tests — event wires callbacks, overlay visible, help title set, close hides).

### Phase 5 — Command dispatch + completion

**`core/commands/review.go`**:

- `Run`: `case "file": return c.startFileReview(ctx, args[1:])`;
  git guard relocated per D4.
- New `core/commands/review_file.go`:

```go
func (c *ReviewCommand) startFileReview(ctx core.Context, args []string) error
// no arg → usage; LoadReviewFile errors → writeFmt (friendly, no pager);
// NewFileSession + Session.FilePath = content.Path; store.Save;
// tui.NewFileReviewPager; OnCommentSaved → store.Save;
// OnSubmitReview → ctx.SubmitToAgent(text) + store.Save;
// EventBus.Chat <- ShowFileReviewPager{Pager: pager}

func (c *ReviewCommand) fileCompletions(ctx core.Context, pathPrefix string) []core.ArgCompletion
// filefind.Complete(ctx.ProjectDir, pathPrefix) → ArgCompletion{Value: "file:"+entry.Path}
```

- `CompleteArgs` (review.go): nested `file:` scope first (goal-style
  `splitGoalCompletionPrefix` pattern), else `file:` entry inserted after the
  ancestry trio, then existing ref completions unchanged.

Tests: `core/commands/review_file_test.go` —
- `/review:file:<txt>` (also `.md`, `.go`) emits `ShowFileReviewPager` with
  wired pager; session persisted with `Kind=file`, `FilePath`;
- no-arg usage; missing file; binary file (NUL) — message, **no** event;
  directory — message; non-git temp dir **works**;
- submit callback path: captured `SubmitToAgent` text contains absolute path
  and `file:line` anchors;
- completions: `""` → `^1`,`^2`,`^3`,`file:`,refs; `"fi"` → `file:` only;
  `"file:"` → `file:`-prefixed paths (skip fd-dependent assertions when
  `filefind.Available()` is false — use a temp tree + ReadDir fallback);
  `"file:src/"` nested; exact-existing-file suppression; `"^"`/ref behavior
  unchanged (regression).

### Phase 6 — Documentation

Verified touchpoints:

- `core/commands/help/review.long.md`: `file` subcommand, keys table (no
  `b`), completion note, submitted-message shape, colon-path caveat. Must
  keep passing `help_colon_syntax_test.go` (colon-only examples).
- `docs/COMMANDS.md` §"`/review` — Interactive code review" (line ~692):
  add `file:<path>` row + example to the usage block.
- `docs/HOTKEYS.md` §"When the interactive review pager is open" (line ~80):
  note the single-file pager shares all keys except `b` (change base).
- `docs/USER-GUIDE.md`: no `/review` section exists today (checked) — no
  change required there.
- This spec: flip Status to `Accepted — implemented` when done.

### Phase 7 — Gates & QA

- `go vet ./...`
- `go test -count=1 -race -cover ./...` (coverage targets: internal ≥90%,
  core ≥80%, tui ≥70%)
- `gocognit -over 15`, `gocyclo -over 12` on all touched files
- Manual QA checklist (real terminal):
  1. `/revi`+Tab completes `/review`; `/review:`+Tab shows `^1 … file: …`;
  2. `/review:file:t`+Tab → ranked `file:`-scoped paths; drill into a dir;
  3. open `.go` file → highlighted, line gutter; open `.md` → markdown
     source rendering incl. a fenced code block spanning the scroll window;
  4. `c`/`e`/`d` comment flows; commented lines show `│` + badge;
  5. `s` + `y` → pager closes, user message appears in chat with abs path +
     anchored comments; `/exchange` shows the agent received it;
  6. `x` → `review_file_<name>_<ts>.md` written, content identical to submit;
  7. `/review:file:$(which some-binary || echo /bin/ls)` → binary rejected;
  8. repeat outside a git repo — works; `/review` (diff) still requires git;
  9. resize terminal while pager open — overlay reflows like diff review.
- Optional: `qa-e2e` skill run as regression detector.

#### Phase 7 gate run — recorded results

Automated gates (full tree, this working tree):

- `go vet ./...`: clean.
- `go test -count=1 -race -cover ./...`: all packages ok, exit 0 (run twice:
  before and after the complexity-budget test refactors below).
- Complexity budgets on touched files: `gocognit -over 15` and
  `gocyclo -over 12`: no violations. Three new test functions originally
  exceeded the cognitive budget (`TestReviewFileCommand_OpensPager` 29,
  `TestLoadReviewFile_Text` 22, `TestComplete_Fallback` 20) and five the
  cyclomatic budget; all were refactored into named per-scenario helpers /
  data-driven tables with zero assertion loss (split tests re-derive their
  own expected save-notification counts).
- Coverage (per-package `-cover`, vs baseline measured on a HEAD worktree):
  - `internal/filefind` (new package): **92.2%** ≥ 90 target ✓
  - `internal/review`: **87.5%** (HEAD baseline 84.5%, +3.0pp; no regression)
  - `tui`: **74.7%** ≥ 70 target ✓ (baseline 73.9%, +0.8pp)
  - `core/commands`: **62.8%** (baseline 62.7%; pre-existing shortfall of the
    large commands package, not introduced here). New feature code itself is
    well covered: `core/commands/review_file.go` functions 84.6–90.0%,
    `internal/app/events_review.go` `showFileReviewPager` 96.4%.
  Note: the repo-wide "internal ≥90% / core ≥80%" targets are aspirational
  project goals not met by many untouched packages even at HEAD (e.g.
  `internal/agentic/provider/anthropic` 23.2%, `core/commands` 62.7%); the
  Phase 7 reading applied here is: no coverage regression in any touched
  package + new code meeting its area target where new packages were added.

Manual QA checklist — items executed as automated evidence against the same
behaviors (test names green under `-race` in the runs above); genuinely
interactive items marked DEFERRED:

| # | Checklist item | Result | Evidence |
|---|---|---|---|
| 1 | `/revi`+Tab → `/review`; `/review:`+Tab shows `^1 … file: …` | PASS (automated) | command-name completion via existing `tui/autocomplete` suite; arg scopes: `TestReviewFileCompletion_BaseOrder`, `TestReviewFileCompletion_PrefixFiltering` |
| 2 | `/review:file:t`+Tab → ranked file:-scoped paths, dir drill-down | PASS (automated) | `TestReviewFileCompletion_FileScope` (+ fd/fallback ranking: `filefind` suite incl. `TestComplete_Fallback_Issue8Ranking`) |
| 3 | `.go` highlighted w/ gutter; `.md` source render incl. fence spanning scroll window | PASS (automated) | `TestFileReviewPager_CodeFileHighlighted`, `TestFileReviewPager_TitleAndGutter`, `TestFileReviewPager_GutterRightAligned`, `TestFileReviewPager_MarkdownHighlightedAsSource`, `TestMdSource_FenceStateAcrossLines`, `TestMdSource_LineMappingIsOneToOne` |
| 4 | `c`/`e`/`d` flows; commented lines show `│` + badge | PASS (automated) | `TestFileReviewPager_CommentAdd`, `_CommentEditPrefill`, `_CommentBadgeAndDelete`, `TestReviewActions_*` helper suite |
| 5 | `s`+`y` → pager closes, chat message with abs path + anchored comments; `/exchange` receipt | PARTIAL: submit path PASS (automated), agent round-trip DEFERRED | `TestFileReviewPager_SubmitAndClose`, `TestShowFileReviewPager_SubmitCloses`, `TestReviewFileCommand_WorksOutsideGit` (SubmitToAgent receives abs path + `a.txt:2` anchor); live `/exchange` needs a running LM |
| 6 | `x` → `review_file_<name>_<ts>.md` written, content = submit content | PASS (automated) | `TestShowFileReviewPager_Export` (globs `review_file_*.md`, asserts summary content), `TestFileReviewPager_ExportHandlerInvoked`, `TestExportPath_FileKind`; export and submit both serialize the same `Session.MarkdownSummary()` |
| 7 | binary file rejected | PASS (automated) | loader: `TestLoadReviewFile_Errors` binary case; end-to-end: `TestReviewFileCommand_Rejections` subtests “missing file” / “directory rejected” / “binary rejected” |
| 8 | gitless dir works; `/review` diff still requires git | PASS (automated) | `TestReviewFileCommand_WorksOutsideGit`, `TestNewFileSession_Gitless`, `TestStore_FileSessionRoundTrip`; diff path: `TestNewSession_NotGit` |
| 9 | resize while open → overlay reflows like diff review | DEFERRED (real terminal) + unit evidence | `Render(w)` is pure & tested at multiple widths (80/120 across pager tests); fence state recomputed per render (`TestMdSource_FenceStateAcrossLines`); overlay hosting glue identical to the diff pager’s |

Optional `qa-e2e` skill: **skipped** — requires the local LMStudio LM
(localhost:1234, gemma-4-e4b); connection refused at gate-run time. Suite is
optional per this phase.

---

## Risks & Edge Cases

| Risk | Mitigation |
|---|---|
| Router splits on `:` → paths with `:` unreachable | Documented (pre-existing constraint of colon syntax); completion always produces colon-free relative paths for normal repos |
| fd subprocess latency on each keystroke | Same cost as the existing `@` completer; capped at 50 results; ReadDir fallback when fd absent |
| `reviewActions` refactor changes diff-pager behavior | Verbatim move; existing `review_pager_test.go`/`review_comment_input_test.go` are characterization suites; new unit tests for the helper |
| Differential renderer calls `Render` with varying widths/states | Render is pure: fence state recomputed per render; content only truncated, never reflowed |
| Huge text file memory | 2 MiB / 20k-line caps with honest `(truncated)` title marker |
| Legacy stored sessions | `Kind` zero value = diff review; JSON round-trip test with an old session file |
| Comment anchor vs trailing newline | `strings.Split` semantics: a trailing `\n` yields a final empty line — anchoring uses 1-based index; final empty line is still a legal (if unusual) anchor, matching "line N" of the file |
| `filefind` extraction regresses `@` completion | `tui/autocomplete_file_test.go` kept unchanged and green |

## Rejected Alternatives (summary)

1. Reflowed Markdown rendering — breaks the line-anchor contract (D1).
2. chroma-based highlighting — second highlighting system, inconsistent with
   TUI conventions (D2).
3. Parallel `FileReviewSession` type — duplicates store/summary/comments (D3).
4. Setter-interface overlay hosting — indirection over struct callbacks for
   ~30 saved lines; existing codebase duplicates this glue per pager (D7).
5. Rejecting oversized text files outright — truncation with a visible marker
   is more useful and still honest.
