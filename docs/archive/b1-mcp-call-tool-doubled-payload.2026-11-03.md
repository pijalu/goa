<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# B1 — `mcp` tool `call_tool` returns every payload twice (and `raw` does not help)

Closed 2026-11-03. Archived from `bugs.md`.

## Reported

Calling an MCP tool through the agent's `mcp` tool with `action: call_tool`
returned the result twice: once as a JSON string in `content[0].text` and again,
parsed, in `structuredContent`. Measured on a real call (`rea` /
`procedure_pseudo_code`): ~24 KB of output for a ~12 KB result. `raw: true` did
not change it. `agent-mcp call-tool --format text` additionally carried the full
`analysis_profile` (~2 KB) and `limitations` (~1.5 KB) Evidence envelope on
every call.

## Root cause

Spec-compliant MCP servers (REA's `ToolResultDelivery` sends the
`structured-and-text` representation) repeat the structured payload as
serialized text content for back-compat clients. The agent-mcp client shaper
(`client.Shape`) kept both copies on every path, including `raw`.

## Fix (agent-mcp v0.1.2, commit 51c63ce)

- `client/payload.go`: new `DropMirroredContent` — a call-tool result's
  `content` array is dropped when every text part decodes to the same value as
  `structuredContent`. Applies to the default and the `raw` pass; `raw` keeps
  every structured field and adds no totals. Text that differs (plain prose,
  images, partial mirrors) survives untouched.
- `internal/cli/text.go`: `call-tool --format text` prints the
  `normalized_result` body of an Evidence-shaped result; new global `--verbose`
  flag restores the complete envelope.

## Tests

- `client/payload_dedupe_test.go`: single-copy mapper contract, byte-count
  assertion (~10.5 KB payload must not double), distinct-text and non-JSON
  boundaries, passthrough shapes, raw single-representation, discovery raw
  passthrough.
- `internal/cli/text_internal_test.go`: Evidence body prints, envelope hidden,
  `--verbose` restores envelope.
- goa `tools/mcp_live_test.go` (`TestAgentMCPLiveMirroredPayloadOnce`):
  in-process MCP fixture returns the mirrored shape; the tool output must carry
  the body once for default and raw.

## Validation (real terminal output)

```
$ agent-mcp call-tool --server rea --tool search_strings --args '{"pattern":"DISTORT_BASS"}' --format json
  before: 8060 bytes (content + structuredContent, byte-identical copies)
  after:  3913 bytes, content key absent            # ratio ~2.0x -> ~1.0x
$ agent-mcp call-tool --server rea --tool binary_overview --format text
  prints normalized_result only; analysis_profile/limitations absent
$ agent-mcp call-tool --server rea --tool binary_overview --format text --verbose
  prints the complete envelope (analysis_profile, limitations present)
```

Quality gates: go vet, staticcheck, gocognit, gocyclo clean in both repos;
`go test -count=1 -race` green in both (pre-existing gocognit/gocyclo warnings
in unrelated goa files noted: b53_table_resize_test, provider
manager_auth_startplan_test, core/goal_driver.go).
