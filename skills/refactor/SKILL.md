---
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Copyright (C) 2026 Pierre Poissinger
name: refactor
description: Refactor code for clarity, idiomatic style, and correctness
inline: false
mode: coder
temperature: 0.1
---

# Refactor Skill

Analyze and refactor the given code to improve readability, performance, and adherence to idiomatic patterns.

## Process
1. Read the target file(s)
2. Identify code smells: long functions, deep nesting, duplicate code, unclear names
3. Apply refactoring patterns: extract method, rename, simplify conditionals
4. Verify the refactored code compiles and tests pass
5. Present a summary of changes made

## Rules
- Do NOT change the public API or behavior
- Preserve existing comments and documentation
- One refactoring at a time — verify after each step
- If the code has tests, ensure they still pass
