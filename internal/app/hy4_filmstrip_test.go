// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/agentic/provider/protocol"
	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/pijalu/goa/tui"
)

type hy4FilmstripProvider struct {
	api   provider.Api
	body  string
	calls atomic.Int32
}

func (p *hy4FilmstripProvider) API() provider.Api { return p.api }

func (p *hy4FilmstripProvider) Stream(_ provider.Model, _ provider.Context, _ provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	body := p.body
	if p.calls.Add(1) > 1 {
		body = hy4FilmstripSSE("content", "Final clean response.")
	}
	stream := provider.NewAssistantMessageEventStream(128)
	go func() {
		model := schema.Model{ID: "hy4-flash-free", Api: schema.ApiOpenAICompletions, Provider: schema.ProviderOpenCodeGo}
		if err := protocol.ParseResponseForModel(protocol.ForAPI(model.Api), strings.NewReader(body), stream, model, schema.ResolveProfile(model)); err != nil {
			stream.CloseWithError(err)
		}
	}()
	return stream, nil
}

func (p *hy4FilmstripProvider) StreamSimple(m provider.Model, c provider.Context, o provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(m, c, provider.BuildSimpleOptions(m, o))
}

func hy4FilmstripSSE(field, value string) string {
	chunk := map[string]any{"choices": []any{map[string]any{
		"index": 0,
		"delta": map[string]any{field: value},
	}}}
	data, _ := json.Marshal(chunk)
	return "data: " + string(data) + "\n\n"
}

func TestAgentTranscript_Hy4TaggedCallFilmstripAndTerminalStayClean(t *testing.T) {
	const suffix = "6124c78e"
	tagged := "<tool_calls:" + suffix + "><tool_call:" + suffix + ">read" +
		"<arg_key:" + suffix + ">path</arg_key:" + suffix + ">" +
		"<arg_value:" + suffix + ">docs/COMMANDS.md</arg_value:" + suffix + ">" +
		"</tool_call:" + suffix + "></tool_calls:" + suffix + ">"
	prov := &hy4FilmstripProvider{
		api: provider.Api("hy4-filmstrip-test"),
		body: hy4FilmstripSSE("reasoning_content", "Need inspect. "+tagged) +
			hy4FilmstripSSE("content", ".</think:"+suffix+">Tagged call recovered.") +
			"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n",
	}
	provider.RegisterApiProvider(prov)
	events := runHy4FilmstripAgent(t, prov)
	sc := newUIScenario(t, 100, 16)
	for i := range events {
		sc.apply(&events[i])
	}
	assertHy4FilmstripClean(t, sc, suffix)
	terminal := assertHy4TerminalClean(t, sc, suffix)
	t.Logf("Hy4 filmstrip:\n%s\nHy4 terminal visible rows + scrollback:\n%s", sc.filmstrip().Render(), terminal)
}

func runHy4FilmstripAgent(t *testing.T, prov *hy4FilmstripProvider) []agentic.OutputEvent {
	t.Helper()
	tool := &staticTool{name: "read", result: "clean tool result"}
	agent := agentic.NewAgent(agentic.Config{
		Model:        provider.Model{ID: "hy4-filmstrip", Api: prov.API(), Provider: schema.ProviderOpenCodeGo},
		SystemPrompt: "test",
		Tools:        []agentic.Tool{tool},
	})
	collector := &eventCollector{}
	agent.AddObserver(collector)
	if _, err := agent.RunAndCollect(context.Background(), "exercise Hy4"); err != nil {
		t.Fatalf("agent RunAndCollect: %v", err)
	}
	if got := prov.calls.Load(); got != 2 {
		t.Fatalf("provider rounds = %d, want tagged tool round plus final response", got)
	}
	collector.mu.Lock()
	events := append([]agentic.OutputEvent(nil), collector.events...)
	collector.mu.Unlock()
	results := 0
	for _, event := range events {
		if event.Type == agentic.EventToolResult && event.ToolCallID == "hy4-tag-1" {
			results++
			if event.ToolResult != "clean tool result" {
				t.Errorf("recovered tool result = %q", event.ToolResult)
			}
		}
	}
	if results != 1 {
		t.Fatalf("recovered tool results = %d, want exactly one", results)
	}
	return events
}

func assertHy4TerminalClean(t *testing.T, sc *uiScenario, suffix string) string {
	t.Helper()
	// Replay the real app renderer's bytes into a terminal emulator so both
	// native scrollback and the visible grid are inspected, not only the DOM.
	emu := tui.NewTermEmulator(16, 100)
	for _, write := range sc.term.writes {
		emu.Process(write)
	}
	rows := append([]string{}, emu.Scrollback()...)
	for row := 0; row < 16; row++ {
		rows = append(rows, emu.Visible(row))
	}
	terminal := strings.Join(rows, "\n")
	assertNoHy4ControlText(t, "terminal", terminal, suffix)
	for _, want := range []string{"Tagged call recovered.", "Final clean response."} {
		if !strings.Contains(terminal, want) {
			t.Errorf("terminal missing %q:\n%s", want, terminal)
		}
	}
	return terminal
}

func assertHy4FilmstripClean(t *testing.T, sc *uiScenario, suffix string) {
	t.Helper()
	for i, frame := range sc.filmstrip().Frames() {
		visible := strings.Join(frame.Frame.Visible, "\n")
		assertNoHy4ControlText(t, fmt.Sprintf("filmstrip frame %d", i), visible, suffix)
	}
	film := sc.filmstrip().Render()
	assertNoHy4ControlText(t, "filmstrip diffs", film, suffix)
	for _, want := range []string{"Tagged call recovered.", "Final clean response."} {
		if !strings.Contains(film, want) {
			t.Errorf("filmstrip missing %q:\n%s", want, film)
		}
	}
	widgets := toolWidgets(sc)
	if len(widgets) != 1 {
		t.Fatalf("tool widgets = %d, want exactly one recovered read widget", len(widgets))
	}
	if widgets[0].Status() != tui.ToolSuccess {
		t.Fatalf("recovered tool widget status = %v, want done", widgets[0].Status())
	}
}

func assertNoHy4ControlText(t *testing.T, where, text, suffix string) {
	t.Helper()
	for _, marker := range []string{suffix, "think:", "tool_calls:", "tool_call:", "arg_key:", "arg_value:"} {
		if strings.Contains(text, marker) {
			t.Errorf("%s leaked Hy4 control marker %q:\n%s", where, marker, text)
		}
	}
}
