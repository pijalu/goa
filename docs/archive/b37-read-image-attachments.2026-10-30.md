# B37 — Explicit Read image attachments

Closed: 2026-10-30

## Report

`read` classified PNG and other image files as binary and returned only metadata.
The model therefore could not view an image even when the active model accepted
image input. Tool results carried only strings, so provider serializers had no
generic attachment path.

The requested option was `include_image`, defaulting to `false`. Explicit image
reads had to support PNG, JPEG, GIF, and WEBP by content signature; preserve
ordinary reads, errors, binary notices, renderers, and deduplication; and never
send image bytes or attachment paths to text-only or unknown-capability models.

## Resolution

- `read` accepts `include_image:true`. Supported images return concise metadata
  and a filesystem-path attachment. Invalid content retains a structured tool
  error; ordinary reads and default binary handling are unchanged.
- The optional `agentic.ResultTool` contract carries generic image attachments
  without changing the base `Tool` interface. Scheduler results, observer events,
  in-memory history, and persisted session events propagate those attachments.
- The active model's vision capability gates attachments. Unsupported and
  unknown models receive `(tool image omitted: model does not support images)`;
  attachment paths and image bytes are removed. History replay applies the same
  downgrade when switching to a text-only model.
- Anthropic Messages, OpenAI Completions, OpenAI Responses, Google Generative AI,
  and legacy Google/Vertex serializers encode attachments in their native
  tool-result envelopes. Text-only results retain their existing string wire
  shapes. Provider hooks preserve image blocks without changing user-image URL
  behavior.
- Read documentation describes the explicit option, supported formats, metadata,
  and capability-gated omission behavior.

## Regression and terminal validation

Coverage includes:

- `TestReadFileSchemaIncludeImageIsExplicitOption`
- `TestReadFileIncludeImageAttachesSupportedFormats`
- `TestReadFileIncludeImageRejectsInvalidContent`
- `TestReadFileImageDefaultKeepsBinaryNoticeAndNoAttachment`
- `TestReadFileIncludeImageMissingPathKeepsReadError`
- `TestReadFileIncludeImageReachesNextVisionModelRequest` — a provider-mocked
  agent turn using the real Read tool; the following vision-model request contains
  the image attachment.
- `TestAgentToolImageAttachmentHonorsActiveModelCapability` — vision, text-only,
  and unknown models, including history and observer propagation.
- `TestToolSchedulerPreservesImageAttachments`
- `TestToolImageAttachmentSurvivesSessionEventRoundTrip`
- `TestSessionStoreToolImageAttachmentRoundTrip` — actual JSONL persistence and
  reload into model history.
- `TestToolResultImageEncodingAllProtocols`
- `TestToolResultImageTextOnlyDowngradeHasNoImagePayload`
- `TestToolResultWithoutImagesKeepsTextWireShape`
- Provider-hook and legacy Google/Vertex supported/unsupported payload tests.
- `TestFilmstripReadToolResultImageMetadata` — production app event handling and
  ANSI-free filmstrips show ordinary Read content unchanged and concise image
  metadata without encoded image bytes in the TUI.

## Verification run

- `go test -count=1 -race -timeout 60s ./internal/app -run '^TestFilmstripReadToolResultImageMetadata$' -v` — PASS
- `go test -count=1 -timeout 120s ./tools ./internal/agentic/...` — PASS
- `go test -count=1 -race -timeout 120s ./internal/app ./core` — PASS
- `go vet ./...` — PASS
- `staticcheck ./...` — PASS
- `go test -count=1 -race -cover ./...` — PASS

Complexity gates contain no new B37 findings. The remaining reports are the
previously documented unrelated hotspots:

- `gocognit -over 15 .`: `TestResolveStartPlanAPIKey` 23,
  `GoalDriver.Drive` 21, `initGoalSystem` 16, `AgentManager.executeRunner` 16.
- `gocyclo -over 12 .`: `TestDeviceMid_PersistsAndStable` 13,
  `GoalDriver.Drive` 13.
