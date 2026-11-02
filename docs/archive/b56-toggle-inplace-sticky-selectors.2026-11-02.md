# B56 — toggle menu values must not rerender the whole screen

Archived 2026-11-02. The report and plan lived in `bugs.md` (# To fix).

## Report

In every "toggle"-style config menu value (on/off rows in tools, skills,
compression, bash settings), flipping a value rerendered the complete
screen and moved the selection back to the top — an annoying jump that
lost the cursor position on every toggle.

Expected: toggling updates only the flipped row's on↔off description,
keeps the same selector overlay on the same entry, no full-screen
repaint, no scroll reset, no cursor move.

## Root cause (verified)

Every toggle handler ended with a full menu re-open — e.g.
`core/commands/config_tools.go` `toolToggleHandler` called
`c.settingTools()` again, which invoked a NEW `ctx.SelectOption(...)`
overlay. The old overlay was destroyed and a fresh one built: selection
reset to the first row, full repaint. Same shape in the skills and
compression toggle menus.

## Fix

Two primitives plus a delivery contract:

1. **Sticky selectors** (`tui/selector.go`): `SelectorItem.CloseOnSelect`
   marks rows that close the picker on Enter (navigation/opener rows);
   all other rows deliver their value WITHOUT closing (toggle rows).
   `Selector.SetSticky` flips the mode; `closesOn` gates `emit`'s
   `done()` so a toggle emit leaves the overlay open. Escape (and any
   close path) still delivers `""` and closes.
2. **In-place items update** (`tui/selector.go` `UpdateItems`): replaces
   the item list anchored by the prior `SelectedValue` (selection index
   preserved when the value still exists, clamped otherwise), keeps the
   search text, refilters, no overlay rebuild. Exposed as
   `tui.TUI.ActiveSelector()` + `ShowSelectorSticky`, and on the command
   context as `core.Context.SelectOptionSticky` /
   `UpdateSelectorItems` (Func fields, nil-safe fallbacks/no-ops).
3. **Delivery contract** (`internal/app/commandcontext.go`
   `deliverSelectorEmits`): the sticky delivery loop must deliver EVERY
   emit and the final `""` as `(selected="", ok=false)` — that cancel
   contract is what runs the menu's `back()`. The first draft returned
   on `""` without delivering, which swallowed Escape and stranded the
   user in a dead menu (caught by the compression filmstrip test).

Toggle handlers now end with `ctx.UpdateSelectorItems(refreshedItems)`
instead of re-invoking the opener. Migrated sites:

- `core/commands/config_tools.go` — `settingTools` (tool toggles),
  `settingBash` (warn-on-file-edit toggle), `settingToolsMenu`
  (`tool_call_fixing` toggle; `enabled_tools` opener row
  `CloseOnSelect: true`).
- `core/commands/config_compression.go` — `settingCompression` +
  `settingCompressionAdvanced` via `compressionItems()` /
  `compressionAdvancedItems()` builders (enabled / cache_gate /
  microcompaction toggles flip in place; opener rows CloseOnSelect;
  per-model editor and value pickers stay plain all-opener menus).
- `core/commands/config_skills.go` — `settingStickySkills` +
  `settingSkillSource` (skill toggles flip in place); all-opener menus
  unchanged.

## Tests

- `tui/selector_sticky_test.go` — 8 contract tests: sticky toggle keeps
  the overlay open (incl. second Enter), CloseOnSelect row closes,
  Escape closes with the `""` emit, plain selectors still close on
  Enter, `UpdateItems` anchors selection by value / keeps filter /
  clamps vanished values / handles nil and empty lists.
- `core/commands/b56_toggle_inplace_test.go` — live-engine test on the
  real TUI + production surfaces: toggle a row → SAME selector instance,
  SAME selection, flipped description on the live overlay, exactly one
  delivery; Escape → exactly one `("", false)` delivery and overlay
  closed. Single-goroutine drain harness (filmstrip/uiScenario pattern)
  because the production delivery goroutine would race the inline
  `engine.Apply` under `-race` in loop-less test engines.
- Harness wiring updated: `tool_lifecycle_runtime_test.go`,
  `config_menu_test.go` (sticky + UpdateSelectorItems),
  `internal/app/config_compression_filmstrip_test.go` (sticky queue/
  drain — this test caught the swallowed close emit).

## Validation

- `go test ./core/commands/ ./tui/ ./internal/app/ -count=1 -race` green.
- Full `go test -count=1 -race -cover ./...` green.
- Gates separately: `go vet ./...` clean, `staticcheck ./...` clean,
  `gocognit -over 15 .` / `gocyclo -over 12 .` — only pre-existing
  accepted hotspots (see bugs.md guideline note); the refactored
  `wireInteractiveCallbacks` and the B56 test measure under budget.
- Filmstrip validation via the compression filmstrip suite (real render
  output through the sticky menus, including reopen-after-Escape).
