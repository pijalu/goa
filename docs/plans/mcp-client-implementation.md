# MCP Client Implementation Plan for Goa

> **Goal:** Goa becomes a full MCP (Model Context Protocol) **client**. Users connect Goa
> to external MCP services (local stdio servers and remote HTTP/SSE servers) and their
> tools become available to the agent — **dependency-free (Option B)**, hand-rolled on top
> of Goa's existing stdio client.
>
> Reference: OpenCode's `packages/opencode/src/mcp/` (`index.ts`, `catalog.ts`, `auth.ts`,
> `oauth-provider.ts`, `oauth-callback.ts`) and its config schema
> (`packages/core/src/v1/config/mcp.ts`).
>
> Mode: **integrate** — read the contract, validate schemas, handle timeouts/retries/
> pagination/partial responses, test error paths.

---

## 0. Guiding constraints (Goa hard rules + posture)

- **Dependency-free.** No `mcp-go` / official SDK. Extend the existing
  `internal/mcp/client` package with new hand-rolled transports. (Existing
  `StdioClient` is solid and keeps its tests.)
- **A huge correct implementation beats a small incorrect one.** Do the full design
  (pagination, notifications, reconnection, OAuth in a later phase) — but sequence it.
- **Every fix/feature ships a test** that would have caught the bug; `<100ms` per unit
  test, `<5s` per package, `go test -count=1 -race -cover ./...`.
- **Complexity budget:** config parsing ≤ 20 gocognit / 12 gocyclo; other logic ≤ 15/12.
- **SOLID:** small composable primitives + factories; one responsibility per type.
  Transports behind the existing `client.Client` interface; a `Transport` interface for
  the wire; the `Manager` orchestrates, never parses wire frames.
- **Tool errors** use `internal.ToolError`. Prompts/markdown via `//go:embed`.
  Commands self-register via `init()` in `core/commands/`.

---

## 1. Target architecture

```
┌────────────────────────────────────────────────────────────────────┐
│ config.Config.MCP  (YAML cascade: embedded→home→project→local→env) │
│   map[string]MCPServerConfig  {type, command|url, env, headers,    │
│                                 cwd, enabled, timeout, oauth}       │
└───────────────┬────────────────────────────────────────────────────┘
                │ load + validate + merge
                ▼
┌────────────────────────────────────────────────────────────────────┐
│ internal/mcp.Manager  (owns lifecycle, status, tool registration)  │
│   - Connect(ctx, cfg) / Disconnect(name) / Status() / Call()       │
│   - per-server: reconnect backoff, tools/list_changed re-register  │
│   - registers group "mcp__<server>__*" into tools.ToolRegistry     │
└───────┬───────────────────────────────┬────────────────────────────┘
        │ factory                       │ factory
        ▼                               ▼
┌──────────────────┐          ┌──────────────────────────────────────┐
│ client.StdioClient│          │ client.HTTPClient (NEW)              │
│ (existing)        │          │  - Streamable HTTP POST (primary)    │
│ command+args+env  │          │  - SSE fallback (legacy transport)   │
│ cwd, timeout      │          │  - headers, OAuth bearer (phase 3)   │
└───────┬──────────┘          └───────┬──────────────────────────────┘
        │ JSON-RPC/ndjson             │ JSON-RPC over HTTP POST + SSE stream
        ▼                             ▼
        └──────────►  client.Transport (NEW interface)  ◄──────────┘
                     Send(req) / Recv() stream / Close()

        ┌─────────────────────────────────────────────────────────┐
        │ internal/mcp.catalog (NEW) — shared protocol helpers     │
        │  paginate(listTools cursors) · sanitize(name) ·          │
        │  toolName(server,tool) · convert content→text ·          │
        │  tolerant tools/list (outputSchema validation fallback)  │
        └─────────────────────────────────────────────────────────┘
```

**Key design decisions**

1. **Keep `client.Client` as the single abstraction.** `Manager` only ever talks to
   `client.Client`. Stdio and HTTP are two constructors returning the same interface.
2. **Extract a `Transport` interface** so the request/response/correlation logic
   (currently private to `StdioClient`) is reused by HTTP without copy-paste. Stdio is
   refactored to sit on `Transport` too (behavior-preserving refactor, proven by its
   existing tests staying green).
3. **New `catalog` subpackage** holds protocol-level helpers shared by transports and the
   manager: pagination, name sanitization, content→text conversion, tolerant decoding.
   This keeps `Manager` and the transports free of wire-schema minutiae (SRP).
4. **Tool naming stays `mcp__<server>__<tool>`** (Goa's existing, collision-safe
   double-underscore scheme) but each segment is **sanitized** to `[a-zA-Z0-9_-]`.
   OpenCode uses `server_tool`; Goa's existing scheme is already registered/tested and is
   unambiguous — keep it.
5. **`mcpTool` implements `agentic.ContextTool`** (`ExecuteContext`) so the agent's turn
   context (Stop / Ctrl+C) cancels in-flight MCP calls. Today it only implements
   `Execute` with a background context — that is a real bug for hung remote servers.

---

## 2. Work breakdown

### Phase 0 — Wire up the dormant client (P0)  *[unblocks everything]*

The `Manager` + `StdioClient` exist but are **never called** from the app. Phase 0 makes
local stdio MCP servers usable end-to-end with minimal surface.

**Files:**
- `config/config.go` — add `MCP map[string]MCPServerConfig` field + `MCPServerConfig`.
- `config/config_merge.go` — `mergeMCP` (per-server, last-write-wins like `mergeProviders`
  but keyed by map name; deep-merge `Environment`/`Headers` maps key-by-key).
- `config/config_validate.go` — `validateMCP`: require `type ∈ {local,remote}`; `local`
  requires non-empty `command`; `remote` requires parseable `url`; `timeout` parses via
  `time.ParseDuration`; reject unknown fields' typos is out of scope.
- `config/configs/*.yaml` (embedded default) — add commented-out `mcp:` example block.
- `internal/mcp/config.go` — extend `ServerConfig` (below); keep legacy `mcp.json` loader
  but mark deprecated and translate into `MCPServerConfig`.
- `internal/app/bootstrap.go` — `registerMCPServers(reg, cfg, projectDir, logger)` called
  from `registerTools`; builds `mcp.NewManager(reg)`, loops enabled servers, `Connect`
  **concurrently** (`errgroup`-style with `sync.WaitGroup` + per-server error capture —
  no new dep), logs failures, never aborts startup.
- `internal/app/subsystems.go` — add `mcpManager *mcp.Manager` to `subsystems`.
- `internal/app/app.go` — on shutdown path, `mcpManager.Close()`.

**`MCPServerConfig` (new):**
```go
type MCPServerConfig struct {
    Type        string            `yaml:"type"`                  // "local" | "remote"
    Command     []string          `yaml:"command,omitempty"`     // local: argv[0] is cmd
    Cwd         string            `yaml:"cwd,omitempty"`         // local: working dir (rel → projectDir)
    Environment map[string]string `yaml:"environment,omitempty"` // local: merged over os.Environ
    URL         string            `yaml:"url,omitempty"`         // remote
    Headers     map[string]string `yaml:"headers,omitempty"`     // remote
    Enabled     *bool             `yaml:"enabled,omitempty"`     // nil=true (tri-state)
    Timeout     string            `yaml:"timeout,omitempty"`     // duration; default 30s
    OAuth       *MCPOAuthConfig   `yaml:"oauth,omitempty"`       // remote; phase 3
}
```

**Tests (P0):**
- `config`: merge precedence (embedded < home < project < local), env-var override,
  validate errors for each bad shape. Table-driven.
- `internal/mcp`: fake `client.Client` via existing `SetClientFactory`; `Connect`
  registers `mcp__srv__*` tools; `Disconnect` unregisters; failed `ListTools` closes the
  client and records status.
- `internal/app`: `registerMCPServers` with one enabled local server registers tools;
  disabled server registers none; a failing server logs but startup continues.

**Acceptance:** `goa` with a stdio MCP server in `.goa/config.yaml` exposes
`mcp__<srv>__<tool>` to the agent and the agent can call it.

---

### Phase 1 — Transports done right (P1)

#### 1a. Extract `client.Transport` (refactor, behavior-preserving)
- `internal/mcp/client/transport.go` — new `Transport` interface:
  ```go
  type Transport interface {
      Send(ctx context.Context, frame []byte) error
      // Recv returns the next inbound frame; blocks until frame/error/close.
      Recv(ctx context.Context) ([]byte, error)
      Close() error
  }
  ```
- New `client.RPC` type: owns id counter, pending-waiter map, single reader goroutine,
  write mutex, ctx-aware `Call(method, params)`, notification dispatch — **moved
  verbatim** from `StdioClient`. `StdioClient` becomes: spawn process + expose
  stdio as a `Transport` + delegate to `RPC`. Its existing `stdio_test.go` must pass
  unchanged (proves equivalence).
- This is the keystone: it lets HTTP reuse the exact correlation/cancellation logic.

#### 1b. `client.HTTPClient` — Streamable HTTP (primary) + SSE (fallback)
Spec basis: MCP "Streamable HTTP" transport (2025-03-26) with automatic fallback to the
legacy "HTTP+SSE" transport (2024-11-05), exactly as OpenCode does.

- `internal/mcp/client/http.go`:
  - `NewHTTPClient(url string, opts HTTPOpts{Headers, Timeout, AuthProvider})`.
  - `Initialize`: POST `initialize` to `url` with `Accept: application/json, text/event-stream`.
    - If server responds `2xx` with JSON or opens an SSE stream → **Streamable HTTP mode**:
      - subsequent requests = HTTP POST of a single JSON-RPC message; response may be
        `application/json` (single result) or `text/event-stream` (stream of events until
        the response for that request id arrives).
      - honor `Mcp-Session-Id` response header → send on all subsequent requests.
      - optional server→client stream via GET on the endpoint (SSE); support it for
        notifications, tolerate `405` (server doesn't offer it).
    - If POST returns `4xx/5xx` or content-type indicates legacy → **fallback**: GET
      `url` over SSE; first event `endpoint` gives the POST URL (legacy HTTP+SSE).
  - Implements `client.Client` (Initialize/ListTools/CallTool/Close) by driving `RPC`
    over the HTTP `Transport`.
  - Timeouts: per-request `context.WithTimeout(cfg.Timeout)`; **reset on progress**
    (if a `notifications/progress` for the in-flight request arrives, extend deadline) —
    mirror OpenCode's `resetTimeoutOnProgress`.
  - Retries: connect/list are **not** auto-retried (fail → `failed` status, surfaced to
    user). A single idempotent reconnect on transient network error is handled by the
    Manager (1d), not the transport.
- SSE parsing: `internal/mcp/client/sse.go` — minimal `text/event-stream` decoder
  (`event:`/`data:`/`id:`/`retry:` lines, multi-line `data:`), `bufio.Scanner` with a
  generous buffer. Reconnect with `Last-Event-ID` on dropped stream.

#### 1c. `catalog` subpackage
- `internal/mcp/catalog/paginate.go` — generic cursor pagination
  (`Paginate(ctx, list func(cursor string) (Page, error))`), dedupe guard, max 1000 pages,
  error on duplicate cursor (OpenCode parity).
- `internal/mcp/catalog/names.go` — `Sanitize(s)` (`[^a-zA-Z0-9_-]→_`), `ToolName(server,
  tool)` → `mcp__<san(server)>__<san(tool)>`.
- `internal/mcp/catalog/convert.go` — MCP `content[]` → text (concat `type:"text"`;
  when empty but `structuredContent` present → `json.Marshal(structuredContent)`);
  `isError` → `internal.ToolError{Type:"mcp_call_failed"}`.
- `internal/mcp/catalog/tolerant.go` — `tools/list` decoding that, on schema-validation
  error mentioning `outputSchema`/unresolvable `$ref`, retries with a tolerant schema
  that omits `outputSchema` (OpenCode `TolerantListToolsResultSchema`).

#### 1d. Manager maturity
- `internal/mcp/manager.go`:
  - `Status() map[string]ServerStatus` with
    `ServerStatus{State: connected|disabled|failed|connecting, Err string, Tools int}`.
  - `Connect` records per-server status; concurrent connect; **env merge** for local
    (`os.Environ()` + `cfg.Environment`), **`cmd.Dir = resolve(projectDir, cfg.Cwd)`**,
    per-server `timeout`.
  - **`onclose` handling:** transports report unexpected close → set status `failed`,
    `UnregisterGroup(prefix)` so stale tools disappear from the agent.
  - **`notifications/tools/list_changed`:** re-`ListTools` and atomically swap the
    registered group (unregister old, register new) — keeps agent tool set fresh.
  - **server log notifications** (`notifications/message`) → `agentic.Logger` by level.
  - **roots:** answer `roots/list` with `[{uri: file://<projectDir>}]` (OpenCode does;
    enables servers that scope to workspace roots).
  - **process-tree cleanup** on `Close()` for stdio: kill child + descendants
    (`pgrep -P` walk on unix; skip on Windows — same as OpenCode).
- `mcpTool` → implement `agentic.ContextTool.ExecuteContext(ctx, input)`; keep `Execute`
  delegating with `context.Background()` for the base interface.

**Tests (P1):**
- `client`: httptest-based fake MCP server for **both** Streamable HTTP and legacy SSE;
  table cases: JSON response, SSE response, session-id propagation, GET-stream `405`
  tolerated, fallback on legacy, timeout, progress-reset, malformed frame.
- `client/sse`: multi-line data, comments, `id:`/`retry:` handling.
- `catalog`: pagination across 3 pages, duplicate-cursor error, >1000 pages error;
  sanitize edge cases; content conversion incl. structuredContent; tolerant decode.
- `manager`: env merge; cwd resolution; onclose → failed + unregister; tools/list_changed
  swap; status transitions. Refactor equivalence: existing `stdio_test.go` untouched & green.

**Acceptance:** Goa connects to a **remote** MCP server over HTTP (and a legacy SSE
server), with per-server env/cwd/timeout honored; tool list refreshes live.

---

### Phase 2 — CLI + lifecycle polish (P1)

Commands self-register via `init()` in `core/commands/` (pattern from `docs.go`).

- `core/commands/mcp.go` — parent `/mcp` command + subcommands:
  - `/mcp:list` — table: name, type, status icon (✓/○/⚠/✗), tool count, target
    (command or url). Reads `subsystems.mcpManager.Status()`.
  - `/mcp:add <name> --url <u> [--header K=V]...` or `/mcp:add <name> -- <cmd...>`
    `[--env K=V]...` — writes into the **project** `.goa/config.yaml` under `mcp:`,
    preserving existing content (YAML node append, not full rewrite of comments).
  - `/mcp:remove <name>` — remove from project config.
  - `/mcp:enable <name>` / `/mcp:disable <name>` — flip `enabled` (persist + live
    connect/disconnect).
  - `/mcp:reconnect <name>` — drop + reconnect.
  - `/mcp:debug <name>` — connect one-shot, print negotiated protocol version, server
    info, and the tool list; for diagnosing a failing server.
- Optional mirror: `cmd/goa` subcommand `goa mcp …` (headless) reusing the same logic —
  decide during implementation; slash commands are the primary surface.
- Footer/TUI (optional, behind flag): show count of connected MCP servers; on
  connect/fail publish a toast via the existing event bus (`internal/event`).

**Tests (P2):** command parsing/validation (url-vs-command exclusivity, K=V parsing),
`add`/`remove` round-trip against a temp project config, `list` rendering from a fake
manager, `enable/disable` toggles manager state. Use `t.TempDir()`.

**Acceptance:** user runs `/mcp:add fs -- npx -y @mcp/server-filesystem .`, sees it in
`/mcp:list` as connected, and the agent gains its tools — no manual YAML.

---

### Phase 3 — OAuth for remote servers (P2)

Port OpenCode's three pieces, dependency-free, against Goa's `internal/secrets` + a tiny
localhost listener. Only for `type: remote` with `oauth` not `false`.

- `internal/mcp/oauth/store.go` — persistent `mcp-auth.json` in the goa data dir
  (`~/.goa/`), mode `0600`, guarded by a file lock; stores per-server
  `{tokens{access,refresh,expiresAt,scope}, clientInfo{clientId,clientSecret,...},
  codeVerifier, oauthState, serverUrl}` (OpenCode `McpAuth.Entry`).
- `internal/mcp/oauth/provider.go` — `Provider` implementing the MCP auth flow:
  - **Dynamic client registration (RFC 7591)** when no `clientId` configured; reuse stored
    `clientInfo` keyed by `serverUrl`; drop expired client secrets.
  - **PKCE** (S256 code verifier/challenge) + `state` for CSRF.
  - **Authorization URL** build + token exchange + **refresh** on `expiresAt`.
  - Injects `Authorization: Bearer <token>` into the HTTP transport's headers; on `401`
    → trigger (re)auth flow.
- `internal/mcp/oauth/callback.go` — localhost HTTP listener on
  `http://127.0.0.1:<port>/mcp/oauth/callback` (default port `19876`, configurable via
  `oauth.callbackPort` / `oauth.redirectUri`); waits for the `code`+`state`, validates
  state, returns code to the flow; opens the system browser (`open`/`xdg-open`/`start`)
  with the auth URL; publishes a toast if the browser can't open.
- `MCPOAuthConfig`:
  ```go
  type MCPOAuthConfig struct {
      ClientID     string `yaml:"clientId,omitempty"`
      ClientSecret string `yaml:"clientSecret,omitempty"`
      Scope        string `yaml:"scope,omitempty"`
      CallbackPort int    `yaml:"callbackPort,omitempty"`
      RedirectURI  string `yaml:"redirectUri,omitempty"`
      Disabled     bool   `yaml:"disabled,omitempty"` // oauth:false equivalent
  }
  ```
- Status gains `needs_auth` and `needs_client_registration`; `/mcp:auth <name>`,
  `/mcp:logout <name>`, `/mcp:auth` (list auth status) commands.

**Tests (P3):** httptest authorization server (authorize + token + register + refresh);
state-mismatch rejection; expired-token refresh path; dynamic-registration reuse;
callback listener round-trip; `401`→needs_auth transition. Keep network local.

**Acceptance:** connecting to an OAuth-protected remote MCP server opens a browser,
completes the flow, stores tokens, and refreshes them on expiry; `/mcp:logout` clears.

---

### Phase 4 — MCP resources & prompts as first-class tools (P3)

OpenCode surfaces server **resources/prompts** via dedicated meta-tools. Adds agent value
beyond plain tools.

- Extend `client.Client` (or a secondary `ResourceClient` interface) with
  `ListResources`, `ReadResource`, `ListResourceTemplates`, `ListPrompts`, `GetPrompt` —
  capability-gated (`getServerCapabilities`).
- New Goa tools (registered per connected server):
  - `mcp__<srv>__list_resources`, `mcp__<srv>__read_resource{uri}`,
    `mcp__<srv>__list_resource_templates`, `mcp__<srv>__get_prompt{name,args}`.
- Permission patterns `mcp:<server>:<uri>` integrate with `internal/perms` so resource
  reads are governable like other tools.
- Inject server **instructions** (`getInstructions()`) into the system prompt section for
  connected servers (OpenCode does this), with the tool list.

**Tests (P4):** capability gating (no resources capability → no meta-tools), read/list
round-trips, permission-rule matching, instructions injection.

---

## 3. Config example (final shape)

```yaml
mcp:
  filesystem:                      # local stdio
    type: local
    command: ["npx", "-y", "@modelcontextprotocol/server-filesystem", "."]
    cwd: .
    environment:
      DEBUG: "1"
    enabled: true
    timeout: 30s

  github:                          # remote, bearer header, no OAuth
    type: remote
    url: https://api.githubcopilot.com/mcp/
    headers:
      Authorization: "Bearer ${GITHUB_TOKEN}"   # ${VAR} expanded at load
    timeout: 30s

  internal_api:                    # remote with OAuth (phase 3)
    type: remote
    url: https://mcp.internal.example.com/mcp
    oauth:
      scope: "mcp:read mcp:write"
      callbackPort: 19876
    timeout: 45s
```

Env expansion: `${VAR}` / `${VAR:-default}` in `headers` and `environment`, resolved at
config load (reuse the same expansion helper Goa already uses for provider `api_key`, if
present — otherwise add a small `internal/expand` helper).

---

## 4. File-by-file change list

| File | Change | Phase |
|---|---|---|
| `config/config.go` | `MCP map[string]MCPServerConfig`, `MCPServerConfig`, `MCPOAuthConfig` | 0 |
| `config/config_merge.go` | `mergeMCP` (per-server LWW + deep-merge env/headers) | 0 |
| `config/config_validate.go` | `validateMCP` | 0 |
| `config/configs/default.yaml` | commented `mcp:` example | 0 |
| `internal/mcp/config.go` | extend `ServerConfig`; legacy `mcp.json`→`MCPServerConfig` bridge | 0 |
| `internal/mcp/manager.go` | status map, env/cwd/timeout, onclose, tools/list_changed, roots, tree-kill, `ContextTool` | 0+1 |
| `internal/mcp/status.go` (new) | `ServerStatus`, state enum | 1 |
| `internal/mcp/client/transport.go` | `Transport` iface + shared `RPC` (moved from stdio) | 1 |
| `internal/mcp/client/stdio.go` | refactor onto `Transport`+`RPC`; env/cwd; tree-kill | 1 |
| `internal/mcp/client/http.go` (new) | Streamable HTTP + SSE-fallback client | 1 |
| `internal/mcp/client/sse.go` (new) | SSE decoder | 1 |
| `internal/mcp/catalog/*` (new) | paginate, names, convert, tolerant | 1 |
| `internal/app/bootstrap.go` | `registerMCPServers`; call from `registerTools` | 0 |
| `internal/app/subsystems.go` | `mcpManager` field | 0 |
| `internal/app/app.go` | shutdown `mcpManager.Close()` | 0 |
| `core/commands/mcp.go` (new) | `/mcp` + list/add/remove/enable/disable/reconnect/debug | 2 |
| `internal/mcp/oauth/{store,provider,callback}.go` (new) | OAuth | 3 |
| `internal/mcp/client/resources.go` (new) | resources/prompts client methods | 4 |
| `internal/mcp/tools_resources.go` (new) | meta-tools + perms | 4 |
| tests alongside each | regression + table-driven | all |

---

## 5. Testing & quality gates

Per Goa's gate before any commit: `go vet ./...`, `go test -count=1 -race -cover ./...`,
`gocognit -over 15`, `gocyclo -over 12`. Coverage targets: `internal ≥90%`, `tools ≥80%`.

- **No live network in unit tests.** Stdio → in-memory pipes / a tiny fake stdio server
  binary; HTTP/SSE → `net/http/httptest`; OAuth → httptest auth server. All <100ms.
- **Regression-first.** Each Phase-0/1 behavior change lands with a failing test first
  (RED) where it fixes an existing gap (e.g., `ExecuteContext` cancellation, tools
  persisting after server close).
- **Refactor equivalence (1a):** `client/stdio_test.go` must pass **unmodified** after
  the `Transport`/`RPC` extraction — that is the proof the refactor is behavior-preserving.
- **Race safety:** the manager mutates the tool registry from notification callbacks;
  cover `tools/list_changed` + concurrent `Call` under `-race`.
- Use the **`tui-test`** skill to assert `/mcp:list` and connect/fail toasts render
  without a live model, and **`golang-check`** before committing.

---

## 6. Risks & mitigations

| Risk | Mitigation |
|---|---|
| Hand-rolling Streamable HTTP/SSE gets protocol details wrong | Follow the MCP spec versions OpenCode targets (2025-03-26 streamable, 2024-11-05 legacy); httptest fixtures captured from real servers; tolerant decoders. |
| `Transport` refactor breaks the working stdio path | Pure code-move; keep `stdio_test.go` green; no behavior change in the same commit. |
| Registry mutation from notification goroutines races | Single `Manager.mu`; route all registry writes through one method; `-race` tests. |
| Hung remote tools block the agent turn | `ExecuteContext` + per-request timeout + reset-on-progress; transport enforces deadline. |
| OAuth token leakage | `0600` store + file lock; reuse `internal/secrets` redaction so tokens never hit logs. |
| Scope creep (resources/prompts/OAuth) | Phased; each phase independently shippable; Phase 0 alone delivers value. |

---

## 7. Sequencing & effort (rough)

1. **Phase 0** — config + wiring (small; ~1 day) → *local stdio usable.*
2. **Phase 1** — transports + catalog + manager (largest; ~3–4 days) → *remote usable.*
3. **Phase 2** — CLI (medium; ~1–2 days) → *self-service UX.*
4. **Phase 3** — OAuth (medium-large; ~2–3 days) → *protected remote servers.*
5. **Phase 4** — resources/prompts (medium; ~2 days) → *full MCP surface.*

Each phase is a separate, green, committable unit. Phases 0–2 deliver the core "Goa is an
MCP client" story; 3–4 round out parity with OpenCode.
