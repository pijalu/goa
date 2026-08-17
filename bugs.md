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

No current issues.

# Archive

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
