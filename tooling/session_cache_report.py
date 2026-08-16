#!/usr/bin/env python3
"""session_cache_report.py — Provider prompt-cache timeline for a Goa session JSONL.

Parses a Goa session file (.goa/sessions/*.jsonl), extracts the per-request
`token_stats` events (prompt_n / cache_read_tokens / predicted_n) and the
flanking `context_stats` / tool events, and prints a per-request cache
timeline with miss flags.

A "miss" is reported two ways, mirroring Goa's detectors
(internal/app/stats.go cacheBustDropToleranceTokens = 1024 and
internal/agentic/provider/cache_forensics.go):

  BUST    cache_read dropped more than the 1024-token block-quantization
          wobble vs the previous request of the same conversation.
  MISS    cache_read == 0 (cold start — expected for request #1 of a
          conversation or after /clear + baseline reset).
  SHRINK  total prompt (prompt_n + cache_read) shrank vs the previous
          request by more than `shrink_tol` tokens — evidence of an in-place
          history rewrite (compaction / elision / injection).

Usage:
    python3 tooling/session_cache_report.py SESSION.jsonl [--full]
                                          [--shrink-tol N] [--bust-tol N]

    --full         print every request, not just the flagged ones
    --shrink-tol N total-shrink flag threshold in tokens (default 1000)
    --bust-tol N   cache-read drop tolerance in tokens (default 1024,
                   mirrors cacheBustDropToleranceTokens)

Exit status is always 0; this is a reporting tool, not a gate.
"""
from __future__ import annotations

import argparse
import json
import sys


def iter_events(path):
    with open(path, "r", encoding="utf-8") as fh:
        for ln, line in enumerate(fh, 1):
            line = line.strip()
            if not line:
                continue
            try:
                yield ln, json.loads(line)
            except json.JSONDecodeError:
                continue


def collect(path):
    """Return (requests, events) where requests is a list of dicts:
    {seq, line, prompt_n, cache_read, predicted, msgs, est_tokens, max_tokens}
    and events maps line -> short description of the nearest preceding
    content/tool event for annotation."""
    requests = []
    last_note = {}  # line -> note string, for annotation
    seq = 0
    prev_total = None
    for ln, ev in iter_events(path):
        t = ev.get("Type")
        if t == "token_stats":
            tm = ev.get("timings", {})
            seq += 1
            requests.append(
                {
                    "seq": seq,
                    "line": ln,
                    "prompt_n": tm.get("prompt_n", 0),
                    "cache_read": tm.get("cache_read_tokens", 0),
                    "predicted": tm.get("predicted_n", 0),
                    "prev_total": prev_total,
                }
            )
        elif t == "context_stats" and requests:
            cs = ev.get("context_stats", {})
            requests[-1]["msgs"] = cs.get("Messages")
            requests[-1]["est_tokens"] = cs.get("EstimatedTokens")
            requests[-1]["max_tokens"] = cs.get("MaxTokens")
            prev_total = requests[-1]["prompt_n"] + requests[-1]["cache_read"]
        elif t == "content" and not ev.get("IsDelta"):
            role = ev.get("Role", "?")
            text = (ev.get("Text") or "").replace("\n", " ")
            last_note[ln] = f"{role}: {text[:140]}"
        elif t == "tool_call":
            last_note[ln] = f"tool_call {ev.get('ToolName')}: {(ev.get('ToolInput') or '')[:120]}"
        elif t == "tool_result":
            res = ev.get("ToolResult") or ""
            last_note[ln] = f"tool_result {ev.get('ToolName')} len={len(res)}"
    return requests, last_note


def note_before(events, line):
    """Return the most recent annotation strictly before `line`."""
    best = None
    for ln in sorted(events):
        if ln >= line:
            break
        best = (ln, events[ln])
    return best


def main(argv=None):
    ap = argparse.ArgumentParser(description="Goa session cache-miss timeline")
    ap.add_argument("session", help="path to .goa/sessions/<id>.jsonl")
    ap.add_argument("--full", action="store_true", help="print every request")
    ap.add_argument("--shrink-tol", type=int, default=1000)
    ap.add_argument("--bust-tol", type=int, default=1024)
    args = ap.parse_args(argv)

    requests, events = collect(args.session)
    if not requests:
        print("no token_stats events found", file=sys.stderr)
        return 0

    print(f"# {args.session}")
    print(f"# {len(requests)} requests with token_stats")
    hdr = f"{'seq':>4} {'line':>6} {'prompt_n':>9} {'cache_read':>10} {'hit%':>6} {'pred':>6}  flags"
    print(hdr)
    print("-" * len(hdr))

    flagged = 0
    prev_cache = None
    prev_total = None
    for r in requests:
        total = r["prompt_n"] + r["cache_read"]
        hit = 100.0 * r["cache_read"] / total if total else 0.0
        flags = []
        if prev_cache is not None:
            if r["cache_read"] == 0:
                flags.append("MISS(cold)")
            elif r["cache_read"] + args.bust_tol < prev_cache:
                flags.append(f"BUST(-{prev_cache - r['cache_read']})")
        if prev_total is not None and total < prev_total - args.shrink_tol:
            flags.append(f"SHRINK({prev_total - total})")
        if r["prompt_n"] > 15000:
            flags.append(f"GROW(+{r['prompt_n']} uncached)")
        prev_cache = r["cache_read"]
        prev_total = total

        show = args.full or flags
        if show:
            flagged += 1
            note = note_before(events, r["line"])
            note_str = f"  <- {note[0]}:{note[1]}" if note else ""
            print(
                f"{r['seq']:>4} {r['line']:>6} {r['prompt_n']:>9} "
                f"{r['cache_read']:>10} {hit:>5.1f}% {r['predicted']:>6}  "
                f"{'; '.join(flags)}{note_str}"
            )
    print(f"# {flagged} rows shown (of {len(requests)})")
    return 0


if __name__ == "__main__":
    sys.exit(main())
