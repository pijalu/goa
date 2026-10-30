# B35 — Image attachment paths containing whitespace are split and rejected

Date: 2026-10-30
Status: closed

## Report

Submitting an image reference such as:

```text
Image: @/Users/muaddib/Desktop/Screenshot 2026-10-30 at 14.52.06.png
```

did not attach the image. The whitespace-only parser validated each field
independently, retained the `@` marker as part of the path, and reported only
the final image-suffixed fragment as unreadable:

```text
[image not attached — could not read: 14.52.06.png]
```

The complete path, including spaces, must be validated and attached without an
unreadable-image error.

## Reproduction and regression coverage

The new `TestSplitUserInput` cases failed before the fix: both bare and
`@`-prefixed screenshot paths produced no attachments and reported
`14.52.06.png` as unreadable. Coverage now includes:

- bare and `@`-prefixed absolute image paths containing spaces;
- surrounding prose and newlines;
- missing `@`-prefixed paths, which remain in the message and produce one
  full-path diagnostic;
- repeated `@`-prefixed paths, which attach only once;
- the existing prose, URL, non-image, newline, and deduplication cases.

## Fix

`splitUserInput` now retains source offsets for whitespace-delimited fields and
recognizes candidate spans rather than treating every field as an independent
path. Explicit paths (`@`, absolute, `./`, `../`, and `~/` forms) may span fields
through their image suffix, preserving the exact whitespace in the on-disk name.
The optional `@` attachment marker is removed before filesystem validation.

Ordinary prose and bare image filenames retain their previous single-field
behavior. Images still require an existing regular file whose bytes sniff as an
image; URLs and missing files are not stripped from the message.

## Validation

Passed:

- `go test -timeout 60s ./internal/app -run 'TestSplitUserInput' -count=1`
- `go test -count=1 -race -cover -timeout 120s ./internal/app` (66.5% coverage)
- `go test -count=1 -race -cover -timeout 900s ./...`
- `go vet ./...`
- `staticcheck ./...`
- `gocyclo -over 12 ./internal/app`
- `git diff --check`

A real PTY session submitted `Image: @/tmp/goa-e2e/b35/work/Screenshot
2026-10-30 at 14.52.06.png`. The rendered terminal contained:

```text
[attached image: /tmp/goa-e2e/b35/work/Screenshot 2026-10-30 at 14.52.06.png]
```

and did not contain `image not attached`. The PTY-driven Goa process and mock
LLM (pid 56306) were shut down; a post-run process check found no matching test
processes.

Complexity gates report only unrelated pre-existing warnings:

- `gocognit -over 15 .`: `provider.TestResolveStartPlanAPIKey`,
  `core.(*GoalDriver).Drive`, `core.(*AgentManager).executeRunner`, and
  `app.initGoalSystem`.
- `gocyclo -over 12 .`: `startplan.TestDeviceMid_PersistsAndStable` and
  `core.(*GoalDriver).Drive`.

No changed function appears in either complexity report.

## Residual risk

Whitespace in a bare relative filename remains ambiguous with ordinary prose;
such files can use the reported `@` form or an explicit `./` path. No quoting or
URL semantics changed.
