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

## Compression (closed)
Root-cause: compressionLabel built a rich per-layer preview and collapsed to "off" whenever the preview parts were empty — micro compaction (and any mechanism not in the preview) was invisible, showing "off" while compression ran. Fix: row is now a simple mechanism count — "disabled" (master off), "none active", or "N active" (soft/trigger/hard/on-error/micro). Rich preview removed (too wide for the row). Table test covers all cases incl. micro-only regression; interactive /config PTY check shows "Compression 2 active" with tuned defaults. Gates green (vet/staticcheck/gocognit/gocyclo no new; race tests ok).

## default config (closed)
Adopted maintainer-tuned values into embedded config/configs/default.yaml (scope agreed with owner: execution + tools + context_compression, minus providers/models/API keys): execution.mode yolo, retries 2, loop_warning 10, loop_interrupt 15, max_tool_calls 200, auto_heal_tool_calls false, stream_loop_max_strikes 5, bash.compress_output false; tools.enabled now lean (on: pty_exec/python/webfetch; off: bg_exec/delegate_to/request_review/verify/agent/agent_swarm/goa/lsp/todo_list); context_compression.preserve_recent_turns 0. Personal values (providers, models, modes, dream, memory, completion, teams) NOT adopted. Default tests updated to encode new defaults; docs/CONFIGURATION.md example synced. Smoke: fresh HOME/project PTY run starts in coder|YOLO with 12-tool lean set, no crash. Full go test ./... green.

## Sticky skill (closed)
Fixed + validated: skills.sticky / skills.sticky_off config lists (project-level persist), registry SetStickyOverrides + FrontmatterSticky, /skill:sticky:<name> toggle + (sticky) marker in /skill listing, /config → Skills → Sticky skills toggle submenu, startup banner "⟡ Sticky skills (always-on): …", sticky provider re-bound to live subsystems (stale-registry latent bug fixed). Interactive PTY validation: banner at start, /skill listing marker, toggle off/on round trip, cross-session persistence via .goa/config.yaml. Gates: vet ✓, staticcheck (no new), gocognit/gocyclo (no new), go test -race 82 pkgs ok.

# To fix

## Sticky skill
Sticky skills active are not visible at start nor in config - eg: when starting skill telegram, it should be sticky across all sessions but this should be visible in the config via /config and via /skills - the sticky flag should be changable via /config and via /skills commands

Sticky skills are stored at project level

CLOSED — see Archive.

## default
Review current ~/.goa config as a base for default goa config

CLOSED — see Archive.

## Compression
compression config menu show "off" even if there are some compression enabled - remove the "off" / preview as it's too rich to be shown

CLOSED — see Archive.
