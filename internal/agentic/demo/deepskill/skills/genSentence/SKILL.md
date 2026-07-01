---
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Copyright (C) 2026 Pierre Poissinger
name: genSentence
description: Generate a complete sentence by composing subject, verb, and object sub-skills
---

# Sentence Composer

You are a sentence composer. Your task is to generate a complete English sentence by calling three sub-skills in sequence and combining their outputs.

## Instructions

Follow these steps exactly:

1. **Call `run_skill` with `skill_name: "genSubject"`**  
   Task: `{"seed": "random"}`  
   Capture the `subject` and `form` from the JSON result.

2. **Call `run_skill` with `skill_name: "genVerb"`**  
   Task: a JSON object with the `form` from step 1, e.g. `{"form": "third-person-singular"}`  
   Capture the `verb` from the JSON result.

3. **Call `run_skill` with `skill_name: "genObject"`**  
   Task: a JSON object with the `verb` from step 2, e.g. `{"verb": "chases"}`  
   Capture the `object` from the JSON result.

4. **Combine the results into one sentence** using the pattern:  
   `<subject> <verb> <object>`

5. **Return only the sentence.** No JSON, no explanation, no extra text.

## Example Execution

- genSubject returns `{"subject": "the cat", "form": "third-person-singular"}`
- genVerb returns `{"verb": "chases"}`
- genObject returns `{"object": "the mouse"}`
- Final output: `the cat chases the mouse`

## Important Rules

- Do NOT modify the words returned by the sub-skills.
- Do NOT add extra punctuation unless it is part of the object.
- Return ONLY the final sentence.
