<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# B27 — `/login:codex` → "Sign in with OAuth (browser)" froze Goa; B28 — the auth-kind picker ignored its declared order

Closed 2026-10-16. Archived from `bugs.md`.

## Reported

"running oauth codex login freeze goa" — the `/login:codex` picker rendered, and the
application went dead; clarified as "the crash occurs after selecting oauth and typing
enter".

## Root cause (B27)

`SelectOptionFunc` callbacks are delivered **on the TUI command loop**: production wires
the selector as `app.apply(func(){ onSelected(...) })`
(`internal/app/commandcontext.go`), and every keystroke is routed through `Apply` too
(`tui/tui.go` `Start`). `pickAuthKind` called `c.Run` **inline from that callback**, so
the chosen flow executed on the loop:

* `handleOAuth` → `selectCodexMethod` parked the loop on `r := <-resCh` waiting for the
  browser/device picker — whose callback can only be delivered *by* the loop it just
  blocked. Permanent deadlock: the picker renders (the renderLoop is a different
  goroutine) but every key queues behind the parked loop, so the UI looks frozen.

A probe on a live engine with production-style delivery showed it exactly:

```
PROBE selector RESULT title="Sign in to openai-codex:" sel="oauth"
PROBE callback RUN  title="Sign in to openai-codex:" sel="oauth"
PROBE kind-picker oauth: responsive = false      <-- loop never accepts work again
PROBE flow NOT started
```

Two more entry points shared the defect:

* `/login:<provider>:oauth` typed inline — `handleSlashCommand` runs the command body on
  the loop and `LoginCommand` did not implement `core.AsyncCommand`, so
  `selectCodexMethod` parked the loop directly. The probe hung until the package
  timeout.
* `/login:<provider>:apikey` (and "Use an API key" in the picker) — `handleAPIKey` →
  `clarifier.Prompt` → `ctx.ClarifyFunc`, which **blocks by contract**
  (`core/context.go`, `internal/app/app.go` `clarify` waits on `resCh`) and must never be
  called from the loop.

## Fix (B27)

1. One helper, `runOffCommandLoop(ctx, what, run func() error)`, runs a continuation that
   blocks on the user or the network on a background goroutine and flashes the error (a
   selector callback has nowhere to return it). `pickAuthKind` dispatches through it
   instead of calling `Run` inline.
2. `LoginCommand` implements `core.AsyncCommand`: `AsyncHint` returns a spinner label for
   the invocations that block (`:<provider>:oauth`, `apikey` without an inline key) and
   `""` for the instant ones (discovery, the kind picker, `apikey <key>`, a legacy inline
   token), so the host runs the body off the loop (`runAsyncCommand`).
3. `selectCodexMethod`'s comment claimed `handleOAuth` already ran on the
   `runOAuthFlow` goroutine (it does not) — replaced with the real precondition: the
   caller must be off the loop, which both entry points now satisfy.

## Root cause and fix (B28)

`SelectorItem.PreserveOrder` was not set on the picker's items, so `sortSelectorItems`
alphabetized them: `Sign in with ChatGPT (device code)`, `Sign in with OAuth (browser)`,
`Use an API key`. The highlighted default (`kinds[0]` = `apikey`) landed on the last row
and arrow navigation walked an order contradicting the declared preference order. Every
item now sets `PreserveOrder: true`, matching `codexMethodSelectorItems()`.

## Tests (RED before the fix)

`core/commands/login_loop_block_test.go` runs a real engine with `RunLoops()` and the
production selector contract (show on the loop, deliver the callback through the loop):

* `TestLoginKindPicker_OAuthKeepsCommandLoopResponsive` — the reported path: picker →
  "Sign in with OAuth (browser)" → `assertEngineResponsive` (fails with the loop parked),
  the browser/device list renders, answering "browser" starts the flow, tokens stored.
  Also asserts the rendered row order (B28).
* `TestLoginKindPicker_APIKeyKeepsCommandLoopResponsive` — the picker's API-key row, with
  a `ClarifyFunc` that needs a free loop to answer.
* `TestLoginDirectOAuth_RunsOffCommandLoop` — `AsyncHintOf` is non-empty (the host
  contract) and the off-loop run answers the method picker.
* `TestLoginCommand_AsyncHint` — table over blocking vs instant invocations.
* `TestLoginKindPicker_ItemOrder` — declared order + `PreserveOrder` on every item.
* `TestRunOffCommandLoop_FlashOnError` — failures reach the user as a flash.

`TestLoginKindPicker_SelectsOAuthDevice` was adapted to the new (correct) asynchronous
dispatch — it waits for the store instead of assuming the callback ran the flow inline —
and `TestLoginCodexOAuth_Filmstrip` now answers the browser/device picker the fixed
ordering leads it to.

## Validation

* RED confirmed on the probe before the fix (`responsive = false`, `flow NOT started`).
* Real rendered frames of the reported path (`engine.AgentFrame().Dump()`): the picker
  rows in declared order (`› ✓ Use an API key`, then OAuth, then device), the
  `OpenAI Codex login method` list, then the live chat panels
  (`Open this URL to sign in:` … `Cmd+click to open — browser opened automatically`) and
  `OAuth login for openai succeeded.`
* `go vet ./...`, `staticcheck ./...`, `gocognit -over 15 .`, `gocyclo -over 12 .` clean
  for the touched files (the remaining repo warnings are pre-existing, in
  `core/goal_driver.go`, `core/agentmanager_turns.go`, `internal/app/subsystems_goal.go`).
* `go test ./... -count=1 -race -cover` green (90 packages, exit 0).
