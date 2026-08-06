# Plan — /fork: session → turn picker for new conversation start

## Goal

Replace the current `/fork:<parent-node-id>` (session-tree branch) command with
a pi-style fork flow:

1. `/fork` (no args) opens a **session picker** (all saved sessions with model
   turns, newest first — same ordering/labels as `/session:restore`).
2. Selecting a session opens a **turn picker** listing that session's user
   messages (the forkable turns), most recent preselected.
3. Confirming a turn starts a **new conversation** (fresh session ID) whose
   history = source events **up to, not including** the selected user message.
   The selected message text is made available for re-submission (editor
   prefill), matching pi semantics (fork → edit → resend).

No backward compatibility: the session-tree branch behavior of `/fork` is
removed (tree branching remains available via `/tree` / `/clone`).

## Current state (evidence)

- `core/commands/fork.go` — only caller of `sessiontree.Manager.Fork`
  (verified by grep); requires an explicit parent node ID.
- `core/commands/session_persist.go` — existing patterns to reuse:
  `showSessionPicker` (selector flow), `buildSessionItems` (timestamped
  labels), `restoreSession` (history rebuild + replay), and
  `filterSessionsWithModelTurn`.
- `internal/agentic/events_to_history.go` — `EventsToHistory` rebuilds
  `[]Message` from stored `OutputEvent`s; user turns are
  `OutputEvent{Type: EventContent, Role: User, Text != ""}`.
- `core.SessionStoreAPI` — has `ListSessions`, `LoadSession`,
  `StartSessionWithID` (no bare `StartSession`; fresh IDs can be derived, see
  below).
- `core/agentmanager.go` — `SetPendingInputHistory` +
  `internal/app/submithandler.go:671` applies pending history to the editor
  (`inp.SetHistory`) after command execution. This is history-list injection,
  not direct text prefill; editor text prefill needs the app seam
  (`restoreSteeringToInput` pattern at `internal/app/submithandler.go:212`
  uses `inp.SetText`).

## Design

### New file: `core/commands/fork.go` (rewritten)

`ForkCommand` deps change from `{Manager *sessiontree.Manager}` to using
`core.Context` role interfaces only (project convention): `OutputWriter`,
`EventSink` (+`AgentEventReplayer` assert), `Selector`, `SessionStoreAPI`,
`*core.AgentManager`. No new Context fields.

Flow (`Run(ctx, args)`):

1. `sessions := filterSessionsWithModelTurn(store.ListSessions())`; empty →
   "No saved sessions found." (reuse existing helpers from
   `session_persist.go` — they live in the same package).
2. `ctx.SelectOption("Select session to fork:", buildSessionItems(sessions), "", cb)`.
3. In callback: `events := store.LoadSession(selected)`; build fork points:
   index of every `EventContent`/`Role==User`/`Text!=""` event, in order.
   Empty → flash "No messages to fork from".
4. Turn picker items (`tui.SelectorItem`, `PreserveOrder: true`):
   - `Value`: stable encoding `<event-index>` (decimal string) — selection
     callback maps back to the cut point.
   - `Label`: `Turn N  <truncated first line>` (reuse
     `truncateFirstMessage`); `Description`: turn position `N of M`.
   - Order: oldest first or newest first? **Newest first is wrong** for a
     conversation transcript; use chronological (oldest→newest) with the
     cursor preselected on the **last** turn (`current` = last value),
     mirroring pi (`initialSelectedId = last message`).
5. Confirming a turn → `forkSession(...)`:
   - `cut := events[:idx]` (everything before the selected user message).
   - `history := agentic.EventsToHistory(cut)`.
   - New session ID: `<sourceID>_fork_<unix>` via
     `store.StartSessionWithID(forkID)` — a fresh identity so continuing the
     fork never appends to the source file (pi forks to a new file).
     Validate the derived name against the store's session-name rules; fall
     back to `fork_<unix>_<rand>` if the source ID is not a valid filename
     base. (Avoids extending `SessionStoreAPI`; keeps the interface
     unchanged.)
   - Agent wiring (mirror `restoreSession`): if `agentMgr.CurrentAgent() != nil`
     → `agent.SetHistory(history)`; `opts.SessionID = forkID`;
     `agent.SetStreamOptions(opts)`. Input history: reuse
     `buildCombinedInputHistory` for the **source** session (keeps the user's
     recent inputs for recall).
   - UI: `es.ClearChat()`, flash `Forked '<src>' at turn N → new session
     '<forkID>'`, then goroutine `replayer.ReplayAgentEvent(ev)` for `cut`
     events (async, same deadlock-avoidance as restore).
   - Editor prefill of the selected message: **deferred** — the command layer
     has no SetText seam today; adding one (`Context` callback +
     `internal/app` wiring) is listed as optional step 6 below. Without it the
     user re-types or recalls via input history (the source session's inputs
     are loaded, so the selected message is an Up-arrow away).

### Optional step 6 (editor prefill, pi-parity)

Add `Context.SetEditorTextFunc func(string)` (nil-safe, like
`SelectOptionFunc`); implement in `internal/app/commandcontext.go` via
`inp.SetText`. Fork calls it with the selected message text when available.
Small, isolated; do it in the same change if the wiring is trivial, otherwise
follow-up.

### Help text

Rewrite `core/commands/help/fork.long.md` for the new flow.

### Command registration

`core/commands/register.go:131` — `&ForkCommand{}` (no deps; drop the
`SessionTree` field from this command's struct only; `dep.SessionTree` stays
for tree/clone commands).

## Tests (test-first; new `core/commands/fork_test.go`, old tree-fork tests removed)

Reuse the fakes from `session_command_test.go` (fake store with in-memory
sessions, capture-selector that records `SelectOption` calls and invokes the
callback with a chosen value).

1. **No sessions** → message "No saved sessions found.", no selector shown.
2. **Session picker shown** → items carry timestamped labels, PreserveOrder,
   model-turn filter applied (command-only session excluded).
3. **Turn picker contents** → given a fixture session with interleaved
   user/assistant/tool events, selecting session S shows exactly the user
   turns in chronological order, labels `Turn N`, cursor preselected on last.
4. **Fork truncation** → selecting turn K: fake agent's `SetHistory` receives
   `EventsToHistory(events[:idxK])` (assert via message count + last message
   role/content); store writer switched to derived `<src>_fork_*` ID
   (assert `SessionID()` and that source file is untouched).
5. **Replay** → `ReplayAgentEvent` invoked with exactly `events[:idxK]`
   (async — wait on a channel/WaitGroup in the fake replayer); `ClearChat`
   called once.
6. **Cancel paths** → cancelling either picker leaves agent/store untouched
   (no `SetHistory`, no `StartSessionWithID`).
7. **Empty-message session** (no user turns) → flash "No messages to fork
   from", no second selector.
8. **Derived ID validity** → source ID with filesystem-unsafe chars falls
   back to `fork_<unix>_<rand>`; result still passes store validation.

Update/remove: existing `fork` references in `register_test.go` expectations
and any fork command tests asserting node-ID behavior.

## Validation steps

1. `go vet ./core/...`
2. `go test -count=1 -race ./core/commands/ ./core/sessiontree/`
3. `staticcheck ./core/commands/`
4. `gocognit -over 15 core/commands/fork.go` / `gocyclo -over 12 ...` (split
   `Run` into `pickSession`/`pickTurn`/`forkSession` helpers to stay under
   budget).
5. **Interactive check** (bugs.md guideline #5): run `goa`, create a session
   with ≥3 user turns, `/fork`, pick session, pick turn 2, verify chat shows
   history truncated at turn 2, footer/session ID changed, and continuing the
   conversation does not append to the source session file
   (`~/.goa/sessions/<src>.jsonl` mtime unchanged).

## Risks / open questions

- **Replay fidelity**: truncated event streams may end mid-assistant-turn
  (e.g. tool_call without result). `EventsToHistory` flushes partial state
  safely (it tolerates dangling accumulators); display replay of a cut
  mid-tool-call shows the tool widget without a result — acceptable, matches
  what restore of an interrupted session already shows. Cutting at a **user**
  message boundary makes this rare (only when the selected message follows a
  crashed turn).
- **Session ID derivation**: must pass store filename validation; covered by
  test 8.
- **`/clone` command** also references fork-adjacent semantics
  (`clone.go`) — untouched.
- Goal `bright.bison` completion criterion updated for no-backward-compat.

## Out of scope

- Forking from the **live** in-memory session when it has unsaved turns
  (fork sources = persisted store only; the auto-saved current session file
  covers this in practice).
- Cross-session tree visualisation of forks (sessiontree linkage); the fork
  records no parent link beyond the `<src>_fork_` naming convention.
