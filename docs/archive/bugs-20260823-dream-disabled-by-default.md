# Bug fix report: Dream is not wired into the agent session

Date: 2026-08-23 · Branch: feature/ratatui · Status: CLOSED

## Symptom (bugs.md `# To fix`)

Memory consolidation (dream) only ran as a standalone CLI mode (`--dream`) or
via the app-layer scheduler; it was not wired into the running agent/session
lifecycle. Meanwhile the embedded dream skill WAS reachable from the model:
it appeared in the `<available_skills>` catalog (frontmatter flags default to
invocable when omitted) and could be executed through `run_skill` — an
advertised-but-half-wired surface.

**Expected:** Dream either works end-to-end where advertised or stays fully
disabled by default until wired.

## Resolution: fully disabled by default on every agent-facing surface

Per the expected behavior, dream is now fully disabled by default on all
agent/session surfaces while explicit, documented user entry points remain:

1. **Skill hidden from the agent.** The embedded dream skill ships
   `hidden: true` (skills/dream/SKILL.md). The loader now parses
   `Meta.Hidden` and every agent-facing predicate honors it:
   - `Skill.IsModelInvocable()` / `SkillSummary.IsModelInvocable()`: requires
     BOTH invocation flags AND NOT hidden → dream never enters the
     `<available_skills>` catalog (skills/prompter.go filterModelInvocable);
   - `run_skill` refuses hidden skills outright (skills/runner.go): hidden
     skills stay LOADED for internal features that fetch them by name via
     `Get`, but are never executable by the agent;
   - sticky-skill banner excludes hidden skills;
   - `DefaultEmbeddedOffNames` keeps hidden skills out of the default-off
     enable list (they are internal-only, so toggling them for the agent is
     meaningless).
2. **No auto-dream scheduler by default.** `newDreamScheduler` returns nil
   unless BOTH `memory.dream.enabled` AND `memory.dream.auto` are set; the
   shipped defaults are both false → no goroutine, no timers.
3. **Explicit surfaces remain advertised & working**: `/dream[:apply|:review|:status]`
   (user command, help-documented, gated on memory.enabled), `--dream` /
   `--dream-apply` CLI modes. Auto consolidation requires explicit opt-in via
   config.

## Test approach & validation (all passing)

- `internal/app/dream_disabled_test.go`:
  - `TestDreamScheduler_NilWithShippedDefaults` — cascade load of shipped
    defaults → dream disabled; scheduler nil; enabled+auto required (either
    alone insufficient);
  - `TestDreamSkill_HiddenFromAgentSurfaces` — hidden skill loaded but absent
    from the rendered `<available_skills>` section while normal skills still
    appear;
  - `TestDreamEmbeddedSkill_IsHidden` — the SHIPPED embedded dream skill has
    `hidden:true`, never model-invocable.
- `skills/runner_test.go`
  `TestHiddenSkill_NotModelInvocableAndRejectedByRunTool` — registry keeps a
  hidden skill loaded, catalog omits it, run_skill rejects it.
- Existing suites green: ./skills, ./core, ./internal/app.
- Quality gates run separately: go vet / staticcheck / gocognit / gocyclo /
  race-cover — same profile as baseline (pre-existing unrelated warnings
  noted in bugs.md guideline).

## Closure

Dream no longer leaks into the running agent's surfaces by default; it works
exactly where advertised (/dream, --dream) and stays disabled everywhere else
until wired end-to-end. Closed 2026-08-23.
