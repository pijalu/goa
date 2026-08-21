// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/internal/ansi"
)

type AgentLayer struct {
	Name    string
	Z       int
	Rect    Rect
	Lines   []string // ANSI-stripped content
	Visible bool     // whether any part falls inside the visible viewport
}

// AgentNode represents a single UI element in the agent-accessible DOM. It
// gives a component's screen bounds, type, content, and focus state so tests
// and agents can reason about the TUI without parsing escape sequences.
type AgentNode struct {
	Name     string
	Type     string
	Rect     Rect
	Text     string // ANSI-stripped, newline-separated content
	Focused  bool
	Cursor   *CursorPos // cursor position relative to this node, or nil
	Children []AgentNode
}

// AgentFrame is a structured, protocol-free representation of the current
// screen for AI agent tooling: it lets an agent "see" the TUI without parsing
// escape codes. Computed from the same Scene the Compositor renders, so agent
// and terminal always agree.
type AgentFrame struct {
	Width, Height int
	Cursor        *CursorPos
	Layers        []AgentLayer // in z-order
	Nodes         []AgentNode  // DOM nodes for agentic testing
	Visible       []string     // ANSI-stripped visible viewport, top-to-bottom reading order
}

// AgentFrame produces the plain-text structured view of the Scene.
// viewportH is the terminal height (number of visible rows).
func (s *Scene) AgentFrame(viewportH int) AgentFrame {
	canvas, _ := s.compose(0)
	height := len(canvas)
	vTop := height - viewportH
	if vTop < 0 {
		vTop = 0
	}
	vBottom := vTop + viewportH
	if vBottom > height {
		vBottom = height
	}

	frame := AgentFrame{Width: s.TerminalW, Height: viewportH, Cursor: s.Cursor, Nodes: s.Nodes}
	frame.Nodes = fillNodeText(frame.Nodes, s.Layers)

	// Layers, base then overlays by Z, all ANSI-stripped.
	ordered := make([]Layer, 0, len(s.Layers))
	ordered = append(ordered, s.Layers...)
	sortLayersByZ(ordered)
	for _, l := range ordered {
		al := AgentLayer{Name: l.Name, Z: l.Z, Rect: l.Rect}
		al.Lines = make([]string, len(l.Content))
		for i, line := range l.Content {
			al.Lines[i] = ansi.Strip(line)
		}
		al.Visible = l.Rect.Y < vBottom && l.Rect.Y+l.Rect.H > vTop
		frame.Layers = append(frame.Layers, al)
	}

	// Visible viewport, top-to-bottom.
	for i := vTop; i < vBottom && i < len(canvas); i++ {
		frame.Visible = append(frame.Visible, ansi.Strip(canvas[i]))
	}
	return frame
}

// Compositor owns ALL terminal-protocol concerns: it composes a Scene's
// layers into a virtual canvas, then renders that canvas to the terminal.
//
// # Rendering model
//
// The compositor maintains three exact quantities per frame, and the terminal
// is driven purely as an output device for them:
//
//	V        — the full virtual canvas (transcript + fixed chrome band).
//	scrollTop — the scrollback watermark: rows V[0:scrollTop] have been emitted
//	           into the terminal's scrollback EXACTLY once, in order, and are
//	           never re-emitted. scrollTop is clamped to the chrome band start
//	           so fixed chrome can never scroll off the top.
//	vt       — the viewport top: rows V[vt : vt+height] are the visible window,
//	           drawn each frame with absolute CUP. vt = max(0, len(V)-height).
//
// A frame is therefore one atomic CSI-2026 sync containing:
//  1. the newly scrolled-off rows V[prevScrollTop : scrollTop], each written
//     followed by \n so the terminal pushes them into scrollback in order;
//  2. the visible window, repainted with absolute CUP (skipped for rows whose
//     bytes are unchanged since the previous frame);
//  3. the hardware-cursor restore, folded into the same sync.
//
// There is exactly ONE scroll path — no first-scroll / large-scroll /
// shrink / delete special cases. Because scrollback rows are written
// explicitly and monotonically (scrollTop never decreases except on an
// explicit resize/clear, which resets state), correctness does not depend on
// the terminal's incidental native-scroll side effects.
//
// The diff math is kept cohesive with the render logic itself.

func (f *AgentFrame) FindNode(name string) *AgentNode {
	for i := range f.Nodes {
		if f.Nodes[i].Name == name {
			return &f.Nodes[i]
		}
	}
	return nil
}

// FindNodeByType returns the first node with the given type prefix, or nil.
func (f *AgentFrame) FindNodeByType(typePrefix string) *AgentNode {
	for i := range f.Nodes {
		if strings.Contains(f.Nodes[i].Type, typePrefix) {
			return &f.Nodes[i]
		}
	}
	return nil
}

// FocusedNode returns the first focused node, or nil.
func (f *AgentFrame) FocusedNode() *AgentNode {
	for i := range f.Nodes {
		if f.Nodes[i].Focused {
			return &f.Nodes[i]
		}
	}
	return nil
}

// CursorNode returns the node that contains the absolute cursor, or nil if
// the cursor is hidden or no node overlaps it.
func (f *AgentFrame) CursorNode() *AgentNode {
	if f.Cursor == nil {
		return nil
	}
	for i := range f.Nodes {
		n := &f.Nodes[i]
		if f.Cursor.Row >= n.Rect.Y && f.Cursor.Row < n.Rect.Y+n.Rect.H &&
			f.Cursor.Col >= n.Rect.X && f.Cursor.Col < n.Rect.X+n.Rect.W {
			return n
		}
	}
	return nil
}

// Dump returns a human-readable description of the agentic screen model for
// debugging test failures. It includes the terminal size, cursor, and every
// node with its bounds and content.
func (f AgentFrame) Dump() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("AgentFrame %dx%d\n", f.Width, f.Height))
	if f.Cursor != nil {
		b.WriteString(fmt.Sprintf("cursor: (%d,%d)\n", f.Cursor.Row, f.Cursor.Col))
	} else {
		b.WriteString("cursor: hidden\n")
	}
	for _, n := range f.Nodes {
		focus := ""
		if n.Focused {
			focus = " [focused]"
		}
		b.WriteString(fmt.Sprintf("node %s (%s) rect=%+v%s\n", n.Name, n.Type, n.Rect, focus))
		for _, line := range strings.Split(n.Text, "\n") {
			b.WriteString(fmt.Sprintf("  %q\n", line))
		}
	}
	return b.String()
}

// fillNodeText sets each node's Text by ANSI-stripping its matching layer's
// content. agentNodeFor defers this O(n) Join+Strip so the live render path
// (which never builds an AgentFrame) does not pay it every frame for the chat
// layer; it is paid once here, only when AI tooling requests the DOM.
func fillNodeText(nodes []AgentNode, layers []Layer) []AgentNode {
	if len(nodes) == 0 {
		return nodes
	}
	textByLayer := make(map[string]string, len(layers))
	for _, l := range layers {
		if _, ok := textByLayer[l.Name]; ok {
			continue
		}
		textByLayer[l.Name] = ansi.Strip(strings.Join(l.Content, "\n"))
	}
	for i := range nodes {
		if text, ok := textByLayer[nodes[i].Name]; ok {
			nodes[i].Text = text
		}
	}
	return nodes
}
