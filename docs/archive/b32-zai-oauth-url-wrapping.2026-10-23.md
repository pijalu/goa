<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# B32 — z.ai OAuth sign-in panel hid the authorization URL

Closed 2026-10-23. Archived from `bugs.md`.

## Reported and expected behavior

The z.ai Coding Plan login panel displayed the sign-in instruction, a blank row,
`(Cmd+click to open)`, and `Waiting for authorization...`, but no usable URL. The
complete provider-issued URL must remain visible and clickable while authorization
is pending.

## Investigation and root cause

A live broker initialization (`POST /oauth/cli/init`) returned `code:0` and a
197-character authorization URL. Only endpoint metadata, query-field names, and
value lengths were recorded; no state or flow credential was retained. The broker
therefore supplied a non-empty URL, and `zaiStartFlow`/`NotifyURL` passed it through
as intended.

The production z.ai writer renders that URL as both the label and target of an
OSC-8 hyperlink. Existing fixtures used short URLs, so they never entered the
overwidth path. The long live-shaped URL exposed two connected defects:

1. Preformatted system panels did not wrap their input. The compositor clipped
   rows wider than the terminal through `ansi.Truncate`.
2. `splitAnsiSegments`, used by `ansi.Truncate`, recognized CSI sequences but not
   OSC sequences. It counted the invisible OSC-8 target as visible columns and
   returned before reaching the URL label. The resulting row contained a border
   and a truncated escape payload instead of the visible address.

The production-path RED filmstrip showed:

```text
│ Open this URL to sign in to your z.ai Coding Plan:
│ \e]8;;https://chat.z.ai/api/oauth/authorize?client_id=...
│ (Cmd+click to open)
│ Waiting for authorization...
```

The focused ANSI reproduction returned visible text
`"│ \x1b]8;;https://chat.z"` instead of `"│ https://chat.z.ai/"` for a
20-column truncation.

## Fix

* ANSI segmentation now recognizes complete CSI and OSC sequences through one
  scanner, so hyperlink targets have zero visible width during truncation and
  wrapping.
* Long-word splitting uses the same segments and grapheme widths rather than a
  rune-level escape tracker that could terminate inside an OSC URL.
* Wrapped rows close bleeding hyperlink/underline state and restore the logical
  state on continuation rows. Hyperlink-only `AnsiState.GetActiveCodes` now
  restores the link even without an accompanying SGR attribute.
* Preformatted panels preserve intentional newlines but wrap overwidth lines to
  their inner width before compositor clipping.

The URL remains a real OSC-8 hyperlink on every continuation row; panel borders
and padding are not part of the link.

## Regression and visible-terminal evidence

`TestTruncate_OSC8HyperlinkUsesVisibleText` and `TestWrap_LongOSC8Hyperlink` pin
zero-width OSC targets, complete visible text, width limits, and per-row hyperlink
close/reopen behavior.

`TestLoginZaiOAuthURL_LongBrokerURLFilmstrip` drives the production z.ai URL writer
through `LoginCommand` and a live TUI engine at 100 columns. Before the fix it
failed with the truncated escape row above. After the fix, the complete URL is
reconstructable from three visible rows:

```text
│ https://chat.z.ai/api/oauth/authorize?client_id=client_P8X5CMWmlaRO9gyO-KSqtg&redirect_uri=zcode │
│ %3A%2F%2Foauth%2Fcallback%3Fflow_id%3Dflow-visible&state=0123456789abcdef0123456789abcdef&respon │
│ se_type=code                                                                                     │
│ (Cmd+click to open)                                                                              │
│ Waiting for authorization...                                                                     │
```

The test then completes login and verifies credential storage. Its browser opener
is not invoked. The ANSI, TUI, and command packages also passed their complete
package tests and race runs.

## Validation actually run

* `go test -count=1 -race ./internal/ansi ./tui ./internal/agentic/provider/oauth ./core/commands` — passed.
* `go test -count=1 -race -cover -timeout 180s ./...` — passed; final run 56.44s.
* `go vet ./...` — exit 0, no diagnostics.
* `staticcheck ./...` — exit 0, no diagnostics.
* `gocognit -over 15 .` — initially found new `splitAnsiSegments` score 22;
  extracting the sequence scanner fixed it. Final output contains only unrelated
  pre-existing `GoalDriver.Drive` 21, `AgentManager.executeRunner` 16, and
  `app.initGoalSystem` 16.
* `gocyclo -over 12 .` — only unrelated pre-existing `GoalDriver.Drive` 13.

A real account authorization was not performed; validation used one unauthenticated
live broker initialization plus local reproducible OAuth fixtures. All httptest
servers were closed by test cleanup. No `goa server`, browser process, or persistent
validation server was left running.
