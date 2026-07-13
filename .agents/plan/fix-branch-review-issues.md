# Fix all issues from the `feature/self` branch review

Work **autonomously through every item**, in the order listed (priority grouping).
For each fix, follow AGENTS.md Hard Rules — especially:
- **Rule 3: every fix must include a test that would have caught it.** Fix the code, not the test.
- **Rule 1/4: fix root causes; a huge correct implementation beats a small incorrect one.**
- **Rule 6: SOLID** — small generic primitives, no fat per-type APIs.

## Constraints / gates (must be green before you finish)
- `go build ./...`
- `go vet ./...`
- `go test -count=1 -race -cover ./...`
- `gocognit -over 15` (all logic ≤15; config ≤20; tui ≤18)
- `gocyclo -over 12` (all logic ≤12; config ≤12; tui ≤12)
- `staticcheck ./...` (clean)

Testing skills: use the **golang-check** skill before committing each area.
Where behavior is visible in the TUI (background panel, footer), also exercise the **tui-test** skill (Filmstrip, no raw ANSI).

**Commit incrementally, one commit per priority group (A, B, C, D, E, F).**
Use clear messages like `fix(auth): close RLock data race in Store.Save and propagate load errors`.

---

## Group A — Data races & concurrency correctness

### A1. `internal/auth/store.go` — confirmed data race + swallowed errors
Evidence: `Save()` takes `mu.RLock()` then calls `loadKey()` which writes `s.key` and writes the key file. Reproduced under `-race` (Store.go:50 write vs Store.go:134 read).
Required fix:
- Make `NewStore(path string) (*Store, error)`. Load key + creds once, in the constructor, propagating errors. Update **all** callers (search `auth.NewStore(`).
- `Save()` must never call `loadKey()`. It may take `RLock` only because the key is guaranteed loaded after construction. If you keep lazy key init anywhere, do it under a **write** lock.
- `loadEncrypted` likewise must not re-enter `loadKey` from a read path.
Required test: `TestStore_ConcurrentSaveNoRace` — force key-nil scenario, hammer `SetAPIKey` + `Save` from many goroutines, run under `-race`, assert clean + file decrypts.

### A2. `internal/background/manager.go` — `Start()` returns a live `*Task` that another goroutine mutates under lock
Required fix: `Start` must return a **copy** (e.g. `cp := *task; return &cp, nil`), consistent with `Get`/`List`.
Required test: start a process, mutate the returned task's Status, assert the manager's copy is unaffected (use `Get`).

### A3. `internal/background/manager.go` — `persist()` races + non-atomic writes
Required fix:
- `persist()` must be **atomic**: write to `path+".tmp"` then `os.Rename` to `path`. `fsync` optional.
- Serialize: `Start()` should snapshot tasks under the lock and the persist must run consistently (either fully under lock, or snapshot-under-lock + atomic-write-outside). Ensure `waitForExit`'s persist and `Start`'s persist can't interleave on the file.
Required test: `TestManager_PersistAtomicConcurrent` — many short-lived procs + concurrent `Start`, under `-race`; assert the registry file always parses as valid JSON and final state is consistent.

---

## Group B — Background manager correctness vs. its documented design

The package doc claims: *"durable JSON registry ... so tasks survive agent restarts"*. Today this is false. Fix all of:

### B1. `StatusKilled` is dead code; killed tasks are reported as `error`
Today `terminate()` signals; `waitForExit()` then does `if exitErr != nil { StatusError }`. The signal makes `cmd.Wait()` return non-nil, so kills are always `error`. `StatusKilled` is only ever compared against.
Required fix: introduce a `killed bool` on `runningProc`. `terminate()` sets it (under lock) before signalling. `waitForExit()` must keep `StatusKilled` when killed (do not overwrite with Error). Exit code should be the signal's code (negative on Unix).
Required test: `TestManager_StopMarksKilled` — start `sleep 30`, `Stop(id, grace)`, assert `Get(id).Status == StatusKilled` and ExitCode < 0. Fix the existing test at `manager_test.go:74` that tolerates `Killed || Error` to assert `Killed` exactly.

### B2. Restart durability + ID collision
`load()` fills `tasks` but not `procs`; `counter` stays 0 → first post-restart `Start` reuses `proc-1` and overwrites an existing task.
Required fix:
- Reconstruct `m.counter` from loaded task IDs (parse numeric suffix of `proc-N`); start at max+1.
- On `load()`, reconcile each task that is still `StatusRunning`: probe whether its PID is alive (`os.FindProcess(pid)` + `signal 0` on Unix; on Windows use `OpenProcess`/`tasklist`). If dead → mark `StatusCompleted`/`StatusError` with a recorded note. If alive → keep `StatusRunning` and **support Stop-by-PID** (B3) so the user can still kill it.
Required test: `TestManager_Restart_ReconstructsCounterAndReconciles` — start a long-running durable process, create a **new** `Manager` on the same path, assert: counter continues (next Start yields a new id, no collision), the still-running task is loadable, and a dead task gets reconciled to a terminal state.

### B3. Reattach-for-kill + persisted output (OutputDir is currently dead)
`OutputDir` is created but never written; `ReadOutput` returns nil after restart.
Required fix:
- Tee stdout/stderr to files under `OutputDir` (`stdout.log`/`stderr.log`) as lines arrive (append). Keep the ringBuffer for fast reads.
- `Stop`/`StopAll` must work for tasks that have a live PID but no `runningProc` (the restart case): signal by PID directly (reuse the cross-platform kill helpers in `internal/sandbox` or a small `killPID` helper).
- `ReadOutput(id, n)`: when a `runningProc` exists, read the ring (current behavior); otherwise read the last `n` lines from the persisted log files.
Required test: `TestManager_OutputPersistsAcrossRestart` — start a process that prints lines, reload manager, `ReadOutput` returns persisted lines; `TestManager_StopAfterRestart` — reload manager on a live process, `Stop` kills it by PID and marks `Killed`.

---

## Group C — LSP subsystem

### C1. `internal/lsp/manager.go` — `DidChange` hardcodes `Version: 2` (comment admits it's wrong)
Required fix: add per-document version tracking (`map[string]int` under a mutex, or atomics). `OpenDocument` records version (start at 1); `DidChange` increments and sends the next version.
Required test: extend the fake server in `internal/lsp/*_test.go` to capture received versions; assert two consecutive `DidChange` calls send strictly increasing versions.

### C2. LSP diagnostics are never consumed; gopls leaks on shutdown
Evidence: `writefile.go`/`editfile.go` call `OpenDocument`/`DidChange` and discard errors; `DiagnosticsFor`/`HasErrors` have zero readers outside tests; `mgr.Close()` is never wired into shutdown (`internal/app/tui.go` only calls `StopAll`).
Required fix:
- In `WriteFileTool`/`EditFileTool`, after notifying the LSP, query `DiagnosticsFor(resolvedPath)` (small settle delay is OK, e.g. ≤200ms via a helper) and **append** any diagnostics for the edited file to the tool result as a compact `Diagnostics:` block. Errors from LSP notify must not fail the tool (continue gracefully).
- Add a `DiagnosticsFor` method to the `LSPDocumentManager` interface in `tools/` (keep nil-safe).
- Wire `lsp.Manager.Close()` into app shutdown: in `internal/app/tui.go` (and headless path), call `Close` on the LSP manager during teardown, alongside the existing `StopAll`. Expose the manager from `subsystems`/tools so the app can reach it.
Required test:
- `tools/lsp_integration_test.go`: extend `fakeLSPManager` to return diagnostics via `DiagnosticsFor`; assert the edit/write tool result contains them.
- Use the **tui-test** skill to verify that returning diagnostics from a tool doesn't break tool-rendering / footer activity in the Filmstrip.

---

## Group D — Security

### D1. `internal/secrets/scanner.go` — `aws_secret_access_key` mass false positives
Evidence: pattern `[A-Za-z0-9/+=]{40}` matches **any** 40-char hex/base64. Confirmed a git commit SHA (`e3b0c44298fc1c149afbf4c8996fb92427ae41e4`) is flagged + redacted — disastrous for a code agent that shows `git log` output.
Required fix: drop the unconditional bare-40-char pattern. Only match AWS secret keys when there is a contextual key name, e.g. extend `genericSecret` to include `aws_secret_access_key|secret_access_key|aws_secret` as key prefixes, OR require the secret to be *near* an `AKIA` access key id. Keep coverage for real AWS secrets via context, not by length alone.
Required test:
- `TestScanner_GitSHANotFlagged` — 40-hex SHA is **not** matched.
- `TestScanner_AWSSecretWithContext` — `aws_secret_access_key=<40 base64>` **is** matched.
- Keep the existing scanner tests green; update any test that relied on the bare pattern.

### D2. `plugins/manager.go` — `isGitURL` accepts `http://` (MITM → RCE for executed JS)
Required fix: reject `http://`. Allow only `https://`, `git@`, or `.git` over https/ssh. Return a clear error.
Required test: `TestInstall_RejectsHTTP` — `http://example.com/x.git` → error mentioning https.

### D3. `plugins/manager.go` + `lockfile.go` — no synchronization
Required fix: add a `sync.RWMutex` to `Manager` (guard `enabled` + all lock mutations/reads) and to `Lockfile` (guard `Plugins`). Keep public method signatures stable.
Required test: `TestManager_ConcurrentAccess` — concurrent `Install`(via `SetCloneFunc` fake)/`Enable`/`Disable`/`List`, under `-race`, assert clean.

### D4. `plugins/manager.go` — `moveDir` shells out to `mv` (not Windows-portable)
Required fix: implement an in-Go cross-device fallback (recursive copy preserving file mode, then `RemoveAll` src). Keep `os.Rename` as the fast path.
Required test: `TestMoveDir_AcrossTempDirs` — move between two `t.TempDir()` roots, assert contents + file modes equal.

### D5. `plugins/manager.go` — content hash never re-verified
Required fix: on `Enable` (after the trust check) and on `loadEnabledPlugins` entry, recompute the hash and compare to the lockfile; refuse to enable / warn if mismatched (integrity gate). Provide a clear error path.
Required test: `TestEnable_DetectsTamperedPlugin` — mutate a file under the installed plugin dir, assert `Enable` errors.

---

## Group E — Provider/auth wiring & consistency

### E1. `provider/manager.go` — `ListModels`/`TestConnection` ignore the auth store
Evidence: `BuildStreamOptions` uses `resolveAPIKey(authStore,...)`; `ListModels` uses only `provider.APIKey`. OAuth/API-key-only providers can chat but `/models` fails.
Required fix: in `ListModels`, when `provider.APIKey == ""`, resolve via `pm.authStore` using the same `resolveAPIKey` helper (refactor so both call sites share one resolver).
Required test: `TestListModels_UsesAuthStore` — provider with empty config key but a stored API key → request carries `Authorization: Bearer <stored>` (use an `httptest.Server`).

### E2. `provider/manager.go` — refreshed OAuth token never persisted (rotated refresh token lost)
Required fix: when `resolveAPIKey` refreshes a token, persist the resulting `*oauth.Tokens` back to the store (`store.SetOAuth`) so refresh-token rotation survives. Be careful to avoid a recursion/write storm (only write when the token actually changed).
Required test: `TestResolveAPIKey_PersistsRefreshedToken` — fake token source returns a new token with a new refresh token → store now holds the new tokens; subsequent resolve does not refresh again within validity.

### E3. `provider/manager.go:738` `applyProviderStreamOptions` gocyclo = 13 (budget 12)
Required fix: split into focused helpers (e.g. `applyAPIKey`, `applyTimeoutAndRetries`, `applyTransportAndCache`, `applySessionMetadata`). Keep behavior identical.
Required test: existing provider tests must stay green.

### E4. `core/commands/login.go` — Anthropic kinds vs flow mismatch
`supportedAuthKinds("anthropic")` advertises `oauth` but `newOAuthFlow("anthropic")` returns nil.
Required fix: make the two consistent. Simplest correct fix: `supportedAuthKinds("anthropic")` returns only `apikey` until OAT is actually implemented. (If you implement Anthropic OAT instead, add a real flow + test.)
Required test: `TestSupportedAuthKinds_Anthropic` — only `apikey`; and `TestLogin_AnthropicOauth_Rejected`.

---

## Group F — Hooks & verify polish

### F1. `internal/hooks/hooks.go` — `exitCode()` masks launch failures for non-beforeTool events
When a hook binary doesn't exist, `ProcessState` is nil and err isn't an `ExitError` → returns `0` (success); `fire()` returns nil for non-beforeTool events, silently dropping a broken hook.
Required fix: distinguish "failed to start" (return a distinct non-zero code, e.g. `-1`, and record `Output`/reason). For non-beforeTool events, still record the failure in the audit entry (and optionally return an error so callers can log), but do **not** veto.
Required test: `TestFire_NonexistentCommand_RecordsFailure` — a hook pointing at `/no/such/binary` produces an audit Entry with non-zero exit code and non-empty error info.

### F2. `internal/verify/runner.go` — `DurationMs` uses CPU user-time, not elapsed
Required fix: capture `time.Now()` before `cmd.Run`/`CombinedOutput`, set `DurationMs = time.Since(start).Milliseconds()`.
Required test: `TestGoTestRunner_DurationIsWallClock` — run a trivial `go test` in a temp module, assert `DurationMs >= 0` and roughly matches elapsed (sanity range).

### F3. `internal/verify/runner.go` — NPM/Pytest runners never populate `Failures`
Required fix: add minimal failure extraction: npm (mocha/jest) → lines containing `✕`/`failing`/`failed`; pytest → `FAILED` lines. Keep it best-effort. Even partial extraction helps the remediator.
Required test: `TestParseNPMFailures` / `TestParsePytestFailures` on captured sample output.

### F4. `tools/verify.go` — `verifyRunnerFactory` closure carries an unused `raw` param
Required fix: clean the closure signature so branches don't carry ignored args (use a small typed dispatcher). Behavior unchanged.
Required test: existing `verify_test.go` stays green; add a case asserting `command: "go test ./..."` routes to `GoTestRunner`.

### F5. `tools/bgexec.go` — tool constructor installs a process-wide signal handler
`NewBGExecToolWithManager` calls `signal.Notify(SIGINT/SIGTERM)` on every construction (accumulates; a tool hijacking signals is an SRP violation).
Required fix: remove the `signal.Notify` block from the constructor. Keep `StopAll()`; the app already calls it on shutdown (`internal/app/tui.go`). Confirm the app's shutdown path still calls `StopAll` after the change.
Required test: `TestBGExecTool_StopAllStillWorks` — start a proc, call `StopAll()`, assert it's killed. Use **tui-test** to confirm the background panel still clears on shutdown.

---

## When you finish
1. Run the full gate (`go build`, `go vet`, `go test -count=1 -race -cover ./...`, `gocognit -over 15`, `gocyclo -over 12`, `staticcheck ./...`) — all must be green.
2. Optionally run the **tui-test** skill once over a session that uses `bg_exec` + an edit that yields LSP diagnostics, to confirm the Filmstrip is healthy.
3. Print a concise per-group summary of what changed and the tests added.
