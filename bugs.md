# Bug and feature Tracking

## Guideline
1. Create a detailed fix plan for each bug - the plan must contain test approach and validation steps - execute the plan and validate the fix when all elements are in place.
2. Any issues found must be fixed and the fix plan must be updated accordingly.
3. Issues found during testing must be fixed and the fix plan must be updated accordingly.
4. Each bug should be moved to archive when tested and closed as the associated plan.
5. Use interactive shell to validate the output of the tool - you must verify the actual terminal output.
6. Check code quality with each tool run separately (do not chain them with `;` or `&&`):
   - `go vet ./...`
   - `staticcheck ./...`
   - `gocognit -over 15 .`
   - `gocyclo -over 12 .`
   - `go test -count=1 -race -cover ./...`
   Fix any new issues introduced by the change. Pre-existing warnings are acceptable only if they are unrelated to the change and explicitly noted.

At the end of the session - the bug list should be empty and this file should only contain the guidelines for bug reporting.
If new items are added, restart the process.

## Workflow for bugs
1. Reproduce the failure before editing — ideally a command or script that triggers it on demand.
2. State the observed failure exactly (command + output).
3. Localize to the smallest region — ideally the specific lines — before editing. Precise localization is the strongest predictor of a correct fix.
4. Change one hypothesis at a time.
5. Prefer the minimal fix over a broad refactor.
6. Verify against the original failing command before declaring done.
7. Run the code-quality checks from guideline #6 separately and confirm the fix does not introduce new violations.
8. Move the bug list to `docs/archive/bugs.<fixdate>.md` when all items are closed.

---

## Runaway-loop guardrail bricks the session; TUI warning omits the repeated sequence — OPEN

- **Observed** (2026-08-03, TUI session): the runaway-loop guardrail stopped
  the turn and the session could not continue ("session stopped due to a
  runaway loop; please review the conversation and retry"). There was no
  clear warning on the TUI before the stop, and from the transcript the
  alleged loop was not obvious — so it is not possible to tell whether the
  runaway loop was real or a false positive of the detector.
- **Relevant code**: warning/stop messages built in
  `internal/agentic/agent_streaming.go` (`handleStreamLoopStrike` → "stream
  loop detected: the assistant kept repeating the same text after N
  warnings…") and `internal/agentic/agent.go` (`checkLoopStopped` →
  "session stopped due to a runaway loop…", `checkProgressLoop` → "runaway
  loop detected: the assistant repeated the same response N consecutive
  times…"). None of these messages include the repeated sequence itself,
  and the soft-strike warnings are ephemeral hints to the model rather than
  visible TUI warnings.
- **Plan**:
  1. Validate the detection is real: add (or surface) enough evidence in the
     message to confirm a genuine loop — the repeated sequence must be shown
     so the user can judge. Check the detector
     (`checkProgressLoop` / stream-loop strike path) for false positives on
     legitimate near-repetition (e.g. long structured outputs with repeated
     headers).
  2. TUI warning and stop messages must include the repeated sequence so the
     loop is visible, both on the soft warning strikes and on the final
     stop.
  3. Long sequences must be elided as
     `start of repeat...(x chars)...end of repeat` to avoid a multi-line
     dump in the TUI. The elision must kick in as soon as the full sequence
     is longer than its elided form (i.e. only elide when it actually
     shortens the display).
  4. Tests: unit test the elision helper (table-driven: short message kept
     as-is; long message elided; boundary where elided length crosses the
     raw length); agent-level test asserting the warning and terminal error
     strings contain the (elided) repeated sequence.
  5. Validate with guideline 6 checks run separately; verify the rendered
     TUI warning interactively per guideline 5.

