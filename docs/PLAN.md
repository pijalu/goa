# Plan Mode

Goa's `/plan` command brings structured, event-sourced work planning to the
terminal. Plans are persisted, reviewable, and sequentially executable.

## Quick start

```
/plan:new:objective=Refactor the authentication module
```

This creates a plan and starts the planner agent. The planner explores the
codebase and drafts plan items. When ready, it calls `submit_review` to open
the annotation pager.

## Pager keys

| Key | Action |
|-----|--------|
| `↑/↓` | Navigate lines |
| `n` / `p` | Jump to next / previous item |
| `c` | Add comment on current line |
| `e` / `d` | Edit / delete comment |
| `s` | Submit annotations to the planner |
| `a` | Approve the plan |
| `q` / Esc | Close without submitting |

## Commands

| Command | Description |
|---------|-------------|
| `/plan` | Interactive action list |
| `/plan:new:objective=...` | Create a plan + start planner |
| `/plan:review:id=` | Open the plan pager |
| `/plan:approve:id=` | Approve and start execution |
| `/plan:status:id=` | Open plan-status overlay |
| `/plan:replan:id=` | Re-enter planning phase |
| `/plan:list` | List all plans |
| `/plan:delete:id=,confirm=true` | Delete a plan |

## How it works

Plans are stored under `.goa/plans/<plan-id>/` as an event-sourced log
(`events.jsonl`) with a JSON snapshot (`plan.json`). The planner agent uses
the `plan` tool to add, update, remove, and reorder items. On approval, the
execution orchestrator dispatches items one at a time to worker agents with
limited context windows. Workers report results via the `task_outcome` tool.

## Retention

Terminal-state plans (`done`, `blocked`, `failed`) are automatically cleaned
up after the configured retention period (default: 7 days). Configure via:

```yaml
plan:
  retention:
    enabled: true
    days: 7  # 0 = keep forever
```
