<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# B25 — web input: macOS composed characters (Option / Shift+Option) were dropped

Closed 2026-10-10. Archived from `bugs.md`.

## Reported

"special mac char like pipe (shift+option+l) are not rendering".

## Root cause

macOS has no browser setting for "Option as Meta": the Option layer produces glyphs
and the page receives the glyph. Shift+Option+L therefore arrives as `key: "|"`,
`code: "KeyL"`, `alt: true`, `shift: true` — and a real terminal inserts `|`
(Terminal.app's default, iTerm2, every GUI terminal).

`EncodeKey` sent **every** Alt chord as `ESC` + character (`encodeAltPrintable`), so
the engine received `alt+|` — an unbound key — and the pipe never appeared. Every
Option glyph was unreachable in the web UI: `|`, `∫`, `€`, `¡`, ``, …

## Fix

`composedAltKey` distinguishes the two cases without asking the platform, because the
browser reports them differently:

* a **Meta** chord reports the character the physical key makes (Alt+b arrives as
  `"b"` on Linux/Windows) → keep the `ESC` prefix;
* a **composed** chord reports a glyph that key could not have produced → insert it as
  text.

The yardstick is `usLayoutRune(code, shift)`: the character a US layout makes for that
physical key with that shift state. A code the map does not know (punctuation codes,
an absent `code`) means "cannot tell" and keeps the Meta path, so no existing binding
changes meaning. The rule sits in `encodePrintable`, before `encodeAltPrintable`, and
applies only to the pure-Alt case; Ctrl/Alt combinations keep their CSI-u contract.

## Tests

Table cases in `TestKeyEncoder_SpecTable`:

| event | expected |
|---|---|
| `KeyL` + shift + alt + `"|"` | `"|"` |
| `KeyB` + alt + `"∫"` | `"∫"` |
| `Digit2` + shift + alt + `"€"` | `"€"` |
| `KeyK` + shift + alt + `""` | `""` |
| `KeyB` + alt + `"b"` | `"\x1bb"` (Meta) |
| `Digit2` + shift + alt + `"@"` | `"\x1b@"` (Meta) |
| `IntlBackslash` + shift + alt + `"|"` | `"\x1b|"` (unknown code) |

RED before the fix: the four composed-glyph cases failed with
`EncodeKey(...) = "\x1b|", want "|"` — the reported bug itself.

## Validation (real browser, rebuilt `goa server`)

Driving the live page with the exact macOS key event:

* `{key:"|", code:"KeyL", alt:true, shift:true}` → the input row shows `|` and the
  caret advanced to column 1 (`16.44px`). **Before** the fix this inserted nothing.
* `{key:"b", code:"KeyB", alt:true}` → no literal `b` appeared and the caret jumped to
  column 0 (the engine's word-motion binding): a Meta chord is still a chord.

`go test -count=1 -race ./internal/webui/` ok; vet, staticcheck, gocognit and gocyclo
clean on the package.

## Residual risk

A Ctrl+Alt chord on macOS (e.g. Ctrl+Option+L) still goes through the CSI-u path, so
its composed glyph is not inserted — the same class of problem, left as it is because
Ctrl+Alt chords are rare and the Ctrl contract is separate. A non-US layout whose
*uncomposed* key genuinely differs from the US yardstick could have an Alt chord read
as composed; the map only covers letters and digits, and everything else keeps the
Meta path.
