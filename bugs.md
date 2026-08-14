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

# Archive

## Sticky skill (closed)
Fixed + validated: skills.sticky / skills.sticky_off config lists (project-level persist), registry SetStickyOverrides + FrontmatterSticky, /skill:sticky:<name> toggle + (sticky) marker in /skill listing, /config → Skills → Sticky skills toggle submenu, startup banner "⟡ Sticky skills (always-on): …", sticky provider re-bound to live subsystems (stale-registry latent bug fixed). Interactive PTY validation: banner at start, /skill listing marker, toggle off/on round trip, cross-session persistence via .goa/config.yaml. Gates: vet ✓, staticcheck (no new), gocognit/gocyclo (no new), go test -race 82 pkgs ok.

# To fix

## Sticky skill
Sticky skills active are not visible at start nor in config - eg: when starting skill telegram, it should be sticky across all sessions but this should be visible in the config via /config and via /skills - the sticky flag should be changable via /config and via /skills commands

Sticky skills are stored at project level

CLOSED — see Archive.

## default
Review current ~/.goa config as a base for default goa config

### Fix plan (in progress)
- Scope decided with owner: FULL adoption of execution + tools + context_compression values from ~/.goa/config.yaml into config/configs/default.yaml, minus providers/models/API keys and null artifacts (dedup: keep struct default).
- Values: execution.mode yolo, retries 2, loop_warning 10, loop_interrupt 15, max_tool_calls 200, auto_heal_tool_calls false, thinking_stall 0/0, stream-loop keys (5/5/10, detection on); tools: bash (compress_output false, max_output_bytes 0, complexity off), terminal.sandbox block (off), smartsearch block (off), python.jail false, enabled: bg_exec/delegate_to/request_review/verify/agent/agent_swarm/goa/lsp/todo_list false, pty_exec true, webfetch/python/true as user; context_compression: preserve_recent_turns 0 (rest equal).
- Test approach: update config default tests asserting changed values; add test enumerating default.yaml execution/tools values against expected constants to prevent drift.
- Validation: gates individually; fresh-config e2e smoke (goa starts, /config shows adopted values).

## Compression
compression config menu show "off" even if there are some compression enabled - remove the "off" / preview as it's too rich to be shown

### Fix plan (in progress)
- Root cause: compressionLabel builds a rich preview (soft N% · trigger N% · hard N% method · on-error X) and collapses to "off" whenever the preview parts are empty — micro compaction and other enabled mechanisms are not counted, so "off" shows while compression is actually enabled.
- Fix: replace root /config Compression row description with a simple count ("N active", N = soft + trigger + hard + on-error + micro mechanisms enabled; "none" when 0). No rich preview, no "off".
- Test approach: table test on compressionLabel: all-off → none; micro-only → 1 active; user config (hard+on-error) → 2 active; update existing label tests.
- Validation: gates individually; interactive /config check of the Compression row.
