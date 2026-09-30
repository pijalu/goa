#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Copyright (C) 2026 Pierre Poissinger

"""Live-probe z.ai OpenAI-compat vs Anthropic SSE termination.

Reads the coding-plan API key from ~/.goa/config.yaml at runtime; never prints it.

Usage: zai_probe.py <openai|anthropic> <model> <run> <mode> [think:on|off]

modes:
  plain            text-only turn
  tools            text + tool definitions + tool_stream:true (goa's fixed shape)
  tools-noflag     text + tool definitions, NO tool_stream (export-A shape)
  long             text-only turn with a long answer
  long-tools       long turn + tool definitions + tool_stream:true
  long-tools-noflag

Classification per probe:
  clean_eof        server closed the body after a terminator
  held_open        no more bytes after the last event, connection still open
  no_terminator    clean EOF but no [DONE]/message_stop/finish_reason
"""
import http.client
import json
import os
import re
import ssl
import sys
import time

CFG = os.path.expanduser("~/.goa/config.yaml")
IDLE_AFTER_LAST = 20.0   # s to wait for a byte after the last event
OVERALL_MAX = 180.0      # hard cap per probe


def read_key():
    txt = open(CFG, encoding="utf-8").read()
    m = re.search(r"- id: zai\b.*?\n\s*api_key:\s*(\S+)", txt, re.S)
    if not m:
        sys.exit("no zai api_key in " + CFG)
    return m.group(1)


def build(surface, model, mode, think):
    long = mode.startswith("long") or "unbounded" in mode
    base = mode.replace("long-", "").replace("long", "")
    tools = base.startswith("tools") or mode.startswith("force-tool")
    tool_stream = tools and not base.endswith("noflag")
    unbounded = "unbounded" in mode
    if "force-tool" in mode:
        prompt = ("Call the get_time tool with tz=UTC. Do not answer in text "
                  "before calling the tool.")
    elif long:
        prompt = ("Write a detailed ~400 word technical explanation of SSE stream "
                  "termination semantics, including [DONE], finish_reason and "
                  "connection close behaviour. No tools.")
    else:
        prompt = "Reply with exactly the word OK and nothing else."
    # NOTE: the coding-plan OpenAI-compat endpoint rejects the object/array content
    # form ({"type":"text",...}) with 400 code 1210 — verified live. String only.
    msg = {"role": "user", "content": prompt}
    if surface == "openai":
        body = {"model": model, "messages": [msg], "stream": True,
                "stream_options": {"include_usage": True},
                "temperature": 0}
        if not unbounded:
            body["max_tokens"] = 4096
        if think == "on":
            body["thinking"] = {"type": "enabled", "clear_thinking": False}
        if tools:
            body["tools"] = [{"type": "function", "function": {
                "name": "get_time", "description": "Get the current time",
                "parameters": {"type": "object", "properties": {"tz": {"type": "string"}},
                               "required": []}}}]
            if tool_stream:
                body["tool_stream"] = True
        path = "/api/coding/paas/v4/chat/completions"
    else:
        body = {"model": model, "messages": [msg], "stream": True,
                "temperature": 0}
        if not unbounded:
            body["max_tokens"] = 4096
        if tools:
            body["tools"] = [{"name": "get_time", "description": "Get the current time",
                              "input_schema": {"type": "object",
                                               "properties": {"tz": {"type": "string"}},
                                               "required": []}}]
        path = "/api/anthropic/v1/messages"
    return path, body


def probe(surface, model, run, mode, think):
    key = read_key()
    path, body = build(surface, model, mode, think)
    host = "api.z.ai"
    if surface == "openai":
        headers = {"Authorization": "Bearer " + key, "Content-Type": "application/json",
                   "Accept": "text/event-stream", "User-Agent": "goa-zai-sse-probe/1"}
    else:
        headers = {"x-api-key": key, "anthropic-version": "2023-06-01",
                   "Content-Type": "application/json", "Accept": "text/event-stream",
                   "User-Agent": "goa-zai-sse-probe/1"}
    payload = json.dumps(body).encode()
    headers["Content-Length"] = str(len(payload))

    res = {"surface": surface, "model": model, "mode": mode, "think": think,
           "run": run, "tool_stream": body.get("tool_stream"),
           "max_tokens": body.get("max_tokens", "ABSENT")}
    t0 = time.monotonic()
    conn = http.client.HTTPSConnection(host, 443, timeout=IDLE_AFTER_LAST,
                                       context=ssl.create_default_context())
    try:
        conn.request("POST", path, body=payload, headers=headers)
        resp = conn.getresponse()
        res["http"] = resp.status
        res["resp_headers"] = {k.lower(): v for k, v in resp.getheaders()
                              if k.lower() in ("content-type", "transfer-encoding",
                                               "connection", "content-length", "server")}
        res["ttfb_s"] = round(time.monotonic() - t0, 3)
        if resp.status != 200:
            res["error_body"] = resp.read(400).decode("utf-8", "replace")
            res["verdict"] = "http_%d" % resp.status
            return res

        events = 0
        markers = {"done": False, "message_stop": False, "message_delta": False,
                   "finish_reason": None, "stop_reason": None, "usage": False,
                   "tool_calls": False}
        first_event_s = None
        last_event_s = None
        content_bytes = 0
        event_types = {}
        buf = b""
        raw_tail = b""
        start = time.monotonic()
        while time.monotonic() - start < OVERALL_MAX:
            try:
                chunk = resp.read(1)
            except (TimeoutError, OSError) as exc:
                res["read_error"] = type(exc).__name__ + ": " + str(exc)
                break
            if not chunk:
                res["eof"] = True
                break
            buf += chunk
            if not buf.endswith(b"\n"):
                if len(buf) > 1 << 20:
                    buf = buf[-4096:]
                continue
            line = buf.rstrip(b"\r\n")
            buf = b""
            now = time.monotonic()
            if line:
                events += 1
                first_event_s = first_event_s if first_event_s is not None else now - t0
                last_event_s = now - t0
                text = line.decode("utf-8", "replace")
                if text.startswith("event:"):
                    ev = text.split(":", 1)[1].strip()
                    event_types[ev] = event_types.get(ev, 0) + 1
                if text.strip() == "data: [DONE]":
                    markers["done"] = True
                if "message_stop" in text:
                    markers["message_stop"] = True
                if "message_delta" in text:
                    markers["message_delta"] = True
                if '"usage"' in text:
                    markers["usage"] = True
                if '"tool_calls"' in text or '"function"' in text and '"arguments"' in text:
                    markers["tool_calls"] = True
                m = re.search(r'"(?:stop|finish)_reason"\s*:\s*"([^"]+)"', text)
                if m:
                    key_name = "stop_reason" if "stop_reason" in m.group(0) else "finish_reason"
                    markers[key_name] = m.group(1)
                content_bytes += len(line)
                raw_tail = (raw_tail + line + b"\n")[-500:]

        terminated = markers["done"] or markers["message_stop"] or \
            bool(markers["finish_reason"]) or bool(markers["stop_reason"])
        res.update(events=events, markers=markers, terminated=terminated,
                   content_bytes=content_bytes,
                   first_event_s=round(first_event_s or 0, 3),
                   last_event_s=round(last_event_s or 0, 3),
                   total_s=round(time.monotonic() - t0, 3),
                   event_types=event_types,
                   tail=raw_tail.decode("utf-8", "replace")[-300:])
        if not terminated:
            res["verdict"] = "no_terminator" if res.get("eof") else "held_open_no_terminator"
        else:
            res["verdict"] = "terminated"
            res["gap_after_last_event_s"] = round(
                (time.monotonic() - t0) - (last_event_s or 0), 3)
    finally:
        conn.close()
    return res


if __name__ == "__main__":
    surface = sys.argv[1] if len(sys.argv) > 1 else "openai"
    model = sys.argv[2] if len(sys.argv) > 2 else "glm-5.3-flash"
    run = sys.argv[3] if len(sys.argv) > 3 else "1"
    mode = sys.argv[4] if len(sys.argv) > 4 else "plain"
    think = sys.argv[5] if len(sys.argv) > 5 else "off"
    print(json.dumps(probe(surface, model, run, mode, think), ensure_ascii=False))
