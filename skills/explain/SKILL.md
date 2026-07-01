---
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Copyright (C) 2026 Pierre Poissinger
name: explain
description: Explain code in detail for understanding
inline: false
mode: coder
temperature: 0.3
---

# Code Explanation Skill

Provide a thorough explanation of the given code, suitable for a developer who needs to understand it deeply.

## Structure
1. **Overview**: What does this code do at a high level?
2. **Key Types**: Explain important structs, interfaces, and their relationships
3. **Data Flow**: How does data move through the code? (inputs → processing → outputs)
4. **Key Functions**: Deep dive into the most important functions
5. **Edge Cases**: What edge cases are handled? What's missing?
6. **Dependencies**: What external packages or systems does this code depend on?

## Rules
- Be precise — reference exact function/type names
- Include line numbers for key sections
- Explain WHY decisions were made, not just WHAT the code does
- If the code has issues or could be improved, note them politely
