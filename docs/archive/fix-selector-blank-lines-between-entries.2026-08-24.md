<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<!-- Copyright (C) 2026 Pierre Poissinger -->

# Archived: Command list can render an incorrect space (blank line) between entries

Closed 2026-08-24. Moved from `bugs.md`. Fixed by commit `8621261`
("fix(tui): selector flattens multi-line item text — no blank rows in
pickers").

Evidence: terminal capture in the original `bugs.md` entry — the /config
selector showing two blank rows between `Goals 7 days` and
`Loop detection warn:10 stop:15`.

## Investigation

The Selector is the only component rendering a `search>` line; its items are
the /config menu entries (23 = 8 shown + "(15 more)"), sorted alphabetically.
An exhaustive reproduction campaign through the real pipeline (Selector →
compositor full-repaint/diff paths → emit-time SGR coalescer → terminal
emulator) could not produce blank rows from clean data across navigation,
window scroll, filters, stacked selectors, reopen cycles, resizes, streaming
transcript growth, and a seeded 40-step churn fuzz.

## Root cause

`renderItem` concatenates Label + Description raw into one row-oriented
canvas line. Dynamic item text carrying embedded `\n`/`\r`/`\t` (malformed
config values, external names, plugin-provided strings) reached that line
unsanitized:

- on the current build, width measurement silently cuts the text at the first
  line feed (proven empirically: description `"custom\n\nbindings"` rendered
  as just `"custom"`);
- on older builds it split the popup into shifted physical rows with blank
  gaps — exactly the captured artifact.

## Fix

Single-line sanitization at the ingestion boundary (`sortSelectorItems`, the
one choke point for both `NewSelector` and `SetItems`): newlines, carriage
returns, and tabs become spaces; semantic `Value`s pass through untouched.
Multi-line data now renders inline with no loss on every picker.

## Tests

`tui/selector_contiguity_test.go`:
- `TestSelectorItemTextSanitized` — boundary unit test for both constructors;
  display fields flattened, Value preserved byte-exact;
- `TestSelectorMultilineDescriptionRendersInline` — end-to-end through real
  compositor + SGR coalescer + terminal emulator; poisoned description sorted
  between Goals and Loop detection renders complete on one row, no blank rows;
- `TestSelectorOverlayContiguityUnderChurn` — seeded 40-step churn over
  transcript+editor+footer chrome asserting popup contiguity after every frame.
