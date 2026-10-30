# B36 — Confirmation-gated user image attachments

Closed: 2026-10-30

## Report

Valid image references in a user submission were displayed and sent immediately,
without an opportunity to decline. This affected plain and `@`-prefixed paths and
multiple images. Shell-escaped spaces were validated as literal backslash bytes,
so paths such as
`/Users/muaddib/Desktop/Screenshot\ 2026-10-30\ at\ 15.29.10.png`
were not found. A valid absolute image path at byte zero was also misclassified
as a slash command.

## Resolution

- `splitUserInput` still uses filesystem and content sniffing as the attachment
  authority. It now tries the literal path first and then an ordinary
  shell-backslash-unescaped form. Literal-first validation preserves valid Unix
  filenames containing backslashes. Plain and `@` paths, absolute and relative
  paths, escaped and unescaped spaces, multiple images, and cross-spelling
  deduplication are covered.
- A valid leading absolute image path takes the user-message route. Slash
  commands remain commands when an image appears only in their arguments.
- Valid images produce one batch confirmation card listing every unique path.
  No chat attachment or provider request occurs while the card is pending.
- **Attach** explicitly attaches the whole batch and retains the existing
  stripped-message-plus-image presentation.
- **Send as text without attaching** is the safe default. It sends the original
  submission as text with no images, so declined references are not discarded.
- **Cancel/Escape** sends nothing and restores the original editor text.
- Confirmation continuations return through `App.apply` / the TUI command loop;
  the waiter also exits on engine shutdown.

## Regression and terminal validation

RED was observed before implementation:

- Escaped parser cases returned no images and reported the escaped path as
  unreadable.
- Full-submit cases never showed the confirmation; the leading absolute path
  became `Unknown command`, while other image submissions went straight to the
  provider.

GREEN coverage includes:

- `TestSplitUserInput_AttachesShellEscapedImagePaths`
- `TestSplitUserInput_AttachesShellEscapedRelativeImagePaths`
- `TestSplitUserInput_DedupesEscapedAndLiteralImagePaths`
- Existing B35 whitespace, multiple-image, missing-file, URL, non-image, and
  newline parser tests.
- `TestImageAttachmentConfirmation_AcceptAttachesAllImages` — real production
  component tree, two images, plain/`@` paths, escaped screenshot names, leading
  absolute path, pending-send assertion, filmstrip prompt and accepted frames.
- `TestImageAttachmentConfirmation_DeclineSendsOriginalTextWithoutImages`
- `TestImageAttachmentConfirmation_CancelRestoresInputWithoutSending`
- `TestImageAttachmentConfirmation_SlashCommandImageArgumentStaysCommand`
- `TestWebUI_ImageAttachmentConfirmationAccept`, `...Decline`, and `...Cancel` —
  browser key encoding through a real `webui.VirtualTerminal`, with the prompt
  asserted in the actual cell grid and model submissions inspected at the
  provider boundary.

## Verification run

- `go test -count=1 -timeout 120s ./internal/app` — PASS
- Targeted parser/TUI/WEB tests with `-race` — PASS
- `go vet ./...` — PASS
- `staticcheck ./...` — PASS
- `go test -count=1 -race -cover -timeout 300s ./...` — PASS

Complexity gates contain no new B36 findings. The remaining reports are the
previously documented unrelated hotspots:

- `gocognit -over 15 .`: `TestResolveStartPlanAPIKey` 23,
  `GoalDriver.Drive` 21, `AgentManager.executeRunner` 16, `initGoalSystem` 16.
- `gocyclo -over 12 .`: `TestDeviceMid_PersistsAndStable` 13,
  `GoalDriver.Drive` 13.
