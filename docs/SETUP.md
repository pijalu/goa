<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Setup Guide

## Prerequisites

- **Go 1.25+** — check with `go version`
- **gocognit** (for complexity checks): `go install github.com/uudashr/gocognit/cmd/gocognit@latest`
- **gocyclo** (for cyclomatic complexity): `go install github.com/fzipp/gocyclo/cmd/gocyclo@latest`

## Installation

```bash
# Clone the repository
git clone <repo-url> goa
cd goa

# Build the binary
make build

# (Optional) Install to GOPATH/bin
make install

# Verify
./goa --help
```

## First Run

When you run `./goa` for the first time, the **Setup Wizard** launches interactively:

```
⟡  First run detected — launching setup wizard

                      ┌─ Welcome to Goa ─┐
                      │                   │
                      │ Let's get you     │
                      │ set up.           │
                      │                   │
                      │ [ Next → ]        │
                      └───────────────────┘
```

The wizard guides you through these steps:

1. **Welcome** — Project overview, what to expect
2. **Provider** — Configure your LLM provider
   - Endpoint URL (e.g., `http://localhost:1234/v1/chat/completions`)
   - Model name (e.g., `gpt-4o`, `llama-3.2-1b-instruct`)
   - API key (optional, for OpenAI/managed services)
3. **WebFetch Summarization** — Enable sub-agent summarization for fetched pages
4. **Memory Dreams** — Enable automatic memory consolidation
   - `Yes` — Periodically merge `.goa/memory/*.md` into a single cleaned file
   - `No` — Keep memories as individual files only
5. **Companion Model** — Use the same model for the companion agent or configure separately
6. **Mode** — Choose a major mode
   - `coder` — Implements code changes
   - `planner` — Decomposes and plans work
   - `reviewer` — Reviews and provides feedback
7. **Autonomy** — Choose execution mode
   - `yolo` — Auto-approve all tool calls
   - `confirm` — Pause before each tool call
   - `review` — Queue writes for batch approval
8. **Advanced Options** — Compression, tool limits, fuzzy edits
9. **Prompts & Workflows** — Copy built-in prompts/workflows to `.goa/`
10. **Done** — Summary and save

Configuration is saved to `~/.goa/config.yaml`. You can edit it directly at any time.

## Manual Configuration

If you prefer to skip the wizard, create `~/.goa/config.yaml` manually:

```yaml
active_provider: local
active_model: llama-3.2-1b-instruct

mode:
  default:
    major: coder
    autonomy: solo

execution:
  mode: yolo
  retries: 3
  token_warning: 70
  token_critical: 85
  loop_warning: 5
  loop_interrupt: 10

providers:
  - id: local
    name: Local LLM
    endpoint: http://localhost:1234/v1/chat/completions
    default_model: llama-3.2-1b-instruct
    preferred: true
  - id: openai
    name: OpenAI
    endpoint: https://api.openai.com/v1/chat/completions
    api_key: ${OPENAI_API_KEY}
    default_model: gpt-4o
    preferred: false

tui:
  theme: dark
  layout: default
```

## Running

```bash
# Start the TUI
./goa

# Start with a specific model
./goa --model gpt-4o

# Start with a specific mode
./goa --profile planner

# Start with a specific config
./goa --config ~/.goa/custom-config.yaml
```

### Web UI (`goa server`)

`goa server` runs the same interactive session against a virtual terminal and
serves the screen to browsers. Nothing else changes: same engine, same agent,
same commands, same overlays.

```bash
# Serve on loopback (default 127.0.0.1:8080), no credentials
./goa server

# Serve beyond this machine: credentials are mandatory off-loopback
export GOA_SERVER_AUTH_TOKEN=$(openssl rand -hex 32)
./goa server --server-addr 0.0.0.0:7331 --server-auth=token

# Basic auth instead of a token
./goa server --server-auth=basic --server-auth-user you   # password: GOA_SERVER_AUTH_PASSWORD

# Screen sharing: browsers see the session, keystrokes and uploads are refused
./goa server --server-read-only
```

* Startup refuses a non-loopback bind without credentials; `--insecure-no-auth`
  is the explicit, typed opt-out.
* Opening `http://127.0.0.1:8080/` redirects to the live session
  (`/s/<id>`); `/s/<id>/text` is a plain-text mirror of the screen.
* The same flags apply over SSH port-forwarding, so a remote box stays
  loopback-only and you still reach it from your laptop:
  `ssh -L 8080:127.0.0.1:8080 host`.

See [WEBUI.md](WEBUI.md) for the security model, transports and the full URL
map.

## Config File Locations

Goa searches for config in this order (later overrides earlier):

| Priority | Location | Description |
|----------|----------|-------------|
| 1 | Embedded | Compiled-in defaults (`config/defaults.go`) |
| 2 | `~/.goa/config.yaml` | Home/global config |
| 3 | `.goa/config.yaml` | Project-level config |
| 4 | `.goa/config.local.yaml` | Local overrides (gitignored) |
| 5 | `GOA_*` env vars | Environment overrides |
| 6 | CLI flags (`--model`, etc.) | Highest priority |

See [CONFIGURATION.md](CONFIGURATION.md) for all available settings.
