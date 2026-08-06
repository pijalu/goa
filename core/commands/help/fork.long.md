<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

/fork  open the session picker, then pick a user turn to fork from

Selecting a session shows its user turns (most recent preselected).
Confirming a turn starts a **new conversation** whose history is the source
session truncated just before that turn — the selected message stays an
Up-arrow away in the input history so you can edit and resend it (pi-style
fork → edit → resend). The fork persists under a fresh session ID
(`<source>_fork_<timestamp>`), so continuing it never appends to the source
session file.

/fork:<parent-node-id>  create a new session-tree branch from the given node
(legacy tree behavior; see also /tree and /clone)
