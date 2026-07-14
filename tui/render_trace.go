// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// renderTracer writes one JSONL record per Compositor frame to a debug file.
// It captures the Compositor's INTENT per frame (which path it took, the
// viewport anchors, the exact screen rows it painted, and the Scene's layer
// layout) so that rendering bugs — which live in the bytes emitted, not in the
// Scene the filmstrip sees — become fully diagnosable offline when paired with
// a LogTerminal byte log.
//
// Enable via TUI.SetRenderTrace(path), config Logging.render_trace, the
// --render-log CLI flag, or the GOA_LOGGING_RENDER_TRACE env var.
type renderTracer struct {
	mu    sync.Mutex
	file  *os.File
	enc   *json.Encoder
	frame int64
}

func newRenderTracer(path string) (*renderTracer, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	return &renderTracer{file: f, enc: json.NewEncoder(f)}, nil
}

func (r *renderTracer) close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file != nil {
		_ = r.file.Close()
		r.file = nil
	}
}

// frameTrace is the per-frame record assembled by the Compositor while it holds
// its lock, then emitted once at the end of Render.
type frameTrace struct {
	Frame               int64        `json:"frame"`
	Path                string       `json:"path"` // diff|full|resize|cursor|deleted
	TermW               int          `json:"termW"`
	TermH               int          `json:"termH"`
	CanvasLen           int          `json:"canvasLen"`
	PrevVtop            int          `json:"prevVtop"`
	NewVtop             int          `json:"newVtop"`
	Scroll              int          `json:"scroll"`
	Scrolled            bool         `json:"scrolled"`
	FullViewportRepaint bool         `json:"fullViewportRepaint"`
	FirstChanged        int          `json:"firstChanged"`
	LastChanged         int          `json:"lastChanged"`
	WroteRows           []int        `json:"wroteRows"`
	ClearedRows         []int        `json:"clearedRows"`
	Layers              []layerTrace `json:"layers"`
}

// layerTrace is the Scene-intended layout of one layer (ANSI-free bounds +
// content length), so intent can be diffed against the bytes the Compositor
// actually emitted.
type layerTrace struct {
	Name       string `json:"name"`
	Kind       int    `json:"kind"` // 0=base, 1=overlay
	Z          int    `json:"z"`
	Y          int    `json:"y"`
	H          int    `json:"h"`
	W          int    `json:"w"`
	ContentLen int    `json:"contentLen"`
}

func (r *renderTracer) emit(ft frameTrace) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return
	}
	r.frame++
	ft.Frame = r.frame
	_ = r.enc.Encode(ft)
	_ = r.file.Sync()
}

// sceneLayersTrace snapshots a Scene's layers into trace records.
func sceneLayersTrace(layers []Layer) []layerTrace {
	out := make([]layerTrace, 0, len(layers))
	for _, l := range layers {
		out = append(out, layerTrace{
			Name: l.Name, Kind: int(l.Kind), Z: l.Z,
			Y: l.Rect.Y, H: l.Rect.H, W: l.Rect.W, ContentLen: len(l.Content),
		})
	}
	return out
}
