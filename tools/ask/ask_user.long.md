<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# ask_user_question

Ask the user one or more clarifying questions when requirements are ambiguous
or underspecified, and return the aggregated answers.

## When to use

Reach for this tool BEFORE starting uncertain work rather than guessing. It is
the sanctioned way to request clarification: the user answers through the input
line, and each question is shown as a card in the conversation with its title,
summary, question, and numbered options.

## Parameters

- `questions` (array, required): one or more questions; each is asked separately.
  - `question` (string, required): the question text.
  - `title` (string): short label shown on the card and the input title.
  - `summary` (string): optional context explaining why the question is asked.
  - `options` (array of string, max 6): answer choices. The user may type the
    number (`1`) or the option text.
  - `required` (bool, default false): if true, cancelling returns an error.
  - `allow_free_text` (bool, default true): if false with options present, the
    answer is restricted to a listed option.

## Behavior

- Each question is posed separately and blocks until answered (or cancelled).
- A numeric answer maps to the corresponding option; an exact text match
  resolves to that option's canonical label.
- The returned result is a JSON array of `{question, answer, skipped}`.

## Disabling

The tool is enabled by default. Set in config:

```yaml
tools:
  enabled:
    clarify_disabled: true
```
