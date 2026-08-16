<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Export review tooling for goa diagnostic bundles

`scripts/export/` contains a dependency-free Python analyzer for the
diagnostic zip bundles goa writes via its export command
(`.goa/exports/goa-export-YYYYMMDD-HHMMSS.zip`, built by
`internal/logs/export`).

> No Python dependencies beyond the standard library (`zipfile`, `json`).
> The bundle is read in place — no extraction needed. Python 3.8+.

## Files

| File | Purpose |
| --- | --- |
| `review_export.py` | Parse a bundle and report session metadata, the HTTP request timeline, cache-miss forensics with per-miss verdicts, and trace anomalies. |

## Quick start

```sh
# human-readable report
python3 scripts/export/review_export.py .goa/exports/goa-export-20260816-090406.zip

# include mutated-message previews for prefix-mutation verdicts
python3 scripts/export/review_export.py export.zip --verbose

# machine-readable output
python3 scripts/export/review_export.py export.zip --json

# tune the idle-eviction threshold (default 120s)
python3 scripts/export/review_export.py export.zip --ttl 300
```

Exit codes: `0` = every detected cache miss is explained by client-side
evidence (or no miss was recorded); `1` = at least one miss is unexplained;
`2` = bundle unreadable. The `1` case is usable as a regression gate.

## Cache-miss verdicts

For each entry of `logs/cache_miss_requests.json` (produced by the
cache-forensics journal in `internal/agentic/provider/cache_forensics.go`,
which retains the request that missed plus the conversation sequence's
request before it), the analyzer reports:

* token delta (`prev_cache_read_tokens` → `cache_read_tokens`),
* the time gap between the two retained requests,
* sequence-number gaps (requests that ran in between but were not retained),
* the exact message index where the two request bodies first diverge.

The first matching heuristic explains the miss, in priority order:

| Verdict | Meaning | Action |
| --- | --- | --- |
| `prefix-mutation` | The bodies diverge **before** the missing request's last message: goa rewrote already-sent history. | Real client bug — the mutated message preview (`--verbose`) points at what changed. |
| `interleaved-seq(N missing)` | N request(s) ran between the two retained ones (planner, sub-agent, retry sharing the session). They are not in the report because only the missing request's predecessor is retained. | Check what the interleaved request was; it may have displaced the provider cache entry. |
| `ttl/idle(>Ns)` | The gap between the requests exceeds the TTL threshold: provider-side idle eviction is plausible. | Expected on long pauses; tune with `--ttl`. |
| `provider-eviction?` | Prefix stable, short gap, adjacent sequence numbers: nothing on the client explains the miss. | Server-side eviction or cache-block granularity; report to the provider. |

A divergence exactly at the end of the earlier request
(`divergence_is_append=True`) is the healthy case: the conversation only
grew, so the client did not cause the miss by rewriting history.

## Other bundle artifacts

The analyzer also surfaces:

* `manifest.json` — session id, goa version, provider/model, issue
  description, files that could not be collected (`missingFiles`).
* `logs/http.jsonl` — one row per API request: timestamp, duration, gap to
  the previous request, message count, last role, finish reason.
* `diagnostics/trace.json` — `anomalies` detected by goa's own trace
  builder (e.g. a tool result never followed up by the model).
