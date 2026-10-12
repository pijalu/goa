<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# B18 — image copy/paste in the TUI does nothing on macOS (Cmd+V types a `v`)

Closed 2026-10-10. Archived from `bugs.md`.

## Reported

With an image (a screenshot) on the clipboard, pasting into the TUI failed:

> when there is an image in clipboard — paste (cmd-v), on v is typed — using paste
> menu does not have any impact

i.e. `Cmd+V` inserts a literal `v` into the input line, and the terminal's own
Edit → Paste menu item does nothing. Reported while working on the web UI branch,
from a local TUI in **Ghostty 1.3.1**.

## Measured repro

The B6 check already drove the documented chord through a real PTY and passed, so
the reported chord was driven the same way (`e2e/ptydrive`, a known 8×8 PNG on the
OS clipboard, config pinned to `e2e/mockllm` so no model traffic):

```
$ e2e/clipimg.sh                       # Ctrl+V (0x16)
[PASS] ctrl-v pasted the clipboard image: input line = …/goa/images/goa-image-3189671098.png (8x8)
$ ptydrive … --send-raw $'\x1b[118;9u' # the bytes a Kitty terminal sends for Cmd+V
--- screen tail --- … (lmstudio) mock • medium • [∞]v ─   ← a literal "v", no image path
```

The screen tail is the reported symptom verbatim, and it is reproduced by the two
bytes a Kitty-protocol terminal writes for `Cmd+V`: `ESC [ 118 ; 9 u`.

## Root cause

A terminal only pastes for you when its own chord can carry the clipboard's
*text*. With an image-only clipboard, macOS leaves the terminal's **Paste menu
item disabled**, so the chord is not consumed by the menu and falls through to
the surface — and a terminal speaking the Kitty keyboard protocol reports it to
the application as a key event instead: `ESC [ 118 ; 9 u` (code point `v`,
modifier value `9` = 1 + `super` = 8).

`tui/keys.go:csiModPrefix` named modifier values **2…8** only, so `super` fell
into `default: return ""`: the modifier was silently *discarded* and
`decodeCSIuNumeric` returned the **bare** `"v"`, which the editor treated as text
and inserted. Three defects sat in that one table:

1. the `super` bit (8) had no name — this bug;
2. the lock bits (`caps_lock` 64, `num_lock` 128) were not stripped, so `Cmd+V`
   with Caps Lock on (`ESC [ 118 ; 73 u`) degraded to `"v"` as well;
3. an unnameable modifier leaked the bare key on the standard CSI path too
   (`ESC [ 1 ; 257 A` → `up`), and a *modified* key name is exactly the shape the
   editor treats as text.

Even once decoded correctly, `Cmd+V` could not paste: `KbPaste`
(`tui/keybindings.go`) was bound to `ctrl+v` / `ctrl+shift+v` only.

A fourth, related defect was found while fixing it: `ESC [ 86 ; 6 u` — the
shifted code point Kitty reports for `Ctrl+Shift+V` — decoded to
`ctrl+shift+V`, so the *documented* `ctrl+shift+v` paste alias could never match
in a Kitty terminal.

## Fix (in `tui/`)

* **`keys.go` — the modifier table is now the whole bitmask.** `csiModPrefix`
  returns `(prefix, ok)`: bits are named in binding order (`ctrl+alt+shift+super+
  hyper+meta`), the lock bits are stripped (a lock is not a chord), and `ok=false`
  means "this modifier has no name" — the caller then drops the key instead of
  emitting it bare (`withCSIModifier`, used by every decoder path: CSI-u numeric,
  CSI-u letter/SS3, standard CSI). A shifted letter is normalised to its base
  letter when another modifier is present (`printableKeyName`), which is what
  makes `ctrl+shift+v` reachable again.
* **`keybindings.go` — `KbPaste` carries `super+v`** (`KeySuperV`) next to
  `ctrl+v` / `ctrl+shift+v`, so the forwarded `Cmd+V` resolves the OS clipboard
  with the same precedence (file paths → image → text).
* **`editor_input.go`, `line_editor.go` — a chord name is not text.**
  `isChordName` (modifier-prefixed names only) keeps an *unbound* chord from being
  typed into a buffer: `Cmd+V` inserted `v`, and `Cmd+C`/`Cmd+Q`/`Cmd+T` would
  insert `super+c`/`super+q`/`super+t` the same way. Unmodified names are
  deliberately excluded — `delete`, `insert`, `left` are ordinary words a user can
  paste, and this check must never swallow a clipboard.

## Tests

* `tui/keys_super_modifier_test.go` — decode table for every form above
  (`ESC [ 118 ; 9 u` → `super+v`; caps/num lock reported alongside → `super+v`;
  `ESC [ 118 : 86 ; 9 u` → `super+v`; `ESC [ 86 ; 6 u` → `ctrl+shift+v`; hyper,
  meta, ctrl+alt+shift…), the drop-don't-leak rule for unnameable modifiers, the
  bare-key-is-still-text rule, and `isChordName`.
* `tui/keys_cmd_v_paste_test.go` — `KbPaste` matches `super+v`; the **reported
  case end to end**: the decoded `ESC [ 118 ; 9 u` stores the clipboard image in
  the real image store and leaves the stored path in the input line; Caps-Lock-on
  `Cmd+V` still pastes; an unbound chord is never typed; pasted words
  (`insert`, `f5`) still insert.
* `e2e/clipimg.sh` — now drives **both** chords through a real PTY (`Ctrl+V`
  `0x16` and `Cmd+V` `ESC [ 118 ; 9 u`), and asserts for each that the rendered
  input line holds the image-store path *and nothing else* (the assertion that
  catches the stray `v`), that the stored file is the clipboard image (IHDR
  dimensions re-read), and exits non-zero when either chord fails.
* `docs/HOTKEYS.md` §Editing and `docs/USER-GUIDE.md` §8 document `Cmd+V`, and
  `e2e/README.md` documents the two-chord scenario.
* `tui/ctrl_c_test.go` — two tests that pinned the *mechanism* "the editor types
  the characters of a decoded name" were rewritten to pin the *invariant* they
  were standing in for (the editor neither consumes nor types `Ctrl+C`, which
  belongs to the TUI). Their old assertion — `hello world` → `hello worldctrl+c`
  — is the very behaviour this bug is about.

## Validation — real terminal, real clipboard

```
$ ./e2e/clipimg.sh          # build + real PTY + real OS clipboard
[PASS] ctrl-v pasted the clipboard image: input line = …/goa/images/goa-image-2703976660.png (8x8, file on disk)
[PASS] cmd-v  pasted the clipboard image: input line = …/goa/images/goa-image-1073822165.png (8x8, file on disk)
$ echo $?   → 0
```

Gate: `go vet ./...`, `staticcheck ./tui/...`, `gocognit -over 15 tui/`,
`gocyclo -over 12 tui/` all silent; `go test -count=1 -race ./...` green
(`tui` coverage 76.4 %). No `goa`/`ptydrive`/mock-LLM process or port survived the
runs (`ps`, `lsof -iTCP:8017`).

## Regression evidence (re-measured 2026-10-10)

With the modifier table reverted to the old behaviour (drop the `super` bit, emit
the bare key):

```
--- FAIL: TestEditor_PasteFromClipboard_CmdVStoresImageAndInsertsStoredPath
    keys_cmd_v_paste_test.go:53: inserted "v", want a path inside the image store …
--- FAIL: TestEditor_CmdVWithCapsLockStillPastes
    keys_cmd_v_paste_test.go:78: Cmd+V with Caps Lock on inserted "v", …
--- FAIL: TestDecodeKeys_KittyModifiersAreNamed  (every super/hyper/meta/lock case)
```

and the script:

```
[PASS] ctrl-v pasted the clipboard image: …
[FAIL] cmd-v produced no image path in the input line (ptydrive rc=1)
$ echo $?   → 1
```

Its raw log holds the reported screen tail — `[∞]v ` — so the check fails with
the user's own symptom. Restored: both chords pass and the script exits 0.

## Residual risk

* The byte form driven here is what a Kitty-protocol terminal writes for `Cmd+V`;
  a terminal that consumes the chord itself and pastes nothing (no image, no text)
  cannot be helped by goa — `Ctrl+V` remains the chord that always works.
* `isChordName` trades one rare case for the reported one: pasting the *literal
  text* `super+v` / `ctrl+x` is treated as that chord, exactly as pasting `ctrl+v`
  already was (the binding is matched before text insertion). Unmodified names are
  excluded from the check so ordinary words stay pasteable; an unbound bare name
  (`f5`) is still inserted as text, as it always was — cosmetic, and knowingly left.
* `goa attach` forwards raw bytes, so an image paste in an attached client resolves
  the *server's* clipboard: pasting a local screenshot into a remote server session
  does not bring the image across (the byte protocol has no upload channel). The
  same was true of `Ctrl+V` before this fix.
* Ghostty itself was not driven in-process (no GUI harness here): the fix is pinned
  against the bytes it writes, and the reporter's own screen output is that same
  `v`.
