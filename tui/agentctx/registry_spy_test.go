// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentctx

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/tui"
)

// spyTerminal is a tui.Terminal that records every byte stream written to it.
type spyTerminal struct {
	w, h   int
	writes []string
}

func (s *spyTerminal) Start(func(string), func())      {}
func (s *spyTerminal) Stop()                           {}
func (s *spyTerminal) Write(p []byte) (int, error)     { s.writes = append(s.writes, string(p)); return len(p), nil }
func (s *spyTerminal) WriteString(str string)          { s.writes = append(s.writes, str) }
func (s *spyTerminal) Size() (int, int)                { return s.w, s.h }
func (s *spyTerminal) SetRaw() (func(), error)         { return func() {}, nil }
func (s *spyTerminal) HideCursor()                     {}
func (s *spyTerminal) ShowCursor()                     {}
func (s *spyTerminal) ClearScreen()                    {}
func (s *spyTerminal) SetTitle(string)                 {}

// TestAgentViewRegistry_InactiveViewsArePureData drives a REAL engine on a
// spy terminal: two transcripts are registered, only the active one is
// mounted into the component tree, and the inactive one keeps accumulating
// rows. The spy terminal's byte stream must contain the active view's rows
// and NEVER the inactive view's — inactive views are pure data (R-inactive).
func TestAgentViewRegistry_InactiveViewsArePureData(t *testing.T) {
	term := &spyTerminal{w: 60, h: 12}
	engine := tui.NewTUI(term)
	if err := engine.Start(); err != nil {
		t.Fatalf("engine Start: %v", err)
	}
	defer engine.Stop()

	reg := NewAgentViewRegistry()
	main := NewAgentTranscript(MainAgentID)
	coder := NewAgentTranscript("dlg-coder-03")
	reg.Add(MainAgentID, &AgentView{Transcript: main, Compositor: main.Compositor()})
	reg.Add("dlg-coder-03", &AgentView{Transcript: coder, Compositor: coder.Compositor()})
	main.Mount()

	engine.ApplySync(func() {
		engine.AddChild(main.View())
		// The inactive transcript accumulates rows as pure data.
		main.View().AddAssistantMessage("MAIN-SPY-ROW")
		coder.View().AddAssistantMessage("CODER-SPY-ROW")
	})
	engine.RenderNow()

	all := ansi.Strip(strings.Join(term.writes, ""))
	if !strings.Contains(all, "MAIN-SPY-ROW") {
		t.Error("active view's rows never reached the terminal")
	}
	if strings.Contains(all, "CODER-SPY-ROW") {
		t.Error("inactive view wrote to the terminal — must be pure data")
	}
	if coder.Len() != 1 {
		t.Errorf("inactive transcript Len = %d, want 1 (rows still accumulate)", coder.Len())
	}
	if coder.Mounted() {
		t.Error("inactive transcript must stay unmounted")
	}
}
