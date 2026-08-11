# Bug and feature Tracking

## Guideline
1. Create a detailed fix plan for each bug - the plan must contain test approach and validation steps - execute the plan and validate the fix when all elements are in place.
2. Any issues found must be fixed and the fix plan must be updated accordingly.
3. Issues found during testing must be fixed and the fix plan must be updated accordingly.
4. Each bug should be moved to archive when tested and closed as the associated plan.
5. Use interactive shell to validate the output of the tool - you must verify the actual terminal output.
6. Check code quality with each tool run separately (do not chain them with `;` or `&&`):
    - `go vet ./...`
    - `staticcheck ./...`
    - `gocognit -over 15 .`
    - `gocyclo -over 12 .`
    - `go test -count=1 -race -cover ./...`
    Fix any new issues introduced by the change. Pre-existing warnings are acceptable only if they are unrelated to the change and explicitly noted.

At the end of the session - the bug list should be empty and this file should only contain the guidelines for bug reporting.
If new items are added, restart the process.

## Must fix

### BUG: python tool — `AttributeError: 'str' has no attribute 'splitlines'` (gpython does not implement `str.splitlines`)

**Status:** analysis + fix plan (root cause confirmed; not yet implemented)

#### 1. Symptom (observed in a real session)

A trivial, valid Python line-split idiom fails inside the `python` tool:

```
>>> import re, collections
... lines = open('/tmp/status_now.json').read().splitlines()
Error: [python error: execution_error]
Traceback (most recent call last):
  File "<python>", line 3, in <module>
AttributeError: "'str' has no attribute 'splitlines'"
```

`open(...).read()` returns a `str`, and CPython's `str.splitlines()` exists —
so the embedded interpreter does not implement it. The model receives only an
`AttributeError` with no hint that the method is simply unimplemented, and
must guess at a workaround (`s.split("\n")`, which is NOT equivalent for
`\r\n`, `\r`, `\v`, `\f`, `\x1c-\x1e`, `\x85`, `\u2028`, `\u2029`, and also
differs in the trailing-empty-string behavior).

(The second failure in the same session — `SyntaxError: 'invalid syntax'` on a
malformed `%`-format — was the user's own broken code, not a tool bug, and is
out of scope for this entry.)

#### 2. Root cause (confirmed)

The `python` tool runs on the embedded **gpython** interpreter
(`github.com/pijalu/gpython`, a Py3.4 subset). Its `py/string.go` registers a
method table on `py.StringType.Dict` with `split`, `strip`, `rstrip`,
`lstrip`, `join`, `upper`, `lower`, `startswith`, … but **no `splitlines`**.
Attribute lookup therefore falls through to "no attribute", surfacing as the
runtime `AttributeError` above.

Goa already maintains an interpreter-patch shim for exactly this class of
gap: `tools/python_file_methods.go` patches the global `py.FileType` with the
omitted `readlines` via `py.MustNewMethod` inside a `sync.Once`, registered
from an `init()` (pattern confirmed at `tools/python_file_methods.go:21-94`).
There is no equivalent shim for `py.StringType`.

#### 3. Fix plan (clean/elegant — mirror the existing shim)

Add `tools/python_string_methods.go` mirroring `python_file_methods.go`:

1. `registerPyStringMethods()` (idempotent, `sync.Once`, called from `init()`)
   patches `py.StringType.Dict["splitlines"]` via `py.MustNewMethod`.
2. `stringSplitlines(self, args, kwargs)` implements CPython semantics:
   - signature `splitlines([keepends])` — optional bool `keepends` (default
     False), rejected arg count > 1 / wrong type → `TypeError` like the other
     gpython str methods.
   - split on the full CPython line-boundary set: `\n`, `\r`, `\r\n`, `\v`
     (`\x0b`), `\f` (`\x0c`), `\x1c`, `\x1d`, `\x1e`, `\x85`, `\u2028`,
     `\u2029`.
   - `keepends=False` strips the terminator; `keepends=True` retains it.
   - a trailing line boundary does NOT yield a trailing empty element;
     `"".splitlines()` == `[]`.
   - return `py.NewListFromStrings(...)`.
3. Tests (`tools/python_string_methods_test.go`), table-driven, driving the
   real tool (same harness as `python_test.go` / `python_fs_test.go`):
   - the reported transcript: `open(...).read().splitlines()` returns the
     lines (no `AttributeError`).
   - `\n`, `\r\n`, `\r`, mixed, and the CPython-only boundaries (`\v`, `\f`,
     `\x1c`, `\u2028`, `\u2029`).
   - `keepends=True/False`, trailing-newline (no trailing empty element),
     empty string → `[]`, no-newline → single element.
   - arg validation: `splitlines(1,2)` → TypeError.

#### 4. Validation steps

- Reproduce the reported transcript against the fixed tool: no
  `AttributeError`; correct line list.
- `go vet ./...`, `staticcheck ./...`, `gocognit -over 15 .`,
  `gocyclo -over 12 .`, `go test -count=1 -race -cover ./...` (each run
  separately).

#### 5. Residual risks / follow-ups (out of scope here)

- Other gpython str-method gaps may exist (e.g. `splitlines` siblings); this
  fix only adds `splitlines`. A broader audit of the gpython str table is a
  separate, non-blocking follow-up.
- Error classification (`execution_error` vs `syntax_error`) is unchanged;
  the reported `SyntaxError` was user code, not a tool defect.
