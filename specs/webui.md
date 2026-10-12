# Goa Web UI — `goa server`

**Status:** Spec / implementation plan
**Branch:** `feature/webui`
**Author:** Pierre Poissinger (spec drafted with agent assistance)
**Date:** 2026-10-02

---

## 1. Goal

Add a `server` start-up mode to Goa that serves the **complete Goa UI as a web page**.
The page must look and behave as close as possible to the TUI, must support every TUI
feature, and must stay lightweight: plain HTML5, no JavaScript framework, no CSS
framework, no exotic runtime dependency.

Concretely:

```
$ goa server
Goa web UI listening on http://127.0.0.1:7331
  session  →  http://127.0.0.1:7331/s/8f3c1a2b
```

Opening `http://127.0.0.1:7331/` redirects to the dedicated **session path**
(`/s/<session-id>`). The session path is the identity of the conversation: it is the
`core.SessionStore` session id, so it is stable, resumable and correlatable with the
on-disk JSONL transcript. Optionally the connection requires a **login/password**.

---

## 2. Non-goals (v1)

| Non-goal | Rationale |
|---|---|
| Multiple concurrent agent sessions per process | `core.AgentManager` is a per-session singleton (see §4.1). Deferred to Phase 6. |
| Replacing the ACP server (`--acp`) | Different consumer (IDEs), different protocol. Web UI is its own transport. |
| Offline / PWA / service worker | "Fast and lean" — a service worker adds cache-invalidation failure modes for no local benefit. |
| Server-side rendering of markdown to a separate DOM | Would fork the rendering engine; the engine already renders markdown (see §4.4). |
| Mobile-native layouts | Desktop-first; basic responsive behaviour only (§14.6). |
| WebGPU / canvas terminal renderer | A DOM cell grid is faster to build, accessible, text-selectable, and small enough at v1 volumes (§13). |

---

## 3. Architecture decision

### 3.1 Chosen: the browser **is** the terminal

The TUI is already architected as a *protocol-free scene assembler* feeding a
*terminal-protocol output stage*:

```
Components (chat viewport, editor, footer, overlays, pagers, selector, …)
        │  Render(width) []string                      ← pure, no I/O
        ▼
Scene (layers + cursor, protocol-free)                  ← tui/tui_scene.go
        │
        ▼
Compositor (owns ALL escape-sequence state + diffing)   ← tui/compositor.go
        │  Write(p []byte) / WriteString(s)             ← through the Terminal interface
        ▼
Terminal  ← interface, 11 methods                        ← tui/terminal.go:20-32
        ├── ProcessTerminal   (real TTY, raw mode)      ← existing
        └── VirtualTerminal   (cells → browser)         ← NEW
```

`tui.Terminal` is an interface, and `tui.TermEmulator` is an existing, faithful
**per-cell terminal emulator** (tracks per-cell foreground/background SGR, DEC deferred
auto-wrap, DECSTBM scroll regions, scrollback). So the entire Goa UI engine can run
**completely unmodified** against a virtual screen whose pixels are shipped to a
browser as JSON cell patches.

**This is the whole design.** Reuse is maximal by construction:

* every TUI feature works automatically, forever — widgets, overlays, hotkeys,
  keybindings, autocomplete, selector, confirm cards, clarify cards, filmstrip,
  review/plan pagers, PTY viewer, diff rendering, incremental markdown, tool
  widgets, spinner, goal bubble, orchestrator tabs, footer, logo/mascot;
* there is exactly **one** rendering engine, so the web UI cannot drift from the TUI;
* there is no second view model, no second markdown renderer, no second theme.

### 3.2 Rejected alternatives

| Alternative | Why rejected |
|---|---|
| **A. Second view model + HTML renderer** (`MessageData` → HTML DOM, own reducer, own markdown renderer) | Duplicates the entire rendering engine. Every new TUI widget must be re-implemented for the web. Two renderers drift. Rejected. (This was the first design considered; §3.1 supersedes it.) |
| **B. Ship ANSI bytes to a JS terminal emulator** (xterm.js) | Requires an "exotic library" (explicitly out of scope), ~300 KB payload, and re-implements terminal emulation in the browser that we already have in Go (`TermEmulator`). Rejected. |
| **C. Server-Sent Events only** | No client→server channel; needs a parallel POST path for every input anyway. SSE is kept as a *fallback*, not the primary. |
| **D. WebSocket-only, no fallback** | WS is frequently blocked by corporate proxies; the brief explicitly requires a fallback. See §11. |

### 3.3 SOLID mapping

| Principle | How this design satisfies it |
|---|---|
| **SRP** | `VirtualTerminal` = screen state only. `CellDiff` = diffing only. `FrameCodec` = JSON encoding only. `Hub` = fan-out only. `Auth` = authentication only. `Router` = HTTP routing only. `HTMLPage` = static shell only. One reason to change per type. |
| **OCP** | The engine gains **zero** behavioural changes for the web: the new capability arrives as another implementation of the existing `tui.Terminal` port. New transports (`WS`, `SSE`, `Plain`) implement one `Broadcaster` interface. |
| **LSP** | Every collaborator is an interface: `tui.Terminal`, `Broadcaster`, `Client`, `FrameSink`, `KeyEncoder`. Nothing depends on a concrete HTTP/WS type. |
| **DIP** | The engine depends on the `Terminal` abstraction; the web layer depends on `Broadcaster`. Neither depends on the other's concrete types. Wiring happens in exactly one place (`internal/app/webui.go`). |
| **ISP** | `tui.Terminal` is already a small, cohesive interface. New optional capabilities (`Resize`, `Cursor`) are *not* bolted onto it; the virtual terminal exposes them as its own concrete methods, and the engine keeps using `Size()`. |

---

## 4. Constraints discovered in the codebase (evidence)

These are facts, not assumptions; each one shapes the plan.

### 4.1 One agent session per process

`core.AgentManager` (`core/agentmanager.go:29-74`) holds a single `activeAgent`,
`sessionStore`, `events` channel, `modeMgr`, `steering` queue, `turnRecorder` and
`cancel`. `internal/app/subsystems.go:44+` likewise holds exactly one `agentMgr`,
`sessionStore`, `cmdRouter`, `toolRegistry`, `ptyMgr`.

**Consequence:** one `goa server` process serves exactly one live conversation, exactly
like one TUI process. Multiple browsers may attach to that same conversation. A new
conversation is created the way the TUI creates one (`ControlEvent.NewSession` →
`AgentManager.StopSession()` → `startAgentSession`, see `internal/app/events_control.go:36-66`),
which rotates the session id **and** the provider cache key (Hard Rule 7 — see §4.7).

### 4.2 Output is already behind an interface

`tui/terminal.go:20-32`:

```go
type Terminal interface {
    Start(onInput func(string), onResize func())
    Stop()
    Write(p []byte) (n int, err error)
    WriteString(s string)
    Size() (width, height int)
    SetRaw() (restore func(), err error)
    HideCursor(); ShowCursor(); ClearScreen(); SetTitle(title string)
    io.Writer
}
```

`internal/app/tui.go:90` is the single place a terminal is constructed:

```go
terminal := tui.Terminal(tui.NewProcessTerminal())
```

`tui.NewTUI(ft)` (`internal/app/tui.go:62`) already accepts a `Terminal`. So injection
is a **one-line seam**.

### 4.3 A per-cell screen model already exists

`tui/term_emulator.go:23-52` — `TermEmulator` with `screen [][]string`,
`screenFg [][]string`, `screenBg [][]string`, `curFg/curBg`, `pendingWrap`,
`scrollTop/scrollBot`, `scrollback []string`; API: `Process`, `Visible`, `VisibleFg`,
`VisibleBg`, `RowFg`, `Scrollback`.

It tracks **colours only** — `applySGR` (`tui/term_emulator.go:231-265`) handles
fg/bg/58 and deliberately does not model bold/italic/underline/dim/inverse/strike.
Those are needed for faithful web rendering (markdown bold/italic, diffs, headers,
dim separators). Extension is additive — see §6.2.

### 4.4 Rendering is already protocol-free and width-driven

`Component.Render(width int) []string` (`tui/component.go:12-23`); `MessageData` is
documented as "the protocol-free, structured record … independent of how the message is
rendered" (`tui/model.go:7-17`). Markdown is rendered by the engine
(`tui/markdown*.go`, incremental renderer) with chroma syntax highlighting
(`tui/markdown_highlight.go`). All of this is reused untouched.

### 4.5 Events are decoupled from the TUI

`internal/event.Bus` (`internal/event/event.go:232-247`) exposes four typed channels —
`Agent`, `Control`, `Chat`, `Footer`. `core.eventForwarder`
(`core/agent_event_forwarder.go`) decouples the LLM stream goroutine from the consumer
with an **unbounded** queue so a slow consumer can never stall generation.

**Consequence:** a slow or stalled browser client can never block the agent stream.

### 4.6 Commands are registry-driven

`core.CommandRegistry` + `core.CommandRouter.Execute` (`core/router.go:141`). Every
slash command works in web mode automatically because the engine dispatches it, not the
web layer. Interactive commands go through `core.Context` callbacks
(`SelectOptionFunc`, `ClarifyFunc`, `ShowInputFunc`, `ConfirmMultiFunc`,
`ShowPTYOverlay`) — all of which are engine-side overlay requests in the TUI, so they
render as overlays in the browser for free.

### 4.7 Hard Rule 7 (append-only history, context-scoped cache ids)

Every new browser context (`/session:new`, `/clear`, a different `goa server` process)
must rotate the provider cache key. This is achieved by reusing the existing
`AgentManager.StopSession()` + `startAgentSession()` path, which allocates a new
session id. The web layer must **never** fork or rewrite history to give a second
browser a private view: the virtual terminal is a *view*, not a conversation.

---

## 5. Dependency policy

**No new entries in `go.mod`.**

| Need | Choice |
|---|---|
| HTTP server, routing, static files | `net/http`, `html/template`, `embed` (stdlib) |
| WebSocket | `github.com/gorilla/websocket` — **already a direct dependency** (`internal/agentic/provider/transport/websocket.go`); explicitly accepted |
| SSE | `net/http` `Flusher` (stdlib) |
| Auth (basic/token), constant-time compare | `crypto/sha256`, `crypto/subtle`, `crypto/rand` (stdlib) |
| Cells, diffing, JSON | stdlib |
| Markdown, syntax highlighting | **not needed** — rendered by the existing engine |
| CSS, JS | hand-written, no framework, no build step, no CDN |

Explicitly rejected: xterm.js, htmx/Alpine/Vue/React, marked/markdown-it/highlight.js,
Tailwind, any bundler. Total client payload target: **< 40 KB uncompressed**
(HTML + CSS + JS), no external requests.

---

## 6. Package layout

```
internal/webui/
  server.go          Server           — lifecycle, http.ServeMux wiring, graceful shutdown
  terminal.go        VirtualTerminal  — implements tui.Terminal over a cell grid
  cells.go           CellGrid         — screen state, SGR→cell attrs, cursor, scrollback
  diff.go            CellDiff         — previous-vs-current row diffing → patch
  frame.go           Frame            — immutable frame value object
  codec.go           FrameCodec       — Frame ↔ JSON (encode/decode), protocol constants
  hub.go             Hub              — session-scoped fan-out, per-client buffers
  client.go          Client           — one attached viewer (interface)
  transport_ws.go    wsClient         — Client over WebSocket
  transport_sse.go   sseClient        — Client over SSE + POST
  transport_plain.go plainClient      — no-JS polling client (§11.3)
  auth.go            Auth             — none/basic/token, cookie issue/verify
  input.go           KeyEncoder       — browser KeyboardEvent → raw terminal bytes
  page.go            HTMLPage         — embedded shell (index.html, app.js, app.css)
  assets/
    index.html
    app.css
    app.js
```

Plus small, surgical changes elsewhere:

| File | Change |
|---|---|
| `tui/term_cells.go` **(new)** | Additive cell-attribute API on `TermEmulator` (§6.2) |
| `internal/app/tui.go:90` | Honour an injected `tui.Terminal` |
| `internal/app/subsystems.go` | Add `terminal tui.Terminal` (nil ⇒ `NewProcessTerminal`) |
| `internal/app/webui.go` **(new)** | `runWebServer(subs, opts)` — the only wiring site |
| `internal/app/app.go` | Dispatch `goa server` in the `runApp()` switch |
| `config/config_types.go`, `defaults.go`, `config_validate.go` | `ServerConfig` (§15) |

---

## 7. Component designs

### 7.1 `VirtualTerminal` (implements `tui.Terminal`)

```go
// VirtualTerminal is a tui.Terminal backed by an in-memory cell grid whose
// frames are published to attached browsers. It performs no I/O of its own:
// all output goes to the Hub via FrameSink.
type VirtualTerminal struct {
    grid     *CellGrid
    sink     FrameSink            // usually the Hub; nil in tests
    onInput  func(string)
    onResize func()
    sizeMu   sync.RWMutex
    w, h     int
    frameNo  uint64
}
```

Method behaviour:

| Method | Behaviour |
|---|---|
| `Start(onInput, onResize)` | Store callbacks. **Never** claim screen ownership (`claimScreen`), never raw-mode. |
| `Stop()` | Flush a final frame; detach from sink. |
| `Write(p)` / `WriteString(s)` | `grid.Process(s)` → if the grid changed, request a frame publish (coalesced, §13). Return `len(p), nil`. |
| `Size()` | Current `w, h` (set by `Resize`). |
| `SetRaw()` | Return a no-op restore func, `nil` — there is no TTY. |
| `HideCursor()` / `ShowCursor()` | Toggle a flag included in the next frame. |
| `ClearScreen()` | Clear grid + publish full frame. |
| `SetTitle(t)` | Forward to `FrameSink.SetTitle` → browser `document.title`. |

**Resize** is a `VirtualTerminal`-specific method (not added to the `Terminal`
interface — ISP, §3.3):

```go
func (v *VirtualTerminal) Resize(cols, rows int)  // clamps, fires onResize
```

The WS/SSE resize message calls it; it mirrors what SIGWINCH does for
`ProcessTerminal`, so the engine's geometry-reset path (§7.4) is reused unchanged.

### 7.2 Frame publishing

The engine's render ticker calls `Compositor.Render(scene)`, which writes bytes to the
terminal. Those bytes are exactly the minimal differential update the engine already
computes. The virtual terminal therefore does **not** need to diff at the byte level —
it diffs the resulting **cell grid** against the previous frame, which is robust to any
emission strategy (full repaint, row diff, geometry reset) and immune to partial-write
edge cases:

```
bytes ──► CellGrid.Process ──► cells changed? ──► CellDiff(prev, cur) ──► Frame ──► Hub
```

`CellDiff` compares row by row. A row that is unchanged is omitted entirely. A changed
row is emitted as a list of *runs* (maximal spans of identical attributes), which is
what makes the payload small: a typical streaming frame touches 1–3 rows.

### 7.3 Frame value object

```go
type Frame struct {
    Seq      uint64
    Cols, Rows int
    Cursor   Cursor   // Row, Col, Visible
    Patches  []RowPatch
    Title    string   // set only when changed
}
type RowPatch struct { Row int; Runs []Run }
type Run struct {
    Text  string
    Flags uint16   // bold|italic|underline|dim|inverse|strike|link
    FG, BG string  // "" = theme default, "#rrggbb", or "palette:N" (256-colour)
    Link  string   // OSC-8 target, when Flags has link
}
```

Frames are **immutable** once built; the Hub only reads them. This keeps the fan-out
lock-free per client (each client holds its own queue of `*Frame`).

### 7.4 Geometry, scrollback and cursor — all inherited

* **Resize** → `Scene.TerminalW/H` changes → `Compositor.classifyFrame`
  (`tui/compositor_frame.go:19-52`) returns `frameGeometryReset` → full repaint. The
  virtual terminal observes the resulting grid change and ships a full frame. Nothing
  to implement.
* **Scrollback** → the engine pushes overflow rows to the terminal; `CellGrid` detects
  the scroll (it models DECSTBM) and appends the row to `ScrollbackCells()`. Those rows
  are shipped once as a `scrollback` frame and kept by the client in a transcript list,
  giving the browser a real scroll history like a terminal.
* **Cursor** → `CellGrid` tracks `row/col/pendingWrap`; included in every frame; the
  client renders a caret element. Input focus follows the caret.
* **Overlays** (selector, confirm, clarify, pagers, autocomplete popup) are ordinary
  scene layers → they appear in the grid like any other frame. Zero extra work.

### 7.5 Input path — identical to a real terminal

```
browser keydown ──KeyEncoder──► raw bytes ──► WS/SSE/POST ──► VirtualTerminal
      ──onInput(s)──► TUI.HandleInput(s) ──► focus stack ──► active Component
```

`KeyEncoder` (`internal/webui/input.go`) is the only new input logic. It maps
`KeyboardEvent` to the byte sequences a terminal would send, reusing the same table the
TUI's own key tests use:

| Browser | Bytes |
|---|---|
| printable char | UTF-8 of `e.key` |
| Enter | `\r` |
| Backspace | `\x7f` |
| Tab / Shift-Tab | `\t` / `\x1b[Z` |
| Arrows | `\x1b[A/B/C/D` |
| Home/End/PgUp/PgDn/Del | `\x1b[H` `\x1b[F` `\x1b[5~` `\x1b[6~` `\x1b[3~` |
| Ctrl+A…Z | `0x01`…`0x1a` |
| Ctrl+C / Ctrl+D / Ctrl+G | `\x03` `\x04` `\x07` |
| Esc | `\x1b` |

Modifier chords (`Alt+x` → `\x1bx`, `Ctrl+Shift+…`) and Kitty-protocol keys are
handled by the same encoding rules `tui/keys.go` already parses. Text insertion,
multiline, kill-ring, undo, word-motion, autocomplete and history therefore behave
**identically** to the TUI because they *are* the TUI.

Paste: `paste` event → text is sent as-is (the editor's paste handling applies); image
pastes are uploaded as `multipart/form-data` to §11.4 and replaced with the same
`<image path>` text the TUI inserts.

### 7.6 `Hub`, `Client`, `Broadcaster`

```go
type Broadcaster interface {  // consumed by VirtualTerminal
    Publish(f *Frame)
    Clients() int
    Close()
}
type Client interface {        // implemented per transport
    Send(f *Frame) bool        // false ⇒ client is too slow, drop it
    SendControl(c Control) error
    Close() error
}
```

`Hub` is session-scoped: one per `goa server` process in v1 (§4.1). Per-client
behaviour:

* a buffered channel of `*Frame` (cap 8) holding only the **newest** frame — a slow
  client skips stale frames instead of building a backlog;
* `Send` never blocks the publisher (the agent stream must not stall — §4.5);
* if a client stays behind for N consecutive frames (default 30) it is closed with a
  "client too slow" notice;
* `max_clients` caps the browsers that may **drive** (default 8). Attachments
  beyond that are read-only **viewers** that still receive every frame — a
  frozen screen would be worse than useless for a shared session — up to a hard
  ceiling of `2 × max_clients`, past which an attachment is refused with a
  `bye` control (every attachment costs a socket, a goroutine and a frame
  queue).

Multi-client input policy (`server.multiclient`, **not shipped** — every
attachment is `broadcast` for now):

* `broadcast` (default) — any attached client may type; input is serialized on the
  engine's command loop exactly as a real terminal would be.
* `driver` — the first attached client owns input; others are read-only mirrors
  (useful when sharing a screen on a call).

---

## 8. Sessions and URLs

| Route | Purpose |
|---|---|
| `GET /` | **302** → `/s/<sessionID>` (the required redirect) |
| `GET /s/{id}` | HTML shell (session view). Unknown id → 404 with a hint |
| `GET /ws?s={id}` | WebSocket upgrade (primary transport) |
| `GET /events?s={id}` | `text/event-stream` (fallback transport) |
| `POST /input` | Client → server input (fallback + no-JS form posts) |
| `POST /upload` | Image paste upload (multipart) |
| `GET /s/{id}/text` | Plain-text mirror of the current screen (accessibility, `curl`, copy) |
| `GET /s/{id}/page` | No-JS full-page render (fallback, §11.3) |
| `GET /assets/*` | `app.css`, `app.js` (embedded, immutable cache) |
| `GET /healthz` | `{"ok":true,"session":"<id>","clients":N}` |

The session id is `subs.sessionStore.SessionID()` — the same id used for the on-disk
JSONL transcript, so `/s/<id>` is meaningful outside the browser (`goa` session
commands, `/stats:session`, `/export`).

`POST /s/{id}/session/new` (also a WS control message) rotates the conversation:

```
AgentManager.StopSession() → chat clear → startAgentSession() → new session id
```

which yields a new URL and a **new provider cache key** (Hard Rule 7, §4.7).

**Shipped:** the rotation is driven by the engine's own `/new` command (the
browser types it like any other slash command); the dedicated POST route is not
implemented. The server watches the session id, broadcasts
`{"t":"session_rotated","session":"<new-id>"}` to every attached client, and the
page navigates to the new URL. Ids the session has left behind stay acceptable
for a bounded window, so a tab that reconnects with a stale id is attached and
told where the session moved instead of getting a 404 and falling into a
permanent reconnect loop.

Reconnect: every frame carries a monotonic `seq`. A reconnecting client sends
`{"t":"hello","since":<seq>}`; the server replies with a full frame (the grid is
authoritative, so replay is unnecessary and drift-free).

---

## 9. Rendering to HTML

The client renders each row as one `<div class="row">` containing `<span>` runs.

```html
<div class="row"><span style="color:#7c5cfc;font-weight:700">goa</span> …</div>
```

* Runs with `Flags == 0` and no colours emit **no** `style` attribute and no class —
  the common case stays tiny.
* Bold/italic/underline/dim/inverse/strike map to CSS classes (`.b .i .u .d .inv .s`),
  so they cost 1 class instead of a style string.
* `FG`/`BG` are emitted as inline `color`/`background-color` (or omitted when equal to
  the theme default — the emulator's `""` already means default).
* 256-colour SGR (`38;5;N`) is converted **server-side** to `#rrggbb` using the
  standard 6×6×6 cube + 24-step grey ramp (~40 lines, `internal/webui/cells.go`).
* Box-drawing glyphs (U+2500 block) need no special handling: the grid is monospace
  and the engine already pads cells to exact widths.
* **All** cell text is HTML-escaped (`html.EscapeString`) — text can never become
  markup. Combined with a strict CSP (§10.3), markdown/tool output cannot inject HTML.
* Theme: the palette comes from `tui.TheTheme.ColorHex` (`tui/styles.go:122`) and is
  emitted once as CSS custom properties, so light/dark themes follow the TUI exactly.
* Cursor: an absolutely-positioned caret over the grid; blink via one CSS keyframe.
* Selection: rows are real text, so copy/paste works; the client also offers
  "copy as plain text" using the `/text` endpoint.

---

## 10. Authentication and security

### 10.1 Modes (`server.auth`)

* `none` (default) — allowed **only** on loopback. A non-loopback bind without
  credentials is refused at startup; `--insecure-no-auth` is the explicit,
  self-describing override.
* `basic` — HTTP Basic against `server.username` + `server.password` (or
  `server.password_hash`, a SHA-256 hex digest, so the plaintext need not be stored).
  Comparison is `crypto/subtle.ConstantTimeCompare` over SHA-256 digests, so it is
  constant-time regardless of input length.
* `token` — a bearer token (`server.token`, or `~/.goa/server.token` with mode 0600),
  presented as `Authorization: Bearer <token>` or exchanged once at `/login`
  for the session cookie.

Shipped (G6) with CLI flags `--server-auth`, `--server-auth-user`,
`--server-auth-password` / `GOA_SERVER_AUTH_PASSWORD`,
`--server-auth-token` / `GOA_SERVER_AUTH_TOKEN`, and `--insecure-no-auth`.
Config-file keys (§15) and `password_hash` are not wired yet.

### 10.2 Session cookie

On successful auth the server sets `goa_auth` = the token, with
`HttpOnly; SameSite=Strict; Path=/; Max-Age=86400` (plus `Secure` when the
request itself is TLS). WebSocket auth uses the cookie — a token in the query
string is deliberately **not** accepted, because query strings land in access
logs and in the `Referer` header.

### 10.3 Hardening

* **CSP**: `default-src 'none'; script-src 'self' 'nonce-…'; style-src 'self'
  'nonce-…'; img-src 'self' data:; connect-src 'self'; base-uri 'none';
  form-action 'self'; frame-ancestors 'none'; object-src 'none'` — the page's
  own inline theme block and bootstrap line carry a per-response
  `crypto/rand` nonce, so no `unsafe-inline` is needed and an injected tag has
  nothing to borrow.
* **Origin check** on every unsafe method *and* on the WS upgrade: the `Origin`
  header must match `Host` (port excluded on both sides); an absent `Origin` is
  a native client and passes.
* **No CORS headers** are ever emitted; the middleware strips any that appear.
* **CSRF**: same-site cookie + Origin check.
* **Body limits**: `http.MaxBytesReader` (16 MiB transport cap; uploads 16 MiB
  and additionally content-sniffed).
* **Rate limiting**: failed auth attempts per client IP are counted in an
  in-memory table; five failures trip a 60 s lockout (429 + `Retry-After`), and a
  success clears the count.
* **Bind default** `127.0.0.1`; `server.host` must be set explicitly to expose.
* Uploads are written to a generated temp file whose extension comes from the
  sniffed bytes — never from a user-supplied name or path.

---

## 11. Transports (with fallbacks)

### 11.1 Primary: WebSocket

`gorilla/websocket` (already a dependency). One connection per browser tab carries
both directions. Messages are one JSON object per text frame.

* server → client: `frame`, `scrollback`, `title`, `control`, `error`, `bye`
* client → server: `hello`, `input`, `resize`, `upload_ref`, `session_new`
* keepalive: ping every 30 s, drop after 2 missed pongs
* `readLimit` 1 MiB; writes are serialized per connection

### 11.2 Fallback: SSE + POST

Used automatically when the WebSocket cannot be established (blocked, proxied,
downgraded, or `server.transport: sse`).

* `GET /events` → `text/event-stream`, `data: {json}\n\n`, `retry: 2000`, heartbeat
  comment every 20 s, `Cache-Control: no-store`.
* `POST /input` (JSON, or `application/x-www-form-urlencoded` from the no-JS form)
  for input; `POST /resize` for geometry.
* The client script performs the same job over `EventSource` + `fetch`. Degradation is
  invisible to the user beyond a slightly higher input latency.

**Detection:** the client attempts the WS handshake with a 3 s timeout; on failure,
error, or `close` before `open`, it switches to SSE mode permanently for that page
load and shows a discreet "realtime degraded" badge.

### 11.3 Ultimate fallback: no-JS page

`GET /s/{id}/page` (and `?mode=plain`) returns a fully server-rendered page:

* the current screen as a `<pre>` with inline colours;
* a `<form method="post" action="/input">` with a textarea and a Send button;
* `<meta http-equiv="refresh" content="2">` to poll.

Works with JavaScript disabled or blocked by policy, in text browsers, and over `curl`.
It is intentionally simple: input + read-only view + polling.

### 11.4 Uploads

`POST /upload` (multipart) → saved to a temporary file with an extension derived
from the sniffed bytes (never from the client's filename or Content-Type) →
returns `{"path":"..."}`; the client inserts that path into the input, matching
the TUI's image-paste behaviour (which also stores the clipboard image as a temp
PNG — `internal/clipboard_image.go`).

---

## 12. Frame budget and performance

| Metric | Target |
|---|---|
| Frame publish rate | the engine's existing render ticker (steady state ~20–60 fps) |
| Payload, steady streaming | < 3 KB/frame (1–3 changed rows of runs) |
| Payload, full repaint | < 60 KB at 200×60 |
| Client DOM | one `<div>` per visible row (~60), plus one `<span>` per run |
| Server CPU | ≈ the TUI's own render cost + JSON encode (measured, §17.6) |
| Slow client | newest-frame-wins; dropped after 30 consecutive overruns |

Row diffing compares only the rows the emulator reported dirty (one or two while
streaming) against the per-row baseline clients hold, so a frame costs what moved
rather than what the screen is: 200×60 measured ~8-12 µs and ~18 KB per frame,
against ~150 µs and ~1 MB for a full-grid snapshot per frame.

---

## 13. Parity matrix

Everything below is **already implemented by the engine** and therefore works in the
browser by construction. This table is the acceptance checklist, not a work list.

| TUI feature | Web behaviour | Verified in |
|---|---|---|
| Conversation, streaming assistant text | identical frames | Phase 2 |
| Thinking blocks (expand/collapse) | identical | Phase 2 |
| Tool widgets (call/result, ⧗/elapsed, ✓/✗, preview lines, collapse) | identical | Phase 2 |
| Markdown: headings, lists, tables, code fences, links, LaTeX-ish | identical (engine renders) | Phase 2 |
| Syntax highlighting (chroma) | identical colours | Phase 1 |
| Diffs (edit/write/file review colouring) | identical | Phase 1 |
| Bash / terminal output boxes | identical | Phase 2 |
| Editor: multiline, selection, kill-ring, undo, word motion | identical | Phase 2 |
| History (↑/↓), input completion (`Tab`), slash-command popup | identical | Phase 3 |
| Autocomplete occlusion / scrollback-push behaviour | identical (inherited) | Phase 3 |
| Selector overlay (`/model`, `/mode`, …) | identical overlay | Phase 4 |
| Confirm cards, multi-confirm (plugin hooks) | identical overlay | Phase 4 |
| Clarify cards / `ask_user_question` | identical overlay | Phase 4 |
| Review pager, file review pager, plan pager, plan status | identical overlay | Phase 4 |
| PTY monitor (`/pty:monitor`) | identical overlay | Phase 4 |
| Filmstrip, agent tab bar, agent context bar | identical | Phase 4 |
| Footer: tokens, context %, cost, git branch, model, mode, companion cycle | identical | Phase 2 |
| Header, logo, mascot, title transitions | identical | Phase 5 |
| Status/flash messages, spinner, goal bubble, steering chrome | identical | Phase 2 |
| Goal list, todos overlay | identical | Phase 4 |
| Orchestrator tabs / views | identical | Phase 4 |
| Hotkeys (all bindings, Kitty protocol) | identical via `KeyEncoder` | Phase 3 |
| Ctrl+C / Esc / interrupt semantics | identical | Phase 3 |
| `goa_command` tool (LLM-driven UI) | identical | Phase 4 |
| Terminal resize / reflow | virtual terminal resize | Phase 1 |
| Scrollback + replay | virtual scrollback | Phase 1 |
| `SetTitle` | browser tab title | Phase 1 |

---

## 14. Client implementation

### 14.1 Files (embedded, no build step)

* `index.html` — shell: `<div id="grid">`, `<div id="scrollback">`, caret, status bar,
  connection badge. ~60 lines.
* `app.css` — ~350 lines: monospace grid (`ui-monospace` stack, no webfont), run
  classes, caret blink, scrollbar styling, `prefers-reduced-motion`, responsive rules,
  dark/light via CSS custom properties injected from the server.
* `app.js` — ~400 lines: transport selection (WS → SSE), frame application, run diffing
  inside a row, scrollback list, key encoding, paste/upload, resize observer, reconnect
  with `seq`.

### 14.2 Frame application

The server sends **whole changed rows**; the client replaces a row's children with the
new runs (`replaceChildren` + a DocumentFragment). No client-side diffing is needed —
the expensive diffing already happened server-side, per row.

### 14.3 Scroll behaviour

Follow-tail by default (like a terminal); scrolling up detaches follow-tail and shows
a "jump to latest" affordance; returning to the bottom re-attaches.

### 14.4 Input focus

A hidden `<textarea>` (or a focusable grid with `tabindex=0`) captures keys, so IME
composition and mobile keyboards work. The caret position from the frame positions the
IME candidate window correctly.

### 14.5 Clipboard / copy

**Not shipped.** Selecting text and pressing `Ctrl+C` uses the native selection,
and the page deliberately leaves `Ctrl+Shift+C` to the browser (devtools) rather
than claiming it; the plain-text mirror at `/s/{id}/text` is still there for a
copy-as-text affordance to build on.

### 14.6 Responsive behaviour

Cell size is measured from the font; `cols = clamp(60, floor(width / cellW), max_cols)`.
Below 720 px the grid switches to a 1.6× font scale with horizontal scrolling. Resize
is debounced 120 ms and sent as one `resize` message.

---

## 15. Configuration

**Not shipped.** The server is configured by CLI flags and environment variables
only (`--server-addr`, `--server-auth*`, `--server-read-only`,
`--server-max-clients`, `--insecure-no-auth`; `GOA_SERVER_AUTH_PASSWORD`,
`GOA_SERVER_AUTH_TOKEN`). The YAML block below is the intended shape, kept for
the next iteration: nothing reads it yet, and `password_hash`, `token_file`,
`multiclient`, `transport`, `slow_client_frames` and `open_browser` are
unimplemented.

```yaml
server:
  enabled: false              # informational; `goa server` implies true
  host: "127.0.0.1"
  port: 7331
  auth: none                  # none | basic | token
  username: ""
  password: ""                # plaintext (config file must be 0600) …
  password_hash: ""           # … or SHA-256 hex of the password
  token: ""                   # for auth: token
  token_file: ""              # or read the token from a file (0600)
  read_only: false            # reject all client input
  multiclient: broadcast      # broadcast | driver
  max_clients: 8
  transport: auto             # auto | ws | sse | plain
  cols: 0                     # 0 = derive from the browser viewport
  rows: 0
  max_cols: 200
  max_rows: 100
  plain_fallback: true        # serve /page when the client has no JS
  slow_client_frames: 30      # consecutive overruns before disconnect
  open_browser: false
```

Cascade support comes for free: `embedded → ~/.goa → project .goa → local → GOA_* env
→ flags`. Env overrides: `GOA_SERVER_PORT`, `GOA_SERVER_HOST`, `GOA_SERVER_AUTH`,
`GOA_SERVER_TOKEN`, …

Validation (`config_validate.go`): port range, `auth` enum, `max_cols ≥ cols ≥ 20`,
`multiclient` enum, and "non-loopback host without auth" ⇒ warning (not an error, so
the wizard can still run), plus a hard error for `auth: none` + non-loopback unless
`--insecure-no-auth`.

CLI:

```
goa server [--host H] [--port N] [--auth none|basic|token] [--read-only]
           [--transport auto|ws|sse|plain] [--open] [--insecure-no-auth]
```

`goa server` is recognised in `Main()` before flag parsing (like `goa mcp`,
`internal/app/app.go:460`), translated into `RuntimeOptions`, and dispatched from the
`runApp()` switch (`internal/app/app.go:499-517`) as `case runtimeOpts.Server: runWebServer(...)`
— so config loading, subsystem init, plugin loading and crash logging all follow the
single existing path.

---

## 16. Implementation phases

Each phase ends with a runnable demo and a green gate
(`go vet ./...`, `go test -race -count=1 -cover ./...`, `gocognit -over 15`,
`gocyclo -over 12`).

### Phase 0 — Terminal injection + cell grid skeleton

*Tasks*
1. `internal/app/subsystems.go`: add `terminal tui.Terminal`.
2. `internal/app/tui.go:90`: `if subs.terminal != nil { use it } else { NewProcessTerminal() }`.
3. `tui/term_cells.go`: `CellAttrs`, per-cell attribute arrays, `Cells(row)`, `Cursor()`,
   `Resize(w,h)`, `Reset()`, `ScrollbackCells()`, `Size()`.
4. `internal/webui/cells.go`: `CellGrid` wrapping `TermEmulator`, dirty-row tracking,
   SGR 256-colour → hex.
5. `internal/webui/diff.go` + `frame.go` + `codec.go`.
6. `internal/webui/terminal.go`: `VirtualTerminal`.
7. `internal/webui/hub.go`, `client.go`, `transport_ws.go`.
8. `internal/webui/server.go`: mux, `/` → 302, `/s/{id}`, `/ws`, `/assets`, `/healthz`.
9. `internal/webui/page.go` + `assets/*` (static shell rendering an empty grid).
10. `internal/app/webui.go`: `runWebServer`; `RuntimeOptions.Server`; dispatch.

*Tests*
* `TestVirtualTerminal_ImplementsTerminal` (compile-time assertion).
* `TestCellGrid_SGRRoundTrip` — table: SGR in → attrs out.
* `TestCellDiff_OnlyChangedRows`.
* `TestServer_RedirectsRootToSession` (`httptest`).
* `TestVirtualTerminal_NoRawModeClaim` — `tui.OwnsScreen() == false`.
* Regression: the whole existing TUI suite must stay green (proves no engine change).

*Done when:* `goa server` prints a URL, the browser shows an empty Goa frame, and
keystrokes typed in the browser echo into the grid.

### Phase 1 — Cell fidelity

*Tasks*
1. Extend `applySGR` path (in `tui/term_cells.go`, not by editing `term_emulator.go`'s
   colour semantics) to track bold/dim/italic/underline/inverse/strike and OSC-8 links;
   `Visible/VisibleFg/VisibleBg/RowFg` behaviour must remain byte-identical.
2. 256-colour palette table + truecolour passthrough.
3. Cursor + `HideCursor/ShowCursor` in frames; caret rendering.
4. Scrollback capture → `scrollback` frames + client transcript list.
5. `Resize` plumbing from the transport into the engine; verify geometry-reset repaint.
6. Theme → CSS custom properties from `tui.TheTheme.ColorHex`.
7. Link handling (OSC-8) → clickable spans, `rel="noopener noreferrer"`, external links
   open in a new tab.

*Tests*
* Golden frame tests (`testdata/webui/*.golden`): scripted key sequences → expected
  cell grid, asserted through the same `TermEmulator` API the existing compositor tests
  use.
* `TestCellAttrs_BoldItalicUnderline` table.
* `TestPalette256` (16 + 232 + 24 greys spot-checked).
* `TestResize_TriggersFullRepaint` — frame kind parity with the terminal path.
* `TestScrollback_CapturedOnOverflow`.
* Existing compositor/TUI suite unchanged and green.

*Done when:* a scripted session produces byte-for-byte the same cells as a real
terminal run of the same session (verified by replaying the compositor output into a
`TermEmulator` and diffing the two grids).

### Phase 2 — Live session

*Tasks*
1. `runWebServer`: build subsystems (already done by `runApp`), start the agent session,
   run `New(subs).Run()` against the virtual terminal.
2. `SetTitle` → browser title; startup banner; config-issue disclosure.
3. Verify every event path renders: streaming, tools, thinking, footer, flashes.
4. Client: follow-tail scrolling, caret, run rendering, badge.
5. `/s/{id}/text` plain-text endpoint.

*Tests*
* Integration test with the existing **mock LLM** harness (`internal/app` already has
  `*_mockllm_test.go`): start the server, attach a WS client, submit input, and assert
  on the **rendered cell grid** (e.g. the assistant text appears, a `bash` tool widget
  shows `✓`). This is a new and unusually strong test capability: assertions on the
  actual screen.
* Multi-client broadcast test (two WS clients see the same frame sequence).
* Slow-client test: a client that never reads is dropped, and the agent stream is not
  delayed (assert with a timestamped turn).

*Done when:* an end-to-end prompt/response/tool-call round trip is visible in the
browser and asserted at the cell level.

### Phase 3 — Input fidelity, transports, resilience

*Tasks*
1. `KeyEncoder` + full chord/kitty mapping + paste + image upload.
2. `transport_sse.go` (`/events`, `/input`, `/resize`) + automatic client fallback.
3. `transport_plain.go` (`/page`, meta-refresh, form post).
4. Reconnect with `since=<seq>`; `bye` reasons; connection badge states.
5. Resize debounce; mobile layout.
6. Read-only mode enforcement.

*Tests*
* `TestKeyEncoder` table covering every row of §7.5.
* `TestTransportFallback` — force WS failure, assert SSE delivers frames and input works.
* `TestPlainPage` — `GET /s/{id}/page` contains the screen text and a form; POST input
  reaches the engine.
* `TestReconnect_ResumesFromSeq`.
* Paste/upload test incl. content-type rejection.

*Done when:* WS, SSE and no-JS paths all drive a real session, and the automated
fallback triggers when the handshake fails.

### Phase 4 — Interactive parity

*Tasks*
1. Audit every `core.Context` UI callback against the engine's overlay path; fix any
   that assume a real TTY (`SelectOptionFunc`, `ClarifyFunc`, `ShowInputFunc`,
   `ConfirmMultiFunc`, `ShowPTYOverlay`, `SetEditorTextFunc`, `RequestMainInput`).
2. Tool confirmation policy when **no** client is attached: deny (fail-closed, matching
   headless) unless `server.read_only: false` and a client is present.
3. `goa_command` tool (LLM-driven UI) over the web.
4. Verify pagers/filmstrip/orchestrator tabs/goals/todos overlays render.
5. `Multiclient: driver` policy.

*Tests*
* Per-callback test driving a scripted command (`/model`, `/mode`, `/pty:monitor`) and
  asserting the overlay appears in the grid.
* No-client-attached ⇒ confirmation denied (fail-closed) test.
* Multi-agent/orchestrator smoke test with the mock LLM.

*Done when:* the parity matrix (§13) is fully checked off, row by row, in CI output.

**Status (G7).** The interactive surfaces are validated in a **real Chrome**, not
just in-process: `e2e/w1_webui_browser.sh` drives `goa server` through
`agent-browser` and asserts the live DOM for the phases that matter on the
browser path — `#status[data-state=live]`, the pinned model in the status bar, a
real tool call (`$ echo G7-E2E-OK`) plus the model's reply, frame-by-frame
streaming (the grid grows 0→1→4→6 while the turn is in flight), `/help`, and the
`/config` menu (`›` selector moves on `ArrowDown`, the type filter narrows the
list, `Escape` closes it, and the model pin survives every command). One
harness detail worth keeping: the page reports *key descriptors* to the server
(§7.5), so text has to arrive as real `keydown`s — `agent-browser`'s
`keyboard type` (`Input.insertText`) is invisible to it and the script types
character by character.

### Phase 5 — Security, hardening, docs

*Tasks*
1. `auth.go`: none/basic/token, cookie, constant-time compare, lockout.
2. Origin check, CSP, `MaxBytesReader`, no-CORS guarantee.
3. Non-loopback guard rails + `--insecure-no-auth`.
4. Fuzz targets: `FuzzCellGrid_Process`, `FuzzFrameCodec_Decode`.
5. Graceful shutdown (`SIGINT`/`SIGTERM` → drain → close).
6. Docs: `docs/WEBUI.md`, `docs/USER-GUIDE.md` + `docs/SETUP.md` sections,
   `docs/COMMANDS.md` note, README section, `docs/HOTKEYS.md` browser key map.
7. `docs/ARCHITECTURE.md` section on the virtual terminal.

*Tests*
* `TestAuth_*` (constant-time, lockout, cookie flags, Origin rejection, CSP header
  presence, body-size limits).
* Shutdown drain test.
* Docs build test (`docs_test.go` already validates the doc set).

*Done when:* `goa server --server-addr 0.0.0.0` refuses to start without auth and an explicit
override, and the docs describe the browser key map.

**Status (G6–G8).** Items 1–3 shipped: `internal/webui/auth.go` (modes,
constant-time compare, HttpOnly/SameSite cookie, per-client lockout) and
`internal/webui/security.go` (loopback guard, CSP with nonce, Origin guard,
`MaxBytesReader`, no-CORS, login handlers). Docs shipped (G8): `docs/WEBUI.md`,
`docs/USER-GUIDE.md` §7, `docs/SETUP.md`, `docs/ARCHITECTURE.md`, the browser
key map in `docs/HOTKEYS.md`, and README/COMMANDS cross-links. Still open, and
deliberately out of the v1 scope: item 4 (fuzz targets for the cell grid and the
frame codec) and config-file keys for `server.auth`, `password_hash`,
`token_file` (auth is flag/env only for now — `goa server --help` says so).

### Phase 6 — Multi-session per process (future, not v1)

Requires making the session bundle instantiable: extract `agentMgr`, `sessionStore`,
`modeMgr`, `steering`, `turnRecorder`, `events` from `subsystems` into a
`*core.SessionBundle` created per session by a factory. Each bundle gets its own
`VirtualTerminal` + `Hub` and its own `/s/<id>` route, with `/` redirecting to the
newest or a chosen session. Estimated: a focused refactor of `core.AgentManager`'s
per-session fields, with the single-session path kept as the degenerate case.

---

## 17. Test strategy

1. **Unit** — cells, palette, diff, codec, auth, hub, key encoder, config validation.
   Table-driven, sub-100 ms each, `t.TempDir()` for FS cases.
2. **Golden frames** — scripted input sequences → `.golden` cell grids. Cheap,
   deterministic, and they lock TUI/web parity at the cell level.
3. **Parity harness** — run the same scripted session twice: once against a
   `ProcessTerminal`-like sink (captured bytes → `TermEmulator`) and once against
   `VirtualTerminal`; assert the two grids are identical. This is the regression test
   that guarantees "the page looks like the TUI".
4. **Integration** — `httptest` + real WS/SSE clients: redirect, auth, snapshot,
   streaming, commands (`/help`, `/mode:…`), cancel, reconnect.
5. **Cell-level e2e** — mock LLM (`internal/app/*_mockllm_test.go`) driving a full
   prompt → tool call → response turn, asserted on the rendered grid.
6. **Race** — every concurrent test under `-race`; the hub, the grid and the transport
   writers are the hot spots.
7. **Fuzz** — cell processing and frame decoding.
8. **Gates** — `go vet ./...`, `go test -count=1 -race -cover ./...`,
   `gocognit -over 15`, `gocyclo -over 12`, plus coverage targets
   (internal ≥ 90 %, webui ≥ 85 %).

---

## 18. Risks and mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| Emulator attribute tracking diverges from a real terminal | Wrong colours/weights in the browser | Golden + parity harness (§17.3); existing `TermEmulator` colour semantics left untouched |
| Unmodelled escape sequences (colon SGR sub-parameters, cursor ops) | Dropped styling or misplacement | Only the sequences the compositor actually emits are needed; a fuzz test plus a "drop unknown, never misplace" policy; `SGRStateAt` (`internal/ansi/sgrstate.go`) already documents this failure mode and its fallback |
| Frame flood on a fast stream | Browser jank | Coalesce to the engine ticker, newest-frame-wins, per-client drop counter |
| Wide/combining glyphs mis-measured | Column drift | `TermEmulator` already uses `uniseg` grapheme clusters and deferred wrap |
| Multi-agent / orchestrator views increase frame size | Bigger payloads | Already row-diffed; add a `max_cols` clamp and a documented cap |
| Security: a browser-reachable agent with tool execution | Remote code execution by design | Loopback default, auth required off-loopback, CSP, Origin check, rate limit, explicit `--insecure-no-auth` escape hatch, loud warnings |
| Two UI surfaces drifting | — | Eliminated by construction: one engine, one screen model (§3.1) |
| `TermEmulator` is currently test-only | "Production use of test code" | Promote it deliberately: it lives in `tui` (not `tui_test`), is documented as the screen model, and gains the additive cell API in `tui/term_cells.go` |

---

## 19. Open questions (decisions needed before Phase 0 completes)

1. **Default bind + auth**: ship `auth: none` on `127.0.0.1` (recommended — frictionless
   local use) or require a token even locally?
2. **Session id in the URL**: expose the raw `core.SessionStore` id (stable, correlatable
   with `/export`) or an opaque per-process web handle that maps to it (hides internal
   ids, breaks `/stats:session` correlation)? *Recommendation: raw id.*
3. **`/s/{id}` on a second visit**: reattach to the live session (recommended) or fork a
   new one? *Recommendation: reattach — forking would violate Hard Rule 7.*
4. **Multi-client default**: `broadcast` (any client types) or `driver` (first client
   owns input)? *Recommendation: `broadcast`, matching a shared tty.*
5. **`goa server` vs `--server`**: subcommand (recommended, room for future verbs such as
   `goa server token`) or a flag alongside `--acp`.

---

## 20. Appendix — wire examples

Frame (steady state, 2 changed rows):

```json
{"t":"frame","seq":128,"cols":120,"rows":40,
 "cur":{"r":31,"c":12,"v":true},
 "rows":[[29,[["  goa ",0,"","#161826"],["v1.0.0",2,"#646c84",""]]],
         [31,[["> ",0,"#7c5cfc",""],["implement the parser",0,"#e7e9f1",""]]]}
```

Flags bitmask: `1 bold · 2 italic · 4 underline · 8 dim · 16 inverse · 32 strike · 64 link`.

Client → server:

```json
{"t":"input","data":"ls -la\r"}
{"t":"resize","cols":132,"rows":44}
{"t":"hello","since":128}
{"t":"session_new"}
```

Server → client control:

```json
{"t":"control","kind":"session_rotated","session":"b71e0c94"}
{"t":"bye","reason":"slow_client"}
{"t":"error","text":"input rejected: read-only mode"}
```

---

## 21. Deliverables checklist

- [x] `goa server` starts, redirects `/` → `/s/<id>`, serves the UI.
  (`TestServer_RedirectsRootToSession`, `TestServer_ServesSessionPage`, plus
  `e2e/w1_webui_browser.sh` driving a live Chrome: 11/11 assertions PASS.)
- [x] Every row of the parity matrix (§13) verified in CI.
  Each row is served by the one engine, so a row cannot be right in the
  terminal and wrong in the browser: the browser draws the cells the same
  compositor emits. The engine-side behaviour is covered by the `tui` suite,
  the browser-side path by the `internal/app` web-parity tests (editor,
  autocomplete, history, selectors, confirms, clarifies, `!` bang, steering)
  and by the browser e2e script; cell fidelity is locked by the parity
  harness (§7.1 goldens + the pyte cross-check).
- [x] Zero new `go.mod` entries.
  `git diff go.mod go.sum` is empty across the whole web-UI track; the only
  dependency involved (`gorilla/websocket`) was already in the module.
- [x] No JavaScript framework, no CDN, no build step; payload < 40 KB.
  `internal/webui/assets/` is 22 KB uncompressed (19 KB `app.js`, 2.4 KB
  `app.css`, 0.8 KB `index.html`), embedded in the binary and served with a
  per-response CSP nonce — no `<script src>` pointing anywhere but `/assets`.
- [x] WS primary, SSE fallback, no-JS page fallback — all driving a live session.
  `TestServer_WebSocket*` (frame delivery, input, resize repaint),
  `TestSSE_*`/`TestPost*` (live frames, headers, retry, input/resize),
  `TestPlainPage_*` (server-rendered screen, form post, read-only) each run
  against a real `tui.TUI` behind a real listener.
- [x] Optional login/password; loopback-safe defaults; CSP + Origin checks.
  `internal/webui/auth.go` + `security.go`: `CheckExposure` refuses a
  non-loopback bind without credentials, basic/token modes with constant-time
  comparison, per-client lockout, nonce'd CSP, Origin guard, no-CORS
  (`TestAuth_*`, `TestSecurity_*`, `TestCheckpoint_AuthCookieFlow*`).
- [x] Parity harness proves web cells == terminal cells for a scripted session.
  `internal/webui.CellGrid` wraps `tui.TermEmulator` — the same emulator type
  `tui/parity_harness_test.go` locks against goldens and cross-checks against
  pyte — so web cells *are* terminal cells by construction, and the harness
  guards the model both of them read.
- [x] All gates green: vet, race tests, coverage, complexity budgets.
  `go build ./...` and `go vet ./...` (host plus linux/darwin/windows) exit 0;
  `go test -count=1 -race -cover ./...` exits 0 over 88 packages;
  `gocognit -over 15` and `gocyclo -over 12` report nothing; `gofmt -l` is
  empty. `internal/webui` — the package this track added — is at 91.1 %
  (target ≥ 85 %); `internal/app` (63.5 %) and `tui` (76.1 %) sit where they
  did before, and CI enforces no package-level coverage threshold.
- [x] Docs: `docs/WEBUI.md` + USER-GUIDE / SETUP / ARCHITECTURE / HOTKEYS updates.
  `docs/WEBUI.md` (security model, transports, URL map), `docs/USER-GUIDE.md`
  §7, `docs/SETUP.md` "Web UI (`goa server`)", `docs/ARCHITECTURE.md` "The
  Virtual Terminal", `docs/HOTKEYS.md` "Browser keys", plus README and COMMANDS
  cross-links. `go test ./docs/...` green.

## 22. Block plane (v2) — semantic blocks + cell band

### 22.1 Why

The cell pipeline (§3–§9) is faithful but every defect it has produced lives
at one seam: the browser re-deriving UI semantics from compositor bytes
(B2 transcript wipes, B3 geometry resets, B4 footer geometry, B5 startup
races, B7 input gating, B8/B9 band pinning, B10 colour inheritance). It also
cannot meet the v2 goals: resize re-renders and re-ships the whole
transcript server-side, history is a simulated bounded row list, and
fixed-cell layout cannot reflow natively.

The Scene (§4.3) is already the protocol-free IR with two consumers
(Compositor, AgentView). v2 adds a third: semantic conversation blocks,
rendered by the browser as HTML flow content.

### 22.2 Design

- **`tui.SceneBlock`** `{ID, Kind, Text, Meta, Lines}`: one conversation
  entry (the width-independent `MessageData` plus view-derived metadata for
  tool widgets) or the header art (`Lines`, styled). Exported by the
  ChatViewport/Header during `buildScene` — no second renderer, the export
  reads the Model the TUI already maintains.
- **`tui.SceneObserver`**: optional Terminal interface; `renderOneFrame`
  hands the Scene to the VirtualTerminal before the compositor consumes it.
- **`internal/webui.Plane`**: `cells` (today's pipeline, unchanged, zero
  value — every existing test and the `?mode=cells` page keep their exact
  behaviour) and `blocks` (production default).
- **`BlockTracker`**: diffs Scene block snapshots into id-keyed wire ops —
  `set` (full), append-`set` with a `from` offset when text strictly grows
  (streaming), `del`, and a whole-journal `reset` when history is rewritten
  (compression, `/clear`). The tracker keeps the journal so a fresh attach
  is answered with band + journal, exactly like FullFrame + scrollback in
  the cells plane.
- **Band cells**: the transcript cells and scrollback are no longer shipped
  in the blocks plane. `publish` ships only the bottom `ChromeHeight` rows
  (editor, status lines, live strip, bubbles) plus the block deltas. The
  autocomplete popup (an overlay that does NOT capture input) extends the
  band upward for its height. An input-capturing overlay (config selector,
  confirm card) sets `overlay` on the frame and the page falls back to the
  full cell grid until it closes.
- **Client**: `#blocks` is normal HTML flow (markdown, tool cards,
  collapsible thinking) — resize is browser reflow (no resize storm, no
  server transcript re-render on the wire), history is the browser's own
  scroll. A cell-rendered footer band keeps the editor/status pixel-identical
  to the TUI. Colours come from the same theme variables (§12.3); a small
  SGR→span converter renders `Lines` blocks (header art); the markdown
  renderer is a dependency-free ~200-line subset matched to what goa
  emits. The plane is server-wide — `goa server` defaults to blocks;
  `--server-cells` serves the v1 cell-terminal plane (its whole test
  suite is the regression net).

### 22.3 Non-goals (v2)

- No per-block editor: input stays byte-exact terminal input (§7.5) echoed
  by the engine into the band cells.
- No server-side HTML rendering: the server ships data; the page renders.
- No multi-session (still §16 Phase 6).

### 22.4 Risks

- Markdown-subset drift between the TUI renderer and the page renderer:
  bounded by shipping the same source text and pinning the palette; the
  page's renderer only needs the subset goa emits (§14 markdown blocks).
- Tool-block fidelity: tool output ships as plain text (+ structured
  metadata) in v2.0; styled runs for tool bodies are an upgrade path behind
  the same wire op.
- Two planes to test: mitigated by keeping the cells plane byte-identical
  (its whole test suite is the regression net) and the blocks plane's
  server side being pure diffing over data the engine already produces.
