// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"

	"github.com/pijalu/goa/internal/ansi"
)

// ExportBlocks implements BlockExporter. It walks the conversation (the
// Model) and describes each entry as a SceneBlock: the entry's own
// width-independent data plus the few view-derived facts a non-terminal
// renderer needs (tool identity/status/duration, collapse state).
//
// It runs on the commandLoop during buildScene — the same single-owner
// discipline as every other component read — so it needs no lock, and its
// cost is one pass over the entries building small structs whose strings are
// shared with the model (nothing is copied except headers).
func (cv *ChatViewport) ExportBlocks() []SceneBlock {
	out := make([]SceneBlock, 0, cv.Len())
	cv.ForEach(func(e MessageEntry) {
		out = append(out, sceneBlockForEntry(e))
	})
	return out
}

// sceneBlockForEntry converts one conversation entry into its block. The
// view contributes kind-specific metadata; the model contributes identity
// and text.
func sceneBlockForEntry(e MessageEntry) SceneBlock {
	b := SceneBlock{
		ID:   e.Data.ID,
		Kind: blockKindFor(e.Data.Type),
		Text: e.Data.Text,
	}
	if e.Data.Type < 0 {
		// Raw components (clarify cards, goal markers) carry no Model
		// text: describe them by their rendered lines, stripped of SGR.
		b.Text = strings.TrimRight(ansi.Strip(strings.Join(e.renderedLines, "\n")), " \n")
	}
	applyViewMeta(&b, e.View)
	mergeModelMeta(&b, e.Data.Meta)
	markCompanion(&b, e.Data.Type)
	return b
}

// applyViewMeta adds the view-derived facts of the entry's widget, when its
// type is one the block plane describes.
func applyViewMeta(b *SceneBlock, v Component) {
	switch view := v.(type) {
	case *ToolExecutionComponent:
		b.Meta = map[string]string{
			"tool":     view.toolName,
			"args":     view.toolArgs,
			"status":   view.status.String(),
			"duration": view.duration,
			"expanded": boolMeta(view.effectiveExpanded()),
		}
		b.Text = view.output
	case *thinkingBlock:
		setMeta(b, "expanded", boolMeta(view.expanded))
		if view.agentLabel != "" {
			setMeta(b, "agent", view.agentLabel)
		}
	}
}

// mergeModelMeta copies the entry's own metadata for keys the view did not
// already set.
func mergeModelMeta(b *SceneBlock, meta map[string]string) {
	for k, v := range meta {
		if _, held := b.Meta[k]; !held {
			setMeta(b, k, v)
		}
	}
}

// markCompanion tags companion output so a client can draw the purple gutter.
func markCompanion(b *SceneBlock, t ConsoleItemType) {
	if t == ConsoleCompanionMessage || t == ConsoleCompanionThinkingBlock {
		setMeta(b, "companion", "1")
	}
}

// setMeta lazily allocates the meta map, then sets one key.
func setMeta(b *SceneBlock, key, value string) {
	if b.Meta == nil {
		b.Meta = map[string]string{}
	}
	b.Meta[key] = value
}

// boolMeta renders a bool as the wire-friendly "1"/"0".
func boolMeta(v bool) string {
	if v {
		return "1"
	}
	return "0"
}
