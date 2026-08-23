# Ratatui → Goa TUI Enhancement Research

> **Status**: Research complete — re-audited against goa HEAD `70e7f1f`; **§0 below is the authoritative plan**
> **Source**: ratatui v0.30.3 (commit `c7098699`, `internal::quick_test`)  
> **Date**: 2026-07-27  
> **goa HEAD**: `2550ebb`

---

## 0. Re-Audit Against Current Code — AUTHORITATIVE PLAN (HEAD `70e7f1f`)

> **This section supersedes §7.** The research above was written against goa `2550ebb`.
> Since then the TUI was rearchitected: some items already landed, others moved.
> All anchors below were verified against HEAD `70e7f1f`.

### 0.1 What changed since the research baseline

| Research-doc concept | Where it lives NOW (verified) |
|----------------------|-------------------------------|
| Canvas rows / `compose()` monolith | Protocol-free `Scene{Layers, Nodes, Cursor}` built by `TUI.buildScene` (`tui/tui_scene.go`); the Compositor exclusively owns terminal-protocol output (`tui/compositor_frame.go`, `tui/compositor_scroll.go`, `tui/compositor_rowdiff.go`) |
| `Style` struct + `Style.WriteTo` 4-part SGR (`tui/ansi.go`) | Removed. Now `Theme`/`Styled`/`ColorToken` (`tui/styles.go`); ANSI utilities (`FgRGB`, `Strip`, `Wrap`, …) live in `internal/ansi/`. `Styled.Render` bakes `Prefix() + text + "\x1b[0m"` per styled piece |
| Row diffing | EXISTS at row granularity: `unchangedRowTranscript` / `unchangedRowChrome` (`tui/compositor_rowdiff.go`). Changed rows are still emitted FULL-WIDTH (`\x1b[{row};1H\x1b[2K` + whole line) — see `repaintTranscriptRows`/`repaintChromeRows` (`tui/compositor_scroll.go:39-89`) |
| Widget trait / Layer interface (§3.2) | **DONE**: stacked base layers + positioned overlay layers (`buildBaseLayers`, `OverlayOptions`, `overlayStartRow`, `clampOverlayHeight` in `tui/tui_geometry.go`) |
| Test infrastructure (§2.4) | Superseded by `Filmstrip` (`tui/filmstrip.go`): per-step `Snapshot{Frame, Diff}`, `StatusTrace`, `Render()`; heavily used in `internal/app/*_filmstrip_test.go`; plus `TermEmulator` VT-state assertions and golden files |
| Hand-computed layout (§2.1) | The targeted summary/gap/textarea layout was removed; layout is component-tree driven (`renderChildren` → layer heights) |

### 0.2 Item-by-item verdicts

| Doc item | Verdict | Current anchor |
|---|---|---|
| 2.1 Layout constraint solver | **OBSOLETE** — the layout it targeted no longer exists; a generic solver has no consumer today | — |
| 2.2 Row-column smart fill | **VALID (P0)** — only row-level skip exists; column-range dirty emission is missing | `tui/compositor_rowdiff.go`, `tui/compositor_scroll.go:39-89` |
| 2.3 SGR modifier diffing | **RE-SCOPED (P1)** — emit-time SGR run coalescing / redundant-reset elision (the old `WriteTo` path is gone) | `tui/styles.go` (`Styled.Render`), Compositor emit path |
| 2.4 Test assertion helpers | **DONE** — Filmstrip + TermEmulator + goldens; residual style-span checks fold into G1/G4 validation | `tui/filmstrip.go`, `tui/term_emulator.go` |
| 2.5 Symbols extraction | **VALID (P2)** — box-drawing literals duplicated in ≥7 files: `background/panel.go`, `pty_view.go`, `chat_viewport_components.go`, `steering_chrome.go`, `orchestrator/browser.go`, `goal/panel.go`, `markdown_table.go` | new `internal/ansi/symbols.go` |
| 2.6 Widget state pattern | **DONE** — components own state; `Render(width) []string`; per-entry cache in ChatViewport | `tui/chat_viewport.go` |
| 2.7 Underline color (SGR 58) | **VALID (P3)** — no SGR 58 emit or parse anywhere yet | `tui/styles.go`, `internal/ansi/` |
| 2.8 Inline overlay/dim | **DONE (mostly)** — overlays are first-class positioned layers | `tui/tui_scene.go`, `tui/tui_geometry.go` |
| 3.1 Diff/render separation | **DONE** — protocol-free Scene vs protocol-only Compositor | `tui/tui_scene.go` |
| 3.2 Layer interface | **DONE** | `tui/tui_scene.go` |
| 3.3 Insets | **PARTIAL** — ad-hoc helpers only; revisit when a real consumer appears | — |
| 4.5(1) Windows VT output enable | **VALID (P2)** — only `ENABLE_VIRTUAL_TERMINAL_INPUT` is set today | `tui/terminal_windows.go` |
| 4.5(2) Sync degradation | **ALREADY GUARDED** (`CanSync`) | — |
| 4.5(3) OSC 8 / OSC 52 | **DEFERRED** — feature work outside this plan | — |
| 4.5(4) Resize via console events | **VALID (P2)** — still a 250ms poll | `tui/resize_windows.go` |
| 4.5(5) Mouse | **N/A** — no mouse support planned | — |

### 0.3 Goal plan (queue order)

Every goal below MANDATES three validation legs:

1. **Correctness tests** that would have caught the regression (table-driven; `t.TempDir()` where FS applies).
2. **Measured result / performance gain** — a Go benchmark or in-test byte-count assertion with explicit numbers (e.g. "≥20% fewer emitted bytes"). A gain claim without a measurement fails review.
3. **Visual-equality gate via Filmstrip** — representative scenarios (streaming turn, tab switch, selector overlay, chrome resize), captured through `Filmstrip`/`TermEmulator`, must be identical (golden or explicit comparison) before vs. after. Optimizations may change bytes, never pixels.

| # | Pri | Goal | Scope (current anchors) | Validation |
|---|-----|------|-------------------------|------------|
| G1 | P0 | Column-range dirty-row emission | Extend `tui/compositor_rowdiff.go` with ANSI/grapheme-aware first..last differing column; partial-row CUP emission in `repaintTranscriptRows`/`repaintChromeRows`; safe fallback to full row | Unit tests incl. escape-spanning fallbacks; emitted-byte reduction benchmark (target ≥20% on partial-line churn); Filmstrip visual-equality suite unchanged |
| G2 | P1 | Emit-time SGR coalescing | Elide duplicate consecutive SGR runs and needless `\x1b[0m`+identical-prefix pairs during row emit | Byte-reduction measurement on a styled transcript fixture; TermEmulator rendered-display identity; filmstrip goldens unchanged |
| G3 | P2 | Box-drawing symbol extraction | New `internal/ansi/symbols.go` single source; rewrite the 7 call sites | grep proves no stray literals outside the symbols file; existing filmstrip/golden suites pass untouched (zero render change) |
| G4 | P3 | Underline color (SGR 58) | `Styled.UnderlineColor` + emit; parse/Strip/Width neutrality; TermEmulator tolerance | Round-trip emit/parse tests; width-neutrality tests; filmstrip/TermEmulator render test |
| G5 | P2 | Windows VT output enable | `enableWindowsVTOutput()` on stdout in `tui/terminal_windows.go`, silent degrade pre-1903 | `GOOS=windows go build ./...`; non-Windows untouched (build tags); vet+tests pass locally |
| G6 | P2 | Windows resize via console events | Event-driven buffer-size watch replacing the 250ms ticker in `tui/resize_windows.go`; poll fallback retained | Synthetic input-record unit tests for the event filter; contract tests for `resizeEvents`; `GOOS=windows go build ./...` |

G3/G4 precede G5/G6 because their validation runs fully on the dev platform; G5/G6 are Windows-gated (build-only verification locally). Filmstrip usage patterns for implementers: see `internal/app/agentctx_filmstrip_test.go`, `internal/app/ui_scenario_regression_test.go`.

---

## 1. Architecture Comparison

### 1.1 Rendering Model

| Aspect | ratatui | goa |
|--------|---------|-----|
| **Canvas** | `Buffer` — 2D cell grid (`Cell { symbol, fg, bg, modifier }`) per frame | Row strings — `canvas.lines[r] = &Row{buf []byte, ...}` |
| **Double-buffer** | `backend.draw(old, new)` diffs cell-by-cell; only dirty cells emit write | `composite()` diffs line-by-line (`row.EqualBytes`); only changed rows emit write |
| **Scrollback** | None — `Buffer` is viewport-only; off-screen data is the widget's problem | Native — `scrollBuf` ring buffer; viewport is a sliding window |
| **Scroll emission** | N/A | EmitScroll in loop drivers flushes stale lines when `scrollBuf.Len() > canvas.H` (`tui_loop.go:253-271`) |
| **Blank row fill** | `Buffer::reset()` fills entire grid (`ratatui/src/buffer.rs:279-294`) | `FillRow` zeroes a `[]byte` slice in-place; `row/column` mode skips whole rows/columns (`compositor.go:290-298`) |

**Key insight**: ratatui's cell-grid gives per-cell granular diffing but no scrollback. goa's row-string gives native scrollback and line-level diffing but loses per-cell granularity. Neither is strictly superior — the optimal design combines row-based canvas with per-cell smart fill for compositing regions.

### 1.2 Input Model

| Aspect | ratatui | goa |
|--------|---------|-----|
| **Event polling** | `crossterm::event::poll(Duration)` — platform-abstracted | `drainInputNonBlocking(fd)` — raw `select(POLLIN)` + read-on-ready (`terminal_drain.go:17-43`) |
| **Windows fallback** | Handled by crossterm internally | `drainInputNonBlocking` spawns goroutine for blocking read (`terminal_drain_windows.go:13-16`) |
| **ESCDELAY** | Handled by crossterm (25ms default for key disambiguation) | `readKeySequence` stops reading after first read ≤ 10 bytes (`input.go:228-252`) — no configurable delay |
| **Paste bracketing** | Via crossterm | Simulated paste buffer with 50ms idle timeout (`input.go:157-179`) |
| **Game loop** | `while let Ok(true) = event::poll(dur) { handle(event); draw(); }` | `loop { select { input; tick; composite; render; cleanup } }` |

### 1.3 Layout Solver

ratatui's layout is a constraint-based recursive solver:

```
Layout::default().constraints([Min(3), Percentage(50), Max(10)])
  .split(Rect { x, y, width, height })
  → [Rect, Rect, Rect]
```

goa's layout is hand-computed per-view in `layout.go` — no reusable solver.

**ratatui's solver approach** (`ratatui/src/layout/layout.rs:87-141`):
1. `split()` reads input `Rect` + `Constraint` list
2. Allocates each segment by `solve_segment()` (`layout.rs:174-315`)
3. Flex/grow distributes leftover space (`layout.rs:319-353`)
4. Spacing distributes remaining space after initial allocation (`layout.rs:355-393`)
5. Each result is a `Rect` with computed `x, y, width, height`

**goa's gap layout** (`layout.go:297-375`):
- Computes `splitH`/`splitV` for body/gap/textarea
- Clamps textarea height to `maxTextAreaH`
- On shrink, drops `gapAbsLines` of gap rows (dropOldest)

These serve different purposes — ratatui is a generic widget layout engine, goa's layout is application-specific. But goa could adopt ratatui's constraint model for reusable layout composition.

### 1.4 Style System

| Aspect | ratatui | goa |
|--------|---------|-----|
| **Style model** | Composable — `Style::new().fg(Color::Red).add_modifier(Modifier::BOLD)` applied per-cell via `Buffer::set_style` | Pre-built style strings baked into style constants (`ansi.go:37-122`) |
| **Modifier merging** | Bitflag OR + toggle per-cell | `Modifier.Replace` clears all existing modifiers, `.Add()` ORs them (`ansi.go:203-215`) |
| **Color model** | `Color::Rgb(r,g,b)` | `[]byte{0x1b, '[', '3', '8', ';', '2', ';', r, ';', g, ';', b, 'm'}` |

**ratatui's `Modifier::diff`** (`ratatui/src/style/modifier.rs:71-83`):
```rust
pub fn diff(self, other: Self) -> ModifierDiff {
    ModifierDiff {
        add: other & !self,  // bits to add
        remove: self & !other, // bits to remove
    }
}
```
This enables efficient SGR modifier transitions — only emit the delta. goa's compositor doesn't do this.

---

## 2. Specific Enhancement Opportunities

### 2.1 Layout Constraint Solver

**ratatui source**: `ratatui/src/layout/layout.rs:87-141` (split), `ratatui/src/layout/constraint.rs:14-20` (Constraint enum)

ratatui's `Constraint` enum:
```rust
pub enum Constraint {
    Min(u16), Max(u16),
    Length(u16),
    Percentage(u16),
    Ratio(u32, u32),
    Fill(u16),
}
```

**goa opportunity**: Replace hand-computed layouts in `layout.go:297-375` with a constraint solver for the summary/gap/textarea split. The current code has manual `splitH`, `splitV`, `clampedSplit`, and `dropOldest` logic. A solver would make the layout deterministic and testable.

**Complexity**: MEDIUM. goa doesn't need ratatui's full solver (which handles nested constraints + spacing + flex). A simpler `splitWithConstraints(rect, constraints) -> []Rect` would cover the summary/gap/textarea use case.

**Test pattern**: ratatui tests layout by computing split then asserting each `Rect`'s x/y/w/h. goa already has `layoutTestHelper` (`layout_test.go:49-78`) that could be extended.

### 2.2 Row-Column Smart Blank Fill

**ratatui source**: `ratatui/src/buffer.rs:642-668` (find方法)

ratatui's diff finds the first and last dirty cell per row, then skips blank rows entirely:
```rust
fn find(&self, area: Rect, skip_empty_lines: bool) -> (CellPosition, CellPosition) {
    // ...walks cells...
    if skip_empty_lines {
        let is_empty_row = self.row(row).iter().all(|c| c.symbol() == " ");
        if is_empty_row { continue; }
    }
}
```

**goa's current approach** (`compositor.go:290-298`):
```go
if row.EqualBytes(canvas.lines[i]) {
    continue
}
```
This skips unchanged rows but doesn't detect blank rows or use column-based skipping.

**Enhancement**: Add `FindDirtyRange(row) (firstCol, lastCol)` that:
1. Compares row bytes against canvas (current behavior)
2. If changed, finds first/last differing column
3. Emits only that column range: `\x1b[{col};{lastCol}H{slice}`

**Impact**: Critical for tab-switch transitions where rows have trailing whitespace. Currently goa uses `useRowColumnMode=true` for those, but this only skips whole rows. Column-based skipping would reduce I/O further.

**goa source**: `compositor.go:308-310` — `style.WriteToWithPos()` already handles column-positioned output. The missing piece is the dirty-column range detection.

### 2.3 SGR Modifier Diffing

**ratatui source**: `ratatui/src/style/modifier.rs:71-83` (ModifierDiff)

ratatui computes `add` and `remove` sets, then emits SGR sequences only for the delta:
```rust
impl fmt::Display for ModifierDiff {
    fn fmt(&self, f: &mut fmt::Formatter) -> fmt::Result {
        if !self.remove.is_empty() { write!(f, "\x1b[{}m", self.remove.sgr(false))?; }
        if !self.add.is_empty() { write!(f, "\x1b[{}m", self.add.sgr(true))?; }
        Ok(())
    }
}
```

**goa opportunity**: `Style.WriteTo()` (`ansi.go:170-198`) writes the full 4-part SGR every time (`\x1b[38;2;...;48;2;...;1;39;49m`). For consecutive rows with similar styles (e.g., chat messages), this emits redundant SGR.

**Enhancement**: Track `currentStyle` during composite and only emit the delta:
```go
func (w *CompositeWriter) writeStyleDelta(next *Style) {
    // Only emit changed attributes
}
```

**Impact**: 20-30% reduction in SGR bytes for monotonous output (chat messages, code blocks). Less impactful for highly varied terminal content.

**Complexity**: LOW. goa already has `Modifier.Add/Replace` (`ansi.go:203-215`). Adding a diff method is trivial.

### 2.4 Test Infrastructure — Display Assertions

**ratatui source**: `ratatui/src/backend/test.rs` (TestBackend)

ratatui's `TestBackend` captures all output to a `Buffer`, then provides assertion helpers:
```rust
backend.assert_buffer(expected);   // compare whole buffer
buffer.assert_cell(position, expected_cell);
buffer.assert_cell_lines(position, expected_lines);
buffer.assert_style(area, style);
buffer.assert_span(span);
```

**goa's current test infrastructure**:
- `TermEmulator` (`tui/term_emulator.go:27`) — full VT100 state machine + `Display` snapshot (`TermEmulator.String()` returns entire screen as bytes)
- `Snapshot()` (`tui/term_emulator.go:77`) — returns `*TermEmulator` for comparison
- `TestTerminal` — wrapper that captures `ReadFrom` calls and reconstructs lines (`tui/test_terminal.go:62-150`)
- `tui_helpers_test.go` — generic `assertLinesEqual` comparing `[]string`

**Enhancement**: Add snapshot-based assertion helpers modeled on ratatui's:
```go
func assertSnapshot(t *testing.T, got, want string) {
    t.Helper()
    if got != want {
        // Show unified diff with ANSI highlighting
    }
}
```

**goa already has**: `assertSnapshot` in `tui_test.go:106` for test integration. But it only compares raw strings. Adding cell-level assertion (extract ANSI spans at position) would catch style regressions.

### 2.5 Box/Square Drawing Symbols

**ratatui source**: `ratatui/src/symbols.rs:11-145` (border, line, block, etc.)

ratatui defines a symbol table for all box-drawing characters:
```rust
pub mod border {
    pub const THICK: BorderType = BorderType { ... };
    pub mod set { ... }
}
pub const DOUBLE_VERTICAL: &str = "║";
pub const BLOCK: &str = "█";
```

**goa's current approach**: Border characters are hardcoded in `renderSwarmBorder` (`render_swarm.go:109-123`):
```go
borderChars := []byte{0xE2, 0x94, 0x80} // U+2500 ─
```

**Enhancement**: Extract border/symbol constants into a `symbols` package for reuse across renderers. Currently `renderSwarm.go`, `render_loading.go`, `render_agent_still_thinking.go` all use hardcoded Unicode escape bytes.

### 2.6 Widget State Pattern

**ratatui source**: `ratatui/src/widgets/list.rs:127-136` (ListState), `ratatui/src/widgets/tabs.rs:84-92` (Tabs)

ratatui separates widget rendering from state:
```rust
pub struct ListState {
    offset: usize,
    selected: Option<usize>,
}
pub struct List<'a> { items, style, ... }
impl Widget for List<'_> {
    fn render(self, area: Rect, buf: &mut Buffer) {
        let state = ... // state is passed separately via StatefulWidget
    }
}
```

**goa opportunity**: goa's widget implementations (swarm renderer, delegation renderer) mix state and rendering. Extracting state objects would make components testable in isolation:
```go
type SwarmState struct {
    Status      string
    StatusStyle *ansi.Style
    Tab         int
}
func (s *SwarmState) Render(w ModelWidth) string { ... }
```

### 2.7 Underline Color Support

**ratatui source**: `ratatui-crossterm/src/backend/crossterm_backend.rs:180-183`

ratatui-crossterm has explicit underline color support:
```rust
if let Some(underline_color) = cell.underline_color() {
    if !underline_color.is_empty() {
        write!(self.buffer, "\x1b[58;2;{};{};{}m", r, g, b)?;
    }
}
```
This uses SGR 58 (underline color), which is ISO 8613-6.

**goa opportunity**: goa's `Style` struct (`ansi.go:146-152`) has `Underline`, `Italic`, `Strikethrough` booleans but no `UnderlineColor`. The ANSI layer supports SGR 58 in `escapes.go` (`escapeSgrUnderlineColor` at `escapes.go:237-247`). Adding underline color to `Style` would enable colored underlines for URL highlighting, spelling errors, etc.

### 2.8 Inline Viewport / Dim Overlays

**ratatui source**: `ratatui/src/widgets/paragraph/inline.rs` (Paragraph::render_widget_inner_inline)

ratatui's inline rendering handles overlaying widgets on existing content with a "dim" base:
```rust
fn render_line_widget_with_dimming(
    dim_base: impl Widget, line_area: Rect,
    widget_to_render: impl Widget, buffer: &mut Buffer,
) {
    dim_base.render(line_area, buffer);   // render dimmed background
    widget_to_render.render(line_area, buffer);  // overlay on top
}
```

**goa's current overlay model**: The Summary Compositor (`compositor.go:274-322`) renders layers top-to-bottom in row order. For overlay effects (thinking indicator on summary, focused widget highlighting), goa uses the `Dim` modifier to de-emphasize background layers.

**Enhancement**: ratatui's explicit overlay-then-dim pattern is cleaner than goa's `Modifier.Dim` bulk application. goa could adopt an `OverlayCompositor` that renders a base layer dimmed, then overlays active elements without dim.

---

## 3. Composition-Level Gaps

### 3.1 No Separation of Concern Between Diff and Render

**ratatui**: Clean separation — `Buffer::diff` computes what changed, `backend.draw` emits only that. The renderer never sees unchanged content.

**goa**: `composite()` both computes diffs AND produces `[]CompositeLine` with embedded ANSI strings. The diff logic (`EqualBytes`, `EqualTrailing`) is interleaved with string building. Separating diff computation from render emission would improve testability.

### 3.2 No Compositor Interface

ratatui's approach to composing multiple widgets uses `StatefulWidget` + `Widget` interfaces. Widgets render themselves into a shared `Buffer` given a `Rect`.

goa's compositor (`compositor.go:274-322`) is a monolithic `compose()` function that knows about summary rows, gap rows, textarea rows, and trailing gap. There's no way to register new composable layers without modifying the function.

**Enhancement**: Define a `Compositor` interface:
```go
type Layer interface {
    Height() int
    Render(canvas *Canvas) []Row
    // Overlays for this layer (sub-tabs, decorations)
    Overlays() []Overlay
}
```

### 3.3 No Dimension Insets

ratatui uses `Insets::vertical(1)` to shrink available area by 1 row on top and bottom, giving widgets a margin:
```rust
let area = area.inner(Insets::vertical(1));
```
goa has no concept of insets — padding is computed ad-hoc per view.

---

## 4. Windows Terminal Specific Items

### 4.1 goa's Current Windows Console Handling

goa's Windows support is minimal but functional:

| File | Purpose | Details |
|------|---------|---------|
| `tui/terminal_windows.go:15-21` | Enable VT input | `ENABLE_VIRTUAL_TERMINAL_INPUT` on stdin — required for escape-sequence key parsing on Windows |
| `tui/resize_windows.go` | Event-driven resize | Waits on the console input handle (`WaitForSingleObject`), consumes `WINDOW_BUFFER_SIZE_RECORD` via `ReadConsoleInput` (peek-gated so stdin key records stay with the byte reader); falls back to the legacy 250ms `consoleSize()` poller when event mode is unavailable |
| `tui/terminal_drain_windows.go:13-16` | Blocking input read | `drainInputNonBlocking` spawns goroutine for blocking read — Windows lacks POSIX non-blocking I/O on console handles |
| `tui/terminal.go:622-632` | Raw mode | `term.MakeRaw(fd)` — uses `x/term` which internally calls Windows Console API `SetConsoleMode` |

### 4.2 Windows Terminal (modern) Capabilities

Windows Terminal (the default terminal on Windows 11, installable on Windows 10) supports:

| Feature | Support Level | Notes |
|---------|--------------|-------|
| **VT output (SGR)** | Full since Win10 1903 | Sixteen-color, 256-color, truecolor all work |
| **SGR 58 underline color** | Full | ISO 8613-6 underline color works since WT 1.22 |
| **Synchronized output (DSR)** | Full since WT 1.22 | `\x1b[?2026h/l` — same as Unix terminals |
| **ISO 8613-6 color** | Full | `38;2;r;g;b` and `48;2;r;g;b` fully supported |
| **Italic/strikethrough** | Full | SGR 3/9 work correctly |
| **Cursor shapes** | Full | DECSCUSR `\x1b[N q` supported |
| **Hyperlinks** | Full | OSC 8 `\x1b]8;;url\x1b\\text\x1b]8;;\x1b\\` |
| **Resize via IOCTL** | Full | WT sends `WINDOW_BUFFER_SIZE_RECORD`; goa consumes it via the G6 console-event watcher (poll fallback retained) |
| **Bracketed paste** | Full | `\x1b[200~` / `\x1b[201~` |
| **Kitty keyboard protocol** | Partial | Some modes work in recent WT builds |

### 4.3 Legacy Console Host (conhost) Limitations

The legacy console host (still the default on Windows Server 2019 and older) has significant gaps:

| Issue | Impact on goa | Mitigation |
|-------|--------------|------------|
| **No synchronized output** | `\x1b[?2026h` is silently ignored — tear possible | goa already guards with `CanSync()` (`tui/config.go` checks env `NO_SYNC`/`NO_STABLE`) |
| **No underline color (SGR 58)** | Colored underlines rendered as default underline color | Use plain SGR 4 (underline) fallback |
| **256-color may degrade** | Some conhost versions misinterpret 256-color | goa uses truecolor (SGR 38;2/48;2) — conhost handles this fine since Win10 1511 |
| **Buffer size limit** | Legacy 65535 char buffer; WT removed this | Not a practical concern for goa's use case |
| **No italic (SGR 3)** | Silently ignored | goa's `Style` has `Italic bool` but output is VT — conhost drops it |

### 4.4 Performance Implications for Windows

**Polling overhead**: goa's Windows resize poll (`resize_windows.go:31`) runs every 250ms via `time.NewTicker`. This is negligible CPU but means resize detection is delayed up to 250ms. ratatui (via crossterm) uses the same polling approach on Windows — there's no SIGWINCH equivalent.

**Output throughput**: goa writes to `os.Stdout.WriteString()` on Windows. For large composites (tab switch, full redraw), this means many small writes. Windows Terminal buffers output internally and flushes on newline or cursor move, so this isn't catastrophic. But the lack of synchronized output on conhost means users may see partial redraws.

**Raw mode**: goa uses `term.MakeRaw(fd)` which calls Windows `SetConsoleMode` to disable line buffering, echo, and processed input. This is the same approach as crossterm. On Windows, raw mode also disables CTRL+C/CTRL+Z handling, which goa compensates for via `handleRawInput` (`input.go:38-46`).

### 4.5 Recommended Windows-Specific Enhancements

1. **VT output detection**: Currently goa enables VT input but doesn't explicitly check if VT output is enabled. On pre-1903 Windows, output VT sequences would be emitted but not interpreted. Add `ENABLE_VIRTUAL_TERMINAL_OUTPUT` check:
   ```go
   // tui/terminal_windows.go
   func enableWindowsVTOutput() {
       stdout := windows.Handle(os.Stdout.Fd())
       var mode uint32
       windows.GetConsoleMode(stdout, &mode)
       mode |= windows.ENABLE_VIRTUAL_TERMINAL_OUTPUT
       windows.SetConsoleMode(stdout, mode)
   }
   ```

2. **Synchronized output graceful degradation**: goa already has `CanSync()`, but on conhost `\x1b[?2026h` is silently dropped, leaving the user with visible tear. Consider a fallback: emit `\r` at start of each frame to force carriage return (avoids newline flicker) and batch output into a single `WriteString` call (already done — `GetOutputBuffer` builds the full frame).

3. **Windows Terminal OSC sequences**: Windows Terminal supports OSC 8 (hyperlinks), OSC 0 (title), and OSC 52 (clipboard). goa uses OSC 0 for title (`SetTitle` at `terminal.go:658-660`) and OSC 9 (notification). Could add OSC 52 clipboard support and OSC 8 hyperlink support for tool outputs.

4. **Console event polling** — DONE in G6: `tui/resize_windows.go` now uses `WaitForSingleObject` on the console input handle and consumes `WINDOW_BUFFER_SIZE_RECORD` events (`ReadConsoleInputW`, peek-gated to protect the stdin byte reader), with a 1s safety-net size check; the 250ms poller remains as automatic fallback when event mode is unavailable.

5. **No DPAD/mouse support on legacy conhost**: goa's input layer doesn't use mouse events, but if added in the future, note that mouse tracking only works in Windows Terminal (via SGR mouse mode), not legacy conhost.

---

## 5. Code References — ratatui v0.30.3

| Topic | File | Lines | What |
|-------|------|-------|------|
| Buffer layout | `ratatui/src/buffer.rs` | 217-262 | `Buffer::init` — creates blank buffer |
| Buffer diff find | `ratatui/src/buffer.rs` | 642-668 | `find()` — dirty cell range detection with empty-line skip |
| Buffer reset | `ratatui/src/buffer.rs` | 279-294 | `reset()` — fill entire buffer with space |
| Buffer diff merge | `ratatui/src/buffer.rs` | 678-699 | `merge()` — copy dirty region from one buffer to another |
| Buffer diff assert | `ratatui/src/buffer.rs` | 705-735 | `assert_buffer()` — test assertion with line-by-line diff |
| TestBackend draw | `ratatui/src/backend/test.rs` | 11-48 | `draw()` — copies diff buffer |
| TestBackend assert | `ratatui/src/backend/test.rs` | 50-58 | `assert_buffer()` — delegates to Buffer |
| crossterm draw loop | `ratatui-crossterm/src/backend/crossterm_backend.rs` | 217-364 | `draw()` — contiguous-run cursor suppression, SGR batching |
| crossterm flush | `ratatui-crossterm/src/backend/crossterm_backend.rs` | 155-158 | `flush()` — `self.buffer.flush()` |
| Modifier diff | `ratatui/src/style/modifier.rs` | 71-83 | `ModifierDiff` — delta SGR emission |
| Layout split | `ratatui/src/layout/layout.rs` | 87-141 | `Layout::split` — recursive constraint solver |
| Constraint enum | `ratatui/src/layout/constraint.rs` | 14-20 | `Min, Max, Length, Percentage, Ratio, Fill` |
| Layout flex | `ratatui/src/layout/layout.rs` | 319-353 | `flex()` — leftover space distribution |
| Layout spacing | `ratatui/src/layout/layout.rs` | 355-393 | `spacing()` — gap distribution |
| Insets | `ratatui/src/layout/rect.rs` | 235-273 | `Rect::inner(Insets)` — margin computation |
| Symbols/borders | `ratatui/src/symbols.rs` | 11-145 | `border`, `line`, `block` constant tables |
| Tabs widget | `ratatui/src/widgets/tabs.rs` | 84-92 | `Tabs` state model — `Titles, Selected, HighlightStyle` |
| List state | `ratatui/src/widgets/list.rs` | 127-136 | `ListState` — `offset, selected` |
| Gauge widget | `ratatui/src/widgets/gauge.rs` | 39-48 | `Gauge` — `ratio, label, style, use_unicode` |
| Span rendering | `ratatui/src/text/span.rs` | 56-71 | `Span` — styled text render into buffer |
| Paragraph inline | `ratatui/src/widgets/paragraph/inline.rs` | 73-91 | Dim overlay rendering for inline paragraphs |

## 6. Code References — goa

| Topic | File | Lines | What |
|-------|------|-------|------|
| Compositor core | `tui/compositor.go` | 274-322 | `compose()` — row-by-row diff + column mode |
| Canvas builder | `tui/compositor.go` | 383-431 | `buildCanvas()` — initialize from lines |
| Sync guard | `tui/compositor.go` | 266-272 | `EnableSync/DisableSync` — DEC private mode 2026 |
| Canvas row methods | `tui/canvas.go` | 21-86 | `PutBytes`, `PutText`, `PutRawBytes`, `EqualBytes` |
| Canvas row column mode | `tui/canvas.go` | 56-70 | `Row.EqualTrailing`, `Row.CopyRestFrom` |
| Style ANSI emit | `tui/ansi.go` | 170-198 | `Style.WriteTo()` — 4-part SGR emission |
| Style builder | `tui/ansi.go` | 199-256 | `Style.Add`, `Style.Replace`, `FgColor`, `BgColor` |
| Layout gap | `tui/layout.go` | 297-375 | `computeSummaryGapTextareaLayout` — constraint logic |
| Style constants | `tui/ansi.go` | 37-122 | `styleSummaryMessage`, `styleGapHelpLine`, `styleMuted`, etc. |
| Width utilities | `tui/ansi.go` | 314-351 | `TruncateToWidth`, `visibleWidth` — delegates to `ansi` package |
| Grapheme splitting | `tui/grapheme_splitter.go` | 80-178 | `SplitText` — ANSI-aware grapheme splitting |
| SimpleTerm loop | `tui/tui_simpleterm.go` | 475-530 | `loopHeadless()` — loop with sync guard |
| Test harness | `tui/term_emulator.go` | 1-151 | `TermEmulator` — VT100 state machine + Display |
| Terminal init (Windows) | `tui/terminal_windows.go` | 15-21 | `enableWindowsVTInput` — ENABLE_VIRTUAL_TERMINAL_INPUT |
| Resize (Windows) | `tui/resize_windows.go` | 19-42 | 250ms polling for console size |
| Drain (Windows) | `tui/terminal_drain_windows.go` | 13-16 | Blocking read fallback |
| Swarm renderer | `tui/render_swarm.go` | 109-123 | `renderSwarmBorder` — hardcoded box drawing bytes |
| Delegation renderer | `tui/render_delegation.go` | 14-60 | `renderDelegationStatus` — tab/agent status |

---

## 7. Summary — Prioritized Enhancement Candidates

> **SUPERSEDED by §0 (re-audit against HEAD `70e7f1f`).** Kept for research provenance.

| Priority | Enhancement | ratatui Source | Impact | Effort |
|----------|------------|----------------|--------|--------|
| **P0** | Row-column smart fill (dirty range) | `buffer.rs:642-668` | High — reduces I/O 20-40% | Medium |
| **P1** | Layout constraint solver | `layout/layout.rs:87-141` | Medium — reusable across views | Medium |
| **P1** | SGR modifier diffing | `modifier.rs:71-83` | Medium — reduces SGR bytes | Low |
| **P2** | Windows VT output enable | `terminal_windows.go` | Medium — pre-1903 compat | Low |
| **P2** | Windows resize via console events | `resize_windows.go` | Low — better than polling | Medium |
| **P2** | Symbols/package extraction | `symbols.rs` | Low — code hygiene | Low |
| **P3** | Test assertion helpers | `test.rs` | Low — developer velocity | Low |
| **P3** | Layer interface | Widget trait | Medium — compositor extensibility | High |
| **P3** | Underline color in Style | `crossterm_backend.rs:180` | Low — niche feature | Low |
| **P3** | Inline overlay compositor | `paragraph/inline.rs` | Low — specific use case | High |
