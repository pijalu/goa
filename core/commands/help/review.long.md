<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# /review — Code Review

Review code changes in a git project. The review opens a pager showing the diff, where you can scroll, add comments, and submit the review to the agent.

## Usage

Subcommands are separated from `/review` by a colon (`:`).

- `/review` — start a review. If the working tree has uncommitted changes, the diff is against `HEAD`; otherwise it shows the most recent commit (`HEAD^1..HEAD`).
- `/review:<commit>` — start a review against a specific base commit.
- `/review:list` — list the last 10 commits.
- `/review:status` — show active review sessions and their comments.
- `/review:submit` — send the latest review (diff + comments) to the main agent as a user message.
- `/review:export` — write the latest review to `review_<basesha>_<timestamp>.md` in the project root.

## Pager Keys

Inside the review pager:

- `↑` / `↓` or `k` / `j` — scroll one line
- `PgUp` / `PgDn` — scroll one page
- `c` — add a comment on the current line (inline input)
- `e` — edit the comment on the current line
- `d` — delete the comment on the current line
- `b` — change the base commit (shows the last 10 commits)
- `s` — submit the review (asks for confirmation)
- `x` — export the review to a Markdown file (`review_<basesha>_<timestamp>.md` in the project root) without sending it to the agent. The exported content is identical to what `s` (submit) sends to the model.
- `q` or `Esc` — close the pager

Comments are typed inline in the review panel. The input line shows the file and line number so the target is always clear. Lines that have comments are highlighted with a dark background and a green pipe (`│`).
