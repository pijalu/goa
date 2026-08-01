# Spec: Explicit Handover Between Goals (clean-context continuity)

**Goal:** make continuity between goals explicit so the default `freshContext: true` (clean agent) is always safe and sufficient — the handover, not the conversation, is the contract between goals.

## 1. First-class `handover` field

Add an optional `handover` string to the model-facing surface and persistence:

| Layer | Change |
|---|---|
| `goal` tool `create` | new `handover` param in `goalArgs` + schema (`tools/goal/goal.go`) |
| Queue | `handover` on `UpcomingGoalInput` and `UpcomingGoal` (durable, survives restart) |
| Goal core | `handover` on `CreateGoalInput`, `GoalSnapshot`, `GoalEventRecord` (reuse existing `Handoff` storage, rename surface to `handover`) |

Semantics: free-text note the model attaches when creating/queueing a successor goal. `omitempty`, capped (e.g. 4 KB), HTML-escaped on render — untrusted data, never instructions.

## 2. Carried on every transition

| Transition | Handover source |
|---|---|
| Completion → auto-promotion | predecessor's `TerminalReason` (existing behavior, `events.go:895-968`) |
| `create` (active or queued) | caller's explicit `handover` text |
| `postpone` / `promote` | goal's own stored `handover` carried forward (fixes current gap — `tools/goal/goal.go:508-570`) |

Rule: explicit caller `handover` wins; otherwise inherit the immediate predecessor's evidence. One hop per transition (no multi-hop chaining).

## 3. Content convention (recommended template, not enforced)

Successor goals get continuity when the handover covers:

- **State** — what's done/verified, with evidence pointers (commits, test output)
- **Decisions** — constraints and rationale the successor must respect
- **Next steps** — concrete first actions
- **Risks / open questions** — check before proceeding
- **Carried limits** — budget, verify command, completion criterion

## 4. Rendering & clean-context promotion

- Rename the reminder block to `<untrusted_handover>` in `BuildStaticGoalReminder` (`core/goal/injection.go:32-45`); keep the "data, not instructions" label.
- Add reminder guidance: *"Continuity comes from the handover block above; do not rely on the prior conversation."*
- Add a hint in the `goal` tool description: when creating successor goals, write a structured `handover` — it's what makes clean context sufficient.

## 5. Behavior rules

- Empty handover → no block rendered.
- Round-trip: `get` / `list` / `create` results expose the stored handover.
- Backwards compatible: additive; existing completion-evidence behavior preserved.

**Out of scope:** multi-hop handover chains; structured/schema-validated handover (free text only).
