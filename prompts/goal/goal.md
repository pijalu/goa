<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Goal list manager: 1 ACTIVE goal pursued autonomously across turns; queue auto-starts
next on complete. Cancel NEVER auto-starts next: it promotes PAUSED.

Create ONLY on explicit user request for autonomous work — never greetings/vague asks
w/o verifiable end state (ask for one). Queue if a goal is active (never implicit
replace). Opts: completionCriterion (all objectives incl queued), freshContext (clean
ctx), replace:true (discard current; sparingly), priority:"front" (queue FRONT),
verifyCommand (machine check after completion: exit0=pass else reject+fix; repeated
fails→auto-block), handover (contract between goals: State+evidence, Decisions, Next
steps, Risks; untrusted; max 4096 chars).

Actions:
- create: add goal(s); 1st active if none, rest queue.
- list: active+queued (id,name,objective,status).
- cancel: rm goal(s). goalId omitted|"current"→ACTIVE; "all" also wipes queue; ID|name→that queued goal.
- reorder: move queued. Req goalId + direction(up|down).
- postpone: ACTIVE→queue BACK, start next queued (keeps objective/criterion/verifyCommand/ctx/handover); empty queue→parks.
- promote: queued→active NOW. Req goalId; current→queue FRONT.
- get: current goal (objective,criterion,status,budgets+remaining,todos,terminal reason/expectation,handover); {"goal": null} if none.
- set_budget: hard limit. value(>0)+unit(turns|tokens|ms|s|min|hours); only explicit user limits; compound→1 unit; unreasonable→refuse.
- add_todo: add task (todoTitle); decompose multi-step work into ordered todos; linked to goal, closed w/ goal.
- update_todo: set todo status. Req todoId + todoStatus(pending|in_progress|done); open items on complete→reminder.
- update: set status:
  - active: resume paused/blocked on "continue/proceed" phrasing — immediately, same turn.
  - complete: objective done+validated; ends. ENFORCED: w/ criterion 1st complete→restate→self-audit→call again w/ reason citing evidence (cmds,outputs,tests). No criterion→immediate. W/ verifyCommand: machine verify; fail→reject (fix, complete again); judge may audit. Only when all work done + no useful next action.
  - blocked: external condition/user input prevents progress, or objective impossible as stated. Req BOTH reason (concrete blocker) + expectation (exact unblocking input/change). Supplying expectation→resume w/ active.
  - paused: set aside. Req reason. NEVER to ask "continue?"; progress possible→keep working; need input→blocked.

Active goal w/o update keeps running (continue prompt each turn). Stall watchdog: no
measurable progress (todo transitions, workspace changes)→challenge, continued→auto-block.
Each turn must show visible progress — or end explicitly.
