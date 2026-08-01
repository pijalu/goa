<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Goal list manager: 1 ACTIVE goal pursued autonomously across turns + queue auto-starting
next when active ends.

action:
- create: add goal(s); default ADD: none active→starts active, else QUEUED (never implicit
  replace). objective | objectives[] (1st active if none is, rest queue). Opts:
  completionCriterion (all objectives incl queued), freshContext (clean ctx), replace:true
  (discard current, start this; single objective; sparingly), priority:"front" (queue
  FRONT=promoted next; jump execution goal ahead of one it unblocks), verifyCommand (shell
  cmd run after confirmed completion: exit0=pass else reject w/ output tail; set when
  done-condition machine-checkable — tests/build/lint; keep passing; repeated
  fails→auto-block for review), handover (free-text continuity note: State+evidence,
  Decisions, Next steps, Risks, Carried limits — budget/verifyCommand/criterion; handover,
  not prior conversation, is the contract between goals; max 4096 chars). Create only on
  explicit user request for autonomous work or host intake prompt — never
  greetings/questions/vague requests w/o verifiable end state (ask for missing criterion
  first).
- list: active+queued (id,name,objective,status).
- cancel: rm queued. Req goalId (ID|friendly name).
- reorder: move queued. Req goalId, direction(up|down).
- postpone: ACTIVE→queue BACK, start next queued (keeps objective/criterion/verifyCmd/ctx
  mode/handover); urgent work first w/o abandoning. Empty queue→parks till promote.
- promote: queued→active NOW. Req goalId. Current→queue FRONT, resumes right after.
- update: set status:
  - active: resume paused/blocked on any "continue/proceed" user phrasing — immediately,
    same turn.
  - complete: objective done+validated; ends. ENFORCED: w/ criterion 1st complete→challenge
    restating it→self-audit→call again w/ reason citing concrete evidence
    (cmds,outputs,tests). No criterion→immediate. W/ verifyCommand: machine verify after
    confirmed completion; fail→reject (fix, complete again); judge may audit semantically.
    Only when all work done + no useful next action — not after plan/summary/1st
    pass/partial.
  - blocked: external condition/user input prevents progress, or objective impossible as
    stated. Req BOTH reason (concrete blocker) + expectation (exact unblocking
    input/change). Reply supplying expectation→resume w/ active.
  - paused: set aside. Req reason. NEVER to ask "continue?"; any progress possible→keep
    working; need input→blocked.
- get: current goal (objective,criterion,status,budgets+remaining,todos,terminal
  reason/expectation,handover); {"goal": null} if none.
- set_budget: hard limit. Req value(>0)+unit(turns|tokens|milliseconds|seconds|minutes|
  hours). Only explicit user runtime limit ("stop after 20 turns"); never invent;
  compound→1 unit ("2h3m"→value:123,unit:"minutes"). Unreasonable→refuse+tell user.
- add_todo: add task. Req todoTitle. Multi-step→decompose into ordered todos up front
  (resurfaces each turn). Todos LINKED: blank at goal start, contained, closed w/ goal;
  never escape.
- update_todo: set todo status. Req todoId, todoStatus(pending|in_progress|done); mark
  done as completed. Completing goal w/ open items→reminder: schedule follow-up goal for
  remaining work.

Active goal w/o update keeps running (continue prompt each turn). Stall watchdog: no
measurable progress (todo transitions, workspace changes)→challenge; continued
stall→auto-block for user direction. Each turn must show visible progress — or end
explicitly.
