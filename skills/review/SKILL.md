---
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Copyright (C) 2026 Pierre Poissinger
name: review
description: Code review checklist and analysis
inline: false
mode: reviewer
temperature: 0.1
---

# Code Review Skill

Perform a thorough code review of the given changes.

## Checklist
- [ ] Correctness: does the logic handle all edge cases?
- [ ] Security: are there injection vectors, unsafe operations, or exposure of secrets?
- [ ] Performance: any obvious bottlenecks, unnecessary allocations, or N+1 queries?
- [ ] Idiomatic: does it follow Go best practices? (error handling, interfaces, naming)
- [ ] Testing: are there tests for the new code? Do they cover edge cases?
- [ ] Documentation: are exported symbols documented?
- [ ] Breaking changes: does the change affect the public API?

## Output Format
```
## Review: <file>
### Issues
- **Severity**: <high/medium/low>
  **Description**: <what's wrong>
  **Suggestion**: <how to fix>

### Strengths
- <what was done well>
```
