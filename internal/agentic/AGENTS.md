<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# AGENTS.md

Guidance for AI agents working on this codebase.

## Project Overview

**Name**: agentic (merged into github.com/pijalu/goa/internal/agentic)
**Type**: Go SDK for building AI agents that interact with LLMs and execute tools

Agents run a loop: send conversation history to an LLM provider, stream the response, execute any tool calls, feed results back, and repeat until the LLM responds without tools.

**Tech Stack**:
- Go 1.22
- Single external dependency: `github.com/xeipuuv/gojsonschema` (JSON Schema validation)
- LLM Compatibility: OpenAI-compatible APIs (llama.cpp, LM Studio, Ollama, OpenAI)

---

## Directory Structure

```
internal/agentic/
├── agent.go                 # Agent struct, config, run loop (CORE)
├── provider.go              # LLMProvider interface
├── provider_openai.go       # OpenAI-compatible provider implementation
├── tool.go                  # Tool interface and ToolSchema
├── tool_registry.go         # Tool lookup and schema aggregation
├── message.go               # Message, Role, MessageType types
├── schema.go                # JSON schema validation
├── sse.go                   # Server-Sent Events parser
├── retry.go                 # Exponential backoff retry logic
├── logger.go                # Leveled logger (Error/Warn/Info/Debug)
├── observer.go              # Output observer interface and events
├── session_openai.go        # OpenAI session implementation
├── skillrunner/             # Skill runner package (SKILLS feature)
│   ├── skill.go             # Skill struct and SKILL.md parser
│   ├── loader.go            # Load skills from disk
│   ├── runner.go            # Runner implements Tool interface
│   └── tools/              # Sub-agent tools for skills
│       ├── readfile.go      # read_file tool
│       ├── editfile.go      # edit_file tool
│       ├── runcommand.go    # run_command tool
│       └── restclient.go    # rest_api tool
├── demo/
│   ├── simple/              # Simple calculator demo
│   │   └── main.go
│   ├── skill/               # Skill demo (requires local LLM)
│   │   ├── main.go
│   │   └── skills/
│   │       └── file-resumer/ # File resume skill
│   │           └── SKILL.md
│   ├── api/                  # REST API demo (requires local LLM)
│   │   ├── main.go
│   │   └── skills/
│   │       └── wiki-search/ # Wikipedia search skill
│   │           └── SKILL.md
│   ├── inline-skill/        # Inline skill execution demo
│   │   ├── main.go
│   │   └── skills/
│   │       └── code-reviewer/
│   │           └── SKILL.md
│   ├── stream-xml-demo/     # XML streaming demo
│   │   └── main.go
│   └── shared/              # Shared demo configuration helpers
│       └── config.go
├── helper/                  # Helper utilities
│   ├── bus.go               # Event bus for observers
│   ├── console.go           # Console output observer
│   ├── format.go            # Output formatting
│   └── log.go               # Message log observer
├── observer/                # Output observer implementations
│   └── xmlstream/           # XML streaming observer
│       ├── observer.go      # XML stream observer implementation
│       ├── state.go         # XML state machine
│       ├── escape.go        # XML escaping utilities
│       └── README.md        # Usage documentation
└── go.mod                   # Module: github.com/pijalu/agentic
```

---

## Key Interfaces

### LLMProvider (`provider.go`)
```go
type LLMProvider interface {
    Stream(ctx context.Context, req LLMRequest, out chan<- Message) error
}
```
Implement this to add new LLM backends.

### Tool (`tool.go`)
```go
type Tool interface {
    Schema() ToolSchema
    Execute(input string) (string, error)
}
```
Implement this to add capabilities. Schema is JSON Schema format.

### OutputObserver (`observer.go`)
```go
type OutputObserver interface {
    OnEvent(event Event)
}
```
Implement to receive agent events (content, tool calls, state changes, etc.)

### Skill Runner (`skillrunner/`)
```go
type Config struct {
    Loader      *SkillsLoader   // Required: NewFileSkillsLoader or NewEmbeddedSkillsLoader
    Provider    LLMProvider
    WorkDir     string
    Logger      *Logger
    ParentTools []Tool          // Optional: tools shared with skill sub-agents
}

type SkillsLoader struct { ... }  // Handles filesystem or embedded skill loading

func NewFileSkillsLoader(dirs []string) *SkillsLoader
func NewEmbeddedSkillsLoader(fs embed.FS, basePath string) *SkillsLoader

type ExecutionMode string
const (
    ExecutionModeSubAgent ExecutionMode = "subagent" // default
    ExecutionModeInline   ExecutionMode = "inline"
)

// Agent-level skill execution mode (typed enum)
type SkillExecutionMode string
const (
    SkillExecutionModeSubAgent SkillExecutionMode = "subagent" // default
    SkillExecutionModeInline   SkillExecutionMode = "inline"
)

// Context compression strategy (typed enum)
type CompressionStrategy string
const (
    CompressionToolElision CompressionStrategy = "tool_elision"
    CompressionSelective   CompressionStrategy = "selective"
    CompressionSummarize   CompressionStrategy = "summarize"
    CompressionHybrid      CompressionStrategy = "hybrid"
)

type ContextCompressionConfig struct {
    MaxTokens           int
    ThresholdPercent    int
    OnContextError      bool
    Strategy            CompressionStrategy
    PreserveRecentTurns int
}

type ContextStats struct {
    Messages        int
    Characters      int
    EstimatedTokens int
    MaxTokens       int
    UsagePercent    int
}

type Runner struct { ... }
func NewRunner(cfg Config) (*Runner, error)
func (r *Runner) Schema() ToolSchema
func (r *Runner) Execute(input string) (string, error)
func (r *Runner) GenerateSkillsSection() string
func (r *Runner) GetSkill(name string) *Skill
func (r *Runner) GetAllSkills() []*Skill

// Detect configured context window from provider
func DetectContextWindow(endpoint, model, apiKey string) int

// Convenience constructors for creating agents with skills
func NewAgentWithSkills(cfg agentic.Config, skillsDirs []string, workDir string) (*agentic.Agent, error)
func NewAgentWithSkillsLoader(cfg agentic.Config, loader *SkillsLoader, workDir string) (*agentic.Agent, error)
```
The skill runner loads skills from disk (SKILL.md files) and exposes them via a single `run_skill` tool. When executed, it creates a sub-agent with the skill's instructions and dedicated tools (`read_file`, `edit_file`, `run_command`, `rest_api`).

#### Loading Skills (Filesystem)
```go
loader := skillrunner.NewFileSkillsLoader([]string{"./skills"})
runner, err := skillrunner.NewRunner(skillrunner.Config{
    Loader:   loader,
    Provider: myProvider,
    WorkDir:  ".",
    Logger:   myLogger,
})
```

#### Using Skills (Convenience Constructor)
For simpler setup, use the convenience constructors that handle the skill runner creation:
```go
// With filesystem skills
agent, err := skillrunner.NewAgentWithSkills(
    agentic.Config{
        Provider:     myProvider,
        SystemPrompt: "You are helpful.",
        Logger:       myLogger,
    },
    []string{"./skills"},  // Skills directories
    ".",                  // Work directory
)

// With embedded skills
//go:embed skills
var embeddedSkills embed.FS

agent, err := skillrunner.NewAgentWithSkillsLoader(
    agentic.Config{
        Provider:     myProvider,
        SystemPrompt: "You are helpful.",
    },
    skillrunner.NewEmbeddedSkillsLoader(embeddedSkills, "skills"),
    ".",
)
```

#### Loading Skills (Embedded with go:embed)
```go
//go:embed skills
var embeddedSkills embed.FS

loader := skillrunner.NewEmbeddedSkillsLoader(embeddedSkills, "skills")
runner, err := skillrunner.NewRunner(skillrunner.Config{
    Loader:   loader,
    Provider: myProvider,
    WorkDir:  ".",
})
```

#### Sharing Parent Tools with Skills
By default, skill sub-agents only have the built-in tools. To share tools from the parent agent, pass them via `ParentTools`:
```go
runner, err := skillrunner.NewRunner(skillrunner.Config{
    Loader:      skillrunner.NewFileSkillsLoader([]string{"./skills"}),
    Provider:    myProvider,
    WorkDir:     ".",
    ParentTools: []agentic.Tool{MyCustomTool{}}, // Available to all skill sub-agents
})

agent := agentic.NewAgent(agentic.Config{
    Tools: []agentic.Tool{runner, MyCustomTool{}}, // Runner + shared tools
})
```

#### Using in Agent
```go
agent := agentic.NewAgent(agentic.Config{
    Provider:     myProvider,
    SystemPrompt: "You are helpful.\n" + runner.GenerateSkillsSection(),
    Tools:        []agentic.Tool{runner}, // Add runner as a tool
})
```

### XML Streaming Observer

The `observer/xmlstream` package provides real-time XML output of agent conversations.

```go
import (
    agentic "github.com/pijalu/agentic"
    "github.com/pijalu/agentic/observer/xmlstream"
    "github.com/pijalu/agentic/skillrunner"
)

// Create XML streaming observer
obs, err := xmlstream.NewXMLStreamingObserver(xmlstream.Config{
    Writer:         xmlstream.NewConsoleWriter(os.Stdout),
    Model:          "local-model",
    ConversationID: "my-conv-123",
    IncludeTimings: true,
})

// Share observer with skill runner for sub-agent events
runner, _ := skillrunner.NewRunner(skillrunner.Config{
    Loader:   skillrunner.NewFileSkillsLoader([]string{"./skills"}),
    Provider: myProvider,
    WorkDir:  ".",
    Observer: obs,  // Share with sub-agents
})

agent := agentic.NewAgent(agentic.Config{
    Provider:     myProvider,
    SystemPrompt: "You are helpful.\n" + runner.GenerateSkillsSection(),
    Tools:        []agentic.Tool{runner},
})
agent.AddObserver(obs)

agent.Run(ctx, "Hello!")
obs.Flush()  // Write closing tags
```

**XML Structure:**
```xml
<conversation>
  <metadata>
    <id>my-conv-123</id>
    <model>local-model</model>
    <start>2024-01-01T00:00:00Z</start>
  </metadata>
  <messages>
    <message>
      <role>system</role>
      <blocks>
        <content>System prompt</content>
      </blocks>
    </message>
    <message>
      <role>user</role>
      <blocks>
        <content>User message</content>
      </blocks>
    </message>
    <message>
      <role>assistant</role>
      <blocks>
        <thinking>Reasoning...</thinking>
        <content>Response...</content>
        <stats>...</stats>
      </blocks>
    </message>
  </messages>
</conversation>
```

See `observer/xmlstream/README.md` for full documentation.

## Common Patterns

### Adding a New Provider
1. Implement `LLMProvider` interface
2. Follow `provider_openai.go` as reference:
   - Marshal request body to JSON
   - POST to endpoint
   - Parse SSE chunks
   - Emit `Message` structs for content and tool calls
   - Signal completion with `Message{Type: End}`

### Adding a New Tool
1. Implement `Tool` interface
2. Define schema as `map[string]interface{}` (JSON Schema format)
3. `Execute` receives raw JSON input as string, returns result as string

### Adding a New Observer
1. Implement `OutputObserver` interface
2. Handle event types: `EventStateChange`, `EventContent`, `EventToolCall`, `EventToolResult`, `EventEnd`, `EventTokenStats`, `EventProgress`, `EventContextStats`
3. Register with `agent.AddObserver(observer)`

### Context Compression

The agent supports automatic conversation history compression to manage context window limits. This is useful for any long-running agent, but especially critical when using inline skill execution mode.

#### Basic Configuration

```go
agent := agentic.NewAgent(agentic.Config{
    Provider:     myProvider,
    SystemPrompt: "You are helpful.",
    ContextCompression: agentic.ContextCompressionConfig{
        MaxTokens:           8192,
        ThresholdPercent:    80,
        OnContextError:      true,
        Strategy:            agentic.CompressionHybrid,
        PreserveRecentTurns: 2,
    },
})
```

#### Compression Strategies

Strategies are typed enums (`agentic.CompressionStrategy`):

| Strategy | Behavior | Cost |
|----------|----------|------|
| `CompressionToolElision` | Replace old tool args/results with placeholders | Zero LLM calls |
| `CompressionSelective` | Drop oldest messages, keep system + recent turns | Zero LLM calls |
| `CompressionSummarize` | Use LLM to summarize old messages | One LLM call |
| `CompressionHybrid` | Apply `tool_elision`, then `selective`, then `summarize` as needed | 0-1 LLM calls |

#### Monitoring Context Usage

```go
// One-time query
stats := agent.ContextStats()
fmt.Printf("Using %d%% of context (%d / %d tokens)\n",
    stats.UsagePercent, stats.EstimatedTokens, stats.MaxTokens)

// Real-time monitoring via observer
agent.AddObserver(agentic.OutputObserverFunc(func(event agentic.OutputEvent) {
    if event.Type == agentic.EventContextStats && event.ContextStats != nil {
        fmt.Printf("Context: %d%%\n", event.ContextStats.UsagePercent)
    }
}))
```

#### Manual Compression

Call `Compact()` to manually summarize the entire history:

```go
err := agent.Compact(ctx)
// History is now: [system prompt] + [summary]
```

This is useful when you know a conversation has reached a natural breakpoint.

#### Context Error Recovery

When `OnContextError` is enabled, the agent automatically detects provider errors like "context length exceeded," compresses the history, and retries the current turn without user intervention.

### Adding Skills
Skills are loaded from directories containing `SKILL.md` files.

The convenience constructors (`NewAgentWithSkills`, `NewAgentWithSkillsLoader`) automatically choose the right tool based on `SkillExecutionMode`:
- **Sub-agent mode** (default): registers `run_skill` via `Runner`
- **Inline mode**: registers `learn_skill` via `SkillLearner`

To set up skills manually:

1. Create a skill directory with `SKILL.md` (frontmatter: name, description; optionally input-schema)
2. Create a `skillrunner.Runner` (sub-agent mode) or `skillrunner.SkillLearner` (inline mode)
3. Add it to the agent's `Tools` slice
4. Inject `GenerateSkillsSection()` into the system prompt

The skill sub-agent has access to:
- Built-in tools: `read_file`, `edit_file`, `run_command`, `rest_api`
- Parent tools: any tools passed via `ParentTools` in the Runner config
- Parent skills: by default all parent skills are inherited; use the `skills` header to whitelist specific ones

**SKILL.md format:**
```markdown
---
name: my-skill
description: What this skill does
input-schema: {"type":"object","properties":{"input":{"type":"string"}},"required":["input"]}
---

# Instructions for the skill sub-agent

You are a [role]. Your task is to [goal].
```

**Controlling inheritance:**
- `tools`: JSON array of parent tool names to expose to this skill (empty = all)
- `skills`: JSON array of parent skill names this skill is allowed to call (empty = all). Use this to prevent unintended skill-call loops.

### Skill Execution Modes

Skills support two execution modes set via the typed enum `Config.SkillExecutionMode`:

| Constant | Value | Behavior |
|----------|-------|----------|
| `SkillExecutionModeSubAgent` | `"subagent"` | Default. Each skill runs in an isolated sub-agent with its own conversation history and system prompt. Best for complex, multi-turn skills. |
| `SkillExecutionModeInline` | `"inline"` | Skill instructions are returned to the parent LLM as a tool result. The LLM follows them using the parent agent's tools. Best for simple delegated tasks. Requires context compression. |

#### Configuring Inline Mode

```go
agent, err := skillrunner.NewAgentWithSkills(
    agentic.Config{
        Provider:           myProvider,
        SystemPrompt:       "You are helpful.",
        SkillExecutionMode: agentic.SkillExecutionModeInline,
        ContextCompression: agentic.ContextCompressionConfig{
            MaxTokens:           128000,
            ThresholdPercent:    75,
            OnContextError:      true,
            Strategy:            agentic.CompressionHybrid,
            PreserveRecentTurns: 2,
        },
    },
    []string{"./skills"},
    ".",
)
```

#### Auto-Detecting Context Window

Use `DetectContextWindow` to query the provider and discover the configured context window size. Detection order:

1. **llama.cpp `/props`** → `default_generation_settings.n_ctx` (actual server config, e.g. 32768)
2. **llama.cpp `/v1/models`** → `meta.n_ctx_train` (model training length, e.g. 131072)
3. Other providers → returns 0

```go
maxTokens := agentic.DetectContextWindow(endpoint, model, apiKey)
if maxTokens > 0 {
    cfg.ContextCompression.MaxTokens = maxTokens
}
```

The `/props` endpoint is preferred because it returns the **actual configured context window** (e.g. 32768), not the model's training length (e.g. 131072).

#### Demo CLI Flags

All skill demos support these flags via `demo/shared/config.go`:

| Flag | Default | Env Var | Description |
|------|---------|---------|-------------|
| `-skill-mode` | `subagent` | `AGENTIC_SKILL_MODE` | `subagent` or `inline` |
| `-compression` | `hybrid` | `AGENTIC_COMPRESSION` | `tool_elision`, `selective`, `summarize`, `hybrid` |
| `-max-tokens` | `0` | `AGENTIC_MAX_TOKENS` | Context window size (0 = auto-detect) |
| `-threshold` | `75` | — | Compression trigger percentage |
| `-detect-context` | `true` | — | Auto-detect context window from provider |

Example:
```bash
go run demo/inline-skill/main.go -skill-mode=inline -compression=hybrid -max-tokens=8192
go run demo/skill/main.go -skill-mode=subagent -compression=selective -detect-context=false
```

In inline mode, the convenience constructor registers `learn_skill` (via `SkillLearner`) instead of `run_skill`. The `learn_skill` tool returns skill instructions plus the task, and the LLM follows them by calling the standard skill tools (`read_file`, `edit_file`, `run_command`, `rest_api`) within the same parent session.

#### Mode Comparison

| Aspect | Sub-agent | Inline |
|--------|-----------|--------|
| Latency | Higher (new session per skill) | Lower (same session) |
| Provider connections | One per skill call | One total |
| Context isolation | Full | None |
| Tool availability | Skill defines its own | Parent agent's tools |
| History growth | Bounded per skill | Unbounded, needs compression |
| Best for | Complex multi-turn skills | Simple delegated tasks |

### Using OpenAIProvider

The `OpenAIProvider` is the standard provider for OpenAI-compatible APIs.

**Constructor (Recommended)**:
```go
provider := agentic.NewOpenAIProvider(
    "http://localhost:1234/v1/chat/completions",
    "local-model",
)
provider.Logger = agentic.NewLogger(agentic.Debug)
```

The constructor automatically initializes a default `http.Client`.

**Struct Initialization (Alternative)**:
```go
provider := &agentic.OpenAIProvider{
    Endpoint: "http://localhost:1234/v1/chat/completions",
    Model:    "local-model",
    Client:   &http.Client{},  // Required if not using constructor
    Logger:   agentic.NewLogger(agentic.Debug),
}
```

---

## Common Patterns

## Build & Test Commands

```bash
make build              # Build all packages and demos
make test              # Run all tests
make test-verbose      # Verbose test output
make run-simple        # Run simple demo (requires LLM at localhost:1234)
make run-skill         # Run skill demo (requires LLM at localhost:1234)
make run-api           # Run API demo (requires LLM at localhost:1234)
make run-inline-skill  # Run inline skill demo (requires LLM at localhost:1234)
```

**Note**: Demos require a local LLM endpoint at `http://localhost:1234/v1/chat/completions` (e.g., LM Studio, Ollama, llama.cpp server).

---

## Naming Conventions

| Element | Convention | Examples |
|---------|------------|----------|
| Exported types/functions | `PascalCase` | `Agent`, `NewAgent`, `System` |
| Private fields/variables | `camelCase` | `cfg`, `history`, `err` |
| Multi-word files | `snake_case.go` | `tool_registry.go`, `provider_openai.go` |
| Test files | `{source}_test.go` | `agentic_test.go`, `provider_openai_test.go` |
| Constants (iota) | `PascalCase` | `Error`, `Warn`, `Info`, `Debug` |

---

## File Organization Pattern

```go
package agentic

// 1. Imports (standard lib → external)
import (
    "context"
    "encoding/json"
    "github.com/xeipuuv/gojsonschema"
)

// 2. Constants
const MaxRetries = 3

// 3. Type definitions
type Role string

// 4. Struct definitions
type Agent struct {
    cfg     Config
    reg     *ToolRegistry
    Output  chan Message
}

// 5. Constructor functions
func NewAgent(cfg Config) *Agent { ... }

// 6. Methods
func (a *Agent) Run(ctx context.Context, input string) error { ... }
```

---

## Key Implementation Details

### Channel-Based Communication
- `Provider.Stream()` writes `Message` structs to output channel
- `Agent.Output` exposes responses to caller
- Use accumulators for streaming tool call arguments

### Safe JSON Parsing
- `parseChunk()` handles missing fields and type mismatches gracefully
- Returns `nil` for invalid JSON instead of error

### llama.cpp Compatibility
- Handles `finish_reason: "tool_calls"` (llama.cpp uses this instead of "stop")
- Tool messages include `tool_call_id` for API compatibility
- Handles streaming tool arguments across multiple SSE chunks

### Error Handling
- Wrap errors with `fmt.Errorf("context: %w", err)`
- Log errors and continue (graceful degradation)
- Use `Retry()` function for tool execution with exponential backoff
- Guard against nil Logger: `if l != nil && ...`

---

## Testing Patterns

### Test Function Naming
```
TestFunction_Scenario or TestType_Method_Scenario
```

### Subtests with t.Run()
```go
func TestHandleFinishReason(t *testing.T) {
    tests := []struct {
        name   string
        reason string
        want   MessageType
    }{ ... }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) { ... })
    }
}
```

### Mock Tools (private in test files)
```go
type mockTool struct {
    name   string
    schema ToolSchema
}
func (m mockTool) Schema() ToolSchema { return m.schema }
func (m mockTool) Execute(input string) (string, error) { return "mock result", nil }
```

---

## Quick Reference

| Need to... | Look at |
|------------|---------|
| Understand agent loop | `agent.go` - `Agent.Run()` |
| Add new LLM backend | `provider.go` + `provider_openai.go` |
| Add new tool | `tool.go` - implement `Tool` interface |
| Add event observer | `observer.go` - implement `OutputObserver` |
| Parse LLM response | `provider_openai.go` - `parseChunk()` |
| Validate tool input | `schema.go` - `Validate()` |
| Handle streaming | `sse.go` - `ParseSSE()` |
| Retry failed tool | `retry.go` - `Retry()` |
| Log messages | `logger.go` - `Logger` type |
| Use skills | `skillrunner/` - `Runner` implements `Tool` |
| Load skills | `skillrunner/loader.go` - `LoadSkills()` |
| Use inline skill mode | `skillrunner/runner.go` - `ExecutionModeInline` |
| Add context compression | `agent.go` - `ContextCompressionConfig` |
| Monitor context usage | `agent.go` - `Agent.ContextStats()` |
| Auto-detect context window | `provider_openai.go` - `DetectContextWindow()` |
| Run simple demo | `demo/simple/main.go` |
| Run skill demo | `demo/skill/main.go` (requires local LLM) |
| Run inline skill demo | `demo/inline-skill/main.go` (requires local LLM) |
| Stream XML output | `observer/xmlstream/observer.go` - `XMLStreamingObserver` |
| Run XML stream demo | `demo/stream-xml-demo/main.go` (requires local LLM) |

---

## Important Constraints

- **Single package project** - all code is in `package agentic` (except `helper/` and `skillrunner/` sub-packages)
- **Minimal dependencies** - only `gojsonschema` for validation
- **No getters/setters** - Go idiom is direct field access
- **No panic in library code** - return errors instead
- **Always close response bodies** - `defer resp.Body.Close()`

---

## Common Tasks

### Run Tests
```bash
go test ./... -count=1
```

### Add a New Tool
1. Create struct implementing `Tool` interface
2. Define `Schema()` returning `ToolSchema` with JSON Schema
3. Implement `Execute(input string) (string, error)`
4. Add to agent's `Tools` slice in config

### Add a New Provider
1. Implement `LLMProvider` interface
2. Handle request marshaling, HTTP POST, SSE parsing
3. Emit `Message` types: `Content`, `ToolCall`, `End`

### Debug Streaming Issues
1. Check `sse.go` - `ParseSSE()` function
2. Check `provider_openai.go` - `parseChunk()` and accumulators
3. Enable debug logging: `Logger: agentic.NewLogger(agentic.Debug)`