<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# /review — Code Review

Review code changes in a git project. The review opens a pager showing the diff, where you can scroll, add comments, and submit the review to the agent. You can also review a single text file (`file:` subcommand) — no git repository required.

## Usage

Subcommands are separated from `/review` by a colon (`:`).

- `/review` — start a review. If the working tree has uncommitted changes, the diff is against `HEAD`; otherwise it shows the most recent commit (`HEAD^1..HEAD`).
- `/review:<ref>` — start a review against a specific checkpoint: a tag, branch, or `HEAD` ancestor (e.g. `/review:v1.2.0`, `/review:main`, `/review:^3`). Tab-completion after `/review:` suggests `^1` (the default base) first, then the most recent tags and branches.
- `/review:list` — list the last 10 commits.
- `/review:status` — show active review sessions and their comments.
- `/review:submit` — send the latest review to the main agent as a user message. The message contains the base/head refs and your comments plus the `git diff <base>..HEAD` command — the diff itself is not embedded, keeping the agent's context small.
- `/review:export` — write the latest review to `review_<basesha>_<timestamp>.md` in the project root.
- `/review:file:<path>` — open one text file in the review pager (e.g. `/review:file:internal/app/tui.go`). Binary files are rejected with a friendly message; oversized files load truncated with a `(truncated)` marker in the title. Works outside git repositories too. Markdown files render as highlighted *source* (not reflowed), so comment anchors always match real source lines.

### Single-file review notes

- Completion: after `/review:` Tab suggests `^1`/`^2`/`^3`, then `file:` ("Review a single file"), then tags and branches. After `/review:file:` the path completes exactly like an `@` mention: fd-backed, gitignore-aware, ranked; directories complete with a trailing `/`.
- Caveat: paths containing a `:` cannot be addressed — the command router splits on colons, so such paths are unreachable by design.
- Submitting sends the agent a message whose shape is:

  ```markdown
  # File Review

  - **File:** <absolute path>
  - **Lines reviewed:** <count>

  Read the file to see each comment in context. Comments are anchored to the
  line numbers of that file.

  ## Comments

  - `<project-relative path>:<line>`: <comment>
  ```

  The absolute path lets the agent's read tools open the file directly; anchors use project-relative paths so the agent can jump straight to the line. With no comments yet the summary says `No comments yet.` instead.

## Pager Keys

Inside the diff review pager:

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

### File review pager keys

The single-file pager (`/review:file:<path>`) shares every key above **except** `b` (change base — meaningless for one file):

| Key | Action |
|---|---|
| `↑`/`↓`, `k`/`j`, `PgUp`/`PgDn` | navigate |
| `c` | add comment on current line (prompt shows `<file>:<line>`) |
| `e` / `d` | edit / delete comment on current line |
| `s` | submit (confirm via `y/n`) — sends the summary to the agent, closes the pager |
| `x` | export summary to `review_file_<name>_<timestamp>.md` in the project root; pager stays open |
| `q` / `Esc` / `Ctrl+C` | close |
