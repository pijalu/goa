# Bug and feature Tracking

## Guideline
1. Create a detailed fix plan for each bug - the plan must contain test approach and validation steps - execute the plan and validate the fix when all elements are in place.
2. Any issues found must be fixed and the fix plan must be updated accordingly.
3. Issues found during testing must be fixed and the fix plan must be updated accordingly.
4. Each bug should be moved to docs/archive when tested and closed as the associated plan.
5. Use interactive shell/filmstrip to validate the output of the tool - you must verify the actual terminal output.
6. Check code quality with each tool run separately (do not chain them with `;` or `&&`):
- `go vet ./...`
- `staticcheck ./...`
- `gocognit -over 15 .`
- `gocyclo -over 12 .`
- `go test -count=1 -race -cover ./...`
Fix any issues.
! For cognitive and cyclomatic complexity, Pre-existing warnings are acceptable only if they are unrelated to the change and explicitly noted !

At the end of the session - the bug list should be empty, change committed and this file should only contain the guidelines for bug reporting.
If new items are added, restart the process.

Use goals to execute the fix plan - focus on micro tasks goals with new contextto lower context usage - use todos for micro tasks that should share context

Commit at the end of each fix with a clear and descriptive commit message

## Report format
Describe the bug or feature request under `# To fix` below. Keep one section
per item with a short title, the observed behavior, and the expected behavior.

# To fix

## Provider 400: tool arguments must be valid JSON
Observed: `Error: 400 - Error from provider (Console Go): Upstream request failed:
[invalid_request_error] arguments must be valid JSON - /Users/muaddib/dev/goa/.goa/exports/goa-export-20260907-090335.zip`
Expected: tool call arguments containing a filesystem path (zip export path) are sent as
valid JSON (properly escaped/quoted); no provider 400. Investigate argument
serialization for the tool call carrying the export path (likely missing JSON
escaping or raw string interpolation) and add a regression test.

## Luna session stuck: provider stream went silent mid-response, watchdogs have unbounded escape holes, diagnostics blind at the moment of the stall
Observed: export `goa-export-20260922-064313.zip` (session `1790051421_y7jyq4e3`,
provider `openai-codex`, model `gpt-5.6-luna`, mode `coding-posture`). Request 4
was sent at 06:42:27.936 ("Re-streaming after tool call (round 3)") and streamed
content deltas until 06:42:32.286, then went totally silent: no deltas, no
thinking, no stats events until export at 06:43:14 (41s+). At export time the
HTTP stream was still open — `http.jsonl` contained only the 3 completed
transactions (entries finalize only on body EOF/close), the turn history showed
"Total: 0 turn(s)", and the TUI was frozen on a "thinking…" spinner. The user
reported "Stuck".

Root-cause analysis (from bundle + code) found the 2-minute watchdogs
(byte-idle `provider/idle_timeout.go`, event-stall `agent_streaming.go`) should
have recovered a *silent* stream, but there are structural gaps that can turn a
stall into an indefinite hang, and the diagnostics needed to confirm the case
are blind exactly when the stall happens:

- **F1a (HIGH)**: `emitEvent` (`internal/agentic/agent_events.go`) calls
  observers synchronously on the stream-consumer goroutine with no bound. A
  wedged observer blocks the consumer *outside* the stream receive, so the
  event-stall watchdog's `CloseWithError` cannot unblock it → permanent hang.
- **F1b (HIGH)**: the event-stall watchdog resets on **every** event, while
  unmapped event types are silent no-ops (`agent_streaming.go` handleStreamEvent
  returns for unknown types). A provider streaming periodic unmapped
  keep-alive/pacing events keeps both watchdogs alive while the user sees
  nothing → indefinite apparent hang with "healthy" logs.
- **F2 (MEDIUM)**: `HTTPLogEntry` is added only when the response body closes
  (`transport/http.go` `logOnCloseBody`). During a stuck stream the primary
  diagnostics (`logs/http.jsonl`, `diagnostics/trace.json`) show nothing about
  the open request — no status, no tail, no error — which is precisely the
  moment diagnosis needs them.
- **F3 (MEDIUM)**: `summarizeRequestBody` only parses `raw["messages"]`; codex
  `/responses` payloads use `input` → `messageCount=0`, `lastIsToolResult=false`,
  `requestSummary=nil` for every codex request in the trace, and the 2048-byte
  `requestBody` tail lands inside tool schemas. The README's headline check
  ("was a tool result sent back?") cannot be performed for `openai-codex`.
- **F4 (LOW)**: `execution.activity_timeout: 30s` is validated and merged but
  consumed nowhere — dead config; the user's configured expectation is ignored.
- **F5 (UX)**: after the last content delta there is no user-visible progress
  feedback during provider silence (the thinking-stall warn only covers
  thinking-only gaps); a dead spinner for up to 120s reads as "stuck" even when
  the watchdog would self-heal.

Expected: a provider silence (silent connection *or* invisible keep-alive
events) is detected, surfaced to the user, and recovered within the configured
window; no observer can wedge the stream consumer; an export taken during a
stall shows the in-flight request with enough detail to diagnose it; request
summaries work for codex payloads; every advertised config key is wired.

### Fix plan (test approach + validation)
1. **F3** — `transport`: parse `input` when `messages` is absent (chat-style
   `role` and codex `function_call`/`function_call_output` items); capture the
   message-region tail as `RequestBody` instead of the raw body tail.
   *Test*: table-driven `summarizeRequestBody` cases (messages / codex input /
   garbage) + body-capture assertion that recent tool results are inside the
   tail. Validate: `go test ./internal/agentic/provider/transport/…`.
2. **F2** — `transport`: register a *pending* entry at response-header time,
   finalize it on close (moves to the ring); export merges pending+completed
   sorted by time; `trace.json` marks the open request and flags
   "last request still in flight at export". *Test*: pending lifecycle
   (visible while open, moves on finish) + trace anomaly assertion.
3. **F4** — `provider`: `BuildStreamOptions` uses `execution.activity_timeout`
   as `opts.IdleTimeout` when the provider has no explicit `idle_timeout`
   (bounds both byte-idle and event-stall guards). *Test*: manager test with
   config `activity_timeout: 30s`, provider idle empty/overridden.
4. **F5** — `agentic`: per-stream quiet-warning timer at stallTimeout/2 emits
   one `EventProgress` ("provider quiet for Xs, will auto-retry at Ys").
   *Test*: silent-stream harness observes exactly one progress event before the
   stall error.
5. **F1a** — `agentic`: per-observer bounded delivery queue + pump goroutine;
   if the queue stays full for the delivery timeout, warn and detach the
   observer instead of blocking the stream. *Test*: wedged observer — emit
   returns within the (test-shrunk) timeout, later events skip, healthy
   observers unaffected and in-order; panic in observer doesn't kill the pump.
6. **F1b** — `agentic`: only *mapped* events reset the event-stall watchdog;
   unmapped event types are logged (once per type per stream) and do not reset
   it. *Test*: provider streaming only unmapped events trips the stall and ends
   the turn.
7. **Gate**: run `go vet ./...`, `staticcheck ./...`, `gocognit -over 15 .`,
   `gocyclo -over 12 .`, `go test -count=1 -race -cover ./...` separately; no
   new warnings vs baseline (staticcheck `probe.go:171` S1008 and gocyclo
   `agent_probe_retry_test.go`/`editfile.go` are pre-existing). Commit each fix.

### Status: FIXED — all items executed and validated
| Item | Commit | Regression tests | Validation |
|---|---|---|---|
| F3 | `d504523` | `TestSummarizeRequestBodyCodexInput`, `TestRequestAnalysisCapturesConversationRegion` | transport pkg green |
| F2 | `da3d254` | `TestHTTPLogPendingLifecycle`, `TestHTTPLogSnapshotAllMergesChronologically`, `TestHTTPLogPendingDuringStalledStream`, `TestBuildLLMTrace_PendingLastRequest`, `TestBuildLLMTrace_NoPendingAnomalyWhenFinalized` | transport+export green |
| F4 | `b6c1872` | `TestBuildStreamOptions_ActivityTimeoutIsConsumed` (4 cases) | provider+config green |
| F5 | `28c120d` | `TestSilentStream_EmitsQuietProviderWarning`, `TestPacedStream_NoQuietProviderWarning` | agentic green |
| F1a | `ec2e287` | `TestEmitEvent_WedgedObserverDetached`, `TestEmitEvent_SynchronousForHealthyObserver`, `TestEmitEvent_PreservesOrder`, `TestEmitEvent_PanickingObserverIsolated`, `TestRemoveObserver_SynchronousWithinOnEvent` | agentic/core/tui green |
| F1b | `b6a7597` | `TestUnmappedEventFlood_TripsStallWatchdog`, `TestNoteStreamEventProgress` | agentic green |

Gate (run separately, post-change): `go vet ./...` clean · `staticcheck ./...`
= only the pre-existing `probe.go:171` S1008 · `gocognit -over 15 .` clean ·
`gocyclo -over 12 .` = only the two pre-existing entries ·
`go test -count=1 -race -cover ./...` → 87 packages ok, 0 FAIL (exit 0),
`internal/agentic` 87.9%. Issue entry ready to archive per guideline 4.

## Vercel AI Gateway: no way to add an API key
Observed: the Vercel AI Gateway provider cannot be given a credential. Goa ships
the provider in its catalog — `internal/agentic/provider/models/api.json` has
`"vercel": {"id": "vercel", "name": "Vercel AI Gateway", "npm": "@ai-sdk/gateway",
"env": ["AI_GATEWAY_API_KEY"], ...}` — and models can be selected for it, but:

- `/login` only offers the hardcoded list `loginProviders = ["copilot", "github",
  "openai", "openai-codex", "codex", "anthropic", "kimi"]`
  (`core/commands/login.go:108`) — vercel (and every other catalog provider) is
  absent from the list, from completions and from `/login` discovery, so there is
  no advertised way to store its key;
- the setup/provider flow never asks for a key at all: `core/commands/setup.go`
  contains no API-key prompt, and neither `/provider` nor the wizard prompts when
  a selected provider has no credential;
- the catalog's `env` names are dead data: no code reads them
  (`grep '"env"'` finds no consumer), so `AI_GATEWAY_API_KEY` in the environment
  is ignored;
- key resolution is only `ProviderConfig.APIKey` → auth store
  (`provider/manager_auth.go:14-34 resolveAPIKey`, used by
  `provider/manager_streamopts.go effectiveAPIKey`), with no environment fallback.

Net effect: selecting Vercel AI Gateway leaves the request unauthenticated (401)
with no prompt and no documented alternative — the user cannot add the key.

### Additional evidence — export agent-mcp/.goa/exports/goa-export-20260926-113044.zip

The failure is worse than a missing key: the request is sent to the WRONG HOST,
which is why the user sees a model error rather than an auth error.

- `config/project.yaml` / `config/user.yaml`: `active_provider: vercel`,
  `active_model: stealth/pixel-canary`, the `vercel` provider entry has
  **`endpoint: ""`**, and the model entry correctly declares
  `provider: vercel` / `model: stealth/pixel-canary`;
- `logs/http.jsonl` (single request):
  `POST https://api.openai.com/v1/chat/completions` with
  `"model": "stealth/pixel-canary"` → **400 invalid model ID** (OpenAI does not
  know a Vercel-namespaced model).

Cause: a provider entry with an empty endpoint silently falls back to the OpenAI
host (`internal/agentic/provider/runtime.go:246-252` returns
`https://api.openai.com/v1/chat/completions`), and nothing supplies a per-provider
default base URL for catalog-only gateways. Vercel AI Gateway also has no
credential prompt, so even after fixing the host the request has no key (the
user's report). Two further gaps this evidence pins down:

- the catalog carries no base URL for these gateways (only `npm`/`env`), so the
  default must come from an explicit per-provider/gateway mapping, not from the
  OpenAI fallback;
- with an empty endpoint there is no diagnostic: the request goes out to another
  vendor's API instead of failing with "provider vercel has no endpoint".
Expected: any provider Goa can select can also be given a key. Concretely
(a) the sign-on/key surface is derived from the provider catalog instead of a
hardcoded list, so vercel (and future catalog providers) appear in `/login`,
`/setup` and `/provider`; (b) selecting or adding a provider with no credential
asks for the API key, naming the catalog's env var as a hint
(`AI_GATEWAY_API_KEY`) and offering to read it from the environment; (c) the key
resolution chain honors that env var when neither config nor auth store has one;
(d) when no credential is available the failure is actionable (names the provider
and the ways to add a key) rather than a bare 401.

### Fix plan (test approach + validation)
1. **catalog-driven auth providers** (`quality: single source of truth`): expose
the provider catalog's id/name/env (from `internal/agentic/provider/models`,
where `api.json` is loaded) and derive the `/login` provider list + completions
from it, keeping the curated entries' richer auth kinds (oauth/device-code) as a
capability overlay. `loginProviders` stops being the gate for "can I add a key".
   *Test*: `TestLoginProviders_IncludeCatalogProviders` (vercel present with
   `apikey` kind), `TestLoginCompletions_CatalogProvider`,
   `TestLogin_AliasStillResolves` (codex/openai behaviour unchanged).
2. **prompt for a missing key**: when a provider is selected/added without a
credential, `core/commands/setup.go` and the `/provider` path prompt for the key
(pre-filled from the catalog env var when set), storing it via
`auth.Store.SetAPIKey`; skip the prompt when config/env/store already has one.
   *Test*: `TestSetupProvider_PromptsForKeyWhenMissing`,
   `TestSetupProvider_SkipsPromptWhenKeyPresent`,
   `TestSetupProvider_EnvVarPrefill`.
3. **env fallback in resolution**: `provider.resolveAPIKey` (or
`effectiveAPIKey`) gains the catalog env-var fallback after config → auth store,
so headless/CI use works too.
   *Test*: `TestResolveAPIKey_EnvFallback` (env set → key used; config/auth win
   over env; no key anywhere → empty).
4. **actionable failure**: when a request goes out with no credential, surface a
message naming the provider and the ways to add a key (config `api_key`,
`/login:<provider>:apikey`, env var).
   *Test*: `TestProviderRequestWithoutCredential_ActionableError`.
5. **docs**: document the catalog `env` names and the `/login:<provider>:apikey`
path in the providers doc (embedded markdown), so "where do I put the key?" has
an answer without reading code.
6. **default endpoint for gateway providers** (from the export evidence above):
give catalog-only gateways a real default base URL — for `vercel` the AI Gateway
(`https://ai-gateway.vercel.sh/v1`) — so an empty `endpoint` never routes to
`api.openai.com`; and when a provider ends up with no endpoint at all, fail with
an actionable error naming the provider instead of silently calling another
vendor. The model id must be passed through unchanged (`stealth/pixel-canary` is
valid ON the gateway).
   *Test*: `TestProviderDefaultEndpoint_VercelGateway` (empty endpoint → gateway
   host, model id untouched), `TestProviderWithoutEndpoint_FailsActionably` (no
   silent OpenAI fallback for a non-OpenAI provider), plus an
   `internal/agentic/provider` test asserting the resolved URL for a vercel model
   is the gateway's chat-completions path.
7. **end-to-end acceptance**: with only `AI_GATEWAY_API_KEY` in the environment
and `active_provider: vercel`, a request must go to the gateway host with the
catalog model id (succeeding, or failing with a gateway-side auth/permission
error — never "invalid model ID" from another vendor).
   *Test*: `TestVercelGateway_RequestRouting` (fake transport asserts host +
   model + Authorization header derived from the env key).
8. **Gate**: `go vet ./...`, `staticcheck ./...`, `gocognit -over 15 .`,
`gocyclo -over 12 .`, `go test -count=1 -race -cover ./...` (separately); archive
+ commit.
