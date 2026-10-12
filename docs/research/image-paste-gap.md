# Goa — Image Post/Paste Gap Analysis

> **Objective:** Validate whether Goa correctly supports posting/pasting images into a
> conversation, compare against three reference terminal agents
> ([opencode](https://github.com/sst/opencode), [pi](https://github.com/badlogic/pi),
> ZCode), enumerate every gap with evidence, and record what was implemented.

**Status:** G1–G7 and G9 implemented, plus the file-manager half of G12. G8, G10
(pruning is opportunistic only) and G11 (de-duplication is per submission only)
remain documented gaps. See §6.

---

## 1. Method

Reference implementations inspected (read-only):

| Repo | Relevant code |
|------|---------------|
| `opencode` | `packages/tui/src/clipboard.ts` (blob platform dispatch), `packages/tui/src/component/prompt/index.tsx:372` (`prompt.paste`), `packages/tui/src/config/keybind.ts:162` (`input_paste: ctrl+v`) |
| `pi` | `packages/coding-agent/src/utils/clipboard-image.ts`, `.../utils/clipboard-command.ts`, `.../core/keybindings.ts:142` (`app.clipboard.pasteImage: ctrl+v`), `.../modes/interactive/components/custom-editor.ts:95`, `.../modes/interactive/interactive-mode.ts:3105` (`handleClipboardPaste`) |
| `ZCode` | Electron desktop app — `packages/ui/src/components/ai-elements/attachments.tsx`, `prompt-input.tsx`, virtual clipboard for embedded browser. Not a terminal paste path; used only as a UI-attachment reference. |

Goa pipeline traced end to end:

```
terminal bytes → StdinBuffer → TUI.handleKey → decodeKeyForRouting → Editor.HandleInput
             → handlePaste / (new) pasteFromClipboard
             → SaveClipboardImage(temp PNG) → insert path into input line
submit → splitUserInput → extractImagePaths / stripImagePaths
       → AgentManager.SendUserInputWithImages → Agent.RunWithImages
       → Message.Images → migrateMessage → ContentBlock{ImageData: <path>}
       → protocol.BuildRequest → per-API converter → base64 on the wire
```

---

## 2. What works today

| Aspect | State |
|--------|-------|
| OpenAI-family APIs (`ApiOpenAICompletions`, Mistral, Bedrock-Converse) | ✅ path → base64 data URL (`protocol/openai_completions_messages.go:240`) |
| Vision gating | ✅ non-vision models get image blocks replaced by a text placeholder (`provider/transform.go`) |
| Web UI paste | ✅ real `/upload` endpoint, magic-byte sniffing, size cap (`internal/webui/upload.go`) |
| Linux clipboard (Wayland `wl-paste`, X11 `xclip`), WSL (`powershell.exe`) | ✅ image bytes readable |
| Text paste (bracketed) + large-paste collapsing markers | ✅ |
| Image files pasted as paths are re-read and encoded per request | ✅ |

---

## 3. Gaps (evidence-based)

Legend: ❌ broken · ⚠️ partial · 🔮 deferred

### G1 ❌ macOS clipboard image read entirely missing — **CRITICAL**

`internal/clipboard_image.go:readClipboardImageBytes` dispatches only to `wl-paste`,
`xclip`, `powershell.exe`. There is no darwin backend and no build tags.

Evidence (this machine, macOS, `osascript` present):

```
$ go test ./internal/ -run TestProbeClipboardImage -v
    readClipboardImageBytes err: no clipboard image tool found
    osascript present
```

Consequence: every image paste on macOS silently yields a text paste only. Both
references solve this with `osascript`:

```applescript
set imageData to the clipboard as "PNGf"   -- opencode clipboard.ts:52
```

### G2 ❌ Images sent as a literal file path for Anthropic / Google (and the legacy
anthropic/google/bedrock adapters) — **CRITICAL**

Only the OpenAI-format converters call `imagePathToDataURL`. The Anthropic and Google
converters pass `b.ImageData` — which holds a **filesystem path** — straight into the
Base64 `data` field, with an empty `media_type`.

Evidence:

```
$ go test ./internal/agentic/provider/protocol/ -run TestProbeImageEncoding -v
  ANTHROPIC body: [{"text":"look","type":"text"},
                   {"source":{"data":"/tmp/foo.png","media_type":"","type":"base64"},"type":"image"}]
  GOOGLE body:    [{"text":"look"},{"inlineData":{"data":"/tmp/foo.png","mimeType":""}}]
```

`ImageMimeType` is never populated anywhere in the codebase (grep: only copied, never
set). So on Anthropic/Google the request is malformed and the image is lost (or the
request is rejected).

Affected call sites: `protocol/anthropic_messages.go:149`, `protocol/google_generative.go:98`,
`anthropic/stream.go:141`, `google/provider.go:105`, `bedrock/provider.go:209`.

### G3 ❌ No explicit image-paste trigger — **HIGH**

There is no `ctrl+v` binding (`tui/keybindings.go`) and no `0x16` entry in `ctrlMap`
(`tui/keys.go:173`). `ReadClipboardImage` is only called from `Editor.handlePaste`,
which is only reached when the pasted *text* is multi-line, tab-containing, >1000 bytes
or >10 newlines (`tui/editor_input.go:35`).

Consequence: a pure image on the clipboard (the common case — Cmd+V after a screenshot,
where the terminal emits no text at all) never triggers a clipboard read. Image paste
appears to work only when the user happens to paste a large multi-line text blob too.

Both references bind an explicit key: pi `app.clipboard.pasteImage` = `ctrl+v`
(`alt+v` on Windows), opencode `input_paste` = `ctrl+v`.

### G4 ❌ Text paste is hijacked by a clipboard image — **HIGH**

`handlePaste` calls `tryPasteImage()` **first and unconditionally** (`tui/editor.go:406`).
If the clipboard holds both text and an image (e.g. copied from a browser), the image
wins and the pasted text is dropped silently.

pi's precedence (`interactive-mode.ts:3105`) is: **file paths → image → text**, and only
on the explicit paste key.

### G5 ❌ `extractImagePaths` destroys user prose — **HIGH**

`extractImagePaths`/`stripImagePaths` are heuristic: any whitespace-separated token whose
lowercased form ends in `.png/.jpg/.jpeg/.webp/.gif` is treated as an attachment and
removed from the message, with no existence check.

Evidence:

```
input="check the logo at docs/logo.png please" -> images=[docs/logo.png] stripped="check the logo at please"
input="see https://example.com/pic.jpg for context" -> images=[https://example.com/pic.jpg] stripped="see for context"
```

So prose is mutated, URLs are mistaken for local files, and the two functions are
separate implementations of the same predicate that can drift.

### G6 ❌ Unreadable / non-existent image silently dropped — **MEDIUM**

`imagePathToDataURL` returns `""` on `os.ReadFile` failure and the block is omitted with
no user-visible trace (`protocol/openai_completions_messages.go:240`). Combined with G5,
the words *and* the image both vanish.

Related: attachments are stored in history as **paths** into `os.TempDir()`. A resumed
session after a reboot/tmp-clean, or a fork, finds the file gone → silent drop. opencode
stores base64 in the message part and is immune; pi has the same path-based flaw.

### G7 ⚠️ No size/dimension normalisation — **MEDIUM**

No downscale or transcode anywhere. A 4K screenshot PNG can exceed provider byte limits
(Anthropic ~5 MB) or the 8000 px dimension ceiling and be rejected. pi ships
`utils/image-resize.ts` / `image-process.ts` for exactly this.

### G8 🔮 Format asymmetry

`isImagePath` accepts `.gif/.webp`, the web UI sniffs GIF/WebP, but
`internal/clipboard_image.go` only registers PNG/JPEG decoders and every clipboard
backend requests PNG. Pasted *files* in GIF/WebP/BMP/TIFF are never validated.
pi converts unsupported formats (incl. Windows DIB/BMP) to PNG via Photon.

### G9 ⚠️ Non-vision model degrades silently — **MEDIUM**

`provider/transform.go` rewrites image blocks to `(image omitted: model does not support
images)`, while the TUI still printed `[attached image: …]`. The user believes the image
was sent.

### G10 🔮 Temp-file leak

Every image paste writes `goa-clipboard-*.png` to `os.TempDir()` and never removes it.

### G11 🔮 No de-duplication

Pasting the same image twice creates two temp files and two identical blocks.

### G12 🔮 No "paste files" from a file manager

pi inserts the original paths when files are copied in Finder
(`readClipboardFilePaths`, `interactive-mode.ts:3107`). Goa has no equivalent — but this is
now partly covered because a pasted image *path* is validated and attached (G5 fix).

---

## 4. Reference behaviour summary

| Concern | opencode | pi | goa (before) | goa (after) |
|---------|----------|----|--------------|-------------|
| Trigger | `ctrl+v` → `prompt.paste` | `ctrl+v` → `app.clipboard.pasteImage` | none (side effect of text paste) | `ctrl+v` → paste precedence |
| Precedence | image → text | file paths → image → text | image → text (hijack) | file paths → image → text |
| macOS image | `osascript` PNGf | native module + `osascript` fallback | ❌ none | `osascript` PNGf |
| Linux image | `wl-paste`, `xclip` | `wl-paste` types, `xclip TARGETS`, native | `wl-paste`, `xclip` | `wl-paste` types, `xclip TARGETS` |
| WSL image | PowerShell → PNG | `wslpath` + PowerShell → PNG | PowerShell → PNG | PowerShell → PNG |
| Wire format | base64 in message part | temp file path → base64 | path (only OpenAI converted) | base64 for **all** APIs, path or `data:` |
| Resize | yes | yes (Photon) | ❌ | ✅ stdlib box-filter downscale |
| Unsupported fmt | convert | convert to PNG | silent | placeholder + warning |
| Attachment storage | base64 in part | temp path | temp path (volatile) | durable cache-dir path + prune |

---

## 5. Root causes

1. **Clipboard reading was written for Linux/WSL only** and never extended to darwin —
   the single most visible failure, and it is environment-dependent so CI stayed green.
2. **Image encoding lives in the wire converters instead of one resolver.** Each protocol
   adapter re-implemented "how do I turn `ImageData` into bytes"; the OpenAI one did it
   correctly, the others never did. `ImageMimeType` was declared but never set, which hid
   the omission.
3. **Attachment detection is a text heuristic, not a validation.** Suffix matching without
   `os.Stat` + magic-byte sniffing makes prose, URLs and typo'd paths indistinguishable
   from real attachments, and the strip step then destroys the text.
4. **No explicit paste affordance.** Treating "clipboard image" as a side effect of
   "clipboard text" inverts the user's intent.

---

## 6. Implemented changes

| Gap | Change |
|-----|--------|
| G1 | `internal/clipboard_image.go` rewritten as a platform dispatcher with per-command timeouts: darwin `osascript` (PNGf → temp file), Linux `wl-paste --list-types` MIME selection and `xclip -t TARGETS`, WSL PowerShell, plus a native-tool fallback. Added `ReadClipboardFilePaths` (darwin AppleScript / Linux `text/uri-list`) and `ReadClipboardText`. |
| G2 | New single resolver `schema.EncodeImage` / `schema.ImageToDataURL` (`internal/agentic/provider/schema/image.go`). Anthropic, Google, Bedrock and both OpenAI converters now encode through it and emit a text placeholder when a source is unreadable. `ImageMimeType` is populated. |
| G3 | `KeyCtrlV` + `ctrlMap[0x16]` (`tui/keys.go`), keybinding `KbPaste` = `input.pasteClipboard` (`tui/keybindings.go`), handled in `Editor.handleControlKeys`. |
| G4 | `Editor.handlePaste` no longer reads the clipboard image. New `Editor.pasteFromClipboard` implements the explicit precedence file paths → image → text. |
| G5 | `extractImagePaths`/`stripImagePaths` replaced by one validation pass inside `splitUserInput`, which requires an existing regular file that sniffs as an image, excludes URLs, and de-duplicates — so prose is preserved. |
| G6 | Unreadable candidates are kept in the message and reported to the user; temp files are stored in a durable cache directory (`internal.ImageStore`) with opportunistic pruning, so resumed sessions still resolve. |
| G7 | `schema.FitImage`: stdlib box-filter downscale + re-encode when an image exceeds 4 MiB decoded or 8000 px before encoding. |
| G9 | `sendToAgentWithImages` warns when images are attached but the active model is not a vision model (`provider.IsVisionModel`). |
| G12 | Clipboard file paths are inserted as text by `pasteFromClipboard` and then validated/attached by the new extraction pass. |

### Deferred

- **G8** — GIF/WebP are passed through without resize or decode validation (would need a
  decoder; stdlib covers PNG/JPEG/GIF only). WebP therefore skips G7 clamping.
- **G10** — pruning is opportunistic (age-based), not exact; no per-session cleanup.
- **G11** — de-duplication is by path within a single submission only, not across turns.

---

## 7. Verification

Every command below was run on the change; the manual clipboard check was run
against a real macOS clipboard (`osascript` round-trip of a PNG, a file URL and
text).

| Check | Command |
|-------|---------|
| macOS / Wayland / X11 clipboard backends | `go test ./internal/ -run 'TestReadDarwinClipboardImage|TestReadWlPasteImage|TestReadXclipImage'` |
| File-list and text clipboard flavours | `go test ./internal/ -run 'TestReadClipboard|TestExistingRegularFiles'` |
| Attachment predicate + durable store + prune | `go test ./internal/ -run 'TestIsImageFile|TestSniffImageExt|TestPreferredImageMime|TestNewImageFile|TestPruneImages'` |
| Image resolver (encode/sniff/fit) | `go test ./internal/agentic/provider/schema/ -run 'TestEncodeImage|TestImageToDataURL|TestFitImage|TestSniffImageMime'` |
| Every wire format embeds Base64 (G2 regression) | `go test ./internal/agentic/provider/protocol/ -run TestImageEncoding` |
| Paste trigger + precedence + text-paste non-hijack | `go test ./tui/ -run 'TestEditor_PasteFromClipboard|TestEditor_TextPaste'` |
| Clipboard bytes → store → stored path (fake runner, any OS) | `go test ./internal/ -run 'TestReadClipboardImage'` |
| Paste key → real store → **input line**, + no-image / no-reader paths | `go test ./tui/ -run 'TestEditor_PasteFromClipboard'` |
| Pasted path lands in the input line (app tail) | `go test ./internal/app/ -run TestPastedImagePathLandsInTheInputLine` |
| Real terminal, real clipboard, rendered input line | `e2e/clipimg.sh` |
| Image copied *in a browser* → PTY hotkey → stored path | `agent-browser open --headed … ; CLIP_KEEP=1 e2e/clipimg.sh` (see bugs.md B6) |
| Attachment extraction keeps prose / rejects URLs | `go test ./internal/app/ -run TestSplitUserInput` |
| Full gate | `go vet ./... && go test -count=1 -race -timeout 900s ./... && gocognit -over 15 <changed dirs> && gocyclo -over 12 <changed dirs>` |

Result: `go vet` clean, `staticcheck` clean, full suite green under `-race`,
complexity within budget on every touched package.

### Known flake (pre-existing, unrelated)

`TestWebSocketTransport_ConcurrentSameSessionSerializes`
(`internal/agentic/provider/transport`) timed out once in a full non-race run. It
passes 7/7 in isolation and under the full `-race` suite, and nothing in this
change touches the transport package — it is a timing-sensitive concurrency test.
