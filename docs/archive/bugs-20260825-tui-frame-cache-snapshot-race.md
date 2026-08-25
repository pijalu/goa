# Bug: TUI frame cache mutated while compositor reads previous scene

**Date:** 2026-08-25 · **Status:** IMPLEMENTED — tested, validated, archived.

## Observed

`go test -count=1 -race ./...` failed in
`TestQuotaPlugin_ExtendedInfoInTUI_Filmstrip` (internal/app) with
`WARNING: DATA RACE`: the command-loop goroutine wrote via
`tui.(*ChatViewport).appendEntry` → `runtime.slicecopy`
(tui/chat_viewport.go appendEntry) while the render-loop goroutine read the
same backing array in `tui.placeLayer` (`l.Content[i]`,
tui/compositor_scene.go).

## Root cause

Zero-copy handoff of mutable state across goroutines. `ChatViewport.Render`
returned `bottomAlign(cv.renderCache.lines)`, which returns the **live cache
slice** once content ≥ allocated height. `buildBaseLayers` published that
slice as `Layer.Content` into the Scene; the render loop composes that scene
asynchronously while the command loop processes the next frame, where
`fullRebuild`/`updateLastEntry` truncate the same slice (`lines[:0]`) and
append into the shared backing array. The channel handoff of `*Scene` orders
the publish but cannot prevent later writes into aliased arrays.

## Fix

`bottomAlign` became `frameSnapshot`: every Render exit now hands out an
independent, bottom-aligned copy of the frame lines, so every published
`Layer.Content` is owned by its scene and immutable afterwards (strings are
immutable; only the line slice needs copying). The internal frame cache still
patches/rebuilds in place — only the publish boundary changed.

Regression tests in `tui/chat_viewport_race_test.go`:

- `TestChatViewport_RenderReturnsOwnedSnapshot` — two consecutive Renders must
  not share a backing array (fails on the old aliasing fast path).
- `TestChatViewport_PublishedFrameSafeDuringRebuild` — mirrors the production
  topology: one goroutine mutates+renders+publishes 300 frames, another reads
  published frames end-to-end like placeLayer; flagged by `-race` pre-fix.

## Validation

- `go test -race -count=5 -run TestChatViewport ./tui/` — ok.
- `go test -race -count=3 -run TestQuotaPlugin_ExtendedInfoInTUI_Filmstrip
  ./internal/app/` — ok.
- Full gates separately: `go vet ./...`, `staticcheck ./...`, `gocognit
  -over 15 .`, `gocyclo -over 12 .` (pre-existing test-file warnings unchanged
  from HEAD, unrelated to this change), `go test -count=1 -race -cover ./...`
  — 87 packages ok, no races.
