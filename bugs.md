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

## OpenAI codex support
Import OpenAI codex support from ../pi - this include oauth login (note: /login:openai currently crash) - provider selection should allow the user to select between api key/oauth token.

The auth should support normal oauth *and* device tokens

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
