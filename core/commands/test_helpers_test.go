// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/skills"
	"github.com/pijalu/goa/tui"
)

// testCmdModel returns a minimal valid model for command tests.
func testCmdModel() agenticprovider.Model {
	return agenticprovider.Model{
		ID:         "test-model",
		Name:       "test-model",
		Api:        agenticprovider.ApiOpenAICompletions,
		Provider:   agenticprovider.ProviderCustom,
		InputTypes: []string{"text"},
		BaseURL:    "http://localhost:9999/v1/chat/completions",
	}
}

// fakeOutputWriter collects Writef calls for test inspection.
type fakeOutputWriter struct {
	lines []string
}

func newWriter() *fakeOutputWriter { return &fakeOutputWriter{} }
func (f *fakeOutputWriter) Writef(format string, args ...interface{}) {
	f.lines = append(f.lines, fmt.Sprintf(format, args...))
}
func (f *fakeOutputWriter) Text() string    { return strings.Join(f.lines, "\n") }
func (f *fakeOutputWriter) Lines() []string { return f.lines }

// fakeSessionRecorder implements core.SessionRecorder for testing.
type fakeSessionRecorder struct {
	history []core.TurnRecord
}

func (f *fakeSessionRecorder) TurnHistory() []core.TurnRecord { return f.history }
func (f *fakeSessionRecorder) LastTurn() *core.TurnRecord {
	if len(f.history) == 0 {
		return nil
	}
	return &f.history[len(f.history)-1]
}

// fakeSystemPromptProvider implements core.SystemPromptProvider for testing.
type fakeSystemPromptProvider struct {
	prompt string
}

func (f *fakeSystemPromptProvider) SystemPrompt() string { return f.prompt }

// recordingProviderManager records SetActive calls and mirrors
// testProviderManager behavior for ResolveActiveModel.
type recordingProviderManager struct {
	testProviderManager
	setProvider string
	setModel    string
}

func (p *recordingProviderManager) SetActive(providerID, model string) error {
	p.setProvider = providerID
	p.setModel = model
	p.model = model
	return nil
}

// fakeEventSink implements core.EventSink for testing.
type fakeEventSink struct {
	flashes []string
}

func (f *fakeEventSink) Flash(text string)                   { f.flashes = append(f.flashes, text) }
func (f *fakeEventSink) FooterRefresh()                      {}
func (f *fakeEventSink) StopRequest()                        {}
func (f *fakeEventSink) ClearChat()                          {}
func (f *fakeEventSink) NewSession()                         {}
func (f *fakeEventSink) InterAgent(from, to, content string) {}

// fakeSelector implements core.Selector for testing.
type fakeSelector struct {
	chosen string
	ok     bool
}

func newSelector(chosen string, ok bool) *fakeSelector { return &fakeSelector{chosen, ok} }
func (f *fakeSelector) SelectOption(title string, options []tui.SelectorItem, current string, onSelected func(selected string, ok bool)) {
	if onSelected != nil {
		onSelected(f.chosen, f.ok)
	}
}

// fakeSessionStore implements core.SessionStoreAPI for testing.
type fakeSessionStore struct {
	sessions []core.SessionInfo
	events   map[string][]agentic.OutputEvent
	err      error
}

func newSessionStore(sessions []core.SessionInfo) *fakeSessionStore {
	return &fakeSessionStore{sessions: sessions, events: make(map[string][]agentic.OutputEvent)}
}
func (f *fakeSessionStore) ListSessions() ([]core.SessionInfo, error) { return f.sessions, f.err }
func (f *fakeSessionStore) LoadSession(name string) ([]agentic.OutputEvent, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.events[name], nil
}
func (f *fakeSessionStore) SaveCurrent(name string) error               { return f.err }
func (f *fakeSessionStore) DeleteSession(name string) error             { return f.err }
func (f *fakeSessionStore) ImportSession(name, sourcePath string) error { return f.err }
func (f *fakeSessionStore) SessionID() string                           { return "" }
func (f *fakeSessionStore) CurrentSessionPath() string                  { return "" }
func (f *fakeSessionStore) AddEvents(name string, events []agentic.OutputEvent) {
	f.events[name] = events
}

// skillTestContext builds a minimal core.Context for testing skill commands.
// It uses the given output buffer and starts with an empty turn history so
// that inline skills are loaded into the system prompt rather than submitted
// as user messages.
func skillTestContext(buf *strings.Builder) core.Context {
	cfg := &config.Config{}
	ss := core.NewSessionState(internal.ModeState{Major: internal.MajorCoder, Autonomy: internal.AutonomyYolo})
	tuiEvents := event.MakeBus(10, 10, 10, 10)
	return core.Context{
		Config:       cfg,
		OutputBuffer: buf,
		AgentManager: core.NewAgentManager(cfg, nil, nil, ss, tuiEvents, ""),
	}
}

// skillTestContextWithHistory builds a core.Context where the conversation has
// already started, so inline skills are submitted as user messages.
func skillTestContextWithHistory(buf *strings.Builder) core.Context {
	ctx := skillTestContext(buf)
	// Set last user input to signal that the conversation has started.
	if am := ctx.AgentManager; am != nil {
		am.SetLastUserInputForTest("hello")
	}
	return ctx
}

// fakeSkillRegistry implements core.SkillRegistry for testing.
type fakeSkillRegistry struct {
	skills map[string]*skills.Skill
}

func newSkillRegistry(skills map[string]*skills.Skill) *fakeSkillRegistry {
	return &fakeSkillRegistry{skills: skills}
}
func (f *fakeSkillRegistry) Get(name string) (*skills.Skill, bool) {
	s, ok := f.skills[name]
	return s, ok
}
func (f *fakeSkillRegistry) List() []skills.SkillSummary {
	var out []skills.SkillSummary
	for _, s := range f.skills {
		out = append(out, skills.SkillSummary{
			Name:        s.Meta.Name,
			Description: s.Meta.Description,
			Inline:      s.Meta.Inline,
		})
	}
	return out
}
func (f *fakeSkillRegistry) IsInline(name string) bool {
	s, ok := f.skills[name]
	return ok && s.Meta.Inline
}

// fakeDocsProvider implements core.DocsProvider for testing.
type fakeDocsProvider struct {
	list    []core.DocInfo
	findErr error
	getErr  error
}

func (f *fakeDocsProvider) List() ([]core.DocInfo, error)   { return f.list, f.findErr }
func (f *fakeDocsProvider) Get(name string) (string, error) { return "content for " + name, f.getErr }
func (f *fakeDocsProvider) FindDocFile(query string) (core.DocInfo, error) {
	for _, d := range f.list {
		if d.Name == query {
			return d, nil
		}
	}
	return core.DocInfo{}, f.findErr
}
