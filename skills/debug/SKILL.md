---
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Copyright (C) 2026 Pierre Poissinger
name: debug
description: Analyze errors and debug output to find root causes
inline: false
mode: coder
temperature: 0.2
---

# Debug Analysis Skill

Analyze error messages, logs, and debug output to identify root causes and suggest fixes.

## Process
1. Collect all available diagnostic information (error output, logs, stack traces)
2. Identify the symptom and trace it to likely root causes
3. Formulate and test hypotheses using available tools
4. Present findings with recommended fixes

## Analysis Framework
- **Symptom**: What is visibly wrong?
- **Environment**: What context might matter? (OS, versions, config)
- **Root Cause**: What is the fundamental issue?
- **Fix**: How to resolve it?
- **Prevention**: How to avoid this in the future?

## Rules
- Start with the most likely cause (Occam's razor)
- Distinguish between symptoms and causes
- If unsure, suggest diagnostic commands to narrow it down
- Never suggest destructive actions without warning
- Present the fix as actionable steps the user can take
