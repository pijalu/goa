#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Copyright (C) 2026 Pierre Poissinger

"""Matrix runner: writes one JSON line per probe to /tmp/zai-probe/matrix.jsonl."""
import json
import subprocess
import sys
import time

PROBE = "/tmp/zai-probe/zai_probe.py"
OUT = "/tmp/zai-probe/matrix.jsonl"
MODEL = sys.argv[1] if len(sys.argv) > 1 else "glm-5.3-flash"
RUNS = int(sys.argv[2]) if len(sys.argv) > 2 else 3

OPENAI_MODES = ["plain", "tools", "tools-noflag", "long", "long-tools", "long-tools-noflag"]
ANTHROPIC_MODES = ["plain", "tools", "long", "long-tools"]

jobs = []
for run in range(1, RUNS + 1):
    for mode in OPENAI_MODES:
        jobs.append(("openai", mode, "off"))
    for mode in ANTHROPIC_MODES:
        jobs.append(("anthropic", mode, "off"))
# mirror goa's default: thinking enabled on the OpenAI-compat surface
for run in range(1, RUNS + 1):
    for mode in ["plain", "tools", "long-tools"]:
        jobs.append(("openai", mode, "on"))

with open(OUT, "w") as fh:
    for surface, mode, think in jobs:
        t = time.monotonic()
        p = subprocess.run([sys.executable, PROBE, surface, MODEL, "1", mode, think],
                           capture_output=True, text=True)
        line = p.stdout.strip().splitlines()[-1] if p.stdout.strip() else ""
        try:
            rec = json.loads(line)
        except Exception:
            rec = {"surface": surface, "mode": mode, "think": think, "model": MODEL,
                   "verdict": "probe_error", "stderr": p.stderr[-300:]}
        rec["wall_s"] = round(time.monotonic() - t, 1)
        fh.write(json.dumps(rec) + "\n")
        fh.flush()
        print(surface, mode, think, rec.get("verdict"), rec.get("wall_s"), flush=True)
print("MATRIX_DONE", flush=True)
