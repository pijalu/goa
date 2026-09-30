#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Copyright (C) 2026 Pierre Poissinger

"""Round-2 matrix: real tool-call streams and goa's unbounded (no max_tokens) shape."""
import json
import subprocess
import sys
import time

PROBE = "/tmp/zai-probe/zai_probe.py"
OUT = "/tmp/zai-probe/matrix2.jsonl"
MODEL = "glm-5.3-flash"
RUNS = 3

jobs = []
for run in range(1, RUNS + 1):
    for surface in ("openai", "anthropic"):
        for mode, think in [("force-tool", "off"), ("force-tool", "on"),
                            ("unbounded-long", "off"), ("unbounded-long", "on"),
                            ("force-tool-unbounded", "on")]:
            jobs.append((surface, mode, think))

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
        m = rec.get("markers", {})
        print(surface, mode, think, rec.get("verdict"),
              "fin=%s" % m.get("finish_reason"), "tool=%s" % m.get("tool_calls"),
              "total=%s" % rec.get("total_s"), rec["wall_s"], flush=True)
print("MATRIX2_DONE", flush=True)
