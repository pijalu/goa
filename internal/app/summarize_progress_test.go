// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"testing"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/tui"
)

func summarizeProgressEvent() *agentic.OutputEvent {
	return &agentic.OutputEvent{
		Type: agentic.EventCompactionProgress,
		Text: "Summarizing…",
		Timings: &agentic.TokenTimings{
			PredictedN:         42,
			PredictedPerSecond: 25.5,
			PredictedMs:        1600,
		},
		Metadata: map[string]string{"category": "summarize-progress"},
	}
}

func TestHandleAgentOutputEvent_SummarizeProgressShowsGlobalSpinner(t *testing.T) {
	app := New(testSubsystems())
	app.subs.chat = tui.NewChatViewport()
	app.subs.tuiEngine = tui.NewTUI(tui.NewProcessTerminal())
	app.subs.footer.SetData(tui.FooterData{
		Activity:     "working",
		MainActivity: "Existing activity",
	})
	app.tokenPredictedTotal = 7

	app.handleAgentOutputEvent(summarizeProgressEvent())

	want := "Summarizing context… ~42 tokens · 25.5 tok/s · 1.6s"
	if got := app.subs.statusMsg.Text(); got != want {
		t.Errorf("status text = %q, want global summarize spinner %q", got, want)
	}
	if !app.subs.statusMsg.IsVisible() {
		t.Error("summarize progress must make the global spinner visible")
	}
	if got := app.subs.footer.Data().MainActivity; got != "Existing activity" {
		t.Errorf("footer activity = %q, want existing activity unchanged", got)
	}
	if app.subs.footer.Data().ModelBusy {
		t.Error("summarize progress must not change footer model-busy state")
	}
	if got := app.tokenPredictedTotal; got != 7 {
		t.Errorf("session generated-token total = %d, want unchanged 7", got)
	}
	if containsRendered(app.subs.chat, "~42 tokens") {
		t.Error("summarize telemetry belongs in the global spinner, not chat")
	}
}

func TestCompactionEnd_ClearsSummarizeSpinner(t *testing.T) {
	app := New(testSubsystems())
	app.subs.tuiEngine = tui.NewTUI(tui.NewProcessTerminal())
	app.handleAgentOutputEvent(summarizeProgressEvent())
	if !app.subs.statusMsg.IsVisible() {
		t.Fatal("summarize progress did not show the spinner to be cleared")
	}

	app.handleAgentStatsEvent(&agentic.OutputEvent{
		Type:         agentic.EventCompactionEnd,
		CompactionTx: &agentic.CompactionTx{ID: "cx-test"},
	})

	if app.subs.statusMsg.IsVisible() {
		t.Errorf("summarize spinner remained visible after compaction end: %q", app.subs.statusMsg.Text())
	}
}

func TestCompactionEnd_DoesNotClearExistingSummarizeStatus(t *testing.T) {
	app := New(testSubsystems())
	app.subs.tuiEngine = tui.NewTUI(tui.NewProcessTerminal())
	app.subs.statusMsg.Show("Existing status")
	app.subs.footer.SetData(tui.FooterData{
		Activity:     "summarizing",
		MainActivity: "Existing activity",
		ModelBusy:    true,
	})

	app.handleAgentStatsEvent(&agentic.OutputEvent{
		Type:         agentic.EventCompactionEnd,
		CompactionTx: &agentic.CompactionTx{ID: "cx-test"},
	})

	if got := app.subs.statusMsg.Text(); got != "Existing status" {
		t.Errorf("status text = %q after compaction end, want unchanged", got)
	}
	if got := app.subs.footer.Data().MainActivity; got != "Existing activity" {
		t.Errorf("footer activity = %q after compaction end, want unchanged", got)
	}
	if !app.subs.footer.Data().ModelBusy {
		t.Error("compaction end must not change unrelated footer model-busy state")
	}
}
