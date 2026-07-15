<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Bug Archive — 2026-07-15

Archived after fixing in the 2026-07-15 session.

## edit/write silently corrupted backslash escapes (drove models to bash/python)

**Original:** Session `.goa/sessions/1784126185_wstvk497.jsonl`. While editing
`internal/python/stdlib/re.go` and `re_test.go` (a module full of regex and
string escapes), the `edit` tool silently corrupted the model's edits. A
correctly-escaped Go source line such as

```go
resultCode := code + "\n__result__ = str(_r)\n"
```

was written to the file as

```
resultCode := code + "
_result__ = str(_r)
```

i.e. the literal `\n` (backslash + n) was converted into a real newline,
breaking the string literal. After this kind of silent corruption, models lost
trust in the `edit` tool and fell back to `bash`/`python`/`sed` to modify files.

**Root cause:** The `edit` and `write` tools re-interpreted escape sequences
*after* JSON unmarshalling, which is a layering violation. JSON is already the
single source of truth for escaping: a real newline arrives as JSON `"\n"`, a
literal backslash-n (a source escape) arrives as JSON `"\\n"`. Re-interpreting
escapes a second time cannot distinguish "the model double-escaped" from "the
model legitimately wants the literal characters", so it corrupts any code that
contains backslash escapes (Go/Python string literals, regex metacharacters
such as `\n`, `\d`, `\s`, printf format strings).

Three sites were affected:

1. `tools/editfile.go` `editByOperation`: `new_content` was passed through
   `strings.ReplaceAll(p.NewContent, "\\n", "\n")` before being split into
   lines.
2. `tools/editfile.go` `replacePattern`: the `pattern` was passed through
   `unescapePattern`, which turned `\n`/`\t`/`\"`/`\'`/`\\` into the
   corresponding characters and could reroute a single-line regex into
   multi-line block matching.
3. `tools/writefile.go` `Execute`: `content` was passed through
   `strings.ReplaceAll(p.Content, "\\n", "\n")` before being written.

This corrects the 2026-07-09 "Tool call error" fix (`unescapePattern`), whose
cure was worse than the disease for any codebase dealing with regex/escapes.

**Fix:** All three input paths now use the model-supplied text verbatim; JSON
unmarshalling is the only escape layer.

- `tools/editfile.go`: `new_content` is split with the existing `splitLines`
  helper (verbatim); the `pattern` is matched verbatim, and only genuinely
  multi-line patterns (a real newline after JSON decoding) route to block
  matching. `unescapePattern` was removed.
- `tools/writefile.go`: `content` is written verbatim.

A model that still double-escapes now gets a clear `pattern_not_found` / no-op
result with the existing hint instead of silent corruption, and can correct
itself by sending real newlines.

**Tests:**

- `tools/edit_escape_repro_test.go` (new):
  `TestEditEscapeRepro_NewContentPreservesLiteralBackslashN`,
  `TestEditEscapeRepro_ReplacePatternStillWorks`,
  `TestEditEscapeRepro_RealNewlinePatternRoutesToBlock`.
- `tools/editfile_test.go`: replaced
  `TestEditFileTool_ReplacePattern_EscapedNewlinesAndQuotes` (which locked in
  the old double-unescape behavior) with
  `TestEditFileTool_ReplacePattern_MultilineBlockWithQuotes` (real newlines) and
  added `TestEditFileTool_ReplacePattern_LiteralBackslashNIsVerbatim` (verbatim
  matching, clear error when absent).
- `tools/writefile_test.go` (new): `TestWriteFilePreservesLiteralBackslashN`.

**Validation:** `go vet ./...` clean; `gocognit -over 15`, `gocyclo -over 12`,
`staticcheck` clean on `./tools/`; `go test -count=1 -race -cover ./tools/`
passes (79.5% coverage); `go build ./...` clean.
