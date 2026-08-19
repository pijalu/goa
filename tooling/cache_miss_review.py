#!/usr/bin/env python3
"""Validate and summarize logs/cache_miss_requests.json from a Goa export."""
from __future__ import annotations
import argparse, json, sys, zipfile


def load(path):
    with zipfile.ZipFile(path) as z:
        try:
            return json.loads(z.read("logs/cache_miss_requests.json"))
        except KeyError as exc:
            raise ValueError("missing logs/cache_miss_requests.json") from exc


def validate(reports):
    errors = []
    if not isinstance(reports, list): return ["root must be an array"]
    for i, r in enumerate(reports):
        if not isinstance(r, dict): errors.append(f"report {i}: not an object"); continue
        for key in ("id", "requests"):
            if key not in r: errors.append(f"report {i}: missing {key}")
        reqs = r.get("requests", [])
        if not isinstance(reqs, list) or not reqs: errors.append(f"report {i}: requests must be non-empty")
        elif any(reqs[j].get("seq", 0) >= reqs[j+1].get("seq", 0) for j in range(len(reqs)-1)):
            errors.append(f"report {i}: requests are not ordered")
    return errors


def summary(reports):
    out = []
    for r in reports:
        prev, cur = r.get("prev_cache_read_tokens", 0), r.get("cache_read_tokens", 0)
        out.append({"id": r.get("id"), "model": r.get("model", ""), "kind": "full" if cur == 0 else "partial",
                    "previous_cache_read": prev, "cache_read": cur, "lost_tokens": prev if cur == 0 else max(0, prev-cur),
                    "gap_ms": r.get("gap_since_prev_response_ms", 0), "likely_cause": r.get("likely_cause", "unknown"),
                    "affinity_hint_sent": r.get("affinity_hint_sent", False),
                    "request_sequences": [x.get("seq") for x in r.get("requests", [])]})
    return out


def main(argv=None):
    p = argparse.ArgumentParser()
    p.add_argument("command", choices=("extract", "validate", "report"))
    p.add_argument("export")
    args = p.parse_args(argv)
    try: reports = load(args.export)
    except (OSError, zipfile.BadZipFile, ValueError) as exc:
        print(f"error: {exc}", file=sys.stderr); return 2
    errors = validate(reports)
    if args.command == "validate":
        if errors:
            print("\n".join(errors), file=sys.stderr); return 1
        print(f"valid: {len(reports)} reports"); return 0
    data = summary(reports)
    if args.command == "extract":
        for x in data: print(json.dumps(x, sort_keys=True))
    else:
        print(f"# Cache miss review: {args.export}\n\nReports: {len(data)}\n")
        for x in data:
            print(f"- Report {x['id']}: {x['kind']} {x['previous_cache_read']} -> {x['cache_read']} tokens; lost {x['lost_tokens']}; cause `{x['likely_cause']}`; gap {x['gap_ms']}ms; affinity={x['affinity_hint_sent']}; sequences={x['request_sequences']}")
        if errors: print("\nValidation warnings:\n" + "\n".join(f"- {e}" for e in errors))
    return 0

if __name__ == "__main__": sys.exit(main())
