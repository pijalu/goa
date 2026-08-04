<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Bug Archive — 2026-08-04

Archived from `bugs.md` after fixing the setup-wizard black-screen bug (bug A).

## A. Setup wizard is not drawn — black screen on first start (input still works)

**Original:** On the very first run (no `~/.goa/config.yaml`), goa prints "⟡  First run detected — launching setup wizard", clears the screen to black, and shows nothing. The wizard never paints. Pressing Esc (Back) does exit ("Setup skipped…"), so keystrokes are being processed — the user just cannot see the wizard.

**Reproduction (guideline 5):**
```
$ rm -rf /tmp/goa-test-home && mkdir -p /tmp/goa-test-home
$ ( sleep 1; printf '\x1b' ) | HOME=/tmp/goa-test-home ./goa
⟡  First run detected — launching setup wizard
^[[?25l^[[?2026h^[[2J^[[H^[[3J^[[?2026l^[[?2026l^[[0m^[[r^M
^[[?25hSetup skipped. Edit ~/.goa/config.yaml manually, then restart.
```
Screen-clear sequences (`2J`/`H`/`3J`) with no wizard text between the clear and the "Setup skipped" line — the `renderWelcome` output never reaches the terminal.

**Root cause:** `RunSetupWizard` (`config/wizard_core.go`) called `engine.Start()` but never `engine.RunLoops()` nor `engine.RenderNow()`.
- `Start()` runs `compositor.InitialClear()` (hence the black screen) and returns. It does NOT launch the render goroutines and does NOT request a first frame.
- `RunLoops()` creates `dirtyChan` and starts the `commandLoop` and `renderLoop` goroutines. Without it, `t.dirtyChan` stays nil.
- `RequestRender()` is a no-op when `dirtyChan` is nil. So no frame is ever rendered, even though `HandleInput` calls `RequestRender`.
- Keystrokes still arrive because the terminal callback uses `t.Apply(func(){ t.handleKey(data) })` and `Apply` runs the command inline when loops are not running. So Esc → `handleEscape` → `finish()` → sends on the `done` channel → wizard exits — but invisibly.

**Fix:**
1. `config/wizard_core.go`: Extracted `runSetupWizardWithTerminal` (testable core accepting a `tui.Terminal`). After `engine.Start()`, added `engine.RenderNow()` (paint first frame synchronously) and `engine.RunLoops()` (launch commandLoop + renderLoop). This mirrors the production app's lifecycle: Start → RenderNow → RunLoops (`internal/app/app.go:209,218`). Once loops are running, every input-driven state change flows through `commandLoop → applyCommand → RequestRender → renderLoop` — the refresh-on-all-changes guarantee.
2. `tui/tui.go` `RunLoops`: Fixed a data race exposed by the new multi-goroutine wizard test. The original code used `loopsRunning.Swap(true)` BEFORE creating the channels (`t.cmds`, `t.snapReq`, `t.dirtyChan`), so `Apply` could observe `loopsRunning=true` with nil channels — sending on a nil channel and blocking forever. Reordered to create channels FIRST, then `CompareAndSwap(false, true)`. The happens-before chain through the atomic CAS guarantees `Apply` always sees a fully-initialized engine.

**Tests (RED first, then green):** Added `config/wizard_render_test.go` with a `wizardTestTerminal` fake terminal and four tests:
- `TestRunWizardWithTerminal_FirstFrameRenders`: asserts `RenderNow` paints the welcome screen ("Start setup"). Verified RED (empty buffer without the fix) before implementing.
- `TestRunWizardWithTerminal_RefreshOnEveryChange`: drives the wizard through Enter→advance, Down→navigate, Up→navigate, Escape→go-back, asserting each produces a new rendered frame.
- `TestRunWizardWithTerminal_NumberKeyRefreshes`: asserts number-key quick-pick (1-9) triggers a refresh.
- `TestRunWizardWithTerminal_TextInputRefreshes`: navigates to the Custom-endpoint input screen, types characters, and asserts the rendered output updates — completing the refresh-on-all-changes matrix (action keys, nav keys, number keys, free-form text).

**Validation (guideline 5 — interactive terminal):** Drove the wizard through welcome → Enter → provider screen → Down → number key "3" → Escape → Escape, verifying each input produced a visible frame change:
- Initial render: welcome screen with "Goa -- goa coding agent", "Welcome! Let's get you set up.", "[Enter] Start setup"
- Enter: provider screen with "LLM Provider", "Choose your LLM provider:", ">   1) OpenAI"
- Down: selection moves to ">   2) LM Studio"
- Number key "3": selection moves to ">   3) Ollama"
- Escape: back to welcome screen
- Escape: wizard exits

**Code quality (guideline 6 — run separately):**
- `go vet ./...` — clean
- `staticcheck ./...` — no new warnings (all pre-existing U1000/ST1005/ST1008 in unrelated files)
- `gocognit -over 15` — no new violations in changed files
- `gocyclo -over 12` — no new violations in changed files
- `go test -count=1 -race -cover ./...` — all pass
