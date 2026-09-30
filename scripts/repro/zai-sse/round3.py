#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Copyright (C) 2026 Pierre Poissinger

"""Round 3: Anthropic-surface capability coverage (models, thinking, tool_choice)."""
import http.client
import json
import os
import re
import ssl
import time

CFG = os.path.expanduser("~/.goa/config.yaml")
key = re.search(r"- id: zai\b.*?\n\s*api_key:\s*(\S+)", open(CFG).read(), re.S).group(1)
PATH = "/api/anthropic/v1/messages"
TOOLS = [{"name": "get_time", "description": "Get the current time",
          "input_schema": {"type": "object", "properties": {"tz": {"type": "string"}},
                           "required": []}}]


def call(body, stream=True, label=""):
    body = dict(body)
    body["stream"] = stream
    payload = json.dumps(body).encode()
    conn = http.client.HTTPSConnection("api.z.ai", 443, timeout=90,
                                       context=ssl.create_default_context())
    t0 = time.monotonic()
    try:
        conn.request("POST", PATH, body=payload, headers={
            "x-api-key": key, "anthropic-version": "2023-06-01",
            "Content-Type": "application/json", "Accept": "text/event-stream",
            "Content-Length": str(len(payload))})
        r = conn.getresponse()
        if r.status != 200:
            print("%-42s http=%s %s" % (label, r.status,
                                        r.read(220).decode("utf-8", "replace")[:180]))
            return
        raw = r.read().decode("utf-8", "replace")
        stops = raw.count("event: message_stop")
        fr = re.findall(r'"stop_reason"\s*:\s*"([^"]+)"', raw)
        print("%-42s http=200 t=%5.1fs message_stop=%d stop_reason=%s bytes=%d" % (
            label, time.monotonic() - t0, stops, sorted(set(fr)), len(raw)))
    finally:
        conn.close()


msg = [{"role": "user", "content": "Reply with exactly the word OK."}]
for m in ("glm-5.3-flash", "glm-5.3", "glm-5.2", "glm-4.6", "nonexistent-model"):
    call({"model": m, "messages": msg, "max_tokens": 512}, label="model=" + m)

call({"model": "glm-5.3-flash", "messages": msg, "max_tokens": 2048,
      "thinking": {"type": "enabled", "budget_tokens": 1024}}, label="thinking(anthropic-style)")
call({"model": "glm-5.3-flash", "messages": msg, "max_tokens": 2048,
      "thinking": {"type": "enabled", "clear_thinking": False}}, label="thinking(zai-openai-style)")
call({"model": "glm-5.3-flash", "messages": msg, "max_tokens": 2048, "tools": TOOLS,
      "tool_choice": {"type": "any"}}, label="tools+tool_choice=any")
call({"model": "glm-5.3-flash", "messages": msg, "max_tokens": 2048, "tools": TOOLS,
      "tool_stream": True}, label="tools+tool_stream(zai flag on anthropic)")
call({"model": "glm-5.3-flash", "messages": msg, "max_tokens": 2048, "stream": False,
      "system": "You are terse."}, stream=False, label="system+non-stream")
call({"model": "glm-5.3-flash", "messages": msg, "max_tokens": 2048,
      "prompt_cache_key": "goa-probe-1"}, label="prompt_cache_key")
