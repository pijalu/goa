// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// B49 production-path validation: the real engine + Editor + FileCompleter
// tree — the wiring internal/app/configureInputEditor builds — running the
// Actor-model loops, driven through Apply-injected keys on the command
// loop. Asserts the @-completion popup appears asynchronously (never
// synchronously on the keystroke), filters as the user types, expands ~,
// decodes %20, and Tab inserts the candidate.

// seedB49Dir writes names (slash-separated, mkdir -p semantics) under dir.
func seedB49Dir(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, n := range names {
		p := filepath.Join(dir, filepath.FromSlash(n))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// b49TypeText injects text through the engine's Apply queue — the
// production input path once RunLoops is active.
func b49TypeText(engine *TUI, ed *Editor, text string) {
	for _, r := range text {
		ch := string(r)
		engine.Apply(func() { ed.HandleInput(ch) })
	}
}

// b49WaitPopup polls AgentFrame until sub appears, returning the dump.
func b49WaitPopup(t *testing.T, engine *TUI, sub string) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		last = engine.AgentFrame().Dump()
		if strings.Contains(last, sub) {
			return last
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("popup never showed %q; last frame:\n%s", sub, last)
	return ""
}

// b49StopEngine stops the loops and waits for them to wind down.
func b49StopEngine(t *testing.T, engine *TUI) {
	t.Helper()
	engine.Stop()
	select {
	case <-engine.Stopped():
	case <-time.After(time.Second):
		t.Fatalf("loops did not stop within 1s")
	}
}

func TestEditor_AutoComp_ProductionPathFilmstrip(t *testing.T) {
	workdir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	seedB49Dir(t, workdir, "README.md", "plans/plan.md", "f g.md")
	seedB49Dir(t, home, "todo.txt")

	term := &fakeTerminal{w: 120, h: 30}
	engine := NewTUI(term)
	ed := NewEditor()
	engine.AddChild(NewChatViewport())
	engine.AddChild(ed)
	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	engine.RunLoops()
	// All Editor state mutations now go through Apply (production Actor model).
	engine.ApplySync(func() {
		ed.SetTUI(engine)
		ed.SetFocused(true)
		ed.SetCompleter(NewFileCompleter(workdir))
		ed.compDebounce = 30 * time.Millisecond
		engine.SetFocus(ed)
	})
	defer b49StopEngine(t, engine)

	// 1. Typing "@" must NOT complete synchronously: the popup only shows
	// up after the debounce, delivered by the async search.
	b49TypeText(engine, ed, "@")
	if dump := engine.AgentFrame().Dump(); strings.Contains(dump, "README.md") {
		t.Errorf("completion applied synchronously on the keystroke:\n%s", dump)
	}
	dump := b49WaitPopup(t, engine, "README.md")
	t.Logf("filmstrip after @ (async popup):\n%s", dump)

	// 2. Drilling: "@p" narrows to plans/ (ranked directory drill-down).
	b49TypeText(engine, ed, "p")
	dump = b49WaitPopup(t, engine, "plans/")
	if strings.Contains(dump, "README.md") {
		t.Errorf("@p popup still shows non-matching README.md:\n%s", dump)
	}
	t.Logf("filmstrip after @p (drill-down):\n%s", dump)

	// 3. Tab inserts the selected drill-down candidate.
	engine.Apply(func() { ed.HandleInput(KeyTab) })
	var buffer string
	engine.ApplySync(func() { buffer = ed.Text() })
	if !strings.HasPrefix(buffer, "@plans/") {
		t.Fatalf("after Tab buffer = %q, want @plans/…", buffer)
	}
	t.Logf("buffer after Tab: %q", buffer)

	// 4. Tilde expansion: "@~" lists $HOME entries with ~ display.
	engine.ApplySync(func() { ed.SetText("") })
	b49TypeText(engine, ed, "@~")
	dump = b49WaitPopup(t, engine, "~/todo.txt")
	t.Logf("filmstrip after @~ (tilde):\n%s", dump)

	// 5. Encoded path: "@f%20g" completes the file named "f g.md".
	engine.ApplySync(func() { ed.SetText("") })
	b49TypeText(engine, ed, "@f%20g")
	dump = b49WaitPopup(t, engine, "f g.md")
	t.Logf("filmstrip after @f%%20g (encoded):\n%s", dump)
}
