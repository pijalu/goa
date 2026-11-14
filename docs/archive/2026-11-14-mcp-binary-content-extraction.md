<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<!-- Copyright (C) 2026 Pierre Poissinger -->

# Archived: MCP tool ships raw base64 / drops binary content from MCP results

Closed 2026-11-14. Moved from `bugs.md`.

## Observed

`tools/mcp.go` marshals the whole shaped MCP payload into the tool output string. MCP `image`/`audio` content parts and resource `blob` bodies are megabytes of base64: vision models never receive pixels (no `ToolResult.Images`), text models burn the token budget on undecodable base64 that head/tail truncation then cuts. A chrome `take_screenshot` result (0.5–2 MB) is unusable in both cases.

## Expected

Route binary content by MIME at the tool boundary: images decode to temp files attached via `ToolResult.Images` (native image blocks for vision models; `ToolImageOmittedPlaceholder` for text models), every other binary (audio, video, PDF, octet-stream, resource blobs) saved to a temp file and replaced in the JSON output by a short `[binary: <mime> → <path> (<size>)]` marker readable via `read_media`/shell. No base64 crosses the tool boundary.

## Fix

1. `AgentMCPTool` now implements `ContextResultTool` (`ExecuteContextWithResult`) — the framework prefers it (`runTool` checks ContextResultTool first); `Execute` delegates to it. Turn ctx (Stop()/cancellation) finally reaches the tool.
2. New `tools/mcp_binary.go`: `binaryExtractor` recursively walks the shaped result (`map[string]any` / `[]any`), recognizes base64 carriers (`data`+`mimeType` from image/audio content parts, `blob`+`mimeType` from resource contents), decodes (std + raw URL-safe), writes one file per part (canonical extension map, OS mime DB fallback, `.bin` fallback), and rewrites the part in place to a text marker. Undecodable payloads degrade to an error marker, never a failed call.
3. Image paths populate `ToolResult.Images`, feeding the existing vision pipeline (`EncodeImage`/`FitImage`/downgrade) — native image blocks on vision models, `ToolImageOmittedPlaceholder` on text-only models.
4. Artifacts accumulate under one lazily-created temp dir per tool instance (injectable `ExtractDir` for tests); `Close()` removes the whole dir when the tool created it, individual files when injected.
5. Docs: `mcp.long.md` gained "Binary content (screenshots, audio, resource blobs)"; `mcp.short.md` mentions files+markers.

## Regression coverage

- `tools/mcp_binary_test.go`: image part → file with decoded bytes + `Images` + marker (no base64 in output); audio part / resource blob / raw-URL-safe image (table-driven); text+structured result byte-identical; invalid base64 → error marker; `Execute` fallback; `Close` cleanup for injected and owned dirs; `humanSize`.
- `tools/mcp_live_binary_test.go`: end-to-end through the real agent-mcp client library against an in-process MCP server returning `ImageContent` + embedded blob resource — typed MCP result → `Shape` → extractor → file+marker (same path a chrome `take_screenshot` result takes). SDK note: `mcp.ImageContent.Data []byte` / `ResourceContents.Blob []byte` hold raw bytes; the SDK does the base64 on the wire.

## Validation

`go vet ./...`, `staticcheck ./...`, `gocyclo -over 12 .` clean; `gocognit -over 15 .` reports only pre-existing unrelated warnings (tools test func 23, provider/app/core/config tests — noted per guideline); `go test -count=1 -race -cover ./...` all packages pass (tools 84.1%). Companion write-up for the CLI-side half of this problem lives in `~/dev/agent-mcp/bugs.md` entry 2.
