<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Spec: No File-Type Mode Triggers

## Status

Accepted — implemented.

## Decision

Goa does **not** link agent mode (major mode, autonomy, or skill stack) to the
type or name of the file the agent is currently editing. There is no
"auto-mode-alist" or file-pattern trigger system.

## Rationale

In a coding assistant, the agent frequently touches many kinds of files in a
single task: source files, tests, documentation, build files, configuration,
generated artifacts, and so on. Changing the agent's mode based on the file it
happens to read or write is confusing and counter-productive:

- **Mode becomes unpredictable.** A task that starts in `coder` mode can flip to
  `planner` because the agent opened a `*.md` file, then to `reviewer` because it
  opened a test file, without the user asking for any of those changes.
- **It fights the user.** The user picks a mode for a reason; silently
  overriding that choice because of a filename undermines control and
  transparency.
- **It breaks multi-file tasks.** Real coding work mixes file types, so a
  file-based mode would oscillate or require complex sticky/revert logic that
  is hard to explain.
- **Skills are not file-bound.** A skill like `test-gen` is a user-level
  instruction, not something that should activate automatically just because a
  `*_test.go` file is open.

Mode changes in Goa are therefore **explicit**: user commands (`/mode`,
`/autonomy`), skills, or workflow orchestration. The mode stack still records
*why* a mode was pushed (a source string), but that source is never derived from
a filename pattern.

## Consequences

- No `triggers` section in config.
- No `FileTrigger`, `TriggerEngine`, or `OnToolCall` mode evaluation.
- No `/which-mode` or `/trigger` slash commands.
- `ModeChange` events carry a `Source` string but no `TriggerPattern`.
- The mode stack and `Source()` accessor remain for skills, workflows, and other
  intentional temporary mode changes.

## Related Research

This supersedes the exploratory design in
`docs/archive/research/03-file-mode-triggers.md`, which was implemented and then
removed after real-world use showed it did not fit a coding assistant's control
model.
