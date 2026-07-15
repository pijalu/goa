<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

The `python` tool runs Python code inside Goa using the embedded gpython
interpreter. Each call gets a fresh interpreter context; stdout and stderr are
captured and returned.

Use it for quick calculations, data transformation, or small scripts without
leaving the agent conversation. The interpreter supports the Python 3.4
language subset implemented by gpython plus its stdlib modules (note: f-strings
are auto-rewritten to `%` formatting).

Output is truncated automatically when it exceeds the configured limits; the
full output is saved to a file in those cases.

The tool is enabled by default. Disable it via `/config` → Tools, or with
`/tools python:off`.

## File access

The embedded interpreter ships a **curated, jail-aware `os` module** (and
`os.path`) so typical Python file code runs unmodified. The stock gpython `os`
is both too sparse (no `os.walk`, `os.stat`, `os.path`) and unsafe
(`os.system` / `os._exit`), so it is shadowed by this curated version.

Available under standard names:

| API | Notes |
| --- | --- |
| `os.getcwd()`, `os.listdir(path=".")` | cwd is the project root |
| `os.walk(top)` | yields `(dirpath, dirnames, filenames)` tuples, top-down |
| `os.stat(path)` / `os.lstat` | returns a result with `.st_size`, `.st_mtime`, `.st_mode`, `.st_nlink` |
| `os.mkdir`, `os.makedirs(exist_ok=...)`, `os.remove`/`os.unlink`, `os.rmdir`, `os.removedirs` | |
| `os.path.join/exists/isfile/isdir/getsize/abspath/basename/dirname/splitext/split/relpath/normpath/isabs` | |
| `os.environ`, `os.getenv(name, default=None)`, `os.getpid()`, `os.sep`, `os.name` | |
| `open(file, mode="r")` | built-in, jail-confined; supports `read`/`write`/`close` and `with` |

Example — the idiom that previously failed (`os.walk` + `os.path.join` +
`os.stat(...).st_size`):

```python
import os
total = 0
for dirpath, _, filenames in os.walk("."):
    for name in filenames:
        if name.endswith(".go") and not name.endswith("_test.go"):
            total += os.stat(os.path.join(dirpath, name)).st_size
print("bytes:", total)
```

### Jail

When `tools.python.jail` is true (the default in solo autonomy), every file
operation is confined to the project directory and below; paths that escape it
raise `PermissionError`. When the jail is off, relative paths still resolve
against the project root, but absolute paths outside it are permitted.

## Supported stdlib modules

In addition to the curated `os` module, the embedded interpreter provides these
Go-backed stdlib modules. They are registered globally, so they are available
in every `python` tool invocation.

| Module | Notes |
| --- | --- |
| `re` | `compile`, `search`, `match`, `findall`, `sub`, `split`, `escape`; `I`/`IGNORECASE`; `Pattern` and `Match` objects. Backed by Go's RE2 engine: no lookarounds or backreferences, and `re.sub` uses literal replacement only. |
| `json` | `loads`, `dumps(obj, indent=None)`. Supports dicts with string keys and JSON scalar types. |
| `datetime` | `datetime`, `date`, `timedelta`; `now`, `today`, `fromtimestamp`, `strptime`, `isoformat`, `strftime`; basic arithmetic with `timedelta`. |
| `random` | `random`, `randint`, `choice`, `shuffle`, `sample`, `uniform`, `seed`. |
| `hashlib` | `md5`, `sha1`, `sha256`, `sha512`; `Hash.hexdigest()` / `Hash.digest()`. |
| `base64` | `b64encode`, `b64decode`, `urlsafe_b64encode`, `urlsafe_b64decode`. Returns strings, not bytes. |
| `urllib.parse` | `quote`, `quote_plus`, `unquote`, `unquote_plus`, `urlencode`, `urlparse`, `urlunparse`, `parse_qs`, `parse_qsl`. |
| `collections` | `Counter` with `update`, `subtract`, `elements`, `most_common`, and dict-like access. |
| `itertools` | `count`, `cycle`, `repeat`, `chain`, `islice`, `combinations`, `permutations`, `product`. |
| `stat` | Constants (`S_IFREG`, `S_IFDIR`, permission bits) and `S_IMODE`/`S_IFMT`. |

Example — regex and JSON in one snippet:

```python
import re, json
m = re.search(r"\d+", "order 42")
print(json.dumps({"order_id": int(m.group())}))
```

### Intentionally absent

For safety, the curated module **does not expose** `os.system`, `os._exit`,
`os.exec*`, `os.open`, or other shell/raw-fd primitives. Use the `bash` tool
for shells. The confinement is lexical (like the bash jail): symlinks and other
stdlib modules with direct filesystem access (e.g. `glob`, `tempfile`) are not
confined — treat the jail as best-effort for the common file idioms.
