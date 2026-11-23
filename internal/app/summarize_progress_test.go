// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"strings"
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

func TestHandleAgentOutputEvent_SummarizeShowsLiveTelemetry(t *testing.T) {
	app := New(testSubsystems())
	app.subs.tuiEngine = tui.NewTUI(tui.NewProcessTerminal())
	app.tokenPredictedTotal = 7

	app.handleAgentOutputEvent(summarizeProgressEvent())

	want := "Summarizing… ~42 tokens · 25.5 tok/s · 1.6s"
	if got := app.subs.statusMsg.Text(); got != want {
		t.Errorf("status text = %q, want %q", got, want)
	}
	if got := app.subs.footer.Data().MainActivity; got != want {
		t.Errorf("footer activity = %q, want %q", got, want)
	}
	if !app.subs.footer.Data().ModelBusy {
		t.Error("footer model must remain busy while summarize generates")
	}
	if got := app.tokenPredictedTotal; got != 7 {
		t.Errorf("session generated-token total = %d, want unchanged 7", got)
	}
}

func TestCompactionEnd_ClearsSummarizeTelemetry(t *testing.T) {
	app := New(testSubsystems())
	app.subs.tuiEngine = tui.NewTUI(tui.NewProcessTerminal())
	app.handleAgentOutputEvent(summarizeProgressEvent())

	app.handleAgentStatsEvent(&agentic.OutputEvent{
		Type:         agentic.EventCompactionEnd,
		CompactionTx: &agentic.CompactionTx{ID: "cx-test"},
	})

	if app.subs.statusMsg.IsVisible() {
		t.Errorf("summarize status still visible after compaction end: %q", app.subs.statusMsg.Text())
	}
	if got := app.subs.footer.Data().MainActivity; got != "" {
		t.Errorf("footer activity = %q after compaction end, want empty", got)
	}
	if app.subs.footer.Data().ModelBusy {
		t.Error("footer model still busy after compaction end")
	}
}

func TestSummarizeProgress_FilmstripShowsAndClearsTelemetry(t *testing.T) {
	engine, _, _, app, cleanup := newToolAnimationApp(t)
	defer cleanup()

	engine.ApplySync(func() { app.handleAgentOutputEvent(summarizeProgressEvent()) })
	engine.RenderNow()
	live := strings.Join(engine.AgentFrame().Visible, "\n")
	t.Logf("live summarize terminal frame:\n%s", live)
	if !strings.Contains(live, "Summarizing… ~42 tokens · 25.5 tok/s · 1.6s") {
		t.Fatalf("live terminal frame omits summarize telemetry:\n%s", live)
	}

	engine.ApplySync(func() {
		app.handleAgentStatsEvent(&agentic.OutputEvent{
			Type:         agentic.EventCompactionEnd,
			CompactionTx: &agentic.CompactionTx{ID: "cx-filmstrip"},
		})
	})
	engine.RenderNow()
	finished := strings.Join(engine.AgentFrame().Visible, "\n")
	t.Logf("completed summarize terminal frame:\n%s", finished)
	if strings.Contains(finished, "~42 tokens") {
		t.Fatalf("stale summarize telemetry remains after compaction end:\n%s", finished)
	}
}
