// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"
	"time"
)

// A1: measure the per-spinner-tick cost of patching running tool widgets.
// patchRunningToolWidgets runs ~12×/second during tool execution. With the
// bodyVersion memoization (buildBody cached), the per-tick cost should be the
// cheap header rebuild, NOT a re-highlight of the (potentially large) body.
// This benchmark guards against a regression that re-introduces an expensive
// body rebuild on every tick.
func BenchmarkPatchRunningToolWidgets(b *testing.B) {
	cv := NewChatViewport()
	// A running tool with a large output body (the starvation case).
	bigOutput := strings.Repeat("line of tool output with some content\n", 2000)
	for i := 0; i < 4; i++ {
		tc := cv.AddToolExecution("bash", `{"command":"make build"}`)
		tc.SetOutput(bigOutput)
		tc.SetStatus(ToolRunning)
		tc.startTime = time.Now().Add(-5 * time.Second)
	}
	cv.Invalidate()
	_ = cv.Render(120) // prime the frame cache

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		cv.patchRunningToolWidgets(120)
	}
}
