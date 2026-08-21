// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/multiagent"
)

// TestApplyStartupTeam verifies RC-4 fix: teams.active from config is applied
// at boot (the configured team becomes real) and failures surface as a chat
// flash instead of leaving a hidden/divergent team state.
func TestApplyStartupTeam(t *testing.T) {
	cfg := &config.Config{ActiveProvider: "orig-p", ActiveModel: "orig-m"}
	cfg.Teams.Active = "pair"
	cfg.Models = []config.ModelConfig{{ID: "m-main"}, {ID: "m-rev"}}
	cfg.Teams.Definitions = map[string]config.TeamDefinition{
		"pair": {
			Main:      &config.TeamMember{Model: "m-main"},
			Companion: &config.TeamMember{Model: "m-rev"},
		},
	}

	subs := testSubsystems()
	subs.cfg = cfg
	subs.teamManager = newTeamManager(cfg, nil, nil, nil, nil, nil, nil)
	subs.applyStartupTeam()

	if got := subs.teamManager.Active(); got != "pair" {
		t.Errorf("Active() = %q after startup activation, want pair", got)
	}
	// The main member drives the session model: config-level switch applied.
	if cfg.ActiveModel != "m-main" {
		t.Errorf("ActiveModel = %q after startup activation, want m-main", cfg.ActiveModel)
	}
}

// TestApplyStartupTeam_NoTeamConfigured is the backward-compat guard: no
// teams.active → no activation, no flash.
func TestApplyStartupTeam_NoTeamConfigured(t *testing.T) {
	cfg := &config.Config{ActiveProvider: "orig-p", ActiveModel: "orig-m"}
	subs := testSubsystems()
	subs.cfg = cfg
	subs.teamManager = newTeamManager(cfg, nil, nil, nil, nil, nil, nil)
	subs.applyStartupTeam()
	if got := subs.teamManager.Active(); got != "" {
		t.Errorf("Active() = %q, want empty", got)
	}
}

// TestApplyStartupTeam_UnknownTeamFlashes: a dangling teams.active (definition
// removed) must surface a chat flash, not fail silently.
func TestApplyStartupTeam_UnknownTeamFlashes(t *testing.T) {
	cfg := &config.Config{ActiveProvider: "orig-p", ActiveModel: "orig-m"}
	cfg.Teams.Active = "ghost"
	cfg.Teams.Definitions = map[string]config.TeamDefinition{}

	subs := testSubsystems()
	subs.cfg = cfg
	subs.teamManager = newTeamManager(cfg, nil, nil, nil, nil, nil, nil)
	subs.applyStartupTeam()

	select {
	case ev := <-subs.events.Chat:
		if ev.Flash == nil || !strings.Contains(ev.Flash.Text, "ghost") {
			t.Errorf("flash = %+v, want mention of ghost", ev.Flash)
		}
	default:
		t.Fatal("no chat flash for dangling teams.active")
	}
}

// TestTeamChangeCallbackWiring verifies the assembled callback announces every
// transition in chat and requests a footer refresh (RC-4: no hidden team).
func TestTeamChangeCallbackWiring(t *testing.T) {
	cfg := &config.Config{ActiveProvider: "orig-p", ActiveModel: "orig-m"}
	cfg.Models = []config.ModelConfig{{ID: "orig-m"}, {ID: "m-main"}, {ID: "m-rev"}}
	cfg.Teams.Definitions = map[string]config.TeamDefinition{
		"pair": {
			Main:      &config.TeamMember{Model: "m-main"},
			Companion: &config.TeamMember{Model: "m-rev"},
		},
	}

	subs := testSubsystems()
	subs.cfg = cfg
	subs.teamManager = newTeamManager(cfg, nil, nil, multiagent.NewAgentPool(agenticprovider.Model{}, agenticprovider.StreamOptions{}, nil), nil, nil, nil)
	// Install the same callback assembleSubsystems installs.
	subs.teamManager.SetChangeCallback(func(effective, reason string) {
		text := teamChangeAnnouncement(effective, reason)
		select {
		case subs.events.Chat <- event.ChatEvent{Flash: &event.Flash{Text: text}}:
		default:
		}
		select {
		case subs.events.Footer <- event.FooterEvent{FooterRefresh: true}:
		default:
		}
	})

	if err := subs.teamManager.Activate("pair"); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	assertChatFlash(t, subs, "Team active: pair")
	assertFooterRefresh(t, subs)

	if err := subs.teamManager.Deactivate(); err != nil {
		t.Fatalf("Deactivate: %v", err)
	}
	assertChatFlash(t, subs, "deactivated")
}

// assertChatFlash requires a chat flash containing substr.
func assertChatFlash(t *testing.T, subs *subsystems, substr string) {
	t.Helper()
	select {
	case ev := <-subs.events.Chat:
		if ev.Flash == nil || !strings.Contains(ev.Flash.Text, substr) {
			t.Errorf("chat flash = %+v, want substring %q", ev.Flash, substr)
		}
	default:
		t.Fatalf("no chat flash (want %q)", substr)
	}
}

// assertFooterRefresh requires a pending footer-refresh event.
func assertFooterRefresh(t *testing.T, subs *subsystems) {
	t.Helper()
	select {
	case ev := <-subs.events.Footer:
		if !ev.FooterRefresh {
			t.Errorf("footer event = %+v, want FooterRefresh", ev)
		}
	default:
		t.Fatal("no footer refresh event")
	}
}

// TestTeamChangeAnnouncement covers the message matrix.
func TestTeamChangeAnnouncement(t *testing.T) {
	cases := []struct {
		effective, reason, want string
	}{
		{"", "deactivated", "Team deactivated"},
		{"pair", "activated", "Team active: pair"},
		{"crew", "overlay", "Team overlay active: crew"},
		{"pair", "overlay removed", "Team overlay removed — team pair governs again"},
		{"", "overlay removed", "Team overlay removed — session model restored"},
	}
	for _, c := range cases {
		if got := teamChangeAnnouncement(c.effective, c.reason); !strings.Contains(got, c.want) {
			t.Errorf("teamChangeAnnouncement(%q, %q) = %q, want substring %q", c.effective, c.reason, got, c.want)
		}
	}
}
