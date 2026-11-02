# B40 — Multi-file batch edit form for the `edit` tool

Date: 2026-11-09
Bug: bugs.md B40 — "Editing a file takes a shell/python3 detour for repeated
small replacements" (multi-file half; the single-file batch already existed).

## Problem

The `edit` tool had two shapes: a single edit and an `edits` batch targeting
exactly one file (atomic per file, with `not_found`/`occurrence`/stale-line
diagnostics). Multi-file work — "apply this replacement to every file in the
list" and "replace this symbol everywhere in the package" — degraded to shell
loops or python heredocs, bypassing every diagnostic and mixing tracked and
untracked file writes.

## Fix

Two new forms on the same `edit` tool (`tools/editfile_multifile.go`):

- `files` — explicit targets: `{"files": [{"path": ..., "edits": [...]}, ...]}`
- `glob` — shared edits over a scope: `{"glob": "pkg/*.go", "edits": [...]}`
  (shell patterns `*` `?` `[..]`, project-root relative, sorted, regular files
  only; `**` unsupported on purpose; zero matches hard-fail with
  `glob_no_match`).

Semantics:

- Per file the existing single-file batch semantics are preserved: edits apply
  in order against in-memory content, and the `stale_line_numbers` guard runs
  per file before anything is written.
- Across files the call is all-or-nothing: every file is resolved, read,
  drift-checked and applied in memory first; only if ALL succeed is any file
  written. A non-matching pattern in any file is a hard failure naming that
  file and the failing edit — never a silent skip — and the error carries a
  per-path report (`ok` / `fail` / `skip`). The first failing error's
  `ToolError.Type` (e.g. `not_found`) carries over for retry logic.
- Duplicate paths apply once and are reported as `skip`s in the result;
  fuzzy-path reads surface as notes. Success output is one block per file
  (summary + net unified diff).
- `files`, `glob`, `path` and the single-file `edits` batch are mutually
  exclusive; nesting multi-file forms inside edit entries is rejected.
- `Access()` declares every expanded write path so the concurrent-tool
  scheduler serializes correctly; glob expansion there is a read-only scan.

Renderer (`tools/edit_renderer.go`): `extractDiffSections` (a
`diffSectionParser`) now splits output into one section per `@@` hunk with its
own summary header, so multi-file results render per-file diffs;
`RenderCall`/`RenderPartial` name the first target (+N more files) or the
glob pattern. Single-hunk output rendering is unchanged.

Schema: `files`/`glob` properties added; `path` no longer globally required
(exactly one form is enforced at execution time); descriptions kept under the
per-tool and total schema byte ceilings.

## Validation

- New `tools/editfile_multifile_test.go`: files-form success + per-path
  summaries, partial failure writes nothing (and preserves the failing
  error type), mixed match/no-match hard-fails, glob success (single-star
  scope), `glob_no_match` hard fail, glob matches only regular files,
  per-file stale-line guard, duplicate-target dedupe + reported skip, nine
  form-conflict rejections, `Access()` path declaration, two-hunk result,
  renderer call/partial/result-section coverage.
- Schema/docs tests updated: `TestEditFileTool_Schema_HasRequiredFields`,
  `TestEditFileTool_Schema_DocumentsNewBehavior`, long-doc pins for the
  multi-file semantics.
- Quality gates: gocognit/gocyclo clean on `./tools` (after refactoring
  `resolveBatchForm`, `executeBatchFiles`, `extractDiffSections`,
  `RenderPartial` into small helpers), staticcheck clean.
- `go test -count=1 -race -timeout 600s ./tools/... ./internal/app/...` and
  `go vet ./...` pass.
