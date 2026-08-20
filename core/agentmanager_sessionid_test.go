// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
)

// TestAgentManager_SetStreamOptions_PreservesSessionID pins the Rule 7
// contract for mid-session provider/model switches: replacing stream options
// via SetStreamOptions (used by /model and the team session controller with
// ProviderManager.BuildStreamOptions, which carries no SessionID) must not
// silently drop the live conversation's SessionID — the conversation is an
// append of itself, so it keeps its provider cache key. Dropping it
// re-keyed the next turn onto a fresh cache namespace with the bust
// detector still armed (apparent cache miss + lost cache).
func TestAgentManager_SetStreamOptions_PreservesSessionID(t *testing.T) {
	cfg := &config.Config{}
	am := NewAgentManager(cfg, nil, nil, NewSessionState(internal.ModeState{}), nil, "")

	if _, err := am.StartSession(agenticprovider.Model{}, agenticprovider.StreamOptions{SessionID: "sess-live"}, "", nil, cfg); err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	// Model switch shape: fresh options WITHOUT a SessionID must inherit the
	// live one.
	am.SetStreamOptions(agenticprovider.StreamOptions{Timeout: 42})
	if got := am.CurrentAgent().StreamOptions().SessionID; got != "sess-live" {
		t.Errorf("SessionID after empty-id SetStreamOptions = %q, want %q", got, "sess-live")
	}

	// Deliberate override shape: a non-empty incoming SessionID wins.
	am.SetStreamOptions(agenticprovider.StreamOptions{SessionID: "sess-static"})
	if got := am.CurrentAgent().StreamOptions().SessionID; got != "sess-static" {
		t.Errorf("SessionID after explicit SetStreamOptions = %q, want %q", got, "sess-static")
	}
}
