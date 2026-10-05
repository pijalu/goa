<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# B14 — A clipboard offering only WebP pasted nothing, although the upload path stores WebP

Closed 2026-10-05. Archived from `bugs.md`.

## Reported

`internal.SniffImageExt` accepts `.webp` (`internal/imagestore.go`), and
`internal/webui/upload.go` stored an upload's *raw bytes* under that extension —
but the terminal clipboard path decoded the image and re-encoded it to PNG
(`SaveClipboardImage` → `png.Encode`) with only GIF and JPEG decoders registered,
so a clipboard whose only image flavour is `image/webp` failed `image.Decode`
with "unknown format" and the paste was dropped silently (`tryPasteImage`
swallowed the error). `image/bmp` was reachable the same way through
`preferredImageMime`'s any-`image/*` fallback.

B6 had claimed "web/image parity is structural"; it was not structural in the
store: the upload path kept bytes, the clipboard path re-encoded them. pi keeps
bytes plus MIME and converts only the formats it must.

## Root cause

The clipboard path modelled an image as a decoded `image.Image` while every other
path in the codebase (upload, attachment sniff, provider encode) models it as
bytes plus a sniffed format. The decode step therefore added a format
requirement — a decoder for WebP — that nothing else in the pipeline had, and the
requirement was never tested because no test drove a WebP clipboard.

## Fix

* **One store writer.** `internal.StoreImage(head, body, maxBytes)` is now the
  single writer for images arriving from outside: it sniffs the format with
  `internal.SniffImageExt`, creates the file with that extension, enforces the
  caller's byte cap, and leaves nothing behind on refusal. Both the web upload
  (`saveUploadedImage`) and the clipboard (`internal.SaveClipboardImageBytes`) go
  through it, so they share one format table, one size rule and one store and
  cannot drift apart again.
* **Bytes all the way.** `ReadClipboardImageBytes` returns the clipboard's bytes
  after a sniff check, and `SaveClipboardImageBytes` stores them verbatim; the
  `image.Image` seam disappeared from the editor
  (`Editor.readClipboardImage func() ([]byte, bool)`,
  `saveClipboardImage = internal.SaveClipboardImageBytes`). A paste no longer
  rewrites the user's pixels, and WebP pastes exactly like it uploads.
* **Selection matches the store.** `preferredImageMime` returns only the four
  formats the store can keep (png/jpeg/webp/gif) and no longer falls back to an
  arbitrary `image/*`: a clipboard offering only something else reads as "no
  image" rather than as a paste that silently does nothing.
* **A bug found by the new tests, fixed here.** `StoreImage` initially sniffed
  the head without writing it, truncating every stored image by its first up-to-64
  bytes; the first run of the new chain tests failed on
  `IsImageFile(...) = false` and byte-inequality, and the head is now written
  before the body. The web upload path had the same primitive's shape, so the
  shared writer is what makes that class of bug visible in one place.

## Validation

`internal/clipboard_paste_chain_test.go` (rewritten) covers the whole chain on
this host: clipboard bytes → sniff → store → stored path accepted by
`internal.IsImageFile` (the submit path's own attachment predicate), asserting
byte-identity and extension, plus a WebP variant driven through the Wayland
backend, a refused non-image that leaves no file, and the silent "no image" and
"unknown format" paths. `tui/editor_image_paste_chain_test.go` gained
`TestEditor_PasteFromClipboard_StoresWebP` at the real `Ctrl+V` entry point.

RED, measured with the old decode-and-re-encode store restored:

```
--- FAIL: TestSaveClipboardImageBytes_KeepsWebP
    SaveClipboardImageBytes: image: unknown format
--- FAIL: TestEditor_PasteFromClipboard_StoresWebP
    Ctrl+V on a clipboard WebP inserted nothing
```

The second line is the reported symptom, verbatim. Restored: both pass, and the
real-terminal check `e2e/clipimg.sh` still passes
(`input line = …/goa-image-3987286049.png (8x8, file on disk)`).

## Residual risk

* Formats outside the store's four (BMP, TIFF, …) are not pasted. pi converts
  them with photon (WASM/native); goa deliberately carries no image-conversion
  dependency, and the `preferredImageMime` change makes the outcome explicit —
  "no image" — rather than a paste that inserts nothing visible.
* A clipboard offering WebP now stores a `.webp` attachment; whether the
  provider accepts it is a provider question (the vision encoder sniffs and
  forwards WebP unchanged), not a paste question.
