# Bug and feature Archive

Completed entries moved here from bugs.md per guideline 4.

### BUG: python tool — `AttributeError: 'str' has no attribute 'splitlines'` (gpython does not implement `str.splitlines`)

**Status:** FIXED — implemented, tested, validated, archived.

**Resolution:** added `tools/python_string_methods.go` — an interpreter shim mirroring
`tools/python_file_methods.go` (sync.Once + py.MustNewMethod on py.StringType.Dict)
implementing str.splitlines([keepends]) with full CPython semantics (boundary set
\n \r \r\n \v \f \x1c \x1d \x1e \x85 \u2028 \u2029; \r\n single boundary; no
trailing empty element; "" → []; bool/int keepends; TypeError otherwise).
Tests: `tools/python_string_methods_test.go` — reported transcript replay plus
table-driven boundary/keepends/validation cases through the real python tool.
Validated: `go test -timeout 30s ./tools/` ok; reported transcript reproduced
successfully via interactive shell (`['line one', 'line two', 'line three']`,
no AttributeError). Gates run separately: go vet ✓; staticcheck — pre-existing
warnings only, none from this change ✓; gocognit -over 15 — pre-existing only ✓;
gocyclo -over 12 — pre-existing only ✓; go test -count=1 -race -cover ./... ✓.
**Follow-up noted during implementation:** gpython str also lacks `index`
(`AttributeError: 'str' has no attribute 'index'`) — broader str-method audit
remains a non-blocking follow-up as already noted in §5.

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

**Status:** FIXED — implemented, tested, validated, archived.

**Resolution:** `internal/python/stdlib/re.go` — reSub and patternSub now share
`substituteAll`: a callable repl (py.I__call__) is invoked per match with a
*Match via py.Call and its str-coerced return is spliced in (exceptions
propagate unchanged); a str/bytes repl without backslashes keeps the literal
fast path; otherwise CPython template expansion applies (\1..\99, \g<N>,
\g<name>, \\, verbatim non-letter escapes; IndexError 'invalid group
reference'/ValueError 'bad escape'/IndexError 'unknown group name' mirroring
CPython wording; non-str non-callable repl → TypeError 'repl must be a
string or callable'). Docstrings + tools/python.long.md re row updated.
Tests: internal/python/stdlib/re_test.go (TestReSubCallable{,Pattern,NoMatch,
Exception}, TestReSubTemplate{,Errors} — 21 cases) + end-to-end
TestPythonTool_Execute_ReSubCallable in tools/python_test.go.
Validated: go test ./internal/python/... ok; go test ./tools/ ok; reported
transcript replayed via PTY — skillEnabled(cfg, "refactor") →
skillEnabled(cfg, "refactor", nil), changed: True, no TypeError. Gates run
separately: go vet ✓; staticcheck pre-existing only ✓; gocognit -over 15 ✓
(expandTemplate refactored into helpers to fit the budget); gocyclo -over 12
pre-existing only ✓; go test -count=1 -race -cover ./... ✓.

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

### BUG: python tool — gpython `dict` is string-keys-only: `d.get(200, 0)` raises `KeyError` instead of returning the default

**Status:** FIXED — implemented, tested, validated, archived.

**Resolution:** added `tools/python_dict_methods.go` — an interpreter shim
mirroring `tools/python_file_methods.go` (sync.Once + py.MustNewMethod
replacing `py.StringDictType.Dict["get"]`). The patched `get` accepts any key
type: str keys are looked up directly; any other key type is normalized
through `str(key)` (`py.StrAsString`), so `d.get(200, 0)` finds the value
stored under `'200'` and returns the default (or `None` with one arg) when
absent — CPython-compatible for the tally idiom. Arg validation matches
CPython: 0 or >2 positional args → `TypeError`.
Tests: `tools/python_dict_methods_test.go` — reported transcript replay,
normalization/default/arg-error cases through the real python tool, plus a
restriction-pinning test for the residual setitem KeyError.
Doc sync: new prominent section "dict keys are strings" in
`tools/python.long.md` (get normalization + the setitem/literal restriction
with the str() workaround); json row cross-references it.
**Message improvement (plan step 2) — assessed, NOT possible without forking:**
`py.SetItem` (`py/internal.go:197`) and `py.SequenceContains`
(`py/sequence.go:142`) dispatch to the native Go interface methods
`I__setitem__`/`I__contains__` on `py.StringDict` *before* ever consulting
the type's method table, so neither the `FIXME` message nor the setitem/
contains behavior can be patched from Goa; the upstream `FIXME` message
remains and the restriction is documented in the long doc instead.
Validated: `go test -timeout 30s ./tools/` ok; reported transcript reproduced
successfully via interactive shell (`d.get(200, 0)` → `0`, tally idiom →
`[('200', 2), ('404', 1)]`, int-key get finds string bucket, default/None
honored; residual `d[200] = 2` KeyError confirmed as documented). Gates run
separately: go vet ✓; staticcheck — pre-existing warnings only, none from
this change ✓; gocognit -over 15 — pre-existing only ✓; gocyclo -over 12 —
pre-existing only ✓; go test -count=1 -race -cover ./... ✓.

#### 1. Symptom (observed in the exported session, reproduced standalone)

```python
d = {}
print(d.get(200, 0))
```
```
Error: [python error: execution_error]
Traceback (most recent call last):
  File "<python>", line 2, in <module>
KeyError: '200'
```

In CPython this prints `0`. Worse, the same restriction hits every int-key
operation, with a second, self-identifying message:

```python
d = {'x': 1}
d[200] = 2
```
```
KeyError: 'FIXME can only have string keys!: 200'
```

and dict literals with int keys fail at construction:

```python
d = {200: 'found'}
```
```
KeyError: 'FIXME can only have string keys!: 200'
```

#### 2. Root cause (confirmed)

gpython's `dict` is `py.StringDict` — a `map[string]Object` — with string
keys **only**. The upstream source literally marks the restriction unfinished:

- `py/dict.go:241` `M__setitem__`: non-`String` key →
  `KeyError: "FIXME can only have string keys!: %v"`.
- `py/dict.go:288` `M__contains__`: same message.
- `py/dict.go:72-96` the `get` method: if `args[0]` is not a `String` it
  raises `KeyError: "%v"` unconditionally — **the `default` argument is
  ignored entirely** for non-string keys (`switch length { case 2: return
  args[1] ... }` is only reached on the string-key path).

CPython dicts accept any hashable key and `dict.get(key, default)` returns
`default` for missing keys of any type. The model sees only a bare
`KeyError` with no hint that the interpreter restricts keys to strings, and
must guess at the workaround (use `str(...)` keys).

#### 3. Fix plan (clean/elegant)

The pragmatic fix is to make the failure *discoverable* and the common
counter/tally idiom work, without forking gpython:

1. **Patch the `get` method** via the existing shim pattern
   (`tools/python_file_methods.go` — `sync.Once` + `py.MustNewMethod` on
   `py.StringDictType.Dict`): accept any `py.Object` key by normalizing
   non-string keys through `str(key)` (or the object's `__str__`), then
   perform the map lookup. Missing key → return the `default` (or `None`
   with one arg), matching CPython.
2. **Patch `M__setitem__`/`M__contains__`-equivalent surface** is harder
   (interface methods, not dict entries) — instead, keep the restriction
   but **rewrite the error message** to say the key must be a string
   (drop the `FIXME` wording): `KeyError: "dict keys must be strings, got
   %s"`. This is a gpython-upstream edit; since gpython is vendored as a
   module, prefer patching in Goa via a shim only if the method table
   allows (methods registered in `init()` can be replaced; the
   `M__setitem__` interface cannot be swapped without forking).
3. If forking upstream is acceptable, fix `py/dict.go` `M__setitem__`
   (line 241), `M__contains__` (288) and the `get` method (72-96) to
   coerce keys via `String(o)`/`Str` and honor the default. Coordinate
   with the gpython maintainer.
4. **Doc sync**: add a prominent row to `tools/python.long.md` — "dict
   keys are strings-only; int/other keys raise KeyError; use str() keys".

#### 4. Validation steps

- Reproduce the reported transcript against the fixed tool: the
  `by_status.get(st, 0)` tally runs to completion with int keys.
- `go vet ./...`, `staticcheck ./...`, `gocognit -over 15 .`,
  `gocyclo -over 12 .`, `go test -count=1 -race -cover ./...` (each run
  separately).

#### 5. Residual risks / follow-ups (out of scope here)

- A full dict-key upgrade to arbitrary hashables is an upstream gpython
  change (StringDict → Dict); out of scope here. The `FIXME` wording in
  the setitem/contains KeyError message likewise requires the upstream
  edit (dispatch order confirmed at `py/internal.go:197` and
  `py/sequence.go:142` — the native interface wins over the type's
  method table).
- Related but separate: `collections.Counter` (Goa shim) had the same
   string-only restriction — fixed in the Counter entry below.

### BUG: python tool — `collections.Counter` rejects int keys and has no `.get` (diverges from CPython dict semantics)

**Status:** FIXED — implemented, tested, validated, archived.

**Resolution:** `internal/python/stdlib/collections.go` — a new `counterKey`
helper normalizes any Counter key to its string form: `py.String`/`py.Bytes`
are used directly, any other object is stringified through `str`/`__str__`
(falling back to `__repr__`) via `py.StrAsString`. It replaces the strict
`compat.AsString` coercion in `M__getitem__` / `M__setitem__` /
`M__contains__` / `addFromIterable`, so `c[200] = c[200] + 1` with an int
status code now tallies under the `"200"` bucket instead of raising
`TypeError: 'Counter() argument must be str, not int'`. Added `get` on
`counterType.Dict` with CPython semantics — `get(key, default=None)` returns
the stored count (which may be 0) for a present key, else `default` (`None`
when omitted); non-string keys are normalized through `str(key)` before
lookup. Type doc updated to note key normalization. Doc sync: the
`collections` row in `tools/python.long.md` now documents `get(key,
default=None)` and the `str(key)` key normalization (c[200] and c['200']
share a bucket).
Tests: `internal/python/stdlib/collections_test.go` —
TestCollectionsCounterNonStrKeys (reported `c[200] = c[200] + 1` transcript,
constructor/update/contains/int-vs-str bucket sharing, bool normalization),
TestCollectionsCounterGet (reported `c.get('a', 0)` transcript, None default,
int-key get, stored-zero count), TestCollectionsCounterGetArgErrors (0 and 3
args → TypeError) — all through the real interpreter harness.
Validated: `go test -run TestCollectionsCounter ./internal/python/stdlib/` ok
(all cases pass). Interactive terminal verification (pty_exec) against the
real `python` tool reproduced both reported transcripts: `c[200] = c[200] + 1`
tallies (tally 2), `c2.get('a', 0)` → 2, `get('zzz', 0)` → 0, `get('nope')`
→ None, int/str bucket sharing → 7, `200 in c` → True / `404 in c` → False —
no TypeError/AttributeError. Gates run separately: go vet ✓; staticcheck —
pre-existing warnings only, none from this change ✓; gocognit -over 15 —
pre-existing only ✓; gocyclo -over 12 — pre-existing only ✓;
go test -count=1 -race -cover ./... ✓ (no FAIL/race; stdlib coverage 74.0%).

#### 1. Symptom (observed in the exported session, reproduced standalone)

```python
import collections
c = collections.Counter()
c[200] = c[200] + 1
```
```
Error: [python error: execution_error]
Traceback (most recent call last):
 File "<python>", line 3, in <module>
TypeError: 'Counter() argument must be str, not int'
```

And `Counter` lacks `dict`'s `.get`, which the model reaches for next:

```python
c = collections.Counter()
c.update(['a', 'b', 'a'])
c.get('a', 0)
```
```
AttributeError: "'Counter' has no attribute 'get'"
```

CPython: `Counter` accepts any hashable key (it subclasses `dict`) and
inherits `.get(key, default)`.

#### 2. Root cause (confirmed)

Goa ships its own `collections` module: `internal/python/stdlib/collections.go`.

- The type was documented as "A counter for **string** keys", but that doc
 was not surfaced to the model.
- `M__getitem__` / `M__setitem__` / `M__contains__` coerced keys with
 `compat.AsString(key, "Counter")` (`internal/python/compat/compat.go`),
 which raised exactly `TypeError: '%s() argument must be str, not %s'` for
 non-str keys — the observed message.
- There was no `get` method registered on `counterType.Dict`, so
 `c.get(...)` → `AttributeError`.

#### 3. Fix (implemented)

1. **Support non-str keys** by normalizing through the key's `__str__`
  (fall back to `repr`): new `counterKey(o py.Object)` helper stringifies
  `py.String`/`py.Bytes` directly and any other object via `py.StrAsString`;
  used in `M__getitem__` / `M__setitem__` / `M__contains__` /
  `addFromIterable`. Int keys tally as `"200"`.
2. **Add `get`** on `counterType.Dict`, CPython semantics:
  `get(key, default=None)` returns the stored count for key (which can be 0)
  or `default` if the key is absent.
3. **Doc sync**: updated the `collections` row in `tools/python.long.md` to
  note key normalization to strings and the new `.get`.

#### 4. Validation performed

- Reproduced the reported transcripts against the fixed tool: `c[200] =
 c[200] + 1` and `c.get('a', 0)` both work (see interactive verification
 above).
- `go vet ./...`, `staticcheck ./...`, `gocognit -over 15 .`,
 `gocyclo -over 12 .`, `go test -count=1 -race -cover ./...` (each run
 separately) — no new issues.

#### 5. Residual risks / follow-ups (out of scope here)

- Stringifying int keys means `c[200]` and `c['200']` become the same
 bucket — acceptable for counting use and documented in the tool's long doc.
- gpython's base `dict` remains string-keys-only (see the dict entry above);
 the Counter fix is independent of that.

### BUG: python tool — `AttributeError: "'bytes' has no attribute 'decode'"` (gpython does not implement `bytes.decode`)

**Status:** FIXED — implemented, tested, validated, archived.

**Resolution:** added `tools/python_bytes_methods.go` — an interpreter shim
mirroring `tools/python_file_methods.go` (sync.Once + py.MustNewMethod on
py.BytesType.Dict) implementing bytes.decode(encoding='utf-8',
errors='strict') with CPython semantics for the UTF-8 family: 'strict'
raises UnicodeDecodeError at the first invalid byte (CPython-style message
'utf-8 codec can't decode byte 0xNN in position N: invalid start byte'),
'ignore' drops invalid bytes, 'replace' substitutes U+FFFD per invalid byte;
UTF-8 aliases (utf-8/utf8/utf_8/u8/utf/cp65001) accepted, unknown encodings
raise LookupError, unknown errors modes raise LookupError, non-str
encoding/errors raise TypeError. Decoding is implemented with the standard
library unicode/utf8 (no new dependency).
Tests: `tools/python_bytes_methods_test.go` — TestPythonTool_Execute_BytesDecode
(reported `b"abc".decode('utf-8')` transcript, default encoding, multibyte
decode, utf8 alias, errors=replace/ignore, empty bytes) and
TestPythonTool_Execute_BytesDecodeErrors (strict→UnicodeDecodeError, unknown
encoding→LookupError, unknown errors→LookupError, too many args→TypeError,
non-str encoding→TypeError) through the real python tool.
Doc sync: new "bytes.decode" section in tools/python.long.md documenting the
idiom, errors modes, UTF-8-only scope, and the absent str.encode.
**Follow-up noted during implementation:** gpython str has no `.encode`
(`AttributeError: 'str' has no attribute 'encode'`) — a separate gap,
documented in the long doc; a broader bytes/str-method audit remains a
non-blocking follow-up as already noted in §5. Also, kwargs form
`decode(encoding=..., errors=...)` raises TypeError because gpython's
`py.UnpackTuple` rejects keyword args for these shim methods (same upstream
class as the fixed enumerate kwarg issue); positional form works.
Validated: `go test -run TestPythonTool_Execute_BytesDecode ./tools/` ok.
Interactive terminal verification (pty_exec) against the real python tool:
`b"abc".decode('utf-8')` → abc (no AttributeError), `b'hello'.decode()` →
hello, `b'a\xffb'.decode('utf-8','replace')` → ab,
`b'a\xffb'.decode('utf-8','ignore')` → ab, and strict default raises
`UnicodeDecodeError: 'utf-8' codec can't decode byte 0xff in position 0:
invalid start byte`. Gates run separately: go vet ✓; staticcheck — no
bytes_methods issues ✓; gocognit -over 15 — none ✓; gocyclo -over 12 — none ✓;
go test -count=1 -race -cover ./... ✓ (no FAIL/race; tools coverage 81.0%).

#### 1. Symptom (observed in the exported session, reproduced standalone)

```python
print(b"abc".decode('utf-8'))
```
```
Error: [python error: execution_error]
Traceback (most recent call last):
 File "<python>", line 1, in <module>
AttributeError: "'bytes' has no attribute 'decode'"
```

CPython prints `abc`.

#### 2. Root cause (confirmed)

gpython's `py/bytes.go` registers only `"replace"` on `BytesType.Dict`; there
was **no `decode` method** on the bytes type, so method lookup fell through
to "no attribute". (The only `decode`-related code was the `NotImplementedError`
for *string* encoding in `BytesNew` — not the bytes method.)

#### 3. Fix (implemented)

Mirrored the existing shim pattern (`tools/python_file_methods.go`):

1. `registerPyBytesMethods()` (idempotent, `sync.Once`, called from `init()`)
   patches `py.BytesType.Dict["decode"]` via `py.MustNewMethod`.
2. `bytesDecode(self, args, kwargs)` implements CPython semantics:
   - signature `decode(encoding='utf-8', errors='strict')` — accept and
     validate `encoding`/`errors`; UTF-8 family succeeds, unknown encodings
     raise `LookupError` like CPython.
   - `errors` in {'strict','ignore','replace'} for the utf-8 family;
     'strict' raises UnicodeDecodeError, others substitute.
   - returns `py.String(decoded)`.
3. Tests as listed above.

#### 4. Validation performed

- Reproduced the reported transcript against the fixed tool: no
  `AttributeError`; `abc` returned (see interactive verification above).
- `go vet ./...`, `staticcheck ./...`, `gocognit -over 15 .`,
  `gocyclo -over 12 .`, `go test -count=1 -race -cover ./...` (each run
  separately) — no new issues.

#### 5. Residual risks / follow-ups (out of scope here)

- Other gpython bytes-method gaps may exist (e.g. `split`, `join`, `encode`
  on str); this fix only adds `decode`. A broader audit of the bytes/str
  method tables is a separate, non-blocking follow-up.
- Keyword-argument form `decode(encoding=..., errors=...)` raises TypeError
  (gpython `py.UnpackTuple` rejects kwargs for shim methods); positional
  form is the supported usage.

### ENHANCEMENT: compression settings should be configurable per model (compression type + trigger)

**Status:** ALREADY IMPLEMENTED — verified, validated, archived.

**Resolution:** analysis found the requested capability is already fully
implemented and wired (landed in commit `7bde11c` "feat(agentic): tiered
per-model compression triggers (soft/trigger/hard)"); the bugs.md entry was
stale. Both the **type** and the **trigger** of compression are configurable
per model with global fallback:

- **Schema** (`config/config.go`): `ContextCompressionConfig.PerModel
  map[string]ModelCompressionOverride` (`per_model:` keyed by `models[].id`).
  `ModelCompressionOverride` carries `Strategy` + `Strategies`
  (soft/trigger/hard) for the compression **type**, and `ThresholdPercent` /
  `Thresholds.{Soft,Trigger,Hard}Percent` for the **trigger** (plus
  `MaxTokens`, `CacheGate`, `PreserveRecentTurns`).
- **Merge** (`config/config_merge.go`): `mergeCompressionPerModel` overlays
  per-model entries field-wise across the cascade.
- **Validation** (`config/config_validate.go`): `validateCompressionOverride`
  per model; per_model keys must reference a configured model id.
- **Runtime** (`core/agentmanager_lifecycle.go`): `buildCompressionConfig(cfg,
  mdl.ID, mdl.ContextWindow)` calls `overlayCompressionForModel`, which starts
  from the global section and applies each non-zero per-model field —
  type (`Strategy`/`Strategies.*`) and trigger (`ThresholdPercent` /
  `Thresholds.*Percent`) included — then the agent is built with the resolved
  config. Unknown/empty model id inherits the global section.

Tests (already present, all passing): `TestAgentManager_SetModel_AppliesPerModelOverride`,
`TestAgentManager_BuildCompressionConfig_PerModelOverlay` (full override /
partial inherits global / unknown model / empty id / legacy threshold),
`TestOverlayCompressionForModel_StrategiesAndCacheGate` in `core/`;
`TestDeepMergeContextCompressionPerModel` in `config/`.

Validated: targeted tests pass (`go test -run 'TestAgentManager_SetModel_AppliesPerModelOverride|TestAgentManager_BuildCompressionConfig_PerModelOverlay|TestOverlayCompressionForModel_StrategiesAndCacheGate|TestDeepMergeContextCompressionPerModel' ./core/ ./config/`).
Interactive terminal verification (guideline 5) via the real cascade loader
(`config.NewCascadeLoader`) against a temp HOME with a per-model config:
`big-model` resolved strategy=summarize + trigger=95 (overridden) with
max_tokens=100000 (inherited); `small-model` resolved max_tokens=8000 +
trigger=60 (overridden) with strategy=hybrid (inherited); `other-model`
(no override) inherited all globals. Type and trigger both customizable per
model with global fallback, as requested. Gates: no code changed by this
entry (verification only); full `go test -count=1 -race -cover ./...` run in
the same session showed no FAIL/race.

#### 1. Request (original)

Context-compression settings were thought to be global-only; the request was
to make both the compression type and trigger configurable per model.

#### 2. Finding

Already implemented — see Resolution above. No code change required.

#### 3. Residual / follow-ups

- None blocking. Per-model overrides are keyed by `models[].id`; a model not
  listed under `per_model` uses the global `context_compression` section.

### BUG: `/new` may end with a black screen

**Status:** NOT REPRODUCIBLE — already guarded by regression test; validated,
archived.

**Resolution:** analysis found this was already investigated and addressed.
Commit `2da17ab` ("test(tui): guard /new transcript clear against stale
content") records that a distinct /new redraw defect **could not be
reproduced** — the reported artifact matched the stream-retry duplication
fixed separately — and added a regression test locking the clear path.
`internal/app/events.go` `handleNewSession` performs a deliberate transcript
reset: it wipes the screen + terminal scrollback and resets the compositor
watermark (`tuiEngine.ClearTranscript()`), then restarts the agent session
and calls `RequestRender()` so the fresh canvas renders as a first frame.
Without that the old scrollback watermark could pin the window on blanks
(the reported symptom). The `/new` command (`core/commands/session.go`
`runNew`) additionally stops the current agent session cleanly before
signalling `NewSession()`.

Tests (already present, passing): `TestNewSessionClearsTranscript`
(`internal/app/newsession_redraw_test.go`) drives a populated session through
the real `handleNewSession` control path and asserts neither the chat
viewport nor the rendered frame retains pre-/new content.

Validated: `go test -run TestNewSessionClearsTranscript ./internal/app/` →
PASS. Interactive terminal verification (guideline 5) — ran the real goa
binary in a PTY (24x100), sent `/new`, and captured the post-command frame:
the screen repainted with the full welcome view (mascot art, version line,
context-loaded/skills/prompt-context/connection boxes, status bar) — 2673
non-whitespace bytes rendered, RESULT: "screen repainted with content after
/new (not black)". Gates: no code changed by this entry (verification only);
full `go test -count=1 -race -cover ./...` run in the same session showed no
FAIL/race.

#### 1. Symptom (original report)

Running `/new` could leave the TUI in a black/blank screen state instead of
rendering the fresh session view. Intermittent; reproduction not confirmed.

#### 2. Finding

Not reproducible. The transcript-clear + repaint path is implemented in
`handleNewSession` and guarded by `TestNewSessionClearsTranscript`; live PTY
verification shows the screen repaints with content. The originally reported
artifact matched a separately-fixed stream-retry duplication.

#### 3. Residual / follow-ups

- None blocking. If a concrete reproduction (specific timing/terminal)
  surfaces, reopen with the captured terminal output per guideline 5.

---

### BUG: `/` completion returns 352 options — typing delay and poor sub-parameter proposals

**Status:** FIXED — implemented, tested, validated, archived.

**Symptom:** Typing `/` opened a completion popup with 352 options and a
noticeable delay. Sub-parameter (modifier) completions for often-used commands
(e.g. `/goal`) were not proposed well — nested scopes like `/goal:cancel:current`
never surfaced.

**Root cause:** `tui/autocomplete.go` `(*CommandCompleter).Complete` built the
modifier tier for **every** matched base command plus each command's nested
args, unconditionally. Bare `/` matches every command (all share the `/`
prefix), so the tier exploded to hundreds of options. Separately, the
colon-triggered arg path only expanded level-2 when the level-1 value was an
exact arg-completer result, so a partially-typed nested segment
(`/goal:cancel`, argPrefix `cancel` with no trailing colon) completed to
nothing at its own level and never reached the nested scope.

**Fix:**
1. Defer modifier expansion until the user has typed ≥2 chars after `/`
   (`minCharsForModifiers`): bare `/` and `/x` return base commands (+Most
   Used) only; a near-exact command prefix surfaces its sub-params.
2. Bound the total option count (`maxCompletionOptions` = 100); the popup
   already renders top rows with a "(N more)" hint, so the cap only removes
   latency.
3. Added a parent-scope fallback in the colon path (extracted `completeArgs` +
   `expandArg`): when a partial nested segment completes to nothing, complete
   the parent scope and keep entries that extend the typed text — so `/goal`
   proposes `/goal:cancel` and `/goal:cancel:current`.

**Tests:** `tui/completer_test.go` — `TestCommandCompleter_BareSlashLimitsAndDefersModifiers`,
`..._SingleCharDefersModifiers`, `..._TwoCharsExpandsModifiers`,
`..._GoalProposesSubParams` (faithful `/goal` router replica incl. nested
scopes), `..._OptionCountBounded`. Two pre-existing tests
(`ExpandsModifiersForPartial`, `MostUsedTier`) documented the old 1-char
expansion contract and were updated to the new ≥2-char contract (their
regression intent — modifiers still expand for partial prefixes — is
preserved). `(*CommandCompleter).Complete` was refactored to keep gocognit in
budget.

**Validated:** completer unit tests green; real PTY terminal run —
bare `/` shows only `── Commands ──` (no Modifiers tier, `(55 more)`), `/m`
(1 char) shows base commands only, `/goal` shows `── Modifiers ──` with
`/goal:new`, `/goal:next`, `/goal:next:first/last/fresh`, `(17 more)`.
Gates (run separately): go vet ✓; staticcheck — fixed one new S1003
(`strings.Contains`) in autocomplete.go; remaining warnings pre-existing and
unrelated ✓; gocognit -over 15 — only pre-existing test/render functions ✓;
gocyclo -over 12 — only pre-existing `scrollOffUnstable` ✓;
go test -count=1 -race -cover ./tui ✓ (74.4%).

---

### BUG: `/new` after a long session — empty screen, no TUI redraw, input at top

**Status:** FIXED — implemented, tested, validated, archived.

**Symptom:** `/new` after a long session (or a few commands) intermittently
left an empty screen: no TUI redraw, the input appearing at the top of the
screen. Inconsistent — sometimes worked as expected.

**Root cause (race):** `(*App).handleNewSession` runs on the TUI **commandLoop**
(via `apply`), but `(*TUI).ClearTranscript` → `(*Compositor).Clear()` wrote the
screen+scrollback wipe (`\x1b[2J\x1b[H\x1b[3J`) to the terminal **immediately**,
while `renderLoop` → `(*Compositor).Render` paints frames on a separate
goroutine. Both serialize on `c.mu`, but each frame is emitted as several
terminal writes and the wipe was its own write outside any frame's CSI-2026
sync. A wipe landing adjacent to an in-flight frame (built from pre-clear
state) let that stale frame be painted after the clear; the next frame's
`frameFirst`/`drawWindow` then repaints in place without a wipe, so a blank
region the stale frame left behind persisted — the empty screen with the
editor at the top.

**Fix:** Removed the immediate wipe from `Clear()`. `Clear()` now only resets
compositor state (scrollTop/vt/prevLines/regionBot) and sets a new
`clearRequested` flag. The next `Render` consumes the flag and emits the wipe
**atomically inside its own CSI-2026 sync** (`drawWindow(..., wipe=true)`),
forcing `frameFirst` so the fresh canvas is fully repainted. Wipe and repaint
commit as one frame — a stale pre-clear frame can never be painted on top.

**Tests:** `tui/compositor_clear_race_test.go` —
`TestCompositor_ClearNeverInterleavesWithFrame` (slow byte-serialized terminal,
40 racing Render×Clear pairs, then fresh frame must write transcript content
with no wipe spliced after the content) and
`TestCompositor_ClearDoesNotCorruptNextFrame` (struct-invariant stress).
Existing `internal/app/newsession_redraw_test.go` still green.

**Validated:** new race tests pass 5× under `-race`; real PTY terminal run —
typed `/help`, `/team:list`, then `/new`: screen redrew cleanly with the fresh
mascot banner, editor at the bottom, footer present, no stale content, no
blank screen. Gates (run separately): go vet ✓; staticcheck — pre-existing
only ✓; gocognit -over 15 — pre-existing only ✓; gocyclo -over 12 — only
pre-existing `scrollOffUnstable` ✓; go test -count=1 -race -cover ./tui
./internal/app ✓ (74.4% / 55.2%).

---

### BUG: Goa teams UX — team creation not wizard-like; `/team` cannot add/remove like `/model`

**Status:** FIXED — implemented, tested, validated, archived.

**UX review (specialist pass, per the bug report):**
- HIGH — `/team` could not add/remove. TEAMS.md §8.1 mandates "`/team` behaves
  like `/model`", and `/model` supports inline add (`/model add`, `— add —` in
  the picker) and remove (`__delete__`). `/team` only selected/showed/synced.
- HIGH — Two divergent creation paths; creating a team required leaving the
  command for `/config → Teams → definitions → — add team —` (4 levels).
- MED — No in-place remove; `/config → Teams` split "active" from
  "definitions" and buried creation under the definitions submenu.

**Fix (mirroring `/model`):**
1. `/team` picker gains `— add team —` → opens the add-team wizard; the
   empty-state ("no teams defined") now offers `— add team —` instead of a
   dead-end message.
2. `/team:add` opens the same add-team wizard; `/team:remove:<name>` deletes a
   definition after confirmation (refused for the active team). `add`/`list`/
   `remove` work without an active team manager (like `/model add`). Completion
   proposes `add` and `remove:<name>`.
3. `/config → Teams` redesigned wizard-forward: one flat list — `— add team —`
   first (the wizard), then each defined team (select to edit/remove, active
   team annotated), then the Active-team row. The redundant `definitions`
   submenu was removed.

**Tests:** `core/commands/team_test.go` — `TestTeamCommand_AddOpensWizard`,
`..._PickerAddOpensWizard`, `..._NoTeamsOffersAdd`, `..._RemoveConfirmed`,
`..._RemoveActiveRefused`, `..._RemoveUnknown`, `..._CompleteArgs` (add +
remove: entries), updated `..._SelectorItems` (add + none entries);
`TestConfigMenu_TeamsRootIsWizardForward`. `(*TeamCommand).Run` was split into
`teamConfigOnlyDispatch`/`teamManagedDispatch` to keep gocyclo in budget.

**Validated:** 13 team-command tests + wizard-forward config test green; full
`core/commands` + `config` packages green. (The interactive selector could not
be driven blindly over a PTY — output is spinner-saturated — so the selector
item contents and wizard/remove flows are asserted via `SelectOptionFunc`/
`ShowInputFunc` in unit tests, which capture exactly what the user is shown.)
Gates (run separately): go vet ✓; staticcheck — pre-existing only ✓;
gocognit -over 15 ✓; gocyclo -over 12 ✓ (after Run split);
go test -count=1 -race -cover ./core/commands ✓ (57.9%).

---

### BUG: Incorrect team configuration — Goa creates a team named `LocalTeam` it then refuses to start with

**Status:** FIXED — implemented, tested, validated, archived.

**Symptom:** Goa created this config, then refused to start:
`teams.definitions.LocalTeam: team name must match [a-z0-9][a-z0-9-]{0,63}`.

**Root cause:** The team add-team wizard (`config_teams.go addTeamWizard`) and
the add-member flow (`addTeamMember`) accepted any typed name and persisted it
without validating against the documented naming rule (`config.teamNamePattern`,
TEAMS.md §3.5). A camelCase name like `LocalTeam` was written to the home
config; the next startup's config validation then hard-failed on it. The
config-level pattern is correct per spec — the bug was the missing input-side
validation in Goa's own creation flows.

**Fix:** Added exported `config.IsValidTeamName` and wired it into both entry
points: an invalid name now flashes an error (`must match
[a-z0-9][a-z0-9-]{0,63}`) and re-prompts instead of persisting. The wizard
prompt hint now documents the exact rule. Config-level validation is unchanged
(defense-in-depth).

**Tests:** `config/teams_test.go TestIsValidTeamName` (table incl. `LocalTeam`,
leading dash, 64/65-char boundary; trailing dash confirmed allowed by the
pattern). `core/commands/team_test.go
TestConfigMenu_TeamWizardRejectsInvalidName` + `..._AddMemberRejectsInvalidName`
(RED-verified: both fail without the fix — invalid name persisted / wizard
advanced to the model selector).

**Validated:** unit tests green (RED confirmed by temporarily reverting).
Real binary run: the reported `LocalTeam` config still correctly errors at
startup (name validation preserved), and a corrected lowercase `local-team`
config passes validation. Gates (run separately): go vet ✓; staticcheck —
pre-existing only ✓; gocognit -over 15 — pre-existing only ✓; gocyclo -over
12 — pre-existing only ✓; go test -count=1 -race -cover ./config
./core/commands ✓ (78.7% / 57.5%).
