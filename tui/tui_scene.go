// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import "fmt"

func (t *TUI) handleToggleExpand(data string) bool {
	if !matchesKey(data, "ctrl+o") {
		return false
	}
	cv := t.findChatViewport()
	if cv == nil {
		return false
	}
	cv.ToggleAllToolsView()
	t.RequestRender()
	return true
}

// findChatViewport finds the ChatViewport child, if any.
func (t *TUI) findChatViewport() *ChatViewport {
	for _, child := range t.children {
		if cv, ok := child.(*ChatViewport); ok {
			return cv
		}
	}
	return nil
}

// RenderNow synchronously renders one frame and returns the rendered lines.
// Intended for tests; production renders go through the throttled renderLoop.
// RenderNow synchronously renders one frame and returns the composed canvas.
// Intended for tests; production renders go through the throttled renderLoop.
// The snapshot is taken on the command loop (ApplySync) so it cannot
// interleave with overlay mutations queued from other goroutines — the
// selector apply-callback path adds overlays via engine.Apply.
func (t *TUI) RenderNow() []string {
	var lines []string
	t.ApplySync(func() { lines = t.renderNow() })
	return lines
}

// SendKey injects a decoded key into the TUI, routes it to the focused
// component, and synchronously renders one frame. This is the primary API for
// agentic tests that drive the UI without a real terminal.
func (t *TUI) SendKey(key string) {
	t.handleKey(key)
	t.RenderNow()
}

// renderNow assembles a protocol-free Scene (layers + cursor + DOM nodes) from the
// component tree and overlays, then hands it to the Compositor which owns all
// terminal-protocol output. The TUI never
// emits escape sequences or touches the diff baseline.
func (t *TUI) renderNow() []string {
	if !t.started.Load() || t.stopped.Load() {
		return nil
	}

	scene := t.buildSnapshot()
	t.compositor.Render(scene)
	t.publishScrollWatermark()
	return t.compositor.Buffer()
}

// buildScene renders every child component into a stacked base Layer and every
// overlay into a positioned overlay Layer, producing the protocol-free Scene
// consumed by both the Compositor and the AgentView. Layer Rect.Y accumulates
// for base layers; overlays are positioned relative to the visible viewport.
// Components that expose a viewport height or total height are culled so the
// compositor only sees the visible tail, while the absolute Y accounting
// preserves the full virtual buffer height for correct scrolling.
// The focused editor's CURSOR_MARKER is extracted into Scene.Cursor (explicit,
// grapheme-aware) and stripped from layer content.
func (t *TUI) buildScene(w, h int) *Scene {
	scene := &Scene{TerminalW: w, TerminalH: h}
	rendered, _ := t.renderChildren(w, h)
	scene.Layers, scene.Nodes = t.buildBaseLayers(rendered, w, h)
	scene.ChromeHeight = t.bottomChromeHeight(rendered)
	scene.OverlayCapturesInput = t.buildOverlayLayers(scene, w, h)
	extractCursorMarker(scene)
	return scene
}

// publishScrollWatermark pushes the compositor's scrollback watermark (the
// count of canvas rows committed to terminal scrollback) to the chat
// viewport, whose IsScrolledOff uses it as the ground truth for "this
// entry's rows can never be repainted". It must run after EVERY
// compositor.Render — the only place the watermark moves — so the viewport
// always observes the watermark of the last committed frame.
func (t *TUI) publishScrollWatermark() {
	cv := t.findChatViewport()
	if cv == nil {
		return
	}
	cv.SetScrollWatermark(t.compositor.ScrollWatermark())
}

// bottomChromeHeight returns the total rendered height of the fixed chrome
// stacked BELOW the scrollable transcript (the HeightAllocated child): status
// bar, goal/steering bubbles, bg panel, input editor, footer. These rows must
// never enter terminal scrollback when the transcript scrolls. Children above
// the transcript (the header) scroll with it and are not counted.
func (t *TUI) bottomChromeHeight(rendered [][]string) int {
	transcriptIdx := t.transcriptChildIndex()
	if transcriptIdx < 0 {
		return 0
	}
	chrome := 0
	for i := transcriptIdx + 1; i < len(t.children); i++ {
		chrome += len(rendered[i])
	}
	return chrome
}

// transcriptChildIndex returns the index of the scrollable fill child (the
// conversation viewport), or -1 when none is present. It is the single
// HeightAllocated component; everything after it is pinned bottom chrome.
func (t *TUI) transcriptChildIndex() int {
	for i, child := range t.children {
		if _, ok := child.(HeightAllocated); ok {
			return i
		}
	}
	return -1
}

// renderChildren renders all base children, setting viewport/allocated heights
// first and returning the per-child rendered lines plus the total chrome height.
func (t *TUI) renderChildren(w, h int) ([][]string, int) {
	rendered := make([][]string, len(t.children))
	chromeHeight := 0
	var fills []int

	for i, child := range t.children {
		if _, ok := child.(HeightAllocated); ok {
			fills = append(fills, i)
			continue
		}
		t.setViewportHeight(child, h)
		rendered[i] = child.Render(w)
		chromeHeight += len(rendered[i])
	}
	if len(fills) > 0 {
		budget := (h - chromeHeight) / len(fills)
		if budget < 0 {
			budget = 0
		}
		for _, idx := range fills {
			t.children[idx].(HeightAllocated).SetAllocatedHeight(budget)
			t.setViewportHeight(t.children[idx], h)
			rendered[idx] = t.children[idx].Render(w)
			chromeHeight += len(rendered[idx])
		}
	}
	return rendered, chromeHeight
}

func (t *TUI) setViewportHeight(c Component, h int) {
	if vh, ok := c.(interface{ SetViewportHeight(int) }); ok {
		vh.SetViewportHeight(h)
	}
}

func (t *TUI) totalHeight(c Component, renderedLen int) int {
	if hr, ok := c.(interface{ TotalHeight() int }); ok {
		if th := hr.TotalHeight(); th > renderedLen {
			return th
		}
	}
	return renderedLen
}

// buildBaseLayers converts rendered children into base layers and agent nodes.
// It also collects each child's transient popup (PopupRenderer) and emits those
// as LayerOverlay layers so the base canvas height stays constant (see
// PopupRenderer).
func (t *TUI) buildBaseLayers(rendered [][]string, w, h int) ([]Layer, []AgentNode) {
	var layers []Layer
	var nodes []AgentNode
	var seeds []popupSeed
	y := 0
	for i, child := range t.children {
		lines := rendered[i]
		totalH := t.totalHeight(child, len(lines))
		if len(lines) == 0 {
			y += totalH
			continue
		}
		rectY := y
		if totalH > len(lines) {
			rectY = y + totalH - len(lines)
		}
		rect := Rect{X: 0, Y: rectY, W: w, H: len(lines)}
		layers = append(layers, Layer{
			Name:    componentLayerName(child),
			Kind:    LayerBase,
			Rect:    rect,
			Content: lines,
		})
		// Publish the transcript's canvas origin so ChatViewport.IsScrolledOff
		// can map entry lineOffsets to canvas rows comparable to the
		// compositor's scrollback watermark. The origin skips the rows stacked
		// above the transcript (header) and, when the content fits the
		// allocated budget, the blank bottom-align padding the viewport
		// prepends (renderCache rows start after it).
		if cv, ok := child.(*ChatViewport); ok {
			cv.setTranscriptOrigin(rectY + max(0, len(lines)-totalH))
		}
		nodes = append(nodes, agentNodeFor(child, rect, lines))
		if pr, ok := child.(PopupRenderer); ok {
			if pl := pr.PopupLines(w); len(pl) > 0 {
				seeds = append(seeds, popupSeed{lines: pl, rect: rect})
			}
		}
		y += totalH
	}
	popLayers, popNodes := buildPopupOverlays(seeds, w, h, y)
	layers = append(layers, popLayers...)
	nodes = append(nodes, popNodes...)
	return layers, nodes
}

// popupSeed pairs a PopupRenderer's transient lines with the canvas rect of the
// base component that owns them, so buildPopupOverlays can position the popup
// relative to its owner after the stacked base height is known.
type popupSeed struct {
	lines []string
	rect  Rect
}

// buildPopupOverlays turns popup seeds into LayerOverlay layers. Each popup
// floats ABOVE its owning component as a viewport-relative overlay, so the
// base canvas height never changes and opening/closing a popup can never push
// base content into terminal scrollback.
//
// Placement: prefer directly above the owner (the conventional autocomplete
// position, and overflow-safe when the owner is bottom-anchored like the
// editor). If there is not enough room above, fall back to below the owner.
// The result is clamped to the visible viewport so the overlay never extends
// the canvas beyond the terminal height (which would itself trigger a scroll).
func buildPopupOverlays(seeds []popupSeed, w, h, baseHeight int) ([]Layer, []AgentNode) {
	if len(seeds) == 0 {
		return nil, nil
	}
	viewportStart := baseHeight - h
	if viewportStart < 0 {
		viewportStart = 0
	}
	var layers []Layer
	var nodes []AgentNode
	for _, s := range seeds {
		popupH := len(s.lines)
		if popupH <= 0 {
			continue
		}
		lines := s.lines
		if popupH > h {
			lines = append([]string(nil), lines[:h]...)
			popupH = h
		}
		screenTop := s.rect.Y - viewportStart
		if screenTop < 0 {
			screenTop = 0
		}
		// Prefer above the owner; fall back to below if it does not fit.
		y := screenTop - popupH
		if y < 0 {
			y = screenTop + s.rect.H
		}
		// Clamp into the visible viewport so the overlay never grows the canvas
		// past the terminal height.
		if y+popupH > h {
			y = h - popupH
		}
		if y < 0 {
			y = 0
		}
		rect := Rect{X: 0, Y: y, W: w, H: popupH}
		content := append([]string(nil), lines...)
		layers = append(layers, Layer{
			Name:    "popup",
			Kind:    LayerOverlay,
			Z:       1,
			Rect:    rect,
			Content: content,
		})
		nodes = append(nodes, AgentNode{Name: "popup", Type: "*tui.Popup", Rect: rect})
	}
	return layers, nodes
}

// buildOverlayLayers appends overlay layers to the scene and reports whether any
// overlay captures input.
func (t *TUI) buildOverlayLayers(scene *Scene, w, h int) bool {
	captures := false
	t.overlayMu.RLock()
	overlays := append([]*overlayEntry(nil), t.overlayStack...)
	t.overlayMu.RUnlock()
	for _, ov := range overlays {
		olines := ov.comp.Render(w)
		if len(olines) == 0 {
			continue
		}
		if ov.opts.CaptureInput {
			captures = true
		}
		oh := clampOverlayHeight(len(olines), h)
		startRow := overlayStartRow(ov.opts, oh, h)
		rect := Rect{X: 0, Y: startRow, W: w, H: oh}
		scene.Layers = append(scene.Layers, Layer{
			Name:    componentLayerName(ov.comp),
			Kind:    LayerOverlay,
			Z:       1 + len(scene.Layers),
			Rect:    rect,
			Content: append([]string(nil), olines[:oh]...),
		})
		scene.Nodes = append(scene.Nodes, agentNodeFor(ov.comp, rect, olines[:oh]))
	}
	return captures
}

// agentNodeFor builds a lightweight AgentNode (Name, Type, Rect, Focused)
// from a component and its rendered layer. It intentionally does NOT compute
// the node's Text (an O(n) ansi.Strip+Join over the layer's lines): that text
// is only consumed by AI tooling via AgentFrame, never by the live render
// path, so it is filled lazily in Scene.AgentFrame to avoid an O(history)
// string allocation every streaming frame for the chat layer.
func agentNodeFor(c Component, rect Rect, lines []string) AgentNode {
	node := AgentNode{
		Name: componentLayerName(c),
		Type: fmt.Sprintf("%T", c),
		Rect: rect,
	}
	if f, ok := c.(Focusable); ok {
		node.Focused = f.Focused()
	}
	return node
}

// extractCursorMarker scans layers (topmost overlay first, then base layers)
// for the CURSOR_MARKER emitted by the focused input, sets Scene.Cursor to
// its absolute (row, col) position, and strips the marker. col is
// grapheme-aware (matches the terminal).
//
// When a capturing overlay is open, the base editor's cursor must NOT be used:
// the overlay owns input and a non-cursor overlay (like the tab picker) should
// leave the hardware cursor hidden.
