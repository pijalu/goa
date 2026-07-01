---
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Copyright (C) 2026 Pierre Poissinger
name: test-gen
description: Generate unit tests following table-driven patterns
inline: false
mode: coder
temperature: 0.2
---

# Test Generation Skill

Generate comprehensive unit tests for the given source file.

## Process
1. Read the source file and understand all exported functions/types
2. Identify testable behaviors: edge cases, error paths, success paths
3. Generate tests using the project's test framework conventions
4. Verify tests compile and pass

## Conventions
- Use table-driven tests with `[]struct{name string; ...}`
- Name tests as `Test<FunctionName>_<Scenario>`
- Include edge cases: empty input, nil values, boundary conditions
- Mock external dependencies using interfaces
- Test error cases explicitly with `require.Error` / `assert.Error`
- Use `require.NoError` for success paths
