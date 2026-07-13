<!--
SPDX-License-Identifier: GPL-3.0-or-later
Copyright (C) 2026 Pierre Poissinger
-->

# Goa — Code Review Report (2026-07-12)

Focus areas: **channels (producer/consumer)**, **TUI culling/redraw**, **network/API
non-happy paths**, **LLM context (caching, compaction, prompt bloat)**.

Severity scale: 🔴 High (correctness/data-loss/panic) · 🟠 Medium (perf/robustness) · 🟡 Low (smell/maintainability).

Findings are grouped by area but cross-referenced where they interact. Each item
lists **Location**, **Problem**, **Impact**, and **Fix options** (≥2 where useful).

---

## Area 1 — Channels & producer/consumer

### 1.1 🔴 `AgentBus`: send-on-closed-channel race (panic)
- **Location:** `internal/agentic/comm.go`
  - `Send` (line 70): `b.mu.RLock()` → lookup `ch` → `b.mu.RUnlock()` (line 73) → then `select { case ch <- msg: ... }` (line 80) **after releasing the lock**.
  - `Unregister` (line 57): takes `b.mu.Lock()`, `close(ch)` (line 62), deletes entry.
- **Problem:** `Send` obtains the channel pointer under RLock, drops the lock, then sends.
  If `Unregister` runs in between, it `close(ch)`; the subsequent `ch <- msg` in `Send`
  **panics** (`send on closed channel`). The RLock/Lock pair only guards the map, not the
  lifetime of the channel it hands out.
- **Impact:** Any multi-agent setup where one agent unregisters (shutdown/restart) while a
  peer is sending → unrecoverable panic that kills the sending goroutine (often a whole
  agent turn). Classic TOCTOU on a channel.
- **Fix options:**
  1. **Send under RLock, recover the close race.** Hold `b.mu.RLock()` across the whole send
     and have `Unregister` not close the channel inline but mark it for closing after all
     readers have drained (e.g. a `sync.RWMutex` write-lock in Unregister that waits out
     active senders). This serializes send vs close.
  2. **Per-agent mutex + closed flag** on the entry (`type inbox struct{ ch chan ...; closed atomic.Bool; mu sync.Mutex }`):
     `Send` takes `inbox.mu`, checks `closed`, sends, releases; `Unregister` takes
     `inbox.mu`, sets `closed`, closes, removes. Closing under the per-inbox mutex makes a
     concurrent send either observe `closed` (and return an error) or complete before close.
  3. **Don't close at all.** Channels in Go need not be closed if the only purpose is
     signaling “no more sends.” Senders instead return an explicit error when the recipient
     is gone; the inbox GC-collects when both sides drop it. Remove `close(ch)` and let
     `Unregister` just delete + mark `closed`.
- **Recommended:** option 2 (deterministic, no reader-drain wait, keeps the close signal
  that `CommConnector.loop`/`ReceiveMessageTool` rely on via `!ok`).

### 1.2 🟠 `emitAgentEvent`: blocking send on the streaming goroutine (backpressure coupling)
- **Location:** `core/agentmanager.go:969` (`am.eventsOut.Agent <- event.AgentEvent{...}`),
  also `core/agentmanager.go:961` (`emitInternalEvent`) and `core/context.go:489`
  (`ReplayAgentEvent`). Buffer = `MakeBus(1024, 32, 32, 32)` at `internal/app/subsystems.go:310`.
- **Problem:** These are invoked synchronously from the agent's observer
  (`emitEvent` → `OnEvent`), which runs **on the LLM-stream-consumer goroutine**. The send is
  **blocking**; once the 1024-deep `Agent` channel fills (e.g. the TUI `commandLoop` stalls —
  slow render, a big tool-result render, or an `ApplySync`), the producer goroutine stalls,
  which means **`consumeStream` stops reading SSE tokens**. After the 2-minute idle window
  (`DefaultStreamIdleTimeout`) the reader fires `ErrStreamIdle` → `handleStreamFailure` →
  retry → duplicate/spurious “Reconnecting…” bubbles.
- **Contradiction:** sibling emitters (`emitFlash`, `goalEventPublisher.Publish`,
  `FooterRefresh`) correctly use `select { case …: default: }` (drop). The highest-volume
  path (every content delta) does not — and **cannot** simply drop, because dropped content
  deltas lose user-visible text.
- **Impact:** Stream throughput is coupled to TUI render speed; a frozen terminal for >1024
  events stalls the model stream and can masquerade as a network drop.
- **Fix options:**
  1. **Decouple with a dedicated forwarder goroutine.** The observer pushes onto an
     unbounded (or large) async queue owned by the AgentManager; a single forwarder goroutine
     drains the queue and does the blocking send into `eventsOut.Agent`. The streaming
     goroutine never blocks. This is the canonical producer/forwarder/consumer split.
  2. **Bounded queue + coalescing for deltas.** Keep a ring that merges consecutive
     `EventContent`/`IsDelta` events for the same message so backpressure degrades to “throttled
     UI” rather than “stalled stream.”
  3. **Make the observer path strictly non-blocking** with a **lossy** policy only for
     transient event *kinds* (progress/flash), while content/tool events go through the
     forwarder (option 1). Never drop `EventContent`/`EventToolCall`.
- **Recommended:** option 1 (forwarder). The forwarder also gives a single place to apply
  backpressure policy and is consistent with the `Apply(cmd)` command-loop discipline already
  used by the TUI.

### 1.3 🟠 `ToolScheduler`: unbounded parallelism + `Collect()` can hang forever
- **Location:** `internal/agentic/tool_scheduler.go`
  - `collectUnblocked` (line ~180) greedily starts **all** non-conflicting pending tasks.
  - `Collect` (line ~228): `for _, st := range s.tasks { <-st.done }` with no timeout.
- **Problem (a):** No max-parallelism cap. If the model emits 50 independent `read_file`
  calls, 50 goroutines + 50 concurrent file/network ops run at once (FD exhaustion, disk
  thrash, provider rate limits on web tools). The conflict matrix only serializes *conflicting*
  accesses; unrelated tools are assumed infinitely parallel — too optimistic.
- **Problem (b):** If any `Execute` ignores its ctx (e.g. a `bash` running an interactive
  process, or a tool that spawns children not in the process group), `Collect` blocks until
  `Shutdown()` is called. There is no per-task deadline.
- **Fix options:**
  1. **Add a configurable `MaxParallel` semaphore** (default e.g. 8) acquired in `start()`
     before launching the goroutine and released in `finish()`; conflict-serialized tasks
     additionally respect the conflict matrix. Document the two-layer admission control.
  2. **Per-task timeout in `Collect`** (or wrap each `Execute` with
     `context.WithTimeout`). Surface a `ToolResult.Err = ctx.DeadlineExceeded` instead of
     hanging the whole turn.
  3. Combine: semaphore + inherit a per-tool-class timeout from the registry (bash already
     has its own; network tools get a transport default).
- **Recommended:** options 1+2. Cheap, high robustness win, and aligns with the AGENTS.md
  “methods should be generic and composable” guidance.

### 1.4 🟡 `idleTimeoutReader`: goroutine-per-`Read`
- **Location:** `internal/agentic/provider/idle_timeout.go` (`Read`, line ~38).
- **Problem:** Every `Read()` spawns a new goroutine (`done := make(chan result,1); go func(){...}`).
  During a bursty SSE stream this is one goroutine per buffer read. They exit fast, so it’s not
  a leak, but it’s needless scheduler churn on a hot path, and the alloc-per-read (`tmp` slice)
  doubles memory pressure vs. reusing a scratch buffer.
- **Fix options:**
  1. **Single long-lived reader goroutine + resettable timer.** Loop reading in one goroutine,
     push results to a `chan result`; the public `Read` selects on that chan and a `time.Timer`
     it `Reset`s each time. One goroutine for the lifetime of the reader.
  2. **Sync `SetReadDeadline`-based** approach if the underlying reader supports deadlines
     (net.Conn): set deadline = now+timeout, refresh after each successful read; on
     `*net.OpError` with `Timeout()` return `ErrStreamIdle`. Avoids goroutines entirely but
     only works when the body is a plain conn (not always true after decompression/wrappers).
- **Recommended:** option 1 where deadlines aren’t available; keep the current path as the
  fallback.

### 1.5 🟡 `runAgentEventReader`: recursive self-restart grows the stack
- **Location:** `internal/app/events.go:85-110` — `defer` recovers from panic and calls
  `a.runAgentEventReader(done, ch)` again from inside the deferred function.
- **Problem:** On persistent render panics the goroutine never actually returns; each panic
  adds one stack frame (recover → recursive call → `for{select}`). With a tight panic loop
  this grows unbounded and the original goroutine can never be joined/cleaned.
- **Fix options:**
  1. **Loop + inline recover:** `for { func(){ defer recover(); ... select... }() }`. Each
     iteration is an independent frame; no recursion.
  2. **Circuit-breaker:** after N panics in a window, stop the reader and surface a sticky
     error instead of infinitely retrying.
- **Recommended:** option 1 (minimal, removes the smell); optionally add option 2 for
  defense-in-depth.

### 1.6 🟡 `RetryWithBackoff` is dead, non-context-aware code
- **Location:** `internal/agentic/provider/retry.go` — deprecated, **zero callers** (verified
  via grep). Uses bare `time.Sleep`; the live retry path is `retryStream`
  (`agent_streaming.go:888`).
- **Problem:** Dead code that looks like an entry point; future contributors may reach for it
  and reintroduce a non-context-aware sleep.
- **Fix:** Delete it, or if kept for tests, mark `//nolint` + move behind a build tag. Prefer
  deletion (see Area 3 for the real retry path that needs the work).

---

## Area 2 — TUI culling & redraw

### 2.1 🟠 Chat viewport culling is wired but non-functional (full transcript per frame)
- **Location:**
  - `tui/chat_viewport.go`: `SetViewportHeight` (line ~102) sets `cv.viewportH`, but `Render`
    (line ~129) **ignores `viewportH`** and returns the full `cv.renderCache.lines`.
    `tailCache` (line ~74) is declared and cleared everywhere but **never populated/read**.
  - `tui/tui.go:buildScene` (line 925): `lines := child.Render(w)` then `Layer{Content: lines}`
    — the **entire transcript** becomes one base layer’s `Content`. The doc comment
    (“Components that expose a viewport height or total height are culled so the compositor
    only sees the visible tail”) is aspirational; the culling is not implemented.
- **Problem:** Every frame ships the whole transcript into the Scene. Downstream costs (see
  2.2–2.4) are therefore O(history), and they re-run on **every streamed token** (render is
  throttled to 60 fps, but a long transcript makes each frame expensive).
- **Impact:** CPU + GC pressure scale with conversation length during streaming; on a 10k-line
  transcript each frame touches 10k strings even though only ~24 rows are visible.
- **Fix options:**
  1. **Make `Render` viewport-aware:** when `cv.viewportH > 0`, return only the visible tail
     (last `viewportH` lines of `renderCache.lines`) and expose the true virtual height via
     `TotalHeight()` (already present). `buildScene` then sets `Rect.Y = y + totalH - len(tail)`
     so absolute scroll accounting is preserved (the `if totalH > len(lines)` branch already
     exists for this). This is the intended design — finish wiring it.
  2. **Two-layer split:** a “scrollback” base layer (full history, rendered lazily/cheaply or
     backed by the ring buffer) + a “live tail” overlay layer that the compositor diffs each
     frame. Keeps the hot path O(viewport).
  3. **Cap the layer Content in `buildScene`** as a stop-gap: `if len(lines) > 2*h { lines =
     lines[len(lines)-2*h:] }` with the same `Rect.Y` offset. Less elegant but a 5-line patch
     that removes the O(history) blast radius immediately.
- **Recommended:** option 1 (finish the existing design); option 3 as a same-day mitigation.

### 2.2 🟠 Compositor is O(history) per frame (canvas alloc + copy + placeLayer scan)
- **Location:** `tui/compositor.go`
  - `compose` (line 66): `canvas = make([]string, height)` where `height` = full base-canvas
    height (all transcript lines), then `applyLineResets` over the visible slice (good), but
    the canvas itself and `copySlice(c.prevLines)` (line ~540 in `renderChangePath`) are
    O(history).
  - `placeLayer` (line 148): iterates **every** `l.Content` line, bounds-checking each. With
    the full-transcript layer from 2.1, that’s O(history) per frame.
- **Problem:** Even with the differential write path (`writeDifferential` only emits changed
  visible rows — correct), the *preparation* work (alloc, copy, per-line bounds scan) is
  linear in history, repeated every frame.
- **Impact:** 60 fps × O(history) = steady CPU during streaming; `prevLines` copy also doubles
  transient memory.
- **Fix options:**
  1. **Resolve 2.1 first** — once the layer Content is already the tail, `placeLayer`,
     canvas alloc, and `prevLines` are all O(viewport). This is the single highest-leverage fix.
  2. **Avoid copying `prevLines` when only the tail changed:** keep `prevLines` as the previous
     visible viewport only (not the full canvas) once culling lands; the diff math already
     operates on the visible slice.
  3. **Reuse canvas backing array** across frames (sync.Pool or a reused `[]string`) to avoid
     per-frame allocation of the full-height slice.
- **Recommended:** option 1 (cures the root cause); options 2/3 are follow-on micro-opts.

### 2.3 🟠 `fullFrame` re-emits the entire canvas on resize / full redraw
- **Location:** `tui/compositor.go:fullFrame` (line 556): `for i, line := range canvas { ... buf.WriteString(line) }`.
- **Problem:** On any width change, height change, or `clearOnShrink` trigger, the **entire**
  canvas (full transcript) is re-emitted to the terminal as bytes — potentially megabytes of
  output for a long conversation. The terminal already retains scrollback, so this both
  double-writes history and can cause visible flicker / slow redraws on resize.
- **Impact:** A window resize during a long session re-prints the whole transcript; on slow
  terminals (SSH, Windows conhost) this is a multi-second freeze.
- **Fix options:**
  1. **On resize, redraw only the visible viewport** (height rows), not the full canvas. The
     scrollback is already in the terminal; we only need the current screen to be correct.
     Reserve full-canvas emission for the first-scroll case (`emitFirstScroll`) where
     scrollback genuinely needs populating.
  2. **Debounce resize redraws** (coalesce within ~50 ms) so dragging a window doesn’t emit
     one full-canvas per resize event.
  3. **Reserve `fullFrame(clear=true)` for genuine `\x1b[2J` moments** (mode switch, alt-screen
     entry/exit) and use a viewport-limited repaint for pure size changes.
- **Recommended:** option 1 + 2. (Mitigated substantially once 2.1 shrinks the canvas, but
  resize specifically should still avoid re-emitting scrollback.)

### 2.4 🟡 `clearOnShrink` forces full redraws forever after first shrink
- **Location:** `tui/compositor.go:460` (`if c.clearOnShrink && len(canvas) < c.maxLinesRendered && !hasOverlay`).
- **Problem:** `maxLinesRendered` is a high-water mark. Once any frame has been large, *every*
  subsequent shrink (e.g. an edit that shortens a rendered message, a collapsed thinking block)
  trips a full redraw. This is correctness-driven (scrollback gap avoidance) but over-triggers
  on ordinary content edits.
- **Fix options:**
  1. **Differentiate “scrollback-affecting shrink” from “in-viewport shrink.”** Only force a
     full redraw when the removed lines were *above* the current viewport top (i.e. scrollback
     would be wrong); in-viewport shrinks are handled by the differential path’s
     extra-line-clear logic (already present in `writeDifferential`).
  2. **Track `maxLinesRendered` per-scrollback-region** rather than globally.
- **Recommended:** option 1.

### 2.5 🟡 `agentNodeFor`: O(history) `strings.Join` + `ansi.Strip` every frame
- **Location:** `tui/tui.go:986` (`Text: ansi.Strip(strings.Join(lines, "\n"))`) inside
  `buildScene`, called for every layer every frame.
- **Problem:** For the chat layer this joins + ANSI-strips the entire transcript into a DOM
  node `Text` that is only consumed by agent tooling / tests (`AgentFrame`), not by the live
  terminal render. Pure overhead in production frames.
- **Fix options:**
  1. **Build `Nodes` lazily** (only when an `AgentFrame` is actually requested by a tool/test),
     not unconditionally in `buildScene`.
  2. **Strip over the visible tail only** (ties into 2.1).
  3. **Cache the node text** keyed on generation, invalidated on mutation.
- **Recommended:** option 1 (cleanest — separates the terminal render path from the agentic
  inspection path entirely).

---

## Area 3 — Network / API non-happy paths

### 3.1 🔴 `retryStream` ignores the rich `ProviderError` classification (retries the unrecoverable)
- **Location:** `internal/agentic/agent_streaming.go:888` (`retryStream`), called from
  `handleStreamFailure`. The classification exists in
  `internal/agentic/provider/hooks/errors.go` (`ProviderError{IsRetryable, IsContextOverflow,
  IsRateLimit, RetryAfter, RetryAfterMs}`) and is populated by the hook pipeline.
- **Problem:** `retryStream` loops `for retry := 0; retry < 2` with a fixed
  `time.Duration(retry+1) * time.Second` backoff and retries **any** non-`Canceled` error,
  never consulting `IsRetryable`, `IsRateLimit`, or `RetryAfter`. Consequences:
  - **HTTP 400 / malformed-request** is retried twice (guaranteed to fail again) — wastes a
    round-trip and delays the user-visible error.
  - **401/403 auth** is retried (never succeeds without new credentials).
  - **429 rate limit with `Retry-After: 30`** is retried after only 1 s / 2 s — guaranteed to
    hit the rate limit again and burn the retry budget, then surface “connection lost.”
  - **5xx** (genuinely retryable) gets the same 1–2 s as everything else.
  - The `RetryAfter`/`RetryAfterMs` fields parsed by the hook pipeline are computed and then
    discarded — dead classification.
- **Impact:** Slow, wrong user feedback on unretryable errors; self-inflicted rate-limit
  amplification; the model looks “flaky” when the real issue is a 400.
- **Fix options:**
  1. **Classify before retrying.** In `handleStreamFailure`, `errors.As(err, &provErr)`:
     - `!provErr.IsRetryable` (4xx except 408/429, auth) → do **not** retry; surface immediately
       with a clear message.
     - `provErr.IsRateLimit` → honor `RetryAfter`/`RetryAfterMs` (fallback to exponential
       backoff) and retry until a configured deadline.
     - 5xx / network / `io.EOF` / idle-timeout → exponential backoff with jitter, capped
       attempts.
  2. **Centralize the policy** in the provider layer (a `RetryPolicy` the transport applies),
     so the agent loop just calls `provider.Stream` and the transport handles idempotent
     retries of the *request setup* (the SSE body itself can’t be replayed mid-stream, so only
     connection-setup failures are transport-retryable; mid-stream failures still bubble to the
     agent).
  3. **At minimum** extract a `shouldRetry(err) bool` + `backoffFor(err, attempt) time.Duration`
     and call them from `retryStream`. Keep the agent-level retry but make it informed.
- **Recommended:** option 1 now (highest value per LOC), then evolve toward option 2.

### 3.2 🔴 `SSEFallbackError` is dead — WebSocket “fallback to SSE” never happens
- **Location:**
  - Defined/returned: `internal/agentic/provider/transport/websocket.go:98-105, 169`
    (`fallbackError` produces `&SSEFallbackError{...}` when `t.SSEEndpoint != ""`).
  - Consumed: **nowhere** (`grep` confirms only definition + production sites; no
    `errors.As(&SSEFallbackError{})` anywhere). `executeRequest` (`runtime.go`) just passes the
    error to `stream.CloseWithError`; the agent’s `retryStream` retries on the **same**
    transport, so the failure repeats identically.
- **Problem:** A documented resilience feature (WS → SSE fallback) is wired to produce a typed
  error that nothing catches. WS users (e.g. providers requiring WS) get a hard failure where
  the design promises graceful degradation.
- **Impact:** Hidden “stop state”: the user sees repeated retries then “connection lost,” with
  no indication that an SSE fallback was available and never attempted.
- **Fix options:**
  1. **Catch `SSEFallbackError` in `executeRequest`/`selectTransport`** and re-issue the same
     request on `transport.Default()` (HTTP/SSE) for that one stream. Keep it scoped to the
     single request so connection affinity isn’t disturbed.
  2. **Promote the decision to the provider layer:** on `SSEFallbackError`, mark the model’s
     transport preference as “degraded → SSE” for a cooldown window so subsequent turns skip WS.
  3. **If fallback is intentionally not supported, delete the type** and make WS failures hard
     errors with a clear message (don’t ship a feature that doesn’t fire).
- **Recommended:** option 1 (honors the existing design); option 3 only if SSE fallback is
  genuinely out of scope.

### 3.3 🟠 WebSocket streams have **no idle/stall guard**
- **Location:** `internal/agentic/provider/runtime.go:executeRequest` (line ~110):
  `if reqCtx.Options.Transport != schema.TransportWebSocket { reader = NewIdleTimeoutReader(...) }`
  deliberately skips the idle guard for WS. No equivalent exists in
  `transport/websocket.go:copyMessages` (line 230) — `conn.conn.ReadMessage()` blocks with no
  deadline.
- **Problem:** A half-open WS connection (proxy timeout, idle server, dropped route) where the
  server stops sending but keeps the socket open → `ReadMessage` blocks **forever**. The
  per-turn ctx is the only cancel, so a silent WS stall hangs the whole turn with no progress
  indicator and no retry (unlike SSE, which gets `ErrStreamIdle` → retry).
- **Impact:** “Too optimistic” assumption that a WS either delivers or errors. Real networks
  silently stall WS.
- **Fix options:**
  1. **Set a read deadline loop** in `copyMessages`: `conn.SetReadDeadline(time.Now().Add(idle))`
     before each `ReadMessage`; on `*net.OpError{Timeout:true}` treat as a stall →
     `pw.CloseWithError(ErrStreamIdle)` (reuse the SSE error so the agent’s existing idle-retry
     path fires). Reset deadline after each successful message.
  2. **Heartbeat/pong handler** if the provider sends pings (gorilla exposes pong hooks).
  3. **Wrap the WS body reader with the same `idleTimeoutReader`** by making `copyMessages`
     write to the pipe in a way the existing reader can guard (less clean — pipe writes aren’t
     cancellable by deadline).
- **Recommended:** option 1 — symmetric with SSE, reuses `ErrStreamIdle` semantics.

### 3.4 🟠 WebSocket pool: unsafe concurrent reuse of one connection + `failures` data race
- **Location:** `internal/agentic/provider/transport/websocket.go`
  - `acquireConnection` (line ~177) returns the **same** pooled `*WebSocketConnection` for a
    `sessionID` with no “in-use” lock.
  - `Do` (line 127) calls `conn.conn.WriteMessage` (line 139) and `copyMessages` (line 230)
    reads on the **same** `*websocket.Conn`.
  - `removeOnFailure` (line 214): `conn.failures++` with **no lock**; `failures` is read in
    `Do` (`conn.failures >= t.maxFailures()`) and written here from both `Do` and the
    `copyMessages` goroutine.
- **Problem (a — correctness):** WebSocket is a **framed, non-multiplexed** protocol. Two
    concurrent `Do` calls sharing one conn interleave Write/Read frames → corrupted protocol
    / wrong responses attributed to the wrong request. The session-ID pool assumes serial use
    per session, but nothing enforces it.
- **Problem (b — race):** `conn.failures` is mutated outside `conn.mu` (the mutex only guards
    `lastUsed`). `go test -race` will flag this once a concurrent test exists.
- **Problem (c):** `isHeaderTimeout` (line ~258) string-matches `"timeout"` in the error
    message — fragile, locale/impl-dependent.
- **Impact:** Under concurrent turns on the same WS session (e.g. multi-agent, or a re-entrant
    turn) the stream silently corrupts; the failures counter is unreliable so the
    “max stream failures” fallback trips at the wrong time (or never).
- **Fix options:**
  1. **Per-connection in-use mutex** (or a `sync.Mutex` in `WebSocketConnection` taken around
     the WriteMessage+streamResponse pair) so a conn is used by at most one request at a time;
     concurrent requests either wait or dial a second conn. Simplest correctness fix.
  2. **Don’t pool; dial per request** (with a short keepalive). Loses connection reuse but
     removes the multiplexing hazard entirely. Acceptable if WS latency isn’t critical.
  3. **Guard `failures` with `conn.mu`** (or make it `atomic.Int32`) regardless of the
     pooling fix.
  4. Replace `isHeaderTimeout` string-match with `errors.Is(err,
     os.ErrDeadlineExceeded)` / typed dialer errors.
- **Recommended:** option 1 + 3 + 4.

### 3.5 🟡 `summarizeHistory`/`Compact`: summarization request can self-overflow; no retry; cache-destroying
- **Location:** `internal/agentic/agent_compression.go`
  - `Compact` (line 15): replaces **all** history with `[{system}, {assistant: summary}]`.
  - `summarizeHistory` (line 36): sends the **entire** non-system history to the model in one
    request; no overflow guard, no retry, no token estimate before sending.
- **Problem (a):** `Compact` produces a conversation that starts with `system, assistant, …` —
    an **assistant message with no preceding user message**, and it discards the entire prefix.
    For strict providers (DeepSeek, some OpenAI deployments) an assistant-first or
    non-alternating history is rejected; and any provider-side prompt cache is obliterated
    because the prefix changes wholesale. Also the original `system` message is re-stored as a
    history entry while the system prompt is *also* sent via `Context.SystemPrompt` — double
    system content on the next turn (see `buildProviderHistory` which skips only index-0
    system).
- **Problem (b):** Summarizing a near-overflow history sends a request that is itself near
    overflow → the summarization call returns the same `context_length_exceeded` and `Compact`
    fails with no fallback (the turn that triggered `CompressionSummarize` then errors out).
- **Impact:** Summarize strategy is the least robust: it can fail exactly when it’s needed
    most, and when it succeeds it sacrifices caching + risks role-ordering rejection.
- **Fix options:**
  1. **Pre-flight estimate** the summarization request size; if it exceeds the window, first
     run `selective`/`tool_elision` to shrink, *then* summarize the remainder.
  2. **Preserve a valid role sequence** after compaction: `system, user("[summary of prior
     conversation]: ..."), assistant, …` so the next user turn alternates correctly. Keep the
     system prompt only in `Context.SystemPrompt`, not duplicated in history.
  3. **Add a single retry** with a shorter input (drop oldest tool results) if the first
     summarize call overflows.
  4. **Cache-aware compaction** generally — see 4.1.
- **Recommended:** options 1 + 2 (correctness); 3 for resilience.

### 3.6 🟡 Stream-error surfacing: recovery rounds can append hidden duplicate bubbles
- **Location:** `internal/agentic/agent_streaming.go` — `handleStreamFailure` emits a
  `System` content bubble per failure (`formatRetryMessage`) and `runRecoveryStream` injects a
  system message each time. The overflow guard (`overflowRecoveryAttempted`) prevents infinite
  compress+retry, and `undoLastAssistantMessage` cleans partial turns — these are good.
- **Problem (minor):** On a flaky connection the user can accumulate several
  “Error: 500 - … - retrying” system bubbles plus a final answer with no clear terminal state
  indication if all retries fail (“LLM connection lost after retries”). The terminal failure
  path returns an error, but the *intermediate* system bubbles remain in history and are sent
  to the model on the next turn (they are `Role: System` with `category: system-notification`),
  adding noise/context.
- **Fix options:**
  1. Mark retry/notification bubbles as **ephemeral** (a metadata flag) so they render but are
     excluded from `buildProviderHistory` on the next turn.
  2. Collapse multiple retry bubbles into one “Retried N times” summary entry.
- **Recommended:** option 1 (keeps the UX signal, removes context pollution).

---

## Area 4 — LLM context: caching, compaction, prompt bloat

### 4.1 🔴 Compaction strategy breaks prompt caching; `CacheMissThreshold` (the cache-aware trigger) is dead config
- **Location:**
  - `internal/agentic/compaction.go:17,33` — `MicroCompactionConfig.CacheMissThreshold` is set
    (`1 * time.Hour`) and documented (“how long the agent must have been idle before micro
    compaction is triggered”) but **never read** (grep confirms only its definition). Actual
    trigger is purely `contextRatio() < cfg.MinContextRatio`.
  - `internal/agentic/agent_compression.go`:
    - `elideToolMessages` (line ~238) **mutates** old `Assistant.ToolCalls[].Arguments` and
      `ToolRole.Content` in place to `[elided]`/`[tool result elided]`.
    - `compressSelective` drops oldest messages.
    - `microCompactForced` replaces old tool results with the truncated marker.
- **Problem:** Provider prompt caching (Anthropic `cache_control`, OpenAI/DeepSeek prefix
  caching — the infrastructure in `protocol/openai_completions.go`, `protocol/anthropic_messages.go`,
  `provider/openai/cache_control.go`) keys on a **stable prefix**. Every compaction strategy
  here either mutates messages inside the cached prefix (`tool_elision`, `micro`) or truncates
  it (`selective`), causing a **cache miss from the first mutated index onward** — exactly
  when the conversation is largest and caching matters most.
  The *designed* mitigation — `CacheMissThreshold` (“only compact when the cache would miss
  anyway because the user was idle > 1 h”) — is wired into config but **never consulted**.
  So micro-compaction fires on a pure ratio threshold even when the cache is hot, needlessly
  invalidating it. The team clearly understands the caching interaction (the comment in
  `completeStreamTurn` about duplicate assistant messages “break prompt caching” proves it),
  but the cache-aware compaction trigger was never implemented.
- **Impact:** On long sessions, every compaction event flips a cached multi-hundred-K-token
  prefix back to a full (expensive, slow) re-processing on the next turn. With DeepSeek/Anthropic
  this is real money + latency. The cache-aware guard that would prevent this exists only as
  dead config.
- **Fix options:**
  1. **Honor `CacheMissThreshold`.** Track `lastActivity` (last turn end) on the Agent; in
     `microCompactForced`, skip mutation unless `time.Since(lastActivity) > CacheMissThreshold`
     (i.e. the cache is presumed cold anyway) *or* the ratio is at the hard ceiling. This makes
     compaction cache-friendly by construction.
  2. **Compact only from the tail forward, never inside the cached prefix.** Place an explicit
     cache breakpoint; never mutate at or before the most recent cache marker. If the only way
     to fit is to mutate the cached region, do it in one shot (selective drop) rather than
     incrementally (micro), so the new prefix becomes stable immediately and re-caches once.
  3. **Prefer `selective` (drop) over `tool_elision`/`micro` (mutate) for cache health** when
     the provider advertises prefix caching: a single drop produces a new stable prefix;
     repeated in-place elisions churn it.
  4. **Surface cache hit/miss** (`Usage.CacheReadTokens` is already captured) to drive the
     decision adaptively — if the last turn was a cache hit, defer compaction another turn.
- **Recommended:** option 1 (resurrects existing design intent) + option 2 (structural). This
  is the single biggest context-cost win available.

### 4.2 🟠 Compression mutates `a.history` without the agent mutex
- **Location:**
  - `internal/agentic/agent_context.go:enforceContextCeiling` (line 39) reads/reassigns
    `a.history` with no `a.mu`.
  - `internal/agentic/compaction.go:microCompactForced` (line 43), and `truncateToolResults`.
  - `internal/agentic/agent_compression.go:compressToolElision`/`elideToolMessages` (212/238),
    `compressSelective` (262), `compressHistoryWithStrategy` (414).
  - Called from `prepareTurn` (`agent_streaming.go:777-780`) which itself runs *after*
    releasing earlier locks.
- **Problem:** The rest of the file uniformly guards `a.history` with `a.mu` (e.g.
  `buildProviderHistory`, `finalizeStreamTurn`, `GetHistory`). The compression paths do not.
  Today this is *benign in practice* because `runInternal` serializes turns via the
  `a.processing` flag — but that serialization is an invariant the mutex exists to avoid
  relying on. `go test -race` will not catch it until a second goroutine reads history
  off-turn (e.g. a stats poller, the companion agent, or `RunAndCollect` on a bus). It is a
  latent data race and a locking-discipline violation.
- **Impact:** Today: low. Tomorrow: the first off-turn history reader turns this into a live
  `fatal error: concurrent map/slice` under `-race` and potentially corrupted history.
- **Fix options:**
  1. **Hold `a.mu` for the whole compression transaction** (compute stats, mutate, done) — each
     strategy function takes the lock once. Cheap and removes the footgun.
  2. **Move compression onto the turn goroutine explicitly** and document that history is only
     mutated there, with a `// a.mu NOT required: turns are serialized by a.processing` comment
     + a debug assertion that `a.processing` is true.
- **Recommended:** option 1 (consistency with the rest of the file).

### 4.3 🟠 Proactive compaction threshold (90%) leaves little headroom for big tool results
- **Location:** `internal/agentic/agent_compression.go:maybeCompress` (line 107): default
  `ThresholdPercent = 90`; `MinContextRatio` (micro) default `0.5`.
- **Problem:** With a 90% proactive threshold, a single large tool result (e.g. a 6% read of a
  big file) arriving in the *next* turn can blow past 100% before the *reactive*
  `enforceContextCeiling`/`handleContextError` runs — i.e. compaction runs one turn too late
  and the user sees a `context_length_exceeded` error (then overflow recovery) instead of a
  smooth proactive shrink. The “too early compaction” concern is the opposite risk, but the
  current defaults lean toward “too late”: 90% + one big tool result > 100%.
- **Impact:** Intermittent context-overflow errors on tool-heavy turns that could have been
  avoided; reliance on the error-path recovery (which costs a wasted round-trip and a visible
  “Context length exceeded” bubble).
- **Fix options:**
  1. **Predictive threshold:** trigger proactive compaction at `max(ThresholdPercent,
     headroomForNextTurnEstimate)` where the estimate is based on the largest tool result in
     the last K turns. Compacts early only when recent tool results are large.
  2. **Pre-flight the outgoing request size** before sending; if the *next* turn would exceed
     (e.g. because the just-appended tool result is huge), compact *before* sending instead of
     after the model errors.
  3. **Lower default to ~80%** with a config override — conservative but blunt.
- **Recommended:** option 2 (pre-flight the request) — it directly prevents the wasted
  error→retry round-trip.

### 4.4 🟠 Prompt/tool/skill context bloat is not measured or budgeted
- **Location:** `internal/agentic/agent_streaming.go:buildProviderContext` (line ~918) ships
  `SystemPrompt + Messages + Tools` every turn. `mergeGoalProgress` (line ~953) **prepends** a
  `[goal progress]` block to the **last user message** on every turn.
- **Problem (a):** There is no accounting for the *fixed* cost of the system prompt + tool
  schemas + skill documents. These can be large (the project ships many embedded skill
  prompts), and they’re counted by the provider against the window but only loosely estimated
  (`estimateTokensFromHistory` looks at history messages, not system prompt or tool JSON).
  `checkContextLimit`/`enforceContextCeiling` therefore underestimate real usage by the size of
  `SystemPrompt + Tools`, so the ceiling fires late.
- **Problem (b):** `mergeGoalProgress` mutates the last user message by *prepending* goal
  progress text **every turn** — but it rebuilds the message fresh each turn (reads
  `ActiveGoalProgress()`), so the prefix of that user message changes each turn even if the
  user’s actual text is identical. That’s a **cache miss** on the last user message every turn
  for goal-driven sessions (and goal progress can be sizeable).
- **Problem (c):** No tool-result size cap at the source — a `read_file` of a huge file flows
  into history verbatim; the only truncation is after-the-fact via compaction.
- **Impact:** Underestimated context usage → late compaction (see 4.3); per-turn cache churn
  from goal-progress prepending; large raw tool outputs inflate the prefix that later gets
  elided (wasted cache write).
- **Fix options:**
  1. **Account for fixed costs:** include `len(SystemPrompt)` and a serialized `Tools` size
     estimate in `computeContextStats`/`estimateTokensFromHistory` so ceilings reflect reality.
  2. **Stabilize the user message for caching:** put `[goal progress]` in a *separate*, earlier
     message (or a system block) that changes in its own slot, and keep the user’s verbatim
     text as its own block so the prefix up to the user text stays byte-stable. Or only append
     goal progress when it *changed*.
  3. **Cap tool outputs at the source** (per-tool max-bytes with a tail-preserving truncation),
     not just via post-hoc compaction. The infra exists (`tools/common/truncate.go`); make it
     the default for results entering history.
  4. **Tool/schema pruning:** send only tool schemas relevant to the current mode (the project
     already has tool groups / modes — ensure off-mode tools aren’t serialized).
- **Recommended:** options 1 + 2 (correctness + cache); 3 and 4 for follow-on bloat reduction.

### 4.5 🟡 `mergeGoalProgress` mutates the request slice and re-prefixes every turn
- **Location:** `internal/agentic/agent_streaming.go:mergeGoalProgress` (line ~945) — finds the
  last `RoleUser` message and `msgs[i].Content = append([]ContentBlock{prefix}, ...)`.
- **Problem:** Beyond the caching concern (4.4b), this rebuilds the user message content slice
  from scratch every turn, so even when goal progress is identical between turns, the
  underlying `ContentBlock` slice identity changes (fine for the wire, but it means any
  “did this message change?” optimization elsewhere can’t rely on slice stability). Minor, but
  worth folding into the 4.4 fix.
- **Fix:** same as 4.4 option 2.

---

## Cross-cutting summary

| # | Area | Severity | One-line | File |
|---|------|----------|----------|------|
| 1.1 | chan | 🔴 | send-on-closed-channel race in AgentBus | `internal/agentic/comm.go` |
| 3.1 | net  | 🔴 | retry ignores ProviderError classification | `internal/agentic/agent_streaming.go:888` |
| 3.2 | net  | 🔴 | SSEFallbackError produced but never handled | `transport/websocket.go` + `runtime.go` |
| 4.1 | ctx  | 🔴 | compaction churns prompt cache; cache-aware trigger is dead config | `compaction.go`, `agent_compression.go` |
| 1.2 | chan | 🟠 | blocking emit on stream goroutine couples stream↔TUI | `core/agentmanager.go:969` |
| 1.3 | chan | 🟠 | ToolScheduler unbounded parallelism; Collect can hang | `tool_scheduler.go` |
| 2.1 | TUI  | 🟠 | chat viewport culling wired but non-functional | `tui/chat_viewport.go`, `tui/tui.go:925` |
| 2.2 | TUI  | 🟠 | compositor O(history)/frame (alloc/copy/scan) | `tui/compositor.go` |
| 2.3 | TUI  | 🟠 | fullFrame re-emits whole canvas on resize | `tui/compositor.go:556` |
| 3.3 | net  | 🟠 | WebSocket streams have no idle/stall guard | `runtime.go`, `transport/websocket.go` |
| 3.4 | net  | 🟠 | WS pool: non-multiplexed conn shared; failures race | `transport/websocket.go` |
| 3.5 | ctx  | 🟠 | Compact can self-overflow, no retry, cache-destroying | `agent_compression.go` |
| 4.2 | ctx  | 🟠 | compression mutates history without mutex | `agent_context.go`, `agent_compression.go` |
| 4.3 | ctx  | 🟠 | 90% proactive threshold too late for big tool results | `agent_compression.go` |
| 4.4 | ctx  | 🟠 | fixed prompt/tool/skill cost not budgeted; goal-progress churns cache | `agent_streaming.go` |
| 1.4 | chan | 🟡 | idleTimeout goroutine-per-Read | `provider/idle_timeout.go` |
| 1.5 | chan | 🟡 | recursive panic-restart grows stack | `internal/app/events.go` |
| 1.6 | chan | 🟡 | RetryWithBackoff dead code | `provider/retry.go` |
| 2.4 | TUI  | 🟡 | clearOnShrink over-triggers full redraw | `tui/compositor.go:460` |
| 2.5 | TUI  | 🟡 | agentNodeFor O(history) Join+Strip/frame | `tui/tui.go:986` |
| 3.6 | net  | 🟡 | retry bubbles persist into next-turn context | `agent_streaming.go` |
| 4.5 | ctx  | 🟡 | mergeGoalProgress re-prefixes user msg each turn | `agent_streaming.go:945` |

## Recommended fix order (impact × risk)

1. **3.1** classify errors before retry — stops burning retries on 4xx/429 and respects
   `Retry-After`. Low risk, high user-visible payoff.
2. **1.1** AgentBus close race — pure correctness panic. Small, contained fix.
3. **4.1** resurrect `CacheMissThreshold` + cache-aware compaction — biggest context-cost win.
4. **2.1 → 2.2 → 2.3** finish the viewport culling the code already promises; cascade fixes
   compositor cost and resize repaint.
5. **3.2 / 3.3 / 3.4** WS resilience triad (fallback, idle guard, pool safety).
6. **1.2** decouple stream↔TUI with a forwarder.
7. **1.3** ToolScheduler parallelism cap + per-task deadline.
8. The 🟡 items opportunistically while touching each area.

Each fix should land with a test that would have caught it (race test for 1.1/3.4/4.2; a
fake-transport test for 3.1/3.2/3.3; a render-perf or filmstrip test for 2.x per the
`tui-test` skill; a cache-accounting unit test for 4.x).
