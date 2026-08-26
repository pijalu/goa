// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/agentic"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/provider"
)

const restoreModelSessionName = "s1"

// restoreModelConfig is the fixture config: p1/m1 and p2/m2 both fully
// resolvable, with the BOOT selection pointing at p2/m2 — the ~/.goa-latest
// couple a restored session must NOT be dragged to (bugs.md 2026-08-26).
func restoreModelConfig() *config.Config {
	return &config.Config{
		ActiveProvider: "p2",
		ActiveModel:    "m2",
		Providers: []config.ProviderConfig{
			{ID: "p1", Name: "P One", Endpoint: "https://p1.example/v1"},
			{ID: "p2", Name: "P Two", Endpoint: "https://p2.example/v1"},
		},
		Models: []config.ModelConfig{
			{ID: "m1", ProviderID: "p1", Model: "model-one-api"},
			{ID: "m2", ProviderID: "p2", Model: "model-two-api"},
		},
	}
}

func plainContentEvent() agentic.OutputEvent {
	return agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.User, Text: "turn"}
}

// seedSession writes the named session file via a real store, synchronously
// flushed before returning.
func seedSession(t *testing.T, dir, name string, events []agentic.OutputEvent) {
	t.Helper()
	ss := core.NewSessionStore(dir)
	if id := ss.StartSessionWithID(name); id != name {
		t.Fatalf("seed StartSessionWithID = %q, want %q", id, name)
	}
	for _, ev := range events {
		ss.WriteEvent(ev)
	}
	if err := ss.Close(); err != nil {
		t.Fatalf("seed close: %v", err)
	}
}

// restoreFixture holds every REAL surface the restore path touches.
type restoreFixture struct {
	ctx core.Context
	cfg *config.Config
	pm  core.ProviderManager
	am  *core.AgentManager
	ss  *core.SessionStore
	bus *event.Bus
	out strings.Builder
	dir string
}

func newRestoreFixture(t *testing.T, seedEvents []agentic.OutputEvent) *restoreFixture {
	t.Helper()
	f := &restoreFixture{dir: t.TempDir()}
	f.cfg = restoreModelConfig()
	f.pm = provider.NewProviderManager(f.cfg)
	seedSession(t, f.dir, restoreModelSessionName, seedEvents)
	f.ss = core.NewSessionStore(f.dir)
	f.bus = event.MakeBus(8, 8, 8, 8)
	f.am = core.NewAgentManager(f.cfg, f.ss, nil, nil, f.bus, f.dir)

	// Live session bound to M2@P2 — the couple a switch left behind.
	mdl := agenticprovider.Model{ID: "m2", Api: agenticprovider.ApiOpenAICompletions, Provider: agenticprovider.Provider("p2")}
	if _, err := f.am.StartSession(mdl, agenticprovider.StreamOptions{}, "sys", nil, f.cfg); err != nil {
		t.Fatalf("StartSession(m2): %v", err)
	}

	f.ctx = core.Context{
		Config:          f.cfg,
		ProviderManager: f.pm,
		AgentManager:    f.am,
		EventBus:        f.bus,
		OutputBuffer:    &f.out,
	}
	return f
}

// waitForFlashContaining polls the bus chat channel until a flash carrying
// the wanted substring arrives, returning all flashes seen so far. Unlike a
// fixed-window drain, it returns the MOMENT the expected flash is observed
// and tolerates a loaded scheduler (the goroutine-launched replay flash can
// be delayed well past 200ms under CPU starvation — bugs.md FLAKE
// 2026-08-26). It only fails the test after the full deadline passes without
// the expected flash.
func waitForFlashContaining(t *testing.T, bus *event.Bus, want string, d time.Duration) []string {
	t.Helper()
	var got []string
	deadline := time.After(d)
	for {
		select {
		case ev := <-bus.Chat:
			if ev.Flash != nil {
				got = append(got, ev.Flash.Text)
				if strings.Contains(ev.Flash.Text, want) {
					return got
				}
			}
		case <-deadline:
			t.Fatalf("flash containing %q not observed within %s; flashes seen: %v", want, d, got)
			return got
		}
	}
}

// TestWaitForFlashContaining_SlowProducer: a flash posted after a >200ms
// scheduling delay (the loaded-box shape) is still collected — the old
// fixed-window drain missed it (bugs.md FLAKE 2026-08-26).
func TestWaitForFlashContaining_SlowProducer(t *testing.T) {
	bus := event.MakeBus(8, 8, 8, 8)
	go func() {
		time.Sleep(400 * time.Millisecond) // simulated scheduler starvation
		bus.Chat <- event.ChatEvent{Flash: &event.Flash{Text: "late flash"}}
	}()
	got := waitForFlashContaining(t, bus, "late flash", 5*time.Second)
	if len(got) != 1 || got[0] != "late flash" {
		t.Fatalf("got %v, want [late flash]", got)
	}
}

func markersIn(t *testing.T, f *restoreFixture) []agentic.OutputEvent {
	t.Helper()
	events, err := f.ss.LoadSession(restoreModelSessionName)
	if err != nil {
		t.Fatalf("LoadSession(%s): %v", restoreModelSessionName, err)
	}
	var out []agentic.OutputEvent
	for _, ev := range events {
		if ev.Metadata["event"] == core.ModelSelectedEventKey {
			out = append(out, ev)
		}
	}
	return out
}

// TestRestoreSession_AdoptsRecordedModel is the end-to-end RED test for the
// bugs.md entry "session restore does not bind the session's provider/model":
// restoring s1 (last binding m1 @ p1) while the global selection says p2/m2
// must re-bind EVERY live surface to m1 and leave ~/.goa untouched.
func TestRestoreSession_AdoptsRecordedModel(t *testing.T) {
	seed := []agentic.OutputEvent{
		plainContentEvent(),
		core.ModelSelectedMarker(core.ModelMarkerSourceStart, "p1", "m1"),
		plainContentEvent(),
	}
	f := newRestoreFixture(t, seed)

	if err := restoreSession(f.ctx, f.ctx, f.ss, f.am, restoreModelSessionName, 0); err != nil {
		t.Fatalf("restoreSession: %v", err)
	}

	// 1. The agent runs on the session's own model from now on. The resolved
	// Model carries registry-merged identity fields (canonical provider
	// family, API name), so the STABLE truth is the endpoint + selection +
	// recorded couple — all of which must say p1/m1.
	assertAdoptedBindingP1(t, f)
	assertAdoptionMarkerP1(t, f)
	assertAdoptionFlashP1(t, f)
}

// assertAdoptedBindingP1 pins the live re-bind surfaces to the session's own
// model: the agent endpoint, the in-memory config selection and the provider
// manager session selection must all say p1/m1, and the restore banner stays
// on the one-line format (no "· model" buffer note — that rides the flash).
func assertAdoptedBindingP1(t *testing.T, f *restoreFixture) {
	t.Helper()
	got := f.am.ActiveModel()
	if !strings.HasPrefix(got.BaseURL, "https://p1.example") {
		t.Fatalf("active agent BaseURL = %q, want p1 endpoint binding", got.BaseURL)
	}
	// Nothing was persisted anywhere (fixture installs no ConfigSaver — any
	// home write would panic).
	if f.cfg.ActiveProvider != "p1" || f.cfg.ActiveModel != "m1" {
		t.Fatalf("config selection = %s/%s, want p1/m1 in memory only", f.cfg.ActiveProvider, f.cfg.ActiveModel)
	}
	pCfg, _ := f.pm.Active()
	if pCfg == nil || pCfg.ID != "p1" {
		t.Fatalf("provider-manager session selection = %+v, want profile p1", pCfg)
	}
	if strings.Contains(f.out.String(), "· model") || !strings.Contains(f.out.String(), "Restored session 's1'") {
		t.Errorf("unexpected buffer output: %q", f.out.String())
	}
}

// assertAdoptionMarkerP1 pins re-bind durability: a fresh switch marker lands
// in the restored file on top of the seed start marker.
func assertAdoptionMarkerP1(t *testing.T, f *restoreFixture) {
	t.Helper()
	markers := markersIn(t, f)
	if len(markers) < 2 {
		t.Fatalf("expected seed start marker + adoption switch marker, got %d", len(markers))
	}
	lastP, lastM, ok := core.SessionModelFromEvents(markers)
	if !ok || lastP != "p1" || lastM != "m1" {
		t.Fatalf("final marker = (%q,%q,%v), want (p1,m1,true)", lastP, lastM, ok)
	}
}

// assertAdoptionFlashP1 polls until the adoption flash arrives instead of
// draining a fixed 200ms window: under box load the flash can be delayed past
// the window (bugs.md FLAKE 2026-08-26).
func assertAdoptionFlashP1(t *testing.T, f *restoreFixture) {
	t.Helper()
	flashes := waitForFlashContaining(t, f.bus, "model m1 @ p1", 5*time.Second)
	if !strings.Contains(strings.Join(flashes, "\n"), "model m1 @ p1") {
		t.Errorf("flash missing adopted binding: %v", flashes)
	}
}

// TestRestoreSession_LegacyKeepsCurrentSelection: sessions without markers
// (old files) restore exactly as before — no adoption attempt, no extra note.
func TestRestoreSession_LegacyKeepsCurrentSelection(t *testing.T) {
	f := newRestoreFixture(t, []agentic.OutputEvent{plainContentEvent(), plainContentEvent()})

	if err := restoreSession(f.ctx, f.ctx, f.ss, f.am, restoreModelSessionName, 0); err != nil {
		t.Fatalf("restoreSession: %v", err)
	}
	if got := f.am.ActiveModel(); got.ID != "m2" {
		t.Fatalf("legacy restore moved the binding to %+v, want m2 kept", got)
	}
	if strings.Contains(f.out.String(), "· model") {
		t.Errorf("legacy restore must not print a model note: %q", f.out.String())
	}
}

// TestRestoreSession_UnresolvableRecordedModel: a recorded provider that no
// longer exists flashes a keep-current note and the restore still completes.
func TestRestoreSession_UnresolvableRecordedModel(t *testing.T) {
	seed := []agentic.OutputEvent{
		plainContentEvent(),
		core.ModelSelectedMarker(core.ModelMarkerSourceSwitch, "ghost", "gone-model"),
	}
	f := newRestoreFixture(t, seed)

	if err := restoreSession(f.ctx, f.ctx, f.ss, f.am, restoreModelSessionName, 0); err != nil {
		t.Fatalf("restoreSession: %v", err)
	}
	if got := f.am.ActiveModel(); got.ID != "m2" {
		t.Fatalf("binding = %+v, want current m2 kept after failed adoption", got)
	}
	flashes := waitForFlashContaining(t, f.bus, "keeping the current selection", 5*time.Second)
	if joined := strings.Join(flashes, "\n"); !strings.Contains(joined, "keeping the current selection") {
		t.Errorf("missing fallback notice, got: %v", flashes)
	}
}
