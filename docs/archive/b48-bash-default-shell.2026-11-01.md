# B48 — bash tool runs the user's default shell; must default to bash

Closed: 2026-11-01

## Report

`newBashCommand` (tools/bash.go) resolved the shell from `$SHELL` (falling
back to /bin/bash), so on machines whose login shell is zsh/fish the bash
tool executed that shell instead of bash — syntax differences (globbing,
`disown`, `=~` regex, option defaults) leaked into every command, and the
model never knew which shell it was driving. Reproduced with `$SHELL`
pointing at a nonexistent binary: the tool returned "Command exited with
code -1" for a trivially valid `echo`.

Expected: bash always, with an explicit opt-in to the login shell, and —
in user-shell mode — a tool description naming the resolved shell so the
model emits matching syntax.

## Resolution

- **`resolveShell(preference)`** (tools/bash.go): `""` and `"bash"` (the
  default) always resolve to bash (`defaultBashShell = "/bin/bash"`);
  only `"user"` honors `$SHELL` (bash fallback when unset); any
  unrecognized value fails safe to bash.
- **`BashTool.Shell`** field carries the preference; `runCommand` builds
  the command via `newBashCommand(t.shellPath(), p.Command)`
  (signature now takes the resolved shell).
- **Description in user mode**: `Schema()` appends
  " Commands run in the user's shell: <path>." only when `Shell == "user"`
  — bash mode keeps the plain description.
- **Config**: `BashConfig.Shell` (`yaml:"shell"`), embedded default
  `shell: bash` (with comment), registration wiring in
  bootstrap_tools.go; a normal config value, so the cascade and /config
  can change it.
- **Docs**: bash.short.md/bash.long.md note the default and the opt-in;
  docs/TOOLS.md bash section and docs/CONFIGURATION.md bash sample carry
  the same wording.

## Validation

- RED-1 (observed, assertion-level): `TestBashTool_DefaultShellIgnoresUserShell`
  (tools/bash_shell_test.go) with `SHELL=/definitely/not/a/real/shell`
  failed before the fix ("Command exited with code -1").
- RED-2 (observed, assertion-level): `TestDefaultConfig_BashShell`
  (config/defaults_test.go) failed before the default.yaml flip
  (`Shell = ""`).
- GREEN: `go test -count=1 -timeout 120s` per touched package — tools
  (29.8s), config (5.3s), internal/app (38.1s). New coverage:
  user-mode honors a broken `$SHELL` (fails loudly), resolveShell mapping
  table (unknown values fail safe), user-mode description names the shell,
  default description omits it; existing newBashCommand callers updated to
  the new signature.
- Full gates run separately: `go vet ./...`, `staticcheck ./...`,
  `gocognit -over 15 .`, `gocyclo -over 12 .` (only the documented
  pre-existing hotspots, all unrelated), and the full race suite
  `go test -count=1 -race -timeout 600s ./...` green repository-wide.
