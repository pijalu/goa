// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"path/filepath"
	"testing"
	"time"
)

// TestSessionWindowDefaultsToSevenDays pins the default the request asked for:
// with nothing configured, session logs are kept 7 days.
func TestSessionWindowDefaultsToSevenDays(t *testing.T) {
	var c SessionsConfig
	window, on := c.SessionWindow()
	if !on {
		t.Fatal("SessionWindow() reported retention off for an unset config, want the 7-day default")
	}
	if want := 7 * 24 * time.Hour; window != want {
		t.Errorf("SessionWindow() = %v, want %v", window, want)
	}
}

// TestSessionWindowTriState covers every way the flag can be set, including the
// two that a plain bool/int pair cannot express through the cascade: an explicit
// "off" and an explicit "keep forever".
func TestSessionWindowTriState(t *testing.T) {
	trueP, falseP := true, false
	zero, three, thirty := 0, 3, 30

	cases := []struct {
		name     string
		ret      SessionsRetentionConfig
		wantOn   bool
		wantDays int
	}{
		{"unset keeps the default", SessionsRetentionConfig{}, true, 7},
		{"enabled true keeps the default window", SessionsRetentionConfig{Enabled: &trueP}, true, 7},
		{"enabled false disables pruning", SessionsRetentionConfig{Enabled: &falseP}, false, 0},
		{"days 0 keeps every log forever", SessionsRetentionConfig{Days: &zero}, false, 0},
		{"days 3 shortens the window", SessionsRetentionConfig{Days: &three}, true, 3},
		{"days 30 widens the window", SessionsRetentionConfig{Days: &thirty}, true, 30},
		{"explicit off wins over an explicit window", SessionsRetentionConfig{Enabled: &falseP, Days: &thirty}, false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			window, on := SessionsConfig{Retention: tc.ret}.SessionWindow()
			if on != tc.wantOn {
				t.Fatalf("SessionWindow() on = %v, want %v", on, tc.wantOn)
			}
			if !tc.wantOn {
				return
			}
			if want := time.Duration(tc.wantDays) * 24 * time.Hour; window != want {
				t.Errorf("SessionWindow() = %v, want %v", window, want)
			}
		})
	}
}

// TestMergeSessionsTriState pins the cascade: whatever a layer does not state
// keeps the lower layer's value, and an explicit false/0 always wins. With a
// default that is ON, this is exactly what the `Days != 0 || Enabled` replace
// rule used by the older retention structs cannot do (bugs.md B15).
func TestMergeSessionsTriState(t *testing.T) {
	trueP, falseP := true, false
	zero, fourteen := 0, 14

	t.Run("unset higher layer preserves the lower layer", func(t *testing.T) {
		dst := Config{Sessions: SessionsConfig{Retention: SessionsRetentionConfig{Enabled: &trueP, Days: &fourteen}}}
		dst.mergeSessions(&Config{})
		if dst.Sessions.Retention.Enabled == nil || !*dst.Sessions.Retention.Enabled {
			t.Error("Enabled was reset by a layer that did not mention it")
		}
		if dst.Sessions.Retention.Days == nil || *dst.Sessions.Retention.Days != 14 {
			t.Error("Days was reset by a layer that did not mention it")
		}
	})

	t.Run("higher layer false overrides lower true", func(t *testing.T) {
		dst := Config{Sessions: SessionsConfig{Retention: SessionsRetentionConfig{Enabled: &trueP}}}
		dst.mergeSessions(&Config{Sessions: SessionsConfig{Retention: SessionsRetentionConfig{Enabled: &falseP}}})
		if _, on := dst.Sessions.SessionWindow(); on {
			t.Error("enabled: false from a higher layer did not disable retention")
		}
	})

	t.Run("higher layer days 0 means keep forever", func(t *testing.T) {
		dst := Config{Sessions: SessionsConfig{Retention: SessionsRetentionConfig{Days: &fourteen}}}
		dst.mergeSessions(&Config{Sessions: SessionsConfig{Retention: SessionsRetentionConfig{Days: &zero}}})
		if _, on := dst.Sessions.SessionWindow(); on {
			t.Error("days: 0 from a higher layer did not disable retention")
		}
	})
}

// TestSessionsRetentionCascade loads the real cascade (embedded defaults plus a
// project layer) rather than merging structs by hand: the requirement is a
// default of 7 days that a user can switch off, so the default and the off
// switch both have to survive the loader, not just the merge function.
func TestSessionsRetentionCascade(t *testing.T) {
	t.Run("embedded default keeps session logs 7 days", func(t *testing.T) {
		homeDir, projectDir, cleanup := setupTestConfig(t)
		defer cleanup()
		t.Setenv("HOME", homeDir)

		cfg, err := NewCascadeLoader(projectDir, "", nil).Load()
		if err != nil {
			t.Fatalf("Load failed: %v", err)
		}
		window, on := cfg.Sessions.SessionWindow()
		if !on {
			t.Fatal("session retention is off by default, want the 7-day default")
		}
		if want := 7 * 24 * time.Hour; window != want {
			t.Errorf("default session window = %v, want %v", window, want)
		}
	})

	t.Run("project layer can switch it off", func(t *testing.T) {
		homeDir, projectDir, cleanup := setupTestConfig(t)
		defer cleanup()
		t.Setenv("HOME", homeDir)
		writeConfig(t, filepath.Join(projectDir, ".goa", "config.yaml"), `
sessions:
  retention:
    enabled: false
`)

		cfg, err := NewCascadeLoader(projectDir, "", nil).Load()
		if err != nil {
			t.Fatalf("Load failed: %v", err)
		}
		if _, on := cfg.Sessions.SessionWindow(); on {
			t.Error("enabled: false in the project layer did not disable session retention")
		}
	})

	t.Run("project layer can widen the window", func(t *testing.T) {
		homeDir, projectDir, cleanup := setupTestConfig(t)
		defer cleanup()
		t.Setenv("HOME", homeDir)
		writeConfig(t, filepath.Join(projectDir, ".goa", "config.yaml"), `
sessions:
  retention:
    days: 30
`)

		cfg, err := NewCascadeLoader(projectDir, "", nil).Load()
		if err != nil {
			t.Fatalf("Load failed: %v", err)
		}
		window, on := cfg.Sessions.SessionWindow()
		if !on || window != 30*24*time.Hour {
			t.Errorf("SessionWindow() = (%v,%v), want (30 days,true)", window, on)
		}
	})
}
