# Bug and feature Tracking

## Guideline
1. Create a detailed fix plan for each bug - the plan must contain test approach and validation steps - execute the plan and validate the fix when all elements are in place.
2. Any issues found must be fixed and the fix plan must be updated accordingly.
3. Issues found during testing must be fixed and the fix plan must be updated accordingly.
4. Each bug should be moved to archive when tested and closed as the associated plan.
5. Use interactive shell/filmstrip to validate the output of the tool - you must verify the actual terminal output.
6. Check code quality with each tool run separately (do not chain them with `;` or `&&`):
    - `go vet ./...`
    - `staticcheck ./...`
    - `gocognit -over 15 .`
    - `gocyclo -over 12 .`
    - `go test -count=1 -race -cover ./...`
    Fix any new issues introduced by the change. Pre-existing warnings are acceptable only if they are unrelated to the change and explicitly noted.

At the end of the session - the bug list should be empty and this file should only contain the guidelines for bug reporting.
If new items are added, restart the process.

Use goals to execute the fix plan - focus on micro tasks goals with new contextto lower context usage - use todos for micro tasks that should share context

Commit at the end of each fix with a clear and descriptive commit message

## Report format
Describe the bug or feature request under `# To fix` below. Keep one section
per item with a short title, the observed behavior, and the expected behavior.

# TODO

# Archive

## ~~Codex cannot list models~~ — FIXED (2026-08-17)

Error:
```
╭──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╮
│ ⚡ Model discovery failed for openai-codex (provider returned status 403: <html>                                                                                                 │
│   <head>                                                                                                                                                                         │
│     <meta name="viewport" content="width=device-width, initial-scale=1" />                                                                                                       │
│     <style global>body{font-family:Arial,Helvetica,sans-serif}.container{align-items:center;display:flex;flex-direction:column;gap:2rem;height:100%;justify-content:center;width:1
│   <meta http-equiv="refresh" content="360"></head>                                                                                                                               │
│   <body>                                                                                                                                                                         │
│     <div class="container">                                                                                                                                                      │
│       <div class="logo">                                                                                                                                                         │
│         <svg                                                                                                                                                                     │
│           width="41"                                                                                                                                                             │
│           height="41"                                                                                                                                                            │
│           viewBox="0 0 41 41"                                                                                                                                                    │
│           fill="none"                                                                                                                                                            │
│           xmlns="http://www.w3.org/2000/svg"                                                                                                                                     │
│           strokeWidth="2"                                                                                                                                                        │
│           class="scale-appear"                                                                                                                                                   │
│         >                                                                                                                                                                        │
│           <path                                                                                                                                                                  │
│             d="M37.5324 16.8707C37.9808 15.5241 38.1363 14.0974 37.9886 12.6859C37.8409 11.2744 37.3934 9.91076 36.676 8.68622C35.6126 6.83404 33.9882 5.3676 32.0373 4.4985C30.08
│             fill="currentColor"                                                                                                                                                  │
│           />                                                                                                                                                                     │
│         </svg>                                                                                                                                                                   │
│       </div>                                                                                                                                                                     │
│       <div class="data"><div class="main-wrapper" role="main"><div class="main-content"><noscript><div class="h2"><span id="challenge-error-text">Enable JavaScript and cookies to
│     </div>                                                                                                                                                                       │
│   </body>                                                                                                                                                                        │
│ </html>                                                                                                                                                                          │
│ ); using known models.                                                                                                                                                           │
Select model to add:
────────────────────────────────────────────────────────────
search>
────────────────────────────────────────────────────────────
› ── custom model ──  type any model name
────────────────────────────────────────────────────────────
  ↑↓ nav  /  type filter  /  enter  /  esc
```

### Fix plan (detailed)

**Root cause:** the `openai-codex` catalog entry
(`internal/agentic/provider/schema/catalog.go:289`) has no `ModelsDevKey`, so
`buildModelsDevMappings()` never maps any models.dev key to the
`ProviderOpenAICodex` identity and the registry serves zero models for it
(verified: `GetModels("openai-codex")==0`, `GetRuntimeModels("openai-codex")==0`).
Its endpoint `https://chatgpt.com/backend-api/models` has no `/models` route —
Cloudflare answers 403 HTML — so the live fetch always fails and the add-model
picker falls back to an EMPTY registry list, showing only "── custom model ──"
under the misleading "using known models" flash.

**Fix:**
- F1: alias codex → openai in `ProviderManager.ListRegistryModels`
  (provider/manager.go): when the inferred identity is `ProviderOpenAICodex`,
  serve the models.dev/embedded catalog of `openai` filtered to the codex
  family (ID contains "codex", matching Pi's hardcoded codex catalog at
  scripts/generate-models.ts: gpt-5.3-codex-spark, gpt-5.4(-mini), gpt-5.5,
  gpt-5.6-luna/sol/terra are all carried under the openai models.dev key).
  Provider identity for streaming stays `openai-codex` — only the model-list
  lookup aliases.
- F2: accurate flash — `warnLiveModelDiscoveryFallback` (core/commands/model.go)
  gains a `registryCount` hint: when the registry fallback is also empty, flash
  "no known models for this provider; type a custom model name" instead of
  "using known models".
- F3: fix stale catalog default `openai-codex` DefaultModel gpt-5.3-codex →
  gpt-5.5 (Pi: codex endpoint serves 5.4/5.5/5.6 + spark; 5.3-codex is not
  served).

**Test approach:**
- Unit: `ListRegistryModels("openai-codex")` returns the codex family
  (contains gpt-5.3-codex-spark, every ID contains "codex"), while
  `ListRegistryModels("openai")` is unchanged; preset catalog test updated for
  the new DefaultModel.
- Unit: flash text branches on empty registry (no-known-models message).
- Filmstrip (internal/app/modelsdev_providers_filmstrip_test.go pattern):
  drive "Select model to add:" for a codex provider with a 403-ing live
  endpoint; assert codex models render in the selector overlay.

**Validation:** gates (vet/staticcheck/gocognit/gocyclo/race) + live filmstrip
render check of the codex model list.

**Resolution:** implemented per the fix plan below; see the plan + tests.


## ~~Context compression: summarize fails ?!~~ — FIXED (2026-08-17)

Summarize fails with "did not fit the window; dropped oldest messages to the hard ceiling" => Summarize should *always* fit the window / if it doesn't, the hard ceiling should be updated to match the required window size

In case of issue: There should not be dropped messages: 1st tool elision/micro compression - only if the window still doesn't fit, drop of messages should be done !



### Fix plan (detailed)

**Root cause (forensically confirmed from the session JSONL — NOT the initial
Esc reconstruction):** usage was at **94%** of a 262K window with
`hard_percent: 95`. The hard tier (summarize) only fires at `usage ≥ 95%`, so
**summarize never ran** (the log shows zero compaction-tx events before the
fallback). But `prepareTurn` then called `enforceContextCeiling()`
UNCONDITIONALLY, and that fallback's history-only cut target
(`historyCeiling = 95%·window − fixedCost`) is STRICTER than the tier trigger:
with a large fixed cost (system prompt + 258 tool schemas) the *history alone*
exceeded the target even though TOTAL usage (94%) was under the ceiling → it
dropped 3 messages (94%→92%) while summarize was never attempted, and reported
the false reason "summarize did not fit the window". The Esc / "context
canceled" came AFTER, when the user aborted the now-corrupted turn.

Additionally the summarize path was not guaranteed to fit:
- the overflow retry applies micro truncation ONLY; when tool-result
  truncation cannot free enough (little tool payload), the retry overflows
  again and Compact fails → destructive fallback;
- a very large produced summary can itself land over the history ceiling;
- the fallback's detail text is hardcoded even when summarize never ran
  (canceled) or failed for a non-fit reason.

**Fix (per the report: summarize must always fit; message-drop only as a true
last resort after elision/micro):**
- F1 (no cut on dead turn): `prepareTurn` and `startStreamRound` skip
  `enforceContextCeiling` when `ctx.Err() != nil` — a canceled turn must never
  mutate history.
- F2 (summarize always fits — input): on the summarize-overflow path, after
  `applyMicroForSummarize`, if the estimated summarize request (history +
  fixed cost + instruction) still exceeds the window, cut the oldest messages
  (chain-safe) until it fits, THEN retry. Bounded and deterministic.
- F3 (summarize always fits — output): before landing the summary pair, if
  the pair's estimated tokens exceed the history ceiling, truncate the summary
  text with an elision note so the compacted history fits.
- F4 (escalation order + accurate detail): `enforceContextCeiling` first runs
  the model-free passes (elision + micro truncation); only when the window
  STILL doesn't fit does it drop oldest messages. Its event detail reflects
  the true cause: overflow ("summarize did not fit the window…") vs other
  failure ("summarize failed (<err>)…") vs post-summarize overshoot ("summary
  exceeds the window…"). The compression error is threaded from the
  maybeCompress call sites.

**Test approach:**
- F1: prepareTurn/startStreamRound with a canceled ctx → history untouched,
  no "hard fallback" event.
- F2: summarize overflow with tool-payload-poor history (micro cannot free
  enough) → retry fits (provider receives a shrunk input) and succeeds.
- F3: provider returns an oversized summary → landed pair is truncated under
  the ceiling, summary ends with the truncation note, next turn does not
  re-fire the fallback.
- F4: fallback event detail strings per cause; elision-first ordering test
  (fallback on tool-heavy history frees via elision, zero drops).
- Regression: all existing compression/compaction tests must pass unchanged
  except those asserting the old hardcoded detail (updated to the new
  contract).

**Validation:** gates (vet/staticcheck/gocognit ≤15/gocyclo ≤12/race) +
interactive filmstrip: drive a session over the hard ceiling, cancel the
summarize mid-flight, assert NO messages are dropped and no hard-fallback
event renders; then let a summarize complete and assert the "summarize"
event renders with no fallback.

### Original report

```
╭──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╮
│ ⚡ Context compacted (hard fallback): 94% → 92% · 3 messages dropped · ~5898 tokens freed                                                                                        │
│ summarize did not fit the window; dropped oldest messages to the hard ceiling                                                                                                    │
╰──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
╭──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╮
│ Error: context canceled                                                                                                                                                          │
╰──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
╭──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╮
│ Generation stopped by user.                                                                                                                                                      │
╰──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯

────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────

────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
~/dev/goa (✱ main)                                                                                                                                                      coder │ YOLO
↑270.2K ↓65.7K 19.5 tok/s CH:99.8%▸99.6% TC:258 94.5%/262.1K c:0m-1                                                                           (kimi-code) k3-256k • high • [57%|11%]
```

**Resolution:** implemented per the fix plan below; see the plan + tests.

## ~~Tools deferred to tool_search: schedule_create / schedule_delete / schedule_list~~ — NOT A BUG (2026-08-17)

Report: the `schedule_*` tools are deferred to `tool_search` instead of being
directly available.

**Finding: does not reproduce — the schedule tools are eager.** Investigation:
- `tools/schedule.go` (`ScheduleCreateTool/ScheduleDeleteTool/ScheduleListTool`)
  do NOT implement `agentic.Deferred`; they are absent from `tools/deferred.go`
  (the deferred family is terminals/webfetch/bg_exec/memento/smartsearch/
  ssh_bash/session_search/session_event_read only).
- They are registered eagerly in `internal/app/subsystems.go:426-428` for every
  session (no config gate), and `agentic.configureDeferral` only withholds tools
  implementing `Deferred()`.
- Reproduction building the FULL production tool set (all 8 deferred tools +
  schedule + the `tool_search` loader) into the agent's registry shows
  `DeferredStatus("schedule_*") == unloaded:false` and all three names present
  in the eager schema block; deferral is active (webfetch withheld) as a control.
- Both the installed binary (`~/go/bin/goa`, mtime today) and a fresh build
  contain the `schedule_create` symbols.

Likely cause of the report: a stale binary (predating commit `22eca59`
"feat(tools): scheduler tools (TL2)", 2026-08-15) or a misread of the
tool_search catalog. No code change required.

Regression guard added: `tools/tool_search_test.go
TestScheduleToolsAreEager` builds the production-shaped registry and pins that
the schedule tools are eager + absent from the deferred catalog, so a future
edit adding them to `deferred.go` fails loudly.

Gates: vet ✓ gocognit ≤15 / gocyclo ≤12 (no new) ✓ race ✓. (Note: `go test
./tools/` full-package run has a PRE-EXISTING, unrelated hang in
`TestTerminals_Read_OffsetCount_Succeeds` — confirmed identical on the
committed baseline via `git stash`; schedule/search/tool_search tests pass.)

Commit: regression test below.

## ~~Codex login UX — method choice should be a list; OAuth steps should follow Pi TUI~~ — FIXED (2026-08-17)

Both problems from the `/provider` OAuth-freeze follow-up are resolved.

**Problem 1 — login-method prompt renders as free-text question.**
`selectCodexMethod` (core/commands/login.go) asked browser-vs-device via the
free-text clarify prompt. It now routes through `ctx.SelectOption` with
`browser`/`device` SelectorItems (arrow-key navigate, enter to pick, esc
cancels → browser default) whenever a selector is wired — Pi `showAuthSelect`
style — keeping the free-text prompter as the headless fallback. The choice
blocks on a result channel; that is safe because `handleOAuth` runs off the
TUI commandLoop on the `runOAuthFlow` goroutine. Additionally, `App.clarify`
(internal/app/app.go) now answers ANY ClarifyCard that carries `Options`
through the navigable selector overlay instead of the free-text main input
(option-free cards keep the editor-title input path), so the clarify widget
renders provided Options as a list per the report.

**Problem 2 — OAuth link / code not shown inline like Pi.**
`codexUIFromWriter` now mirrors Pi's `showAuth`/`showDeviceCode`/
`showManualInput`: the authorization URL is an OSC-8 clickable hyperlink with
a dim "Ctrl/Cmd+click to open" hint plus best-effort browser auto-open
(`plugins.BrowserBridge`); the device flow shows the verification URL as a
hyperlink, a prominent "Enter code: <userCode>" line, and "Waiting for
authentication..."; the manual-paste prompt advertises "(esc to cancel, enter
to submit)". New `ansi.Hyperlink(url, text)` emits the OSC-8 sequence
(zero-width for `Width`/`Strip`).

**Problem 2 follow-up — URL/code lost to the dead command OutputBuffer.**
Reported: "Selecting device bring back to input - no message/no action to
link." Root cause: the async `runOAuthFlow` runs on a background goroutine
whose `ctx.Writef` targeted the originating `/login` command's OutputBuffer —
echoed by the router only when the command returns. But `runOAuthFlow`
returns immediately while the flow parks, so the URL/code were written to a
buffer never rendered. Fix: new `event.SystemMessage` chat-bus event +
`Context.WriteSystem(text, preformatted)` (goroutine-safe live chat write),
`App.showSystemMessage` handler, and `runOAuthFlowSync` now takes an explicit
`uiWriter` — the async path uses a `liveOAuthWriter` (WriteSystem,
preformatted to preserve OSC-8 links) while the headless sync path keeps
`ctx.Writef`. Each notification is batched into one multi-line message so it
renders as a single chat panel (Pi login-dialog style).

Tests:
- login_test.go: `TestSelectCodexMethod_Selector{Browser,Device,CancelDefaultsBrowser}`
  + `TestSelectCodexMethod_HeadlessKeepsTextPrompt`; `TestCodexUIFromWriter_*`
  assert OSC-8 bytes, click hint, Enter-code/waiting lines, OpenURL wiring, and
  the esc/enter hint.
- login_filmstrip_test.go: `TestLoginCodexMethod_Filmstrip` drives the live
  engine method list (both options visible), arrow-down+enter → device flow,
  engine stays responsive, tokens stored. `TestLoginCodexOAuth_Filmstrip`
  (browser) now asserts the auth URL + click-to-open hint + success line are
  visible in the chat frame; `TestLoginCodexDeviceCode_Filmstrip` (new)
  asserts the verification URL, "Enter code: WXYZ-9876", and "Waiting for
  authentication..." render live. `drainChatEvents`/`applyChatEvent` mirror
  the production chat-event reader so the EventBus output reaches the
  viewport.
- internal/app/app_test.go: `TestClarify_OptionsUseSelector` /
  `TestClarify_OptionsSelectorCancel` pin option-list routing + cancel;
  `TestClarify_InputTitleShowsProgressNotQuestion` now targets the free-text
  path (option cards no longer touch the editor title).
- internal/ansi: `TestHyperlink`. tui: `TestSystemMessage_OSC8Hyperlink` —
  hyperlink survives the system-message panel (raw OSC-8 + visible URL/hint/
  code). Also fixed a latent race in the filmstrip selector stubs
  (`ShowSelector` must run via ApplySync; it mutates selector state the
  commandLoop reads).

Terminal output validated via live-engine filmstrip + ChatViewport render
dumps (selector list shows arrow-nav options; device panel shows clickable
URL, "Enter code:", "Waiting for authentication...").

Gates: vet ✓ staticcheck (5 pre-existing, unrelated, unchanged) ✓ gocognit
≤15 (26→26 no new) ✓ gocyclo ≤12 (33→33 no new) ✓ race+cover ✓.

Commits: `0d5d3ff` (method choice + clarify options as list), `39f5068`
(OSC-8 hyperlink UX + browser auto-open), `1b092fd` (live WriteSystem for
async OAuth output — the "no message/no link" root-cause fix).

<details><summary>Original issue text</summary>

</details>

## ~~Codex OAuth freeze from /provider picker — TUI frozen after OAuth choice~~ — FIXED (2026-06-24)

Report: "Adding oauth provider freeze the screen" — `/provider` → `+` → OpenAI
Codex → "Sign in with ChatGPT (OAuth)" leaves the TUI unresponsive with the
"Authenticate OpenAI Codex:" selector still displayed.

Root cause: `promptCodexAuthChoice`'s OAuth branch ran the codex login flow
synchronously inside the selector callback, which production wiring executes
on the TUI commandLoop (`SelectOptionFunc` → `app.apply`). The flow's
browser-vs-device method prompt (`Context.Clarify` → `app.clarify`) blocks for
input on the main input line — which never arrives because the selector
overlay still holds input focus (its `Hide` is queued behind the blocked
loop). Deadlock: the hidden selector swallows keys, the clarify card never
gets an answer. Same bug class as the archived `/login` freeze (2026-06-05),
one layer up: that fix made the flow *wait* async; this callback blocked
*before* reaching it.

Fix (`core/commands/provider.go`):
- `startCodexOAuthFromPicker` adds the provider optimistically, flashes
  "starting sign-in", and runs the method prompt + `loginFlowRunner` on a
  background goroutine (all UI calls on that path are goroutine-safe
  event-bus posts or internally applied).
- On sign-in failure the freshly-added provider is rolled back (a pre-existing
  entry is kept), per the original code's comment.
- `pickerProviderMu` serializes the off-loop add/rollback against each other.

Tests:
- `TestProviderPicker_CodexOAuth_Filmstrip` (new, core/commands): drives the
  real `/provider` → `+` → codex → OAuth path on a live TUI engine with a
  blocking login-flow stub. Asserts the auth selector is shown, the callback
  returns promptly, the commandLoop stays responsive while the flow is parked
  (ApplySync probe), and the provider is added without a stored key.
  Negative-controlled: a synchronous flow run fails with "UI engine frozen".
- `TestStartCodexOAuthFromPicker_FailureRollsBackProvider`: failure rollback
  (fresh provider removed, pre-existing kept).

Gates: vet ✓ staticcheck ✓ race ✓ gocognit ≤15 ✓ gocyclo ≤12 ✓ (no new hits).

## ~~Codex OAuth freeze — selecting OAuth shows nothing / TUI frozen~~ — FIXED (2026-06-05)

Report: "adding a provider — selection oauth does not bring anything"; TUI
frozen after selecting OAuth.

Root causes (both in the command layer, not the oauth package):
1. **Freeze** — `runOAuthFlow` called the blocking codex browser/device flow
   synchronously. From the auth-kind picker (or provider picker) the selection
   callback runs on the UI goroutine via `app.apply`, so the browser-callback
   wait parked the engine command loop → frozen TUI.
2. **Nothing shown** — the codex flows wrote the auth URL / device code via
   `fmt.Printf` to stdout, which the TUI never renders; no manual-paste prompt
   was wired either.

Fix:
- `runOAuthFlow` runs the flow on a background goroutine when an EventBus is
  wired (live TUI); headless stays synchronous. Progress/result delivered via
  `ctx.Writef`/`ctx.Flash` (goroutine-safe event-bus posts).
- oauth package: added `CodexUIOpts` + `LoginCodexBrowserUI`/`LoginCodexDeviceUI`
  so the host bridges `NotifyURL`/`NotifyDevice`/`PromptManualCode`/`OpenURL`.
- command layer: `codexBrowserFlow`/`codexDeviceFlow` gained `loginUI` variants;
  `codexUIFromWriter` bridges the auth URL + device code to `ctx.Writef` and the
  manual-paste prompt to the prompter (clarify card).

Tests:
- `TestLoginCodexOAuth_Filmstrip` (new, core/commands): drives the real
  `/login:openai-codex` flow on a live TUI engine (`RunLoops` so ApplySync
  serializes) with a blocking codex flow. Asserts selector shown, OAuth pick
  returns promptly, engine stays responsive while the flow is parked
  (ApplySync probe), tokens stored on completion. Negative-controlled: forcing
  the sync path makes the responsiveness probe fail ("UI engine frozen").
- `TestCodexUIFromWriter_*`: URL + device code + manual-prompt bridging.

Gates: vet ✓ build ✓ race ✓ gocognit ≤15 ✓ gocyclo ≤12 ✓ staticcheck clean ✓.

## ~~Login UX — sign-on discovery, codex auth selection, completion~~ — FIXED (2026-06-05)

All three issues resolved, follow pi wizard flow:

1. `/login` now always prints an "Available sign-on:" list (provider + auth
   kinds) alongside any stored credentials — no longer only a stored-cred view.
2. `/login:openai-codex` opens an interactive auth-kind picker (OAuth browser /
   device code / API key) via `SelectOption` when a selector is available
   (headless keeps the text list). `CompleteArgs` now completes auth kinds
   after a provider: `apikey`, `oauth`, `oauth:device`.
3. The provider picker no longer forces an API key for codex: it shows an
   "Authenticate …" choice (Sign in with ChatGPT / OAuth vs API key). OAuth
   runs the codex login flow and adds the provider with no stored key; API key
   keeps the prompt path.

Implementation: `login.go` (`loginProviders`, `splitLoginPrefix`,
`completeAuthKinds`, `pickAuthKind`, `authKindLabel/Description`,
`sharedAuthStore`, `loginFlowRunner` seam); `provider.go`
(`isCodexAuthSelectable`, `promptCodexAuthChoice`,
`finalizePresetProviderFromPicker` codex branch); `register.go`
(`registerLoginStore`). Picker title shows the user-typed alias; storage stays
normalized to `openai`.

Tests: provider+kind completion, discovery list, kind picker (device/apikey/
headless), provider codex auth choice (oauth/apikey/no-forced-key).
Gates: vet ✓ build ✓ race ✓ gocognit ≤15 ✓ gocyclo ≤12 ✓.

<details><summary>Original issue text</summary>

## Login UX — sign-on discovery, codex auth selection, completion (original)

- /login command should provide a list of possible sign-on
- /login:openai-codex should open a list of possible auth + have completion => Follow Pi wizard flow
- Openai-codex provider should not ask for an API-key but allow to select the type of auth to use (and possibly flow into the login:openai-codex)

</details>

## ~~OpenAI codex support~~ — FIXED (2026-06-05)

Import OpenAI codex support from ../pi - this include oauth login (note: /login:openai currently crash) - provider selection should allow the user to select between api key/oauth token. The auth should support normal oauth *and* device tokens.

**Resolution summary** (full plan retained below): imported the codex OAuth flow
from `../pi/packages/ai/src/auth/oauth/openai-codex.ts`. Implemented W1–W5.
Fixed `/login:openai` (now lists apikey+oauth, no silent cancel), real codex
OAuth (browser + device code) with PKCE + refresh + account-id, codex transport
identity headers + backend-api URL for OAuth, `openai-codex` catalog provider,
apikey-vs-oauth selection. All quality gates green (vet/build/race/cover,
gocognit ≤15, gocyclo ≤12, staticcheck clean). Terminal output validated.

<details><summary>Original fix plan + findings</summary>

### Fix plan (detailed)

**Reference implementation:** `/Users/muaddib/dev/pi/packages/ai/src/auth/oauth/openai-codex.ts`
(+ `device-code.ts` poll helper, `providers/openai-codex.ts`, `api/openai-codex-responses.ts`).

**Root-cause findings (current Goa code):**
1. `/login:openai` — `supportedAuthKinds("openai")` returns `["apikey"]` only →
   `handleAPIKey` → prompt → nil `ClarifyFunc` → "cancelled". No OAuth path exists
   for openai. (Reported as "crash".)
2. Existing `oauth.OpenAICodexOAuth` is a stub with wrong endpoints
   (`github.com/login/oauth/*`), wrong client id (`codex`), no refresh-token support,
   no PKCE verifier on exchange, no device-code flow. Reached only via `/login:codex`.
3. Codex API plumbing already exists (`ApiOpenAICodexResponses`, variant
   `openai-codex-responses.json`, `openai_responses` provider, catalog `openai`) but:
   - variant auth is `api_key` only (no OAuth identity headers);
   - `openAICodexResponses.RequestHeaders` returns nil (no `chatgpt-account-id`,
     `originator`, `OpenAI-Beta: responses=experimental` headers);
   - `provider/manager.go:oauthProviderFor` has no openai/codex case (no token refresh);
   - default URL `https://api.openai.com/v1/responses/codex` (pi uses
     `https://chatgpt.com/backend-api/codex/responses` for OAuth subscription tokens).
4. `oauth.Tokens` has no `AccountID` field; codex requires extracting
   `chatgpt_account_id` from the JWT claim `https://api.openai.com/auth` and sending it
   as `chatgpt-account-id` header.

**Work items:**
- W1 oauth: rewrite `OpenAICodexOAuth` — real endpoints (auth.openai.com), client id
  `app_EMoamEEZ73f0CkXaXp7hrann`, scope `openid profile email offline_access`,
  PKCE S256, browser flow with localhost:1455 `/auth/callback` listener + manual
  paste fallback, device-code flow (`/api/accounts/deviceauth/usercode` +
  `/api/accounts/deviceauth/token`, 403/404/`deviceauth_authorization_pending` =
  pending, `slow_down` honored, 15 min timeout), `Refresh` via refresh_token grant,
  `AccountID` extracted from access-token JWT. Device exchange uses redirect
  `https://auth.openai.com/deviceauth/callback`; browser uses `http://localhost:1455/auth/callback`.
- W2 tokens: add `AccountID string json:"account_id,omitempty"` to `oauth.Tokens`;
  populate on exchange/refresh for codex.
- W3 login cmd: `/login:openai` supports both kinds — prompt select apikey vs oauth,
  and for oauth: browser vs device-code (mirrors pi). `/login:openai:oauth`,
  `/login:openai:oauth:device`, `/login:openai:apikey`, `/login:openai:apikey:<key>`.
  Keep `/login:codex` as alias. Fix the nil-prompter crash with a clear error message.
- W4 provider wiring: `oauthProviderFor` returns codex provider for `openai` (and
  `codex`); refresh persists rotated tokens; variant `openai-codex-responses.json`
  gains OAuth identity headers; `openAICodexResponses.RequestHeaders` emits
  `originator`, `OpenAI-Beta: responses=experimental`, `chatgpt-account-id` (from
  token account id when OAuth); base URL for OAuth codex →
  `https://chatgpt.com/backend-api/codex/responses`.
- W5 provider selection: catalog/model flow lets user pick OpenAI codex with
  api key vs oauth token (login store decides which credential wins; oauth preferred
  when present, matching `resolveAPIKey` fallback order).

**Test approach:**
- Unit: PKCE/state generation, auth URL params, `parseAuthorizationInput` equivalent,
  token exchange/refresh against `httptest.Server` (success, error status, missing
  fields), device-code start/poll (pending→complete, slow_down increases interval,
  timeout, 404 → browser-login error), JWT account-id extraction (valid, malformed,
  missing claim), token-store round-trip incl. `account_id`.
- Login cmd: table tests with fake prompter/flow (kind listing, apikey store, oauth
  store, device flow, cancel paths, unknown provider, nil ClarifyFunc → error not panic).
- Headers: `RequestHeaders` for codex OAuth profile emits required headers.
- Manager: `resolveAPIKey` refreshes expired codex token via fake provider and
  persists rotation (extend `manager_auth_test.go` pattern).
- Regression: every existing test must still pass.

**Validation steps:**
1. `go vet ./...`
2. `staticcheck ./...`
3. `gocognit -over 15 .`
4. `gocyclo -over 12 .`
5. `go test -count=1 -race -cover ./...`
6. Interactive: run goa TUI (filmstrip/interactive shell), `/login:openai` → verify
   method menu renders; device flow prints URL+user code; apikey prompt stores key;
   `/login` lists openai credential; select codex model with oauth token → stream
   succeeds (or correct auth error without panic).

**Execution:** goals per guideline — one goal per work item (fresh context per goal),
todos inside each goal for shared-context micro steps. Commit after each work item.

</details>