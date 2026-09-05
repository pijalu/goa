# Edit tool trailing-newline drift

## Root cause
`EditFileTool.editByOperation` called `readLines` but discarded its `trailingNL` result, then joined line-operation output with `"\n"`. Batch edits already preserved this flag, so single and batch calls behaved differently.

## Change
- Preserve `trailingNL` in the single-operation path.
- Append one final newline after a successful line/pattern operation when the input had one and the result is non-empty.
- Keep files that originally had no final newline unchanged.

## Test approach
`tools/editfile_multi_test.go` adds a regression test for `replace_lines` on a newline-terminated file. Existing insert/delete tests were updated to assert the intended preservation behavior.

## Validation
- `go test -timeout 45s ./tools -count=1` — PASS.
- Targeted edit tests — PASS.
