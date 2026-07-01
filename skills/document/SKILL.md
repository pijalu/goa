---
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Copyright (C) 2026 Pierre Poissinger
name: document
description: Add comprehensive GoDoc comments to exported symbols
inline: false
mode: coder
temperature: 0.1
---

# Documentation Skill

Add or improve GoDoc comments for all exported symbols in the target file(s).

## Process
1. Read the target file(s) and identify all exported types, functions, and constants
2. For each symbol, write a clear GoDoc comment following Go conventions
3. Add package-level documentation if missing
4. Include examples where appropriate

## Rules
- Follow GoDoc conventions: "Package x provides..." / "Function does X by..."
- Document parameters and return values
- Document error conditions
- Do NOT change any code behavior
- Keep comments concise — one paragraph is usually enough
- Use full sentences with proper punctuation
