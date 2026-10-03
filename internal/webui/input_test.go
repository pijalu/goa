// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/tui"
)

// KeyEncoder is the single input-fidelity contract: what the browser sends must
// be what a terminal would have written into its pty. The first table pins the
// byte sequences (spec §7.5 plus every chord tui/keybindings.go binds); the
// tests after it prove the bytes are not just plausible but *understood* by the
// real engine, which is the only way to know the browser really behaves like a
// terminal.

func TestKeyEncoder_SpecTable(t *testing.T) {
	cases := []struct {
		name string
		ev   KeyEvent
		want string
	}{
		// §7.5: unmodified keys.
		{"printable", KeyEvent{Key: "a"}, "a"},
		{"printable upper", KeyEvent{Key: "Z"}, "Z"},
		{"printable non-ascii", KeyEvent{Key: "é"}, "é"},
		{"printable emoji", KeyEvent{Key: "🚀"}, "🚀"},
		{"space", KeyEvent{Key: " "}, " "},
		{"enter", KeyEvent{Key: "Enter"}, "\r"},
		{"backspace", KeyEvent{Key: "Backspace"}, "\x7f"},
		{"tab", KeyEvent{Key: "Tab"}, "\t"},
		{"shift+tab", KeyEvent{Key: "Tab", Shift: true}, "\x1b[Z"},
		{"escape", KeyEvent{Key: "Escape"}, "\x1b"},
		{"up", KeyEvent{Key: "ArrowUp"}, "\x1b[A"},
		{"down", KeyEvent{Key: "ArrowDown"}, "\x1b[B"},
		{"right", KeyEvent{Key: "ArrowRight"}, "\x1b[C"},
		{"left", KeyEvent{Key: "ArrowLeft"}, "\x1b[D"},
		{"home", KeyEvent{Key: "Home"}, "\x1b[H"},
		{"end", KeyEvent{Key: "End"}, "\x1b[F"},
		{"pageup", KeyEvent{Key: "PageUp"}, "\x1b[5~"},
		{"pagedown", KeyEvent{Key: "PageDown"}, "\x1b[6~"},
		{"delete", KeyEvent{Key: "Delete"}, "\x1b[3~"},
		{"insert", KeyEvent{Key: "Insert"}, "\x1b[2~"},
		{"f1", KeyEvent{Key: "F1"}, "\x1bOP"},
		{"f5", KeyEvent{Key: "F5"}, "\x1b[15~"},
		{"f12", KeyEvent{Key: "F12"}, "\x1b[24~"},

		// §7.5: Ctrl chords are control bytes.
		{"ctrl+a", KeyEvent{Key: "a", Ctrl: true}, "\x01"},
		{"ctrl+c", KeyEvent{Key: "c", Ctrl: true}, "\x03"},
		{"ctrl+d", KeyEvent{Key: "d", Ctrl: true}, "\x04"},
		{"ctrl+g", KeyEvent{Key: "g", Ctrl: true}, "\x07"},
		{"ctrl+z", KeyEvent{Key: "z", Ctrl: true}, "\x1a"},
		{"ctrl+space", KeyEvent{Key: " ", Ctrl: true}, "\x00"},

		// §7.5: Alt chords prefix ESC.
		{"alt+x", KeyEvent{Key: "x", Alt: true}, "\x1bx"},
		{"alt+digit", KeyEvent{Key: "1", Alt: true}, "\x1b1"},

		// Modifier parameter = 1 + shift + 2·alt + 4·ctrl.
		{"shift+up", KeyEvent{Key: "ArrowUp", Shift: true}, "\x1b[1;2A"},
		{"alt+left", KeyEvent{Key: "ArrowLeft", Alt: true}, "\x1b[1;3D"},
		{"ctrl+right", KeyEvent{Key: "ArrowRight", Ctrl: true}, "\x1b[1;5C"},
		{"ctrl+shift+m", KeyEvent{Key: "M", Ctrl: true, Shift: true}, "\x1b[109;6u"},
		{"ctrl+home", KeyEvent{Key: "Home", Ctrl: true}, "\x1b[1;5H"},
		{"alt+pageup", KeyEvent{Key: "PageUp", Alt: true}, "\x1b[5;3~"},
		{"ctrl+f5", KeyEvent{Key: "F5", Ctrl: true}, "\x1b[15;5~"},
		{"ctrl+f1", KeyEvent{Key: "F1", Ctrl: true}, "\x1b[1;5P"},

		// Chords with no legacy parameterized form: Kitty CSI-u.
		{"alt+enter", KeyEvent{Key: "Enter", Alt: true}, "\x1b[13;3u"},
		{"ctrl+enter", KeyEvent{Key: "Enter", Ctrl: true}, "\x1b[13;5u"},
		{"shift+enter", KeyEvent{Key: "Enter", Shift: true}, "\x1b[13;2u"},
		{"ctrl+tab", KeyEvent{Key: "Tab", Ctrl: true}, "\x1b[9;5u"},
		{"alt+escape", KeyEvent{Key: "Escape", Alt: true}, "\x1b[27;3u"},

		// Ctrl + punctuation has no legacy form either.
		{"ctrl+- (undo)", KeyEvent{Key: "-", Ctrl: true}, "\x1b[45;5u"},
		{"ctrl+alt+] (jump back)", KeyEvent{Key: "]", Ctrl: true, Alt: true}, "\x1b[93;7u"},
		{"alt+[ (tab prev)", KeyEvent{Key: "[", Alt: true}, "\x1b[91;3u"},

		// Backspace chords use the Kitty CSI-u report, which is what a real
		// terminal sends and what tui/keys.go decodes into "ctrl+backspace"
		// (word delete) and "ctrl+shift+backspace" (delete last message).
		{"alt+backspace", KeyEvent{Key: "Backspace", Alt: true}, "\x1b[127;3u"},
		{"ctrl+backspace", KeyEvent{Key: "Backspace", Ctrl: true}, "\x1b[127;5u"},
		{"ctrl+shift+backspace", KeyEvent{Key: "Backspace", Ctrl: true, Shift: true}, "\x1b[127;6u"},
		{"ctrl+delete", KeyEvent{Key: "Delete", Ctrl: true}, "\x1b[3;5~"},
		{"alt+delete", KeyEvent{Key: "Delete", Alt: true}, "\x1b[3;3~"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(EncodeKey(tc.ev)); got != tc.want {
				t.Fatalf("EncodeKey(%+v) = %q, want %q", tc.ev, got, tc.want)
			}
		})
	}
}

// Keys the page must not swallow: the browser keeps its own chords so reload,
// devtools, tab switching and dead keys keep working.
func TestKeyEncoder_UnhandledKeys(t *testing.T) {
	cases := []struct {
		name string
		ev   KeyEvent
	}{
		{"modifier alone", KeyEvent{Key: "Shift", Shift: true}},
		{"ctrl alone", KeyEvent{Key: "Control", Ctrl: true}},
		{"alt alone", KeyEvent{Key: "Alt", Alt: true}},
		{"meta chord", KeyEvent{Key: "r", Ctrl: true, Meta: true}},
		{"cmd alone", KeyEvent{Key: "Meta", Meta: true}},
		{"f13", KeyEvent{Key: "F13"}},
		{"capslock", KeyEvent{Key: "CapsLock"}},
		{"empty", KeyEvent{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EncodeKey(tc.ev); len(got) != 0 {
				t.Fatalf("EncodeKey(%+v) = %q, want no bytes", tc.ev, got)
			}
		})
	}
}

// A dead key reports no character; KeyboardEvent.code is the only source of the
// character it produces on a US layout.
func TestKeyEncoder_DeadKeyUsesCode(t *testing.T) {
	ev := KeyEvent{Key: "Dead", Code: "KeyA", Alt: true}
	if got, want := string(EncodeKey(ev)), "\x1ba"; got != want {
		t.Fatalf("EncodeKey(dead KeyA) = %q, want %q", got, want)
	}
}

// keyEngine builds a real engine with an editor as its only child, fed through a
// VirtualTerminal — the exact path a browser keystroke takes.
func keyEngine(t *testing.T) (*tui.TUI, *tui.Editor, *VirtualTerminal) {
	t.Helper()
	vt := NewVirtualTerminal(80, 24)
	engine := tui.NewTUI(vt)
	if err := engine.Start(); err != nil {
		t.Fatalf("engine start: %v", err)
	}
	ed := tui.NewEditor()
	engine.AddChild(ed)
	engine.SetFocus(ed)
	return engine, ed, vt
}

// press feeds one browser key event through the encoder and the terminal.
func press(t *testing.T, vt *VirtualTerminal, ev KeyEvent) {
	t.Helper()
	b := EncodeKey(ev)
	if len(b) == 0 {
		t.Fatalf("EncodeKey(%+v) produced no bytes", ev)
	}
	vt.Input(string(b))
}

func typeText(t *testing.T, vt *VirtualTerminal, text string) {
	t.Helper()
	for _, r := range text {
		press(t, vt, KeyEvent{Key: string(r)})
	}
}

// The bytes must be understood by the engine, not merely well-formed: each test
// below asserts the editing operation the browser chord is bound to actually
// happens. Split per chord so a failure names the exact binding that broke.

// typeThenSubmit types text, runs the chord under test, then submits and
// returns what the submit handler saw. Keeping the sequence in one helper means
// every chord test proves the same editor state before it fires.
func typeThenSubmit(t *testing.T, text string, chord KeyEvent) string {
	t.Helper()
	engine, ed, vt := keyEngine(t)
	t.Cleanup(engine.Stop)
	var got string
	ed.SetOnSubmit(func(s string) { got = s })
	typeText(t, vt, text)
	press(t, vt, chord)
	press(t, vt, KeyEvent{Key: "Enter"})
	return got
}

func TestKeyEncoder_CtrlW_KillsPreviousWord(t *testing.T) {
	if got := typeThenSubmit(t, "hello world", KeyEvent{Key: "w", Ctrl: true}); got != "hello" {
		t.Fatalf("after ctrl+w submit = %q, want %q", got, "hello")
	}
}

func TestKeyEncoder_CtrlBackspace_KillsPreviousWord(t *testing.T) {
	if got := typeThenSubmit(t, "hello world", KeyEvent{Key: "Backspace", Ctrl: true}); got != "hello" {
		t.Fatalf("after ctrl+backspace submit = %q, want %q", got, "hello")
	}
}

func TestKeyEncoder_AltBackspace_KillsPreviousWord(t *testing.T) {
	if got := typeThenSubmit(t, "hello world", KeyEvent{Key: "Backspace", Alt: true}); got != "hello" {
		t.Fatalf("after alt+backspace submit = %q, want %q", got, "hello")
	}
}

// Undo pops the whole typing session, so the buffer empties. The point is that
// the chord was recognized at all: an undecodable sequence leaves "abc" intact.
func TestKeyEncoder_CtrlMinus_Undoes(t *testing.T) {
	engine, ed, vt := keyEngine(t)
	t.Cleanup(engine.Stop)
	typeText(t, vt, "abc")
	press(t, vt, KeyEvent{Key: "-", Ctrl: true})
	if got := ed.Text(); got != "" {
		t.Fatalf("after ctrl+- text = %q, want empty", got)
	}
}

func TestKeyEncoder_AltEnter_InsertsNewline(t *testing.T) {
	engine, ed, vt := keyEngine(t)
	t.Cleanup(engine.Stop)
	typeText(t, vt, "a")
	press(t, vt, KeyEvent{Key: "Enter", Alt: true})
	typeText(t, vt, "b")
	if got := ed.Text(); got != "a\nb" {
		t.Fatalf("after alt+enter text = %q, want %q", got, "a\nb")
	}
}

func TestKeyEncoder_CtrlEnter_InsertsNewlineInsteadOfSubmitting(t *testing.T) {
	engine, ed, vt := keyEngine(t)
	t.Cleanup(engine.Stop)
	submitted := false
	ed.SetOnSubmit(func(string) { submitted = true })
	typeText(t, vt, "a")
	press(t, vt, KeyEvent{Key: "Enter", Ctrl: true})
	if submitted {
		t.Fatal("ctrl+enter submitted instead of inserting a newline")
	}
	if got := ed.Text(); got != "a\n" {
		t.Fatalf("after ctrl+enter text = %q, want %q", got, "a\n")
	}
}

func TestKeyEncoder_CtrlA_AndCtrlE_MoveToLineEdges(t *testing.T) {
	engine, ed, vt := keyEngine(t)
	t.Cleanup(engine.Stop)
	typeText(t, vt, "hello")
	press(t, vt, KeyEvent{Key: "a", Ctrl: true})
	press(t, vt, KeyEvent{Key: "X"})
	press(t, vt, KeyEvent{Key: "e", Ctrl: true})
	typeText(t, vt, "!")
	if got := ed.Text(); got != "Xhello!" {
		t.Fatalf("after ctrl+a/ctrl+e text = %q, want %q", got, "Xhello!")
	}
}

func TestKeyEncoder_CtrlShiftBackspace_DeletesLastMessage(t *testing.T) {
	engine, _, vt := keyEngine(t)
	t.Cleanup(engine.Stop)
	deleted := 0
	engine.OnDeleteLast = func() { deleted++ }
	press(t, vt, KeyEvent{Key: "Backspace", Ctrl: true, Shift: true})
	if deleted != 1 {
		t.Fatalf("OnDeleteLast called %d times, want 1", deleted)
	}
}

func TestKeyEncoder_PageKeys_ReachTheEditor(t *testing.T) {
	engine, ed, vt := keyEngine(t)
	t.Cleanup(engine.Stop)
	typeText(t, vt, "line")
	press(t, vt, KeyEvent{Key: "PageUp"})
	if got := ed.Text(); got != "line" {
		t.Fatalf("page up changed the editor: %q", got)
	}
	press(t, vt, KeyEvent{Key: "PageDown"})
}

func TestKeyEncoder_Escape_LeavesTextAlone(t *testing.T) {
	engine, ed, vt := keyEngine(t)
	t.Cleanup(engine.Stop)
	typeText(t, vt, "keep")
	press(t, vt, KeyEvent{Key: "Escape"})
	if got := ed.Text(); got != "keep" {
		t.Fatalf("after escape text = %q, want %q", got, "keep")
	}
}

// A pasted blob arrives as one raw event; the editor must treat it as one paste
// (markers for big payloads), exactly as a bracketed paste from a real terminal
// does after the pty strips the markers.
func TestPaste_IsDeliveredAsOneEvent(t *testing.T) {
	engine, ed, vt := keyEngine(t)
	paste := strings.Repeat("pasted line\n", 3)
	vt.Input(paste)
	if got := ed.Text(); got != paste {
		t.Fatalf("editor text = %q, want %q", got, paste)
	}
	engine.Stop()
}
