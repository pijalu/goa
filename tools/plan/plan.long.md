// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

The `plan` tool manages a structured work plan. It is used during the planning
phase (drafting and reviewing items) and the execution phase (dispatching items).

## Actions

| Action | Phase | Description |
|--------|-------|-------------|
| `add_item` | planning | Add a new item. Fields: `title`, `description`, `depends_on?`, `role?`, `after?` |
| `update_item` | planning | Patch item fields. Fields: `id`, `title?`, `description?`, `depends_on?`, `role?` |
| `remove_item` | planning | Remove an item by `id`. Fails if other items depend on it. |
| `reorder` | planning | Reorder items. Field: `ids` (all item IDs in new order). |
| `get` | any | Return the plan as rendered Markdown. |
| `submit_review` | planning | Submit current revision for user review. Stops the turn. |
| `resolve_comment` | planning | Mark a comment as resolved. Fields: `id`, `note?`. |
| `start_item` | execution | Start executing an item. Validates dependencies and single-in-flight invariant. |
| `complete_item` | execution | Mark an item as done. Fields: `id`, `result` (must be non-empty). |
| `block_item` | execution | Mark an item as blocked. Fields: `id`, `reason`. |
| `skip_item` | execution | Skip an item (must be pending or blocked). Fields: `id`, `reason?`. |

Finish by calling task_outcome.
