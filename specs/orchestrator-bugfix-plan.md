# Orchestrator Bug-Fix & UI Plan — Detailed Implementation Reference

## Goal

Make `/orchestrate` work end-to-end in both interactive (TUI) and non-interactive (tool) contexts, with sensible defaults, visible status/error messages, and a dedicated UI for following orchestration work. All new LLM-facing prompt text must live in `prompts/orchestrate/*.md` and be loaded via `//go:embed`.

## Current known defects

1. **No default orchestrator roles** — `config/configs/default.yaml` ships `orchestrator.roles: {}`. A user who has not opened `/config → Orchestrator → Roles` has no roles, so `doNew` errors with `no orchestrator.roles configured`.
2. **Async interactive output is lost** — `/orchestrate` (bare) and `/orchestrate:new` without `objective` use async `ShowInput`/`SelectOption` callbacks. Any `ctx.Writef` in those callbacks writes to the `OutputBuffer` that `CommandRouter.Execute` has already consumed, so status/errors disappear. The user sees only `✓ /orchestrate completed successfully`.
3. **Hard-coded LLM prompts in `core/orchestrator`** — `runtime.go` has `Continue the pipeline with the above context. Objective: ...` and `handle.go` has `[Steering]`. The project convention requires all prompts in `prompts/`.
4. **Adapter run-id violates spec** — `OrchestratorAdapter.NewRuntime` uses `fmt.Sprintf("run-%d", time.Now().UnixNano())` instead of `internal.PrefixedHexID("run", 4)`.
5. **Tool context unsafe** — `coreContextForCommand(subs, nil)` is used by the `goa` tool; several interactive callbacks dereferenced `app` without nil checks. Currently fixed with nil guards, but interactive callbacks should be `nil` so commands can detect non-interactive mode.
6. **No persistent orchestrator UI** — only a transient Summary overlay while a run is active; no browser/list to follow all runs and agents.

## Implementation steps

### Step 1 — Create prompt files under `prompts/orchestrate/`

Create the following files. Each file is plain Markdown; Go templates use `{{.Field}}` syntax.

#### 1.1 `prompts/orchestrate/pipeline_carry.md`

```markdown
Continue the pipeline with the above context.

Objective: {{.Objective}}
```

#### 1.2 `prompts/orchestrate/steering_prefix.md`

```markdown
[Steering]
```

#### 1.3 `prompts/orchestrator/hub_orchestrator.md`

```markdown
You are the orchestrator for a multi-agent coding session. Your job is to coordinate a team of specialist agents to satisfy the user's objective.

You have access to the `delegate` tool. Use it to dispatch concrete, focused tasks to the appropriate specialist roles. Do not try to do the work yourself. After all necessary specialists have reported back, provide a concise summary.

Current objective: {{.Objective}}
```

#### 1.4 `prompts/orchestrate/fanout_role.md`

```markdown
You are part of a fanout multi-agent team working on a common objective. Do your part of the task and produce a concise result.

Objective: {{.Objective}}
```

#### 1.5 `prompts/orchestrate/pipeline_role.md`

```markdown
You are a stage in a multi-agent pipeline. Do your stage of the task based on the input context.

Objective: {{.Objective}}
```

#### 1.6 `prompts/embed.go` update

Add `orchestrate/*.md` to the `//go:embed` directive.

```go
//go:embed *.md mode/*/*.md pair/*.md task/*.md pipeline/*.md tools/*.md orchestrate/*.md
var embeddedFS embed.FS
```

Add loader helpers:

```go
// LoadOrchestratePrompt returns an orchestrator prompt template by name.
func LoadOrchestratePrompt(name string) (string, error) {
    path := filepath.Join("orchestrate", name+".md")
    data, err := embeddedFS.ReadFile(path)
    if err != nil {
        return "", fmt.Errorf("orchestrate prompt %s not found: %w", name, err)
    }
    return string(data), nil
}
```

### Step 2 — Remove hard-coded prompts from `core/orchestrator`

#### 2.1 `core/orchestrator/runtime.go`

- Import `github.com/pijalu/goa/prompts`.
- Add a `prompts` map field to `Runtime` or load them lazily.
- Replace the hard-coded pipeline carry string in `runPipeline`:

```go
// Before:
carry = r.lastMessageFor(role) + "\n\nContinue the pipeline with the above context. Objective: " + objective

// After:
carry = r.lastMessageFor(role) + "\n\n" + r.renderPrompt("pipeline_carry", map[string]any{
    "Objective": objective,
})
```

- Add helper on `Runtime`:

```go
func (r *Runtime) renderPrompt(name string, data map[string]any) string {
    tpl, err := prompts.LoadOrchestratePrompt(name)
    if err != nil {
        return ""
    }
    t, err := template.New(name).Parse(tpl)
    if err != nil {
        return ""
    }
    var buf strings.Builder
    if err := t.Execute(&buf, data); err != nil {
        return ""
    }
    return buf.String()
}
```

- In `driveOne`, render the role prompt based on topology and role:

```go
var rolePrompt string
switch r.topology {
case TopologyHub:
    if role == "orchestrator" {
        rolePrompt = r.renderPrompt("hub_orchestrator", map[string]any{"Objective": prompt})
    } else {
        rolePrompt = r.renderPrompt("fanout_role", map[string]any{"Objective": prompt})
    }
case TopologyPipeline:
    rolePrompt = r.renderPrompt("pipeline_role", map[string]any{"Objective": prompt})
default:
    rolePrompt = r.renderPrompt("fanout_role", map[string]any{"Objective": prompt})
}
if rolePrompt == "" {
    rolePrompt = prompt
}
runErr := h.RunTurn(ctx, rolePrompt)
```

#### 2.2 `core/orchestrator/handle.go`

- Replace the hard-coded `[Steering]` prefix with the loaded prompt:

```go
steeringPrefix, _ := prompts.LoadOrchestratePrompt("steering_prefix")
if steeringPrefix == "" {
    steeringPrefix = "[Steering]"
}
...
prompt = prompt + "\n\n" + steeringPrefix + " " + strings.Join(extra, "\n"+steeringPrefix+" ")
```

#### 2.3 Add `text/template` and `strings` imports as needed.

### Step 3 — Provide default orchestrator roles

#### 3.1 Add helper in `core/commands/orchestrate.go`

```go
// effectiveOrchestratorConfig returns a copy of the orchestrator config with
// default roles synthesized from the active model when no roles are configured.
// It also reports whether defaults were synthesized so the caller can warn the user.
func effectiveOrchestratorConfig(cfg *config.Config) (config.OrchestratorConfig, bool) {
    oCfg := cfg.Orchestrator
    if len(oCfg.Roles) > 0 || cfg.ActiveModel == "" {
        return oCfg, false
    }
    model := cfg.ActiveModel
    oCfg.Roles = map[string]config.OrchestratorRole{
        "orchestrator": {Model: model},
        "coder":        {Model: model},
        "reviewer":     {Model: model},
    }
    return oCfg, true
}
```

#### 3.2 Update `doNew` in `core/commands/orchestrate.go`

- Replace:

```go
oCfg := ctx.Config.Orchestrator
oCfg.Defaults.Topology = topology
if len(oCfg.Roles) == 0 {
    return fmt.Errorf("no orchestrator.roles configured — define roles in config first")
}
```

- With:

```go
oCfg, defaulted := effectiveOrchestratorConfig(ctx.Config)
if defaulted {
    ctx.Flash("No orchestrator roles configured; using default coder/reviewer/orchestrator roles mapped to " + ctx.Config.ActiveModel + ". Run /config → Orchestrator → Roles to customize.")
}
oCfg.Defaults.Topology = topology
```

### Step 4 — Fix async output delivery in interactive flows

#### 4.1 Add `flashFmt`/`flashStr` helpers in `core/commands/orchestrate.go`

```go
func flashFmt(ctx core.Context, format string, args ...interface{}) {
    ctx.Flash(fmt.Sprintf(format, args...))
}

func flashStr(ctx core.Context, s string) {
    ctx.Flash(s)
}
```

#### 4.2 Update `doNew`/`doResume`/`doDelete`/`deleteAll`/`doSteer`

- Replace `writeFmt(ctx, ...)` and `writeStr(ctx, ...)` with `flashFmt(ctx, ...)` and `flashStr(ctx, ...)` for status messages that must be visible after async callbacks.
- Keep returning errors for failure paths; the async entry points will catch and flash them.

#### 4.3 Update async entry points

In `runNewInteractive`, `runResumeInteractive`, `runDeleteInteractive`, `runSteerInteractive`, after the required args are collected, call the synchronous `do*` method and flash any returned error:

```go
func (c *OrchestrateCommand) runNewInteractive(ctx core.Context, in OrchestrateInput) error {
    if in.Objective == "" {
        if !isInteractive(ctx) {
            return fmt.Errorf("missing required argument 'objective'")
        }
        ctx.ShowInput("Objective:", "", func(value string, ok bool) {
            if !ok || value == "" {
                return
            }
            in.Objective = value
            if err := c.runNewInteractive(ctx, in); err != nil {
                ctx.Flash(err.Error())
            }
        })
        return nil
    }
    if err := c.doNew(ctx, in); err != nil {
        return err
    }
    return nil
}
```

(The recursive `runNewInteractive` call will reach `doNew`.)

### Step 5 — Fix adapter run-id generation

#### 5.1 `internal/app/orchestrator_adapter.go`

- Remove the hard-coded timestamp ID:

```go
// Before:
runID := fmt.Sprintf("run-%d", time.Now().UnixNano())
store := orchestrator.NewFileEventStore(rootDir, runID)
rt.SetIDGenerator(func() string { return runID })

// After:
runID := internal.PrefixedHexID("run", 4)
store := orchestrator.NewFileEventStore(rootDir, runID)
rt.SetIDGenerator(func() string { return runID })
```

- Remove the `time` import if it is no longer used elsewhere in the file.

### Step 6 — Make non-interactive context safe and informative

#### 6.1 `internal/app/commandcontext.go`

- Set `SelectOptionFunc`, `ShowInputFunc`, `RequestMainInput`, `ClarifyFunc`, `SubmitToAgent`, `RenderChat`, `ShowPTYOverlay` to `nil` when `app == nil` so `isInteractive(ctx)` returns false.
- Keep the nil-guard fallbacks already added (belt-and-suspenders).

#### 6.2 `core/commands/orchestrate.go`

- `isInteractive(ctx)` returns `ctx.SelectOptionFunc != nil && ctx.ShowInputFunc != nil`.
- Bare `/orchestrate` in non-interactive context prints usage (already implemented).
- Missing required args in non-interactive context return an error (already implemented).

### Step 7 — Dedicated orchestrator UI

#### 7.1 Create `tui/orchestrator/browser.go`

Implement a `Browser` component that:
- Renders a two-pane layout: left = run list, right = details of the selected run.
- Loads runs via `orchestrator.ListRuns(rootDir)`.
- Shows for each run: friendly name, status, topology, objective preview, updated time, agent count.
- Selecting a run shows its agents and a `resume` / `delete` action hint.
- Implements `tui.Component`.

#### 7.2 Add `/orchestrate:browser` command or keybinding

- In `core/commands/orchestrate.go`, add a `browser` subcommand that opens the browser.
- In `internal/app`, wire the browser to a TUI overlay or side panel.

#### 7.3 Auto-open Summary panel on run start

- In `internal/app/orchestrator_panel_forwarder.go`, ensure the overlay is shown when a run becomes active. The current code already does this; verify it works after the async-output fixes.

#### 7.4 TUI tests

- Use the `tui-test` skill to assert:
  - After `/orchestrate:new:objective=...`, the Summary panel is visible.
  - After the run finishes, the browser can be opened and lists the run.

### Step 8 — Tests

#### 8.1 Unit tests

- `core/orchestrator/runtime_test.go` — test pipeline carry prompt uses the embedded template.
- `core/orchestrator/handle_test.go` — test steering prefix uses the embedded template.
- `core/commands/orchestrate_command_test.go` — test default role synthesis, missing-role warning, async error flashing.
- `config/` — test that default config still has empty roles, but runtime synthesis fills them.

#### 8.2 TUI tests (`tui-test` skill)

- Bare `/orchestrate` → menu → new → objective → run starts → panel visible.
- `/orchestrate:new:objective=...` directly → panel visible.
- Non-interactive `/orchestrate` → usage shown.
- `/orchestrate:browser` → run list visible.

#### 8.3 `interactive_shell` smoke test

- Build the binary and run `/orchestrate:new:topology=fanout,objective=Reply with the single word: ready` in a real TUI. Verify the panel opens and agents execute.

### Step 9 — Static-analysis gate

After every step:
- `go vet ./...`
- `go test -count=1 -race -cover ./...`
- `gocognit -over 15 .`
- `gocyclo -over 12 .`

Refactor any function that exceeds the budget.

## Files to create

- `prompts/orchestrate/pipeline_carry.md`
- `prompts/orchestrate/steering_prefix.md`
- `prompts/orchestrate/hub_orchestrator.md`
- `prompts/orchestrate/fanout_role.md`
- `prompts/orchestrate/pipeline_role.md`
- `tui/orchestrator/browser.go` (new UI component)

## Files to modify

- `prompts/embed.go` — add `orchestrate/*.md` to embed and add loader.
- `core/orchestrator/runtime.go` — load role/pipeline prompts, remove hard-coded strings.
- `core/orchestrator/handle.go` — load steering prefix prompt.
- `core/commands/orchestrate.go` — default roles, flash helpers, async error handling, browser subcommand.
- `core/commands/output.go` — add `flashFmt`/`flashStr` helpers (optional, can be local to orchestrate.go).
- `internal/app/orchestrator_adapter.go` — use `internal.PrefixedHexID` for run IDs.
- `internal/app/commandcontext.go` — nil interactive callbacks when `app == nil`.
- `internal/app/orchestrator_panel_forwarder.go` — verify and wire browser refresh.
- `core/commands/help/orchestrate.long.md` — document `/orchestrate:browser` and default roles.

## Success criteria

- `/orchestrate` bare in TUI opens the menu and the new flow works with default roles.
- `/orchestrate:new:objective=...` starts a run, shows the Summary panel, and agents execute.
- Missing roles produce a clear warning and default roles are used.
- `/orchestrate` in the `goa` tool context prints usage and exits cleanly.
- All LLM prompts live in `prompts/orchestrate/*.md` and are loaded via `prompts.LoadOrchestratePrompt`.
- All static-analysis gates pass.
- Coverage does not regress.
