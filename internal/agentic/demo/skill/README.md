<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Skill Demo - Agentic SDK

A demonstration of the **Skills System** in the Agentic SDK. This demo shows how to extend an AI agent's capabilities by loading custom skills from disk and exposing them via a single `run_skill` tool.

## Table of Contents

- [Overview](#overview)
- [How It Works](#how-it-works)
- [Prerequisites](#prerequisites)
- [Getting Started](#getting-started)
- [Understanding Skills](#understanding-skills)
- [SKILL.md Format](#skillmd-format)
- [Available Skills](#available-skills)
- [Code Walkthrough](#code-walkthrough)
- [Running the Demo](#running-the-demo)
- [Expected Output](#expected-output)
- [Creating New Skills](#creating-new-skills)
- [Skill Runner API](#skill-runner-api)
- [Troubleshooting](#troubleshooting)
- [Advanced Usage](#advanced-usage)

---

## Overview

The Skill Demo showcases a powerful feature of the Agentic SDK: **skill-based agent extension**. Instead of hardcoding tools into an agent, skills allow you to:

- Define reusable agent behaviors in markdown files (`SKILL.md`)
- Load skills dynamically from directories at runtime
- Expose all skills through a single `run_skill` tool
- Have the main agent delegate complex tasks to specialized sub-agents

This approach enables a **modular, plugin-like architecture** where you can add new capabilities to your agent without modifying code.

### Key Concepts

| Concept | Description |
|---------|-------------|
| **Skill** | A packaged capability with instructions and optional tools |
| **SKILL.md** | Markdown file containing skill metadata (frontmatter) and instructions |
| **Skill Runner** | Component that loads skills and implements the `run_skill` tool |
| **Sub-Agent** | Temporary agent created to execute a skill with specialized instructions |
| **Tool Set** | Skills have access to `read_file`, `edit_file`, and `run_command` tools |

---

## How It Works

```
┌─────────────────────────────────────────────────────────────────┐
│                        Main Agent                               │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │  System Prompt with Skills Section                       │   │
│  │  "You are helpful. Available skills: file-resumer..."   │    │
│  └─────────────────────────────────────────────────────────┘    │
│                          │                                      │
│                          │ Calls "run_skill" tool               │
│                          ▼                                      │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │  Skill Runner (implements Tool interface)               │    │
│  │  - Loads skills from ./skills/ directory               │     │
│  │  - Generates skills section for system prompt          │     │
│  │  - Executes skills by creating sub-agents              │     │
│  └─────────────────────────────────────────────────────────┘    │
│                          │                                      │
│                          │ Creates sub-agent with skill         │
│                          ▼                                      │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │  Sub-Agent (temporary, for skill execution)             │    │
│  │  - System prompt = skill instructions from SKILL.md     │    │
│  │  - Tools: read_file, edit_file, run_command             │    │
│  │  - Runs task, captures result                           │    │
│  └─────────────────────────────────────────────────────────┘    │
│                          │                                      │
│                          │ Returns result                       │
│                          ▼                                      │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │  Main Agent receives skill output                       │    │
│  └─────────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────────┘
```

### Execution Flow

1. **Initialization**: The `skillrunner.NewRunner()` loads all `SKILL.md` files from specified directories
2. **System Prompt Injection**: `runner.GenerateSkillsSection()` creates a formatted list of available skills
3. **Agent Creation**: The main agent is created with only the `run_skill` tool (provided by the runner)
4. **Task Execution**: When the main agent decides to use a skill, it calls `run_skill` with `skill_name` and `task`
5. **Sub-Agent Creation**: The runner creates a temporary sub-agent with the skill's instructions as its system prompt
6. **Skill Execution**: The sub-agent runs with access to file/command tools, executing the skill's instructions
7. **Result Capture**: The sub-agent's output is captured and returned to the main agent

---

## Prerequisites

### Required Software

- **Go 1.22+** - [Install Go](https://go.dev/doc/install)
- **Local LLM Endpoint** - One of the following:
  - [LM Studio](https://lmstudio.ai/) (recommended for beginners)
  - [Ollama](https://ollama.ai/) with OpenAI-compatible API
  - [llama.cpp](https://github.com/ggerganov/llama.cpp) server mode
  - OpenAI API (if you have an API key)

### LLM Model Requirements

The demo expects a local LLM running at `http://localhost:1234/v1/chat/completions`. The model should:
- Support tool/function calling
- Be capable of following instructions
- Have sufficient context for the task

**Recommended models for testing:**
- `llama-3.2-1b-instruct` (fast, good for simple tasks)
- `llama-3.1-8b-instruct` (better reasoning)
- `mistral-7b-instruct` (good balance)

---

## Getting Started

### 1. Clone the Repository

```bash
git clone https://github.com/pijalu/agentic.git
cd agentic
```

### 2. Install Dependencies

```bash
go mod download
```

### 3. Start Your LLM Endpoint

#### Using LM Studio:
1. Download and install LM Studio
2. Load a compatible model (e.g., Llama 3.2 1B Instruct)
3. Click the "Local Server" tab
4. Start the server (default: `http://localhost:1234/v1/chat/completions`)

#### Using Ollama:
```bash
ollama serve
# In another terminal:
ollama pull llama3.2:1b
# Ollama's OpenAI-compatible endpoint is at http://localhost:11434/v1
# You'll need to update main.go to use this endpoint
```

### 4. Update Model Configuration (if needed)

Edit `demo/skill/main.go` to match your setup:

```go
provider := agentic.NewOpenAIProvider(
    "http://localhost:1234/v1/chat/completions",  // Your endpoint
    "llama-3.2-1b-instruct",                      // Your model name
)
```

### 5. Run the Demo

```bash
cd demo/skill
go run main.go
```

---

## Understanding Skills

### What is a Skill?

A **skill** is a self-contained capability that an AI agent can use to perform specific tasks. Skills are defined in `SKILL.md` files and contain:

1. **Frontmatter** (YAML-like metadata between `---` lines):
   - `name`: Unique identifier for the skill
   - `description`: What the skill does (shown to the main agent)
   - `input-schema` (optional): JSON Schema for validating skill input

2. **Instructions** (markdown content after frontmatter):
   - System prompt for the sub-agent
   - Step-by-step instructions
   - Examples and guidelines

### Skill Directory Structure

```
skills/
└── file-resumer/           # Skill directory (name doesn't matter)
    └── SKILL.md            # Skill definition file
```

You can have multiple skills in multiple directories:

```
skills/
├── file-resumer/
│   └── SKILL.md
├── code-analyzer/
│   └── SKILL.md
└── web-scraper/
    └── SKILL.md
```

---

## SKILL.md Format

### Basic Structure

```markdown
---
name: my-skill
description: Brief description of what this skill does
---

# My Skill Instructions

You are a specialized assistant for [specific task].

## Steps
1. First, do this
2. Then, do that
3. Return the result

## Notes
- Important guideline 1
- Important guideline 2
```

### With Input Schema

```markdown
---
name: file-resumer
description: Summarizes the content of a given file
input-schema: {"type": "object", "properties": {"file_path": {"type": "string"}}, "required": ["file_path"]}
---

# File Resumer Skill

You are a file summarization assistant...

## Input Format
You will receive the input as a JSON object with a `file_path` field.
```

### Frontmatter Fields

| Field | Required | Description |
|-------|----------|-------------|
| `name` | Yes | Unique skill identifier (used in `run_skill` calls) |
| `description` | Yes | Human and AI-readable description of the skill's purpose |
| `input-schema` | No | JSON Schema object for input validation/parsing |
| `tools` | No | JSON array of parent tool names to expose to this skill (empty = all) |
| `skills` | No | JSON array of parent skill names this skill is allowed to call (empty = all) |

### Controlling Skill Inheritance

By default, a skill's sub-agent inherits **all** parent skills (except itself). This can lead to unintended skill-call loops if skills invoke each other recursively. Use the `skills` frontmatter field to whitelist only the parent skills a skill should be allowed to call:

```markdown
---
name: my-skill
description: A skill that only needs wiki-search
skills: ["wiki-search"]
---
```

When `skills` is specified, the sub-agent only receives access to those named skills (plus its own sub-skills). If omitted or empty, all parent skills are inherited (backward-compatible default). The skill always excludes itself, preventing direct recursion even if listed.

### Instruction Guidelines

When writing skill instructions:

1. **Be explicit**: Clearly state what the skill should do
2. **Provide examples**: Show expected input/output formats
3. **Define steps**: Break complex tasks into numbered steps
4. **Specify tools**: Mention which tools (`read_file`, `edit_file`, `run_command`) to use
5. **Set constraints**: Define what the skill should and shouldn't do
6. **Control inheritance**: Use `skills` to whitelist parent skills and prevent loops

---

## Available Skills

### file-resumer

**Location**: `skills/file-resumer/SKILL.md`

**Description**: Summarizes (resumes) the content of a given file. Use when you need a concise summary of a file's content.

**Input**: File path (plain string or JSON with `file_path` field)

**Example Usage**:
```json
{
  "skill_name": "file-resumer",
  "task": "README.md"
}
```

**Sub-Agent Tools**: `read_file`

**How it works**:
1. Receives a file path as input
2. Uses `read_file` tool to read the file
3. Analyzes content and generates a 2-3 sentence summary
4. Returns the summary

---

## Code Walkthrough

### main.go - Detailed Explanation

```go
// 1. Create LLM Provider
provider := agentic.NewOpenAIProvider(
    "http://localhost:1234/v1/chat/completions",
    "llama-3.2-1b-instruct",
)
provider.Logger = agentic.NewLogger(agentic.Warn)
```

The `NewOpenAIProvider` constructor creates a provider that communicates with OpenAI-compatible APIs. The logger is set to `Warn` level to reduce output noise.

```go
// 2. Create Skill Runner
runner, err := skillrunner.NewRunner(skillrunner.Config{
    SkillsDirs: []string{"./skills"},
    Provider:   provider,
    WorkDir:    ".",
    Logger:     agentic.NewLogger(agentic.Warn),
})
```

The skill runner:
- Scans `./skills` directory for `SKILL.md` files
- Loads and caches all valid skills
- Uses the same provider for sub-agents
- Sets working directory for file operations

```go
// 3. Build System Prompt with Skills
systemPrompt := "You are a helpful assistant with skills.\n" + runner.GenerateSkillsSection()
```

`GenerateSkillsSection()` produces a formatted list of available skills:

```markdown
## Available Skills
To use a skill, call the 'run_skill' tool with 'skill_name' and 'task' parameters.

### file-resumer
Description: Summarizes the content of a given file...
```

```go
// 4. Create Agent with run_skill Tool
agent := agentic.NewAgent(agentic.Config{
    Provider:     provider,
    SystemPrompt: systemPrompt,
    Tools:        []agentic.Tool{runner},  // Only the skill runner tool
    Logger:       agentic.NewLogger(agentic.Warn),
})
```

The agent only has one tool: the `run_skill` tool provided by the skill runner. When the agent wants to use a skill, it calls this tool.

```go
// 5. Add Observers
agent.AddObserver(helper.NewConsoleObserver())
logObserver := helper.NewMessageLogObserver()
agent.AddObserver(logObserver)
```

- `ConsoleObserver`: Prints formatted output to the console
- `MessageLogObserver`: Captures the entire conversation for later use (JSON export)

```go
// 6. Run Task
task := "Use the file-resumer skill to summarize the content of README.md"
agent.Run(ctx, task)
```

The agent receives the task, decides to use the `file-resumer` skill, calls `run_skill`, and the skill runner executes it via a sub-agent.

---

## Running the Demo

### Basic Run

```bash
cd demo/skill
go run main.go
```

### Expected Console Output

```
Running task: Use the file-resumer skill to summarize the content of README.md
[STATE: Think]
[TOOL_CALL: run_skill({"skill_name": "file-resumer", "task": "README.md"})]
[STATE: Tool]
[STATE: Think]
Agentic is a Go SDK for building AI agents...
[STATE: End]
{"type":"system","content":"You are a helpful assistant..."}
{"type":"user","content":"Use the file-resumer skill..."}
{"type":"assistant","content":"Agentic is a Go SDK for building AI agents..."}
...
```

### JSON Log Output

At the end, you'll see a JSON array containing the full conversation history, including:
- System message (with skills section)
- User task
- Tool calls (with arguments)
- Tool results
- Assistant responses

---

## Expected Output

When running successfully, the demo will:

1. **Print state transitions**: `[STATE: Think]`, `[STATE: Tool]`, etc.
2. **Show tool calls**: The main agent calling `run_skill`
3. **Display the summary**: The file-resumer skill's output (2-3 sentence summary of README.md)
4. **Output JSON log**: Complete conversation history in JSON format

### Sample Output

```
Running task: Use the file-resumer skill to summarize the content of README.md
[STATE: Think]
[TOOL_CALL: run_skill({"skill_name": "file-resumer", "task": "README.md"})]
[STATE: Tool]
[STATE: Think]
Agentic is a Go SDK for building AI agents that interact with LLMs and execute tools. 
It provides an event-driven architecture with support for skills, tool calling, and multiple LLM backends.
[STATE: End]
[{"type":"system","content":"You are a helpful assistant with skills.\n\n## Available Skills..."},...]
```

---

## Creating New Skills

### Step 1: Create Skill Directory

```bash
mkdir -p demo/skill/skills/my-new-skill
```

### Step 2: Create SKILL.md

```bash
cat > demo/skill/skills/my-new-skill/SKILL.md << 'EOF'
---
name: my-new-skill
description: Description of what your skill does
---

# My New Skill

You are a specialized assistant for [task].

## Instructions
1. Step one
2. Step two
3. Return the result

## Tools Available
- `read_file`: Read file contents
- `edit_file`: Modify file contents
- `run_command`: Execute shell commands
EOF
```

### Step 3: Run the Demo

The skill runner automatically loads new skills on startup. Just run the demo again:

```bash
go run main.go
```

The main agent will now see `my-new-skill` in its system prompt and can use it via `run_skill`.

### Example: Code Analyzer Skill

```markdown
---
name: code-analyzer
description: Analyzes Go code files and provides insights about structure, complexity, and potential issues
input-schema: {"type": "object", "properties": {"file_path": {"type": "string"}}, "required": ["file_path"]}
---

# Code Analyzer Skill

You are a Go code analysis assistant.

## Steps
1. Use `read_file` to read the Go source file
2. Analyze the code structure:
   - Count functions, types, and methods
   - Identify imports
   - Note any potential issues (long functions, deep nesting, etc.)
3. Provide a structured summary:
   - File purpose (inferred from package name and comments)
   - Statistics (function count, line count if possible)
   - Notable observations

## Output Format
Return a structured analysis with sections for Overview, Statistics, and Observations.
```

---

## Skill Runner API

### skillrunner.Config

```go
type Config struct {
    // SkillsDirs is the list of directories to scan for skills
    SkillsDirs []string

    // Provider is the LLM provider for sub-agents
    Provider agentic.LLMProvider

    // WorkDir is the working directory for file operations
    WorkDir string

    // Logger is an optional logger
    Logger *agentic.Logger
}
```

### skillrunner.Runner Methods

#### `NewRunner(cfg Config) (*Runner, error)`
Creates a new skill runner and loads skills from the specified directories.

#### `GetSkill(name string) *Skill`
Returns a skill by name, or `nil` if not found.

#### `GetAllSkills() []*Skill`
Returns a list of all loaded skills.

#### `Schema() agentic.ToolSchema`
Returns the tool schema for the `run_skill` tool. Implements the `agentic.Tool` interface.

#### `Execute(input string) (string, error)`
Executes a skill. Implements the `agentic.Tool` interface.

#### `GenerateSkillsSection() string`
Generates a formatted string listing all available skills. Inject this into the main agent's system prompt.

### Skill Structure

```go
type Skill struct {
    Name         string                 // Skill name (from frontmatter)
    Description  string                 // Skill description (from frontmatter)
    Instructions string                 // Markdown instructions (after frontmatter)
    InputSchema  map[string]interface{} // Optional JSON Schema (from frontmatter)
    Path         string                 // Directory path of the skill
    SubSkills    []*Skill               // Sub-skills loaded from subdirectories
    Tools        []string               // Requested parent tool names (empty = all)
    Skills       []string               // Inherited parent skill names (empty = all)
}
```

---

## Troubleshooting

### Common Issues

#### "connection refused" or "no such host"
**Problem**: LLM endpoint is not running.

**Solution**:
1. Ensure your LLM server is running (LM Studio, Ollama, etc.)
2. Check the endpoint URL in `main.go`
3. Test with: `curl http://localhost:1234/v1/models`

#### "skill not found: xxx"
**Problem**: The skill name doesn't match or wasn't loaded.

**Solution**:
1. Check that `SKILL.md` exists in the skills directory
2. Verify the `name` field in frontmatter matches what you're calling
3. Check for duplicate skill names (warning logged)
4. Ensure the skills directory path is correct in `main.go`

#### "missing required 'name' field" or "missing required 'description' field"
**Problem**: `SKILL.md` is missing required frontmatter.

**Solution**: Ensure your `SKILL.md` starts with `---` and includes `name` and `description` fields.

#### Sub-agent hangs or times out
**Problem**: The LLM might be too slow or the task is too complex.

**Solution**:
1. Use a faster model (e.g., `llama-3.2-1b-instruct`)
2. Increase the timeout in `context.WithTimeout()`
3. Simplify the skill instructions
4. Check LLM server logs for errors

#### JSON parsing errors in skill input
**Problem**: Skill expects JSON but receives plain text (or vice versa).

**Solution**:
1. Define `input-schema` in skill frontmatter for JSON validation
2. Handle both formats in skill instructions (see `file-resumer` for example)
3. The runner attempts to wrap plain strings if `input-schema` is defined

---

## Advanced Usage

### Multiple Skills Directories

```go
runner, err := skillrunner.NewRunner(skillrunner.Config{
    SkillsDirs: []string{
        "./skills",           // Project-specific skills
        "~/.agentic/skills",  // User-wide skills
        "/shared/skills",     // Shared skills
    },
    Provider: provider,
    WorkDir:  ".",
})
```

### Custom Sub-Agent Tools

Currently, skills always have access to `read_file`, `edit_file`, and `run_command`. To customize:

1. Modify `skillrunner/tools/tools.go` to export a configurable tool set
2. Pass custom tools via `skillrunner.Config`

### Skill Input Validation

Use `input-schema` in frontmatter for JSON Schema validation:

```markdown
---
name: advanced-skill
description: Does something advanced
input-schema: {"type": "object", "properties": {"path": {"type": "string"}, "options": {"type": "array", "items": {"type": "string"}}}, "required": ["path"]}
---
```

The runner will attempt to wrap plain string inputs into the expected JSON format.

### Programmatic Skill Access

You can access skills programmatically:

```go
// Get a specific skill
skill := runner.GetSkill("file-resumer")
if skill != nil {
    fmt.Printf("Found skill: %s\n", skill.Description)
}

// List all skills
skills := runner.GetAllSkills()
for _, s := range skills {
    fmt.Printf("- %s: %s\n", s.Name, s.Description)
}
```

### Exporting Skill Definitions

Skills are serializable to JSON:

```go
skill := runner.GetSkill("file-resumer")
data, _ := json.MarshalIndent(skill, "", "  ")
fmt.Println(string(data))
```

---

## Summary

The Skill Demo demonstrates a powerful pattern for building extensible AI agents:

1. **Define skills** in markdown files with clear instructions
2. **Load skills** dynamically from disk at runtime
3. **Expose skills** through a single `run_skill` tool
4. **Execute skills** via specialized sub-agents with relevant tools
5. **Compose behaviors** by combining multiple skills

This approach enables:
- **Modularity**: Add/remove capabilities without code changes
- **Reusability**: Share skills across projects
- **Specialization**: Each skill can have tailored instructions and tools
- **Extensibility**: Users can create custom skills for their use cases

---

## File Reference

| File | Description |
|------|-------------|
| `main.go` | Demo entry point |
| `skills/` | Directory containing skill definitions |
| `skills/*/SKILL.md` | Individual skill definitions |
| `README.md` | This file |

## Related Documentation

- [Agentic SDK README](../..//README.md) - Main SDK documentation
- [AGENTS.md](../..//AGENTS.md) - Contributor guidelines
- [skillrunner package](../../skillrunner/) - Skill runner implementation
- [skillrunner/tools](../../skillrunner/tools/) - Sub-agent tools

---

*Part of the [Agentic SDK](https://github.com/pijalu/agentic) - Building intelligent agents in Go.*
