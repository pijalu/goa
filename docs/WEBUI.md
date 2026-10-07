<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Goa Web UI (`goa server`)

`goa server` runs the ordinary interactive Goa session against a virtual
terminal instead of a TTY, and serves the resulting screen to browsers over
HTTP. The conversation is streamed as semantic blocks the page renders as
HTML — scrolling, resize and history are the browser's own — while the input
line and status band stay live terminal cells, so typing behaves exactly like
the TUI and keys reach the agent byte-for-byte as they do in a real terminal.

```bash
goa server                                   # loopback only, no credentials
goa server --server-auth=token                # reachable beyond this machine
goa server --server-addr 0.0.0.0:7331 --server-auth=token
goa server --server-read-only                 # screen sharing: viewers only
goa server --server-cells                     # legacy: the whole screen as terminal cells
```

---

## 1. Security model

The web UI is a remote control: keystrokes reach a live session, uploads write
files, and the screen shows whatever the agent is doing. Three rules follow.

### 1.1 No auth means loopback

`--server-addr` defaults to `127.0.0.1:8080`. Binding anything else — `0.0.0.0`,
a LAN address — **without** credentials is refused at startup:

```
$ goa server --server-addr 0.0.0.0:7331
Error: refusing to serve 0.0.0.0:7331 without authentication: set --server-auth
(basic|token), or pass --insecure-no-auth to accept that anyone who can reach
this address can drive the agent
```

The unsafe choice is the one that has to be typed: `--insecure-no-auth` is an
explicit, self-describing acceptance, and nothing else implies it.

### 1.2 Auth modes

| Mode | Flag | Credential |
|------|------|------------|
| `none` | `--server-auth=none` (default) | — loopback only |
| `basic` | `--server-auth=basic` | `--server-auth-user` + `GOA_SERVER_AUTH_PASSWORD` |
| `token` | `--server-auth=token` | `GOA_SERVER_AUTH_TOKEN` |

Secrets prefer the environment variable over the flag: a command line is
readable by every other process on the machine and lands in shell history.

In `token` mode a browser exchanges the token once at `/login` for an
`HttpOnly; SameSite=Strict` cookie (`Secure` whenever the request itself is
TLS). The page never holds the secret again. Scripts can skip the form and send
`Authorization: Bearer <token>`. A token in the query string is **not**
accepted — query strings end up in logs and in the `Referer` header.

Credentials are compared with `crypto/subtle.ConstantTimeCompare` over SHA-256
digests, so neither the length of the secret nor the position of the first
differing byte leaks through timing.

### 1.3 Brute force costs time

Failed credentials are counted per client address (five failures by default).
The attempt that trips the limit gets `429` with `Retry-After`; while the window
is open even the *correct* token is refused, because the lockout belongs to the
client, not to the guess. A successful login clears the count, so a typo is
never one mistake away from a lockout, and one noisy client cannot lock out the
rest of the machine.

### 1.4 Cross-origin writes are refused

Every unsafe method (POST) carrying an `Origin` header that does not match the
`Host` is answered `403` before any handler runs. A request without an `Origin`
is a native client (curl, an SSH tunnel, a test), not a browser that another
page can steer, so it passes. The same rule is the WebSocket upgrade's
`CheckOrigin`.

### 1.5 Response hardening

* **CSP** — `default-src 'none'; script-src 'self' 'nonce-…'; style-src 'self'
  'nonce-…'; img-src 'self' data:; connect-src 'self'; base-uri 'none';
  form-action 'self'; frame-ancestors 'none'; object-src 'none'`. No
  `'unsafe-inline'`, no CDN: the page's own theme block and bootstrap line carry
  a fresh per-response nonce from `crypto/rand`, so an injected `<script>` has
  nothing to borrow.
* **No CORS** — no response ever carries an `access-control-*` header; the
  middleware strips any that a future handler might add.
* **Body limits** — `http.MaxBytesReader` caps every request body
  (16 MiB, the upload ceiling); the upload handler additionally sniffs the
  content so the on-disk extension always comes from the bytes.
* **Sniffing/framing** — `X-Content-Type-Options: nosniff`, `X-Frame-Options:
  DENY`, `Referrer-Policy: no-referrer`, `Cross-Origin-Opener-Policy:
  same-origin`, `Cross-Origin-Resource-Policy: same-origin`.
* **Redirect hygiene** — the login form's `next` target accepts local absolute
  paths only, so it cannot become an open redirect.

---

## 2. Transports

All three speak the same frame codec, so the browser degrades without a second
rendering code path:

1. **WebSocket** (`/ws`) — primary; output frames and key events, with resume
   by sequence number (`hello {since}`) so a reconnect never paints a delta
   across a hole.
2. **SSE + POST** (`/events`, `/input`, `/key`, `/resize`, `/upload`) — used
   automatically when the socket cannot be established (proxy, extension,
   downgrade). Detected by a handshake watchdog; a live drop retries the socket.
3. **No-JS page** (`/s/<id>/page`, or `?mode=plain`) — a server-rendered form
   with `<meta refresh>` polling, for a browser with scripting disabled.

`--server-read-only` makes every attached browser a viewer: keystrokes and
uploads are refused with an explicit notice rather than silently ignored.

---

## 3. URLs

| Path | Purpose |
|------|---------|
| `/` | redirect to the current session view |
| `/s/<id>` | the live page (JS, SSE or plain) |
| `/s/<id>/text` | plain-text mirror of the screen (curl, screen readers) |
| `/login` | token exchange (token mode only) |
| `/assets/…` | embedded page assets, immutable cache |
| `/healthz` | JSON status (session id, attached browsers) |
| `/ws`, `/events` | live transports |

---

## 4. Operational notes

* `gzip` is negotiated for text responses; the event stream is deliberately
  **not** compressed (a compressor would buffer the flushes SSE depends on),
  and the WebSocket negotiates `permessage-deflate` per connection instead.
* The default bind address is also the default printed in the URL; the CLI
  prints the resolved address after the listener is up.
* Without a session id yet (before the first agent message), the page is still
  reachable at `/s/` and the plain-text mirror at `/s/text`.

---

## 5. Attaching a terminal (`goa attach`)

The browser is the terminal — so a real terminal can be the browser.
`goa attach` connects to a running `goa server` over the same WebSocket,
renders the session's cell frames locally (truecolor SGR, OSC-8 links, the
server's cursor as the local caret), and forwards your keyboard verbatim:
the bytes your terminal produces are exactly what a local session would
read, so editing, chords and paste behave identically.

```bash
goa attach --server 127.0.0.1:8080                 # attach to the live session
goa attach --server 127.0.0.1:8080 --session <id>  # a specific session
goa attach --server 127.0.0.1:8080 --path ~/repos/foo   # multi-project servers
goa attach --server host:7331 --server-auth-token $T
```

What to expect:

* **Native scrollback** — rows the session scrolls off are pushed into the
  terminal's own scrollback (transcript replacements wipe it first, exactly
  like the page's transcript).
* **Reconnect** — a dropped socket is re-attached with backoff; keystrokes
  typed while offline are held (bounded) and replayed on reconnect.
* **Detach, don't stop** — `Ctrl+]` detaches: the session keeps running on
  the server (type `/quit` inside the session to end it, or stop the server
  from its console). A server-side goodbye ("bye") is reported and attach
  exits; a refused handshake (bad token, unknown session) is an error, not
  a retry.
* **Plane** — attach requests the `cells` plane (whole screen as cells).
  Per-client planes are a session property: a browser on the blocks plane
  and an attached terminal on the cells plane can share one session.

## 6. Multi-project serving (`--server-projects-root`)

One `goa server` can front many projects:

```bash
goa server --server-projects-root ~/repos --server-addr 127.0.0.1:8080
```

The supervisor process hosts no session of its own. Each project directory
gets its own child `goa server` — an ordinary single-project session with
that project's config, plugins and skills — bound to a private Unix socket
inside a `0700` directory and speaking a per-child random bearer token.
Requests map to children by session id (`/s/<id>`, `/ws?s=<id>`), so a
browser page and an attached terminal share one child exactly as they
would share a standalone server.

Sessions are opened on demand:

* **Browser** — `http://host:8080/` is a session index with an
  "open project directory" form; opening `/some/path` redirects to its
  session page.
* **Terminal** — `goa attach --server host:8080 --path ~/repos/foo`
  resolves the path through the same handshake before attaching.

Boundaries and lifecycle:

* **Path policy** — a session's directory must be absolute, exist, be a
  directory, and resolve (symlinks included) under the projects root.
  Everything else is refused before a child is spawned. The parent's
  exposure rule is unchanged: no auth means loopback only.
* **Caps and reaping** — `--server-max-sessions` bounds live children
  (default 8); a session no client has touched for `--server-session-idle`
  (default 30m, negative = keep until shutdown) is stopped, its transcript
  remaining on disk in the project, resumable like any session.
* **Rotation** — a `/new` inside a child rotates its session id; the
  supervisor follows the rotation, and a client presenting the old id is
  still routed to the child that owned it.

## 7. See also

* `specs/webui.md` — the full design specification (architecture, phase plan).
* `docs/ARCHITECTURE.md` — the virtual terminal as a `tui.Terminal`.
* `docs/HOTKEYS.md` — the keys the browser sends are the terminal's keys.