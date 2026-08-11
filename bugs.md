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

### BUG: python tool — `TypeError: 'sub() argument must be str, not function'` (gpython `re.sub` rejects a callable replacement)

**Status:** analysis + fix plan (root cause confirmed; not yet implemented)

**Source:** session export `.goa/exports/goa-export-20260811-151235.zip`
(issue.md: "python issues"). The same export also reproduced the
`str.splitlines` `AttributeError` already tracked above; the only *new*
gpython failure in it is this one.

#### 1. Symptom (observed in the exported session)

The model attempted a routine, valid CPython idiom — a regex rewrite with a
function computing each replacement from the match — and the `python` tool
rejected it:

```python
import re
p = "/Users/muaddib/dev/goa/core/commands/config_skills_test.go"
s = open(p).read()

def sub_se(m):
    return "skillEnabled(%s, %s, nil)" % (m.group(1), m.group(2))
s2 = re.sub(r'skillEnabled\((cfg|got|fresh), (["a-zA-Z0-9_-]+|name)\)', sub_se, s)
```

```
Error: [python error: execution_error]
Traceback (most recent call last):
  File "<python>", line 7, in <module>
TypeError: 'sub() argument must be str, not function'
```

`re.sub(pattern, repl_fn, string)` with a callable `repl` has been valid
CPython since Python 2.7/3.x and is the standard way to compute
replacements from match groups. The model burned a turn discovering the
rejection, then fell back to `perl -0pi -e ...` via bash (its own words:
"gpython's re.sub doesn't support function replacement either"). The error
message gives no hint that callable replacement is simply unimplemented,
nor that the documented substitute is a template string.

#### 2. Root cause (confirmed)

The `python` tool's `re` module is **goa's own Go-backed module**, not
stock gpython: `internal/python/stdlib/re.go` (registered from
`internal/python/stdlib/register.go`). Both entry points coerce `repl` to a
Go string before substituting:

- `reSub` (`re.go:263-289`): `py.UnpackTuple(args, nil, "sub", 3, 4,
  &pattern, &repl, &str, &flagsObj)` then
  `replStr, err := compat.AsString(repl, "sub")` and
  `pp.re.ReplaceAllLiteralString(txt, replStr)`.
- `patternSub` (`re.go:382-398`): same coercion for the bound
  `Pattern.sub(repl, string)`.

`compat.AsString` (`internal/python/compat/compat.go:15-23`) accepts only
`py.String`/`py.Bytes` and otherwise raises exactly
`TypeError: '%s() argument must be str, not %s'` — the observed message
(`%s` = `sub`, type = `function`). Because substitution goes through Go
`regexp.ReplaceAllLiteralString`, the replacement is treated **literally**:
even the string form silently ignores group references (`\1`, `\g<name>`),
a second silent divergence from CPython.

Goa's own long-form tool doc already discloses the gap
(`tools/python.long.md`: "`re.sub` uses literal replacement only"), but the
model only sees the doc after the failure, and the short tool description
("`stdlib beyond os/re/json/...`") implies full `re` parity.

#### 3. Fix plan (clean/elegant — support callables + templates in the Go module)

All changes in `internal/python/stdlib/re.go` (+ tests); no gpython fork
needed:

1. **Callable `repl`**: when `repl` is callable (`py.Callable` /
   `py.Call(ctx, repl, ...)`), substitute per match: use
   `pp.re.FindAllStringSubmatchIndex(txt, -1)`, build a `*Match` for each
   hit (the module already constructs `Match` objects for
   `search`/`match`/`findall` — reuse that constructor), call
   `repl(match)`, coerce the result with `compat.AsString`, and splice
   results into the output. Propagate exceptions raised by the callback
   unchanged. Non-callable `repl` keeps the current fast path.
2. **Template string `repl`**: expand `\1`-style numeric and `\g<name>`
   group references in the string form (CPython semantics; unknown/invalid
   references raise `error: bad escape`/IndexError as CPython does).
   `regexp.ReplaceAllString` is NOT a drop-in (it expands `$name`, not
   `\1`), so implement the small `\`-escape expander by hand or translate
   to `$` form carefully (escaping literal `$`).
3. **Error message**: when a non-callable, non-str/bytes `repl` arrives,
   keep the `TypeError` but prefer the CPython wording
   (`expected string or callable`) so the failure mode is discoverable.
4. **Doc sync**: update the `re` row in `tools/python.long.md` and the
   `sub` docstrings in `re.go` once callables/templates land.
5. **Tests** (`internal/python/stdlib/re_test.go`, table-driven, and one
   end-to-end case via the real `python` tool harness as in
   `tools/python_test.go`):
   - the reported transcript: `re.sub(pattern, fn, s)` with `m.group(1)`
     in the callback performs the rewrite (no `TypeError`).
   - `re.sub` and `Pattern.sub` with callable `repl`: single match,
     multiple matches, no match (callback never invoked), callback raising
     an exception (propagates).
   - template string form: `\1`, `\g<1>`, literal backslash handling, and
     out-of-range group → error.
   - arg validation unchanged: `re.sub(p, 42, s)` still `TypeError`.

#### 4. Validation steps

- Reproduce the reported transcript against the fixed tool: the
  `config_skills_test.go` rewrite runs to completion, no `TypeError`.
- `go vet ./...`, `staticcheck ./...`, `gocognit -over 15 .`,
  `gocyclo -over 12 .`, `go test -count=1 -race -cover ./...` (each run
  separately).

#### 5. Residual risks / follow-ups (out of scope here)

- Go's RE2 engine still means no lookarounds/backreferences in the
  *pattern* itself — a separate, documented limitation not addressed here.
- `re.subn` does not exist in the module at all (only `sub`); adding it is
  a separate enhancement.
- The callable-`repl` path runs one VM call per match; pathological inputs
  (many matches) are slower than the literal fast path — acceptable, but
  note it in the docstring.
