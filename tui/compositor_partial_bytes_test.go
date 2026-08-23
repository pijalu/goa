// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"strings"
	"testing"
)

// Partial-line-churn harness: a full window (no scroll, no blank-row clears)
// whose rows carry end-of-line progress counters that tick between frames.
const (
	pbW      = 80
	pbH      = 24
	pbChrome = 2
	pbRows   = pbH - pbChrome // fill the transcript window exactly
)

func pbRow(worker, item int) string {
	return fmt.Sprintf("worker %02d processing item %03d of 500 ............................ done", worker, item)
}

func pbScene(items *[pbRows]int) *Scene {
	rows := make([]string, pbRows)
	for i := range rows {
		rows[i] = pbRow(i, items[i])
	}
	return &Scene{TerminalW: pbW, TerminalH: pbH, ChromeHeight: pbChrome,
		Layers: []Layer{{Name: "c", Kind: LayerBase, Rect: Rect{X: 0, Y: 0, W: pbW, H: len(rows)}, Content: rows}}}
}

// TestCompositor_PartialRowByteReduction is the mandated measurement leg: a
// partial-line-churn frame must emit >=20% fewer bytes than the full-row
// baseline emitter would for the same changed rows.
func TestCompositor_PartialRowByteReduction(t *testing.T) {
	var itemsA, itemsB [pbRows]int
	for i := range itemsA {
		itemsA[i] = 100
		itemsB[i] = 100
	}
	itemsB[3] = 101
	itemsB[9] = 250
	itemsB[17] = 499
	screenRows := []int{4, 10, 18} // canvas idx 3/9/17 at vt=0, +1 for CUP

	term := &fakeTerminal{w: pbW, h: pbH}
	comp := NewCompositor(term)
	comp.Render(pbScene(&itemsA))
	term.writes = nil
	comp.Render(pbScene(&itemsB))
	diffStr := strings.Join(term.Writes(), "")
	diffBytes := len(diffStr)

	// Envelope: an identical frame emits only the sync wrapper (+cursor seq,
	// which is empty here — no hardware cursor in these scenes).
	term.writes = nil
	comp.Render(pbScene(&itemsB))
	envBytes := len(strings.Join(term.Writes(), ""))

	// Full-row baseline: what the pre-optimization emitter wrote for the same
	// changed rows (CUP + EL2 + truncated line) on top of the same envelope.
	baseline := envBytes
	for _, sr := range screenRows {
		line := pbRow(sr-1, itemsB[sr-1]) + rdR
		baseline += len(fmt.Sprintf("\x1b[%d;1H\x1b[2K", sr)) + len(truncateToWidth(line, pbW, ""))
	}

	// Every changed row must genuinely take the partial path — otherwise a
	// silent fallback could masquerade as a reduction.
	for _, sr := range screenRows {
		if !strings.Contains(diffStr, "\x1b["+itoaStr(sr)+";") {
			t.Fatalf("changed screen row %d was not repainted:\n%s", sr, diffStr)
		}
		if strings.Contains(diffStr, "\x1b["+itoaStr(sr)+";1H\x1b[2K") {
			t.Fatalf("changed screen row %d took the full-row path:\n%s", sr, diffStr)
		}
	}

	ratio := float64(diffBytes) / float64(baseline)
	t.Logf("partial=%dB baseline=%dB envelope=%dB ratio=%.1f%% (%.1f%% saved)",
		diffBytes, baseline, envBytes, ratio*100, (1-ratio)*100)
	if ratio > 0.80 {
		t.Fatalf("byte reduction below 20%%: partial=%d baseline=%d ratio=%.1f%%",
			diffBytes, baseline, ratio*100)
	}
}

// BenchmarkCompositorPartialRowChurn measures real emitted bytes per frame on
// the churn scenario above.
func BenchmarkCompositorPartialRowChurn(b *testing.B) {
	term := &fakeTerminal{w: pbW, h: pbH}
	comp := NewCompositor(term)
	items := [pbRows]int{}
	for i := range items {
		items[i] = 100
	}
	comp.Render(pbScene(&items))

	total := 0
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		items[i%pbRows] = 100 + i%400
		term.writes = nil
		comp.Render(pbScene(&items))
		total += len(strings.Join(term.Writes(), ""))
	}
	b.StopTimer()
	b.ReportMetric(float64(total)/float64(b.N), "bytes/frame")
}

// BenchmarkCompositorFullRowBaseline reports the analytic per-frame byte cost
// of the legacy full-row emitter for the identical churn pattern.
func BenchmarkCompositorFullRowBaseline(b *testing.B) {
	items := [pbRows]int{}
	for i := range items {
		items[i] = 100
	}
	base := 0
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		items[i%pbRows] = 100 + i%400
		sr := i%pbRows + 1
		line := pbRow(sr-1, items[sr-1]) + rdR
		base += len("\x1b[?2026h\x1b[?2026l") +
			len(fmt.Sprintf("\x1b[%d;1H\x1b[2K", sr)) +
			len(truncateToWidth(line, pbW, ""))
	}
	b.StopTimer()
	b.ReportMetric(float64(base)/float64(b.N), "bytes/frame")
}
