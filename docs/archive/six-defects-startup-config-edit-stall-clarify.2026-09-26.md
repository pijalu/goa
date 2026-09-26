# Six user-reported defects — fixed 2026-09-26

Archived from `bugs.md` per guideline 4 ("Each bug should be moved to
docs/archive when tested and closed as the associated plan"). Original entries
logged in commit `558b88d`; each fix below lists its commit, regression tests,
and validation. All gates were run separately and clean at each commit
(`go vet ./...`, `staticcheck ./...`, `gocognit -over 15 .`, `gocyclo -over 12 .`,
`go test -count=1 -race -cover ./...` — final run: exit 0, 0 FAIL lines).

---

## 1. Silent startup death: fatal config errors never reach the console

**Observed**: with a home config that fails load validation, `goa` at HEAD
`474d70c` exits in <100ms with rc=1, no TUI and NO visible message (reproduced
under a pty: 4 bytes of output). The real error only survived in
`<workspace>/.goa/crash.log`. Root cause: `setupCrashLog` re-points stderr into
an async tee (`teeStderr`); the bootstrap fatal path prints to the tee and then
calls `os.Exit(1)`, killing the drain goroutine before it flushes.

**Expected**: ANY startup fatal error is visible on the console (and in the
crash log) before the process exits.

**Fix** — commit `480afc2` "app: fatal startup errors are now visible —
synchronous stderr-tee flush before exit":
- `internal/app/crash_log.go`: `flushStderrTee()` (drain + wait) with a
  synchronous delivery guarantee; single `fatalExitf` helper used by every
  `os.Exit` path in `internal/app` (bootstrap.go, app.go, mcp_cli.go).
- Validated under a pty against a bad `GOA_HOME` config: rc!=0 AND the message
  visible on the pty.

**Tests**: `TestFlushStderrTee_DeliversPendingBytesBeforeExit`
(internal/app/crash_log_unix_test.go) — write → flush → bytes on the original
writer, no goroutine dependency.

---

## 2. Contradictory stall-timing pair in one layer refuses to start — must autocorrect

**Observed**: commit `da8120b` added `checkActivityPairLayer`: when ONE cascade
layer sets both `execution.activity_timeout` and `execution.activity_warn_after`
with warn >= timeout, config load returns an error and goa refuses to start —
breaking existing installs on upgrade, while the runtime already defines
graceful semantics (`effectiveStallWarnAfter` falls back to 2/3 of the window).

**Expected**: an explicit contradictory pair within one layer is AUTO-CORRECTED
at load — the layer's warn value is dropped with a visible warning naming the
file — and startup proceeds. Invalid duration shapes stay errors.

**Fix** — commit `0cdb1bc` "config: autocorrect a contradictory stall-timing
pair instead of refusing to start":
- `config/loader_yaml.go`: the per-layer pair check mutates the layer (drops
  the warn override) and warns on stderr naming the file, instead of failing
  `Load`; `config/loader.go` plumbing. Heal is memory-only — the file on disk
  is untouched until the next legitimate write (see #3's heal-on-write).

**Tests**: `testActivityWarnAfterLayerPair` table extended with `wantHealed`
cases (valid pair / warn==timeout / warn>timeout): load succeeds, warn override
dropped, heal warning surfaced via `requireHealWarning` + `captureStderr`
(config/stall_timing_test.go).

---

## 3. /config writes configs it never validated (wrote the contradictory 45s/45s pair)

**Observed**: setting `execution.activity_timeout` (or warn) via /config
persists through `applyConfigSet` → `SaveHomeField` with NO pair validation —
only the single value's duration shape is checked. goa wrote a config goa
cannot start with.

**Expected**: goa must never write an unvalidated config; incorrect
configuration is autocorrected to avoid startup errors.

**Fix** — commit `a16129f` "config: every persisted config is validated — never
write what goa cannot load":
- Writer-level guard in `config/loader_fields.go` (`writeYAMLDocument`):
  validate the marshaled bytes BEFORE writing. Stall-key edits that would
  persist a contradictory pair are REFUSED; unrelated edits HEAL (drop the
  stale `activity_warn_after` from the file) with a stderr warning.
  `config/loader_edit.go` threads the edited path so the guard can
  distinguish refuse vs heal. `config/loader.go` `Save()` keeps
  write + validate + byte-for-byte rollback.
- `/config set` path (`core/commands/config_cli.go`): pair policy before
  commit — explicit warn >= window refused with a flash; window change drops
  the now-stale warn lead on disk before persisting; invalid candidates
  refused via `internal.ValidationError`.
- Test-infrastructure fix: skills tests were writing into the REAL
  `~/.goa/config.yaml` (no HOME isolation) — `skillTestContext(WithHistory)`
  now `t.Setenv` HOME/USERPROFILE.

**Tests** (config/write_guard_test.go, core/commands/config_stall_timing_test.go):
`TestValidateConfigBytes`, `TestSaveHomeField_RejectsContradictoryPair`
(refused, file byte-identical), `TestSaveHomeField_HealsContradictoryPairOnUnrelatedWrite`
(broken 45s/45s file healed by an unrelated write; reload falls back to the
default 30s lead), `TestSave_RollsBackContradictoryConfig`,
`TestConfigSet_ActivityTimeoutDropsStaleWarnLead`,
`TestConfigSet_ActivityWarnAboveWindowRejected`.

---

## 4. Edit tool renderer drops file names (batch edits show "edit ...", result header hidden)

**Observed**: (a) batch-form edits `{"edits":[{path, ...}]}` have no top-level
`path`, so `EditFileRenderer.RenderCall` shows `✓ edit ...`. (b) `RenderResult`
renders only the first diff hunk body — `extractDiff` discards the tool
result's `[edit: <path>] N edits applied — match: ...` header, so the file name
appears nowhere in the TUI.

**Expected**: the call title shows the target path(s) (first path + "(+N more)"
for batch); the result keeps the header line (muted) above the diff.

**Fix** — commit `d6cb8e2` "tools: edit renderer names the file — batch calls
and diff results" (tools/edit_renderer.go): `editTargetPath` resolves the
top-level path or the first `edits[i].path` with an "(+N more edits)" suffix;
`RenderPartial` shows the per-file path and edit count; `extractDiff` keeps the
non-`@@` preamble and `formatDiffOutput` renders it muted above the diff.
Collapsed preview bound adjusted to keep the header
(tools/edit_renderer_cap_test.go).

**Tests**: `TestEditFileRenderer_RenderCall_BatchPathFromEdits`,
`TestEditFileRenderer_RenderPartial_BatchShowsPath`,
`TestEditFileRenderer_RenderResult_KeepsHeaderPath`
(tools/edit_renderer_test.go) and the filmstrip UI test
`TestEditToolUI_ShowsFileNameForBatchCallAndResult`
(internal/app/edit_tool_ui_test.go) asserting the path and the
"[edit: …] 2 edits applied" header are visible in the rendered frame
(guideline 5).

---

## 5. Stall-timing values are displayed/entered with a glued unit ("60s" instead of "60")

**Observed**: /config retry-settings labels and stall-timing prompts prefill
Go-duration strings ("45s", "2m0s"); the user reads/edits these as plain
seconds and expects "60", not "60s". Entering a bare number ("60") was
rejected by `time.ParseDuration`.

**Expected**: the stall-timing UI speaks plain seconds — labels/prefills render
whole-second values as bare numbers, setters accept a bare number as seconds
alongside explicit durations; persisted values stay canonical ("45s").

**Fix** — commit `1e76873` "config: stall-timing UI speaks plain seconds":
- `core/commands/config_cli_setters.go`: `parseStallDuration` (bare positive
  int → seconds; explicit durations pass through AS TYPED — "60s" is persisted
  as "60s", never rewritten to "1m0s"; garbage → error with a hint),
  `formatStallSeconds` (whole seconds → bare number), `setStallDuration`;
  dead `validateDurationValue` removed.
- `core/commands/config_menu_retry.go`: labels "45 (warn at 30)" /
  "30 (derived: 2/3 of 45)"; prompts prefill plain seconds with
  "(seconds, e.g. 60)".

**Tests**: `TestStallTiming_PlainSecondsPrimitives`,
`TestRetrySettingsMenu_PrefillsPlainSeconds` (prefill "45", bare "60" →
persisted "60s", re-prefill "60") plus label/menu tests re-pinned
(core/commands/config_stall_timing_test.go).

---

## 6. Clarification cards with options give no way to type a custom answer

**Observed**: `App.clarify` routed option-carrying questions exclusively to the
selector overlay: arrow keys + Enter pick a listed option, Esc cancels. The
user could not express an answer that is not one of the proposed options.

**Expected**: a clarification/user question ALWAYS allows the user to express
another option — the selector offers an explicit "type your own answer" affix
that opens the main input line for free text; Esc still cancels.

**Fix** — commit `92e4ec9` "app: clarification always offers
type-your-own-answer; esc cancels pending clarify input"
(internal/app/app.go, internal/app/tui.go):
- The selector appends a `✎ Type your own answer` row (order-preserving).
  Picking it requests the main input (`requestMainInputWithCancel`) with the
  clarify card's title/progress prompt; the typed answer is delivered to the
  waiting tool caller through the normal pending-main-input submit path.
- **Cancel affordance**: Esc now cancels a pending main-input request as the
  first step of the hard-stop sequence (`App.handleEscape` →
  `cancelPendingMainInput`; the request's `onCancel` delivers ok=false). This
  is the standard pending-input cancel semantics, shared with Ctrl+C on an
  empty editor (`engine.OnCancelInputRequest`); `cancelPendingMainInput` is a
  no-op when nothing is pending, so normal Esc behavior is unchanged.

**Tests**: `TestClarify_OptionsOfferCustomAnswer` (selector affix visible →
pick custom row → free-text input registered → typed answer reaches the caller
through the real `makeSubmitHandler`/`handlePendingMainInput` wiring) and
`TestClarify_CustomAnswerEscCancels` (esc at the free-text step → ("", false))
— internal/app/clarify_custom_answer_test.go, replicating the production
wiring (SetOnSubmit, OnEscape → handleEscape, OnCancelInputRequest).
Full clarify suite (6 tests) green.
