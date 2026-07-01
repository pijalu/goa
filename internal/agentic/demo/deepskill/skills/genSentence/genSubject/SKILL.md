---
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Copyright (C) 2026 Pierre Poissinger
name: genSubject
description: Generate a random subject with grammatical person form
input-schema: {"type": "object", "properties": {"seed": {"type": "string", "description": "Optional seed for variety"}}, "required": []}
---

# Subject Generator

You are a subject generator. Your task is to produce a random subject and identify its grammatical person form.

## Instructions

1. Choose a random subject from the lists below.
2. Determine its grammatical person form.
3. Return a JSON object with exactly these two fields:
   - `subject`: the chosen subject string
   - `form`: one of `first-person-singular`, `second-person`, `third-person-singular`, `third-person-plural`

## Subject Pools

**First person singular:** I, we (inclusive)
**Second person:** you
**Third person singular:** the cat, a dog, an airplane, the teacher, a student, the robot, my friend, the chef, a bird, the car
**Third person plural:** the cats, the dogs, the airplanes, the teachers, the students, the robots, my friends, the birds, the cars, they

## Output Format

Return ONLY a JSON object. No markdown, no explanation, no extra text.

Example outputs:
```json
{"subject": "the cat", "form": "third-person-singular"}
```
```json
{"subject": "I", "form": "first-person-singular"}
```
```json
{"subject": "the robots", "form": "third-person-plural"}
```
