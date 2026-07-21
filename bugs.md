<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Bug/Feature Tracking

## Guideline

1. Create a detailed fix plan for each bug/new feature - the plan must contain test approach and validation steps - execute the plan and validate the fix when all elements are in place.
2. Any issues found, even if not related to the bug/feature, must be fixed and the fix plan must be updated accordingly. You can add new items to the bug list as you find them.
3. Each item should be moved to archive when tested and closed as the associated plan.
5. Use filmstrip approach to validate the output of the tool - you must verify the actual terminal output.
6. Check code quality with each tool run separately (do not chain them with `;` or `&&`):
   - `go vet ./...`
   - `staticcheck ./...`
   - `gocognit -over 15 .`
   - `gocyclo -over 12 .`
   - `go test -count=1 -race -cover ./...`
   Fix any new issues introduced by the change. Pre-existing warnings are acceptable only if they are unrelated to the change and explicitly noted.

*At the end of the session*: the list should be empty and this file should only contain the guidelines for bug reporting.
If new items are added, restart the process.

## Workflow for bugs
1. Reproduce the failure before editing — ideally a command or script that triggers it on demand.
2. State the observed failure exactly (command + output).
3. Localize to the smallest region — ideally the specific lines — before editing. Precise localization is the strongest predictor of a correct fix.
4. Change one hypothesis at a time.
5. Prefer the minimal fix over a broad refactor.
6. Verify against the original failing command before declaring done.
7. Run the code-quality checks from guideline #6 separately and confirm the fix does not introduce new violations.
8. Move the bug list to `docs/archive/bugs.<fixdate>.md` when all items are closed.

# Open TODO
## Spinner: hexagon spinner as default

Add a hexagon spinner animation (looping, slow) and make it the default spinner.

Frames:
```
⬡⬢⬣⬢
```

### Fix plan
1. Add `"hexagon": {"interval": ~400ms, "frames": ["⬡","⬢","⬣","⬢"]}` to `internal/spinner/spinners.json`.
2. Change `spinner.Default()` (internal/spinner/spinner.go) to prefer `"hexagon"` (currently prefers `"arc"`).
3. Tests: exact frames/interval (mirror TestRequestedSpinners); `Default()` returns hexagon; `Names()` includes hexagon.
4. Validate: go vet, staticcheck, gocognit -over 15, gocyclo -over 12, go test -count=1 -race -cover (separately).

## Title bar startup sequence

Set the terminal window title as early as possible to `g⬡a`. When the startup
sequence is done — explicit hook after async plugin/history load completes
(5s timer as fallback only) — switch to the actual title `⬡`.

Transition animation (1s frame rate):
```
g⬡a ==> g⬡ => ⬡
```

### Fix plan
1. Early: in internal/app/tui.go around engine.Start(), call engine.SetTitle("g⬡a") (OSC 0 via ProcessTerminal.SetTitle, already implemented).
2. Startup-done hook: after async plugin load + background history load complete, trigger the transition g⬡a → g⬡ → ⬡ at 1s/frame; 5s fallback timer if the hook never fires.
3. Interactive-TUI only (skip headless/tests).
4. Tests: fake terminal capturing SetTitle calls; verify early title + ordered transition frames; config/default behavior.
5. Validate: guideline #6 gates separately.

## Animated title bar while working

Animate the title bar with the spinner animation while goa is working;
configurable; default to the hexagon spinner.

### Fix plan
1. Hook agent state changes (working → animate title with spinner frames; idle → static ⬡).
2. Config: TUI.AnimatedTitle (default on); spinner source cfg.TUI.Spinner (default hexagon).
3. Coordinate with the startup sequence (item above) so there is a single title writer (startup hands off to the working/idle animator).
4. Tests: state transitions drive SetTitle frames; config disables animation.
5. Validate: guideline #6 gates separately.

# Open TODO
## Spinner: hexagon spinner as default

**Source:** ideas.md (2026-07-21). Use the hexagon spinner (looping, slow) as the default spinner:
```
⬡⬢⬣⬢
```

### Fix plan
1. Add a `hexagon` definition to `internal/spinner/spinners.json`: frames `["⬡","⬢","⬣","⬢"]`, slow interval (~400ms).
2. Change `spinner.Default()` to prefer `hexagon` (fall back to `arc`, then any).
3. Tests: frames/interval exact-match (mirror `TestRequestedSpinners`); `Default()` returns hexagon.

## Title bar: startup sequence

**Source:** ideas.md (2026-07-21). Set the terminal title as early as possible to `g⬡a`; when the startup sequence is done — explicit hook after async plugin/history load completes (decided 2026-07-21), with a 5s fallback timer — transition to the final title `⬡` via a slow animation (1s frame rate): `g⬡a → g⬡ → ⬡`.

### Fix plan
1. `internal/app/tui.go`: `engine.SetTitle("g⬡a")` before/around `engine.Start()` (interactive TUI only — skip headless/tests).
2. Add an explicit startup-done hook fired after async plugin + history load completes; on fire (or 5s fallback, whichever first), animate `g⬡a → g⬡ → ⬡` at 1s/frame, then hand the title over to the animated-title controller.
3. Tests: fake terminal captures SetTitle sequence; startup-done hook fires exactly once; fallback timer fires when hook never called.

## Title bar: animated while working

**Source:** ideas.md (2026-07-21). Animate the terminal title with the spinner animation while goa is working; configurable (default on), spinner from `tui.spinner` config (default hexagon).

### Fix plan
1. Title animator owned by the app layer (single writer; startup sequence hands off to it).
2. Hook agent state transitions (working → animate with configured spinner frames at its interval; idle → static `⬡`).
3. Config: `tui.animated_title` (default true) — reuses `tui.spinner` for the frame set.
4. Tests: working→idle transitions drive SetTitle with spinner frames then static title; config off disables animation.

# Open TODO
## Hexagon spinner as default
Use the hexagon spinner (looping, slow) as the default spinner:
```
⬡⬢⬣⬢
```
- Root: `internal/spinner/spinners.json` + `spinner.Default()` (currently prefers
  "arc"). `initSpinner` (internal/app/tui.go:405) uses the default when
  `cfg.TUI.Spinner` is empty.
- Plan: add a "hexagon" definition (frames ⬡⬢⬣⬢, slow interval ~400ms) to
  spinners.json; make `Default()` return hexagon. Keep "arc" available by name.
- Tests: hexagon frames/interval exact (mirror TestRequestedSpinners);
  Default() returns hexagon; Get("arc") still works.

## Title bar startup sequence
Set the terminal title as early as possible to `g⬡a`; when the startup
sequence completes (explicit hook after async plugin/history load — 5s timer
as fallback only), animate the transition at 1s frame rate:
```
g⬡a ==> g⬡ => ⬡
```
- Root: `TUI.SetTitle` (tui/tui.go:172) writes OSC 0 via
  `ProcessTerminal.SetTitle` (tui/terminal.go:518). Startup points:
  `internal/app/tui.go:44` (engine.Start), `internal/app/app.go:166`
  (engine.RunLoops), async plugin load (internal/app/plugins.go
  startAsyncPluginLoad / activatePluginUI).
- Plan: set title `g⬡a` before engine.Start; on the startup-done hook (or 5s
  fallback) run a 1s/frame animator `g⬡a → g⬡ → ⬡` on a dedicated goroutine,
  single title writer.
- Tests: fake terminal captures SetTitle calls; sequence ordering + final
  title; fallback fires when hook never signals.

## Animated title bar while working
Animate the title bar with the spinner animation while goa is working;
configurable; defaults to the hexagon spinner.
- Root: agent working/idle state transitions surface as state events consumed
  in the app layer (status/footer already react). Config: new
  `cfg.TUI.AnimatedTitle` (default true) + reuse `cfg.TUI.Spinner` (default
  hexagon).
- Plan: title animator owned by the app layer: on working → spin title frames
  with the configured spinner at its interval; on idle → static `⬡`. Single
  writer coordinates with the startup sequence (startup handoff → animator).
- Tests: fake terminal + state events: working shows spinner frames, idle
  shows static title; config off → title stays static.
