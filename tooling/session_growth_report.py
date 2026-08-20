#!/usr/bin/env python3
"""session_growth_report.py — Locate sudden context growth in a Goa session.

Correlates each `token_stats` request whose UNCACHED prompt portion
(`prompt_n`, i.e. tokens the provider had to process fresh) exceeds a
threshold with the events that immediately precede it, to answer:

  "which tool call / user injection suddenly added Nk tokens to the context?"

For every growth spike the script prints the nearest preceding tool_result
(with its serialized length), tool_call, and user content, plus the largest
tool results in the whole session (top-N), so unbounded tool output stands
out.

Usage:
    python3 tooling/session_growth_report.py SESSION.jsonl
                                             [--min-delta N] [--top N]

    --min-delta N  flag requests with prompt_n >= N (default 8000)
    --top N        show the N largest tool results overall (default 10)

Exit status is always 0.
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


def main(argv=None):
    ap = argparse.ArgumentParser(description="Goa session context-growth report")
    ap.add_argument("session", help="path to .goa/sessions/<id>.jsonl")
    ap.add_argument("--min-delta", type=int, default=8000,
                    help="flag requests whose uncached prompt_n >= this")
    ap.add_argument("--top", type=int, default=10)
    args = ap.parse_args(argv)

    tool_results = []          # (line, tool, char_len)
    spikes = []                # (line, prompt_n, cache_read)
    recent = []                # rolling window of (line, desc) for context
    total_result_chars = 0
    n_results = 0

    for ln, ev in iter_events(args.session):
        t = ev.get("Type")
        if t == "token_stats":
            tm = ev.get("timings", {})
            pn = tm.get("prompt_n", 0)
            if pn >= args.min_delta:
                spikes.append((ln, pn, tm.get("cache_read_tokens", 0), list(recent)))
            recent.clear()
        elif t == "tool_result":
            res = ev.get("ToolResult") or ""
            tool_results.append((ln, ev.get("ToolName"), len(res)))
            total_result_chars += len(res)
            n_results += 1
            recent.append((ln, f"tool_result {ev.get('ToolName')} len={len(res)}"))
        elif t == "tool_call":
            recent.append((ln, f"tool_call {ev.get('ToolName')}: {(ev.get('ToolInput') or '')[:100]}"))
        elif t == "content" and not ev.get("IsDelta"):
            txt = (ev.get("Text") or "").replace("\n", " ")
            recent.append((ln, f"{ev.get('Role')}: {txt[:120]}"))
        if len(recent) > 12:
            recent = recent[-12:]

    print(f"# {args.session}")
    print(f"# {n_results} tool results, {total_result_chars} chars total "
          f"(~{total_result_chars // 4} tokens rough)")

    print(f"\n== growth spikes (prompt_n >= {args.min_delta}) ==")
    for ln, pn, cr, ctx in spikes:
        print(f"\nline {ln}: prompt_n={pn} cache_read={cr}")
        for cln, desc in ctx[-6:]:
            print(f"    {cln}: {desc}")

    print(f"\n== top {args.top} tool results by serialized size ==")
    for ln, tool, size in sorted(tool_results, key=lambda x: -x[2])[: args.top]:
        print(f"    line {ln}: {tool} len={size} (~{size // 4} tokens)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
