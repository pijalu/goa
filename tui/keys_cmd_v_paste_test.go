// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal"
)

// TestKbPaste_BindsCmdV: the paste action must be reachable from Cmd+V, not only
// from Ctrl+V. A terminal only pastes for you when its own chord can carry the
// clipboard's *text*: with an image-only clipboard (a screenshot) macOS disables
// the terminal's Paste menu item, the chord falls through to the application, and
// a terminal speaking the Kitty keyboard protocol reports it as Cmd+V. Without
// the binding that chord is a no-op (or, before the modifier table knew super, a
// literal "v" typed into the input line).
func TestKbPaste_BindsCmdV(t *testing.T) {
	kb := DefaultKeybindingsManager()
	for _, chord := range []string{KeyCtrlV, "ctrl+shift+v", KeySuperV} {
		if !kb.Matches(chord, KbPaste) {
			t.Errorf("KbPaste does not match %q (bindings: %v)", chord, kb.Keys(KbPaste))
		}
	}
	// A chord the paste must not claim: typing a plain "v" stays text.
	if kb.Matches("v", KbPaste) {
		t.Error("KbPaste matches the plain letter \"v\"")
	}
}

// TestEditor_PasteFromClipboard_CmdVStoresImageAndInsertsStoredPath is the
// reported case end to end: the TUI decodes Cmd+V (ESC [ 118 ; 9 u) into the name
// "super+v" and hands it to the focused editor, which must store the clipboard
// image and leave the stored path in the input line — the same chain Ctrl+V runs.
func TestEditor_PasteFromClipboard_CmdVStoresImageAndInsertsStoredPath(t *testing.T) {
	store := isolateImageStore(t)
	payload := clipboardImageBytes(t)
	e := imageOnlyClipboard(t, payload)

	e.SetFocused(true)
	e.HandleInput(decodeOneKey(t, "\x1b[118;9u"))

	got := e.Text()
	if got == "" {
		t.Fatal("Cmd+V on a clipboard image inserted nothing")
	}
	if filepath.Dir(got) != store {
		t.Errorf("inserted %q, want a path inside the image store %q", got, store)
	}
	if !internal.IsImageFile(got) {
		t.Errorf("IsImageFile(%q) = false: the inserted path is not an attachment", got)
	}
	stored, err := os.ReadFile(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(stored) != string(payload) {
		t.Error("stored image is not the clipboard payload")
	}
}

// TestEditor_CmdVWithCapsLockStillPastes: the terminal reports the lock bits
// alongside the chord (Cmd+V with Caps Lock on is ESC [ 118 ; 73 u). Those bits
// describe a lock, not the chord, so the paste must still fire.
func TestEditor_CmdVWithCapsLockStillPastes(t *testing.T) {
	isolateImageStore(t)
	e := imageOnlyClipboard(t, clipboardImageBytes(t))

	e.SetFocused(true)
	e.HandleInput(decodeOneKey(t, "\x1b[118;73u"))

	if got := e.Text(); got == "" || !strings.HasSuffix(got, ".png") {
		t.Errorf("Cmd+V with Caps Lock on inserted %q, want the stored image path", got)
	}
}

// TestEditor_DecodedChordIsNeverInsertedAsText: a decoded chord name is a key,
// not text. An unbound one must be ignored — never typed into the buffer, which
// is what made Cmd+V insert a literal "v" and what would otherwise insert
// "super+q" for Cmd+Q.
func TestEditor_DecodedChordIsNeverInsertedAsText(t *testing.T) {
	// Clipboard readers stubbed: these chords are unbound, so nothing may read the
	// developer's real clipboard either.
	e := imageOnlyClipboard(t, nil)
	e.SetFocused(true)

	for _, chord := range []string{"super+q", "super+c", "super+up", "ctrl+shift+m", "alt+shift+j"} {
		e.SetText("")
		e.HandleInput(chord)
		if got := e.Text(); got != "" {
			t.Errorf("HandleInput(%q) left %q in the buffer, want nothing", chord, got)
		}
	}
}

// TestEditor_PastedWordThatIsAKeyNameIsStillInserted guards the trade-off the
// chord check makes: an *unmodified* name spells an ordinary word, so keying the
// check on the modifier prefix is what keeps a pasted word out of its way.
//
// Bound names are a different story and a pre-existing one: a word that is
// exactly a binding ("delete", "enter", "left") has always acted as that key —
// the binding is matched before any text insertion. What this test pins is that
// nothing *new* swallows words: an unbound name is inserted as the text it is.
func TestEditor_PastedWordThatIsAKeyNameIsStillInserted(t *testing.T) {
	e := imageOnlyClipboard(t, nil)
	e.SetFocused(true)

	for _, word := range []string{"insert", "f5"} {
		e.SetText("")
		e.HandleInput(word)
		if got := e.Text(); got != word {
			t.Errorf("pasted %q came out as %q, want the text inserted", word, got)
		}
	}
}

// TestEditor_TypedTextIsStillInserted guards the other side of that rule: real
// text — including characters that look like the pieces of a chord — must keep
// being inserted.
func TestEditor_TypedTextIsStillInserted(t *testing.T) {
	for _, text := range []string{"v", "V", "+", "-", "hello", "cmd+v", "a+b"} {
		e := NewEditor()
		e.SetFocused(true)
		e.HandleInput(text)
		if got := e.Text(); got != text {
			t.Errorf("HandleInput(%q) left %q, want %q", text, got, text)
		}
	}
}
