# Skill Execution Specification

## 1. Skill Execution Modes

A skill can be executed in one of two modes:

- **Inline**: The skill body is sent as a user message to the main agent. The main agent executes the instructions using its own tools.
- **Sub-agent**: The skill body is executed by a dedicated sub-agent. The sub-agent returns the result to the main agent.

Mode selection is determined by, in order of precedence:

1. Skill frontmatter `inline: true` → forces inline execution.
2. Skill frontmatter `inline: false` or absent → respects the global `skills.execution_mode` config.
3. Global `skills.execution_mode`:
   - `inline` → inline execution.
   - `subagent` → sub-agent execution.
4. Skills that declare sub-skills (via `sub-skills` frontmatter or a `skills/` subdirectory) are **always** executed as sub-agents, regardless of the global mode. In inline mode, such skills are hidden from the main agent.

## 2. Sub-agent Isolation

A sub-agent executing a skill is isolated from the main agent:

- It has its own conversation history and chat ID.
- It does NOT inherit the main agent's skill registry or the `run_skill` tool.
- It receives only the tools explicitly requested by the skill (via `tools:` frontmatter). If no tools are requested, a safe default set is provided.
- It receives any sub-skills defined by the skill, either inline in the frontmatter or discovered in a `skills/` subdirectory.
- The `terminal` tool is excluded from the sub-agent because its sandboxed environment uses a restricted PATH.

## 3. Skill Sub-skills

A skill can expose sub-skills to the sub-agent in two ways:

1. **Frontmatter list**: `sub-skills: [lint, document]`
2. **Subdirectory**: a `skills/` directory inside the skill directory, containing `SKILL.md` files.

Sub-skills are:

- Loaded when the parent skill is loaded.
- NOT listed in the main agent's available skills (they are hidden in all modes).
- Included in the sub-agent's system prompt when the parent skill is executed.

## 4. Tool Exposure to Sub-agent

The skill frontmatter `tools:` field lists the tool names that should be available to the sub-agent. Examples:

```yaml
tools: [bash, read, edit]
```

If `tools:` is absent, the sub-agent receives a default safe set: `bash`, `read`, `edit`, `write`, and `webfetch` (if available). It never receives `run_skill`, `terminal`, or `learn_skill`.

## 5. Slash Command Behavior

`/skill:run:<name>` behaves as follows:

- If the skill is inline-eligible, the skill body is sent as a user message to the main agent (existing behavior).
- If the skill is sub-agent-eligible, the slash command invokes the `run_skill` tool if it is registered. The result is submitted to the main agent as a user message.
- If the skill is sub-agent-eligible but the `run_skill` tool is not registered (e.g., global mode is inline but the skill has sub-skills), the slash command creates a dedicated sub-agent directly using the same registry and pool.

In all cases, the sub-agent result is visible in the chat.

## 6. Dedicated Chat ID

Every sub-agent created for skill execution receives a unique role/chat ID (e.g., `skill-{name}-{nanos}`). It is evicted from the agent pool after execution to free resources and prevent cross-session contamination.

## 7. Visibility in Chat

When a skill runs in a sub-agent:

- The user sees a "Running skill '<name>' in sub-agent..." message.
- The sub-agent result is submitted as a user message prefixed with `[Skill result: <name>]`.
- The main agent can then respond to the result.

