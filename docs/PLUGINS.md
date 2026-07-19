<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# JS Extensions (Plugins)

Goa supports JavaScript plugins via the **Goja** runtime — a pure-Go JavaScript
engine. Plugins run inside the Goa process and can register tools, commands,
event observers, lifecycle hooks, UI elements (status-bar segments, panes,
modals), hotkeys, and more.

This document is the complete plugin reference. The [provider-quota
plugin](#reference-implementation-provider-quota) is the canonical reference
implementation — read its source alongside this guide to understand every
pattern.

---

## Plugin Manifest

A plugin is a directory with:

```
my-plugin/
├── plugin.yaml        # Manifest (required)
├── plugin.js          # Entry point (default: "plugin.js")
├── lib/               # Modules (CommonJS)
├── fetchers/          # Sub-modules (any structure)
└── README.md          # Optional documentation
```

### Manifest fields

```yaml
id: my-plugin           # Required — unique plugin identifier
name: My Plugin         # Required — human-readable name
version: 1.0.0          # Optional — semver
description: >-         # Optional — what this plugin does
  Provides custom tools for project management
entry: plugin.js        # Optional — default: "plugin.js"
goa_min_version: 0.1.0  # Optional — minimum Goa version
skills_dir: skills      # Optional — relative path to skills loaded on enable
permissions:            # Optional — declare what the plugin needs
  - provider-keys       #   "provider-keys" → expose API keys in goa.config()
```

| Field | Required | Description |
|-------|----------|-------------|
| `id` | Yes | Unique slug used in paths, lockfile, and `/plugin` commands |
| `name` | Yes | Human-readable display name |
| `version` | No | Semver string for update tracking |
| `description` | No | One-line description shown in `/plugin:list` |
| `entry` | No | Entry-point JS file; defaults to `plugin.js` |
| `goa_min_version` | No | Minimum Goa version required (not yet enforced) |
| `skills_dir` | No | Relative path to a directory of SKILL.md files loaded when enabled |
| `permissions` | No | List of permission strings. Currently only `"provider-keys"` is defined; it unmasks API keys in `goa.config()` |

### Plugin storage

Each plugin gets its own persistent storage file at
`~/.goa/plugins/<id>/storage.json` (mode `0600` — it may hold credentials).
Access is via the `goa.storage` API — see [Extended Bridges](#extended-bridges).

---

## Plugin API Reference

The Goja runtime provides a `goa` global object with these methods. All are
synchronous from the JS perspective; bridges (`http.fetch`, `storage`) block
the calling JS code but not the TUI.

### `goa.config()`

Returns the Goa configuration as a JavaScript object. The config is **live** —
calling `goa.config()` returns the current state after provider/model switches.

```javascript
var cfg = goa.config();
// {
//   activeProvider: "local",
//   activeModel: "llama-3.2-1b-instruct",
//   providers: {
//     "local": { id:"local", name:"Local LLM", apiKey:"", baseUrl:"...", provider:"lm-studio" },
//     "openai": { id:"openai", name:"OpenAI", apiKey:"sk-...", baseUrl:"https://api.openai.com" },
//     ...
//   }
// }
```

**API keys** are included only when the plugin declares the `provider-keys`
permission in its manifest. Otherwise the `apiKey` field is empty.

### `goa.logger()`

Returns a logger with `.info()`, `.warn()`, `.error()`, `.debug()` methods.

```javascript
var log = goa.logger();
log.info("Plugin loaded successfully");
log.error("Something went wrong: " + errorMessage);
```

### `goa.registerTool({name, description, execute})`

Registers a new agent tool. The tool appears in the LLM's tool set.

| Parameter | Type | Description |
|-----------|------|-------------|
| `name` | `string` | Tool name for LLM invocation |
| `description` | `string` | LLM-facing description of when to use it |
| `execute` | `function(params)` | Receives `{paramName: value}` and returns string or object |

```javascript
goa.registerTool({
  name: "current_time",
  description: "Get the current date and time in ISO 8601 format",
  execute: function(params) {
    return new Date().toISOString();
  }
});
```

The `execute` function receives a single object with named fields (matching the
LLM's JSON tool-call arguments). Return a string or object (serialized to JSON).

### `goa.registerCommand({name, aliases, shortHelp, longHelp, run})`

Registers a slash command available as `/<name>`.

| Parameter | Type | Description |
|-----------|------|-------------|
| `name` | `string` | Command name (e.g., `quota`) |
| `aliases` | `string[]` | Optional alternative names |
| `shortHelp` | `string` | One-line summary for `/help` listings |
| `longHelp` | `string` | Full usage documentation for `/help <cmd>??` |
| `run` | `function(args)` | Receives `string[]` arguments, returns result string |

```javascript
goa.registerCommand({
  name: "hello",
  shortHelp: "Say hello",
  longHelp: "Usage: /hello [name]\n\nGreets the user by name.",
  run: function(args) {
    return args.length > 0 ? "Hello, " + args[0] + "!" : "Hello, World!";
  }
});
```

The `run` function returns a string that Goa writes into the chat viewport.

### `goa.registerObserver(callback(eventName, payload))`

Subscribes to events from Goa's event bus. The callback receives every event;
filter by `eventName` in your handler.

```javascript
goa.registerObserver(function(eventName, payload) {
  if (eventName === "mode.changed") {
    goa.logger().info("Mode changed from " + payload.from + " to " + payload.to);
  }
});
```

See [Events](#events) for the full event list.

### `goa.registerLifecycle(hook, callback(hook, payload))`

Registers a lifecycle callback — invoked at key moments in the Goa runtime. The
`hook` parameter is one of the lifecycle hook names below. The `callback`
receives `(hookName, payload)` where `payload` is a context map.

**Available hooks:**

| Hook | When triggered | Payload |
|------|----------------|---------|
| `"start"` | After all plugins are loaded and the runtime starts | `{}` |
| `"shutdown"` | During graceful shutdown | `{}` |
| `"tool_call"` | Before a tool is executed | `{name, params}` |
| `"tool_done"` | After a tool completes | `{name, result}` |
| `"mode_enter"` | When the autonomy mode changes | `{from, to}` |

```javascript
goa.registerLifecycle("start", function(hook, payload) {
  goa.logger().info("Runtime started — initializing plugin state");
});

goa.registerLifecycle("tool_done", function(hook, payload) {
  if (payload.name === "write") {
    goa.logger().info("File written: " + payload.params.path);
  }
});

goa.registerLifecycle("shutdown", function(hook, payload) {
  goa.logger().info("Plugin shutting down, saving state…");
  // Persist any runtime state before Goa exits
});
```

### `goa.callTool(name, params)`

Calls any registered tool from JavaScript. Returns the tool's output.

```javascript
var result = goa.callTool("read", { path: "src/main.go" });
goa.logger().info("File content: " + result);
```

### `goa.sessionUsage()`

Returns cumulative session token statistics:

```javascript
var u = goa.sessionUsage();
// { input: 142300, output: 85500, turns: 12, toolCalls: 8 }
```

This is the same data the local (inferred) quota fetcher uses. Useful for
plugins that want to track agent resource consumption.

### `goa.segmentColor(name)`

Returns the active theme's hex color string for a semantic color name, or `""`
when coloring is unavailable. Designed for status-bar segments that want
theme-aware coloring without emitting raw ANSI codes.

| Name | Semantic meaning | Typical theme color |
|------|-----------------|-------------------|
| `"ok"` | All within budget | Green |
| `"warn"` | Approaching limit | Orange/yellow |
| `"critical"` | Over budget or error | Red |
| `"pending"` | Still loading data | Neutral/gray |

```javascript
var hex = goa.segmentColor("warn");
// → "#ff8800" or similar (depends on active theme)
```

The main use is building multi-colored segment strings — the provider-quota
plugin uses this to color each quota window independently:

```javascript
function colorizedSegment(entry) {
  var parts = [];
  for (var i = 0; i < entry.limits.length && parts.length < 2; i++) {
    var hex = goa.segmentColor(partColor);
    parts.push(hex ? ansiWrap(hex, text) : text);
  }
  return "[" + parts.join("|") + "]";
}
```

---

## Events

Plugins observe these events via `goa.registerObserver`. The wildcard
observer receives all events:

```javascript
goa.registerObserver(function(event, payload) {
  // event is one of the strings below
  // payload is the event-specific object
});
```

| Event | Payload | Description |
|-------|---------|-------------|
| `mode.changed` | `{ from, to }` | Autonomy mode changed |
| `skill.changed` | `{ name, type }` | Active skill changed |
| `tool.call` | `{ name, params }` | A tool was invoked |
| `tool.result` | `{ name, result }` | A tool returned a result |
| `session.start` | `{ timestamp }` | A new agent session started |
| `session.end` | `{ timestamp, turns }` | An agent session ended |
| `pipeline.stage` | `{ pipeline, stage, status }` | Pipeline stage changed |

---

## Extended Bridges

These bridges are available through the `goa` global when enabled.

### `goa.http.fetch(url, opts)`

Performs an HTTP request on a background goroutine. The JS call blocks until
the response is ready, but **only the calling JS flow blocks** — the TUI stays
responsive. Requests are HTTPS-only, except loopback hosts (`localhost`,
`127.0.0.1`) for local model servers.

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `method` | `string` | `"GET"` | HTTP method |
| `headers` | `object` | — | Request headers |
| `body` | `string\|object` | — | Payload; objects are JSON-encoded |
| `timeoutMs` | `number` | `30000` | Per-request timeout (max 10 MiB response) |

Returns `{status, headers, body, error}`:

```javascript
var resp = goa.http.fetch("https://api.anthropic.com/v1/usage", {
  method: "GET",
  headers: { "x-api-key": apiKey, "anthropic-version": "2023-06-01" },
  timeoutMs: 15000
});
if (resp.error) {
  // Network error, policy refusal, or timeout
  return { error: resp.error };
}
if (resp.status === 401 || resp.status === 403) {
  return { error: "auth_required" };
}
var data = JSON.parse(resp.body);
```

### `goa.storage`

Per-plugin persistent key/value storage. Backed by
`~/.goa/plugins/<id>/storage.json` (mode `0600`, atomic writes via tmp+rename).
Values are strings — the plugin is responsible for JSON serialization.

```javascript
goa.storage.set("access_token", "sk-ant-...");
var token = goa.storage.get("access_token");     // "" when absent
var exists = goa.storage.get("refresh_token");   // "" when absent
goa.storage.delete("access_token");
var allKeys = goa.storage.keys();                // ["refresh_token", ...]
```

> **Credential safety:** Storage files use `0600` permissions. The bridge never
> interprets values — they are opaque strings. OAuth tokens, API keys, and any
> other secrets should go here, not in plugin code.

### `goa.setInterval(fn, ms)` / `goa.setTimeout(fn, ms)`

Timers for polling and one-shot delays. `ms` is clamped to a **minimum of
250ms** so a buggy plugin cannot busy-spin the JS runtime. Each returns a
numeric timer id.

```javascript
var id = goa.setInterval(function() {
  refreshAllDue(false);
  goa.ui.refreshSegment("quota");
}, 60000);
```

| Function | Behavior | Returns |
|----------|----------|---------|
| `goa.setInterval(cb, ms)` | Repeating timer, clamped ≥250ms | Timer ID (number) |
| `goa.setTimeout(cb, ms)` | One-shot timer | Timer ID (number) |
| `goa.clearInterval(id)` | Cancel a repeating timer | — |
| `goa.clearTimeout(id)` | Cancel a one-shot timer | — |

Timer callbacks run on the plugin VM under the global lock. They can call
bridges (`http.fetch`, `storage`, `ui.refreshSegment`) safely. Panics are
contained — a crashing callback does not crash the app.

### `goa.ui.addSegment({id, priority, render})`

Adds a status-bar segment (footer) to the TUI. The `render()` function returns
a string or `{text, color}` object. **Crucially, `render()` must be a pure
cache read** — it is called on the render path and must never fetch or block.

| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | `string` | Unique segment identifier |
| `priority` | `number` | Display order (lower = earlier in the footer) |
| `render` | `function()` | Returns string or `{text, color}` |

```javascript
goa.ui.addSegment({
  id: "quota",
  priority: 10,
  render: function() {
    return _cache.active ? "[8%|24%]" : "";
  }
});
```

**Return value conventions:**

- **Plain string** — rendered as-is in the footer
- **`{text, color}`** — `text` is the display string; `color` names a semantic
  color (`"ok"`, `"warn"`, `"critical"`, `"pending"`) resolved through the
  active theme via `goa.segmentColor`. Unknown/absent colors render unstyled.
- **Pre-colored string** — you can emit raw ANSI codes using `ansiWrap()` (see
  the provider-quota plugin's `colorizedSegment`), but this bypasses theme
  support.
- **Empty string `""`** — the segment is hidden (no space consumed in footer)

### `goa.ui.refreshSegment(id)`

Signals that a segment's rendered content changed; the footer re-renders it on
the next frame. Non-blocking (drops oldest intent when channel is saturated).

```javascript
goa.ui.refreshSegment("quota");
```

### `goa.ui.addPane({id, title, render})`

Adds a pane to the TUI layout (multi-pane view). Less commonly used than
segments.

### `goa.ui.addModal({id, title, render})`

Adds a modal dialog.

> **Note:** The UI bridge is namespaced under `goa.ui` — use
> `goa.ui.addSegment(...)`, `goa.ui.addPane(...)`, `goa.ui.addModal(...)`, and
> `goa.ui.refreshSegment(id)`. The bare `goa.addSegment`/`goa.addPane`/
> `goa.addModal` forms are deprecated aliases.

### `goa.registerHotkey({key, ctrl, alt, shift, description, handler})`

Registers a keyboard shortcut. Built-in Go bindings take precedence — a plugin
key that collides with a built-in simply never fires.

| Parameter | Type | Description |
|-----------|------|-------------|
| `key` | `string` | Base key name (e.g., `"q"`, `"f5"`) |
| `ctrl` | `boolean` | Ctrl modifier |
| `alt` | `boolean` | Alt modifier |
| `shift` | `boolean` | Shift modifier |
| `description` | `string` | Human-readable description for `/hotkeys` |
| `handler` | `function()` | Called when the shortcut is pressed |

```javascript
goa.registerHotkey({
  key: "q", ctrl: true, shift: true,
  description: "Refresh provider quota",
  handler: function() {
    refreshAllDue(true);
    goa.ui.refreshSegment("quota");
  }
});
```

### `goa.openBrowser(url)`

Opens an `http(s)` URL in the user's default browser. Supports `macOS` (open),
`linux/bsd` (xdg-open), and `windows` (rundll32). Best-effort — always also
print the URL via `goa.output` as a fallback for headless sessions.

```javascript
goa.openBrowser("https://api.anthropic.com/settings/billing");
goa.output("Open this URL in your browser:\n  " + url);
```

### `goa.output(message)`

Writes a user-visible message into the conversation viewport (as an output
modal), not the log. Use for OAuth instructions, login results, and any other
user-facing text.

```javascript
goa.output("Authorize provider quota access:\n  https://auth.example.com/device\nEnter code: ABCD-1234");
```

---

## Concurrency Model

All JavaScript across all plugins is serialized behind a **single global VM
lock** — Goja runtimes are not goroutine-safe, and plugins have asynchronous
entry points (timers, hotkeys, tool exec, commands).

- Bridge calls (`http.fetch`, `storage.*`) block **only the calling JS flow**
  while the Go-side operation runs on a background goroutine.
- Timers and hotkeys that arrive while the VM is busy wait their turn on the
  mutex.
- Segment `render()` is called from the TUI render loop, which acquires the VM
  lock. **If `render()` blocks (fetches, waits), the entire TUI freezes.**

**Rule:** Segment `render()` must be a pure cache read. Use timers to refresh
data and `ui.refreshSegment(id)` to signal a repaint.

### Panic containment

Timer callbacks, hotkey handlers, and segment renders are wrapped in
`recover()` so a misbehaving plugin cannot crash the app. The error is logged
and execution continues.

---

## Module Loading (`require`)

Plugins use a scoped CommonJS `require()` for multi-file projects:

```javascript
// plugin.js
var format = require("./lib/format.js");
var oauth = require("./lib/oauth.js");
var anthropic = require("./fetchers/anthropic.js");
```

### Rules

1. **Relative paths** resolve against the requiring module's directory (Node
   semantics).
2. **Module exports** use `exports.foo = ...` or `module.exports = {…}`.
3. **Cache** is per-plugin and per-load: repeated `require()` calls return the
   same exports object. This supports shared mutable state (e.g., a fetcher
   registry) and circular imports (cache is populated before execution).
4. **Path confinement** — `require` cannot escape the plugin directory. A
   `require("../../etc/passwd")` attempt throws a JS error.
5. **Extension** — `.js` is appended automatically when omitted.

```javascript
// lib/format.js — pure functions, no goa.* deps (trivially testable)
exports.pct = function(used, limit) {
  return Math.round((used / limit) * 100);
};
exports.tokens = function(n) {
  if (n >= 1000000) return (n / 1000000).toFixed(1) + "M";
  if (n >= 1000) return (n / 1000).toFixed(1) + "K";
  return String(Math.round(n));
};
```

---

## Bundled Plugins

Goa ships built-in plugins embedded in the binary via Go's `//go:embed`. The
current bundled plugin is **provider-quota** (in `plugins/bundled/provider-quota/`).

### How bundled loading works

1. **Materialization** — On startup, each bundled plugin is copied from the
   embed FS into `~/.goa/plugins/bundled/<id>@<version>/`. The versioned
   directory name means an upgraded binary (with a bumped plugin version)
   produces a new directory, leaving the old one intact.
2. **Content validation** — The materialized copy is SHA-256 hashed and
   recorded in the lockfile. Every subsequent startup verifies the hash;
   tampered or stale copies are re-materialized from the trusted embed.
3. **Automatic enablement** — Bundled plugins are enabled automatically
   (no trust prompt).
4. **Disable via config:**

```yaml
plugins:
  bundled:
    provider-quota: false
```

### Creating a new bundled plugin

1. Create a directory under `plugins/bundled/<name>/` with `plugin.yaml` and
   `plugin.js`.
2. Add the embed directive in `plugins/bundled/bundled.go`:
   ```go
   //go:embed <name>
   var FS embed.FS
   ```
3. Register a source function (like `ProviderQuotaSource()`) returning the
   plugin's id and version.
4. Wire it in `internal/app/plugins.go` `materializeBundledPlugins()`.

---

## Plugin Management (`/plugin`)

Goa provides the `/plugin` command for managing plugins:

| Command | Description |
|---------|-------------|
| `/plugin` | Interactive selector: list installed plugins and toggle enabled/disabled with Enter |
| `/plugin:list` | List installed plugins as text with status and hash |
| `/plugin:install:<git-url>` | Install a plugin from a git URL (cloned, validated, recorded in lockfile) |
| `/plugin:remove:<id>` | Uninstall a plugin |
| `/plugin:enable:<id>` | Activate an installed plugin (trust-gated; use `/trust:<id>` first if needed) |
| `/plugin:disable:<id>` | Deactivate an enabled plugin |

> **Colon syntax:** Goa uses colons for arguments (`/plugin:enable:provider-quota`),
> not spaces. Tab completion after `/plugin:enable:` / `/plugin:disable:` offers
> only plugins in the matching state.

### Installation workflow

1. `/plugin:install:https://github.com/user/my-plugin.git` — clones, validates
   manifest, records content hash. Plugin starts **disabled**.
2. `/trust:my-plugin` (if not yet trusted) — required for plugins from new
   sources.
3. `/plugin:enable:my-plugin` — activates the plugin (reload required unless
   called from the running session).
4. `/reload` — loads the plugin's JS runtime into the current session.

### Trust system

Plugins are **permission-gated** via the trust system. Untrusted plugins cannot
be enabled — Goa prompts for trust approval:

```
/trust:my-plugin       # Approve the plugin for this project/user
/trust:revoke:my-plugin  # Revoke trust
```

Trust status is persisted in the trust store and checked on every enable
attempt. Bundled plugins are always trusted.

### Integrity verification

Every install computes a SHA-256 content hash over all files in the plugin
directory. `Enable()` re-verifies the hash before activating. If the plugin
directory is modified after install (tampered or corrupted), enable fails:

```
plugin provider-quota integrity check failed: content changed since install
```

Re-install to fix: `/plugin:remove:<id>` then `/plugin:install:<url>`.

---

## Reference Implementation: provider-quota

The `provider-quota` plugin is the **canonical reference implementation** for
Goa plugins. It demonstrates every major API and architectural pattern. Its
source lives at `plugins/bundled/provider-quota/`.

### What it does

- Tracks usage/quota for all configured LLM providers (Anthropic, OpenAI, Z.ai,
  Kimi, MiniMax, OpenRouter, plus a local/inferred fallback).
- Shows a compact, color-coded quota segment in the footer status bar.
- Provides a `/quota` command with subcommands for detailed breakdowns, JSON
  export, and OAuth management.
- Refreshes data on a background timer (every 60s) and on explicit request
  (`Ctrl+Shift+Q` or `/quota:refresh`).

### Architecture (for plugin authors to study)

```
plugin.js                  — Entry point: registry, scheduler, /quota command
  ├── lib/format.js        — Pure formatting functions (no goa.* deps)
  ├── lib/http-quota.js    — Generic HTTP quota-fetch engine (SOLID: Open/Closed)
  ├── lib/oauth.js         — OAuth device-code flow + token refresh
  └── fetchers/*.js        — Per-provider descriptors (Anthropic, OpenAI, etc.)
```

#### Pattern 1: Fetcher registry

The plugin maintains a registry of provider fetchers, each implementing a
`fetch(ctx)` function and metadata (`name`, `auth`, `refreshInterval`,
`quotaEndpoint`). Adding a new provider = adding one file + one `register()`
call:

```javascript
// fetchers/newprovider.js
var hq = require("../lib/http-quota.js");
var desc = {
  auth: hq.apiKeyAuth().auth,
  url: function(ctx) { return "https://api.example.com/v1/usage"; },
  map: hq.windowedUsageMapper({...})
};
function fetch(ctx) { return hq.runFetch(desc, ctx); }
module.exports = {
  name: "New Provider", auth: { type: "api_key" },
  refreshInterval: 300000, quotaEndpoint: true, fetch: fetch
};
```

#### Pattern 2: Separate fetch engine from providers

`lib/http-quota.js` implements the **entire HTTP fetch pipeline** once — auth
resolution, GET request, status/parse error mapping, and result shaping.
Individual fetchers supply only a **descriptor** `{auth, url, headers, map}`,
following the Open/Closed Principle:

```javascript
// descriptor shape (see lib/http-quota.js)
{
  auth: function(ctx) -> string|null,          // resolve bearer/api key
  authError: "no_api_key"|"auth_required",    // error when auth() is null
  url: function(ctx) -> string,                // full request URL
  headers: function(ctx, token) -> object,     // request headers
  map: function(body, ctx) -> result           // shape {plan, limits, costUnit?}
}
```

#### Pattern 3: Cache-read segment render

The status segment `render()` function reads from `_cache[id]` only. A
`goa.setInterval(…, 60000)` timer calls `refreshAllDue(false)` to update the
cache, then signals `goa.ui.refreshSegment("quota")` to repaint. This ensures
the TUI render path is never blocked by network.

#### Pattern 4: Theme-aware coloring via `goa.segmentColor`

Each quota window is colored by its projected window-end usage ratio:
- `ratio < 0.8` → `"ok"` (green)
- `ratio < 1.0` → `"warn"` (orange)  
- `ratio >= 1.0` → `"critical"` (red)

The plugin calls `goa.segmentColor(name)` to get the theme's hex, then wraps
the percentage text in ANSI codes. If `segmentColor` is unavailable, it falls
back to a single worst-window color.

#### Pattern 5: OAuth device-code flow

`lib/oauth.js` demonstrates a complete async flow using polling:
1. Request a device code via `goa.http.fetch`
2. Print and open the verification URL via `goa.output` + `goa.openBrowser`
3. Poll for token completion via `goa.setInterval`
4. Store credentials via `goa.storage`
5. Transparent token refresh before expiry

#### Pattern 6: Error vocabulary

The plugin uses a shared error vocabulary that the status segment understands:

| Error string | Meaning |
|-------------|---------|
| `"no_api_key"` | Provider configured without the required API key |
| `"auth_required"` | OAuth token missing, expired, or 401/403 |
| `"http_<status>"` | Non-200 response from the quota API |
| `"bad_response"` | Transport error or unparseable response body |

### What the quota plugin demonstrates

| Feature | Where used |
|---------|------------|
| `goa.registerCommand()` | `/quota` command with colon subcommands |
| `goa.ui.addSegment()` | Status-bar quota display |
| `goa.ui.refreshSegment()` | Signal segment repaints after refresh |
| `goa.registerHotkey()` | `Ctrl+Shift+Q` force-refresh |
| `goa.setInterval()` | 60-second background refresh scheduler |
| `goa.http.fetch()` | Quota API calls per provider |
| `goa.storage` | OAuth token persistence |
| `goa.sessionUsage()` | Local/inferred token counts |
| `goa.segmentColor()` | Theme-aware per-window coloring |
| `goa.config()` | Live provider config (keys, endpoints, active provider) |
| `goa.output()` | OAuth login instructions in viewport |
| `goa.openBrowser()` | OAuth verification URL |
| `require()` | Multi-file module structure |
| `plugin.yaml` `permissions` | `provider-keys` permission to access API keys |

---

## Installing Third-Party Plugins

### From a git repository

```bash
# Install (clones, validates, records hash)
/plugin:install:https://github.com/user/my-plugin.git

# Trust (first time only)
/trust:my-plugin

# Enable
/plugin:enable:my-plugin

# Reload to activate
/reload
```

### From a local directory

Place the plugin directory in one of:

| Location | Scope | Override |
|----------|-------|----------|
| `~/.goa/plugins/<id>/` | User-global (all projects) | Lowest |
| `.goa/plugins/<id>/` | Project-local | Highest |

Then enable in config:

```yaml
plugins:
  enabled:
    - my-plugin
```

### Via git source (the `/plugin:install` command does this automatically):

1. Clones to a temp directory
2. Validates `plugin.yaml` (requires `id` and `name`)
3. Computes content hash over all plugin files
4. Moves to `~/.goa/plugins/<id>/`
5. Records in `~/.goa/plugins/plugin.lock`
6. Leaves the plugin **disabled** — run `/plugin:enable:<id>` to activate

---

## Writing a Plugin (Development Guide)

This section contains everything Goa needs to autonomously create a new plugin.
Follow these steps and patterns.

### Step 1: Create the directory structure

```
my-plugin/
├── plugin.yaml
├── plugin.js
├── lib/               # Optional: modules
└── skills/            # Optional: SKILL.md files
```

### Step 2: Write the manifest

```yaml
id: my-plugin
name: My Plugin
version: 1.0.0
description: What this plugin does
entry: plugin.js
# permissions:
#   - provider-keys    # Uncomment if you need API keys from goa.config()
```

### Step 3: Write the entry point

```javascript
// plugin.js — entry point
var log = goa.logger();

// --- Registration ---

goa.registerTool({ … });
goa.registerCommand({ … });
goa.registerObserver(function(event, payload) { … });

// --- Scheduling (optional) ---

goa.setInterval(function() {
  // Refresh data, then:
  goa.ui.refreshSegment("my-segment");
}, 60000);

log.info("my-plugin loaded");
```

### Step 4: Add a status segment

```javascript
var _cache = {};

goa.ui.addSegment({
  id: "my-plugin",
  priority: 20,
  render: function() {
    // READ CACHE ONLY — never fetch here
    if (!_cache.value) return "";
    return { text: "[" + _cache.value + "%]", color: budgetColor(_cache.value) };
  }
});
```

### Step 5: Use modules for organization

```javascript
// lib/helpers.js
exports.compute = function(x) { return x * 2; };

// plugin.js
var helpers = require("./lib/helpers.js");
```

### Step 6: Handle errors properly

```javascript
execute: function(params) {
  try {
    var result = doWork(params);
    return JSON.stringify(result);
  } catch (e) {
    return "[tool error: my-plugin]\n" + e.message + "\nHint: check your input";
  }
}
```

### Step 7: Use permissions correctly

If your plugin needs to read API keys from `goa.config()`:

1. Add `permissions: [provider-keys]` to `plugin.yaml`
2. The keys are then unmasked in the config object

Permission design rationale: API keys are masked by default so a plugin that
never declared `provider-keys` cannot exfiltrate credentials. The permission
is declared in the manifest, reviewed at install time via the trust system.

### Complete plugin template

**plugin.yaml:**
```yaml
id: my-plugin
name: My Plugin
version: 1.0.0
description: Template plugin demonstrating core APIs
entry: plugin.js
```

**plugin.js:**
```javascript
// --- Logger ---
var log = goa.logger();

// --- State ---
var _state = { counter: 0 };

// --- Tool ---
goa.registerTool({
  name: "my_tool",
  description: "Demonstrates a plugin tool",
  execute: function(params) {
    _state.counter++;
    return "Tool called with: " + JSON.stringify(params);
  }
});

// --- Command ---
goa.registerCommand({
  name: "mystats",
  shortHelp: "Show plugin statistics",
  run: function(args) {
    return "my-plugin stats:\n  calls: " + _state.counter;
  }
});

// --- Observer ---
goa.registerObserver(function(event, payload) {
  if (event === "tool.call") {
    log.info("Tool called: " + payload.name);
  }
});

// --- Hotkey (Ctrl+Shift+M) ---
goa.registerHotkey({
  key: "m", ctrl: true, shift: true,
  description: "My plugin action",
  handler: function() {
    log.info("Hotkey pressed");
  }
});

log.info("my-plugin loaded");
```

---

## Complete Example

The following plugin registers a `word_count` tool, a `/stats` command, and
observes `tool.call` events to track usage.

**plugin.yaml:**
```yaml
id: stats
name: Stats Plugin
version: 1.0.0
description: Tracks tool usage and provides word counting
entry: plugin.js
```

**plugin.js:**
```javascript
var log = goa.logger();
var toolCalls = {};

goa.registerTool({
  name: "word_count",
  description: "Count the number of words in the provided text",
  execute: function(params) {
    var text = params.text || "";
    var words = text.trim().split(/\s+/);
    return words.length + " words";
  }
});

goa.registerCommand({
  name: "stats",
  shortHelp: "Show plugin usage statistics",
  run: function(args) {
    var result = "Plugin Stats:\n";
    result += "  Tools registered: word_count\n";
    result += "  Tool calls tracked: " + Object.keys(toolCalls).length + "\n";
    for (var name in toolCalls) {
      result += "    " + name + ": " + toolCalls[name] + " calls\n";
    }
    return result;
  }
});

goa.registerObserver(function(event, payload) {
  if (event === "tool.call") {
    var name = payload.name;
    toolCalls[name] = (toolCalls[name] || 0) + 1;
    log.info("Tool called: " + name);
  }
});

log.info("Stats plugin loaded");
```

---

## Best Practices

1. **Use `goa.logger()` for debugging** — it's routed to Goa's logging system
   and respects log level configuration.

2. **Segment render() must be a pure cache read** — never fetch, compute, or
   block inside `render()`. Use timers for data refresh and
   `goa.ui.refreshSegment(id)` to trigger repaints.

3. **Keep execute functions synchronous** — Goja does not support async/await
   or Promises natively. For async operations, use `goa.http.fetch` which
   blocks only the calling JS flow.

4. **Use an error vocabulary for the status segment** — return descriptive
   error strings (`"no_api_key"`, `"auth_required"`, `"http_<status>"`) that
   the segment render can display as human-readable warnings.

5. **Follow Goa's error format** for tools:
   `[tool error: type]\n<detail>\nHint: <action>`

6. **Use namespaced tool/command names** — prefix with your plugin ID to avoid
   collisions: `myplugin_my_tool`.

7. **Keep plugins stateless** where possible — the runtime may be recreated on
   reload. Use `goa.storage` for persistence.

8. **Prefer `goa.segmentColor(name)` over raw ANSI** — using semantic color
   names makes your plugin respect the user's theme choice.

9. **Test with `goa.callTool()`** before releasing — you can test tool
   registration from another command or plugin.

10. **Structure by concern** — follow the provider-quota pattern: separate
    fetch engine from provider specifics, keep formatting functions pure,
    use modules for organization.

---

## Limitations

- **No DOM/browser APIs** — Goja is an ES5.1+ engine with limited ES6 support.
  `Promise`, `Map`, `Set`, `Proxy` are not available.
- **No direct filesystem access** — use Goa's tools (`read`, `edit`, `bash`)
  via `goa.callTool()`.
- **No arbitrary network access** — use `goa.http.fetch` (HTTPS only except
  loopback HTTP).
- **Async operations** — use blocking bridges (`http.fetch`, `storage`), not
  Promises or async/await.
- **Plugin hot-reload is not yet implemented** — `/reload` re-scans directories
  but does not stop old JS runtimes. Restart Goa to fully reload plugins
  (future work).

---

## Troubleshooting

| Symptom | Likely Cause | Solution |
|---------|--------------|----------|
| Plugin not loaded | `plugin.yaml` missing or invalid | Check YAML syntax; ensure `id` and `name` are present |
| `goa` is not defined | Plugin loaded outside Goja runtime | Plugins only work inside Goa's JSBridge |
| Tool not available to agent | Plugin not enabled in config | Check `plugins.enabled` in your config file |
| Command not found | `/reload` not run | Run `/reload` after installing/enabling plugins |
| JS syntax error | ES6+ feature used | Stick to ES5.1 syntax (`var`, `function`, no arrow functions) |
| Segment not updating | `render()` not reading fresh cache | Verify your timer calls `goa.ui.refreshSegment(id)` |
| `goa.http.fetch` returns `auth_required` | OAuth token expired | Run `/quota:login:<provider>` or check API key in config |
| Plugin enable fails: "not trusted" | Plugin not approved by trust system | Run `/trust:<id>` to approve |
| Plugin enable fails: "integrity check failed" | Plugin files modified after install | Re-install with `/plugin:remove:<id>` then `/plugin:install:<url>` |
| API keys empty in `goa.config()` | Plugin missing `provider-keys` permission | Add `permissions: [provider-keys]` to `plugin.yaml` |
