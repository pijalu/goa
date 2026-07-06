<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Orchestrator Tabbed-Run UI — Smoke Filmstrip (2026-07-06)

ANSI-free, reproducible Filmstrip captured from the production component tree driven
by a neutral event sequence (start → 2 agents → stream → stats → finish). Validates:
persistent Stats/per-agent/All tabs, the provider/model/think/in/out/CH columns, the
steer-all prompt, the Ctrl+x tab-picker hint, and live per-frame updates. Regenerated
via `TestSmokeCaptureOnly` (internal/app).

```
=== step 0: pre-run ===
+ ▄▄▄▄▄      ▄▄▄▄▄             ▄▄▄▄▄▄      ▄▄▄▄      ▄▄▄▄
+ ▄▄▄▄▄▄ ▄▄▄▄▄      ▄▄▄▄▄▄ ▄▄  ▄▄    ▄███ ████ ▄███ ████ ▄███ ████
+ ▄ ▄▄▄ ▄       ▄  ▄       ▄  ▄▄      ████ ████ ████ ████ ████ ████
+ ▄ ▄▄               ▄         ▄ ▄    ████ ████ ████ ████ ▄▄▄▄▄████
+ ▀▄   ▄▄▄     ▄  ▄ ▄     ▄          ▀███▄████ ▀███▄███▀ ████▄████
+ ▄▄▄▄▄▄ ▄   ▄▄▄▄▄▄      ▄     ▄▄▄▄ ████
+ ▄          ▄     ▀▀▀▀▀▀▀▀
+ ▄
+ ▄▄▄▄
+ goa coding agent vtest
+ Ctrl+C/D exit  |  / commands  |  Tab complete  |  ↑↓ history
+ ────────────────────────────────────────────────────────────────────────────────────────────────
+ ────────────────────────────────────────────────────────────────────────────────────────────────
+ .                                                                                             │
+ no-model
=== step 1: run_started ===
+ Ctrl+x tabs · /orchestrate:tab:<n>
+ no agents yet
+ objective: ship it
+ orchestration · fanout · running
+ orchestration: Stats │ All                                                                 [1/2]
+ role          (provider) model         think     in    out     CH
+ Σ in=0 out=0 CH=0 · turns=0
+ ─────────────────────────────────────────┨ steer all: ┠─────────────────────────────────────────
=== step 2: agent_started ===
+ coder         (google) gemma           off        0      0 -
+ orchestration: Stats │ coder │ All                                                         [1/3]
- no agents yet
- orchestration: Stats │ All                                                                 [1/2]
=== step 3: agent_started ===
+ orchestration: Stats │ coder │ reviewer │ All                                              [1/4]
+ reviewer      (lmstudio) qwen          medium      0      0 -
- orchestration: Stats │ coder │ All                                                         [1/3]
=== step 4: agent_message ===
=== step 5: agent_stats ===
+ coder         (google) gemma           off       40     12   1024
+ Σ in=40 out=12 CH=1024 · turns=1
- coder         (google) gemma           off        0      0 -
- Σ in=0 out=0 CH=0 · turns=0
=== step 6: agent_finished ===
=== step 7: run_finished ===
+ orchestration · fanout · complete
- orchestration · fanout · running
```
