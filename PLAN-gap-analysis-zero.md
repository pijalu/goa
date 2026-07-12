# Zero → Goa Key Gap Analysis

**Date:** 2026-07-11  
**Source:** `/Users/muaddib/dev/zero` (github.com/Gitlawb/zero)  
**Target:** `/Users/muaddib/dev/goa` (github.com/pijalu/goa)

---

## 1. Sandbox / Execution Security (HIGH VALUE)

**What Zero has that Goa doesn't:**

- **`internal/sandbox/`** — Platform-native sandbox backends:
  - **macOS:** Seatbelt (App Sandbox profiles via `sandbox-exec`)
  - **Linux:** bubblewrap (user-namespace based), Landlock LSM, seccomp-bpf filters
  - **Windows:** Restricted tokens, integrity levels, AppContainer ACLs
  - **WSL:** bwrap fallback detection
- **AST-based command analyzer** (`analyzer.go`): Parses shell scripts with `mvdan.cc/sh/v3` to statically detect destructive (`dd`, `shred`, `mkfs`), network (`curl`, `wget`, `ssh`), and interactive commands — more precise than regex.
- **Permission profiles:** `PermissionProfile` type with grant scopes, `Engine` struct enforcing sandbox policies per tool call.
- **Secure file I/O:** `securefile` package for atomic, permission-safe writes.

**Why it matters for Goa:** Goa currently has `internal/sandbox` but appears minimal (1 file). Adding static shell analysis and native sandbox adapters would prevent destructive commands and provide OS-level confinement without relying on the model to self-police.

**Suggested priority:** Medium (foundational safety feature, large surface area)

---

## 2. Background Process Manager (HIGH VALUE)

**What Zero has: `internal/background/manager.go`**

- Persistent registry of background tasks (`Manager` struct with `map[string]Task`)
- Task lifecycle: `running` → `completed` / `error` / `killed`
- PID tracking, output file capture, exit code recording
- Cross-platform process termination (`process_posix.go`, `process_windows.go`)
- Query and warning accumulation

**Why it matters:** Goa has `ptymgr` and `bgexec` but not a durable background task registry. Zero's `Manager` is persistent (JSON on disk), survives restarts, and integrates with the TUI sidebar for live status. Goa's background tool (`bgexec.go`) creates one-shot tasks without durable tracking.

**Suggested priority:** Medium (rich UX improvement, moderate surface)

---

## 3. Swarm / Multi-Agent Orchestration (HIGH VALUE)

**What Zero has: `internal/swarm/`** (24 files, ~140KB)

- **Coordinator**: Tracks task lifecycle (`pending`/`running`/`done`/`failed`/`handed-off`) with lock-based state machine
- **Scheduler**: Priority-based task dispatch to available team members
- **Mailbox**: Agent-to-agent message passing (typed envelopes)
- **Team**: Member registration, capability matching
- **Lifecycle**: Start → join → collect results → handoff protocol
- **Lock**: Cross-process file lock for shared state
- **Schedule tool**: Agent-driven tool call to schedule specialist tasks
- **Deferred results**: Non-blocking task submission with later collection

**Why it matters:** Goa has `multiagent/` but it's simpler — a pool of sub-agents with basic orchestration. Zero's swarm is production-grade with scheduler, mailbox IPC, lifecycle management, and handoff protocol. The mailbox pattern alone would let Goa agents communicate instead of just being orchestrated.

**Suggested priority:** High (core differentiator, large surface but well-factored)

---

## 4. Daemon & Remote Bridge (HIGH VALUE)

**What Zero has: `internal/daemon/`** (25 files, plus `internal/daemon/remote/`)

- **Server**: Unix socket listener (no TCP binding), single-instance lock, connection tracking
- **Session manager**: Durable session pool over control protocol
- **Remote bridge**: TLS-authenticated connections from external clients
- **Git bundle upload**: Remote peers send git bundles instead of full repos
- **Pool**: Connection pooling with backpressure
- **Protocol**: Framed JSON control protocol (`protocol.go`)

**Why it matters:** Goa has no daemon mode. A daemon would enable persistent model connections (avoid cold-start on every message), shared context across terminal sessions, remote agent access (CI/CD, mobile), and the `zero exec` headless pattern without starting a new agent each time.

**Suggested priority:** High (architectural enabler, large surface)

---

## 5. LSP Integration (HIGH VALUE)

**What Zero has: `internal/lsp/`** (13 files)

- **LSP Client** (`client.go`): Full JSON-RPC 2.0 with Content-Length headers, concurrent request tracking, notification handler
- **LSP Server** (`server.go`): Manages gopls subprocess lifecycle
- **Diagnostics** (`diagnostics.go`): Gathers compiler errors/warnings per file
- **Navigation** (`navigate.go`): Go to definition, find references, hover
- **Documents** (`documents.go`): Open/close/change document tracking
- **Registry** (`registry.go`): Multi-language server lifecycle manager
- **Manager** (`manager.go`): Integrates diagnostics with file write pipeline

**Why it matters:** Goa has no LSP integration. LSP diagnostics would let Goa's edit tools surface errors immediately (like Zero's `inline_diagnostics.go`). Navigation tools would give the model precise code understanding (definitions, references, type info) without regex or fuzzy search.

**Suggested priority:** High (direct model improvement, moderate surface)

---

## 6. OAuth & Credential Management (MEDIUM VALUE)

**What Zero has: `internal/oauth/`** (13 files), `internal/credstore/`, `internal/keyring/`, `internal/securefile/`

- **OAuth flows**: Authorization code + PKCE, device code, loopback (localhost redirect)
- **Token management**: Encrypted disk store (`encrypt.go`), auto-refresh with singleflight, scheduler
- **Provider presets**: Pre-configured OAuth endpoints for OpenRouter, ChatGPT, etc.
- **Keyring integration**: OS keychain via `keyring` package
- **Credential store**: Cross-session credential persistence with validation

**Why it matters:** Goa currently relies on env vars for API keys. OAuth support would let users authenticate via browser flow (no copy-paste of keys), with encrypted token storage and auto-refresh. Essential for CI/CD and non-interactive use.

**Suggested priority:** Medium (UX improvement, moderate surface)

---

## 7. Durable Sessions (MEDIUM VALUE)

**What Zero has: `internal/sessions/`** (15 files)

- **File-based session store** (`store.go`): Append-only event log, SQLite-backed
- **Checkpoint/rewind** (`checkpoint.go`, `rewind.go`): Save/restore conversation state
- **Replay** (`replay.go`): Deterministic replay of session events for debugging
- **Lineage** (`lineage.go`): Session parent/child tracking for fork/resume
- **Sequence durability** (`sequence_durability_test.go`): Crash-safe append semantics

**Why it matters:** Goa has state tracking but not durable session persistence with checkpoint/rewind. Zero's session store survives crashes, enables resume across terminal sessions, and supports forking conversations.

**Suggested priority:** Medium (architectural feature, moderate surface)

---

## 8. Hooks / Lifecycle Event System (MEDIUM VALUE)

**What Zero has: `internal/hooks/`** (3 files)

- **Lifecycle events**: `beforeTool`, `afterTool`, `sessionStart`, `sessionEnd`
- **Dispatch engine**: Fire hooks with JSON payload (tool name, call ID, input/output) written to command's stdin
- **Blocking hooks**: `beforeTool` hooks can veto tool execution by exiting non-zero
- **Audit store**: Records every hook execution for replay
- **Cascading config**: User-level (`~/.config/zero/hooks.json`) + project-level (`.zero/hooks.json`)

**Why it matters:** Goa has no hook system. Hooks enable security policies (reject `rm -rf`), custom logging, CI pipeline integration, and user-defined tool validators — all without modifying Goa itself.

**Suggested priority:** Medium (extensibility feature, moderate surface)

---

## 9. Plugin System (MEDIUM VALUE)

**What Zero has: `internal/plugins/`** (3 files)

- **Git-based distribution**: `plugins install <git-url>` clones plugin manifests
- **Manifest validation**: Required fields (id, name, version, tools)
- **Lockfile**: Content-hash tracking (`plugins.lock`) for integrity
- **Activation**: Permission-gated activation flow
- **Plugin-scoped skills**: Plugins can bundle skills in their tree

**Why it matters:** Goa has built-in skills but no plugin mechanism. A plugin system lets the community distribute tools, MCP servers, and custom renderers without merging into core.

**Suggested priority:** Medium (ecosystem feature, moderate surface)

---

## 10. Self-Verify / Test Remediation Loop (MEDIUM VALUE)

**What Zero has: `internal/verify/` + `internal/selfverify/` + `internal/testrunner/`**

- **Test runner** (`testrunner.go`): Discovers test frameworks (Go test, pytest, jest, etc.)
- **Verify** (`verify.go`): Runs checks against a plan, produces structured reports with output summaries
- **Self-verify** (`selfverify.go`): Loop of run-tests → analyze failures → remediate → rerun, with configurable max attempts
- **Remediator** pattern: Injects agent-driven fix attempts between verification passes

**Why it matters:** Goa has no automated verification. Self-verify would let Goa agents autonomously fix test failures: run tests, capture output, surface errors to the model, apply fixes, and re-run until green.

**Suggested priority:** Medium (agent loop improvement, moderate surface)

---

## 11. Shell Command Analysis & Safety (MEDIUM VALUE)

**What Zero has in `internal/sandbox/analyzer.go` and `internal/tools/`:**

- `AnalyzeCommand()` — AST-based shell analysis using `mvdan.cc/sh/v3`
- Detects destructive, network, interactive programs
- Safe command filter (`safe_command.go`): Regex + AST hybrid for permission decisions
- Command prefix blocking (`command_prefix.go`)
- Shell runtime for permission-aware execution

**Why it matters:** Goa's `bash_jail.go` has basic restrictions. Zero's AST analysis catches obfuscated commands and provides richer safety signals (`interactive`, `destructive`, `network`, `tooComplex`).

**Suggested priority:** Medium (safety improvement, small surface)

---

## 12. Secrets Scanner & Redaction (MEDIUM VALUE)

**What Zero has: `internal/secrets/` + `internal/redaction/`**

- **Secret scanner** (`scanner.go`): Pattern-based detection of API keys (AWS, GitHub, OpenAI, Slack, Google), private keys, JWTs
- **Redaction engine** (`redaction.go`): Scans tool outputs and strips configured credentials + scanned secrets before sending to model
- **Audit fixes**: Redaction boundary testing against credential exfiltration

**Why it matters:** Goa has no secret scanning. Without it, commands or diffs that accidentally print API keys get sent to the model — a security risk and potential data leak.

**Suggested priority:** Medium (security, small surface)

---

## 13. Worktree / Isolated Execution (MEDIUM VALUE)

**What Zero has: `internal/worktrees/`**

- **Git worktree creation**: `git worktree add` — isolates file operations for `zero exec --worktree`
- **Dedup logic**: Reuses existing worktrees when possible
- **Cleanup**: Worktree lifecycle management (abandon on error, prune on success)

**Why it matters:** Goa has `gitworktree.go` but it's basic. Zero's worktree system enables safe parallel agent runs in isolated file contexts — critical for CI usage and safe experimentation.

**Suggested priority:** Low (niche but important for CI)

---

## 14. Dictation / Speech-to-Text (LOW VALUE)

**What Zero has: `internal/dictation/`** (10 files)

- **Batch transcription**: Local (sherpa-onnx), Groq, OpenAI APIs
- **Streaming transcription**: Deepgram, OpenAI realtime, local streaming
- **Recorder**: Audio capture via platform tools (arecord, sox, ffmpeg)
- **Format detection**: WAV/M4A sniffing from magic bytes
- **TUI integration**: Dictation button in composer, live transcript streaming

**Why it matters:** Goa doesn't have voice input. Could be a nice accessibility feature but not core to the coding agent mission.

**Suggested priority:** Low (nice-to-have, small surface)

---

## 15. Notifications (LOW VALUE)

**What Zero has: `internal/notify/`** (3 files)

- Terminal bell (BEL), desktop notification (OSC-9), or both
- Focus-aware gating (only when terminal not focused)
- Slack + generic webhook sinks for remote notification
- Turn completion and awaiting-input events

**Why it matters:** Goa has no notification system. Users running long tasks in background would benefit from desktop notifications on completion.

**Suggested priority:** Low (nice-to-have, small surface)

---

## 16. Cron / Loop Scheduling (LOW VALUE)

**What Zero has: `internal/cron/`** (3 files)

- `loopprompt.go` — Resolves loop.md from project/home config
- `append_run.go` — Schedule management
- Lock reclaim for stale job recovery

**Why it matters:** Goa has `/loop` command but no persistent scheduler. Zero's cron would allow scheduled periodic tasks.

**Suggested priority:** Low (nice-to-have, small surface)

---

## 17. ACP Protocol (MEDIUM VALUE)

**What Zero has: `internal/acp/`** (7 files)

- **ACP Agent**: Full JSON-RPC server implementing the Agent Communication Protocol
- **Methods**: `initialize`, `session/new`, `session/load`, `session/prompt`, `session/cancel`, `session/set_mode`, `session/set_config_option`
- **Notifications**: `session/update` — streams tool calls, thoughts, user messages, plan updates, available commands
- **File operations**: `fs/read_text_file`, `fs/write_text_file` — delegates file I/O to the client editor
- **Capabilities negotiation**: Client and server declare supported features
- **Permission delegation**: `session/request_permission` — pushes permission requests to the editor

**Why it matters:** Goa has an MCP client but no ACP server. ACP is the protocol Cursor uses for agent-mode. An ACP server would let Goa serve as the backend for Cursor, VS Code, or any ACP-compatible editor — dramatically expanding Goa's reach.

**Suggested priority:** Medium (ecosystem enabler, moderate surface)

---

## 18. Provider Health & Diagnostics (LOW VALUE)

**What Zero has: `internal/providerhealth/`** (1 file, ~800 lines)

- Configuration validation
- Connectivity probing (bounded, non-generating request)
- Category-based status (config/auth/rate_limit/network/timeout/provider_error)
- Redaction-aware output

**Why it matters:** Goa has `doctor` command but no structured provider health checking. This would improve the setup wizard and troubleshooting.

**Suggested priority:** Low (UX polish, moderate surface)

---

## 19. Model Registry & Reasoning Capabilities (LOW VALUE)

**What Zero has: `internal/modelregistry/` + `internal/reasoning/`**

- **Model catalog**: Cost tracking, context limits, feature flags
- **Capability catalog**: Community-maintained reasoning effort/budget data
- **Provider model discovery** (`providermodeldiscovery/`): Probe provider for available models
- **Provider model catalog** (`providermodelcatalog/`): Filter logic, local + remote sources

**Why it matters:** Goa has basic model configuration. Zero's structured reasoning capabilities catalog would let Goa properly configure model-specific reasoning controls (effort vs budget vs toggle).

**Suggested priority:** Low (UX polish, moderate surface)

---

## 20. Release & Update Pipeline (LOW VALUE)

**What Zero has: `internal/release/` + `internal/update/` + `internal/selfverify/`**

- **Release build**: Cross-platform archive/checksum generation
- **Update**: GitHub release check, atomic binary replacement, npm wrapper update
- **Self-verify**: `zero verify` — checks binary integrity against stored signature
- **npm packaging**: Wrapper downloads platform binary from GitHub Releases

**Why it matters:** Goa has no release pipeline. Would be needed for distribution but not core functionality.

**Suggested priority:** Low (distribution, not agent features)

---

## Top Recommendations for Goa

### Tier 1 (Highest Impact / Effort Ratio)

1. **Sandbox shell analysis** — Add AST-based command analyzer to Goa's existing sandbox. ~1-2 days. Directly improves safety with minimal code.
2. **Secrets scanner & redaction** — Add pattern-based secret detection. ~2-3 days. Security improvement, independently useful.
3. **LSP integration** — Add LSP client for diagnostics feedback on file writes. ~1 week. Dramatically improves model accuracy.
4. **Self-verify loop** — Add test runner + remediation loop. ~1 week. Enables autonomous fix-attempt workflows.

### Tier 2 (Architectural Enablers)

5. **Hooks system** — Add lifecycle event dispatch. ~3-5 days. Enables extensibility without core changes.
6. **Background process manager** — Convert Goa's bgexec into durable task registry. ~3-5 days.
7. **ACP protocol** — Add ACP server for editor integration. ~1-2 weeks. Major ecosystem reach.

### Tier 3 (Rich Features)

8. **Swarm mailbox** — Add agent-to-agent messaging to Goa's multiagent system. ~1 week.
9. **OAuth flow** — Add browser-based auth flow for providers. ~1 week.
10. **Plugin system** — Git-based plugin distribution with manifest validation. ~1-2 weeks.
