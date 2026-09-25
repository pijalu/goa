# Fix Plan: `edit` tool defects (issues 1–7)

**Source bug report:** `/Users/muaddib/dev/creaves.project/edit-tool-bug.md`
**Evidence export:** `/Users/muaddib/dev/creaves.project/.goa/exports/goa-export-20260925-112854.zip`
**Investigation date:** 2026-09-25 · **Status:** approved plan, awaiting execution
**Repo:** `/Users/muaddib/dev/goa` · packages `tools` + `tools/common`

Mode: **fix** — smallest correct diff per issue, each with a regression test that fails without the fix (Hard Rule #3). No dependency changes. Prompts stay in `//go:embed` Markdown files (no hardcoded prompt text).

---

## Verified root causes (from investigation)

| # | Issue | Verdict | Root cause |
|---|-------|---------|-----------|
| 1 | Spurious `not_found` on byte-exact anchor | **Real bug** | `fuzzyEdit` only compares *whole lines*; a single-line `old_string` that is a substring of a longer line can never match. Error message misleadingly implies content is absent. |
| 2 | Mixed batch corrupts file | **Real bug** | `executeMulti` re-resolves content anchors, but absolute `start_line`/`end_line` harvested before the batch go stale after any line-count-changing edit. |
| 3 | Silent rollback (success, nothing persisted) | **Not reproduced** | Write path is atomic (`os.WriteFile`, error-propagating); likely path confusion (session used two different paths). Fix = post-write verification + resolved path in output. |
| 4 | Default `indent_mode` rewrites caller indentation | **Real bug** | Default `preserve` pads content to the target line's indent; corrupts semantic-whitespace formats (Markdown). |
| 5 | `replace_lines` rejects empty `new_content` | **Over-strict guard** | `""` indistinguishable from "field omitted" after unmarshal; guard from d3f8416 blocks deliberate empty content. |
| 6 | `replace_pattern` drops prefix/suffix | **Real bug** | Operates line-wise: replaces the entire matching line. Bonus defect found: `occurrence > #matches` silently no-ops while reporting success. |
| 7 | `write` not whole-file replace | **Not reproduced** | `write` is a clean `os.WriteFile` overwrite; symptom was preview/path confusion. Doc clarification only. |

Key session evidence (export `session/events.jsonl`): failing `replace` at events 103529/103801 with anchor byte-confirmed present via bash at 103656/103727; `replace_lines` on the same line 744 succeeded at 103977. Confirmed by local reproductions against `tools/`.

## Known facts the implementation relies on (verified)

- `splitLines("")` returns `[""]` (`tools/readfile.go:418`) — explicit-empty `new_content` naturally maps to one empty line, preserving line count.
- `tools/aliases.go` re-exports `common.*` — new shared helpers go in `tools/common` + an alias line.
- `matchTypeDesc` (`tools/editfile.go:441`) is only used in result-message strings; the renderer keys off the diff — no renderer change needed for a new match type.
- Existing batch test `tools/editfile_drift_test.go:157` (`replace_lines` 1:1 → content `replace`) stays **legal** under the F2 guard.
- Existing batch test `tools/editfile_multi_test.go:98-99` (`replace_lines` 1→2 then `replace_lines`) **will be rejected** by F2 — update it to expect the new error (it becomes the guard's documentation test) and move sequential-apply coverage to a legal batch (1:1 ops).
- Most existing `insert_after`/`replace_lines` tests use col-0 targets (preserve delta=0) and should pass unchanged under F4; audit failures individually — never weaken assertions to force green.

## Execution order

**F1 → F6 → F4 → F5 → F2 → F3 → F7 → gates.**
Matcher-level fixes first (self-contained), then op-level, then batch-level (guard semantics depend on final op behavior), cross-cutting write-verify, docs last. Each fix lands with its tests before the next starts.

---

## F1 — Substring fallback for single-line `old_string` (Issue 1)

**Files:** `tools/fuzzyedit.go`, `tools/editfile.go` (message) · **Tests:** `tools/fuzzyedit_substring_test.go` (new)

`fuzzyEdit` (`fuzzyedit.go:101-146`) splits file and `old_string` into lines; `findMatches` requires whole-line equality, so intra-line anchors never match.

1. Add `MatchExactSubstring MatchType = "exact_substring"` to the match-type consts.
2. After the existing strategy loop (exact → trailing → fuzzy) finds nothing, and **only when `len(oldLines) == 1`**, call new `substringEdit(normFile, fileLines, normOld, normNew, useCRLF)`:
   - `count := strings.Count(normFile, normOld)` → `0`: `ErrNotFound`; `>1`: `ErrAmbiguous` (with count, same message shape as line-ambiguous).
   - `normNew == normOld` → `ErrNoChange`.
   - `idx := strings.Index(normFile, normOld)`; `start := strings.Count(normFile[:idx], "\n")` (0-indexed line).
   - `replaced := normFile[:idx] + normNew + normFile[idx+len(normOld):]` (single splice; multi-line `newStr` expands naturally).
   - `newFileLines := strings.Split(replaced, "\n")`; `end := start+1`; `replLines := newFileLines[start : len(newFileLines)-(len(fileLines)-end)]`.
   - Diff via existing `generateDiff(fileLines, start, end, replLines)`; restore CRLF on content+diff when `useCRLF`; return `EditResult{..., MatchExactSubstring, start+1, end}`.
3. **Strictly additive ordering:** substring runs only after all existing strategies found 0 matches — every previously-successful input keeps its exact code path and result.
4. `matchTypeDesc`: add `case MatchExactSubstring: return "exact substring match"`.

**Tests (table-driven):** session repro (~1.2 KB line with `① — §` multi-byte + exact export anchor; must splice mid-line preserving the rest byte-for-byte — fails on current code with `ErrNotFound`); ambiguous (anchor twice → `ErrAmbiguous`, file untouched); multi-line `newStr` expansion; CRLF preservation; full-line `old_string` still resolves via tier-1 exact (`MatchType == MatchExact`); absent substring → `ErrNotFound`.

---

## F6 — `replace_pattern` replaces matched text, not the whole line (Issue 6)

**Files:** `tools/editfile_ops.go` · **Tests:** `tools/editfile_pattern_test.go` (new/extend)

Current (`editfile_ops.go:82-98`): a matching line is replaced wholesale, dropping unmatched prefix/suffix. Also `found < occurrence` silently returns the file unchanged while reporting success.

1. New helper `compileLinePattern(pattern string, caseSensitive bool) *regexp.Regexp`: try `regexp.Compile(pattern)`; on failure compile `regexp.QuoteMeta(pattern)` (uniform literal fallback). Prepend `(?i)` when case-insensitive (replaces the broken lowercase-the-line matching, which destroys case for substitution).
2. Rewrite single-line `replacePattern`:
   - `re.MatchString(line)` selects the Nth matching **line** (`occurrence`, default 1 — unchanged).
   - On the selected line: `re.ReplaceAllLiteralString(line, strings.Join(newLines, "\n"))` — literal replacement, no `$`-group expansion surprises (documented). Multi-line `new_content` splices via `splitLines` when appending.
   - `found == 0` → existing `pattern_not_found` (unchanged).
   - **New:** `found < occurrence` → `internal.ToolError{Type: "occurrence_not_found", Detail: "pattern matched N line(s), occurrence M requested"}`, nothing written.
   - **New no-change guard:** selected line byte-identical after substitution → `ToolError{Type: "no_change"}`.
   - `indent_mode` no longer applies to single-line `replace_pattern` (nothing to re-indent); block path `replacePatternBlock` unchanged, still honors it.

**Tests:** Issue-6 repro (`PREFIX keepme OLD_FRAGMENT suffix` → `PREFIX keepme NEW_FRAGMENT suffix`; fails on current code); regex classes (`\d+`); invalid-regex literal fallback (`a(b`); `(?i)` preserves surrounding case; `occurrence: 3` with 2 matches → `occurrence_not_found`, file unchanged; pattern == replacement → `no_change`; multi-line `new_content` splice.

---

## F4 — Default `indent_mode: as-is` for `insert_after`/`insert_before`/`replace_lines` (Issue 4)

**Files:** `tools/editfile.go` (2 call sites: `editByOperation` ~:252, `applySingleEdit` ~:378) · **Tests:** `tools/editfile_indent_test.go` (new) + audit of `editfile_test.go`, `editfile_multi_test.go`

Default `preserve` pads caller content to the target line's indent (`adjustPreserve`, `editfile_ops.go:280`) — confirmed: col-0 paragraph after an indented list item gains 3 spaces.

```go
func defaultIndentMode(op EditOperation, raw string) IndentMode {
    if raw != "" { return IndentMode(raw) }
    switch op {
    case OpInsertAfter, OpInsertBefore, OpReplaceLines:
        return IndentAsIs
    }
    return IndentPreserve // replace_pattern block path keeps legacy default
}
```

`preserve`/`normalize` remain explicit opt-ins — backward compatible for anyone who passed `indent_mode`.

**Audit step (mandatory):** after the change run `go test ./tools/ -run 'Indent|InsertAfter|ReplaceLines' -count=1`; for each failure decide: test was exercising default-preserve → add explicit `"indent_mode": "preserve"`; test was asserting the buggy default → update expectation. Never weaken assertions.

**New tests:** Issue-4 repro ×3 (insert after indented list item at col 0 → stays col 0; 3-space blockquote unchanged; col-0 bullet replace stays col 0 — all fail on current code); explicit `"indent_mode": "preserve"` still pads (back-compat); `replace_pattern` block path keeps preserve default.

---

## F5 — Accept explicitly-empty `new_content` (Issue 5)

**Files:** `tools/editfile.go` · **Tests:** `tools/editfile_empty_test.go` (new)

`resolveOpContent` (`:518-530`) can't distinguish `""` from omitted. Presence tracking keeps the d3f8416 deletion-guard while allowing deliberate empty content.

1. Add non-serialized `NewContentSet bool` to `editFileParams` + custom `UnmarshalJSON` (wire pattern):
   ```go
   func (p *editFileParams) UnmarshalJSON(data []byte) error {
       type Alias editFileParams
       var w struct { Alias; NewContent *string `json:"new_content"` }
       if err := json.Unmarshal(data, &w); err != nil { return err }
       *p = editFileParams(w.Alias)
       if w.NewContent != nil { p.NewContent = *w.NewContent; p.NewContentSet = true }
       return nil
   }
   ```
   If the alias field competes for the `new_content` tag, mark the alias's `NewContent` field `json:"-"`. Nested `Edits []editFileParams` recurses automatically. **Verify with the first test run.**
2. `resolveOpContent` new logic:
   - `NewContentSet && NewContent == ""` → valid; return `""` + note `"(explicit empty new_content: inserting one empty line)\n"`. Caller's `splitLines("")` = `[""]` → one empty line, count preserved.
   - `!NewContentSet && NewString != ""` → existing fallback (unchanged).
   - Neither set + content-requiring op → existing `missing_parameter` (unchanged).
3. `insert_after`/`insert_before` accept explicit empty too (insert one empty line). `delete_lines` unaffected.

**Tests:** Issue-5 repro (whitespace-only line + `replace_lines` `"new_content": ""` → truly empty line, total count unchanged — fails on current code with `missing_parameter`); omitted fields → still `missing_parameter` (d3f8416 regression); `new_string` fallback intact (`editfile_test.go:618` stays green); batch element with explicit empty works.

---

## F2 — Reject batches whose line-numbered ops follow line-shifting ops (Issue 2)

**Files:** `tools/editfile.go` (or new `editfile_batchguard.go` if complexity budget requires) · **Tests:** `tools/editfile_batch_drift_test.go` (new)

Content anchors re-resolve per edit, but absolute line numbers harvested pre-batch go stale. Confirmed: `delete_lines(2)` + `replace_lines(3)` overwrote the wrong line.

Pre-flight validation in `executeMulti` before the apply loop:

```go
func checkBatchLineDrift(edits []editFileParams) error
```

Two pure helpers (keep each gocognit ≤ 6):
- `usesAbsoluteLines(e)` — `replace_lines`, `delete_lines`: always; `insert_after`/`insert_before`: only when `start_line > 0`.
- `mayShiftLines(e)` — `insert_after`, `insert_before`, `delete_lines`: true; `replace_lines`: `endLine <= 0` or `len(splitLines(content)) != endLine-startLine+1`; `replace`: line-count(old) ≠ line-count(new) (`strings.Count(s,"\n")` after trailing-`\n` trim, mirroring `fuzzyEdit`); `replace_pattern`: conservatively true unless single-line pattern with single-line content.
- Reject at first `i` where `usesAbsoluteLines(edits[i]) && any(mayShiftLines(edits[:i]))` with `ToolError{Type: "stale_line_numbers", Detail: "edit i/N (replace_lines): uses absolute line numbers, but earlier edits in this batch change the line count — pre-harvested line numbers would target stale positions", Hint: "Split the batch: apply line-shifting edits in one call, re-harvest line numbers (grep -n / read), then apply line-addressed edits in a second call. Content ops (replace) and pattern-addressed inserts re-anchor automatically and can stay."}`

**Existing-test impact:** `editfile_multi_test.go:98-99` (1→2 then 1:1 line-ops) gets rejected — update to expect `stale_line_numbers` (becomes the guard's doc test); move sequential-apply coverage to a legal batch.

**Tests:** Issue-2 repro (`delete_lines(2)` + `replace_lines(3)` → error, file byte-identical — core regression test, silently corrupts on current code); `insert_after(start_line)` + `replace_lines` rejected; `replace` with unequal line counts + `replace_lines` rejected; **allowed:** `replace_lines` 1:1 + `replace_lines`; shifting op + content `replace`; `insert_after` by pattern + content op; pure content-op batches; error type/hint asserted.

---

## F3 — Post-write verification (Issue 3 mitigation)

**Files:** `tools/common/writeverify.go` (new), `tools/aliases.go`, `tools/editfile.go` (`writeEditResult`), `tools/writefile.go` (`Execute`) · **Tests:** `tools/common/writeverify_test.go` (new)

Write path is atomic; silent no-write not reproducible (likely path confusion). Defense-in-depth per report recommendation #1.

1. `tools/common/writeverify.go`:
   ```go
   func WriteFileVerified(path string, content []byte, perm os.FileMode) error
   func verifyWrite(path string, want []byte) error // read-back + bytes.Equal
   ```
   `WriteFileVerified` = `os.WriteFile` + `verifyWrite`. Read-back cost negligible (edit path already reads the whole file).
2. Alias in `tools/aliases.go`.
3. `writeEditResult` (single funnel for all edit paths): use `WriteFileVerified`; verification failure → `ToolError{Tool:"edit", Type:"write_verify_failed", Detail:"wrote N bytes to <resolvedPath> but read-back differs — the file may be modified externally", Hint:"Check for external processes modifying the file, then retry."}`.
4. `writefile.go Execute`: same, `Type:"write_verify_failed"`.
5. Resolved-path symmetry: in `executeMulti`/`editByOperation` result messages, append ` (resolved: <targetPath>)` when `targetPath != resolvedPath` (currently only the search/replace path notes redirects).

**Tests:** normal write → passes, bytes match; `verifyWrite` against diverging file → error (test `verify` directly — no flaky permission games); existing suite staying green covers edit-tool integration.

---

## F7 — Documentation (embedded prompts)

**Files:** `tools/editfile.long.md`, `tools/editfile.short.md`, `tools/writefile.long.md`, schema strings in `tools/editfile.go`

1. `old_string` matching: document 4 tiers (exact block → trailing-ws → fuzzy → **exact substring, single-line anchors only**); substring anchors must be unique file-wide.
2. **Batch rule:** absolute line-numbered ops must not follow line-count-changing ops in one batch (`stale_line_numbers` error); prescribe two-call workflow (shift edits → re-harvest → line edits) as the *primary* pattern for large files.
3. `indent_mode`: new default `as-is` for `insert_*`/`replace_lines`; `preserve`/`normalize` opt-in.
4. `replace_pattern`: match-wise substitution; all matches in the selected line replaced literally (no `$` expansion); `occurrence` selects the matching *line*; `occurrence_not_found` error.
5. `new_content: ""` explicitly allowed for `replace_lines`/`insert_*` (one empty line); omitting it still errors.
6. `writefile.long.md`: whole-file overwrite; preview shows only first 10 lines (not a diff).
7. Schema descriptions: `indent_mode` → "default: as-is for line/insert ops, preserve otherwise"; `edits` → "line-numbered ops may not follow line-shifting ops".

---

## Gates (all must pass, in order)

```bash
cd /Users/muaddib/dev/goa
gofmt -l tools/                                  # empty output
go vet ./...                                     # clean
go test ./tools/... -count=1 -race -cover -timeout 120s
gocognit -over 15 tools/ && gocyclo -over 12 tools/
go test ./... -count=1 -timeout 300s             # full suite
```

- If complexity gates flag `executeMulti`/`fuzzyEdit`, extract helpers — never weaken logic (budget 15/12 non-negotiable).
- Optionally finish with the `golang-check` skill for the static-analysis sweep.
- Final sanity: the three export-derived repros (Issue 1 anchor, Issue 2 batch, Issue 4 indent) pass as the new Go tests.

## Residual risks (state in final report)

- **F1:** an anchor that is both a valid full line elsewhere and a substring here resolves as full-line first (order preserved) — documented.
- **F4:** visible behavior change for prompts unknowingly relying on default `preserve`; mitigated by docs + explicit opt-in; call out in commit message.
- **F2:** rejects batches that previously "worked" by accident (model pre-compensated line math) — intended; error message explains the restructure.
- **F3:** read-back doubles write I/O per edit; acceptable for source files; no config knob (YAGNI).
- **F6:** models wanting whole-line replacement must switch to `replace_lines` — documented.
- **Issue 7:** no code defect in `write`; doc clarification only.
