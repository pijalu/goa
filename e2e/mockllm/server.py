#!/usr/bin/env python3
"""OpenAI-compatible mock LLM server for goa e2e/validation testing.

Deterministic, dependency-free (stdlib only), no remote calls. Three modes:

- Normal turns: streams a large filler response (MOCK_FILLER_KB, ~30 KB by
  default) so conversation history grows past a configured
  `context_compression.max_tokens` ceiling quickly.
- Summarize requests (system prompt starts with "Summarize"): streams a short
  fixed summary so Compact succeeds and produces a real summary message.
- Script mode (MOCK_SCRIPT=<json file>): prompt-driven scripted responses,
  including real OpenAI `tool_calls` streams, so an e2e can assert that a tool
  is actually executed and its result rendered without any network.

Endpoints: `GET /v1/models`, `POST /v1/chat/completions` (stream and non-stream).

Configuration (environment):
    MOCK_LLM_HOST         bind host            (default 127.0.0.1)
    MOCK_LLM_PORT         bind port            (default 8017)
    MOCK_MODEL_ID         served model id      (default mock-gen)
    MOCK_CONTEXT_LENGTH   advertised context   (default 32768)
    MOCK_FILLER_KB        filler size in KB    (default 30)
    MOCK_LLM_LOG          request log path     (default: no logging)
    MOCK_ZERO_AT          comma-separated request ordinals (1-based) that
                          report cached_tokens=0 (hard cache miss)
    MOCK_SCRIPT           path to a scripted-response JSON file (see
                          SCRIPT_FORMAT below); unset = classic filler mode
    MOCK_SCRIPT_CHUNK     bytes per SSE delta in script mode (default 64, so
                          streaming is observable step by step)

Cache simulation (deterministic, for /stats:cache validation): each request's
usage reports a simulated prefix cache. The prompt grows per request; the
reported cached_tokens normally re-serves the previous prompt's size (~95%),
and every MOCK_BUST_EVERY-th request busts the cache (cached_tokens drops to
a small residual), so miss/drop surfaces have real signal. Disable with
MOCK_BUST_EVERY=0.

SCRIPT_FORMAT (MOCK_SCRIPT): a JSON file shaped like

    {"rules": [
      {"match": "MARKER",
       "reply": "text streamed when the prompt contains MARKER",
       "tool":  {"name": "bash", "args": {"command": "echo W2-OK"}},
       "after_tool": "text streamed on the follow-up request, i.e. once a
                     role=tool message for this marker is in the history"},
      {"lines": ["alpha", "beta"], "chunk": 8}
    ]}

Rules are tried in order; the first rule whose `match` appears anywhere in the
user-visible prompt wins (goa appends system-generated user turns after the
real prompt, so the newest user message is not necessarily yours). A rule with
a `tool` emits a streaming tool_calls delta, then — because goa executes the
tool and sends the result back as a role=tool message — the same rule's
`after_tool` text answers that follow-up. A rule whose `lines` list is set
matches only when its first line is in the prompt, which makes it a convenient
anchored response. Without MOCK_SCRIPT the server keeps its previous behavior
exactly, so the t*.sh suite is unaffected.

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
ZERO_AT = {int(x) for x in os.environ.get("MOCK_ZERO_AT", "").split(",") if x.strip()}
SCRIPT_PATH = os.environ.get("MOCK_SCRIPT", "")
SCRIPT_CHUNK = int(os.environ.get("MOCK_SCRIPT_CHUNK", "64"))

# Scripted-response rules (MOCK_SCRIPT). Empty in classic mode.
RULES = []
if SCRIPT_PATH:
    try:
        with open(SCRIPT_PATH) as fh:
            RULES = json.load(fh).get("rules") or []
    except Exception as exc:  # a broken script must not silently pass as filler
        sys.stderr.write("mock-llm: cannot load MOCK_SCRIPT %s: %s\n" % (SCRIPT_PATH, exc))
        raise

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
    if n in ZERO_AT:
        cached = 0  # hard cache miss: prefix invalidated, zero reuse
    if LOG_PATH:
        try:
            with open(LOG_PATH, "a") as f:
                f.write("%s REQ n=%d prompt=%d cached=%d\n"
                        % (time.strftime("%H:%M:%S"), n, prompt, cached))
        except OSError:
            pass
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


def sse_chunks(text, chunk=None):
    """Yield OpenAI streaming delta events for text, CHUNK bytes at a time."""
    step = chunk or CHUNK
    for i in range(0, len(text), step):
        part = text[i:i + step]
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


def tool_call_chunks(rule, call_id="call_mock_1"):
    """Yield OpenAI streaming chunks for one tool call, in the delta shape.

    goa's parser (protocol/openai_completions.go parseToolCalls) reads
    delta.tool_calls[].function.{name,arguments} with an index, so the arguments
    are streamed as JSON string fragments exactly like a real provider does.
    """
    tool = rule["tool"]
    args = json.dumps(tool.get("args") or {})
    base = {"id": "chatcmpl-mock", "object": "chat.completion.chunk",
            "created": int(time.time()), "model": MODEL_ID}

    def frame(delta, finish=None):
        ev = dict(base)
        ev["choices"] = [{"index": 0, "delta": delta, "finish_reason": finish}]
        return ev

    # Opening frame: id, name and the first slice of arguments.
    step = max(1, min(SCRIPT_CHUNK, len(args)))
    yield frame({"tool_calls": [{"index": 0, "id": call_id,
                                 "type": "function",
                                 "function": {"name": tool["name"],
                                              "arguments": args[:step]}}]})
    for i in range(step, len(args), step):
        yield frame({"tool_calls": [{"index": 0,
                                     "function": {"arguments": args[i:i + step]}}]})
    yield frame({}, finish="tool_calls")


def user_text(messages):
    """Every user message's text, concatenated.

    NOT just the newest one: goa appends system-generated user turns after the
    real prompt (sticky skills, injected context), so the prompt a rule wants
    to match is rarely the last user message in the history.
    """
    out = []
    for m in messages:
        if isinstance(m, dict) and m.get("role") == "user":
            content = m.get("content")
            if isinstance(content, str):
                out.append(content)
    return "\n".join(out)


def tool_result_seen(messages):
    """True when the history already carries a tool result (follow-up turn)."""
    return any(isinstance(m, dict) and m.get("role") == "tool" for m in messages)


def marker_of_lines(rule):
    """The literal a `lines` rule expects to see in the prompt."""
    return rule.get("marker") or (rule.get("lines") or [""])[0]


def pick_rule(messages):
    """First rule matching the user prompt, or None in classic mode."""
    if not RULES:
        return None
    user = user_text(messages)
    for rule in RULES:
        marker = rule.get("match")
        if marker and marker in user:
            return rule
    for rule in RULES:
        if rule.get("lines") and marker_of_lines(rule) in user:
            return rule
    return None


def scripted_text(rule):
    """The text a rule streams: explicit reply, or its lines joined."""
    if rule.get("reply"):
        return rule["reply"]
    if rule.get("lines"):
        return "\n".join(rule["lines"])
    return ""


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

    # ------------------------------------------------------------ script mode

    def _respond_scripted(self, rule, msgs, payload):
        """Answer one request from a scripted rule (text or a tool call)."""
        usage = simulated_usage()

        # The follow-up turn: goa has already executed the tool and put the
        # result in the history as a role=tool message.
        if tool_result_seen(msgs) and rule.get("after_tool"):
            text, chunks, finish = rule["after_tool"], None, "stop"
        elif rule.get("tool") and not tool_result_seen(msgs):
            text, chunks, finish = "", tool_call_chunks(rule), "tool_calls"
        else:
            text = scripted_text(rule)
            chunks, finish = None, "stop"

        if not payload.get("stream"):
            message = {"role": "assistant", "content": text}
            if finish == "tool_calls":
                message["tool_calls"] = [{
                    "id": "call_mock_1", "type": "function",
                    "function": {"name": rule["tool"]["name"],
                                 "arguments": json.dumps(rule["tool"].get("args") or {})},
                }]
                message["content"] = None
            self._send_json(200, {
                "id": "chatcmpl-mock", "object": "chat.completion",
                "created": int(time.time()), "model": MODEL_ID,
                "choices": [{"index": 0, "message": message, "finish_reason": finish}],
                "usage": usage,
            })
            return

        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.send_header("Transfer-Encoding", "chunked")
        self.end_headers()
        # Small deltas by default so "the screen grew in N steps" is a real
        # streaming signal rather than one burst.
        events = chunks if chunks is not None else sse_chunks(text, rule.get("chunk"))
        self._stream(events, usage)

    def _stream(self, events, usage):
        """Write pre-built SSE events as HTTP/1.1 chunked, then a usage tail."""
        for ev in events:
            data = ("data: " + json.dumps(ev) + "\n\n").encode()
            self.wfile.write(("%x\r\n" % len(data)).encode() + data + b"\r\n")
            self.wfile.flush()
        # Terminal usage chunk (empty choices): the OpenAI-compatible signal
        # goa's parser reads for token/cache stats.
        tail_event = {"id": "chatcmpl-mock", "object": "chat.completion.chunk",
                      "created": int(time.time()), "model": MODEL_ID,
                      "choices": [], "usage": usage}
        tail = ("data: " + json.dumps(tail_event) + "\n\ndata: [DONE]\n\n").encode()
        self.wfile.write(("%x\r\n" % len(tail)).encode() + tail + b"\r\n")
        self.wfile.write(b"0\r\n\r\n")
        self.wfile.flush()

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

        # Script mode takes precedence when a rule matches the user's prompt;
        # otherwise the classic filler/summarize behavior applies, so the
        # t*.sh suite is byte-for-byte unaffected.
        rule = pick_rule(msgs)
        if rule is not None:
            self._respond_scripted(rule, msgs, payload)
            return

        sys_txt = next(((m.get("content") or "") for m in msgs
                        if isinstance(m, dict) and m.get("role") == "system"), "")
        is_summarize = sys_txt.strip().lower().startswith("summarize")
        text = SUMMARY if is_summarize else FILLER

        if not payload.get("stream"):
            self._send_json(200, {
                "id": "chatcmpl-mock", "object": "chat.completion",
                "created": int(time.time()), "model": MODEL_ID,
                "choices": [{"index": 0,
                             "message": {"role": "assistant", "content": text},
                             "finish_reason": "stop"}],
                "usage": simulated_usage(),
            })
            return

        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.send_header("Transfer-Encoding", "chunked")
        self.end_headers()
        self._stream(sse_chunks(text), simulated_usage())


if __name__ == "__main__":
    sys.stderr.write("mock-llm listening on http://%s:%d (model=%s filler=%dKB)\n"
                     % (HOST, PORT, MODEL_ID, FILLER_KB))
    # Threaded: a slow/stuck streaming response must not block health checks
    # or concurrent requests (goa keep-alives and fires parallel calls).
    from socketserver import ThreadingMixIn

    class ThreadingHTTPServer(ThreadingMixIn, HTTPServer):
        daemon_threads = True

    ThreadingHTTPServer((HOST, PORT), Handler).serve_forever()