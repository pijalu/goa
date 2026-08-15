<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# run_code — code-mode dispatch (gap TL7)

Submit one program that performs multiple tool sub-calls in a jailed worker.
The program runs in the same embedded gpython interpreter as the `python` tool,
with a jail that is ON by default: `os` file operations are confined to the
project directory, and the unsafe primitives (`os.system`, subprocess, sockets)
are absent. Opt out per-project with `tools.run_code.jail: false`; sub-calls
still respect their own tools' jails regardless.

## Binding

The program receives a `tools` object with one attribute per registered tool
(`tools.read`, `tools.bash`, `tools.search`, ...). Calling `tools.name({...})`
dispatches that sub-call through the agent's **complete guarded pipeline** —
the exact same path a direct tool call traverses:

1. mode guard policy
2. solo-mode policy
3. user confirmation (when the autonomy level requires it)
4. registry lookup
5. tool execution (with the tool's own jail/sandbox configuration)

A sub-call can therefore never bypass a permission rule, a confirmation prompt,
or the per-tool jail. The tool's result text is returned to the program as a
Python string; a failed or denied sub-call raises an exception whose message
carries the pipeline's error text. `run_code` itself is not exposed as a
binding, so a program cannot recurse into the outer transport.

## Curated result

Only what the program prints comes back to the model, so curate the output.
There is no async/await in the embedded interpreter (Python 3.4 subset), so
calls are synchronous:

```python
hits = tools.search({"query": "TODO"})
tools.write({"path": "notes.txt", "content": "TODO count: " + str(len(hits))})
print("found", len(hits), "markers")
```

## Durable dispatch log

Every sub-call is appended to a durable per-run JSONL dispatch log under
`.goa/dispatch/<run_id>/dispatch.jsonl` with the tool name, exact arguments,
timing, and outcome. Oversized sub-call results are spill-capped: the log entry
stores a bounded head/tail preview and the full result is saved under
`.goa/dispatch/<run_id>/spill/` with the path recorded in the entry.
