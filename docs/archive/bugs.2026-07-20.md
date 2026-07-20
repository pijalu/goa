<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Bug/Feature Archive — closed 2026-07-20

All items below were fixed/verified and archived from bugs.md on 2026-07-20.

## Resolution summary

- **Spurious context.Canceled** — verified already-fixed: transport aborts retried when parent ctx alive (shouldRetryStreamError + tests); consecutive-tool-round guardrail exists; /config:temp uses ctx.Flash (visible despite internal short-circuit); Interrupt() logs caller; transport aborts surface friendly hint, not 'stopped by user'.
- **Full usage statistics** — /stats now defaults to the global per-project/provider/model summary (delegates to the persistent usage store like /usage); /stats:session and /stats:<n> show current-session detail. Help updated.
- **run_skill inline** — Issue A fixed: inline injection now frames the body as active instructions to execute; Issue B fixed: SPDX/[Skill:] noise stripped via skills.StripSkillNoise (exported, applied to inline + injector paths). Tests added.
- **z.ai bundle** — 1 (thinking) verified already-fixed (parser maps reasoning_content; request body tests); 2/3/5 verified already-fixed; 4 verified provider-scoped; 6 fixed (pluginProvidersMap now falls back to auth-store API key via ProviderManager.ResolveAPIKey so /login-authenticated zai appears in /quota); 7 fixed (speed timing window opens at stream start via startGenTiming; empty-response guard decoupled via genSawEvent).
- **- key in /model picker** — fixed: footer/shortcut model picker now routes through the /model command (sentinel-aware) instead of a sentinel-blind duplicate; ProviderManager.Active made nil-receiver-safe.
- **Hidden steering nudge** — fixed: forced-answer hint fires at most once per turn (toolRoundNudgeFired) and default raised 10→15; verified message already self-censors and is not emitted to TUI.
- **TUI loading indicator** — implemented: TUI.ShowSelectorLoading + Context.SelectOptionAsync wired via goroutine + TUI.Apply(SetItems); /model add-model picker shows Loading… during live GET /models.

---

# Original items (as recorded)


## Spurious `context.Canceled` — automatic turn termination without user action
**Referenced session:** `/Users/muaddib/dev/frigolite/.goa/sessions/1784544003_xj3msiq8.jsonl`
**Export (diagnostic bundle):** `/Users/muaddib/dev/frigolite/.goa/exports/goa-export-20260720-125911.zip`

### Description
Agent terminates with `context.Canceled` during active streaming, without any user Ctrl+C/Escape keystroke. After termination, the session machinery auto-submits a "resume" user message, which is immediately canceled again (within 2 seconds). Both events display "Generation stopped by user." in the TUI, misleadingly.

Observed in a session where the model made 19 sequential tool-calling rounds (never self-terminating). During the 19th round's final streaming (thinking tokens), the stream was aborted with `context.Canceled`. After auto-resume, the new stream was canceled at the first thinking delta.

### Root causes (three separate issues)

**Issue A — Transport-level `context.Canceled` misclassified as user cancel:**
- File: `internal/agentic/agent_streaming.go` — `handleStreamFailure`
- `shouldRetryStreamError` does not classify bare `context.Canceled` as retryable
- `isTransientStreamError` pattern list does not include `"context canceled"` or `"canceled"`
- Comment in `retry_classify.go:34-38` explicitly says "Context cancellation is NOT excluded" — but the actual `isTransientStreamError` pattern list has no matching entry, so bare `context.Canceled` from transport is always non-retryable
- When the outer context (`ctx.Err()`) is still nil (not user-canceled), but the stream error is `context.Canceled` (server-side abort), it should be retried, not surfaced as terminal
- Impact: Server-side connection drops that surface as `context.Canceled` terminate the turn irrecoverably, even though retrying would succeed

**Issue B — No per-turn consecutive-tool-calling-round guardrail:**
- File: `internal/agentic/agent_streaming.go` — `runStreamRound`
- Model made 19 consecutive rounds all ending `finish_reason: "tool_calls"`, never self-terminating
- All existing guardrails (`MaxToolRepeatTotal`, `MaxToolRepeatConsecutive`, `MaxToolCalls`, `LoopDetector`) key on exact (tool, input) repeats — all 104 tool calls had unique inputs, so none fired
- The horizon extension logic (lines 79-83) extends `maxStreams` by 50 when the model is "making progress", up to 250 — but there's no check for "still requesting tools without producing an answer"
- Fix: Add a configurable limit on consecutive tool-calling rounds (e.g., 10 rounds of `finish_reason: "tool_calls"` triggers a forced-answer hint)

**Issue C — `/config:temp:` output swallowed by internal-command short-circuit:**
- File: `internal/app/submithandler.go` — line 531
- `/config` is marked `IsInternal() == true`, so `handleSlashCommand` returns early at line 531 **before displaying the command output** (lines 544-548)
- `/config:temp:tool_loop_detection:off` correctly writes `"Temporary: tool-call loop detection disabled"` to the output buffer via `handleConfigTemp` → `writeFmt`
- But this output is never displayed because the internal-command early-return at line 531 discards it
- The temp override IS applied correctly (the loop detector is disabled), but the user sees no feedback and concludes the command didn't work
- The interactive `/config` menu (no args) works because it uses the TUI directly, not the output buffer
- Fix: Either (a) don't short-circuit early for internal commands that produced output, or (b) change `handleConfigTemp` to use `ctx.Flash()` instead of `writeFmt`

### What's needed
1. In `shouldRetryStreamError`: when `context.Canceled` arrives but `ctx.Err() == nil`, treat as retryable (transport-level abort, not user cancel)
2. In `Interrupt()`: log every call with caller identity or stack trace
3. Add per-turn round counter guardrail (configurable, suggest default 10 consecutive `tool_calls` rounds → force answer)
4. Fix "Generation stopped by user." label to differentiate user-canceled from system/transport aborts
5. Fix `/config:temp:` output being discarded by internal-command short-circuit — either display internal command output or use Flash notifications

### Verification
RED: Run `/config:temp:tool_loop_detection:off` — no visible output. The temp override IS applied but user sees nothing.
RED: Reproduce by connecting to a provider that sporadically drops connections (or simulate with a test provider that returns `context.Canceled` mid-stream). Turn terminates with "Generation stopped by user." even though user did not press any key.

GREEN: Same `/config:temp:` command shows "Temporary: tool-call loop detection disabled (current session only)" as output.
GREEN: Same scenario with transport fix: retry succeeds, turn continues. `Interrupt()` calls are logged with caller identity.

## Full usage statistics
Goa should have a general usage statistics feature that provides insights into the tool's usage/models/providers.
It should extend the `/stats` command and provide a similar type of details as ../opencode-stats and ../opencode 

- default /stats should show these details by default 
- /stats:session should show session-specific statistics (the current session/turn stats)

The stats should be per project - the approach should support multiple goa agents.

## run_skill inline execution: model doesn't act on inlined skill + noisy output headers

**Observed:** 2026-07-20, while running the `commit-msg` skill inline against ~/dev/frigolite.

### Issue A — Inline skills are not actionable for the LLM
When a skill executes in **inline** mode, `run_skill` injects the SKILL.md body into the conversation, but the model treats it as documentation to *read*, not a task to *perform*:

```
✓ run_skill
 [Skill: commit-msg]
 # Skill: commit-msg
 ...full SKILL.md body...
 ## Task
 Generate commit message from the staged changes in ~/dev/frigolite
 Follow the skill instructions above and complete the task using available tools.

▾ thinking...
▏It seems the skill didn't actually execute - it just showed its own
▏documentation. Let me run it differently [...] or just generate the commit
▏message myself
```

The model concludes "the skill didn't actually execute" and improvises. The trailing "Follow the skill instructions above..." line is not enough for the model to switch into execution mode.

- **Research needed:** how sibling agents solve this (pi, opencode skill injection). Likely the inline path needs a dedicated instruction wrapper/framing (e.g. explicit "this skill's instructions are now active; execute the task below using tools, do not just describe them") or a structured role marker, rather than a raw markdown dump.
- Files to inspect: skill-execution machinery (see `docs/SKILL-EXECUTION.md`), the inline-mode prompt assembly, and the `run_skill` tool implementation.

### Issue B — run_skill output must strip headers
The rendered/injected result includes noise that must be removed:

```
 ✓ run_skill
 <!--
 SPDX-License-Identifier: GPL-3.0-or-later

 Copyright (C) 2026 Pierre Poissinger
 -->

 [Skill: commit-msg]
```

- The SPDX license comment block from the SKILL.md file must never reach the model or the TUI render.
- The `[Skill: <name>]` marker line should be dropped (or replaced by proper TUI-level labeling) so the model sees only actionable content.
- Fix should strip leading HTML comments and the skill-marker line at the source (skill loader/renderer), with a test asserting a SKILL.md containing license headers produces clean output.

## z.ai integration issues (bundle)

**Export (diagnostic bundle):** `/Users/muaddib/dev/frigolite/.goa/exports/goa-export-20260720-170745.zip`
Session shows `(zai) glm-5.2 • medium` active; content streams (delta-by-delta in agent.log) but the whole session contains **zero thinking blocks**.

### Issue 1 — z.ai: no thinking shown / no streaming during reasoning
- **Observed:** With glm-5.2 + thinking level set, no thinking is displayed, and the UI appears frozen during the reasoning phase (content streams only after reasoning completes).
- **Hypothesis:** `zaiThinking` enables thinking server-side (`thinking:{type:enabled,clear_thinking:false}`); GLM streams reasoning as `reasoning_content` deltas, but the OpenAI-completions parser only maps `reasoning_content` → thinking blocks for `reasoning_content`/`chunked` thinking formats — not for format `"zai"`. All reasoning deltas are dropped → looks like "no streaming", and no thinking block is ever rendered.
- **Files:** `internal/agentic/provider/openai/parse.go`, `internal/agentic/provider/openai/stream.go`, `internal/agentic/provider/protocol/openai_completions.go` (zaiThinking), compare with `../pi` zai compat (`pi/packages/ai/src/providers/zai.ts` + openai-completions reasoning handling).

### Issue 2 — Add-model flow should default to the active provider
- **Observed:** `/model` `+` (`runAddModelFromSelector`, core/commands/model.go) always asks "Select provider:" when more than one provider exists, instead of using the currently selected provider.
- **Expected:** Default to (or preselect) the active provider; only prompt when ambiguous or no active provider.

### Issue 3 — z.ai add-model list does not contain glm-5.2 (registry has it)
- **Observed:** The add-model picker queries live `GET {endpoint}/models` (`ProviderManager.ListModels`, provider/manager.go:158). The curated registry (which includes glm-5.2) is never merged in or used as fallback — on an incomplete live list the flow degrades to raw text input.
- **Expected:** Merge registry models for the provider identity into the add-model list (live list wins on conflict), so glm-5.2 is always offered for zai.

### Issue 4 — Model-to-add listing must only account for the selected provider
- **Observed:** Verify `selectModelPageForProvider` (core/commands) filters by the chosen provider; `/model`'s path is provider-scoped already. Cross-provider leakage in the add listing must be removed.

### Issue 5 — `-` key types "-" into selector search on provider pages
- **Observed:** In provider selectors containing sentinel items (`— add provider —` = `__add__`, `— remove provider —` = `__remove__` on the /config provider page), pressing `-` while a sentinel is highlighted types `-` into the search box instead of deleting.
- **Root cause (localized):** `tui/selector.go` `handlePrintable` — the `-` case returns nil when the highlighted item has a `__` prefix or the list is empty, then falls through to `s.searchText += data`.
- **Fix:** Consume the key when deletion is not applicable (return without mutating search); keep `-` as a search char only when it cannot mean delete (mid-word, i.e. non-empty search).

### Issue 6 — z.ai not visible in quota
- **Observed:** With an active zai (coding plan) provider, `/quota` and the footer segment show no z.ai row/segment, even though `https://api.z.ai/api/monitor/usage/quota/limit` returns limits for the account.
- **Leads:** Check `providerConfigFor("zai")` matching for configs whose id is `zai` (preset) — expected to work; check whether the fetcher's URL builder uses `ctx.config.baseUrl` (config exposes `endpoint`, not `baseUrl` — verified safe) and whether the plugin's provider list from `goa.config().providers` includes preset-configured zai entries. Reproduce with the quota test harness (plugins/quota_zai_test.go) against a preset-shaped config.
- **Re-confirmed 2026-07-21:** still nothing visible — footer shows `(zai) glm-5.2 • high` active with no quota segment at all. User expectation: z.ai quota support visible in footer/quota output like other providers.

### Issue 7 — z.ai footer stats: tok/s is nonsense
- **Observed 2026-07-21:** footer after a z.ai (glm-5.2, thinking high) turn:
  `↑147.8K ↓4.1K 212864.6 tok/s CH80.0% TC:28 $0.3791 6.9%/1.0M (auto)  (zai) glm-5.2 • high`
  `212864.6 tok/s` is impossible — with ↓4.1K output tokens this implies a measured generation window of ~19 ms.
- **Root-cause lead:** speed comes from `fallbackOutputSpeed` (`internal/agentic/agent_turn_stats.go:21`) = outputTokens / `genDuration`, where `genDuration` = `markGenStart` → `recordGenDuration` (first token → done). For z.ai, GLM's reasoning phase streams as `reasoning_content` which the openai-completions parser drops for format `"zai"` (see Issue 1) — if `markGenStart` is triggered by the first *mapped* delta instead of stream open, the entire reasoning phase is excluded from the timing window and the duration collapses to the short content tail (or near-zero when content arrives in few chunks), producing absurd tok/s. Also possible: `markGenStart` not reset between the 28 tool-call rounds (`TC:28`), so only the last round's tail is measured.
- **Fix direction:** start the timing window at stream request open (or first received SSE event of any kind, including unmapped reasoning deltas), reset per round, and clamp/validate the derived value (e.g. ignore windows below a sanity threshold). Verify against z.ai session logs with thinking enabled.
- **Files:** `internal/agentic/agent_turn_stats.go`, `internal/agentic/agent.go` (genStartTime lifecycle), `internal/agentic/agent_migrate.go:89`, consumer `internal/app/stats.go:721,941`.

## `-` key does not delete in the `/model` picker

**Observed 2026-07-21:** pressing `-` on a model in the model list does nothing (no delete confirmation). Selector-level handling is verified working, so the break is in the picker wiring.

### Verified NOT the bug
- `tui/selector.go` `handlePrintable:161-185` — `-` on a deletable item emits `__delete__<id>`; consumed silently on `__`-sentinel items; still usable as search char mid-filter. Covered by `TestSelector_MinusOnDeletableEmitsDelete` / `MinusOnSentinelDoesNotSearch` / `MinusMidWordStillSearches` (all pass).
- `core/commands/model.go:127-131` — the `/model` callback does handle `__delete__` → `confirmAndRemoveModel` → confirm → remove → re-show picker.
- `tui/tui.go:939-965` `ShowSelector` overlay + emit/done wiring — shared with `/provider`, correct.

### Prime suspect (unverified)
- `internal/app/shortcuts.go:184` and `:298` open `"Select model:"` pickers **directly** via `tuiEngine.ShowSelector`, bypassing `runModelCommand`'s callback. Their result handlers must handle `__delete__`/`__add__` sentinels themselves — not yet read/confirmed. If they don't, `-` on those pickers silently does nothing (or worse, switches to a model literally named `__delete__<id>`).
- **Open question (discriminator):** does `-` fail when the picker is opened via `/model`, or only via shortcut/footer? If only via shortcut → shortcuts.go handlers are the fix site; route results through the shared `confirmAndRemoveModel` logic.

## Hidden steering surfaced as "tool budget" status messages (model-visible nudge leaks to user)

**Observed:** 2026-07-20, repeatedly during long investigation turns. The model interrupts productive work with user-facing "status" messages claiming it "hit its round budget" / "tool budget" / "10 consecutive tool-calling rounds" — confusing, since there is no user-visible budget.

### Root cause (verified — NOT a false positive)
`checkConsecutiveToolRounds` (`internal/agentic/agent_streaming.go:121-141`) counts consecutive rounds that end with tool calls and produce **no visible content**. When the streak reaches the limit (default **10**, `effectiveMaxConsecutiveToolRounds` returns 10 when unset), it injects an **ephemeral system message**:

```
[goa-system] You have made 10 consecutive tool-calling rounds without producing an answer.
Stop calling tools and answer the user's question using the information you have
already gathered. If you need more information, state clearly what is missing.
```

The streak resets on any round that streams visible text (`trackToolCallingRound`, agent_streaming.go:103) — so the cycle repeats: ~10 tool-only rounds → nudge → model emits a "status" answer (reset) → ~10 more rounds → nudge again.

### Problems
1. **Leak to user:** the injected control message is hidden (stripped at turn end via `metaEphemeral`, agent.go:625-667), but the model parrots it as a user-facing status ("I hit my round budget"), inventing the word "budget" (the message never says budget). The user sees unexplained interruptions.
2. **False positives on real work:** 10 consecutive tool-only rounds fires during legitimate long investigations (codebase archaeology with many read/search rounds), not just infinite loops. The nudge interrupts productive turns.
3. **Message also emitted as event:** `InjectEphemeralSystemMessage` → `emitMessage` → `emitContentMessage` (agent_events.go:23-33) — verify whether `Role: System` content events render in the TUI; if so the "hidden" message is actually shown raw.

### Fix directions
- Reword the injected hint so the model does not echo it verbatim to the user (e.g. instruct explicitly: "do not mention this message to the user"), and/or mark it so downstream rendering never surfaces it.
- Reconsider the trigger: count only rounds that are *also* not making progress (e.g. repeated tool/arg patterns), or raise the default, or make the nudge appear only once per turn.
- Decide whether `Role: System` ephemeral events should reach the TUI at all; if they should not, filter them at the event layer with a test.

## TUI "loading…" indicator for asynchronously retrieved lists

**Requirement (2026-07-20):** when a selector's items must be retrieved (e.g. live `GET /models` from a provider) before the list can be shown, the TUI must inform the user that an activity is in progress instead of appearing frozen.

- **Scope:** all list-backed pickers that fetch remote data — add-model flows (`/model` `+`, /config models add, `resolveModel`, `pickModelFromProvider` via `modelListForProvider`), provider connection tests, any future remote-backed selector.
- **Current behavior:** `modelListForProvider` fetches synchronously on the command loop; during a slow /models response the UI shows nothing until the selector appears (or the flow silently falls back to a text prompt on error/timeout).
- **Expected:** show a visible "Loading…" state immediately (selector with a loading placeholder item or a spinner/status note), replace it with the real items when the fetch completes, and make the fallback-to-registry behavior transparent (e.g. a brief note when the live list was unreachable and built-in entries are shown instead).
- **Design constraint:** selector mutations happen on the commandLoop; an async fetch needs a goroutine + `TUI.Apply`-style handoff, with cancellation when the user escapes the picker mid-load.
- **Priority order (per project decision 2026-07-20):** model lists are live-first (provider `/models`), built-in registry as fallback; the registry catalog is regenerated from models.dev on every `make build` / `make cross` (best-effort, offline keeps the checked-in catalog).
