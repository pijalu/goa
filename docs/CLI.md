<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Goa — Command-Line Manual

Goa is a terminal-native AI coding agent. Started with no arguments it opens the
interactive full-screen TUI; the same binary also runs headless prompts, serves
the TUI as a web page, manages MCP servers and performs maintenance work.

This manual is the complete command-line reference: every mode, every option,
every configuration layer and every embedded document. `goa --help` prints it in
full, without starting the TUI.

## Synopsis

```
goa [options]                      Interactive TUI (default mode)
goa server [options]               Serve the TUI as a web page (Web UI)
goa mcp <subcommand> [args]        Manage MCP servers from the shell
goa help [topic]                   This manual, or one topic in isolation
```

Goa takes **no positional arguments**. A prompt is passed with `--prompt`
(or `--prompt-file`); a stray word is reported as an unknown command rather than
silently ignored.

## Modes

| Mode | Selected by | What it does |
|------|-------------|--------------|
| **Interactive TUI** | `goa` (default) | Full-screen session: chat, tool calls, diffs, overlays, agent tabs |
| **Headless** | `--prompt "<text>"`, `--prompt-file <path>` | Runs one prompt to completion, prints the result, exits (see *Headless mode*) |
| **Autonomous goal** | `--prompt "<objective>" --goal` | Runs the prompt as a goal objective (headless) |
| **Web UI** | `goa server` | Serves the *same* session over HTTP/WebSocket to a browser (see *Web UI*) |
| **MCP servers** | `goa mcp <subcommand>` | Install, list, enable/disable, remove MCP servers — no TUI, no agent |
| **ACP server** | `--acp` | Speaks the Agent Client Protocol over stdin/stdout for editor clients |
| **Orchestrator resume** | `--orchestrate <run-id>` | Replays a persisted multi-agent run and resumes it headless |
| **Memory consolidation** | `--dream`, `--dream-apply` | Merges `.goa/memory/*.md`; `--dream-apply` writes the consolidated result |
| **Diagnostic export** | `--export-output <zip>` (`--export-session`, `--include-global-log`) | Writes a redacted ZIP bundle of session, logs and config for one issue |
| **Update check** | `--check-update` | Checks for a newer release and exits |
| **Profiling** | `--with-profiling`, `--cpuprofile`, `--memprofile`, `--trace` | Captures Go profiles for the run and writes them on exit |
| **Performance load** | `--perf-load` (`--perf-load-duration`) | Synthetic TUI load instead of an agent turn (rendering benchmarks) |
| **Help** | `goa --help`, `goa help [topic]` | Prints documentation and exits — never starts a session |

## Headless mode

`--prompt` (or `--prompt-file`) switches to a non-interactive session: one turn
(or a goal/orchestrator run) executes, the answer is printed to stdout, and the
process exits. Useful in CI, scripts and batch jobs.

```bash
# One prompt, plain output, no confirmation prompts, 10-turn ceiling
goa --prompt "summarise the changes in git status" --plain --yes --max-turns 10

# Autonomous goal with a budget
goa --prompt "make the test suite pass" --goal --max-turns 40 --timeout 30m
```

Exit codes:

| Code | Meaning |
|------|---------|
| `0` | Success |
| `1` | Configuration or empty-prompt error |
| `2` | Provider/session start failure |
| `3` | `--max-turns` exceeded |
| `4` | `--timeout` expired |
| `5` | Goal not achieved |
| `6` | Orchestrator run failed |

`--yes` auto-approves every tool confirmation, which is what makes a headless
run unattended. Without it, confirmations are asked on the terminal when stdin
is a TTY, and refused outright when it is not — a pipe or a CI job cannot answer
a prompt, and blocking on one would hang the run.

## Web UI (`goa server`)

`goa server` runs the ordinary interactive session against a virtual terminal
and streams the resulting screen to browsers — same engine, same agent, same
commands, same overlays. Nothing about the session changes; only the terminal it
draws into does. The complete server reference is the *Web UI (goa server)*
section below.

## Configuration

Settings are merged in this order, each layer overriding the ones before it:

| Priority | Layer | Location |
|----------|-------|----------|
| 1 | Embedded defaults | compiled in |
| 2 | Home config | `~/.goa/config.yaml` |
| 3 | Project config | `.goa/config.yaml` |
| 4 | Local overrides | `.goa/config.local.yaml` (git-ignored) |
| 5 | Environment | `GOA_*` variables |
| 6 | Command line | `--model`, `--profile`, … |

```bash
goa                                          # defaults + cascade
goa --model gpt-4o --profile planner         # one-off overrides
goa --config ~/.goa/custom-config.yaml       # explicit config file
goa --home /tmp/goa-home                     # relocate the whole home tree
```

`--config` replaces the two project layers (`.goa/config.yaml` and
`.goa/config.local.yaml`) with the single named file — the embedded defaults,
the home config, `GOA_*` variables and the CLI flags still apply. `--home` (or
`GOA_HOME`) relocates the home directory that holds config, cache, logs and
usage state; the project layers stay relative to the working directory.

## Environment variables

| Variable | Effect |
|----------|--------|
| `GOA_<SECTION>_<FIELD>` | Override any config value, `_`-separated path, e.g. `GOA_ACTIVE_MODEL`, `GOA_EXECUTION_MODE`, `GOA_TUI_THEME`, `GOA_PROVIDERS_0_ENDPOINT` |
| `GOA_HOME` | Home directory used for `~/.goa` paths (flag `--home` wins) |
| `GOA_SERVER_AUTH_TOKEN` | Bearer token for `goa server --server-auth=token` (preferred over the flag) |
| `GOA_SERVER_AUTH_PASSWORD` | Password for `goa server --server-auth=basic` (preferred over the flag) |
| `GOA_CRASH_LOG` | Explicit crash-log path (defaults to `.goa/crash.log`) |

Config values support `${VAR}` and `${VAR:-default}` interpolation, so API keys
can stay out of the config file: `api_key: ${OPENAI_API_KEY}`.

## Files and directories

| Path | Content |
|------|---------|
| `~/.goa/config.yaml` | Home configuration |
| `~/.goa/usage.db` | Token usage history (`/usage`) |
| `~/.goa/cache/` | Cached models.dev catalogue and other fetched data |
| `.goa/config.yaml` | Project configuration |
| `.goa/config.local.yaml` | Local overrides (git-ignored) |
| `.goa/crash.log` | Panic and startup-failure diagnostics for this project |
| `.goa/memory/` | Long-term memory notes (`docs/GOALS.md`, dream consolidation) |
| `.goa/goals/` | Goal state |
| `.goa/orchestrator/<run-id>/` | Orchestrator run state, `events.jsonl` |
| `.goa/exports/` | Default destination of `/export` bundles |

## In-session commands

Inside the TUI, slash commands drive everything the agent does not: `/help` lists
every command, `/docs` lists the embedded documentation, `/config` edits
settings, `/mode` switches profile, `/mcp` manages MCP servers with the same
parser as `goa mcp`, `/export` bundles a diagnosis, and so on. See the
`COMMANDS` document for the complete command reference.

## How this manual is put together

`goa --help` prints four parts in this order:

1. this narrative — modes, configuration, environment, files, examples;
2. one section per command-line surface (`goa server`, `goa mcp`), each the
   same text that surface's own `--help` prints;
3. the **documentation index** — every embedded document, each readable with
   `goa help <topic>` or the `goa://<TOPIC>` namespace;
4. the **option reference** — built from the live flag set, so a newly
   registered flag appears automatically.

Parts 3 and 4 are generated rather than written here; they are also addressable
on their own as `goa help docs` and `goa help options`.
