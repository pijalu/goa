// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"fmt"

	"github.com/pijalu/goa/internal/agentic"
)

func formatSummarizeProgress(timings *agentic.TokenTimings) string {
	if timings == nil {
		return "Summarizing context…"
	}
	label := fmt.Sprintf("Summarizing context… ~%d tokens", timings.PredictedN)
	if timings.PredictedPerSecond > 0 {
		label += fmt.Sprintf(" · %.1f tok/s", timings.PredictedPerSecond)
	}
	return fmt.Sprintf("%s · %.1fs", label, timings.PredictedMs/1000)
}

// handleSummarizeProgress updates the global spinner with transient local
// summarize telemetry. The event deliberately bypasses handleTokenStats, so
// auxiliary compaction tokens never enter session totals or the usage store.
func (a *App) handleSummarizeProgress(ev *agentic.OutputEvent) {
	if a.subs == nil || a.subs.statusMsg == nil {
		return
	}
	a.summarizeStatusActive = true
	a.subs.statusMsg.Show(formatSummarizeProgress(ev.Timings))
	if a.subs.tuiEngine != nil {
		a.subs.tuiEngine.RequestRender()
	}
}

// clearSummarizeProgress clears only spinner activity owned by summarize.
// Unrelated status/footer activity and remote/fresh-window transactions are
// left untouched.
func (a *App) clearSummarizeProgress() {
	if !a.summarizeStatusActive {
		return
	}
	a.summarizeStatusActive = false
	if a.subs == nil || a.subs.statusMsg == nil {
		return
	}
	a.subs.statusMsg.Clear()
	if a.subs.tuiEngine != nil {
		a.subs.tuiEngine.RequestRender()
	}
}
