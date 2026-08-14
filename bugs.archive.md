# Bug and feature Archive

Completed entries moved here from bugs.md per guideline 4.

### ENHANCEMENT: /config per-model compression settings — "select model / set compression parameters" UI

**Status:** IMPLEMENTED — tested, validated, archived.

**Gap:** the config FILE layer already supported `context_compression.per_model.<modelID>.*`
(`ContextCompressionConfig.PerModel`, merged via `mergeCompressionPerModel`, validated via
`validateCompressionOverride`, applied at runtime via `overlayCompressionForModel` /
`buildCompressionConfig`, live-refreshed via `RefreshContextCompression`), but the /config
Compression menu edited ONLY the global section — no model picker, no per-model override UI,
and `applyConfigSet`/`setConfigField`/`persistConfigValue` handled only the fixed global keys.

**Resolution:**
- `core/commands/config_cli.go`: `setConfigField` now recognizes dynamic
  `context_compression.per_model.<id>.<field>` keys (`parsePerModelCompressionKey`) and routes
  to `setPerModelCompressionField` / `applyPerModelField` (strategy, strategies.{soft,trigger,hard},
  thresholds.{soft,trigger,hard}_percent, max_tokens, cache_gate, preserve_recent_turns). Validation
  mirrors the global setters: same strategy allow-list, soft layer stays zero-LLM, threshold levels
  are 0/-1 or 10-95 in 5% steps (`setPerModelLevel`), cache_gate on/off. Empty value clears a field
  to "inherit"; `persistConfigValue` persists clears as `DeleteHomeField` so override entries stay
  clean. Cross-field invariants and the unknown-model-ID rejection are enforced by the existing
  `candidate.Validate()` gate.
- `core/commands/config_compression.go`: new "Per-model overrides" row in the Compression menu →
  `settingCompressionPerModel` (configured-model picker showing override/inherit state) →
  `settingCompressionPerModelEdit` (per-field editor with set + "inherit (clear)" + "clear all
  overrides"). All edits go through `applySet`, so validation/persistence/live refresh match the
  global settings.
- Tests (`core/commands/config_compression_test.go`): per-field key setters, invalid-value rejection
  (bad strategy, soft-LLM, bad-step/high thresholds, unknown model, unknown field), clear-to-inherit,
  menu entry presence, full menu flow (picker → edit → set → clear), and a CascadeLoader
  save+reload persistence round-trip. Updated `TestConfigMenu_CompressionSubmenu` item list for the
  new `per_model` row.

**Validated:** `go vet ./...` ✓; `staticcheck` — pre-existing `validatePercentRange` U1000 only
(verified pre-existing on clean tree, not from this change) ✓; `gocognit -over 15` / `gocyclo -over 12`
— no new functions over limit ✓; `go test -count=1 -race -cover ./...` — 81 packages ok, 0 failures ✓.

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

---

### BUG: Companion minor mode sticks after team use — footer shows `mode(companion)` and it is impossible to disable

**Status:** FIXED — implemented, tested, validated, archived.

**Symptom:** After using a team (with a reviewer), the footer permanently
showed companion state (`coding-posture(companion)` plus a companion model
line) and it was impossible to disable: the indicator survived `/team:off`
and returned on restart even after `/companion:off`.

**Root cause:** (1) Team activation applied the review policy via
`teamReviewController.ApplyReview` → `AgentManager.SetAgentDrivenEnabled(true)`
(`core/agentmanager.go:1026`), which **persists** `AgentDrivenEnabled: true`
to the session state store. (2) Deactivation (`restoreReviewLocked` →
`ApplyReview(ReviewApplyOff)`) reset orchestrator mode +
`InjectCompanionReview(false)` but never called
`SetAgentDrivenEnabled(false)`, never cleared `modeMgr.currentMinor`, and
never emitted `ev.MinorMode("")` — so the persisted flag and the footer label
leaked. (3) On restart, `restoreSessionState`
(`internal/app/subsystems.go:1163`) force-enabled companion from
`snap.MinorMode == "companion" || snap.AgentDrivenEnabled`, so the leftover
persisted flag re-asserted companion every startup.

**Resolution:** (a) `internal/app/team_adapters.go`
(`teamReviewController.ApplyReview`): the `ReviewApplyOff` path now also calls
`SetAgentDrivenEnabled(false)` before `InjectCompanionReview(false)`, so team
deactivation / a `review: off` restore fully tears down the agent-driven
companion state (and, because `SetAgentDrivenEnabled` persists, stops writing
the leftover `AgentDrivenEnabled:true` that re-asserted companion on
restart). (b) `internal/app/subsystems.go` (`restoreSessionState`): the
startup guard now only forces the companion minor mode from an explicit
`snap.MinorMode == "companion"`; a bare `snap.AgentDrivenEnabled` restores
agent-driven *tool availability* without stamping the companion minor-mode
label (agent-driven tools on ≠ companion minor mode).

Tests (`internal/app/team_companion_teardown_test.go`):
`TestTeamReviewController_OffDisablesAgentDriven` (RED→GREEN),
`TestRestoreSessionState_AgentDrivenAloneDoesNotForceCompanion` (RED→GREEN),
`TestRestoreSessionState_CompanionMinorModeRestores` (guards the legit
explicit-companion restore). Gates (run separately): go vet ✓; staticcheck
./internal/app ✓; gocognit -over 15 / gocyclo -over 12 on changed files ✓;
go test -count=1 -race -cover ./internal/app ✓ (55.3%) and ./core/team ✓.
Commit `21e1198` ("fix(teams): companion no longer stuck on after team use /
restart").

**Follow-up (non-blocking):** the footer only learns the minor-mode label via
`SetMinorMode` (emitted by `/companion:on|off` and startup restore), never by
team apply — syncing the footer label live on team activate/deactivate is
possible polish, not required for the stuck-state fix.

---

### BUG: Config → Teams navigation never builds a history stack — ESC anywhere in Teams exits the whole menu to root

**Status:** FIXED — implemented, tested, validated, archived.

**Symptom:** In `/config` → Teams, drilling into a team (detail view) or its
Description field and pressing ESC (or completing an edit and navigating
back) dropped the user **out of the config menu entirely** (back to the root
TUI) instead of returning to the Teams list / team detail.

**Root cause:** the config menu drives `back()` off a history stack
(`configMenu.open()` pushes, `back()` pops), but the entire Teams flow never
pushed onto it: `showSubMenu("teams")` called `openTeams` **directly**
(handler map in `core/commands/config.go:218`), `openTeams` → team selection
called `m.openTeamDetail(name)` directly, and `openTeamDetail` → "description"
called `m.promptTeamField(...)` directly. Net effect: `len(m.history) == 0`
for the whole Teams session, so any `m.back()` hit the empty-history branch →
`m.current = nil` → menu closed to root. Same defect affected
`openOrchestrator` and `openGoalsRetention`, which also bypassed `m.open(...)`.

**Resolution:** (a) `core/commands/config.go`: added `openTeamsMenu` /
`openOrchestratorMenu` / `openGoalsMenu` wrappers that push the root page via
`m.open(...)`; the submenu handler map now points at them. (b)
`core/commands/config_teams.go`: `openTeams` opens the team detail via
`m.open(...)`; `openTeamDetail` opens each sub-page (description / review /
gates / members / remove) via `m.open(...)`; `promptTeamField` now sets
`m.current` and returns via `m.back()`; the review/gates completion callbacks
return to the pushed detail via `m.back()` instead of re-invoking
`openTeamDetail` directly.

Tests: `TestConfigMenu_TeamsNavigationHistory`,
`TestConfigMenu_TeamDetailEscReturnsToList`,
`TestConfigMenu_TeamDescriptionEscReturnsToDetail` — all RED before (ESC
exited the menu), GREEN after. Gates (run separately): go vet ./... ✓;
staticcheck ./core/commands ✓; gocognit -over 15 / gocyclo -over 12 on
changed files ✓; go test -count=1 -race -cover ./core/commands ✓ (58.3%).
Commit `411600c` ("fix(config): Teams/Orchestrator/Goals submenus now build a
back-history stack").

### BUG: Team/model activation binds a model whose temperature the endpoint rejects — hard 400 on first turn

**Status:** FIXED — implemented, tested, validated, archived.

**Log:** `/Users/muaddib/dev/localtest/.goa/exports/goa-export-20260812-121044.zip`

**Symptom:** After activating team `Local (kimi-code)` (main model
`google/gemma-4-e4b`), sending any prompt (e.g. "Create simple html
tic-tac-toe") fails immediately:

```
Error: 400 - invalid temperature: only 1 is allowed for this model
[error] The LLM request failed. LLM request failed (not retryable):
  {"error":{"message":"invalid temperature: only 1 is allowed for this model","type":"invalid_request_error"}}
```

The turn is dead — no retry, session stuck until the model/team is changed.

**Root cause — UPDATED after deeper investigation (the real bug is the team
provider-resolution bug, below):**
- `config/user.yaml`: model `google/gemma-4-e4b` sets `temperature: 0.2`
  **and `provider: lmstudio`**. The active team `Local` binds this model as
  its main member **without a `provider:` override** (`main: {model: ...}`).
- Team activation (`teamSessionController.SwitchModel`,
  `internal/app/team_adapters.go:28`) resolves an empty member provider by
  keeping the **current** `ActiveProvider` (`kimi-code`) instead of the
  model's **own configured provider** (`lmstudio`). So the session stays on
  the kimi-code endpoint while sending `model: google/gemma-4-e4b` and that
  model's `temperature: 0.2`.
- `logs/http.jsonl`: POST to `…/coding/v1/chat/completions` (kimi-code) for
  `google/gemma-4-e4b` returns **400** `invalid temperature: only 1 is
  allowed for this model`.
- So the 400 is a **symptom of the team sending the model to the wrong
  provider**. Two independent defects contribute:
  1. **Primary: team does not select the model's provider** (see the dedicated
     bug entry "Team activation does not switch to the member model's
     provider"). Fixing that routes gemma → lmstudio, where `temperature: 0.2`
     is valid, and the 400 never happens.
  2. **Secondary / safety net: no `supports_temperature` capability + opaque
     error.** Even on the wrong endpoint, Goa should not die on a fixed-temp
     rejection without guidance. This part is FIXED (see below).

**Why it's a Goa bug (not just user config):**
1. Goa lets a model be configured with a temperature its endpoint cannot
   accept, with no validation at add/edit/activation time.
2. The failure surfaces only at the first LLM turn as an opaque 400, after the
   team/model is already active — the user can't discover it earlier.
3. The error is marked "not retryable" but Goa offers no actionable guidance
   (e.g. "model X requires temperature=1; fix in /config → Models").

**Fix directions (choose in plan):**
- **A. Validate at config time:** when a model's endpoint/provider is known to
  constrain sampling params, reject or warn on an out-of-range `temperature`
  in `/config → Models` and on `/team` activation. Needs a per-provider /
  per-model capability descriptor (which params are allowed / forced).
- **B. Clamp/omit at request build:** if the provider declares `temperature`
  fixed (=1), omit the field or coerce to the allowed value and log a notice
  instead of erroring the whole turn.
- **C. Better error surfacing (minimum):** detect the
  `invalid temperature: only 1 is allowed` shape and render an actionable
  message naming the model + the exact setting to change, plus a `/config`
  pointer — instead of a raw 400.
- Likely correct: **B + C** (don't die; tell the user what was coerced), with
  **A** as the proper long-term guard once provider capabilities exist.

**Test approach:**
- Unit: request-builder test — a model with `temperature: 0.2` against a
  provider that only allows `1` must either send `1`/omit (with a logged
  notice) or produce a clear pre-flight error, never a dead 400 turn.
- Unit: config validation flags a disallowed temperature when the provider
  capability is known.
- Error-path: the `invalid temperature` 400 body maps to an actionable user
  message (assert the rendered text names the model + setting).
- E2E (interactive shell): configure a gemma-style model on a fixed-temp
  endpoint, activate via team, send a prompt → no dead turn; notice shown.

**Validation steps:**
- Reproduce against the kimi-code (or a stub fixed-temperature) endpoint.
- Gates (each run separately): `go vet ./...` · `staticcheck ./...` ·
  `gocognit -over 15 .` · `gocyclo -over 12 .` ·
  `go test -count=1 -race -cover ./...`.
- Confirm the real terminal output no longer shows the raw 400 box.

**Safety-net fix applied (B + C), on the ACTIVE protocol path:**
- `internal/agentic/provider/schema/variant.go`: added
  `CompatFlags.SupportsTemperature *bool` — the wire quirk as data, matching
  the variant-profile design.
- `internal/agentic/provider/schema/variants/kimi-code.json`:
  `"supports_temperature": false` — kimi-code rejects any temperature but its
  fixed default, so the field is now omitted.
- `internal/agentic/provider/protocol/openai_completions.go`: new
  `openAICompletionsCompat.SupportsTemperature` (default true, set from the
  profile); `buildOpenAIParams` omits `temperature` when it is false so the
  endpoint applies its own default instead of returning HTTP 400.
- `internal/agentic/retry_classify.go` (`actionableHint`): a
  fixed-temperature rejection ("invalid temperature … allowed") now renders an
  actionable hint ("remove the model's temperature setting (/config → Models)
  or set it to the allowed value") instead of a bare 400.
- Tests: `TestBuildOpenAIParams_OmitsTemperatureWhenUnsupported` /
  `..._SendsTemperatureWhenSupported` (unit, RED→GREEN),
  `TestKimiCodeProfileDisablesTemperature` (end-to-end: the real kimi-code
  profile resolves to SupportsTemperature=false and the gemma temperature is
  omitted), `TestFormatFatalStreamMessage_TemperatureHint` (actionable error).
- Gates green: `go vet ./internal/agentic/...` ✓ · `staticcheck` ✓ ·
  `gocognit -over 15` / `gocyclo -over 12` on changed files ✓ ·
  `go test -count=1 -race -cover ./internal/agentic/provider/protocol` ✓ (67.0%).
- **Remaining for full close:** ~~fix the PRIMARY team-provider-resolution bug~~
  (DONE) + ~~run the requested live e2e validation~~ (DONE — see below).

**Notes / open questions:**
- The temperature constraint is per-endpoint-model on kimi-code; the
  capability descriptor now lives in the variant profile (`supports_temperature`).
- The primary defect (team not selecting the model's provider) is tracked in
  the next bug entry.

**Live e2e validation (2026-08-12):** PASSED. Ran the local LM Studio team
(main = `google/gemma-4-e4b`, companion = `qwen/qwen3.5-9b`, review =
framework) in a temp project via PTY against the real binary. The team now
resolves each member to its configured provider: all 12 LLM requests in the
session `logs/http.jsonl` went to `http://localhost:1234/v1/chat/completions`
(LM Studio) — zero to any other endpoint — so gemma's `temperature` was sent
to an endpoint that accepts it and no 400 occurred. The original failure
(gemma mis-routed to kimi-code → 400) is eliminated at the root.

---

### BUG: Team activation does not switch to the member model's provider — requests go to the wrong endpoint

**Status:** FIXED — implemented, tested, validated, archived.

**Symptom:** Activating a team whose main (or companion) member names a model
that belongs to a *different* provider than the current session keeps the
session on the **current** provider and sends the member's model ID + settings
to that wrong endpoint. Example (from the export): session was on `kimi-code`,
team `Local` main = `google/gemma-4-e4b` (whose model config is
`provider: lmstudio`), yet the request went to
`https://api.kimi.com/coding/v1` with `model: google/gemma-4-e4b` → 400.

**Root cause (traced):** `teamSessionController.SwitchModel`
(`internal/app/team_adapters.go:28`):
```go
pid := providerID
if pid == "" {
    pid = c.cfg.ActiveProvider   // ← falls back to CURRENT provider
}
```
A team member with no explicit `provider:` (`main: {model: ...}`, the common
case) passes `providerID == ""`, so the controller reuses `ActiveProvider`
instead of the model's **own configured provider**. The `/model` command does
this correctly via `providerIDForModel` (`core/commands/model.go:564`), which
returns `ModelConfig.ProviderID`; the team path is missing the equivalent
lookup. Affects the main member (session model) and any pool member whose
config is resolved the same way.

**Why it's a Goa bug:** team activation must bind the member's model **on the
provider that model is configured for**. Reusing the session's current
provider silently mis-routes the model and its parameters.

**Fix plan:**
- In `teamSessionController.SwitchModel` (or the manager's
  `applyMainMemberLocked` / pool member config), when the member's `Provider`
  is empty, resolve it from the model's config entry
  (`ModelConfig.ProviderID` for `member.Model`), falling back to
  `ActiveProvider` only when the model names no provider. Mirror
  `providerIDForModel` semantics; keep the explicit-`provider:` override
  highest priority.
- Apply the same resolution to pool members (`teamMemberApplier.MemberConfig`
  sets `ProviderID: rm.Member.Provider`, which is "" in the common case) so
  companion/worker members also land on the right provider.
- Restore path: the snapshot/restore already records prior provider+model, so
  deactivation is unaffected.

**Test approach:**
- Unit (`internal/app`): a team whose main member has no `provider:` and whose
  model is configured with `provider: lmstudio`, activated while the session
  is on `kimi-code` → after activation `ActiveProvider == "lmstudio"` and the
  resolved model's BaseURL is the lmstudio endpoint (not kimi-code).
- Unit: an explicit member `provider:` still wins over the model's configured
  provider; a member model with no configured provider falls back to
  `ActiveProvider`.
- Unit (pool): `MemberConfig` for a companion with no `provider:` resolves the
  companion model's configured provider.
- Regression: the temperature-400 scenario — gemma (provider lmstudio) on a
  kimi-code session routes to lmstudio, where `temperature: 0.2` is accepted.

**Validation steps:**
- Gates (each run separately): `go vet ./...` · `staticcheck ./...` ·
  `gocognit -over 15 .` · `gocyclo -over 12 .` · `go test -count=1 -race -cover ./...`.
- Live e2e per the validation request entry below (gemma codes on lmstudio,
  qwen reviews).

**Fix applied:**
- `internal/app/team_adapters.go` (`teamSessionController.SwitchModel`):
  when the member's provider is empty, resolve the model's own configured
  provider via the new `providerIDForModelConfig` helper (mirrors
  `providerIDForModel` semantics in `core/commands/model.go`); fall back to
  `ActiveProvider` only when the model names no provider. Explicit member
  `provider:` remains highest priority.
- Same resolution in `teamMemberApplier.MemberConfig`: pool members with no
  `provider:` now resolve their model's configured provider so the pool's
  `ProviderModelFactory` lands companion/worker members on the right
  endpoint; models with no configured provider keep `ProviderID` empty
  (legacy pool default wiring preserved).
- Restore path unaffected: snapshot/restore already records prior
  provider+model.
- Tests (`internal/app/team_provider_resolution_test.go`, RED→GREEN through
  the full non-headless path with a recording ProviderManager):
  `TestTeamSessionController_SwitchModelResolvesModelProvider` (gemma on
  lmstudio activated from a kimi-code session → ActiveProvider==lmstudio,
  SetActive(lmstudio, gemma-local)), `..._SwitchModelExplicitProviderWins`,
  `..._SwitchModelFallsBackToActiveProvider` (model with no configured
  provider), `TestTeamMemberApplier_MemberConfigResolvesModelProvider`
  (companion), `..._MemberConfigExplicitProviderWins`,
  `..._MemberConfigNoConfiguredProviderStaysEmpty`.
- Gates green (each run separately): `go vet ./...` ✓ · `staticcheck ./...`
  pre-existing only, none from changed files ✓ · `gocognit -over 15 .` /
  `gocyclo -over 12 .` none on changed files ✓ · `go test -count=1 -race
  -cover ./...` ✓ (81 packages ok, 0 FAIL; internal/app 55.7%, core/team
  76.1%, core/commands 58.3%).
- **Remaining for full close:** ~~live e2e validation~~ (DONE — see below).

**Live e2e validation (2026-08-12):** PASSED — this was the PRIMARY fix under
test. In the live LM Studio team run, gemma (main) and qwen (companion) each
resolved to their own configured provider. Session `logs/http.jsonl` shows all
12 requests on `http://localhost:1234/v1/chat/completions` with the correct
per-request model (`google/gemma-4-e4b` for the 4 main coding turns,
`qwen/qwen3.5-9b` for the 9 companion review turns), interleaved
gemma→qwen→gemma across turns — the exact review→act loop. The dead `other`
endpoint (127.0.0.1:1) configured as a tripwire was never hit. Session log
line: `{"event":"companion_started","model":"qwen/qwen3.5-9b"}`.

---

### BUG: Activating a team persists `teams.active` to the HOME config instead of the project (local) config

**Status:** FIXED — implemented, tested, validated, archived.

**Symptom:** Selecting/activating a team (`/team:<name>` or /config → Teams →
Active team) writes `teams.active` to the **home** config
(`~/.goa/config.yaml`), so the team selection leaks across all projects
instead of being scoped to the current project.

**Root cause (traced):** `persistActiveTeam` (`core/commands/team.go:379`)
calls `ctx.ConfigSaver.SaveHomeFieldValue([]string{"teams","active"}, name)`
— explicitly the home field. The config cascade
(embedded → home → project `.goa/` → local `.goa/config.local.yaml`) means a
home-level `teams.active` applies to every project. The expected scope for a
per-project team binding is the project (`.goa/config.yaml`) or local
(`.goa/config.local.yaml`) layer. Note `/model` persists `active_model` to
home by design (a global default), but a team is a project-scoped working
set, so the same default is surprising.

**Design decision (recorded):** `teams.active` persists to the project
**LOCAL** layer (`.goa/config.local.yaml` — gitignored, per-developer), NOT
the committed project `.goa/config.yaml`. A team is a project-scoped +
per-developer working set: the home layer leaks the selection across all
projects, and the committed project layer would dirty shared config with a
personal selection. Team *definitions* stay in the home config (user level).

**Fix applied:**
- `config/loader.go`: new `SaveLocalFieldValue(path, value)` on
  `ConfigSaver`/`CascadeLoader`, backed by a new `editLocalConfig`; the
  shared `editConfigFile` helper gained a filename parameter so home
  (`config.yaml`), project (`config.yaml`), and local (`config.local.yaml`)
  edits share one read-modify-write path (same `writeMu` serialization,
  minimal-document creation, field-scoped merge).
- `core/commands/team.go`: `persistActiveTeam` now calls
  `SaveLocalFieldValue(["teams","active"], name)` instead of
  `SaveHomeFieldValue` (covers `/team:<name>` and `/team:off`).
- `core/commands/config_teams.go`: `/config → Teams → Active team`
  (`openTeamsActive`) had the same leak via `saveTeamsSection` (whole
  "teams" section to home); it now persists only `teams.active` via the new
  `saveTeamsActive` → local layer. Definition CRUD still saves the section
  to home via `saveTeamsSection`.
- Cascade resolution order unchanged: embedded → home → project → local →
  env → flags; the local layer already wins for `teams.active` (most
  specific). No migration of stale home-layer values (harmless shadowed
  leftovers; the local layer overrides them on next activation).
- Test fakes (`core/commands/config_test.go`, `core/agentmanager_test.go`)
  gained `SaveLocalFieldValue` to satisfy the extended interface.

**Tests (RED→GREEN):**
- `config/loader_local_save_test.go` (new): `TestSaveLocalFieldValueWritesLocalLayerOnly`
  (local file carries the value; home + project configs untouched),
  `TestSaveLocalFieldValueCreatesFile`,
  `TestSaveLocalFieldValuePreservesOtherLocalSettings`,
  `TestCascadeLocalTeamsActiveResolvesOnStartup` (startup cascade resolves
  the local-layer value on reload).
- `core/commands/team_test.go`: `TestTeamCommand_ActivatePersistsToLocalLayer`
  (temp HOME + temp project dir: local file carries `active: alpha`; home
  carries only definitions; committed project config untouched; reload
  through the cascade resolves `alpha`),
  `TestTeamCommand_OffPersistsToLocalLayer` (clears to `active: ""` locally,
  home untouched),
  `TestConfigMenu_TeamsActivePersistsToLocalLayer` (the /config Active-team
  path writes only the local layer).

**Validation:**
- Gates (each run separately): `go vet ./...` exit 0 · `staticcheck ./...`
  pre-existing findings only (none in changed files) · `gocognit -over 15 .`
  0 findings in changed files (43 pre-existing elsewhere) · `gocyclo -over 12 .`
  0 findings in changed files (64 pre-existing elsewhere) ·
  `go test -count=1 -race -cover ./...` exit 0 (81 packages ok, 0 FAIL;
  config 79.0%, core/commands 58.5%).
- Interactive smoke (project A activate → home unchanged, project B
  unaffected) not run; covered by the unit tests above (temp HOME +
  per-project dirs).

(Already validated via unit tests with temp HOME + per-project dirs;
local-layer persistence confirmed. Archived per guideline 4.)

---

### VALIDATION REQUEST (e2e, live LM Studio): local team gemma+qwen writes tic-tac-toe

**Status:** VALIDATED — all 4 points pass (2026-08-12); archived.

**Environment (confirmed 2026-08-12):** LM Studio at `http://localhost:1234/v1`
serves `google/gemma-4-e4b` and `qwen/qwen3.5-9b` (both local).

**Setup:**
1. A local team on the `lmstudio` provider: main = `google/gemma-4-e4b`,
   companion = `qwen/qwen3.5-9b`, review = `framework` (default every-turn).
2. A temp project with that local team selected (persisted per the
   team-save-location decision above).

**Scenario:** ask Goa to "write a tic-tac-toe in HTML".

**Must validate:**
1. **gemma does the coding** — the main turn runs on `google/gemma-4-e4b` via
   the lmstudio endpoint (not any other provider).
2. **qwen does the review** — the framework review runs on `qwen/qwen3.5-9b`,
   and gemma actions the review feedback (review → act loop visible).
3. **TUI shows the active model correctly** — footer/dialog reflect gemma as
   main and qwen as companion (and transitions as each runs).
4. **TUI shows the inter-model dialog** — the review request/verdict and the
   follow-up edits render correctly in the transcript.

**Method:** use the interactive shell / PTY against the real binary (bugs.md
guideline #5) and/or the `qa-e2e` skill against the local LM; capture actual
terminal output (not just logs).

**Result (2026-08-12):** PASSED on all 4 points. Ran against the real binary in
a PTY (guideline 5), temp project with the local team persisted to the project
local layer. Prompt: "Build a small html tic-tac-toe game and save it to
index.html". Evidence (`/export` bundle `logs/http.jsonl` + session jsonl +
live PTY capture):

1. **gemma does the coding via lmstudio** — all 12 requests to
   `http://localhost:1234/v1/chat/completions`; requests 1-2 (`gemma-4-e4b`,
   `finishReason: tool_calls` → `lastIsToolResult: true`) drove the `write`
   tool. `index.html` produced (working game, `checkWin` logic). The dead
   `other` endpoint (127.0.0.1:1) was never connected.
2. **qwen reviews, gemma actions** — HTTP interleave gemma (reqs 1-2, 6, 10) ↔
   qwen (reqs 3-5, 7-9, 11-12); qwen's first request fires right after the main
   turn (review: framework). Session log:
   `{"event":"companion_started","model":"qwen/qwen3.5-9b"}`. Loop closed:
   gemma's thinking block acknowledged "the companion agent stating that the
   review of the index.html file is complete" and concluded the task.
3. **TUI shows both models** — footer `⛃ Local (lmstudio) google/gemma-4-e4b
   • [∞]` throughout; Ctrl+T team dialog: "Team active: Local (gemma-local +
   qwen-local · review:framework)… Base: google/gemma-4-e4b · Companion:
   qwen/qwen3.5-9b"; transcript shows `▾ companion · cycle 1/2` sections.
4. **Inter-model dialog renders** — qwen's review surfaced as ❓ Clarifications
   cards; gemma answered and acted. Companion errors (LM Studio model-swap
   timeout / fetch failed) rendered fail-open in the transcript without
   breaking the main flow.

---

### BUGFIX: Compression config rework — opt-in semantics, menu rewrite, dead-row bug (bugs.md §1–§9)

**Status:** IMPLEMENTED — all micro-tasks 01–09 done, gates green, archived.

**Problem:** the compression engine treated an all-zero SDK config as "hard tier ON at the implicit
default 95%" and degraded the soft layer to zero-LLM micro; the shipped default.yaml relied on that
implicit behavior; the /config Compression menu was a 23-row grab-bag with read-only derived rows
(`_derived_*`) whose selection fell through the opener map and silently closed the overlay back to
the main menu (the "dead row" regression); on-error recovery strategy was not user-configurable.

**Resolution:**
- **Opt-in semantics:** every proactive layer is 0 = disabled (no implicit default-on); the
  embedded `config/configs/default.yaml` now sets `thresholds.hard_percent: 95` explicitly with
  summarize as the hard method; negative values remain the legacy disable spelling; valid levels are
  0/-1 or 5..100 in 5% steps.
- **All-methods soft layer:** the soft layer accepts any strategy (`micro`/`tool_elision`/`selective`/
  `hybrid`/`summarize`); the SDK default when unset is still micro.
- **Configurable on-error strategy:** `context_compression.on_error_strategy` (default hybrid) with
  dispatch: summarize → Compact only; tool_elision → elision only; selective → selective only; micro
  → forced micro; hybrid → elision+selective then Compact only when still ≥ escalation.
- **Menu rewrite:** `settingCompression` now shows exactly 5 main rows (Soft ceiling %, Soft ceiling
  method, Hard ceiling %, Hard ceiling method, On error) + Advanced… submenu (trigger layer, cache
  gate, max tokens, preserve recent turns, micro gates, per-model overrides, Enabled toggle); all
  derived rows removed; ceiling pickers are 0 + 5..100 step 5; method pickers offer all 5 strategies;
  On error picker offers off + 5 strategies (off → `on_context_error=false`). Every row opens a
  picker or applies a set — zero dead rows.
- **Tests:** on-error dispatch table (`internal/agentic/agent_onerror_dispatch_test.go`), dead-row
  regression (`core/commands/config_compression_deadrow_test.go`), filmstrip UI validation
  (`internal/app/config_compression_filmstrip_test.go`) proving the 6-row menu renders and every row
  opens its picker (race-clean ×3 full-package runs).
- **Engine fix:** `tui.RenderNow` snapshots via `ApplySync` so test-goroutine renders cannot
  interleave with async selector apply-callbacks that add overlays via `engine.Apply`.

**Validation:** `go vet ./...` clean; `go test -count=1 -race -cover ./...` green (81 pkgs ok, 0 FAIL);
verify command exit 0; gocognit/gocyclo no new violations (all >15 findings pre-exist on HEAD).
