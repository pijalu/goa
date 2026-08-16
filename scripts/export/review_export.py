#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Copyright (C) 2026 Pierre Poissinger

"""Review a goa diagnostic export bundle (goa-export-*.zip).

Reads the bundle in place (no extraction needed) and reports:

  * manifest / session metadata
  * the HTTP request timeline (from logs/http.jsonl)
  * cache-miss forensics (from logs/cache_miss_requests.json):
      - token deltas (prev_cache_read -> cache_read)
      - time gap between the two retained requests
      - missing sequence numbers between them (interleaved requests)
      - the exact message index where the two request bodies diverge
      - a heuristic verdict per miss (see VERDICTS below)
  * trace anomalies (from diagnostics/trace.json)

Standard library only. Python 3.8+.

Usage:
    python3 scripts/export/review_export.py .goa/exports/goa-export-XXX.zip
    python3 scripts/export/review_export.py export.zip --verbose
    python3 scripts/export/review_export.py export.zip --json

VERDICTS (heuristics, in priority order):
  prefix-mutation   the two requests diverge BEFORE the missing request's
                    last message: the client rewrote history -> real bug.
  interleaved-seq   a sequence number is missing between the two retained
                    requests: another request ran in between (sub-agent,
                    planner, retry) and may have replaced the cache entry.
  ttl/idle          the gap between the requests exceeds --ttl seconds:
                    provider-side idle eviction is plausible.
  provider-eviction none of the above: prefix stable, short gap, no gap in
                    sequence numbers -> server-side eviction / cache
                    granularity. Not explainable from the client.

A miss is "explained" by the first verdict that matches.
"""
from __future__ import annotations

import argparse
import json
import sys
import zipfile
from typing import Any, Dict, List, Optional

# Default idle threshold (seconds) for the ttl/idle verdict. Provider prefix
# caches typically evict after a few minutes of inactivity.
DEFAULT_TTL_SECONDS = 120.0


# --------------------------------------------------------------------------
# bundle access
# --------------------------------------------------------------------------

def read_json(zf: zipfile.ZipFile, name: str) -> Optional[Any]:
    """Return parsed JSON for a bundle member, or None when absent."""
    try:
        with zf.open(name) as fh:
            return json.loads(fh.read().decode("utf-8"))
    except KeyError:
        return None


def read_jsonl(zf: zipfile.ZipFile, name: str) -> List[Dict[str, Any]]:
    """Return parsed objects for a JSONL bundle member ([] when absent)."""
    try:
        with zf.open(name) as fh:
            out = []
            for line in fh.read().decode("utf-8").splitlines():
                line = line.strip()
                if line:
                    out.append(json.loads(line))
            return out
    except KeyError:
        return []


# --------------------------------------------------------------------------
# cache-miss forensics
# --------------------------------------------------------------------------

def _canon(msg: Dict[str, Any]) -> str:
    """Canonical serialization of one chat message for comparison."""
    return json.dumps(msg, sort_keys=True, ensure_ascii=False)


def first_divergence(msgs_a: List[Dict[str, Any]],
                     msgs_b: List[Dict[str, Any]]) -> int:
    """Index of the first message where the two bodies differ.

    Returns len(msgs_a) when msgs_a is a strict prefix of msgs_b (pure
    append, the healthy case).
    """
    n = min(len(msgs_a), len(msgs_b))
    for i in range(n):
        if _canon(msgs_a[i]) != _canon(msgs_b[i]):
            return i
    return n


def _preview(msg: Dict[str, Any], limit: int = 160) -> str:
    """Short human preview of a message for --verbose output."""
    role = msg.get("role", "?")
    content = msg.get("content")
    if isinstance(content, list):
        kinds = ",".join(str(c.get("type", "?")) for c in content
                         if isinstance(c, dict))
        text = "[" + kinds + "]"
    else:
        text = str(content or "")
    text = " ".join(text.split())
    if msg.get("tool_calls"):
        text += " (+tool_calls)"
    if len(text) > limit:
        text = text[:limit] + "..."
    return f"{role}: {text}"


def analyze_miss(report: Dict[str, Any],
                 ttl_seconds: float) -> Dict[str, Any]:
    """Forensic summary of one cache-miss report.

    A report bundles the request that missed and the same conversation
    sequence's request before it (see internal/agentic/provider/
    cache_forensics.go).
    """
    reqs = report.get("requests", [])
    result: Dict[str, Any] = {
        "id": report.get("id"),
        "timestamp": report.get("timestamp"),
        "model": report.get("model"),
        "prev_cache_read_tokens": report.get("prev_cache_read_tokens"),
        "cache_read_tokens": report.get("cache_read_tokens"),
        "request_count": len(reqs),
        "verdicts": [],
        "explained": False,
    }
    if len(reqs) != 2:
        result["verdicts"].append("incomplete-report")
        return result

    prev, miss = reqs[0], reqs[1]
    prev_msgs = prev.get("body", {}).get("messages", [])
    miss_msgs = miss.get("body", {}).get("messages", [])

    div = first_divergence(prev_msgs, miss_msgs)
    result.update({
        "prev_seq": prev.get("seq"),
        "miss_seq": miss.get("seq"),
        "prev_timestamp": prev.get("timestamp"),
        "miss_timestamp": miss.get("timestamp"),
        "prev_messages": len(prev_msgs),
        "miss_messages": len(miss_msgs),
        "divergence_index": div,
        "divergence_is_append": div == len(prev_msgs),
    })

    # verdict 1: client-side prefix mutation (the one class goa can fix)
    if div < len(prev_msgs):
        result["verdicts"].append("prefix-mutation")
        result["explained"] = True
        result["mutated_prev"] = _preview(prev_msgs[div])
        result["mutated_miss"] = (_preview(miss_msgs[div])
                                  if div < len(miss_msgs) else "<absent>")
        return result

    # time gap between the two retained requests
    gap = None
    try:
        from datetime import datetime
        t0 = datetime.fromisoformat(str(prev.get("timestamp")))
        t1 = datetime.fromisoformat(str(miss.get("timestamp")))
        gap = (t1 - t0).total_seconds()
        result["gap_seconds"] = round(gap, 1)
    except (TypeError, ValueError):
        pass

    # verdict 2: interleaved request(s) between the two retained ones
    prev_seq = prev.get("seq") or 0
    miss_seq = miss.get("seq") or 0
    if miss_seq - prev_seq > 1:
        result["verdicts"].append(
            "interleaved-seq(%d missing)" % (miss_seq - prev_seq - 1))
        result["explained"] = True
        return result

    # verdict 3: idle eviction
    if gap is not None and gap >= ttl_seconds:
        result["verdicts"].append("ttl/idle(>%.0fs)" % ttl_seconds)
        result["explained"] = True
        return result

    # verdict 4: nothing on the client explains it. Report block-alignment
    # evidence: providers cache in fixed-size blocks (z.ai/deepseek: 64
    # tokens), so a loss that is an exact block multiple with a stable
    # client prefix points at server-side segment eviction.
    prev_read = report.get("prev_cache_read_tokens") or 0
    cur_read = report.get("cache_read_tokens") or 0
    lost = prev_read - cur_read
    if lost > 0 and prev_read % 64 == 0 and cur_read % 64 == 0:
        result["verdicts"].append(
            "provider-eviction(block-aligned loss: %d blocks of 64)"
            % (lost // 64))
    else:
        result["verdicts"].append("provider-eviction?")
    return result


# --------------------------------------------------------------------------
# timeline
# --------------------------------------------------------------------------

def summarize_timeline(http_entries: List[Dict[str, Any]]) -> List[Dict[str, Any]]:
    """Condense logs/http.jsonl into per-request rows."""
    rows = []
    for e in http_entries:
        summary = e.get("requestSummary") or {}
        rows.append({
            "timestamp": e.get("timestamp"),
            "status": e.get("statusCode"),
            "duration_ms": e.get("durationMs"),
            "messages": summary.get("messageCount"),
            "last_role": summary.get("lastRole"),
            "finish": e.get("finishReason"),
        })
    return rows


# --------------------------------------------------------------------------
# output
# --------------------------------------------------------------------------

def build_report(zf: zipfile.ZipFile, ttl_seconds: float) -> Dict[str, Any]:
    manifest = read_json(zf, "manifest.json") or {}
    misses = read_json(zf, "logs/cache_miss_requests.json") or []
    http_entries = read_jsonl(zf, "logs/http.jsonl")
    trace = read_json(zf, "diagnostics/trace.json") or {}

    return {
        "manifest": {
            "sessionId": manifest.get("sessionId"),
            "exportedAt": manifest.get("exportedAt"),
            "goaVersion": manifest.get("goaVersion"),
            "workspaceDir": manifest.get("workspaceDir"),
            "activeProvider": manifest.get("activeProvider"),
            "activeModel": manifest.get("activeModel"),
            "issueDescription": manifest.get("issueDescription"),
            "missingFiles": manifest.get("missingFiles"),
        },
        "http_requests": len(http_entries),
        "timeline": summarize_timeline(http_entries),
        "cache_misses": [analyze_miss(m, ttl_seconds) for m in misses],
        "trace_anomalies": trace.get("anomalies", []),
    }


def print_text(report: Dict[str, Any], verbose: bool) -> None:
    m = report["manifest"]
    print("== manifest ==")
    for key in ("sessionId", "exportedAt", "goaVersion", "activeProvider",
                "activeModel", "issueDescription"):
        if m.get(key):
            print(f"  {key}: {m[key]}")
    if m.get("missingFiles"):
        print(f"  missing files: {', '.join(m['missingFiles'])}")

    print(f"\n== http timeline ({report['http_requests']} requests) ==")
    prev_ts = None
    for i, row in enumerate(report["timeline"], 1):
        gap = ""
        if prev_ts is not None and row["timestamp"]:
            try:
                from datetime import datetime
                dt = (datetime.fromisoformat(row["timestamp"])
                      - datetime.fromisoformat(prev_ts)).total_seconds()
                gap = f"  +{dt:6.1f}s"
            except (TypeError, ValueError):
                pass
        prev_ts = row["timestamp"]
        dur = (row["duration_ms"] or 0) / 1000.0
        print(f"  #{i:<2} {row['timestamp']}  {dur:6.1f}s{gap}"
              f"  msgs={row['messages']:<4} last={row['last_role']}"
              f"  finish={row['finish']}")

    print(f"\n== cache misses ({len(report['cache_misses'])}) ==")
    for c in report["cache_misses"]:
        print(f"  miss #{c['id']} @ {c['timestamp']} model={c['model']}")
        print(f"    cache_read: {c['prev_cache_read_tokens']}"
              f" -> {c['cache_read_tokens']}")
        if c.get("prev_seq") is not None:
            print(f"    requests: seq {c['prev_seq']} ({c['prev_messages']} msgs)"
                  f" -> seq {c['miss_seq']} ({c['miss_messages']} msgs)")
            print(f"    gap: {c.get('gap_seconds', '?')}s"
                  f"  divergence@{c['divergence_index']}"
                  f"  append={c['divergence_is_append']}")
        tag = "EXPLAINED" if c["explained"] else "UNEXPLAINED (client)"
        print(f"    verdict: {', '.join(c['verdicts'])}  [{tag}]")
        if verbose and "mutated_prev" in c:
            print(f"    mutated prev: {c['mutated_prev']}")
            print(f"    mutated miss: {c['mutated_miss']}")

    if report["trace_anomalies"]:
        print("\n== trace anomalies ==")
        for a in report["trace_anomalies"]:
            print(f"  - {a}")


def main(argv: Optional[List[str]] = None) -> int:
    parser = argparse.ArgumentParser(
        description="Review a goa diagnostic export bundle.")
    parser.add_argument("bundle", help="path to goa-export-*.zip")
    parser.add_argument("--json", action="store_true",
                        help="emit the full report as JSON")
    parser.add_argument("--verbose", action="store_true",
                        help="include mutated-message previews")
    parser.add_argument("--ttl", type=float, default=DEFAULT_TTL_SECONDS,
                        metavar="SECONDS",
                        help="idle gap that makes the ttl/idle verdict "
                             "plausible (default: %(default)s)")
    args = parser.parse_args(argv)

    try:
        with zipfile.ZipFile(args.bundle) as zf:
            report = build_report(zf, args.ttl)
    except (OSError, zipfile.BadZipFile) as exc:
        print(f"error: cannot read bundle: {exc}", file=sys.stderr)
        return 2

    if args.json:
        json.dump(report, sys.stdout, indent=2, ensure_ascii=False)
        sys.stdout.write("\n")
    else:
        print_text(report, args.verbose)

    # exit 1 when a miss is unexplained by client-side evidence: usable as
    # a regression gate in scripts/CI.
    unexplained = [c for c in report["cache_misses"] if not c["explained"]]
    return 1 if unexplained else 0


if __name__ == "__main__":
    sys.exit(main())
