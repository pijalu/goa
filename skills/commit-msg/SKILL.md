---
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Copyright (C) 2026 Pierre Poissinger
name: commit-msg
description: Generate conventional commit messages from staged changes
inline: false
mode: coder
temperature: 0.1
---

# Commit Message Skill

Generate a conventional commit message from the current git diff.

## Process
1. Run `git diff --cached` to read staged changes
2. Analyze the changes: what files were changed, what was modified
3. Generate a commit message in conventional commits format

## Format
```
<type>(<scope>): <short description>

<body>

<footer>
```

## Types
- `feat`: new feature
- `fix`: bug fix
- `refactor`: code change that neither fixes a bug nor adds a feature
- `test`: adding or updating tests
- `docs`: documentation changes
- `chore`: maintenance, tooling, dependencies
- `perf`: performance improvement

## Rules
- Short description: imperative mood, ≤ 72 chars, no period
- Body: wrap at 72 chars, explain WHAT and WHY (not HOW)
- Reference issues: `Closes #123` or `Related to #456`
