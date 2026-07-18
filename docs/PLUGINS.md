<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# JS Extensions (Plugins)

Goa supports JavaScript plugins via the **Goja** runtime — a pure-Go JavaScript
engine. Plugins run inside the Goa process and can register tools, commands,
event observers, and UI elements.

## Overview

A plugin is a directory containing:

```
my-plugin/
├── plugin.yaml        # Manifest (required)
├── plugin.js          # Entry point (required)
└── README.md          # Optional documentation
```

When Goa starts (or on `/reload`), it scans plugin directories for
`plugin.yaml` manifests, loads matching `plugin.js` files, and runs them
inside a Goja JS runtime. The JS code can use the `goa.*` API to extend Goa.

## Plugin Manifest

```yaml
id: my-plugin           # Required — unique plugin identifier
name: My Plugin         # Required — human-readable name
version: 1.0.0          # Optional — semver
description: >-         # Optional — what this plugin does
  Provides custom tools for project management
entry: plugin.js        # Optional — default: "plugin.js"
goa_min_version: 0.1.0  # Optional — minimum Goa version
```

## Plugin API Reference

The Goja runtime provides a `goa` global object with the following methods.

### `goa.config()`

Returns the Goa configuration object as a JavaScript object. Useful for
reading plugin-specific config sections.

```javascript
var cfg = goa.config();
goa.logger().info("Active profile: " + cfg.active_profile);
```

### `goa.logger()`

Returns a logger object with `.info()`, `.warn()`, `.error()`, `.debug()`
methods. Use this instead of `console.log` for structured logging.

```javascript
var log = goa.logger();
log.info("Plugin loaded successfully");
log.warn("Deprecated API used");
log.error("Something went wrong: " + errorMessage);
```

### `goa.registerTool({name, description, execute})`

Registers a new agent tool. The tool becomes available to the LLM agent.

| Parameter | Type | Description |
|-----------|------|-------------|
| `name` | `string` | Tool name (used by LLM, e.g., `current_time`) |
| `description` | `string` | Description for the LLM to understand when to use it |
| `execute` | `function(params)` | Synchronous function that receives params and returns a result |

```javascript
goa.registerTool({
  name: "current_time",
  description: "Get the current date and time in ISO 8601 format",
  execute: function(params) {
    return new Date().toISOString();
  }
});
```

The `execute` function receives parameters as an object with named fields.
Return a string or an object (will be serialized to JSON).

### `goa.registerCommand({name, aliases, shortHelp, longHelp, run})`

Registers a new slash command. The command becomes available as `/<name>`
in the input line.

| Parameter | Type | Description |
|-----------|------|-------------|
| `name` | `string` | Command name (e.g., `hello`) |
| `aliases` | `string[]` | Optional alternative names |
| `shortHelp` | `string` | Short description for completions |
| `longHelp` | `string` | Full usage documentation |
| `run` | `function(args)` | Function that receives string array and returns result string |

```javascript
goa.registerCommand({
  name: "hello",
  shortHelp: "Say hello",
  longHelp: "Usage: /hello [name]\n\nGreets the user by name.",
  run: function(args) {
    if (args.length > 0) {
      return "Hello, " + args[0] + "!";
    }
    return "Hello, World!";
  }
});
```

### `goa.registerObserver(callback(eventName, payload))`

Registers an observer that receives events from the Goa event bus.

```javascript
goa.registerObserver(function(eventName, payload) {
  if (eventName === "mode.changed") {
    goa.logger().info("Mode changed from " + payload.from + " to " + payload.to);
  }
});
```

### `goa.callTool(name, params)`

Calls a registered tool programmatically from JavaScript. Returns the tool's
output.

```javascript
var result = goa.callTool("read", { path: "src/main.go" });
goa.logger().info("File content: " + result);
```

## Events

Plugins can observe these events via `goa.registerObserver`:

| Event | Payload | Description |
|-------|---------|-------------|
| `mode.changed` | `{ from, to }` | Autonomy mode changed |
| `skill.changed` | `{ name, type }` | Active skill changed |
| `tool.call` | `{ name, params }` | A tool was invoked |
| `tool.result` | `{ name, result }` | A tool returned a result |
| `session.start` | `{ timestamp }` | A new agent session started |
| `session.end` | `{ timestamp, turns }` | An agent session ended |
| `pipeline.stage` | `{ pipeline, stage, status }` | Pipeline stage changed |

## UI Extensions

Plugins can add UI elements to the TUI.

### `goa.addPane({id, title, render})`

Adds a pane to the TUI layout.

| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | `string` | Unique pane identifier |
| `title` | `string` | Pane header title |
| `render` | `function()` | Returns a string of ANSI text to render |

### `goa.addSegment({id, priority, render})`

Adds a segment to the mode line (status bar).

| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | `string` | Unique segment identifier |
| `priority` | `number` | Display order (lower = earlier) |
| `render` | `function()` | Returns a string to display |

### `goa.addModal({id, title, render})`

Adds a modal dialog.

| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | `string` | Unique modal identifier |
| `title` | `string` | Modal header title |
| `render` | `function()` | Returns a string of ANSI content |

> **Note:** The UI bridge is namespaced under `goa.ui` — use
> `goa.ui.addSegment(...)`, `goa.ui.addPane(...)`, `goa.ui.addModal(...)`, and
> `goa.ui.refreshSegment(id)`. The bare `goa.addSegment`/`goa.addPane`/
> `goa.addModal` forms are deprecated aliases.

## Extended Bridges

Beyond tools/commands/observers, plugins can use these bridges. All are
optional — a plugin that doesn't need them simply doesn't call them.

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
| `timeoutMs` | `number` | `30000` | Per-request timeout |

Returns `{status, headers, body, error}`. `error` is a non-empty string on
network/policy failure (status stays `0`), so plugins can branch on failures.

```javascript
var resp = goa.http.fetch("https://api.example.com/usage", {
  method: "GET",
  headers: { "Authorization": "Bearer " + token },
  timeoutMs: 15000
});
if (resp.error) { /* handle */ }
var data = JSON.parse(resp.body);
```

### `goa.storage`

Per-plugin persistent key/value storage, backed by
`~/.goa/plugins/<id>/storage.json` (mode `0600` — it holds credentials).
Values are strings.

```javascript
goa.storage.set("access_token", token);
var token = goa.storage.get("access_token");   // "" when absent
goa.storage.delete("access_token");
var keys = goa.storage.keys();
```

### `goa.setInterval(fn, ms)` / `goa.setTimeout(fn, ms)`

Timers for polling and carousels. `ms` is clamped to a 250ms minimum so a
buggy plugin can't busy-spin the JS runtime. Each returns a timer id for
`goa.clearInterval(id)` / `goa.clearTimeout(id)`. Callbacks run on the plugin
VM (serialized with all other JS).

```javascript
var id = goa.setInterval(function() {
  goa.ui.refreshSegment("quota");
}, 3000);
```

### `goa.ui.refreshSegment(id)`

Signals that a status-bar segment's content changed; the footer re-renders it
on the next frame. Segment `render()` functions must **read cached state only**
— they are called on the render path and must never fetch or block.

### `goa.registerHotkey({key, ctrl, alt, shift, description, handler})`

Registers a keyboard shortcut. Built-in Go bindings take precedence; a plugin
key that collides with a built-in simply never fires. The handler runs on the
plugin VM.

```javascript
goa.registerHotkey({
  key: "q", ctrl: true, shift: true,
  description: "Refresh quota",
  handler: function() { refreshAll(); goa.ui.refreshSegment("quota"); }
});
```

### `goa.openBrowser(url)`

Opens an `http(s)` URL in the user's default browser (for OAuth flows). Always
also print the URL via `goa.output` as a fallback for headless sessions.

### `goa.output(message)`

Writes a user-visible message into the conversation viewport (as an output
modal), not the log. Use for OAuth instructions and login results.

### `goa.sessionUsage()`

Returns cumulative session token stats for the local/inferred fetcher:

```javascript
var u = goa.sessionUsage();
// { input, output, cacheRead, cacheWrite, turns, toolCalls }
```

## Concurrency Model

All JavaScript across all plugins is serialized behind a single VM lock —
goja runtimes are not goroutine-safe, and plugins have asynchronous entry
points (timers, hotkeys, tool exec, commands). Bridge calls
(`goa.http.fetch`, `goa.storage`) block **only the calling JS flow**; timers
and hotkeys wait their turn on the lock. Two consequences:

1. **Segment `render()` must be a pure cache read.** The footer calls it on
   the render path; a blocking render stalls the TUI.
2. **Timers/hotkeys run on the VM**, so they can call other bridges safely.

## Module Loading (`require`)

Plugins can split code across files with a scoped CommonJS `require()`:

```javascript
// plugin.js
var format = require("./lib/format.js");
var anthropic = require("./fetchers/anthropic.js");
```

- Paths resolve **relative to the requiring module** (Node semantics).
- Modules use `exports.foo = ...` or `module.exports = {...}`.
- A per-plugin cache means repeated requires share module state and circular
  requires don't recurse infinitely.
- Paths are confined to the plugin directory (no `../../etc`).

## Bundled Plugins

Goa ships built-in plugins embedded in the binary (currently
**provider-quota**). On startup each is materialized to
`~/.goa/plugins/bundled/<id>@<version>/` and enabled automatically — the
versioned directory means upgrades re-materialize cleanly without a trust
re-prompt. Disable a bundled plugin via config:

```yaml
plugins:
  bundled:
    provider-quota: false
```

## Installation

1. **Place the plugin directory** in one of these locations:
   - `~/.goa/plugins/` — user-global (available in all projects)
   - `.goa/plugins/` — project-local (specific to this project)

2. **Enable the plugin** in your Goa config:

```yaml
plugins:
  enabled:
    - my-plugin       # Enable specific plugins
    # - "*"           # Or enable all plugins
```

3. **Run `/reload` or restart Goa** to load the plugin.

## Complete Example

The following plugin registers a `word_count` tool that counts words in a
file, a `/stats` command that shows usage statistics, and observes
`tool.call` events to track tool usage.

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

// Register a tool
goa.registerTool({
  name: "word_count",
  description: "Count the number of words in the provided text",
  execute: function(params) {
    var text = params.text || "";
    var words = text.trim().split(/\s+/);
    return words.length + " words";
  }
});

// Register a command
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

// Observe events
goa.registerObserver(function(event, payload) {
  if (event === "tool.call") {
    var name = payload.name;
    toolCalls[name] = (toolCalls[name] || 0) + 1;
    log.info("Tool called: " + name);
  }
});

log.info("Stats plugin loaded");
```

## Best Practices

1. **Use `goa.logger()` for debugging** — it's routed to Goa's logging
   system and respects log level configuration.

2. **Keep execute functions synchronous** — Goja does not support async/await
   or Promises natively. For async operations, use callbacks.

3. **Handle errors gracefully** — if your tool's execute function throws
   an exception, the agent will see a generic error. Catch and return
   descriptive messages:

```javascript
execute: function(params) {
  try {
    // ... your logic
    return result;
  } catch (e) {
    return "[tool error: my-plugin]\n" + e.message + "\nHint: check your input";
  }
}
```

4. **Test with `goa.callTool()`** before releasing — you can test your
   tool registration from another command or plugin.

5. **Follow Goa's error format** for tools:
   `[tool error: type]\n<detail>\nHint: <action>`

6. **Use namespaced tool/command names** — prefix with your plugin ID
   to avoid collisions: `myplugin_my_tool`.

7. **Keep plugins stateless** where possible — the runtime may be
   recreated on reload. Use Goa's memory system for persistence.

## Limitations

- **No DOM/browser APIs** — Goja is a ES5.1+ engine with limited ES6
  support. Standard library features like `Array.isArray`, `String.trim`,
  `JSON.parse` are available; `Promise`, `Map`, `Set`, `Proxy` are not.
- **No direct filesystem access** — use Goa's tools (`read`,
  `edit`, `bash`) via `goa.callTool()`.
- **No network access** — use Goa's tools or agent capabilities.
- **Async operations** — use callbacks, not Promises or async/await.
- **Plugin hot-reload is not yet implemented** — `/reload` re-scans
  directories but does not stop old JS runtimes. Restart Goa to fully
  reload plugins (future work).

## Troubleshooting

| Symptom | Likely Cause | Solution |
|---------|--------------|----------|
| Plugin not loaded | `plugin.yaml` missing or invalid | Run `goa.validateManifest()` or check YAML syntax |
| `goa` is not defined | Plugin loaded outside Goja runtime | Plugins only work inside Goa's JSBridge |
| Tool not available to agent | Plugin not enabled in config | Check `plugins.enabled` in your config file |
| Command not found | `/reload` not run | Run `/reload` after installing plugins |
| JS syntax error | ES6+ feature used | Stick to ES5.1 syntax (var, function, no arrow functions) |
