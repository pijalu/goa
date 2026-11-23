// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"fmt"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/tui"
)

const summarizeProgressActivity = "summarizing"

func formatSummarizeProgress(timings *agentic.TokenTimings) string {
	if timings == nil {
		return "Summarizing…"
	}
	label := fmt.Sprintf("Summarizing… ~%d tokens", timings.PredictedN)
	if timings.PredictedPerSecond > 0 {
		label += fmt.Sprintf(" · %.1f tok/s", timings.PredictedPerSecond)
	}
	return fmt.Sprintf("%s · %.1fs", label, timings.PredictedMs/1000)
}

// handleSummarizeProgress updates transient UI state only. The event deliberately
// bypasses handleTokenStats, so auxiliary compaction tokens never enter session
// totals or the persistent usage store.
func (a *App) handleSummarizeProgress(ev *agentic.OutputEvent) {
	subs := a.subs
	label := formatSummarizeProgress(ev.Timings)
	subs.statusMsg.Show(label)
	subs.footer.SetData(tui.FooterData{
		Workdir:                subs.projectDir,
		Model:                  activeModelDisplay(subs),
		Profile:                string(subs.effectiveModeState().Major),
		Mode:                   string(subs.effectiveModeState().Autonomy),
		Activity:               summarizeProgressActivity,
		MainActivity:           label,
		CompanionModel:         companionModelDisplay(subs),
		Provider:               sessionProviderID(subs),
		ThinkingLevel:          mainThinkingLevel(subs),
		CompanionThinkingLevel: companionThinkingLevel(subs),
	})
	subs.footer.SetModelBusy(true)
	if subs.tuiEngine != nil {
		subs.tuiEngine.RequestRender()
	}
}

// clearSummarizeProgress restores idle activity after the durable compaction
// transaction closes. Other activity is left untouched (remote/fresh-window
// transactions never emit local summarize progress).
func (a *App) clearSummarizeProgress() {
	subs := a.subs
	if subs == nil || subs.footer.Data().Activity != summarizeProgressActivity {
		return
	}
	subs.statusMsg.Clear()
	subs.footer.SetData(tui.FooterData{Activity: "", MainActivity: ""})
	subs.footer.SetModelBusy(false)
	if subs.tuiEngine != nil {
		subs.tuiEngine.RequestRender()
	}
}
