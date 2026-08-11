// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package team

import (
	"errors"
	"sync"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/multiagent"
)

// ── Fakes ────────────────────────────────────────────────────────────────

type fakeSession struct {
	mu            sync.Mutex
	providerID    string
	modelID       string
	mode          internal.ModeState
	thinkingLevel string
	switchErr     error // injected failure
	calls         []string
}

func (f *fakeSession) SwitchModel(providerID, modelID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.switchErr != nil {
		return f.switchErr
	}
	f.providerID, f.modelID = providerID, modelID
	f.calls = append(f.calls, "switch:"+modelID)
	return nil
}
func (f *fakeSession) CurrentModel() (string, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.providerID, f.modelID
}
func (f *fakeSession) CurrentMode() internal.ModeState {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mode
}
func (f *fakeSession) SetMode(ms internal.ModeState) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mode = ms
	f.calls = append(f.calls, "mode:"+string(ms.Major))
	return nil
}
func (f *fakeSession) SetThinkingLevel(level string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.thinkingLevel = level
	f.calls = append(f.calls, "thinking:"+level)
	return nil
}
func (f *fakeSession) CurrentThinkingLevel() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.thinkingLevel
}

type fakePool struct {
	mu       sync.Mutex
	configs  map[string]multiagent.AgentConfig
	applyErr error
	evicted  []string
}

func newFakePool() *fakePool { return &fakePool{configs: map[string]multiagent.AgentConfig{}} }

func (f *fakePool) ApplyMember(role string, cfg multiagent.AgentConfig) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.applyErr != nil {
		return f.applyErr
	}
	f.configs[role] = cfg
	return nil
}
func (f *fakePool) RoleConfig(role string) multiagent.AgentConfig {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.configs[role]
}
func (f *fakePool) Evict(role string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.configs, role)
	f.evicted = append(f.evicted, role)
}

type fakeReview struct {
	mu       sync.Mutex
	policy   string
	triggers []string
	applyErr error
}

func (f *fakeReview) ApplyReview(policy string, triggers []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.applyErr != nil {
		return f.applyErr
	}
	f.policy, f.triggers = policy, append([]string(nil), triggers...)
	return nil
}
func (f *fakeReview) CurrentReview() (string, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.policy, append([]string(nil), f.triggers...)
}

type fakeMemberApplier struct {
	err error
}

func (f *fakeMemberApplier) MemberConfig(def config.TeamDefinition, rm config.ResolvedMember) (multiagent.AgentConfig, error) {
	if f.err != nil {
		return multiagent.AgentConfig{}, f.err
	}
	return multiagent.AgentConfig{
		ModelName:       rm.Member.Model,
		ProviderID:      rm.Member.Provider,
		ReasoningEffort: agentic.ReasoningEffort(rm.Member.ThinkingLevel),
	}, nil
}

// ── Fixtures ─────────────────────────────────────────────────────────────

func teamsTestConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Teams.Definitions = map[string]config.TeamDefinition{
		"pair": {
			Main:      &config.TeamMember{Model: "m-main", Mode: "coder", ThinkingLevel: "high"},
			Companion: &config.TeamMember{Model: "m-rev", Mode: "reviewer", ThinkingLevel: "low"},
			Review:    "agent",
		},
		"crew": {
			Members: map[string]config.TeamMember{
				"lead": {Model: "m-main", Role: "main"},
				"qa":   {Model: "m-qa", Role: "reviewer"},
				"sec":  {Model: "m-sec", Role: "reviewer"},
				"help": {Model: "m-help"},
			},
			Review:      "gated",
			ReviewGates: config.TeamReviewGates{Triggers: []string{"goal_complete"}, Quorum: "all"},
		},
	}
	return cfg
}

func newTestManager(cfg *config.Config) (*Manager, *fakeSession, *fakePool, *fakeReview, *[]string) {
	fs := &fakeSession{
		providerID: "orig-p", modelID: "orig-m",
		mode:          internal.ModeState{Major: "coder", Autonomy: "solo"},
		thinkingLevel: "medium",
	}
	fp := newFakePool()
	fr := &fakeReview{}
	var events []string
	m := NewManager(cfg, fs, fp, fr, &fakeMemberApplier{}, func(event string, _ map[string]any) {
		events = append(events, event)
	})
	return m, fs, fp, fr, &events
}

// ── Tests ────────────────────────────────────────────────────────────────

func TestManager_ActivateAppliesTeam(t *testing.T) {
	cfg := teamsTestConfig()
	m, fs, fp, fr, events := newTestManager(cfg)

	if err := m.Activate("pair"); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if m.Active() != "pair" {
		t.Errorf("Active = %q, want pair", m.Active())
	}
	// Main member applied to the session.
	if fs.modelID != "m-main" {
		t.Errorf("session model = %q, want m-main", fs.modelID)
	}
	if fs.mode.Major != "coder" {
		t.Errorf("session mode = %q, want coder", fs.mode.Major)
	}
	if fs.thinkingLevel != "high" {
		t.Errorf("session thinking = %q, want high (member override)", fs.thinkingLevel)
	}
	// Reviewer registered under member name AND companion alias.
	if got := fp.RoleConfig("companion"); got.ModelName != "m-rev" {
		t.Errorf("companion pool config = %+v", got)
	}
	if got := fp.RoleConfig("companion"); string(got.ReasoningEffort) != "low" {
		t.Errorf("companion thinking = %q, want low", got.ReasoningEffort)
	}
	// Review policy applied.
	if fr.policy != "agent" {
		t.Errorf("review policy = %q, want agent", fr.policy)
	}
	if len(*events) != 1 || (*events)[0] != "team.activated" {
		t.Errorf("events = %v", *events)
	}
}

func TestManager_ActivateNMemberRegistersAll(t *testing.T) {
	cfg := teamsTestConfig()
	m, _, fp, fr, _ := newTestManager(cfg)

	if err := m.Activate("crew"); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	// First reviewer (qa, sorted first) aliases companion; sec under its name;
	// worker registered; main NOT in the pool.
	if got := fp.RoleConfig("companion"); got.ModelName != "m-qa" {
		t.Errorf("companion = %+v, want qa model", got)
	}
	if got := fp.RoleConfig("sec"); got.ModelName != "m-sec" {
		t.Errorf("sec = %+v", got)
	}
	if got := fp.RoleConfig("help"); got.ModelName != "m-help" {
		t.Errorf("help = %+v", got)
	}
	if got := fp.RoleConfig("lead"); got.ModelName != "" {
		t.Errorf("lead should not be pool-registered, got %+v", got)
	}
	if fr.policy != "gated" || len(fr.triggers) != 1 || fr.triggers[0] != "goal_complete" {
		t.Errorf("review = %q %v", fr.policy, fr.triggers)
	}
}

func TestManager_DeactivateRestoresSnapshot(t *testing.T) {
	cfg := teamsTestConfig()
	m, fs, fp, fr, _ := newTestManager(cfg)
	// Pre-existing companion config must be restored, not wiped.
	_ = fp.ApplyMember("companion", multiagent.AgentConfig{ModelName: "old-companion"})

	if err := m.Activate("pair"); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if err := m.Deactivate(); err != nil {
		t.Fatalf("Deactivate: %v", err)
	}
	if m.Active() != "" {
		t.Errorf("Active = %q, want empty", m.Active())
	}
	if fs.modelID != "orig-m" || fs.providerID != "orig-p" {
		t.Errorf("model = %s/%s, want orig", fs.providerID, fs.modelID)
	}
	if fs.thinkingLevel != "medium" {
		t.Errorf("thinking = %q, want medium restored", fs.thinkingLevel)
	}
	if got := fp.RoleConfig("companion"); got.ModelName != "old-companion" {
		t.Errorf("companion = %+v, want old-companion restored", got)
	}
	// Restore maps the empty pre-team policy to "off" (a valid review state);
	// the important part is the team's "agent" policy is gone.
	if fr.policy != ReviewApplyOff {
		t.Errorf("review policy = %q, want restored off", fr.policy)
	}
}

func TestManager_ReactivationDoesNotLeak(t *testing.T) {
	cfg := teamsTestConfig()
	m, fs, _, _, _ := newTestManager(cfg)

	if err := m.Activate("pair"); err != nil {
		t.Fatalf("Activate pair: %v", err)
	}
	if err := m.Activate("crew"); err != nil {
		t.Fatalf("Activate crew: %v", err)
	}
	if m.Active() != "crew" {
		t.Errorf("Active = %q, want crew", m.Active())
	}
	if err := m.Deactivate(); err != nil {
		t.Fatalf("Deactivate: %v", err)
	}
	// Baseline must be the pre-pair state, not the pair's state.
	if fs.modelID != "orig-m" {
		t.Errorf("model = %q, want orig-m (no leak from pair)", fs.modelID)
	}
	if fs.thinkingLevel != "medium" {
		t.Errorf("thinking = %q, want medium (no leak)", fs.thinkingLevel)
	}
}

func TestManager_ActivateFailureRestores(t *testing.T) {
	cfg := teamsTestConfig()
	m, fs, _, _, _ := newTestManager(cfg)
	fs.switchErr = errors.New("provider unreachable")

	if err := m.Activate("pair"); err == nil {
		t.Fatal("expected activation error")
	}
	if m.Active() != "" {
		t.Errorf("Active = %q after failure, want empty", m.Active())
	}
	// The failed switch never mutated the session model.
	if fs.modelID != "orig-m" {
		t.Errorf("model = %q after failure, want orig-m", fs.modelID)
	}
}

func TestManager_ActivateUnknownTeam(t *testing.T) {
	cfg := teamsTestConfig()
	m, _, _, _, _ := newTestManager(cfg)
	err := m.Activate("ghost")
	if err == nil {
		t.Fatal("expected error for unknown team")
	}
	if got := err.Error(); !contains(got, "ghost") || !contains(got, "pair") {
		t.Errorf("error %q should name the missing team and list defined ones", got)
	}
}

func TestManager_OverlayLifecycle(t *testing.T) {
	cfg := teamsTestConfig()
	m, fs, _, fr, _ := newTestManager(cfg)

	if err := m.Activate("pair"); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if err := m.ApplyOverlay("crew"); err != nil {
		t.Fatalf("ApplyOverlay: %v", err)
	}
	if m.EffectiveTeam() != "crew" {
		t.Errorf("EffectiveTeam = %q, want crew (overlay wins)", m.EffectiveTeam())
	}
	// Cannot switch session team while overlaid.
	if err := m.Activate("pair"); err == nil {
		t.Error("Activate during overlay should fail")
	}
	if err := m.RemoveOverlay(); err != nil {
		t.Fatalf("RemoveOverlay: %v", err)
	}
	// Session-level team state restored.
	if m.EffectiveTeam() != "pair" {
		t.Errorf("EffectiveTeam = %q, want pair after overlay removal", m.EffectiveTeam())
	}
	if fs.modelID != "m-main" || fr.policy != "agent" {
		t.Errorf("after overlay removal: model=%q review=%q, want pair state", fs.modelID, fr.policy)
	}
}

func TestManager_DriftAndSync(t *testing.T) {
	cfg := teamsTestConfig()
	m, fs, _, _, _ := newTestManager(cfg)
	if err := m.Activate("pair"); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if m.Drifted() {
		t.Error("fresh activation should not be drifted")
	}
	// Simulate a manual /model switch (drift).
	fs.modelID = "user-picked"
	m.MarkDrift()
	if !m.Drifted() {
		t.Error("MarkDrift should set drifted")
	}
	if err := m.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if m.Drifted() {
		t.Error("Sync should clear drift")
	}
	if fs.modelID != "m-main" {
		t.Errorf("model = %q after sync, want m-main", fs.modelID)
	}
}

func TestManager_ConcurrentAccess(t *testing.T) {
	cfg := teamsTestConfig()
	m, _, _, _, _ := newTestManager(cfg)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = m.Active()
			_ = m.EffectiveTeam()
			_ = m.Drifted()
			if i%2 == 0 {
				_ = m.Activate("pair")
			} else {
				_ = m.Deactivate()
			}
		}(i)
	}
	wg.Wait()
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
