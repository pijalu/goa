# Plan: Orchestrator Footer — SOLID/DRY stats reuse, per-agent context, minimal agents

## Overview

Four interlocking issues in the orchestrator footer:
1. **Rogue `.│` line + redundant main model line** — line 1 (workdir/mode badge) and line 2
   (main stat/model) are printed alongside the per-agent lines, duplicating information
   and looking cluttered.
2. **No per-agent context info** — the main footer shows `9.8%/1.0M (auto)` context
   usage via `buildFooterStatParts(sessionStats.ContextEstimate/ContextMax)`, but the
   per-agent `sessionStats` built by `formatOrchAgentLine` leaves those fields at zero
   because `AgentEnhancedRow` has no context fields → context % is silently omitted.
3. **Multiple rows per role (`Coder·2`, `Coder·3`)** — the view keys rows by
   handle `AgentID` (`coder-2`, `coder-3`), and the new per-agent footer lines are
   built from rows. Every delegation to the same role creates a new handle/row, so
   sequential delegations produce `Coder`, `Coder·2`, `Coder·3`. The user expects
   ONE line per role ("minimal agents").
4. **Active model coloring missing** — the normal footer colors the active model green
   (`ansi.Fg("#3fb950")`) via `formatModelPart(..., active bool)`. The per-agent lines
   are plain text. "Active" = a request is currently in flight; the orchestrator's
   `AgentStatus` (running/idle/finished) provides this signal.
5. **No DRY/SOLID** — the per-agent line is built by a bespoke function
   (`formatOrchAgentLine`) that partially duplicates the normal footer's stat-building
   code instead of using the exact same `sessionStats` + `formatFooterStats` path.

## Design: one shared stat-line builder, consumed by both footers

**Core principle**: there is ONE function that builds a "stat + model + activity" line.
It takes a `sessionStats` (tokens, cache, tool calls, context) plus model/provider/
thinking/activity metadata. Both the normal footer line 2 and each per-agent
orchestration line call it. Nothing else builds such a line.

```
formatFooterLine(stats sessionStats, model, provider, thinking, activity string, busy, active bool) string
```

This replaces the current ad-hoc line 2 building in `Footer.Render` (which today
happles together `renderTwoCol(buildLeftSide, buildModelDisplay, ...)`) AND
replaces `formatOrchAgentLine`. Both footers call the same function with
different data sources (main agent stats vs per-agent row stats).

---

## Step-by-step changes

### 1. Propagate context window stats per agent (the data pipeline)

Per-agent context is currently dropped at the adapter level. The sequence of
changes spans 6 layers. No new concepts — just plumbing existing data through.

**1a. `core/orchestrator/handle.go` — add context to AgentStats / AgentStatsSnapshot**

Add three fields to both `AgentStats` and `AgentStatsSnapshot`:

```go
ContextEstimate int   // from EventContextStats.EstimatedTokens
ContextMax      int   // from EventContextStats.MaxTokens
ContextAutoMax  bool  // from EventContextStats.AutoMax
```

Add a method `SetContext(estimate, max int, autoMax bool)` on `AgentStats`
that stores them under the mutex. Snapshot copies them.

**1b. `internal/app/orchestrator_adapter.go` — handle EventContextStats in applyOutputEvent**

Add a case to the `switch` in `applyOutputEvent`:

```go
case agentic.EventContextStats:
    applyContextStats(h, rt, ev)
```

Implement `applyContextStats`:

```go
func applyContextStats(h *orchestrator.AgentHandle, rt *orchestrator.Runtime, ev agentic.OutputEvent) {
    if ev.ContextStats == nil {
        return
    }
    h.Stats.SetContext(ev.ContextStats.EstimatedTokens, ev.ContextStats.MaxTokens, ev.ContextStats.AutoMax)
    // Push a throttled live stats event so the TUI updates in real time.
    if rt != nil {
        rt.EmitLiveStats(h, liveStatsInterval)
    }
}
```

`agentic.OutputEvent` already carries `ContextStats` with `MaxTokens`,
`EstimatedTokens`, `AutoMax` (check the struct definition — it's the same
struct the normal footer uses).

**1c. `core/orchestrator/runtime.go` — include context in statsPayload**

In `statsPayload(s AgentStatsSnapshot)` add three keys:

```go
"context_estimate": s.ContextEstimate,
"context_max":      s.ContextMax,
"context_auto_max": s.ContextAutoMax,
```

**1d. `internal/app/orch_view_source.go` — propagate context through AgentStatsDelta**

In the `EventAgentStats` case of `translateOrchEvent`, add:

```go
ContextEstimate: orchInt(ev.Payload, "context_estimate"),
ContextMax:      orchInt(ev.Payload, "context_max"),
ContextAutoMax:  orchBool(ev.Payload, "context_auto_max"),
```

**1e. `tui/orchestrator/event.go` — add context to AgentStatsDelta**

Add three fields:

```go
ContextEstimate int
ContextMax      int
ContextAutoMax  bool
```

**1f. `tui/orchestrator/view.go` — add context to AgentEnhancedRow + copy in applyRowEv**

Add:

```go
ContextEstimate int
ContextMax      int
ContextAutoMax  bool
```

In `applyRowEv`, under `if ev.Stats != nil { … }`, copy them:

```go
row.ContextEstimate = ev.Stats.ContextEstimate
row.ContextMax = ev.Stats.ContextMax
row.ContextAutoMax = ev.Stats.ContextAutoMax
```

### 2. Create the shared footer-line builder

**`internal/app/stats.go` (or a new `footer_line.go`)** — pure function,
no receiver, no side effects:

```go
// formatFooterLine builds ONE rich footer line combining session stats and
// model metadata. Both the normal footer line 2 and every per-agent
// orchestration line are produced by this single function (DRY/SOLID).
// .
// The caller provides:
//   - stats: the same sessionStats struct built from whatever source (main
//     agent or per-agent row)
//   - model, provider: model display fields
//   - thinking: thinking level badge ("" or "off" to omit)
//   - activity: "streaming", "thinking", "tool", etc. (shown after model
//     when busy)
//   - busy: true → prepend animated spinner frame
//   - active: true → model is green (the signal for "this agent is in flight")
//
// Returns the full styled line (SGR-encoded), width-capped only by the caller.
func formatFooterLine(stats sessionStats, model, provider, thinking, activity string, busy, active bool) string {
    // 1. Stats part: buildFooterStatParts(stats) → ["↑10k", "↓5k", "CH96.2%", "9.8%/1.0M (auto)", ...]
    statsStr := strings.Join(buildFooterStatParts(stats), " ")
    // 2. Model part: reuse formatModelPart for consistent busy/active coloring + thinking suffix
    modelStr := formatModelPart(model, level, activity, busy, active)
    // 3. Assemble: "↑10k ↓5k CH96.2% - (google) gemma • high"
    //    or with provider: "↑10k ↓5k CH96.2% - (lmstudio) google/gemma • high"
    var b strings.Builder
    if statsStr != "" {
        b.WriteString(statsStr)
        b.WriteByte(' ')
    }
    b.WriteString("- ")
    if provider != "" {
        b.WriteString("(" + provider + ") ")
    }
    b.WriteString(modelStr)
    return b.String()
}
```

Note: `formatModelPart` is currently a method on `*Footer` (it accesses
`CurrentSpinnerFrame()` and `f.data.*`). You will need to:
- Either extract `formatModelPart` into a **package-level function** with
  explicit parameters for the fields it needs (a function that takes
  `(model, level, activity string, busy, active bool)` and returns the
  styled string). The footer calls it; the shared builder calls it.
- OR copy the formatting logic inline. The first option (extract + share)
  is the SOLID/DRY choice.

The spinner frame (`CurrentSpinnerFrame()`) is a package-level function
in `tui` (already available). The busy prefix styling is the same for
both contexts.

### 3. Use the shared builder in the normal footer

**`tui/footer_render.go`** — `Footer.Render` currently builds line 2 by
combining `buildLeftSide` + `renderTwoCol` + `buildModelDisplay`. Replace
that with a single call to `formatFooterLine`:

```go
// Old line 2 construction (remove buildLeftSide, buildModelDisplay, compactRightSide):
line2 := formatFooterLine(
    sessionStats{PromptN: ..., PredictedN: ..., ...},  // from f.data
    f.data.Model,
    provider,  // extracted from f.data.Model or separate f.data.Provider
    f.data.ThinkingLevel,
    f.data.MainActivity,
    f.data.ModelBusy,
    !f.data.CompanionBusy, // active = main model is the one working
)
```

This replaces the ~80 lines of left-side/right-side building, compacting,
and two-column padding. The footer line 2 now goes through the EXACT same
`buildFooterStatParts` + `formatModelPart` path as the per-agent lines,
guaranteeing format parity.

**Important**: `buildLeftSide`, `renderTwoCol`, `buildModelDisplay`,
`compactRightSide`, `strip*`, `companionVis`, `buildCompanionMainPart`,
`buildCompanionSubPart`, and related helpers become dead code after
this refactor. Keep them until all tests pass, then delete them.

### 4. Use the shared builder for per-agent orchestration lines

**`internal/app/orch_tabs.go`** — replace `formatOrchAgentLine` with a
thin wrapper that translates an `AgentEnhancedRow` into a `sessionStats`
+ activity flags and calls `formatFooterLine`:

```go
func formatOrchAgentLine(r orchpanel.AgentEnhancedRow) string {
    label := roleLabel(r)
    stats := sessionStats{
        PromptN:         r.TokensIn,
        PredictedN:      r.TokensOut,
        CacheReadTotal:  r.CacheRead,
        CacheWriteTotal: r.CacheCreation,
        ToolCalls:       r.ToolCalls,
        ContextEstimate: r.ContextEstimate,
        ContextMax:      r.ContextMax,
        ContextAutoMax:  r.ContextAutoMax,
    }
    active := r.Status == "running"  // the status set by the orchestrator runtime
    busy := active
    return label + ": " + formatFooterLine(stats, r.Model, r.Provider, r.Thinking, /*activity*/"", busy, active)
}
```

`r.Status` is already populated from the orchestrator's `AgentStatsSnapshot.Status`
("running", "idle", "finished") via `EventAgentStats → translateOrchEvent → view.applyRowEv`.
An agent whose status is `"running"` has a request in flight → `active=true`.

**Remove** the old `formatOrchAgentLine`, `orchProviderModel`, `titleFirst`,
`cacheField`, `formatK` helpers. Replace `updateOrchFooterStats` to still
cap at last 5 rows, but now iterate `Rows()` correctly (see step 5 for
the deduplication change).

### 5. Aggregate footer rows by role (fix "Coder·2")

**`internal/app/orch_tabs.go`** — in `updateOrchFooterStats`, deduplicate
by role before building lines:

```go
func (a *App) updateOrchFooterStats() {
    // ... guard checks ...
    rows := v.Rows()
    // Show at most one line per role — aggregate stats for the minimal set.
    aggregated := aggregateByRole(rows)
    if len(aggregated) > 5 {
        aggregated = aggregated[len(aggregated)-5:]
    }
    lines := make([]string, 0, len(aggregated))
    for _, r := range aggregated {
        lines = append(lines, formatOrchAgentLine(r))
    }
    stats := strings.Join(lines, "\n")
    if stats == "" { stats = "orchestration running" }
    a.subs.footer.SetData(tui.FooterData{OrchestrationStats: stats})
}
```

Where `aggregateByRole`:

```go
func aggregateByRole(rows []orchpanel.AgentEnhancedRow) []orchpanel.AgentEnhancedRow {
    // Group by Role. Sum TokensIn/Out/CacheRead/CacheCreation/ToolCalls.
    // Keep the first Role, Model, Provider, Thinking, Status for the group.
    // The Status shown is the most-recently-active among the group members
    // ("running" > "idle" > "finished").
    // Return in first-seen order of roles.
    type acc struct {
        row orchpanel.AgentEnhancedRow
        seenFirst int        // index in original order
    }
    byRole := map[string]*acc{}
    for i, r := range rows {
        if a, ok := byRole[r.Role]; ok {
            a.row.TokensIn   += r.TokensIn
            a.row.TokensOut  += r.TokensOut
            a.row.CacheRead  += r.CacheRead
            a.row.CacheCreation += r.CacheCreation
            a.row.ToolCalls  += r.ToolCalls
            a.row.Turns      += r.Turns
            // Upgrade status: "running" > "idle" > "finished"
            if r.Status == "running" || (r.Status == "idle" && a.row.Status == "finished") {
                a.row.Status = r.Status
            }
            // Keep the latest ContextEstimate/ContextMax (most recent data)
            if r.ContextMax > 0 {
                a.row.ContextEstimate = r.ContextEstimate
                a.row.ContextMax = r.ContextMax
                a.row.ContextAutoMax = r.ContextAutoMax
            }
        } else {
            byRole[r.Role] = &acc{row: r, seenFirst: i}
        }
    }
    // Return in first-seen order.
    out := make([]orchpanel.AgentEnhancedRow, 0, len(byRole))
    for _, r := range rows {
        // Using a secondary map to emit once per distinct role in order
    }
    ...
}
```

**Important**: `aggregateByRole` means the view's `Label` field (which
carries the disambiguated "Coder·2") is replaced by the bare `Role`
("coder"), title-cased to "Coder". The `·N` suffix disappears from the
footer (which is exactly what "minimal agents" asks for). The view's
internal rows remain unchanged (still per-handle); the footer just
displays them grouped.

### 6. Suppress redundant lines during orchestration

**`tui/footer_render.go`** — in `Footer.Render`, when orchestration stats
are present, suppress the chrome lines that are redundant:

```go
if f.data.OrchestrationStats != "" {
    // During orchestration: only show the per-agent lines (no workdir/mode,
    // no main model — each agent line carries its own model).
    orchLines := f.renderOrchStatsLines(width, styler)
    if len(orchLines) > 0 {
        return orchLines
    }
}
// Normal (non-orchestration) path: workdir/mode + main stats/model.
lines := []string{styler(line1), styler(line2)}
lines = append(lines, ...)
return lines
```

This is the simplest correct fix. When `OrchestrationStats` is non-empty,
the footer renders ONLY the per-agent lines (styled, width-fitted). When
empty, it shows the normal 2(+spacer) lines.

The "rogue `.│`" disappears because line 1 is never rendered during
orchestration.

### 7. `formatFooterLine` file location and import wiring

The new shared function lives in a new file `internal/app/footer_line.go`
(package `app`) so it's importable by both orch_tabs.go and the footer
render (in package `tui`). Wait — `formatFooterLine` is called by `tui/footer_render.go`
(package `tui`) AND by `internal/app/orch_tabs.go` (package `app`). If it lives in
`internal/app`, package `tui` cannot import it (would create a circular dep).

**Options:**
a. Put `formatFooterLine` in `tui/` (package `tui`). Then `internal/app/orch_tabs.go`
   can call `tui.FormatFooterLine(...)`. The normal footer render
   (`tui/footer_render.go`) also calls it internally. No circular dep.
b. Put `formatFooterLine` in `internal/app/` and have the footer render call
   through a callback/hook. Over-engineered.
c. Keep `formatFooterLine` in `internal/app/` and have `updateOrchFooterStats`
   set `OrchestrationStats` with already-formatted lines (which is what
   it does now). The footer render just displays them. **But** `formatFooterLine`
   would duplicate `formatModelPart` from the footer.

**Recommendation**: Option (a). Move `formatFooterLine` into `tui/` (package
`tui`). This is the natural home since `formatModelPart` is also in `tui/`.
`internal/app` calls `tui.FormatFooterLine` via explicit function call.

Implement:
- In `tui/`, extract `formatModelPart` from a `*Footer` method to a
  **package-level** function with explicit parameters.
- Add `FormatFooterLine` (exported) in the same package.
- Since `buildFooterStatParts` and `sessionStats` are in `internal/app`,
  `FormatFooterLine` cannot use `sessionStats` directly (circular dep).
  Instead, `FormatFooterLine` takes the individual stat fields as arguments,
  OR we extract `sessionStats` + `buildFooterStatParts` into their OWN
  package (e.g. `internal/app/stats` or just keep them in `internal/app`
  and have the per-agent line still build the stats parts via
  `formatFooterStats` in `internal/app` and pass the resulting string to
  `FormatFooterLine`).

**Simpler approach**: `formatFooterLine` stays in `internal/app` as a wrapper
that calls `buildFooterStatParts` (already in `internal/app`) and calls the
extracted model formatter (which would need to be moved to `tui` or copied).
Since the per-agent line is built in `internal/app/orch_tabs.go` already,
the model part is the only piece needing the `tui` formatter. Extract a
small `FormatModelPart(model, level, activity string, busy, active bool) string`
in `tui/` (lowercase, shared), and have `formatFooterLine` in `internal/app`
call it. No circular deps.

### File-by-file change list

| File | Changes |
|------|---------|
| `core/orchestrator/handle.go` | Add `ContextEstimate int`, `ContextMax int`, `ContextAutoMax bool` to `AgentStats` and `AgentStatsSnapshot`. Add `SetContext(...)` method. |
| `internal/app/orchestrator_adapter.go` | Add `EventContextStats` case to `applyOutputEvent` switch. Implement `applyContextStats`. |
| `core/orchestrator/runtime.go` | Add `context_estimate`, `context_max`, `context_auto_max` to `statsPayload`. |
| `tui/orchestrator/event.go` | Add `ContextEstimate`, `ContextMax`, `ContextAutoMax` to `AgentStatsDelta`. |
| `tui/orchestrator/view.go` | Add same three fields to `AgentEnhancedRow`. Copy in `applyRowEv`. |
| `internal/app/orch_view_source.go` | Propagate context fields from payload to `AgentStatsDelta` in `translateOrchEvent`. |
| `tui/footer_render.go` | Extract `formatModelPart` to package-level exported function with explicit params. Add `FormatFooterLine` (exported). In `Footer.Render`, when `OrchestrationStats != ""` → only render per-agent lines (skip line 1, line 2). When idle → unchanged. |
| `internal/app/orch_tabs.go` | Add `aggregateByRole`. Update `updateOrchFooterStats`: dedup by role, cap at last 5 roles. Replace `formatOrchAgentLine` to call `FormatFooterLine` with role aggregation data. Delete old `cacheField`, `formatK`, `orchProviderModel`, `titleFirst`. |
| `tui/footer_orchestration_test.go` | Update to test the new footer suppression logic (no line 1/line 2 during orch). |
| `internal/app/orchestrator_view_forwarder_test.go` | Might need updates if the footer node Text changes structure (now no line 1/line 2 during orch). |
| `internal/app/orchestrator_tabs_filmstrip_test.go` | Same. |

### Testing strategy

Each step should be testable in isolation:

1. **Context propagation** — write a unit test that emits `EventContextStats`
   through the observer and verifies the handle's `Stats.Snapshot()` returns
   the context values (test in `internal/app/orchestrator_adapter_events_test.go`).

2. **Shared footer line** — test `FormatFooterLine` with known inputs and
   assert the output string contains the expected stats, model, thinking,
   and that `active=true` produces green SGR codes. (Test in `tui/`).

3. **Footer suppression** — test `Footer.Render` with orchestration stats
   set: assert the output has NO workdir/mode line, NO main model line,
   only the per-agent lines + (when no orch) has the normal 3 lines.
   (Test already exists in `tui/footer_orchestration_test.go` — extend it.)

4. **Role aggregation** — test `aggregateByRole` in isolation with multiple
   rows for the same role + different roles, verify tokens are summed,
   status upgraded, count = unique roles. (Test in `internal/app/`.)

5. **End-to-end** — the existing `TestOrchestratorViewForwarder_RendersSimplifiedView`
   and `TestOrchestratorView_Filmstrip_PersistenceAndFooterStats` assert
   footer content. Update them to assert per-agent lines with context
   estimate present and no `.│`/main-model lines during orchestration.

### What NOT to touch

- `multiagent.DelegateTool` (foreground orchestrator) and `multiagent.AgentTool`
  (pair/swarm) — separate features, not part of the `/orchestrate` hub.
- The view's internal row model (`v.rows` keyed by AgentID) — unchanged.
  The footer aggregates by role at display time, not by modifying the view.
- `core/orchestrator/pool.go` and `runtime.go` — the `AcquireOptions{Fresh}`
  threading from the earlier fix stays; this plan builds on top of it.
- The diagnostics export (`RunSnapshot`) — shows all handles, which is
  correct for debugging. The footer display is a separate concern.

### Risk notes

- The `formatModelPart` extraction from a `*Footer` method to a pure function
  must be done carefully: the method accesses `f.data.*` fields (model, level,
  activity, busy from footer data). The new function takes them as explicit
  params. The footer's own usage of `formatModelPart` (in `buildMainModelDisplay`,
  `buildCompanionMainPart`, etc.) must be updated to call the new function.
  These callers all have the data in scope via `f.data.*`, so the change is
  mechanical.
- After extracting `formatModelPart`, the `buildModelDisplay` / `buildMainModelDisplay`
  / `buildCompanion*` / `compactRightSide` / `strip*` / `companionVis` chain
  becomes dead code. Delete it only after all tests pass.
- The `aggregateByRole` function loses granularity (separate stats per handle).
  The user explicitly wants "minimal agents", so one line per role is the goal.
  If per-handle granularity is ever needed later, the view still has the raw rows.
- Context stats per agent depend on the agent emitting `EventContextStats`
  during its turn. If a provider doesn't emit this event (unlikely — the main
  agent path relies on it), context will be absent as before. No regression.
