# Known bugs & features


## BUG: full UI freeze/deadlock after unattended run — stderr crash-log tee deadlocks on a stalled/asleep terminal (2026-08-27) [FIXED]

**Observed:** after a long unattended goal run that ended in a provider 429 (all 15 retries exhausted → goal auto-paused with "Paused after provider connection error"), the whole TUI froze and never recovered. The process (`goa`, e.g. PID 12860 on `/Users/muaddib/dev/frigolite`) stayed alive for 7+ hours with 0% CPU, the screen showing the final paused state (`◦ Goal paused by the system`). The UI was completely unresponsive. SIGQUIT produced NO goroutine dump.

**Forensic evidence (lldb / sample / lsof on the live process):**
- Main thread pinned at `libsystem_kernel write+8` in a *userspace livelock* (PC never advances, write byte-count varies) — NOT a one-shot kernel block.
- `crash.log` (fd 4) stopped growing at the exact pause timestamp (04:00:10) despite the looping write.
- **No thread was reading fd 6** — the read end of the process's stderr self-pipe. The four read-blocked threads were on fds 0 (stdin), 43, 54, 55 (tool/child pipes), never fd 6.
- Two CHILD goa processes (PIDs 30680, 31774 — spawned by 12860, e.g. sub-agents/background `goa`) were both blocked in `write()` to **fd 2 → the SAME stderr pipe** (via macOS `MallocStackLogging` warnings they emit to inherited stderr).
- Ghostty (the PTY master, PID 1083) was healthy (16.9% CPU, in `mach_msg_overwrite`), draining its other windows — so this is NOT a dead terminal; it is a goa-internal deadlock.
- `pmset`: `displaysleep 10`, `powernap 1` — the display had slept during the unattended run.

**Root cause — the stderr crash-log tee has a single point of failure that backpressures the whole process:** `setupCrashLog` → `teeStderr` (`internal/app/crash_log_unix.go`) dup2's fd 2 through an `os.Pipe` so runtime fatal errors (which bypass `os.Stderr` and write straight to fd 2) are captured. A SINGLE drain goroutine (`io.Copy(r → stderrSink)`) is the only consumer of that 64KB pipe. `stderrSink.Write` wrote to the crash.log file AND, when `!OwnsScreen()`, to the saved original-stderr fd (the TTY) via a **blocking** `os.File.Write`.

Trigger sequence: display sleeps → the terminal emulator stops being serviced → the saved-TTY write stalls → **the sole drain goroutine blocks inside `stderrSink.Write`** → the stderr pipe fills (children's `MallocStackLogging` output + the parent's own `log`/panic writes) → **every fd-2 writer blocks**, including the Go runtime's fatal/dump path. That is why SIGQUIT gave no output: the dump writes to fd 2 → the full pipe → it deadlocks too. The freeze is **self-sustaining** — it persisted even after the display woke, because the drain goroutine was permanently wedged, so the pipe never drained.

**Fix (implemented 2026-08-27):** make the tee drain *unblockable*. `internal/app/crash_log_unix.go`: the saved-stderr descriptor is set non-blocking and `stderrSink` now holds the raw fd (`ttyFd`) and forwards with `unix.Write` directly (NOT `os.File.Write`, which would park on Go's netpoller for a poller-managed fd). On EAGAIN/EWOULDBLOCK or a short write the sink DROPS the bytes — the crash log always receives every byte, so nothing diagnostic is lost; the TTY echo is best-effort. The drain can therefore never block, so the pipe always drains and no fd-2 writer (runtime, panic handler, child process) can ever deadlock on it. Cleanup restores the saved descriptor's blocking mode before handing it back to fd 2.

**Tests (RED→GREEN):** new `internal/app/crash_log_unix_test.go` — `TestStderrSink_WriteDropsOnFullTTY` drives `stderrSink.Write` against a full, never-drained raw pipe; with a blocking write it deadlocks (fails on a 5s timeout), with the fix it returns promptly and the crash log still captures the bytes. Also fixed a pre-existing data race in `TestTeeStderr_GatedWhileTUIOwnsScreen` (the `tuiOwns` bool was read by the drain goroutine via the `ownsScreen` closure while the test goroutine flipped it — now `atomic.Bool`). Validation: `go vet` clean; `go test -race` on `internal/app` passes 5×; gocognit/gocyclo clean on touched files.

**Note / remaining hardening opportunity (not a bug, by design):** the renderLoop holds `compositor.mu` across the `os.Stdout.Write` to the TTY (`Compositor.Render` → `writeFrame`). During a display-sleep TTY stall that write blocks, so *rendering* freezes for the duration of the sleep — unavoidable (you cannot draw to an unserviced terminal). The important property restored by the fix above is RECOVERY: once the display wakes and the PTY drains, the blocked stdout write returns and the UI resumes. Previously recovery never happened because the secondary stderr-pipe deadlock outlasted the display stall.


## BUG: Alt+T (cycle thinking level) does nothing on macOS — the only built-in Option shortcut missing its Option-char alias (2026-08-27) [FIXED]

**Observed:** on macOS, pressing the key binding for "cycle thinking level" does nothing. The binding is **Alt+T** (NOT Shift+Tab — Shift+Tab is multi-agent *previous tab*, see `KbAgentTabPrev`). The user report ("shift-tab does not switch thinking level") was the same underlying gap: the documented/expected thinking-level hotkey was dead on Mac.

**Root cause (two parts):**
1. **Binding expectation:** thinking-level cycling is bound to `alt+t` (`KbCycleThinkingLevel`, `tui/tui_keys.go` appShortcuts). Shift+Tab is bound to previous-multi-agent-tab (`resolveAgentTabCycle` → `OnAgentTabPrev`) and correctly stays that way.
2. **The Mac defect:** on macOS with the default keyboard layout, Option+letter delivers the literal Option character, not an ESC-prefixed sequence (Option+t → `†` U+2020). goa handles this via `optionKeyAliases` (`†` → `alt+t`) consulted in `resolveAppShortcut` as `altKey`, matched against each shortcut's `altAlias` field. **The `alt+t` entry was the ONLY built-in alt shortcut missing `altAlias`** (its neighbors `alt+e`/`alt+m`/`alt+o` all have it). `matches` thus never matched the alias path and Option+t silently fell through to the editor. The multi-agent tab aliases (Alt+[/Alt+] and Alt+1..9) had the same gap — their resolvers never received the alias at all.

**Fix (implemented 2026-08-27):** `tui/tui_keys.go` — added `altAlias: "alt+t"` to the thinking-level shortcut; and threaded the computed `altKey` alias into `resolveAgentTabCycle(key, altKey)` and `resolveAgentTabJump(key, altKey)` so Option+[/Option+]/Option+<digit> resolve via their Option characters (`“`/`‘`/`¡™£¢∞§¶•ª`) exactly like the ESC-prefixed forms. The alias is only consulted for *shortcut matching* — it is NOT applied to text input, so typing an unbound Option char (e.g. Option+p → `π`) still inserts the literal character into the editor.

**Tests (RED→GREEN):** new `tui/tui_keys_test.go` — `TestAppShortcut_AltThinkingLevel_MacOptionAlias` (Option+t `†` fires OnCycleThinkingLevel), `TestAppShortcut_AltAliases_AllBuiltins` (every built-in alt shortcut resolves via BOTH the ESC form and its Option char — would have caught the missing altAlias), `TestAppShortcut_AgentTabCycle_MacOptionAliases` (Option+[ / Option+]), `TestAppShortcut_AgentTabJump_MacOptionAlias` (Option+2). Verified RED (alias removed → tests fail) then GREEN. Validation: `go vet` clean; `go test -race ./tui` passes.


## BUG: terminal corruption — autocomplete popup bleeds into /quota output when the command output fills the viewport (2026-08-27) [FIXED]

**Observed:** run `/quota` (tall output: the Session Usage + Provider Quotas tables + the Codex Rate-Limit Resets table), then start typing a new command (`/skill`). The autocomplete popup (`── Commands ──`, `› /skill`, `── Modifiers ──`, `/skill:run:dream`, `/skill:run:telegram`, …) is composited ON TOP of the still-visible `/quota` table rows, and the two interleave on the SAME screen rows — e.g. `──┌───────────────┬────…`, `› /sID  │ Title │ Expires │ Status │`, `  /sRateLimitResetCredit_92430925d90c81918318b809d7a09cce │ Full reset (Weekly + 5 hr) │ …`. The popup box-drawing and the table cells are mixed cell-by-cell, producing unreadable garbage. (Terminal log: `term.log`; the corruption is visible in the synced frames where the `/quota:resets` table occupies the transcript region while the `/skill` autocomplete overlay lands on the same rows.)

**Root cause (diff-repaint baseline off by the scroll count — confirmed byte-level against `term.log` frame 25):** the corrupting frame is NOT the overlay frame (that is a clean `frameFullRepaint`). It is the NEXT frame — the user pressed Enter on `/skill`, the popup closed, and the `/skill` response box appended 9 rows at the bottom, so the frame classified `frameDiff` and performed a DECSTBM scroll of n=9. In `repaintWindow` the per-row baseline came from `prevRowTranscript` (`tui/compositor_rowdiff.go`) using the SCREEN-POSITION mapping `prevIdx = i - vt + c.vt`. After `advanceScrollback` physically scrolled the terminal up by n rows, screen row r PHYSICALLY holds `prevLines[vt_new + r - 1]` — the SAME canvas index the repaint is about to draw — but the screen-position mapping returned `prevLines[i - n]` (off by exactly the scroll count). `planPartialRow` then emitted column-range splices (`\x1b[<r>;<col>H` + segment, no `\x1b[2K`) whose stable prefix/suffix cells were kept from whatever PHYSICALLY sat on that row — the just-closed popup text (`── Modifiers ──`, `› /s`, `/skill:run:dream`, `/skill:run:telegram`) — producing the cell-level interleave `──┌────`, `› /sID │ Title`, `/skill:run:dream  Internal skill f───┴───`. The same off-by-n made `unchangedRowTranscript` compare against the wrong baseline, leaving whole stale rows (term.log row 28 kept `/skill:run:telegram …` over the resets table). The overlay was a red herring: ANY scroll + in-place row change in the same diff frame corrupted; the popup merely supplied the distinctive stale cells. Verified by replaying `term.log` frames 1..25 through a VT100 screen model — rows 23-27 reproduced the screenshot exactly — and by a Go repro (`TestCompositor_ScrollDiff_PhysicalBaseline`, `tui/compositor_scroll_diff_test.go`) that fails RED with `Q20Modifiers`, `Q21skill:run:dream`, `Q22skill:run:telegram` splices when the fix is reverted.

**Fix:** `prevRowTranscript` now picks the baseline by whether the frame scrolled: scrolled (`lastScrollCount > 0`) → `prevLines[i]` (same canvas index = physical post-scroll row); no scroll → the screen-position mapping `prevLines[i - vt + c.vt]` (kept for the transient-shrink dip regime). No full-repaint fallback was added — the corrected baseline makes the incremental diff sound for the pure-append + in-place-edit combination, preserving the streaming redraw budget (`TestUI_NoJitter_NoExcessiveFullRedrawsDuringStreaming`). Note: the assertion must use the CELL-accurate `NewTermEmulator` (`tui/term_emulator.go`), not the string-based `screenEmulator` — the latter models rows as whole strings and cannot represent a partial column splice, so it reports false corruption on the exact head/tail splices under test.

**Requirement:** an overlay appearing over any transcript content must fully occlude the rows it covers in the SAME frame — no cell-level interleaving of popup and transcript. When the overlay closes, the underlying transcript rows must be repainted exactly once (no duplicates, no lost rows).

**Verification:** `TestCompositor_ScrollDiff_PhysicalBaseline` (RED→GREEN on revert/restore of the `prevRowTranscript` fix) + `TestCompositor_ScrollDiff_InPlaceEditNoScrollStillWorks` (no-scroll regime unchanged). Full gate green: `go vet ./...`, `go test -count=1 -race ./...`, gocognit/gocyclo clean. Fix commit: see `tui/compositor_rowdiff.go` `prevRowTranscript`.


## BUG: adding a model whose name already exists under ANOTHER provider is refused or silently replaces it — model identity is the bare derived ID, not provider-scoped (2026-08-27)

**Observed:** with `deepseek-v4-flash` configured under provider `deepseek`, adding `deepseek-v4-flash` served by provider `opencode` (a legitimately distinct entry — same model name, different endpoint/auth) does not work. Via the picker (`/model` → `+` → custom) the add is REFUSED with `Model deepseek-v4-flash already configured.` and the selector re-opens; via the CLI (`/config:add model <id> <provider> <name>` or `/model add`) the EXISTING entry is silently REWRITTEN in place (its `ProviderID`/`Model` overwritten), so the deepseek binding is clobbered into opencode. Either path loses or blocks a valid configuration: two providers serving the same model name cannot coexist.

**Root cause (model identity is the bare ID, provider is not part of it — three colluding defects):**
1. **ID derivation is provider-blind.** `deriveModelID` (`core/commands/config_tail.go:99`) strips any `provider/` prefix and slugifies the base name, so `deepseek/deepseek-v4-flash` and `opencode/deepseek-v4-flash` both derive to the SAME ID `deepseek-v4-flash`. Identity carries no provider.
2. **Picker add refuses on bare-ID match.** `addAndShowModel` (`core/commands/model.go:310`) does `modelIndex(cfg.Models, modelName) >= 0` → flash "already configured" and return. `modelIndex` matches on `ID` only, so a same-named model under a DIFFERENT provider trips the duplicate guard and the cross-provider add never happens.
3. **CLI add upserts across providers.** `doAddModel` (`core/commands/config_cli.go:66-76`) matches on `ID` alone and, on a hit, overwrites `ProviderID` and `Model` in place — a silent replace/clash when the same ID is reused for a different provider. This behavior is currently PINNED by `TestModelCommand_AddUpsert` ("adding an existing model ID updates it in place … matching doAddModel semantics"), so the fix must deliberately re-spec that test.

**Requirement:** adding a model that shares a name with an existing model under a DIFFERENT provider MUST succeed as a distinct, coexisting entry — never refused, never a silent in-place replace of the other provider's binding. Because the whole model architecture is keyed by bare ID (`providerIDForModel`, `modelIndex`, `applyModelSelection`, remove-by-ID), the clean fix that preserves that architecture is to GUARANTEE ID UNIQUENESS at add time: a cross-provider same-name add derives a unique, deterministic provider-qualified ID (e.g. `deepseek-v4-flash-opencode`) instead of colliding. The duplicate guard must fire ONLY for the exact same provider+model pair (genuinely already configured → idempotent no-op/refusal). An explicit-ID CLI add must update in place ONLY when the provider also matches; when the ID exists under a different provider, it must derive a unique ID and add a new entry rather than overwrite.

**Fix plan:**
- `core/commands/config_tail.go`: add `uniqueModelID(models []config.ModelConfig, baseID, providerID string) string` — returns `baseID` when free; when `baseID` is taken by a DIFFERENT provider, returns a deterministic provider-qualified variant (`baseID-<providerSlug>`), then numeric fallback (`-2`, `-3`…) if that is also taken. Same-provider occupants are the caller's idempotent-noop case, handled before this is consulted.
- `core/commands/model.go` `addAndShowModel`: scope the existence check to the (providerID, model-name) pair — refuse only when BOTH match. On a cross-provider name clash, derive the ID via `uniqueModelID` and append the distinct entry (flash notes the disambiguated ID).
- `core/commands/config_cli.go` `doAddModel`: match on (ID AND providerID) for the in-place upsert; when the ID exists under a different provider, append a new entry under `uniqueModelID(...)` instead of overwriting. Re-spec `TestModelCommand_AddUpsert` to same-provider upsert, and add a cross-provider no-clobber case.
- ID-uniqueness invariant keeps every bare-ID lookup (`providerIDForModel`, `modelIndex`, remove, selection) unambiguous — no change needed there.

**Required tests/UT coverage (red before the fix):**
- `uniqueModelID`: base free → base; base taken by other provider → `base-providerSlug`; that also taken → numeric fallback; provider slugified (spaces/case).
- Picker add: same name + same provider → "already configured" refusal (no duplicate); same name + DIFFERENT provider → new entry appended with a unique provider-qualified ID, both entries survive, ActiveModel untouched, persisted.
- `doAddModel`: same ID + same provider → in-place update (upsert preserved); same ID + DIFFERENT provider → new distinct entry, original binding NOT overwritten (no-clobber regression guard).

**Implemented (2026-08-27).** Model identity is now provider-scoped and bare IDs are guaranteed unique at add time, so the whole ID-keyed architecture (`providerIDForModel`, `modelIndex`, remove-by-ID, selection) stays unambiguous. `core/commands/config_tail.go`: extracted `slugifyModelID` from `deriveModelID` (shared slug logic) and added `uniqueModelID(models, baseID, providerID)` — returns `baseID` when free or held by the same provider (the caller's idempotent/upsert case); on a cross-provider clash returns the deterministic `baseID-<providerSlug>`, then a numeric `-2/-3…` fallback; plus a `findModelByID` helper. `core/commands/model.go`: added `modelIndexForProvider` (matches on providerID AND model name) and rewired `addAndShowModel` — the duplicate guard now fires only for the exact same provider+name pair (message "already configured for provider X"), and a cross-provider same-name add appends a distinct entry under `uniqueModelID`. `core/commands/config_cli.go` `doAddModel`: the in-place upsert now matches on (ID AND providerID); a cross-provider ID reuse appends a new entry under `uniqueModelID` instead of overwriting `ProviderID`/`Model`. Tests written RED first, GREEN after: new `core/commands/model_crossprovider_test.go` — `TestUniqueModelID_{FreeBase,CrossProviderClashQualifies,SlugifiesProvider,NumericFallbackWhenQualifiedAlsoTaken}`, `TestAddAndShowModel_{SameProviderSameNameRefused,CrossProviderCoexists}`, `TestDoAddModel_{SameProviderUpsert,CrossProviderNoClobber}`; `TestModelCommand_AddUpsert` re-specced to the same-provider case (it previously pinned the cross-provider clobber). Validation: `go vet ./...` clean; `go test -count=1 -race -cover ./...` PASS (exit 0); gocognit `-over 15` / gocyclo `-over 12` on touched files zero findings; gofmt clean.

## BUG: model-discovery failure dumps the raw HTML error page into the flash UI (regression of "Codex cannot list models", fixed 2026-08-17) (2026-08-27)

**Observed:** in the `/model` picker, live model discovery for `openai-codex` fails (its endpoint has no `/models` route; Cloudflare answers 403 with an HTML challenge page) and the flash renders the ENTIRE raw HTML document across the UI:

```
│ ⚡ Model discovery failed for openai-codex (provider returned status 403: <html>
│ <head>
│   <meta name="viewport" content="width=device-width, initial-scale=1" />
│   <style global>body{font-family:Arial,Helvetica,sans-serif}…
│   … (svg logo, challenge-error-text, meta refresh) …
│ </html>
```

The flash box overflows with multi-line markup, pushing the picker down and rendering the message unreadable. The picker list itself still populates (other providers' models are shown); only the diagnostic is broken.

**Root cause (error text carries the raw response body end-to-end into the UI):**
1. `provider/manager.go:285` builds the discovery error with the raw body: `fmt.Errorf("provider returned status %d: %s", resp.StatusCode, string(body))` — a multi-KB Cloudflare HTML page becomes the error string.
2. `warnLiveModelDiscoveryFallback` (`core/commands/model.go:571-581`) interpolates that error straight into the flash via `%v`, with no truncation or single-line collapsing — so the full HTML lands in the flash overlay.

This is a REGRESSION of the 2026-08-17 "Codex cannot list models" fix (see `docs/archive/bugs.2026-08-18.md`): that fix delivered the accurate no-known-models wording (F2) and the codex registry alias (F1) but never sanitized the error BODY — the raw-HTML dump into the flash was left in place.

**Requirement:** a model-discovery failure flash must be ONE concise single-line diagnostic — provider, HTTP status, and a short reason — NEVER the raw response body. Sanitize at the source (the error must not embed raw multi-line HTML) AND defend at the flash site (any verbose/multi-line error is collapsed to a bounded single line before rendering), so no future verbose provider error can blow up the UI again.

**Fix plan:**
- `provider/manager.go`: in `ListModels`, replace the raw-body error with a sanitized summary — HTTP status plus a bounded, single-line body snippet (HTML pages collapse to a note like `(HTML error page)`; short plaintext bodies kept, whitespace collapsed, hard length cap). Add an unexported `summarizeErrBody`-style helper to keep it testable and within complexity budget.
- `core/commands/model.go` `warnLiveModelDiscoveryFallback`: render the error through a single-line truncation helper (first line / whitespace-collapsed, hard cap) before flashing, as defense-in-depth for any error source.

**Required tests/UT coverage (red before the fix):**
- `provider/manager.go`: a live `/models` fetch answered with 403 + a large HTML body yields an error that contains the status, does NOT contain `<html>`/`<svg>`/raw markup, and is single-line and length-bounded; a short plaintext error body (e.g. `{"error":"..."}`) is preserved (truncated only if over the cap).
- `core/commands/model.go`: `warnLiveModelDiscoveryFallback` with a multi-line verbose error flashes a single-line, capped message (no embedded newlines, no raw HTML).

**Implemented (2026-08-27).** The discovery error no longer carries a raw response body, and the flash is defended against any verbose error. `provider/manager.go` `ListModels`: the non-200 branch now embeds `summarizeModelErrBody(body)` instead of `string(body)` — a new helper that collapses all whitespace runs to one space, folds any HTML/XML document to the fixed note `(HTML error page)` (via `looksLikeHTML`: a leading tag opener or a known document tag anywhere), maps an empty body to `(empty body)`, preserves a short plaintext/JSON reason, and hard-caps the snippet at `modelErrBodyCap` (160). `core/commands/model.go` `warnLiveModelDiscoveryFallback`: the error is now rendered through `singleLineErr` (whitespace-collapsed, markup → `(HTML error page)` via `looksLikeHTMLString`, hard-capped at `flashErrCap` 200) as defense-in-depth, so no future verbose provider error can overflow the flash again. Tests written RED first, GREEN after: new `provider/manager_discovery_error_test.go` — `TestListModels_ErrorBodyHTMLIsSanitized` (403 + Cloudflare HTML → status kept, no `<html`/`<svg>`/`<style>`/`<p>`/`JavaScript`, single-line), `TestListModels_ErrorBodyPlaintextKept` (401 + short JSON reason preserved), `TestListModels_ErrorBodyLengthCapped` (5 KB body → bounded error), all driven through an `httptest` server; and `TestWarnLiveModelDiscoveryFallback_SingleLine` in `core/commands/model_discovery_test.go` (multi-line HTML error → single-line, markup-free flash). Validation: `go vet ./...` clean; `go test -count=1 -race -cover ./...` PASS (exit 0); gocognit `-over 15` / gocyclo `-over 12` on touched files zero findings; gofmt clean.


## BUG: per-model compression never triggers — a stale project-layer `enabled: false` silently kills the per-model hard ceiling, and hard limits must never be cache-deferred (2026-08-26)

**Observed:** home `~/.goa/config.yaml` configures `context_compression.per_model.glm-5-3-flash: {thresholds: {hard_percent: 20}, strategies: {hard: summarize}, cache_gate: off}`. A long glm-5.3-flash session (zai, 1.0M window) in `~/dev/frigolite` sailed past the 20% per-model ceiling with NOTHING firing — no proactive compression at 200K, no reactive net, no warning — until the user fired `/compress` by hand at 31% (310,483 → 13,593 tokens, 939 → 2 messages, 78.74s). The status bar read `26.2%/1.0M` while the configured per-model hard limit sat at 20%. Separately, `context_compression.enabled` is not visible/changeable as a first-class row in `/config` (only under "Advanced…"), so the user could not SEE that the session's project layer had compression disabled.

**Root cause (one observed defect + three latent ones, all in the per-model plumbing):**
1. **Observed (why this session never compressed):** the project layer `~/dev/frigolite/.goa/config.yaml` carries a stale full-struct dump from a pre-field-scoped goa (`SaveProjectConfig` once marshaled the whole Config; made field-scoped in bb89bcc) containing `context_compression.enabled: false` and `on_context_error: false`. The project layer outranks home, so the merged `Enabled` is false → `buildCompressionConfig` zeroes EVERY threshold (the per-model hard 20 included) → and `StartSession`/`RefreshContextCompression` then skip applying the compression block entirely (`EnabledValue() || MaxTokens > 0` is false), so the agent runs on the zero-value `ContextCompressionConfig`: proactive tiers all off AND the reactive on-context-error net off. Compression could never trigger.
2. **Latent (cascade merge):** `mergeCompressionPerModel` (`config/config_merge_sections.go`) overlays per-model entries field-wise but silently DROPS the `Strategies` and `CacheGate` fields — a per-model `cache_gate`/`strategies.*` set at a higher cascade layer over an existing lower-layer entry is discarded.
3. **Latent (apply-site drift):** the three compression-config apply sites use three different guards — `StartSession`/`RefreshContextCompression` apply only when `EnabledValue() || MaxTokens > 0`; `SetModel` applies only when `MaxTokens > 0 || hasCompressionOverride(newModel)`. Switching from an override model to a non-override model leaves the PREVIOUS model's thresholds live on the agent (stale per-model config), and the skip path drops the reactive net even though the docs promise "the reactive safety net is unaffected" by `enabled`.
4. **Semantics gap:** `ModelCompressionOverride` cannot express enablement at all — a global/project `enabled: false` unconditionally neutralizes deliberate per-model ceilings; there is no per-model way back on.

**Requirement:** per-model compression MUST trigger when configured — a per-model entry that states a ceiling is an explicit opt-in for that model and must take effect even when a higher cascade layer carries a blanket `enabled: false` (the most specific layer wins). Per-model `enabled` must be expressible explicitly (tri-state: unset = inherit/auto, `true` = force on for this model, `false` = force off for this model). The reactive net (on_context_error / on_error_strategy) must reach the agent regardless of the proactive enable flag, exactly as documented. Hard limits are HARD limits: at/above the hard ceiling no cache rationale may defer, skip, or soften the hard-layer strategy (cache gates apply to the soft layer only). Cascade merging of per-model entries must be faithful for every field. The `enabled` master switch must be a first-class visible row in the `/config` compression menu.

**Fix plan:**
- `config`: add `Enabled *bool` (`yaml:"enabled,omitempty"`, tri-state) to `ModelCompressionOverride`; new resolver `CompressionEnabledForModel(modelID) bool` — explicit per-model `enabled` wins; else, when the global flag is false, a per-model entry stating any threshold (soft/trigger/hard/legacy threshold_percent) implicitly activates compression for that model; else the global flag. Fix `mergeCompressionPerModel` to also overlay `Enabled` (pointer), `Strategies` (via `mergeCompressionStrategies`) and `CacheGate`.
- `core`: `buildCompressionConfig` consults `CompressionEnabledForModel(modelID)` instead of the global `EnabledValue()` when zeroing thresholds; all three apply sites (StartSession, SetModel, RefreshContextCompression) ALWAYS push the resolved config (`hasCompressionOverride` gate deleted) so the reactive net and per-model ceilings can never be dropped or left stale across a model switch.
- `core/commands`: per-model `enabled` setter (`applyPerModelField` case, empty = inherit) + a menu row in the per-model editor; promote the `enabled` master switch to the top-level compression menu (visible without entering Advanced).
- `internal/agentic`: pin the hard-tier cache freedom with regression tests (policy hard-first with hot cache; maybeCompress hard branch with hot cache; micro overHard bypass with hot cache). No SDK behavior change — the hard paths are already cache-free; the tests keep them that way.

**Required tests/UT coverage (red before the fix):**
- Cascade (`config`): per-model merge keeps `enabled`/`strategies`/`cache_gate` from a higher layer over an existing lower-layer entry; `CompressionEnabledForModel` matrix: global on/off × per-model unset/true/false × thresholds stated or not.
- `core`: `buildCompressionConfig` with global `enabled: false` + per-model hard ceiling (the frigolite shape) yields the per-model hard threshold on the agentic config (not zeroed); per-model `enabled: false` under global `enabled: true` takes the model fully off; `SetModel` override→plain pushes the global config (no stale ceiling); `SetModel`/`StartSession` with global disabled still apply `OnContextError`/strategy fields (reactive net preserved).
- `internal/agentic`: `DecideCompactionPolicy` with `CacheHot: true` at ≥ hard still returns `EmergencyFallback`; `maybeCompress` at/above hard with a presumed-hot cache runs the hard strategy; `microCompactForced` at/above hard mutates despite hot cache (overHard bypass pinned).
- `core/commands`: `applyPerModelField("enabled", …)` accepts true/false/empty(clear) and round-trips through `setPerModelCompressionField`; the compression menu exposes `enabled` as a top-level row.

**Implemented (2026-08-26).** `config/config_compression_types.go`: `ModelCompressionOverride` gained `Enabled *bool` (`enabled,omitempty`, tri-state) and the resolver `(ContextCompressionConfig).CompressionEnabledForModel(modelID) bool` — an explicit per-model `enabled` always wins; otherwise, when the global flag is false, an override that STATES any threshold (soft/trigger/hard or legacy threshold_percent, via `overrideStatesThreshold`) implicitly activates compression for that model ("a stated ceiling is a deliberate opt-in"); otherwise the global flag. Per-model `enabled: false` beats stated thresholds. `config/config_merge_sections.go` `mergeCompressionPerModel` now faithfully overlays `Enabled` (non-nil), `Strategies` (via `mergeCompressionStrategies`) and `CacheGate` (non-empty) — the two previously-dropped fields. `core/agentmanager_lifecycle.go`: `buildCompressionConfig` zeroes thresholds on `!CompressionEnabledForModel(modelID)` (was the global `EnabledValue()`), and `buildAgenticConfig` now ALWAYS sets `ContextCompression` so the reactive net (on_context_error / on_error_strategy) reaches the agent even with proactive compression disabled. `core/agentmanager.go`: `SetModel` and `RefreshContextCompression` push the resolved config unconditionally — the `hasCompressionOverride` gate is deleted, so a switch override→plain can no longer leave the previous model's ceiling live. `core/commands/config_cli_setters.go`: `applyPerModelField` case `"enabled"` → `setPerModelEnabled` (empty=nil inherit; true/on/1/yes / false/off/0/no; extracted to keep gocyclo ≤12). `core/commands/config_compression.go`: the compression menu shows a first-class top-level `enabled` toggle row and the per-model editor an `enabled` row (labels "inherit"/"on (forced)"/"off (forced)"). SDK hard-tier cache freedom was already correct; `internal/agentic/agent_compression_hard_cachefree_test.go` pins it (hard-first with `CacheHot: true`, `proactiveTierLocked` hard ignoring the gate, micro overHard bypass) as regression guards — no SDK behavior change. Tests written RED first and GREEN after: `config/compression_permodel_enabled_test.go` (merge fidelity + tri-state merge + resolver matrix), `core/agentmanager_permodel_enabled_test.go` (frigolite-shape survival, explicit opt-out/opt-in, SetModel staleness, reactive-net-when-disabled), `core/commands/config_compression_test.go` (`TestApplyConfigSet_PerModelEnabledKeys`, top-level and per-model menu rows) plus intentional menu-shape updates in `config_compression_deadrow_test.go` / `config_menu_compression_test.go` (enabled is an inline toggle, asserted to flip config). Validation each gate separately: `go vet ./...` clean; `go test -count=1 -race -cover ./...` all PASS; gocognit `-over 15` / gocyclo `-over 12` on touched files zero findings; gofmt clean on touched files. User note: the stale `~/dev/frigolite/.goa/config.yaml` full-struct dump still carries `enabled: false`/`on_context_error: false` — the fix makes per-model ceilings survive it, but deleting that stale block is advised since the project layer still wins the cascade for `on_context_error`.

## FLAKE: TestRestoreSession_AdoptsRecordedModel fails under box load (2026-08-26)

**Observed:** `--- FAIL: TestRestoreSession_AdoptsRecordedModel (1.01s)` on a loaded shared box. The same test passes in isolation (`go test ./core/commands/ -run TestRestoreSession_AdoptsRecordedModel -count=1` OK, 0.7s) and 5× under `-race` — the failure is environment/timing-sensitive, not a logic regression. This is the same load-flake family already recorded in the 2026-08-26 `/goal:next` entry (load ~600–900, tests timing out then passing individually).

**Root cause (test-side timing fragility, two stacked walls):** the flash assertion drains the event bus with a FIXED 200ms wall-clock deadline (`drainFlashes(f.bus, 200*time.Millisecond)`, `core/commands/session_restore_model_test.go`) instead of polling until the expected flash arrives. Two production-side properties make a fixed short drain unsafe under load: (1) `Context.Flash` (`core/context.go`) posts to `bus.Chat` with a NON-BLOCKING send (`select { … default: }`) — a flash is silently DROPPED when the 8-slot chat buffer is momentarily full (by design: a transient flash must never block a caller); (2) the session-replay flash ("Loaded session…") is emitted from a goroutine (`session_persist.go`) whose scheduling competes with the test under load. Under a starved scheduler the expected "model m1 @ p1" flash either lands after the 200ms window or is dropped against a full buffer — assertion fails at ~1.0s.

**Requirement:** restore-path tests must not depend on a fixed wall-clock drain. Flash assertions poll until the expected text is observed with a generous overall deadline (seconds, not milliseconds), returning as soon as it arrives — load-proof without touching the (intentional) non-blocking Flash contract.

**Fix plan:** replace `drainFlashes(bus, 200ms)` with a poll-until-found helper (`waitForFlashContaining(bus, want, 5s)`) that accumulates flashes and returns the moment the wanted substring is seen; only fails after the full deadline. Apply to all three restore tests that assert on flashes. No production change: the drop-on-full Flash behavior is the designed contract for transient UI notes.

**Required tests/UT coverage (red before the fix):**
- The helper itself: a flash posted AFTER a >200ms scheduling delay (simulated slow producer) is still collected (old helper would miss it); a missing flash fails after the deadline, not instantly.
- `TestRestoreSession_AdoptsRecordedModel` / `TestRestoreSession_UnresolvableRecordedModel` pass under `-race -count=20` and under induced CPU starvation (parallel busy workers) without the fixed-deadline drain.

**Implemented (2026-08-26).** `core/commands/session_restore_model_test.go`: `drainFlashes(bus, 200*time.Millisecond)` replaced by `waitForFlashContaining(t, bus, want, 5s)` — a poll-until-found helper that accumulates flashes and returns the moment the wanted substring is observed, failing only after the full 5s deadline (load-proof without touching the intentional non-blocking `Context.Flash` drop-on-full contract). Applied to both flash-asserting restore tests (`AdoptsRecordedModel` wants "model m1 @ p1"; `UnresolvableRecordedModel` wants "keeping the current selection"); the now-unused `drainFlashes` is removed. New `TestWaitForFlashContaining_SlowProducer` pins the helper contract: a flash posted 400ms late (past the old 200ms window) is still collected. The `AdoptsRecordedModel` assertion body was also split into `assertAdoptedBindingP1` / `assertAdoptionMarkerP1` / `assertAdoptionFlashP1` helpers to bring the test under the gocyclo ≤12 budget. No production change. Validation: targeted tests PASS under `-race` (`go test -count=1 -race -run 'TestRestoreSession_AdoptsRecordedModel|TestRestoreSession_UnresolvableRecordedModel|TestWaitForFlashContaining' ./core/commands/`); `go vet ./...` clean; gocognit/gocyclo clean on the touched file; full `go test -count=1 -race -cover ./...` suite green.

## BUG: /model switch is not pinned into the project config for existing installs — execution.auto_save_model loses its true default in the cascade merge (2026-08-26)

**Observed:** switching models with `/model <x>` did not update the project `.goa/config.yaml`, and a subsequent session booted with a DIFFERENT model than the one last selected — exactly what the per-project pin exists to prevent (bugs.md Bug6). Repro on any pre-existing install: home `~/.goa/config.yaml` written by an older goa (its `execution:` section predates `auto_save_model`, or has no `execution:` mapping at all) → run `/model gpt-x` → project config untouched; if a stale `active_model:` pin from an earlier era still sits in `.goa/config.yaml`, the next session resurrects THAT pin instead of the just-selected model, because the project layer outranks the home fallback that actually received the write.

**Root cause:** `mergeExecution` (`config/config_merge.go`) copied three plain bools unconditionally: `dst.AutoSaveModel = src.AutoSaveModel`. A plain bool cannot distinguish "key absent" from "explicitly false", so EVERY cascade layer built by `yaml.Unmarshal(&Config{})` carries `AutoSaveModel == false` unless the key is literally present. The merge runs over each file layer against the embedded defaults (which set `auto_save_model: true`, `config/configs/default.yaml:28`) — but unconditional assignment OVERWRITES the default with the zero value regardless of intent. Net effect: any user with an existing home or project yaml silently runs with `auto_save_model == false` → `persistModelSwitch` takes the legacy home-only path instead of pinning the project (and the stale highest-precedence project pin then shadows the newer home value on reload). The tri-state merge contract already existed in-tree for exactly this shape: `ContextCompressionConfig.Enabled *bool` merges only when non-nil (`config_merge_sections.go`). Sibling bools `DisableToolBudget`/`AutoHealToolCalls` share the pattern but their compiled-in defaults ARE false, so clobbering is behavior-neutral there; only `auto_save_model` pairs "default true" with a plain bool, which is unspeakable YAML.

**Requirement:** `execution.auto_save_model` must be tri-state across the cascade: nil = inherit from the lower layer, embedded default true; an explicit `true`/`false` at ANY layer wins over lower layers. With the effective default restored, `/model <x>` MUST pin `active_provider`/`active_model` into the project `.goa/config.yaml` again (home untouched), and the next session must boot with the selected model. Explicit opt-out (`auto_save_model: false`) keeps the legacy home-only persistence byte-for-byte.

**Fix plan:** change `ExecutionConfig.AutoSaveModel` to `*bool` with `yaml:"auto_save_model,omitempty"` (tri-state doc comment mirroring `ContextCompression.Enabled`); `mergeExecution` copies only when `src.AutoSaveModel != nil`; add resolver `(ExecutionConfig) AutoSaveModelEnabled() bool { return e.AutoSaveModel == nil || *e.AutoSaveModel }` beside the other feature resolvers; readers (`persistModelSwitch` in `core/commands/config_persist.go`, `saveModelThinkingLevel` in `core/agentmanager_modes.go`) consult the resolver; `/config:set execution.auto_save_model` flips to `setBoolPtr` (materializes an explicit value, same as compression toggles); DeepCopy already handles pointer fields generically via reflection.

**Required tests/UT coverage (red before the fix):**
- Cascade resolution (`config/loader_autosave_model_test.go`): legacy home whose `execution:` omits the key → resolved TRUE; home without any `execution:` mapping → TRUE; no files at all (embedded only) → TRUE; explicit `false` at home, project silent → FALSE; explicit `false` at home overridden by explicit `true` at project → TRUE; explicit `true` at home overridden by `false` at project → FALSE.
- Acceptance end-to-end (`core/commands/model_persist_project_test.go`): legacy-style home+project files ON DISK + real CascadeLoader host → model switch pins the NEW pair into `<project>/.goa/config.yaml`, leaves home's active_model untouched, and reloading the cascade yields the switched pair.
- Opt-out preserved: `auto_save_model: false` on disk + switch → NO project pin appears; project keeps its previous content.
- Setter materialization: `/config:set execution.auto_save_model false|true` writes through the resolver correctly (adapted `TestApplyConfigSet_AutoSaveModel`).

**Implemented (2026-08-26).** `config/config_types.go`: `AutoSaveModel` is now a tri-state pointer field tagged `auto_save_model,omitempty` — nil means inherit-lower-layer, with embedded `default.yaml` seeding `true` at the cascade base. `config/config_merge.go` `mergeExecution`: copies only when the source layer STATES the key (`if src.AutoSaveModel != nil`), keeping `DisableToolBudget`/`AutoHealToolCalls` unconditional (their defaults are false ⇒ no observable change). `config/config_features.go`: `(ExecutionConfig).AutoSaveModelEnabled()` resolves nil→true in the family of tri-state resolvers. Readers switched to the resolver: `core/commands/config_persist.go` `persistModelSwitch`; `core/agentmanager_modes.go` `saveModelThinkingLevel`. `/config:set execution.auto_save_model` now uses `setBoolPtr(&cfg.Execution.AutoSaveModel)` so the CLI materializes an explicit value.

Revealing the true default exposed a second latent bug: catalog flows (`addAndShowModel`, `removeModelFromConfig`, provider removals, the config-menu save) shared `persistModelSwitch`, whose pin-first contract would leave a successfully-pinned project WITHOUT receiving providers/models list changes — invisible until now only because the merge clobber had forced every install onto the legacy home path. Persistence is therefore split by responsibility: `persistModelSwitch` keeps pin-first semantics for SELECTION switches (`/model x`, `/provider:id`, session-restore adoption); new `persistModelCatalogChange(host, cfg, saver)` (`core/commands/config_persist.go`) handles CATALOG changes — home (`SaveHomeProvidersAndModels`) first and unconditionally (global store), then a best-effort pin mirror gated by the same RC-5 team-suppression check as switches, so a removed-active entry cannot resurrect from a stale highest-precedence pin and a governing team's couple never lands in the user's pin. Migrated call-sites: `model.go` add/remove model, `provider.go` `doRemoveProvider`/`removeProviderConfig` (gained a host param), `config_tail.go` menu save.

Tests were written RED first and are GREEN after: `config/loader_autosave_model_test.go` (six-row cascade resolution matrix: legacy execution-section-without-key→true, no-execution-section→true, bare embedded→true, explicit false persists, project-over-home both directions); `core/commands/model_persist_project_test.go::TestModelSwitch_LegacyConfigPinsProjectModel` (real seeded legacy files on disk, boot on the stale pin, real runModelCommand path → project pinned with `new-model`, home untouched, reload yields the pair) and `TestModelSwitch_ExplicitOptOutSkipsProjectPin` (explicit false: no project pin, home still receives it). Existing tests adapted to the corrected semantics: `TestAgentManager_SetThinkingLevel_SavesAtModelLevel` states its opt-out explicitly; `TestApplyConfigSet_AutoSaveModel` asserts materialized pointer values; `TestApplyModelSelectionForProvider_CarriesPickerProvider`/`TestProviderCommand_WithArgs_SwitchesProvider` assert the pin record under explicit-on. Validation gates each run separately: `go vet ./...` clean; staticcheck `./core/... ./config/...` clean; gocognit/gocyclo zero findings on touched files; gofmt clean on touched files; `go test -count=1 ./...` all PASS; `-race` PASS for `./config/... ./core/...`.

## BUG: /goal:next with a space before the objective falls into the create flow instead of queueing next (2026-08-26)

**Observed:** typing `/goal:next <goal text>` did not put the goal in the queue-as-next position — it fell into plain `/goal` create handling and opened the "A goal is already active — where should the new goal go?" replace-or-enqueue prompt (`promptFirstOrLast`). Same for any text-taking form typed with a space where a second colon was expected (`/goal:new x`, `/goal:replace x`, `/goal:reorder m`, `/goal:cancel all`, `/goal:pause next`).

**Root cause:** `CommandRouter.Parse` splits on ':' only, so `Run` receives ONE argument `"next <goal text>"`. `GoalCommand.parseSubcommand` looked the keyword up against the WHOLE `args[0]` (`"next <goal text>"` is not a keyword) and treated every arg as a free-form objective → dispatch `create` → prompt. The colon canonical form worked; its space-glued twin silently rerouted.

**Requirement:** `/goal:<kw> <text>` must route byte-for-byte like `/goal:<kw>:<text>` for text-consuming keywords (new/next/replace/reorder + cancel/pause scopes). No-argument keywords stay un-split so a free-form objective may still begin with one of those words.

**Fix plan:** extract keyword resolution from `parseSubcommand` into `resolveGoalKeyword`: exact keyword match as today, else recognize one of `goalSpaceSplittable` glued to its text inside `args[0]` (space boundary) and prepend the tail to the remaining args so downstream parsing sees the colon shape; cut the tail from the UN-lowered argument so objectives keep their case. Introduce named type `goalSubSpec` for the parse table values.

**Required tests/UT coverage (red before the fix):**
- Parse table: glued forms `"next fix tests"`→next-add/"fix tests", `"NEXT audit queue"` (case-insensitive keyword), `"next last audit"` placement token, bare `"next"`→next-interactive, `"new build the parser"`→create, `"replace flaky suite"`→replace, `"reorder 2B,1A"`→reorder PRESERVING CASE, `"cancel all"`→cancel-all, `"cancel bogus"`→usage hint, `"pause next"`/`"pause next off"`.
- Escape hatch: `"list everything twice"`, `"status now please"`, `"resume tomorrow cleanup"` stay free-form objectives.
- End-to-end: with an ACTIVE goal, `Run(ctx, ["next fix tests"])` prepends "fix tests" to the durable queue and reports "Queued goal to run next"; it must NOT open the replace-or-queue prompt.
**Implemented (2026-08-26).** `core/commands/goal.go`: keyword resolution extracted from `parseSubcommand` into `resolveGoalKeyword` over a named `goalSubSpec` table type. Exact keyword match is unchanged; NEW `goalSpaceSplittable` ({new, next, replace, reorder, cancel, pause} — text-consuming keywords only) recognizes a keyword GLUED to its text by one space inside the single router arg (`/goal:next fix tests` reaches Run as one `"next fix tests"` argument because CommandRouter splits on ':' only). The tail is re-cut from the UN-lowered argument (lowercasing serves the keyword lookup only, so objectives keep their case) and prepended to the remaining args, making downstream parsing see exactly the colon shape `/goal:<kw>:<text>` — placement/context tokens (`last|first|fresh|reuse|rfirst|rlast`), cancel/pause scopes, reorder mapping and usage hints all reused unchanged through the existing parsers. No-argument keywords (status/current/list/manage/log/verify/settings/resume) are deliberately never space-split so a free-form objective may still start with such a word. Net effect: `/goal:next <objective>` queues-as-next even while a goal is active instead of falling into `create` → `promptFirstOrLast`'s replace-or-enqueue selector; agent-invoked commands share the fix via the same router path. Tests were written RED first and are GREEN after: `TestGoalCommand_parseArgs_SpaceGluedSubcommand` (glued forms: next-add with objective, case-insensitive keyword, placement token, bare interactive, new/replace/reorder/cancel/pause scopes, case preservation `"reorder 2B,1A"`, usage-hint errors), `TestGoalCommand_parseArgs_NonTextKeywordsStayObjectives` (escape hatch stays an objective) and `TestGoalCommand_SpaceGluedNextQueuesAsNext` (end-to-end: durable queue prepends "fix tests", output reports "Queued goal to run next", no replace-or-queue prompt). `/goal` long-help documents the space tolerance without using the forbidden broken-form substrings guarded by `TestHelpDocsColonSyntax`. Validation gates each run separately: `go vet ./...` full-repo CLEAN (empty output); `staticcheck ./core/commands/` clean; `gocognit -over 15` / `gocyclo -over 12` on `core/commands/goal.go` zero findings; `go test -count=1 ./core/commands/` PASS (2.97s); goal-command suite GREEN under `-race`. Environment note recorded for honesty: mid-validation the shared box spiked to load ~600–900 with ~64MB free RAM; one combined `-race ./...` sweep produced spurious 10-minute timeout panics across ten unrelated packages (pty/LSP-real-gopls/timing suites) plus one toolchain OOM `[build failed]` with no diagnostics — every single affected test PASSED when rerun individually against the SAME working tree (e.g. `TestRestoreSession_AdoptsRecordedModel`: 59.67s FAIL under load vs 0.73s PASS alone), proving the failures were resource starvation, not regressions from this change.


## BUG: session restore does not bind the session's provider/model — status bar falls back to the latest ~/.goa selection (2026-08-26)

**Observed:** the provider/model pair shown in the status bar must describe what THE SESSION uses, but the only durable record of a model choice is the global config (`active_provider`/`active_model` persisted to `~/.goa/config.yaml`, shared by every goa instance and session). Inside one running process the footer already tracks the live agent binding (`activeModelDisplay` → `boundSessionModel`, `internal/app/models.go`; cross-process leakage via hot reload was fixed in 604f1c9). The remaining hole is durability across `/session restore`: `restoreSession` (`core/commands/session_persist.go`) rebuilds history, adopts the original session ID and re-points the session store writer — but never touches the model. A session created on model M1, abandoned after a switch to M2 (which rewrote `~/.goa`), comes back bound to M2: every next turn runs on M2 and the status bar honestly reports M2 — the ~/.goa-latest value — instead of the restored session's own provider/model. Nothing in the session file records which model ever produced its turns (`SessionInfo`, `core/sessionstore.go`, carries no model; `LogCompanionStarted` writes companion markers with model/provider metadata, but no equivalent exists for the main agent), so even a willing restore has nothing to adopt.

**Root cause:** two gaps of the same missing concept — per-session provider/model binding. (1) The binding is never WRITTEN into the session record: `AgentManager.StartSession`/`SetModel` (`core/agentmanager.go`) change the live agent only; persistence stays accidental (global config). (2) It is never READ back: session restore rehydrates events/ID/store-writer and leaves whatever model the manager last had — for a fresh process that is exactly "the latest model set in goa via ~/.goa".

**Requirement:** the session owns its provider/model. Switching models mid-session must be recorded IN the session; restoring a session must re-bind the recorded pair onto every live surface (provider-manager session selection, in-memory config, agent, thinking level, footer/status bar) WITHOUT writing the shared config cascade — browsing an old session must not rewrite the user's `~/.goa`. Config keeps one job: defaulting brand-new sessions. Sessions without any recorded marker (legacy files) keep today's behavior.

**Fix plan:**
- Marker emission at the two binding choke points: `AgentManager.StartSession` (source `"start"`) and `AgentManager.SetModel` (source `"switch"`), written directly via `sessionStore.WriteEvent` like the existing `companion_started` marker (`core/agentmanager_state.go`) — an `EventProgress` with metadata `{event:"model_selected", model, provider, source}`, bypassing the observer pipeline so nothing is echoed or double-persisted. A last-marker cache dedupes repeated SetModel calls with an unchanged couple.
- `core/session_model.go`: `ModelSelectedEventKey`, marker constructor + `SessionModelFromEvents(events)` — last-write-wins scan over loaded session events.
- Restore adoption: after `StartSessionWithID(name)`, extract the recorded pair and run it through `applyCoupledSwitchPersisting(..., persist=false)` (`core/commands/model.go`) — validate via `ProviderManager.SetActive`, update in-memory cfg, push into agent (+stream options, thinking level) and refresh the footer; on unknown provider/model fall back with a flash note instead of failing the restore.
- Status-bar validation stays end-to-end: display already prefers the bound session model; the new guarantee is that after a restore that binding IS the restored session's pair.

**Required tests/UT coverage (red before the fix):**
- `SessionModelFromEvents`: empty stream → not ok; markers ignored until present; LAST marker wins among several (model *and* provider both taken from it); non-marker progress events skipped.
- Emission: `StartSession` writes one `model_selected` marker (model+provider from the resolved Model) into the live session file; `SetModel` writes a marker when the couple changes and NOTHING when it repeats identically; markers are appended under the CURRENT session ID.
- Restore adoption end-to-end (real SessionStore + ProviderManager + AgentManager): restoring a session whose last marker is `M1 @ P1` while config-selection says `M2 @ P2` leaves `agentMgr.ActiveModel()` on M1's resolved pair, switches PM session selection to P1/M1, updates cfg.ActiveProvider/ActiveModel **in memory**, persists NOTHING to disk (saver absent ⇒ home yaml untouched), refreshes the footer, and appends a fresh switch-marker to the restored file; the restore message names the adopted model.
- Legacy fallback: a session without markers restores exactly as before (no adoption attempt, no extra output); an unresolvable recorded pair flashes a keep-current note and still completes the restore.
- Display regression guard (already green, kept as gate): with a divergent config selection, `activeModelDisplay` shows the adopted binding, proving the bar tracks the session, not ~/.goa.

**Implemented (2026-08-26).** `core/session_model.go`: `ModelSelectedEventKey` ("model_selected") + `ModelSelectedMarker` + `SessionModelFromEvents` (last-write-wins scan, model-less records skipped) — an EventProgress carrying `{event, model, provider, source}` metadata, persisted straight via `SessionStore.WriteEvent` exactly like `companion_started`, so it renders nowhere and never double-persists through the observer pipeline. Emission sits at the two binding choke points (`core/agentmanager.go`): `StartSession` writes a `"start"` marker for the fresh file, `SetModel` a `"switch"` marker only when the couple CHANGED (last-marker cache dedupes identical re-selections); both record the CONFIG couple (`cfg.ActiveProvider/ActiveModel`, falling back to the literal Model fields), NOT the resolved Model — registry merging rewrites Provider to the canonical API family and can swap the ID for the API name, neither of which round-trips through `ProviderManager.SetActive`. Store writes happen outside `am.mu` (InjectSystemMessage lock discipline). `core/commands/session_persist.go`: `adoptRestoredSessionModel` runs after `StartSessionWithID(name)` so the adoption's own marker lands in the restored file — it feeds the recorded pair through the EXISTING one-unit switch primitive `applyCoupledSwitchPersisting(..., persist=false)` (`core/commands/model.go`): PM session-selection validation first, in-memory cfg commit, agent rebinding (+stream options + thinking level + compression overrides), footer refresh — with the home/project config write skipped so browsing an old session never rewrites `~/.goa` (saver passed as nil is safe since persist=false never touches it); unknown recorded couples flash a keep-current note instead of failing the restore, and the success flash names the adopted pair (`· model m1 @ p1`). The plain writeFmt line keeps its old format; adoption lives behind the existing AgentEventReplayer gate (production hosts are always replay-capable). Tests were written RED first and are GREEN after: `core/session_model_test.go` (parse contract incl. last-wins/model-less-skip; start-marker on real store; switch-once/dedupe; writer-rotation lands the marker under the restored ID) and `core/commands/session_restore_model_test.go` (end-to-end with REAL SessionStore + ProviderManager + AgentManager: restore of m1@p1 while selection says p2/m2 rebinds the agent endpoint onto p1, flips PM profile selection + in-memory cfg with NO ConfigSaver present (any home write would be impossible), appends the fresh switch marker, reports the binding in the flash stream; legacy sessions without markers keep today's behavior byte-for-byte; unresolvable ghost-provider markers degrade to keep-current). Status-bar linkage itself stays pinned by the pre-existing `TestActiveModelDisplay_PrefersBoundSessionModel` family — display reads the bound session model first, and after this fix that binding IS the session's own record rather than whatever ~/.goa held last. Validation each gate separately: `go vet ./...` clean; staticcheck ./core clean; `gocognit -over 15` / `gocyclo -over 12` on changed files — zero findings; `go test -count=1 -race -cover ./...` all PASS (new logic at 100% statement coverage; cfgActive* nil-guards partially covered by design).

## BUG: /stats:cache — friendly goal names in section headers + global missed-cache-token totals (2026-08-26)

**Observed:** the `/stats:cache` view labels its per-agent/goal sections with the raw goal ID: `cacheGroupKey` (`core/commands/stats_cache.go`) appends `" · goal:" + t.GoalID`, producing headers like `main · goal:3f9c1a2b`. That value is the opaque hex ID minted by `generateGoalID` (`internal.PrefixedHexID("goal", 8)`, `core/goal/mode.go`) — meaningless to the user — while every goal also carries a human-friendly alias (`GoalSnapshot.Name`, e.g. `cheery.swan`, generated by `internal.FriendlyNameUnique` in `internal/idgen.go`) that the cache report never resolves or shows. Turn/completion records persist only `GoalID` (`core/turnrecorder.go`), so the name lives solely in goal state and is never joined at render time. Separately, the report's token accounting is incomplete relative to what is wanted: cache misses surface only as per-row figures (per-turn CM counters, miss-list "tokens recomputed", drops "lost tokens") — there is **no global "missed cache tokens" total**, and no single headline number answering "how many tokens did imperfect caching cost me this session?".

**Requirement:**
1. **Friendly naming:** section headers must use the goal's friendly name (`main · cheery.swan`) wherever a name exists; fall back to the raw ID only for unnamed goals (pre-naming or externally supplied). A solo no-goal session stays unlabeled as today.
2. **Report sections:** the report should be organized as multiple clearly-titled sections, at minimum:
   - **1. Last 10 exchanges** — one row per exchange/API call of the most recent ~10, keeping the current semantics (bust calls included so the damaging call is visible).
   - **2. Global statistics** — the token-weighted session hit rate **plus explicit "missed cache tokens" totals**. Missed tokens are always measured against the previous content of the same completion chain: a perfect cache loses **0** tokens; a **full cache miss** costs the complete size of the previously-cached prefix (the whole prior prompt volume that stopped being served); a partial miss costs only the vanished portion (`prev cache-read − current cache-read`, floored at 0). Totals must aggregate across the whole session/group and be readable at a glance, not just derivable by summing table rows.

**Fix plan sketch:**
- Name resolution at render time: expose a goal-ID→friendly-name lookup (e.g. via `GoalMode` snapshots persisted with the session history) and map each distinct `GoalID` in `cacheTurnsFromHistory`/`cacheCompletionsFromHistory` through it inside `cacheGroupKey`; empty/unknown mapping falls back to the raw ID.
- Extend the aggregation helpers (`cacheMisses` already computes per-event `missed = prev` for full and `prev − cur` for partial) with a fold that produces `totalMissedTokens` per group, rendered prominently in the Global statistics section (e.g. `Missed cache tokens: 12,431 (3 events)` with comma grouping via `groupThousands`), color-banded red/orange/green analogous to `cacheLevelColor` (0 = green/perfect).
- Keep per-group splitting by agent/goal; every new surface flows through the MD renderer.

**Required tests/UT coverage (red before the fix):**
- Group headers carry the friendly name for named goals; unnamed goals show the raw ID; no-goal sessions remain unlabeled (no regression on today's solo look).
- Global missed-token totals: series with every read intact → total 0 (perfect cache); full bust after an established prefix → total equals the entire previous prefix size; partial narrowing → `prev − cur`; a mixed multi-event series sums all events correctly.
- Last-10 exchanges section retains the current newest-N windowing and per-call ordinals.

**Implemented (2026-08-26).** `core/goal/friendly_names.go`: new `FriendlyNamesFromEventLog` + `(*GoalMode).GoalFriendlyNames` distill the session's goal event log into an ID→alias map (create records carry both; last write wins; store-less mode or read error yields empty/nil so callers fall back to opaque IDs instead of failing the report). `core/commands/transparency.go`: narrow role interface `GoalNameSource` plus optional `StatsCommand.GoalNames` with a nil-safe `goalAliases()` accessor. `core/commands/register.go`: binds the SAME GoalMode `/todo` uses into StatsCommand at registration. `core/commands/stats_cache.go`: the miss classification now lives in ONE place — `scanMisses`/`missScan` (`Full()` = every previously-cached token stopped being served ⇒ whole previous prefix recomputed, `Partial()` = vanished remainder beyond the existing 1024-token wobble tolerance) — and the CM counters/misses table are rebuilt on top of it so footer counters, Lost columns and totals can never disagree (existing behavior byte-identical); report section 1 retitled **Last 10 exchanges**, one row per API call, each with its rate AND a `Lost` figure measured against the PREVIOUS interaction's prefix (healthy exchange shows 0, full bust shows the complete previous size); new section **Global statistics** carries the token-weighted session hit rate plus the headline line `Missed cache tokens: N across M exchange(s) (F full, P partial)` color-banded green (0 = perfect cache), red (any full bust), orange otherwise — completions-granularity with automatic fallback to turn granularity for legacy sessions without a per-call log. Section headers render `main · cheery.swan` when the alias resolves, `main · goal:<id>` otherwise; solo/no-goal sessions stay unlabeled exactly as before. Tests were written RED first and are GREEN after: `stats_cache_test.go` gains scan-semantics parity, totals algebra (perfect/full/partial/multi-call-mixed), friendly-header end-to-end, injection-guard, and updated skeleton coverage; `core/goal/friendly_names_test.go` covers distillation noise rules and drives the method through a real FileEventStore. Validation each gate separately: `go build ./...` OK; `go vet ./...` clean; `go test -count=1 -race -cover ./...` all PASS; `gocognit -over 15 core/commands core/goal` and `gocyclo -over 12` — zero findings; staticcheck binary present locally is incompatible with the current Go export-data version (environment issue predating this change — vet + race suite green).

## BUG: edit tool can silently delete targeted content when replacement fields are lost or absent (2026-08-26)

**Observed:** an `edit` call deleted the old block but the replacement content wasn't inserted, and the tool reported success instead of an error. Reproduced live on 2026-08-26 during goal work on this very file: a classic search/replace (`old_string` = the `## Deferred-tool discoverability...` heading, non-empty multi-line `new_string`) returned "search/replace applied — lines 1-1, match: exact match" with a diff showing the heading replaced by an **empty line**; the intended replacement never reached the computation as usable content and the file was left missing the old block with nothing in its place. No crash or torn disk write was involved — the destruction happened inside the edit validation gap and was hidden behind a success result.

**Root cause (two paths of the same contract violation):**
1. Classic search/replace accepts an empty `new_string` for any matched block and silently turns it into a *deletion*: `tools/editfile.go` (`Execute` classic route → `searchReplace`, mirrored by `applySingleEdit`) only validates that `old_string` is non-empty. Whenever the replacement payload is lost upstream (parameter transport truncation, model emitting an empty field) or simply omitted, the whole anchored block disappears from the target file while the tool reports success — exactly the reported partial edit.
2. `replace_pattern` is absent from `opRequiresContent`, so a call without `new_content`/`new_string` replaces every matched line with an empty insertion (lines vanish, report even claims `0 lines affected`; the multi-line block matcher collapses the block into one blank line).

Both violate the tool contract that content-targeted operations are strictly all-or-nothing: either the full intended replacement lands verbatim in the output, or the tool returns an error and the file is untouched byte-for-byte.

**Requirement:** every operation that targets existing content with a replacement must validate the replacement is present before any mutation (in-memory or on disk). Missing/empty replacement ⇒ up-front `missing_parameter` ToolError, zero mutation, atomic batch semantics preserved. Deliberate deletion stays possible only through `delete_lines` (line ranges) — not through a silently-empty replace field.

**Plan:** add one shared guard `validateReplacePair` used by both classic routes (single `Execute` path and `applySingleEdit` batch element) rejecting empty `new_string` with a hint pointing at `delete_lines`; extend `opRequiresContent` with `OpReplacePattern` so the existing single choke point (`resolveOpContent`) enforces presence/fallback/error uniformly for single-edit, multi-line-block, and batch paths. Comments must reference this incident.

**Required tests/UT coverage (all red before the fix):**
- Classic search/replace where `new_string` arrives empty (the reproduced incident shape): returns `missing_parameter` AND leaves the file byte-for-byte unchanged.
- `replace_pattern` (single-line pattern) without `new_content`/`new_string`: same error, file unchanged.
- `replace_pattern` multi-line block pattern without replacement content: same error, file unchanged (exercises `replacePatternBlock`).
- Batch `edits` whose element lacks replacement content (both classic and pattern variants): whole call fails, file untouched (end-to-end atomicity).
- Sanity success cases unchanged: non-empty replacements apply exactly as before (guard against over-blocking), including fuzzy-match tier.
- Existing regression guards stay green: `replace_lines`/`insert_after`/`insert_before` missing-content errors.

**Implemented (2026-08-26).** `tools/editfile.go`: new shared guard `validateReplacePair` (referencing this incident) now protects both classic search/replace routes — the flat `Execute` path and the batch `applySingleEdit` element — so a lost/empty `new_string` returns a `missing_parameter` error explaining it would delete the matched block (hint: use `delete_lines`) instead of silently deleting it. `opRequiresContent` extended with `OpReplacePattern`, making the existing single choke point (`resolveOpContent`) enforce replacement presence for the single-line matcher, the multi-line block matcher, and batch elements alike; batch failures keep the "no changes were written" atomicity text via `wrapMultiEditError`. Docs updated in `tools/editfile.long.md` (non-empty new_string contract + replace_pattern content requirement). Regression suite `tools/editfile_all_or_nothing_test.go` was RED before the fix and is GREEN after: empty-new_string classic replace leaves the file byte-for-byte unchanged (the live bugs.md reproduction shape), both replace_pattern shapes refused without content, batch with a content-less edit rejected atomically, and a companion table proves every guarded route still applies when content IS present (including fuzzy tier). Validation per guideline 6, each gate run separately: `go vet ./...` PASS, staticcheck not installed locally (noted), `gocognit -over 15 .`/`gocyclo -over 12 .` no findings on changed files (an initial gocognit 19 on the new test table was fixed by extracting `runContentStillAppliesCase`), `go test -count=1 -race -cover ./tools` PASS 83.9% statements, full `go test -count=1 -race ./...` PASS. Note: this running session predates the fix, so live in-TUI verification of the guard will land with the next goa build.


## Deferred-tool discoverability: system-prompt listing + toolset-change notices (2026-08-26)

Requirement: the tools reachable only through `tool_search` should be listed with a short description **in the system prompt**, and a user-role message should be sent to the model whenever a tool is enabled/disabled so it is aware of toolset changes. The system-prompt list tells the model these tools exist; when one is of interest the model runs `tool_search select:<name>` to retrieve details/full call schema before calling (already supported; unloaded calls already get the redirect error naming the exact remedy — `internal/agentic/agent_tools.go:457`).

Review findings (enhancements over the raw requirement):

1. **Stale catalog (bug):** the deferred catalog inside `tool_search`'s schema description is computed once (`catalogOnce sync.Once`, `tools/tool_search.go`) — after any runtime enable/disable the catalog no longer matches the registry until process restart.
2. **Truncated discovery:** the catalog has a 512-byte budget and ends with "… and N more" — the model cannot see every deferred tool name. The system-prompt section should list ALL deferred tools; the `tool_search` description can shrink to query semantics plus a name-only fallback (sub-agents/skill runners do not get goa's app system prompt and still need in-schema discovery).
3. **Cache discipline (Hard Rule 7):** notifications must be append-only user messages (steering-drain style, metadata-tagged), batched/deduped per toggle episode; never rewrite history. System-prompt section must be alpha-stable between changes; changes coincide with tool-block changes anyway, so incremental prefix-cache cost is nil.
4. **Consistency:** disabled tools must vanish from both the section and the loader catalog; toggles rebuild the agent registry (`AgentManager.SetTools` → fresh `agentic.ToolRegistry`, loaded tail reset) — acceptable, the notice tells the model what changed.

Implementation plan:

1. `tools/tool_search.go`: drop `sync.Once`; compute the catalog per `Schema()`/description call from the live registry (cheap: ~14 schema reads). Keep `select:` semantics; embed a compact name-only fallback list.
2. `internal/app/prompt.go`: new `deferredToolsSection(subs)` mirroring `availableSkillsSection` — `<deferred_tools>` block, alpha-sorted `- name: short-desc` lines + instruction "Load with tool_search select:<name> before calling; unloaded calls are rejected." Wire into `buildSystemPrompt` as a low-priority budgeted section (dropped first under pressure).
3. Toolset notices: app-level pending-notice queue (pattern: steering queue); producers are `/tools:*` toggles (`setToolEnabled`, `core/commands/docs.go`), MCP server enable/disable, plugin load/unload. Drain at next turn start → append `Message{Role: User, Metadata: metaToolsetNotice}` with batched "Enabled: … / Disabled: …"; TUI renders it as a system info line, not a user bubble.
4. Tests: catalog freshness after register/unregister; prompt section lists every deferred name, respects budget, byte-stable across unchanged builds; notice batching/dedup/drain order; redirect error text unchanged; `Schemas()` prefix byte-equality across loads.
5. Docs: TOOLS.md "Deferred loading" section; LIMITS row for section/catalog budgets. Gate: `go vet ./... && go test -count=1 -race ./tools ./internal/agentic ./internal/app`.

**Implemented (2026-08-26).** `tools/tool_search.go`: `catalogOnce` removed — the description's name-only list (capped at 64 names) and the results catalog are recomputed from the live registry on every call (`TestToolSearchDescriptionTracksRegistryChanges`, `TestToolSearchNameCatalogCap`; budget test retargeted to the results catalog). `internal/agentic`: partition rules extracted into exported `PartitionDeferred`/`DeferralThreshold` shared by registry construction and the prompt builder; new `InjectUserMessage` + `MetaToolsetNotice` (append-only user-role notice, cache-safe). `internal/app/prompt.go`: `<deferred_tools>` section appended as the lowest-priority budgeted part (`renderDeferredToolsSection` unit tests cover listing, threshold/no-loader inactivity, description caps). `core/agentmanager.go`: `SetTools` diffs previous vs next tool names and injects ONE batched `[goa-tools] Enabled:/Disabled:` user message (seeded at StartSession; identical sets stay silent; enable/disable, MCP, plugin toggles all funnel through it — docs.go's redundant enable-only system message removed); TUI renders the notice as a system info line via `isToolsetNotice`. Validation: `go test -count=1 -race ./tools ./internal/agentic ./tui ./core ./internal/app` PASS, `go vet ./...` clean, gocyclo/gocognit within budget on changed files.

## BUG: /config tool-call fixing is not applied to ongoing sessions (2026-08-26)

**Observed:** Toggling `/config → Tools → "Tool call fixing"` (`execution.auto_heal_tool_calls`) updated the config file and survived a restart, but the RUNNING session kept the value it had snapshotted at startup: malformed tool-call healing stayed off after enabling it (or stayed on after disabling it) until goa was restarted. The persistence half of this incident was already fixed and archived on 2026-08-23 (`docs/archive/bugs-20260823-config-tool-settings.md`, mergeExecution copying `AutoHealToolCalls`); this entry covers the remaining live-sync half.

**Root cause:** `buildAgenticConfig` (`core/agentmanager_lifecycle.go`) copies `cfg.Execution.AutoHealToolCalls` into the `agentic.Config` snapshot ONCE at `StartSession`; `syncRuntimeConfig` had no branch for `execution.auto_heal_tool_calls`, so the menu's `applySet` only mutated the shared config + disk. The heal gate then read `a.cfg.AutoHealToolCalls` (`internal/agentic/agent_stream_heal.go`) — a frozen field, unlike `GetAutonomy`/stream-loop state which are wired to live sources. Reading the raw cfg field was also a lock-discipline smell: heal gating runs outside `a.mu` while setters like `SetTools` mutate cfg under it.

**Requirement:** A change made through `/config` must be saved (already true) AND take effect immediately in any ongoing session without restart.

**Fix plan:** One narrow runtime-sync slice mirroring `RefreshContextCompression`: `Agent.SetAutoHealToolCalls(bool)` + locked accessor `AutoHealEnabled()` used by all three heal-path reads; `AgentManager.RefreshAutoHeal()` pushing `am.cfg.Execution.AutoHealToolCalls` into the active agent under the manager lock; `syncRuntimeConfig` case for `execution.auto_heal_tool_calls` so BOTH the menu toggle and `/config:set execution.auto_heal_tool_calls <v>` propagate.

**Required tests/UT coverage (all red before the fix):**
- agentic: agent built with healing off ignores generic XML tool-call text; `SetAutoHealToolCalls(true)` mid-session makes the very next stream heal and execute it; flipping off again stops healing (no restart, deterministic providers).
- core: with an active session, mutating the shared `*config.Config` (what the toggles do) followed by `RefreshAutoHeal` flips the live agent both ways; refresh with no active session does not panic.
- core/commands end-to-end: real CascadeLoader + real AgentManager + active session; driving the actual menu selection persists through a full cascade reload AND flips the running session.
- Existing regression guards stay green: `TestConfigMenu_ToolCallFixingToggleSurvivesReload`, `TestConfigMenu_EnabledToolsToggleSurvivesReload`, auto-heal parse suites.

**Implemented (2026-08-26).** `internal/agentic/agent_config.go`: new `SetAutoHealToolCalls`/`AutoHealEnabled` pair beside its setter siblings; `agent_stream_heal.go`: gate reads switched to the locked accessor (comment documents why). `core/agentmanager.go`: `RefreshAutoHeal` next to `RefreshContextCompression`. `core/commands/config_cli.go`: `syncRuntimeConfig` case wires menu + CLI set paths. Tests: `internal/agentic/agent_autoheal_toggle_test.go` (three-phase flip, deterministic per-phase providers), `core/agentmanager_autoheal_refresh_test.go` (push both ways + no-session safety), `core/commands/config_tools_live_apply_test.go` (end-to-end menu → reload + live agent).

## Completion echo should read as a boxed continuation of the tool block (2026-08-26)

When a tool finishes while fully scrolled into terminal scrollback, the completion echo (`CompletionEcho`, `tui/tool_execution.go:424`) is appended via `AddToolResult` (`internal/app/stats_stream.go:240`) and rendered by the generic `toolResult` component (`tui/chat_viewport_components.go:680`) — a dim plain line in `system_msg` color with a hardcoded `"  ← "` prefix: no box, no success/error identity, truncated at width.

Desired: the echo should read as a **continuation of the tool call block it belongs to** — a compact boxed one-liner styled with that block's green/red status colors (`tool_success`/`tool_error`). The `←` prefix is KEPT as the continuation-of-message marker.

Example target:

```
← ✓ $ ls -la /Users/muaddib/dev/goa/docs/ | head -60 — Took 0.08s · 2.4 KB · 44 lines
```

Plan sketch: dedicated echo component carrying the widget's terminal status (or a `ConsoleToolEcho` type) rendering one bordered row with the status color instead of routing through generic `toolResult`; keep the `←` marker and the existing content contract (never replay raw output). TUI test asserting box + green styling for ✓ echoes and red for ✗ echoes.

**Implemented (2026-08-26).** New `toolEcho` component (`tui/chat_viewport_components.go`): single boxed row `│ ← <echo> │` with borders and text in `tool_success`/`tool_error` per the widget's terminal status, truncating with an ellipsis to the terminal width; the `←` continuation marker is kept. Wired via `ChatViewport.AddToolEcho` (persisted as `ConsoleToolResult`) from `echoScrolledOffToolResult`. Tests: `tui/tool_echo_test.go` pins status coloring (green ✓ / red ✗), boxing, content preservation, and width truncation. Validation: `go test ./tui -run TestToolEcho -count=1` PASS; full `./tui` race suite PASS.

## Deferred default tool schemas (2026-08-26)

Moved `todo_list`, `verify`, `lsp`, and `run_skill` out of the eager tool schema block. They remain registered and executable, but their schemas are loaded through `tool_search` on demand, reducing per-request context overhead. Added regression coverage for eager omission and `select:todo_list,verify,lsp` loading. Validation: `go test ./tools -run TestDefaultToolsTodoAndVerifyAreDeferred -count=1 -timeout 60s` passes.

**Python joins the deferred set (2026-08-26).** Per follow-up request "python tool should be part of tool_search type — not default": `*tools.PythonTool` now implements `agentic.Deferred` (`tools/deferred.go`) and its schema no longer ships in the eager block; the model discovers it via the `<deferred_tools>` prompt section / tool_search catalog and loads it with `select:python`. Its schema was one of the heaviest non-goal payloads (the partitioned per-request schema drop measured in `TestDeferredToolSchemaCeiling` improved to ~42%). Regression pin: `tools/tool_search_test.go TestPythonToolIsDeferred` (withheld from eager view + catalog listing + select: load path). Validation: `go vet ./...`, `go test -count=1 -race ./...` PASS; golang-check gates PASS.

## OpenCode Go quota segment regression (2026-08-26)

Observed failure: `TestQuota_OpencodeSegmentShowsQuota` returned an empty status segment for the configured `opencode-go` provider, despite the usage API returning rolling/weekly/monthly percentages. The test run also emitted a duplicate-plugin warning while loading a stale copy:

```text
2026/08/26 07:17:04 Warning duplicate plugin id dup at /tmp/TestPluginLoader_DuplicateIDLoadsOnce439849839/001/b/dup — already loaded from another directory, skipping stale copy
--- FAIL: TestQuota_OpencodeSegmentShowsQuota (0.05s)
    quota_plugin_test.go:489: opencode-go segment should show usage percentages, got ""
FAIL
coverage: 81.9% of statements
FAIL github.com/pijalu/goa/plugins
```

Root cause/fix: OpenCode Go was not consistently resolved to the OpenCode quota fetcher and its real `/zen/go/v1/usage` response (`usage.rolling`, `usage.weekly`, `usage.monthly`) was not guaranteed to become window limits. The fetcher now aliases `opencode-go`, routes the usage URL through `/zen/go/v1`, maps all three percentage windows, and the status segment follows the active provider. Regression evidence: `go test ./plugins -run TestQuota_OpencodeSegmentShowsQuota -count=1 -timeout 30s` and `go test ./plugins -count=1 -timeout 120s` pass. The duplicate-plugin warning is retained as test-loader evidence and is unrelated to the quota failure.

## Comprehensive validation evidence (2026-08-25)

Registry parity now has a table-driven 38-ID OpenCode fixture and the PHP Intelephense ID is aligned with OpenCode (`php intelephense`). Hermetic launcher, protocol, diagnostics, lifecycle, workspace-edit, timeout/cancellation, and fake-server tests pass. Evidence:

```text
go test ./internal/lsp ./tools -count=1 -timeout 240s       # PASS
go test ./internal/lsp ./tools -race -count=1 -timeout 300s # PASS
go test ./... -count=1 -race -timeout 300s                  # PASS
go vet ./...                                               # PASS
go test ./internal/lsp -run 'TestRegistryParity|TestSpecForFile|TestLanguageIDFor' -count=1 # PASS
```

The live language matrix remains explicitly opt-in (`GOA_LSP_LIVE=1`) and skips unavailable executables/toolchains with bounded probes; no claim is made for unavailable Java/Razor/ESLint/Oxlint environments. The static-analysis script reports no staticcheck findings, but repository-wide complexity and file-size gates retain pre-existing findings; changed findings are `tools.(*LSPTool).runRefactoring`, `internal/lsp.Manager.spawn`, and the parity test and should be refactored in a follow-up. Remaining server-specific installer/build parity and live navigation coverage are documented in LSP-016 and are not silently treated as complete.


Live language-server tests are opt-in (`GOA_LSP_LIVE=1`) and remain environment-dependent. A server executable or launcher may start without publishing diagnostics; after the bounded diagnostic probe, the write/edit smoke test skips with an explicit reason instead of failing or waiting indefinitely. This policy applies only to optional Python/JavaScript write/edit probes; hermetic `internal/lsp` protocol/lifecycle tests and real-gopls navigation tests remain required. Current evidence is recorded below.

## Declarative registry toolchain lifecycle (2026-08-25)

Added generic platform variants (`GOOS/GOARCH`, OS, default), workspace-local `.goa/bin` and `node_modules/.bin` resolution, and actionable strategy hints. Registry remains the sole source of server launch/install metadata; manager resolution receives workspace root without server-ID branches. npm installs now target the configured cache directory rather than the process working directory. Added hermetic tests for local binaries, platform variants, and unavailable hints. Validation: `go test ./internal/lsp -count=1 -timeout 120s` and `go vet ./internal/lsp ./tools` pass. Repository-wide complexity/file-size checks retain pre-existing failures; `installDownload` is the only new complexity finding.

Download installer validation: declarative HTTPS gzip/tar installation now enforces a 256 MiB bounded archive, rejects non-HTTPS URLs and traversal paths, extracts into an isolated per-server directory, and returns the configured launcher. Optional live download/server checks remain skipped when unavailable.

## Declarative lifecycle update (2026-08-25)

Registry entries now declare workspace-derived initialization metadata (`dynamic.python_venv`) and the manager records negotiated server capabilities from `initialize`; no server-ID branch is needed for this behavior. Server startup passes declarative initialization through `ServerConfig`. Focused validation: `go test ./internal/lsp -run 'Test(Client|Manager|Diagnostics)' -count=1 -timeout 90s` PASS. Optional live Python/JavaScript/Java servers remain environment-dependent and are documented below.

## Automatic tool installation and gopls lifecycle evidence

Confirmed `gopls` is installed (`golang.org/x/tools/gopls v0.21.1`). Root cause of prior real-server timeouts was client-side JSON-RPC handling: server-initiated requests with an ID were not reliably acknowledged, allowing gopls dynamic protocol requests to stall subsequent navigation. Client dispatch now always replies to server requests (registered handler result or JSON null), and unavailable-server errors include the server ID plus installation guidance. Resolution already follows PATH → npx → configured installer for every registry tool when installation is enabled. Regression evidence: all four real-gopls navigation tests pass (`go test ./internal/lsp -run 'TestManager_(Definition|References|Hover|DocumentSymbols)_RealGopls' -count=1`); targeted client/diagnostic/manager tests pass.

## Validation update (2026-08-25)

Current focused evidence:

```text
go test ./internal/lsp -run 'TestManager_.*_RealGopls' -count=1 -timeout 90s  # PASS
go test ./internal/lsp ./tools -count=1 -timeout 240s                    # PASS
go vet ./...                                                        # PASS
```

The focused package run includes the real-gopls lifecycle/navigation integration tests and all tools tests. Optional Python/JavaScript live write/edit probes remain opt-in (`GOA_LSP_LIVE=1`); when a launcher starts but publishes no diagnostics, their bounded probes call `t.Skipf` with the server and reason. This is environment evidence, not a protocol assertion failure. No unrelated language/server parity work is claimed complete; the parity findings below remain follow-up plans.

## Diagnostic/protocol lifecycle implementation update (2026-08-25)

The lifecycle slice now includes monotonic version-aware diagnostic state with explicit clean publications, bounded cancellable waits, pull `textDocument/diagnostic` requests (including result IDs and unchanged reports), refresh notification handling, dynamic capability registration/unregistration tracking, workspace/configuration and workspace-folder request handlers, and expanded initialization capability models. Regression evidence: `go test ./internal/lsp ./tools -count=1 -timeout 240s`, `go test ./... -count=1 -timeout 300s`, and `go vet ./...` pass. Dynamic registration is recorded for subsequent feature decisions; unrelated server parity, advanced refactoring, and configuration hot-reload items below remain planned and are not claimed complete.


When configuration changes during an active session (including compression settings, limits, provider/runtime options, and related tool/session controls), the updated values should be applied directly to the running session and its components. Current behavior must be audited: identify which config consumers snapshot values at startup, add change propagation/update hooks, preserve in-flight operation safety, and add regression tests proving subsequent turns use the new settings without requiring session restart.

Validation evidence: package compile-only checks and `go vet ./...` pass after protocol changes. Race-enabled targeted diagnostics/client/manager tests pass. Remaining registry/install/refactoring parity findings (LSP-001–004, LSP-006, LSP-010–016) remain documented plans; no unsupported language/toolchain claims made.

Pull integration evidence: `Manager.PullDiagnostics(ctx,path)` now waits for the selected client, requests `textDocument/diagnostic`, and merges the report into versioned cache. Targeted validation passed: `go test ./internal/lsp -run 'TestDiagnostics|TestClient|TestManager_(SupportsPath|NoServerForExtension)' -count=1`. Real-gopls integration tests fail in this environment with repeated 22s context deadlines (toolchain/server availability), documented as environment evidence rather than functionality changes.

## Protocol lifecycle implementation evidence (LSP-008/LSP-009 slice)

Added pull-diagnostic request/report protocol models and cancellable client methods; client now supports server-initiated request handlers with JSON-RPC responses. Manager initialization now advertises workspace folders, configuration, work-done progress, dynamic synchronization, pull diagnostics, related-document support, and installs safe handlers for workspace configuration/folders, progress creation, and diagnostic refresh. Focused validation passed: `go test ./internal/lsp -run 'TestClient|TestDiagnostics' -count=1`. Full package run was attempted but exceeded the 300s command bound due to existing slow integration tests; targeted tests pass.

## Diagnostic lifecycle implementation evidence (LSP-005 foundation)

Implemented and validated version-aware push diagnostic state in `internal/lsp/diagnostics.go`: publications retain explicit clean (empty) results, older versions are ignored, document changes mark prior state pending, and bounded context-aware waits observe publication completion. Manager open/change paths mark pending versions; file tools use the lifecycle wait when available while retaining compatibility with custom managers. Regression tests cover stale-to-clean and out-of-order publications. Validation passed: `go test ./internal/lsp ./tools`.

Remaining LSP-008/LSP-009 work (pull diagnostics, dynamic registration, workspace refresh, richer initialization handlers) remains explicitly planned below.

# LSP implementation review (2026-08-25)

## Scope and evidence

Reviewed `internal/lsp/`, `tools/lsp.go`, `tools/editfile.go`, `tools/writefile.go`, the embedded registry, configuration, and unit/integration tests. The focused test command passed:

```text
go test ./internal/lsp ./tools
ok github.com/pijalu/goa/internal/lsp (22.719s)
ok github.com/pijalu/goa/tools (89.701s)
```

The passing tests are mostly fakes and Go/gopls-oriented; they do not establish that all advertised servers are installed or that Java/JavaScript navigation/refactoring works on a real workspace.

## What currently works

- The manager is multi-server and selects servers by extension; the registry includes `gopls` (`.go`), `typescript` (`.js`, `.jsx`, `.ts`, etc.), and `jdtls` (`.java`).
- `didOpen`/full-content `didChange` notifications are versioned and serialized per server client. File writes and edits call the shared LSP manager for every extension handled by the registry.
- Diagnostics are collected from `textDocument/publishDiagnostics`, and output labels use the selected server ID rather than always saying `gopls`.
- The JS language IDs (`javascript`, `javascriptreact`, `typescript`, `typescriptreact`) and Java language ID (`java`) are derived correctly.
- The manager deliberately spawns asynchronously for writes/edits, avoiding a long blocking npm download. Navigation queries wait for a server with the caller context.

## Findings and implementation plans

### LSP-001 — Model-facing `lsp` tool still rejects JavaScript and Java (High)

**Evidence:** `tools/lsp.go:114-116` unconditionally checks `strings.HasSuffix(resolvedPath, ".go")` and returns `"lsp only supports Go files"`. The schema claims “any configured language”, and the manager/registry support JS and Java. `tools/lsp_test.go:47-52` pins this obsolete behavior with `TestLSPTool_NonGoFileRejected`.

**Impact:** An agent can receive diagnostics for a JS/Java write, but cannot use the advertised definition, references, hover, or symbols operations on those files. This fails the requirement that enabled LSP correctly support Go, JavaScript, and Java.

**Plan:**
1. Remove the `.go` guard and ask the manager for support (`ServerIDFor`, or a dedicated `SupportsPath` API) after path resolution.
2. Return a clear unsupported-file error only when no configured server handles the extension.
3. Keep position validation and query error handling unchanged.
4. Replace the Go-only test with table-driven Go/JS/Java acceptance tests and an unsupported-extension rejection test.
5. Add real-server smoke coverage for JS and Java when binaries are available; otherwise retain deterministic protocol fakes.

### LSP-002 — Java is advertised but the default installer is an intentional stub (High)

**Evidence:** `internal/lsp/servers.yaml:62-70` declares `jdtls` with a download installer. `internal/lsp/install.go:105-114` says download installation is “Currently a stub” and always returns an error unless a binary already exists.

**Impact:** On a clean machine with LSP enabled, Java files cannot start the configured server even though the registry suggests automatic installation. Java support therefore depends on a manually installed `jdtls`, with no actionable setup performed by Goa.

**Plan:**
1. Implement a safe download installer with HTTPS, redirect/error/size limits, checksum or archive validation, tar/gzip extraction, and atomic installation under the configured bin directory.
2. Locate the extracted `jdtls` launcher and preserve required sibling files/configuration (jdtls is not generally a standalone binary).
3. Add platform-specific launcher selection and executable permissions.
4. Add tests for successful extraction, malformed archives, HTTP failures, path traversal, and `disable_download` behavior.
5. Document Java prerequisites/fallback behavior and add a live Java diagnostic/navigation test that skips only when installation/toolchain is unavailable.

### LSP-003 — No advanced language features or refactoring tool surface (High)

**Evidence:** `tools/lsp.go` exposes only `definition`, `references`, `hover`, and `symbols`. `internal/lsp/client.go` implements only those request methods plus open/change. There are no `completion`, `codeAction`, `rename`/`prepareRename`, `formatting`, `rangeFormatting`, `applyEdit`, or workspace-edit types/handlers.

**Impact:** The agent cannot use LSP-assisted rename, extract/refactor code actions, quick fixes, import organization, formatting, or completion. Editing remains text-based and cannot safely apply multi-file `WorkspaceEdit` results, so the requested advanced refactoring capability is absent for all three languages.

**Plan:**
1. Add protocol models and client methods for completion, code actions, rename (including prepare), formatting, and optional signature help.
2. Add a generic workspace-edit representation supporting text edits, document changes, resource operations, and version checks.
3. Add a guarded tool operation/API that previews edits, enforces worktree/protected-path policy, stages backups, applies edits atomically, and notifies every affected open document.
4. Advertise client capabilities and parse server capabilities rather than assuming providers exist.
5. Expose operations in the agent tool schema with language-neutral results and actionable errors.
6. Add fake-server protocol tests and real gopls/typescript/jdtls smoke tests for rename/code action application.

### LSP-004 — Diagnostics/hints are not reliable on the first write/edit (Medium)

**Evidence:** `Manager.OpenDocument` and `DidChange` call `clientFor`, which deliberately returns immediately while a server is asynchronously spawning (`internal/lsp/manager.go:157-185`, `430-451`). `collectLSPDiagnostics` polls for only one second (`tools/lsp_diagnostics.go:17-25`). A cold `npx` download or Java installation can take far longer; the write/edit then returns without a hint and ignores the notification error.

**Impact:** “LSP enabled” does not mean the agent gets a diagnostic after the edit that introduced an error. The agent must happen to perform another read/write/edit after startup, and there is no explicit “server starting / diagnostics pending” result.

**Plan:**
1. Separate non-blocking file mutation from an explicit bounded diagnostic wait policy.
2. Return structured status (`server_starting`, `diagnostics_pending`, `clean`, or diagnostics) instead of silently returning an empty block.
3. Add configurable diagnostic wait/debounce time and a longer bounded wait for an already-started server; never block indefinitely on installation.
4. Preserve/carry the original change notification error so the model knows why hints are unavailable.
5. Add tests for cold spawn, clean diagnostics, delayed publish, cancellation, and subsequent retry behavior.

### LSP-005 — Diagnostic cache can return stale results and cannot distinguish clean state (Medium)

**Evidence:** `Diagnostics.Set` deletes entries only when a server publishes an empty list (`internal/lsp/diagnostics.go:66-75`). `collectLSPDiagnostics` returns as soon as any diagnostics exist, without associating them with the current document version (`tools/lsp_diagnostics.go:36-53`). `PublishDiagnosticsParams.Version` is parsed but discarded.

**Impact:** After an edit fixes an error, the tool can immediately report diagnostics from the prior version; a clean edit waits the whole timeout because “no diagnostics” has no completion signal. This produces incorrect hints during iterative editing.

**Plan:**
1. Track URI/document version with each diagnostic publication.
2. Clear or mark a document pending immediately on each didChange/open.
3. Wait for a publication at or after the requested version, including an explicit empty publication, and ignore older publications.
4. Add tests for stale-error-to-clean and out-of-order publication scenarios.

### LSP-006 — Registry routing ignores project markers and makes first extension match win (Medium)

**Evidence:** `specForFile` (`internal/lsp/servers.go:176-185`) returns the first extension match and never consults `FindRoot`/markers for choosing among servers. Thus `.js`/`.ts` files in a Deno project can select the earlier `typescript` entry instead of `deno`; overlapping entries such as Biome are similarly unreachable unless reordered/overridden.

**Impact:** JavaScript support may use the wrong language server, yielding missing or incorrect diagnostics/navigation/features. Project-specific server selection is not reliable when multiple registered servers handle the same extension.

**Plan:**
1. Resolve candidate servers by extension, then rank candidates by nearest matching marker/root (with deterministic priority for explicit configuration).
2. Keep a clear fallback for extension-only servers and custom overrides.
3. Expose the selected server in status/diagnostic metadata.
4. Add routing tests for package.json vs deno.json, biome.json, and nested workspaces.

## Manager test evidence — LSP-001/LSP-007

`internal/lsp/manager_test.go` now verifies `SupportsPath` for supported/unsupported extensions and exercises implementation, workspace symbols, prepare-call-hierarchy, incoming calls, and outgoing calls against the manager's loopback protocol server. Focused validation passed: `go test ./internal/lsp -run 'TestManager_(SupportsPath|NavigationQueries|NoServerForExtension)' -count=1`.


`go test ./internal/lsp ./tools -count=1` passed. Static analysis reports no staticcheck findings; repository-wide complexity/file-size failures are pre-existing and unrelated, while changed LSP functions remain under the configured complexity thresholds after extracting the protocol test server helper. Remaining bugs.md items are explicitly retained as follow-up plans; this slice claims only LSP-001/LSP-007 foundation.


`internal/lsp/client_test.go:TestClient_NavigationRequests` drives a JSON-RPC fake server and verifies all five request methods, result decoding, and call-hierarchy item payloads. Focused validation: `go test ./internal/lsp -run TestClient_NavigationRequests -count=1` passed.


Implemented the parity foundation: the model-facing tool no longer has a Go-only restriction and uses manager support checks when available; JavaScript and Java paths are accepted by deterministic tool tests. Added protocol models/client requests for implementation, workspace symbols, and call hierarchy, manager routing (including prepare-before-calls), schema operation values, and basic output formatting. Validation: `go test ./...` passes (including `internal/lsp` and `tools`). Remaining review items (installer, diagnostics lifecycle, routing, and advanced refactoring) are intentionally not claimed complete.


The comparison target was `../opencode/packages/opencode/src/lsp/lsp.ts`, `lsp/client.ts`, `lsp/server.ts`, and `tool/lsp.ts`. OpenCode's current agent-facing baseline exposes nine operations: `goToDefinition`, `findReferences`, `hover`, `documentSymbol`, `workspaceSymbol`, `goToImplementation`, `prepareCallHierarchy`, `incomingCalls`, and `outgoingCalls`. Goa currently exposes four operations and therefore is not at parity even before adding refactoring.

### LSP-007 — Missing OpenCode navigation operations (High)

**Evidence:** OpenCode's `LSP.Interface` and `tool/lsp.ts` implement `implementation`, `workspaceSymbol`, `prepareCallHierarchy`, `incomingCalls`, and `outgoingCalls`. Goa's `LSPQueryManager`, client, manager, and schema only implement definition, references, hover, and document symbols.

**Plan:** Add the five missing operations end-to-end, including protocol payload/result types, manager methods, schema enum values, output formatting, cancellation, and tests. Call hierarchy must prepare an item before requesting incoming/outgoing calls. Workspace symbols should query all active clients and deduplicate/limit results like OpenCode.

### LSP-008 — Missing OpenCode diagnostic lifecycle and pull diagnostics (High)

**Evidence:** OpenCode tracks publication timestamps and versions, supports both push diagnostics and dynamically registered `textDocument/diagnostic` pull diagnostics, merges/deduplicates results, handles `workspace/diagnostic/refresh`, and waits 5 seconds for document diagnostics or 10 seconds for full diagnostics. Goa only stores the last push list, ignores `PublishDiagnosticsParams.Version`, advertises a minimal publish capability, and polls for one second.

**Plan:** Implement OpenCode-equivalent diagnostic state: per-file publication timestamp/version, pending wait notification, push/pull diagnostic support, dynamic registration/unregistration, workspace refresh handling, merge/deduplication, and separate document/full bounded waits. Preserve Goa's non-blocking file-tool startup by making waiting explicit and cancellable.

### LSP-009 — Initialization capabilities and server protocol hooks are incomplete (Medium)

**Evidence:** OpenCode initializes workspace folders, work-done progress, workspace configuration, watched-file dynamic registration, text-document synchronization, diagnostic dynamic registration, and related-document support. It handles `workspace/configuration`, `workspace/workspaceFolders`, `window/workDoneProgress/create`, and configuration-change notifications. Goa sends only `RootURI`, a minimal `publishDiagnostics` capability, and no server request handlers.

**Plan:** Expand `InitializeParams` and `clientCapabilities` to match the supported protocol surface; implement safe no-op/meaningful handlers for server requests; send workspace folders and configuration settings; record negotiated sync/diagnostic capabilities and use them when touching documents.

### LSP-010 — Server selection differs from OpenCode for overlapping JavaScript servers (High)

**Evidence:** OpenCode's TypeScript root explicitly excludes Deno markers and Deno's root requires `deno.json`/`deno.jsonc`. Goa's registry contains both but `specForFile` returns the first extension match without evaluating roots; `typescript` appears before `deno`, so a Deno JS/TS workspace selects TypeScript.

**Plan:** Port OpenCode's strict/nearest root semantics: candidate extension match, root resolution, exclusion checks, then deterministic selection. Add `typescript` exclusions for Deno markers and tests proving Deno projects select Deno while package-manager projects select TypeScript.

### LSP-011 — TypeScript workspace initialization lacks OpenCode's local tsserver resolution (Medium)

**Evidence:** OpenCode resolves `typescript/lib/tsserver.js` from the workspace and passes it in initialization options. Goa only adds generic `initOptions` and its TypeScript registry entry has no equivalent dynamic tsserver path.

## Workspace-edit partial mutation regression (2026-08-26)

`ApplyWorkspaceEdit` previously validated and replaced files sequentially. If a later file contained an invalid range, earlier files had already been changed, despite the operation being described as atomic. The implementation now prepares and validates all file edits before mutation, then rolls back already-committed files if backup/replacement fails. Regression test: `TestApplyWorkspaceEditValidatesAllFilesBeforeMutation` proves an invalid second edit leaves the first file unchanged. Validation: `go test ./internal/lsp -run 'Test(WorkspaceEdit|ApplyWorkspaceEdit)' -count=1 -timeout 60s` passes.

**Plan:** Resolve the nearest workspace TypeScript installation, pass the server-specific initialization option, and test monorepos/nested package roots and missing-local-TypeScript fallback.

### LSP-012 — LSP status/availability parity is incomplete (Medium)

**Evidence:** OpenCode exposes `status()` entries with server ID, name, root, and connected/error state, emits update events as clients appear, and provides `hasClients(file)` before tool execution. Goa has a string `Status()` and no model-facing status operation/event; `Started()` means only the manager gate is on, not that a file's server is available.

**Plan:** Add structured per-client status (including starting/error/broken state and root), `HasClients`/support checks, and a status command or tool result. Emit/update status when async spawns complete so users and agents can distinguish enabled, starting, unavailable, and connected.

### LSP-013 — OpenCode parity does not itself provide refactoring; Goa still needs an extension beyond parity (High)

OpenCode's current agent-facing LSP tool is navigation/call-hierarchy focused; it does not expose generic rename/code-action/workspace-edit refactoring in `tool/lsp.ts`. Therefore LSP-007 through LSP-012 define the minimum parity target, while LSP-003 remains an additional Goa requirement for the requested advanced refactoring capability.

**Plan:** Complete the parity operations first, then implement LSP-003's rename/code-action/workspace-edit pipeline as a deliberate superset rather than treating OpenCode's limited tool as evidence that refactoring is complete.


## Agent-facing workspace-edit preview/application slice (2026-08-25)

The `lsp` tool's rename operation now previews validated workspace edits by default and accepts `apply: true` to create backups and apply them through the shared protected-path policy. Schema exposes `newName` and `apply`; nil edits and invalid paths return structured `invalid_edit`/`apply_failed` errors. Focused `go test ./tools ./internal/lsp -run 'TestLSPTool|TestWorkspaceEdit' -count=1` passes. Remaining resource-operation/version/rollback limitations are explicitly listed below.

## Workspace edit and advanced-agent regression evidence (2026-08-25)

`WorkspaceEdit` now accepts both `changes` and LSP `documentChanges`, decodes file URIs safely, validates workspace/protected paths, applies UTF-16 positions across multi-line ranges, rejects overlapping edits, and writes per-file backups before replacement. Regression tests cover documentChanges multi-line replacement, Unicode positions, backup creation, and `.goa` protection. Structured manager/tool status and advanced-operation tests are included in the changed LSP/tool suites. Evidence: `go test ./internal/lsp ./tools -count=1` passes; `go vet ./internal/lsp ./tools` remains required as final gate.

Remaining plans: implement resource operations (`create`, `rename`, and `delete` file changes) if servers require them; add version-conflict checks when an open-document version is available; and add rollback across multiple files if a later replacement fails. These are intentionally not silently claimed by the current text-edit-only applier.


Hermetic parity/launcher tests pass: `go test ./internal/lsp -run 'Test(RegistryParityEntries|SpecForFile|InitOptions_TypeScriptWorkspace|InstallDotnet|InstallDownload)' -count=1 -timeout 60s`. Full package evidence also passes: `go test ./internal/lsp ./tools -count=1 -timeout 240s`; `go vet ./internal/lsp ./tools`. Live executable coverage remains opt-in and environment-dependent; no live process was terminated. Remaining OpenCode-specific release/build installers (Clangd, ZLS, ElixirLS) are not claimed implemented; their explicit fallback plans remain in LSP-016.

## Roslyn installer argument coverage (2026-08-25)

Installer metadata now supports declarative dotnet flags; Razor requests the prerelease Roslyn tool and regression tests verify argument ordering. `go test ./internal/lsp -run TestInstallDotnet` passes.

## Registry parity regression fixture (2026-08-25)

Added a table-driven registry fixture asserting ESLint, Oxlint, Razor, Dockerfile basename, and SourceKit extension coverage. Focused registry/launcher tests pass. Remaining platform-specific release downloads and live executable matrix remain environment-dependent/planned.

## TypeScript initialization regression evidence (2026-08-25)

Added a hermetic test proving `dynamic.typescript_server` resolves `<workspace>/node_modules/typescript/lib/tsserver.js` into `initializationOptions.tsserver.path`. Focused test passes.

## Validation/complexity slice (2026-08-25)

Refactored registry validation and archive extraction into focused helpers. Current changed LSP code has no gocognit/gocyclo findings; `go test ./internal/lsp -count=1 -timeout 180s` and `go vet ./internal/lsp` pass. Repository-wide complexity/file-size findings are pre-existing (including unrelated hard-limit files) and remain documented rather than suppressed.

## Launcher fallback slice (2026-08-25)

Extended declarative installation strategies with `dotnet` tool installation for C#/F# and Roslyn/Razor, plus archive launcher discovery for nested JDTLS layouts and executable permission handling. Added a hermetic dotnet command regression test; full internal/lsp validation passes. ESLint's VS Code extension asset build and broader platform release fallbacks remain explicitly planned.

## Parity implementation slice (2026-08-25)

Implemented registry/routing foundation: declarative ESLint, Oxlint, and Razor entries; Dockerfile/Containerfile basename matching; SourceKit `.swift`, `.m`, and `.mm`; Deno marker ranking with TypeScript exclusions; and workspace-local TypeScript `tsserver.js` initialization options. Regression tests cover basename case-insensitivity and Deno-over-TypeScript selection. Validation: `go test ./internal/lsp -run 'TestSpecForFile|TestRegistryLoads' -count=1` passes. ESLint/Razor asset installation and complete per-server download/build fallback matrix remain planned below and are not claimed complete.

## Complete OpenCode language/server parity audit

Source comparison: `../opencode/packages/opencode/src/lsp/server.ts` declares 38 server definitions; `internal/lsp/servers.yaml` declares 35. The three OpenCode server definitions absent from Goa are **ESLint**, **Oxlint**, and **Razor**. The table below lists every OpenCode server and the Goa state.

| OpenCode server | Main language/file types | Goa registry | Assessment |
|---|---|---|---|
| Deno | JS/TS (`.js`, `.jsx`, `.mjs`, `.ts`, `.tsx`) | `deno` | Present, but routing is wrong for overlaps: first-match selection can choose TypeScript before Deno; see LSP-010. |
| Typescript | JS/TS (`.js`, `.jsx`, `.mjs`, `.cjs`, `.mts`, `.cts`, `.ts`, `.tsx`) | `typescript` | Present, but missing OpenCode local `tsserver` initialization and Deno exclusion; see LSP-010/LSP-011. |
| Vue | `.vue` | `vue` | Present; runtime/download behavior needs parity validation. |
| **ESLint** | JS/TS/Vue | **Missing** | No registry entry, launcher, configuration, diagnostics, or tests. |
| **Oxlint** | JS/TS/Vue/Svelte/Astro | **Missing** | No registry entry or support for local `oxc_language_server`/`oxlint --lsp`. |
| Biome | JS/TS/JSON/Vue/Astro/Svelte/CSS/GraphQL/HTML | `biome` | Present; overlap/routing and local package behavior need parity tests. |
| Gopls | Go | `gopls` | Present; root/install behavior requires parity tests. |
| Rubocop (`ruby-lsp`) | Ruby | `ruby-lsp` | Present by ID, but Goa launches `ruby-lsp` while OpenCode launches RuboCop `--lsp`; verify equivalent server. |
| Ty | Python | `ty` | Present, but Goa enables it unconditionally while OpenCode gates it behind experimental runtime configuration. |
| Pyright | Python | `pyright` | Present; initialization/diagnostic lifecycle needs parity work. |
| ElixirLS | Elixir | `elixir-ls` | Present; Goa has no equivalent source-build/download installer. |
| ZLS | Zig | `zls` | Present; Goa has no OpenCode-equivalent platform release download fallback. |
| CSharp | C# | `csharp` | Present by registry name, but OpenCode uses Roslyn language server; Goa command is `csharp-ls`, a different implementation and has no Roslyn installer. |
| **Razor** | `.razor`, `.cshtml` | **Missing** | No Razor language support or Roslyn/Razor extension integration. |
| FSharp | F# | `fsharp` | Present; installer/launcher behavior differs and needs validation. |
| SourceKit | Swift/Objective-C/Objective-C++ | `sourcekit-lsp` | Partial: Goa only registers `.swift`; OpenCode also handles `.objc` and `objcpp` (the OpenCode entries themselves are extension-like names without dots). |
| Rust Analyzer | Rust | `rust` | Present; OpenCode resolves workspace Cargo roots specially, while Goa uses generic markers. |
| Clangd | C/C++ | `clangd` | Present; OpenCode downloads platform releases; Goa only PATH resolution. |
| Svelte | `.svelte` | `svelte` | Present. |
| Astro | `.astro` | `astro` | Present. |
| JDTLS | Java | `jdtls` | Present in registry but download installer is a stub; see LSP-002. |
| Kotlin LS | Kotlin | `kotlin-ls` | Present, PATH-only. |
| YAML LS | YAML | `yaml-ls` | Present; Goa's `package.json` marker is not a meaningful YAML project root fallback. |
| Lua LS | Lua | `lua-ls` | Present, PATH-only. |
| PHP Intelephense | PHP | `intelephense` | Present; npx command must be validated because the package's licensing/runtime behavior may differ. |
| Prisma | Prisma | `prisma` | Present. |
| Dart | Dart | `dart` | Present, PATH-only. |
| OCaml | OCaml | `ocaml-lsp` | Present, PATH-only. |
| Bash LS | Shell (`.sh`, `.bash`, `.zsh`, `.ksh`) | `bash` | Present; npx fallback exists. |
| Terraform LS | Terraform | `terraform` | Present, PATH-only. |
| TexLab | TeX/BibTeX | `texlab` | Present, but extension-derived language ID mapping is present only for `.tex`/`.bib`; root behavior differs. |
| Dockerfile LS | Dockerfile | `dockerfile` | Partial: Goa matches only `.dockerfile`; OpenCode also recognizes a file literally named `Dockerfile`, which `filepath.Ext("Dockerfile")` does not provide. |
| Gleam | Gleam | `gleam` | Present, PATH-only. |
| Clojure LSP | Clojure/EDN | `clojure-lsp` | Present. |
| Nixd | Nix | `nixd` | Present, PATH-only. |
| Tinymist | Typst | `tinymist` | Present, PATH-only. |
| Haskell LS | Haskell | `haskell-language-server` | Present, PATH-only. |
| Julia LS | Julia | `julials` | Present, PATH-only and requires a Julia package environment. |

### LSP-014 — Missing OpenCode servers: ESLint, Oxlint, and Razor (Critical)

**Evidence:** OpenCode exports `ESLint`, `Oxlint`, and `Razor` in `server.ts`; Goa's embedded registry has no corresponding IDs or extensions. This is a direct language/server parity failure, independent of executable availability.

**Plan:**
1. Add declarative entries and dedicated launcher/install strategies for `eslint`, `oxlint`, and `razor`.
2. ESLint: resolve the workspace ESLint package and launch the VS Code ESLint server (or an equivalent maintained stdio server), including required server files and initialization settings.
3. Oxlint: prefer local `oxlint`/`oxc_language_server`, detect `--lsp`, and launch the supported mode exactly as OpenCode does.
4. Razor: add Roslyn language server installation/resolution, locate the VS Code C# Razor extension assets, pass compiler/targets/extension arguments, and support both `.razor` and `.cshtml`.
5. Add registry, routing, launcher, diagnostics, and live smoke tests for all three. Missing optional toolchains should produce structured unavailable status, not silently look like unsupported extensions.
6. Add unit tests: registry entries/extensions/IDs; PATH and workspace-local binary resolution; `disable_download`; command/argument construction; missing dependency and failed-install errors; Razor asset discovery; and unsupported-platform handling.
7. Add integration tests with fake stdio LSP servers: initialize handshake, didOpen/didChange, publishDiagnostics, and one navigation request per server. Assert server ID, root, language ID, and diagnostic propagation.
8. Add live tests gated by executable detection (skip with an explicit reason): ESLint/Oxlint diagnostics for JS, and Razor diagnostics for `.razor`/`.cshtml`; include timeout/cancellation tests so unavailable servers never hang file tools.

### LSP-015 — SourceKit and Dockerfile file-name coverage is incomplete (High)

**Evidence:** OpenCode handles Swift plus Objective-C/Objective-C++ and handles a literal `Dockerfile`. Goa registers only `.swift` for SourceKit and `.dockerfile` for Dockerfile LS. Goa's extension-only `specForFile` cannot select a literal `Dockerfile` because `filepath.Ext("Dockerfile") == ""`.

**Plan:** Add basename matching to server specs (or a separate `filenames` field), register `Dockerfile`, `Containerfile` where appropriate, and Objective-C/Objective-C++ extensions (`.m`, `.mm`) with correct language IDs. Add unit tests for basename/extension routing, case handling, nested roots, and `didOpen.languageId`; add fake-server integration tests asserting the selected server and language ID for each file name.

### LSP-016 — Registry presence is not equivalent to OpenCode support (High)

Most of Goa's 35 matching entries are PATH-only, while OpenCode provides server-specific resolution and, where enabled, installation/build fallback. A clean machine therefore has substantially less actual language coverage than the registry suggests. Known examples include Clangd release download, ZLS release download, ElixirLS source build, Roslyn/C# tooling, F# dotnet-tool installation, and JDTLS archive installation.

**Plan:** Define a per-server launcher contract with PATH, workspace-local binary, package-manager/download/build fallback, platform handling, and `disable_download` enforcement. Record errors/status per server and add a parity test fixture asserting each OpenCode server's expected resolution strategy. Do not mark a language “supported” solely because its YAML row exists.

**Required test/UT coverage:** Add a table-driven parity unit test that parses both registries (or a checked-in expected manifest) and fails when an OpenCode server ID, extension, filename pattern, root marker, or language ID is missing in Goa. Add launcher unit tests for every server's resolution order, install gating, argv, environment, and root directory. Add fake-process integration tests for initialize/open/change/diagnostics/navigation. Add a live matrix test suite that discovers installed servers, runs one diagnostic and one navigation query per available language, skips unavailable toolchains with explicit reasons, and enforces bounded timeouts. Run `go test ./internal/lsp ./tools -race`, `go vet ./...`, and the full `go test -count=1 -race ./...` before considering parity complete.


- No live JavaScript navigation test exists; the live JS test covers write diagnostics only.
- No live Java test exists.
- Existing LSP tool tests explicitly preserve the Go-only rejection, masking LSP-001.
- The focused packages pass, but passing fake-server tests cannot validate executable availability, Java launcher layout, npm downloads, or server-specific refactoring support.