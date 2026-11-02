# B55 — new-install defaults: mcp tool on, smartsearch off

Archived 2026-11-02. The report and plan lived in `bugs.md` (# To fix).

## Report

New-install tool defaults needed verification after B46/B51 reshaped the
toggle surfaces: the `mcp` bridge tool must ship enabled ("on") on a fresh
install (`tools.enabled.mcp`, default-ON with opt-out), while `smartsearch`
must not be registered by default — the default set stays the agreed
minimum (bash/edit/write/read, goals, todos, webfetch, mcp).

## Ground truth (verified before any change)

- `tools/registry.go` ConfigurableTools metadata: `mcp` Default: true,
  `smartsearch` Default: false.
- `config/configs/default.yaml`: `tools.enabled.mcp: true`;
  `tools.smartsearch.enabled` absent → ships off.
- `config/defaults_test.go`: pins smartsearch off;
  `config/tools_default_mirror_test.go`: mirrors every Default flag to the
  embedded config (ask_user_question via the documented
  `!clarify_disabled` inversion).

What was NOT pinned end-to-end: a fresh install (no home, no project
config) booting with ONLY the embedded defaults and the production
registration path actually registering mcp and not smartsearch.

## Fix (test-only — no production change, as planned)

`internal/app/b55_fresh_install_defaults_test.go` — three pinning tests
over a real fresh install (sandboxed `GOA_HOME`, project without `.goa/`,
cascade = embedded defaults alone):

1. `TestFreshInstall_FlagsPinsDefaultOnAndOff` — the fresh config carries
   `tools.enabled.mcp: true`, `tools.smartsearch.enabled: false`, and the
   default-ON webfetch/todo_list/goal flags.
2. `TestFreshInstall_RegistryHasMCPOnlyDefaults` — the production boot
   path (`registerTools` + `registerWebFetchTool` +
   `registerSessionQueryTools`) registers `mcp` and `webfetch`; none of
   smartsearch/python/run_code/terminals/verify/bg_exec/ssh_bash/memento/
   lsp/session_search/session_event_read/schedule_create/agent appear.
3. `TestFreshInstall_DefaultMetadataMirrorsFlags` — every ConfigurableTools
   Default flag agrees with the fresh-install flags (inverted
   `ask_user_question` key handled like the config mirror test).

The first draft's layer-3 assertion caught a real subtlety: comparing
`Default` against `GetEnabled(name)` fails for the opt-out
ask_user_question (inverted `clarify_disabled` flag) — the same rule the
config mirror test documents; the test now applies it.

## Validation

- New tests green under race; internal/app full suite green under race
  (42 s).
- Gates separately: `go vet ./...` clean, `staticcheck ./...` clean,
  `gocognit -over 15 .` — first draft of the test scored 19 (>15) and was
  split into the three subtests above; remaining warnings are the
  documented pre-existing hotspots (b53 filmstrip test, startplan test,
  GoalDriver.Drive, tool-factory coverage test, config mirror test,
  initGoalSystem, executeRunner), all unrelated.
- `gocyclo -over 12 .` clean apart from the same pre-existing set.
- Full `go test -count=1 -race -cover ./...`: exit 0, 91 packages ok, no
  FAIL lines.

Residual risk: none — no production code changed; the pins fail loudly if
any layer (yaml, Default metadata, boot registration) drifts.
