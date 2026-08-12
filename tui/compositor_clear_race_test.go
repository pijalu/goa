// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// slowTerminal serializes every Write byte-by-byte with a small delay so a
// racing Clear() can land between a frame's scroll emission and its window
// repaint — the interleaving a real terminal hits under load.
type slowTerminal struct {
	mu     sync.Mutex
	buf    strings.Builder
	delay  time.Duration
	writes int
}

func (s *slowTerminal) Write(p []byte) (int, error) {
	for _, b := range p {
		s.mu.Lock()
		s.buf.WriteByte(b)
		s.mu.Unlock()
		if s.delay > 0 {
			time.Sleep(s.delay)
		}
	}
	s.mu.Lock()
	s.writes++
	s.mu.Unlock()
	return len(p), nil
}

func (s *slowTerminal) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// unused Terminal methods
func (s *slowTerminal) Start(func(string), func()) {}
func (s *slowTerminal) Stop()                      {}
func (s *slowTerminal) WriteString(str string)     { _, _ = s.Write([]byte(str)) }
func (s *slowTerminal) Size() (int, int)           { return 80, 24 }
func (s *slowTerminal) SetRaw() (func(), error)    { return func() {}, nil }
func (s *slowTerminal) HideCursor()                {}
func (s *slowTerminal) ShowCursor()                {}
func (s *slowTerminal) ClearScreen()               {}
func (s *slowTerminal) SetTitle(string)            {}

func buildScene(w, h, transcriptRows int) *Scene {
	content := make([]string, transcriptRows)
	for i := range content {
		content[i] = strings.Repeat("x", 10) + " transcript row " + strings.Repeat("a", i%3) + string(rune('A'+i%26))
	}
	return &Scene{
		TerminalW:    w,
		TerminalH:    h,
		ChromeHeight: 2,
		Layers: []Layer{
			{Name: "transcript", Kind: LayerBase, Rect: Rect{Y: 0, H: transcriptRows, W: w}, Content: content},
			{Name: "editor", Kind: LayerBase, Rect: Rect{Y: transcriptRows, H: 1, W: w}, Content: []string{"> input"}},
			{Name: "footer", Kind: LayerBase, Rect: Rect{Y: transcriptRows + 1, H: 1, W: w}, Content: []string{"footer"}},
		},
	}
}

// TestCompositor_ClearNeverInterleavesWithFrame: a Clear() racing an in-flight
// Render must not splice the wipe into the middle of a frame's output. Both
// take c.mu, but each Write is a separate lock window — the reported /new bug
// is the wipe landing between a frame's scroll emission and its window
// repaint, after which the next frameFirst skips rows it thinks are already in
// scrollback and the screen goes blank (editor at top). The terminal stream
// must be replayable into a consistent final screen.
func TestCompositor_ClearNeverInterleavesWithFrame(t *testing.T) {
	term := &slowTerminal{delay: 15 * time.Microsecond}
	c := NewCompositor(term)

	big := buildScene(80, 24, 60)
	small := buildScene(80, 24, 3)
	c.Render(big)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 40; i++ {
			c.Render(big)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 40; i++ {
			c.Clear()
		}
	}()
	wg.Wait()

	// Final state: clear then render the fresh session. Whatever interleaving
	// happened, the compositor must still be able to paint the fresh frame.
	c.Clear()
	mark := len(term.String())
	c.Render(small)
	out := term.String()[mark:]
	if !strings.Contains(out, "transcript row") {
		t.Fatalf("after Clear+race the fresh frame wrote no transcript (scrollTop=%d vt=%d regionBot=%d); output:\n%q",
			c.scrollTop, c.vt, c.regionBot, out)
	}
	// The wipe and the repaint must both be present and the wipe must precede
	// the content (a wipe spliced after the repaint would blank the content).
	wipeIdx := strings.LastIndex(out, "\x1b[3J")
	contentIdx := strings.LastIndex(out, "transcript row")
	if wipeIdx > contentIdx {
		t.Errorf("a screen wipe (\\x1b[3J at %d) landed AFTER the fresh content (%d) — blank screen", wipeIdx, contentIdx)
	}
}

// TestCompositor_ClearDoesNotCorruptNextFrame drives a large (scrolling) frame
// on one goroutine while Clear() fires on another, then renders the fresh
// short frame. The pre-fix compositor could interleave the wipe with the
// in-flight frame and corrupt the scrollback watermark, leaving the fresh
// frame's content unwritten (blank screen, editor at top).
func TestCompositor_ClearDoesNotCorruptNextFrame(t *testing.T) {
	term := &slowTerminal{delay: 20 * time.Microsecond}
	c := NewCompositor(term)

	big := buildScene(80, 24, 60)
	small := buildScene(80, 24, 3)

	// Establish a scrolled baseline.
	c.Render(big)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 30; i++ {
			c.Render(big)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 30; i++ {
			c.Clear()
			time.Sleep(50 * time.Microsecond)
		}
	}()
	wg.Wait()

	// After the dust settles, render the fresh (short) session frame and
	// confirm its transcript content actually reaches the terminal.
	c.Clear()
	termStringBefore := len(term.String())
	c.Render(small)
	out := term.String()[termStringBefore:]
	if !strings.Contains(out, "transcript row") {
		t.Fatalf("fresh frame after Clear+race did not write transcript content; compositor state corrupted (scrollTop=%d vt=%d)\nlast output:\n%s",
			c.scrollTop, c.vt, out)
	}
}