<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Skills System

Skills are reusable prompt templates that extend Goa's capabilities. They are defined as plain Markdown files with YAML frontmatter. Each skill has two orthogonal dimensions:

- **Execution mode** — `inline` (injected into the system prompt) or `sub-agent` (executed as a separate agent via `run_skill`)
- **Category** — `knowledge` (provides rules/instructions injected into the system prompt) or `action` (performs a task when invoked)

## Skill Architecture

```
┌──────────────────────────────────────────────┐
│  SKILL.md with YAML frontmatter              │
│  ───                                         │
│  name: my-skill                              │
│  description: Does something                 │
│  inline: false                               │
│  category: action                            │
│  ───                                         │
│                                              │
│  # Skill Instructions                        │
│  1. Do step one                              │
│  2. Do step two                              │
│  3. Return result                            │
└──────────────────────────────────────────────┘
                                               │
        ▼
┌────────────────┐     ┌───────────────────────┐
│  Inline Skill  │     │  Sub-agent Skill      │
│  (in system    │     │  (via run_skill tool) │
│   prompt)      │     │                       │
│                │     │  ┌────────────────┐   │
│  System prompt │     │  │  Sub-agent     │   │
│  + skill body  │     │  │  Agentic Agent │   │
│  → LLM sees it │     │  │  with tools:   │   │
│  as context    │     │  │  read     │        │
│                │     │  │  edit     │        │
│                │     │  │  run_command   │   │
│                │     │  │  rest_api      │   │
│                │     │  └────────────────┘   │
└────────────────┘     └────────────────────── ┘
```

## Skill Structure

### SKILL.md Format

```markdown
---
name: my-skill
description: What this skill does and when to use it
inline: false          # optional, default false
category: action       # optional, "action" (default) or "knowledge"
profile: coder         # optional, linked profile
temperature: 0.3       # optional, overrides agent temp
tools: ["read"]   # optional, parent tools to inherit
skills: ["other-skill"]# optional, parent skills to inherit
input-schema: >-       # optional, JSON Schema for task input
  {"type":"object","properties":{"file_path":{"type":"string"}},"required":["file_path"]}
---

# Skill Instructions

Full Markdown body with step-by-step instructions.
```

### Categories

| Category | Behavior | Example |
|----------|----------|---------|
| `action` (default) | Skill must be explicitly invoked via `/skill:run:<name>` or the `run_skill` tool. Listed in `<available_skills>` but body is not auto-injected. | `golang-check`, `refactor` |
| `knowledge` | Skill body is automatically injected into the system prompt when the skill is enabled. Provides rules, style guides, or context the LLM should always follow. | `telegram` |

### Execution Modes

| Mode | Description |
|------|-------------|
| `inline` | Skill body is injected into the system prompt or submitted as a user message. No sub-agent is spawned. |
| `sub-agent` | Skill is executed by spawning an isolated sub-agent via AgentPool. The skill body becomes the sub-agent's system prompt. |

A `knowledge` skill is typically also `inline: true` (its body goes into the system prompt). An `action` skill is typically `sub-agent` (runs in isolation) but can be `inline` if the instructions are simple enough for the main agent to follow directly.

### Where Skills Live

| Location | Priority | Description |
|----------|----------|-------------|
| Embedded (compiled-in) | Highest | Built-in skills (refactor, test-gen, document, review, explain, commit-msg, debug, telegram) |
| `~/.goa/skills/` | Medium | User/global skills |
| `.goa/skills/` | Lowest | Project-specific skills |

Later sources override earlier ones when names collide.

## Built-in Skills

| Skill | Type | Category | Description |
|-------|------|----------|-------------|
| `refactor` | Sub-agent | Action | Refactor code with specific patterns |
| `test-gen` | Sub-agent | Action | Generate tests for source files |
| `document` | Sub-agent | Action | Generate documentation |
| `review` | Sub-agent | Action | Review code changes |
| `explain` | Sub-agent | Action | Explain code or concepts |
| `commit-msg` | Sub-agent | Action | Generate commit messages |
| `debug` | Sub-agent | Action | Debug code issues |
| `telegram` | Inline | Knowledge | Telegraphic thinking style |

## Skill Registry

Skills are discovered and loaded by `skills.SkillRegistry`:

```go
registry := skills.NewSkillRegistry(dirs)
registry.LoadAll()  // loads from embedded + directories

skill, ok := registry.Get("review")
summaries := registry.List()  // []SkillSummary

// Check if a skill is inline
if registry.IsInline("explain") {
    // inject into system prompt
}
```

## Running Skills

### Inline Skills

Inline skills work via the `<available_skills>` XML block injected into the
system prompt. Each skill is listed with its name, description, file path,
and category. The LLM is instructed to use the `read` tool to load the SKILL.md
file when a task matches the skill description.

The skill body stays **on disk** — it is never injected into the context
window unless the LLM explicitly reads it. This is token-efficient and
follows the inline skill approach used by Pi and other coding agents.

Knowledge-category skills that are activated (via mode or `/skill:run`) get
their body injected into the system prompt automatically through the
`InlineSkillInjector`.

```
<available_skills>
  <skill>
    <name>telegram</name>
    <description>Think and respond in telegraphic style</description>
    <category>knowledge</category>
    <location>/path/to/skills/telegram/SKILL.md</location>
  </skill>
  <skill>
    <name>refactor</name>
    <description>Refactor code for clarity</description>
    <category>action</category>
    <location>/path/to/skills/refactor/SKILL.md</location>
  </skill>
</available_skills>
```

### Sub-agent Skills

Sub-agent skills are invoked via the `run_skill` tool, which is registered
only when `skills.execution_mode` is set to `sub-agent`. The LLM calls:

```json
{
  "tool": "run_skill",
  "input": {
    "skill_name": "refactor",
    "task": "Refactor src/main.go to use interfaces"
  }
}
```

The `SkillRunnerTool` (`skills/runner.go`) spawns a sub-agent via AgentPool
with the skill body as its system prompt, runs the task, and returns the
final assistant content. Thinking tokens, intermediate tool calls, and
metadata are filtered out, giving the parent agent a clean result.

### User Commands

Skills can also be executed directly via the `/skill:run:<name>` command,
which supports both inline and sub-agent execution depending on the
skill's frontmatter and the global `skills.execution_mode` config.

## Sub-Skills

Skills can have nested sub-skills in subdirectories:

```
skills/
└── genSentence/
    ├── SKILL.md
    ├── genSubject/
    │   └── SKILL.md
    ├── genVerb/
    │   └── SKILL.md
    └── genObject/
        └── SKILL.md
```

Sub-skills are available to the parent skill's sub-agent via the same `run_skill` tool.

## Skill Input Schema

Skills can define an input schema for structured task input:

```yaml
input-schema: >
  {"type":"object","properties":
    {"file_path":{"type":"string"}},
    "required":["file_path"]}
```

If the schema expects a single string field and the task is a plain string, the runner automatically wraps it.

## Skill→Profile Linkage

The `profile` frontmatter links a skill to an agent profile:

```yaml
---
name: code-review
profile: reviewer
---
```

Running a skill can temporarily activate its linked profile, restoring the previous one on completion.

## JS Extension Skills

Skills can also be provided by [JS plugins](PLUGINS.md). A plugin registers
custom tools and commands that the agent can invoke, effectively adding new
capabilities without creating SKILL.md files.

| Approach | Description | When to Use |
|----------|-------------|-------------|
| SKILL.md | Markdown file with YAML frontmatter | Pure instruction/guidance for the agent |
| JS Plugin | JavaScript code via Goja runtime | Complex logic, external APIs, custom tools |
| Hybrid | SKILL.md that references a tool registered by a JS plugin | Best of both: instructions + computation |

### Creating a Skill via JS Plugin

A JS plugin can register a tool that acts as a skill:

```javascript
goa.registerTool({
  name: "my-skill",
  description: "Does something useful — acts as a skill",
  execute: function(params) {
    // Custom logic in JavaScript
    return "Skill executed with: " + JSON.stringify(params);
  }
});
```

The agent will see this tool in its available tools and can invoke it
when the task matches the description. See [PLUGINS.md](PLUGINS.md) for
the full plugin development guide.

### Skill Directories (`.agents/skills/`)

Goa supports the cross-agent skill directory convention:

| Directory | Scope | Priority |
|-----------|-------|----------|
| `~/.agents/skills/` | User-global | Highest (overrides all) |
| `$PWD/.agents/skills/` | Project-local | Medium |
| Embedded (compiled-in) | Built-in | Lowest |

These directories are always searched regardless of YAML configuration.
They use the same SKILL.md format as `skills/` directories.

### Cross-Agent Compatibility

Skills in `~/.agents/skills/` follow a format shared by other AI
coding agents (inspired by Pi, Claude Code, etc.). This means you can share skills
between agents — a SKILL.md created for one agent works in Goa, and vice versa.

## Registering a New Skill

1. Create a directory with a `SKILL.md` file:

```bash
mkdir -p skills/my-skill
cat > skills/my-skill/SKILL.md << 'EOF'
---
name: my-skill
description: Analyzes code and explains it
category: action
---

# Code Analyzer

You are a code analysis assistant...

## Steps
1. Use read to read the file
2. Analyze the code structure
3. Provide a clear explanation
EOF
```

2. Add the skill directory to config (if not in default paths):

```yaml
skills:
  dirs:
    - .goa/skills
    - ~/.goa/skills
```

3. Skills are auto-discovered on startup.
