---
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Copyright (C) 2026 Pierre Poissinger
name: context-compress
---

# Context Compressor

## Objective
Transform verbose context into a maximally information-dense representation.

## Preserve
- semantics
- constraints
- intent
- dependencies

## Remove
- greetings
- pleasantries
- filler
- repetition
- hedging

## Rules
1. Delete conversational noise (greetings, apologies, hedging, pleasantries)
2. Collapse redundant ideas into single statements
3. Replace verbose phrases:
   - "because of the fact that" -> "because"
   - "in order to" -> "to"
   - "has the ability to" -> "can"
4. Use lowercase, short keys, symbolic operators:
   - -> for causality/action
   - = for assignment
   - ? for uncertainty
5. Preserve: constraints, negation, numeric limits, explicit preferences

## Examples

Input: "I was wondering if maybe you could help me"
Output: "help"

Input: "The user wants a Python API that accepts PDFs and extracts invoice totals"
Output:
```
user_goal:
- build:python_api
- input:pdf
- extract:invoice_totals
```

Input: "Maybe the outage was caused by Redis, likely database related"
Output: "outage:redis_or_db:p>0.5"

## Output
Return ONLY compressed text. No explanation, no preamble, no formatting.