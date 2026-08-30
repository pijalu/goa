#!/usr/bin/env python3
"""OpenAI-compatible mock LLM server for goa e2e/validation testing.

Deterministic, dependency-free (stdlib only), no remote calls. Two modes:

- Normal turns: streams a large filler response (MOCK_FILLER_KB, ~30 KB by
  default) so conversation history grows past a configured
  `context_compression.max_tokens` ceiling quickly.
- Summarize requests (system prompt starts with "Summarize"): streams a short
  fixed summary so Compact succeeds and produces a real summary message.

Endpoints: `GET /v1/models`, `POST /v1/chat/completions` (stream and non-stream).

Configuration (environment):
  MOCK_LLM_HOST         bind host            (default 127.0.0.1)
  MOCK_LLM_PORT         bind port            (default 8017)
  MOCK_MODEL_ID         served model id      (default mock-gen)
  MOCK_CONTEXT_LENGTH   advertised context   (default 32768)
  MOCK_FILLER_KB        filler size in KB    (default 30)
  MOCK_LLM_LOG          request log path     (default: no logging)

Cache simulation (deterministic, for /stats:cache validation): each request's
usage reports a simulated prefix cache. The prompt grows per request; the
reported cached_tokens normally re-serves the previous prompt's size (~95%),
and every MOCK_BUST_EVERY-th request busts the cache (cached_tokens drops to
a small residual), so miss/drop surfaces have real signal. Disable with
MOCK_BUST_EVERY=0.

Usage:
  MOCK_LLM_PORT=8017 python3 e2e/mockllm/server.py &
  # or via e2e/lib.sh: start_mock_llm /tmp/goa-mock.log
"""
import json
import os
import random
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, HTTPServer

HOST = os.environ.get("MOCK_LLM_HOST", "127.0.0.1")
PORT = int(os.environ.get("MOCK_LLM_PORT", "8017"))
MODEL_ID = os.environ.get("MOCK_MODEL_ID", "mock-gen")
CONTEXT_LENGTH = int(os.environ.get("MOCK_CONTEXT_LENGTH", "32768"))
FILLER_KB = int(os.environ.get("MOCK_FILLER_KB", "30"))
LOG_PATH = os.environ.get("MOCK_LLM_LOG", "")
BUST_EVERY = int(os.environ.get("MOCK_BUST_EVERY", "4"))

# Simulated prompt-cache state: the request counter drives deterministic
# prompt growth and the every-Nth-request bust (see module docstring).
_req_lock = threading.Lock()
_req_count = 0


def simulated_usage():
    """Deterministic per-request usage with a simulated prefix cache.

    Request n: prompt = 1200 + 400·n tokens; cached = the previous prompt's
    size (≈95% prefix reuse) except on every BUST_EVERY-th request, where the
    cache busts and only a 137-token residual is served (goa's OpenAI parser
    reads prompt_tokens_details.cached_tokens as CacheReadTokens and nets the
    rest out of prompt_n).
    """
    global _req_count
    with _req_lock:
        _req_count += 1
        n = _req_count
    prompt = 1200 + 400 * n
    prev_prompt = 1200 + 400 * (n - 1)
    if n == 1:
        cached = 128  # cold start: tiny residual read, like local servers
    elif BUST_EVERY > 0 and n % BUST_EVERY == 0:
        cached = 137  # cache bust: prefix invalidated
    else:
        cached = int(prev_prompt * 0.95)
    completion = 64
    return {
        "prompt_tokens": prompt,
        "completion_tokens": completion,
        "total_tokens": prompt + completion,
        "prompt_tokens_details": {"cached_tokens": cached},
    }


def build_filler(kb):
    """~kb KB of filler where every window of text is effectively unique.

    Plain repeated lorem trips goa's stream-loop detector, which cuts the
    reply, injects control notes and eventually errors the turn — breaking
    deterministic e2e runs. Numbered lines still repeat the same phrase and
    are caught too. Instead: seeded pseudo-random word choices per line keep
    the output deterministic yet non-repeating in any sliding window.
    """
    vocab = ("time year people way day man thing woman life child world school "
             "state family student group country problem hand part place case "
             "week company system program question work government number night "
             "point home water room mother area money story fact month lot right "
             "study book eye job word business issue side kind head house service "
             "friend father power hour game line end member law car city name "
             "team minute idea body back parent face level office door health "
             "person art war history party result change morning reason girl "
             "moment air teacher force education foot boy age policy process "
             "music market sense nation plan college interest death experience "
             "effect use class control care field development role effort rate "
             "heart drug show leader light voice wife whole police mind price "
             "report decision son view relationship town road arm difference "
             "value building action model season society tax director position "
             "player record paper space ground form event official matter "
             "center couple site project activity star table need court produce "
             "eat teach oil situation cost industry figure street image phase "
             "north love personal cat dog bird tree forest river mountain")
    words = vocab.split()
    parts, i, size = [], 0, 0
    target = kb * 1024
    while size < target:
        rng = random.Random(0x5EED_0000 + i)
        n = rng.randint(8, 14)
        line = " ".join(rng.choice(words) for _ in range(n))
        parts.append(line)
        size += len(line) + 1
        i += 1
    return " ".join(parts)[:target]


FILLER = build_filler(FILLER_KB)
SUMMARY = "Summary: the user asked for filler text; the assistant produced it."

CHUNK = 512  # bytes per SSE event


def sse_chunks(text):
    """Yield OpenAI streaming delta events for text, CHUNK bytes at a time."""
    for i in range(0, len(text), CHUNK):
        part = text[i:i + CHUNK]
        yield {
            "id": "chatcmpl-mock", "object": "chat.completion.chunk",
            "created": int(time.time()), "model": MODEL_ID,
            "choices": [{"index": 0, "delta": {"content": part}, "finish_reason": None}],
        }
    yield {
        "id": "chatcmpl-mock", "object": "chat.completion.chunk",
        "created": int(time.time()), "model": MODEL_ID,
        "choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}],
    }


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, fmt, *args):
        if not LOG_PATH:
            return
        try:
            with open(LOG_PATH, "a") as f:
                f.write("%s %s\n" % (time.strftime("%H:%M:%S"), fmt % args))
        except OSError:
            pass

    def _send_json(self, code, obj):
        body = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if self.path.endswith("/models"):
            self._send_json(200, {
                "object": "list",
                "data": [{"id": MODEL_ID, "object": "model",
                          "owned_by": "mock", "context_length": CONTEXT_LENGTH}],
            })
            return
        self._send_json(404, {"error": "not found"})

    def do_POST(self):
        n = int(self.headers.get("Content-Length") or 0)
        payload = {}
        if n:
            try:
                payload = json.loads(self.rfile.read(n))
            except Exception:
                payload = {}
        if not self.path.endswith("/chat/completions"):
            self._send_json(404, {"error": "not found"})
            return

        msgs = payload.get("messages") or []
        if not isinstance(msgs, list):
            msgs = []
        sys_txt = next(((m.get("content") or "") for m in msgs
                        if isinstance(m, dict) and m.get("role") == "system"), "")
        is_summarize = sys_txt.strip().lower().startswith("summarize")
        text = SUMMARY if is_summarize else FILLER

        if not payload.get("stream"):
            usage = simulated_usage()
            self._send_json(200, {
                "id": "chatcmpl-mock", "object": "chat.completion",
                "created": int(time.time()), "model": MODEL_ID,
                "choices": [{"index": 0,
                             "message": {"role": "assistant", "content": text},
                             "finish_reason": "stop"}],
                "usage": usage,
            })
            return

        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.send_header("Transfer-Encoding", "chunked")
        self.end_headers()
        for ev in sse_chunks(text):
            data = ("data: " + json.dumps(ev) + "\n\n").encode()
            self.wfile.write(("%x\r\n" % len(data)).encode() + data + b"\r\n")
            self.wfile.flush()
        # Terminal usage chunk (empty choices): the OpenAI-compatible signal
        # goa's parser reads for token/cache stats.
        tail_event = {"id": "chatcmpl-mock", "object": "chat.completion.chunk",
                      "created": int(time.time()), "model": MODEL_ID,
                      "choices": [], "usage": simulated_usage()}
        tail = ("data: " + json.dumps(tail_event) + "\n\ndata: [DONE]\n\n").encode()
        self.wfile.write(("%x\r\n" % len(tail)).encode() + tail + b"\r\n")
        self.wfile.write(b"0\r\n\r\n")
        self.wfile.flush()


if __name__ == "__main__":
    sys.stderr.write("mock-llm listening on http://%s:%d (model=%s filler=%dKB)\n"
                     % (HOST, PORT, MODEL_ID, FILLER_KB))
    # Threaded: a slow/stuck streaming response must not block health checks
    # or concurrent requests (goa keep-alives and fires parallel calls).
    from socketserver import ThreadingMixIn

    class ThreadingHTTPServer(ThreadingMixIn, HTTPServer):
        daemon_threads = True

    ThreadingHTTPServer((HOST, PORT), Handler).serve_forever()
