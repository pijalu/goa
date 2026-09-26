// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestDefaultConfig_StallTimingDefaults pins the shipped silent-stream timing:
// the stall warning fires 30s into a 45s auto-retry window — exactly what the
// user-visible message reports ("provider quiet for 30s — still waiting; will
// auto-retry after 45s of silence"). Both keys live in the embedded defaults so
// a fresh install ships the documented pair and /config can show and edit them.
func TestDefaultConfig_StallTimingDefaults(t *testing.T) {
	// Isolate from the developer's real ~/.goa/config.yaml (which legitimately
	// pins activity_timeout): this asserts the SHIPPED defaults.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	cfg, err := NewCascadeLoader(t.TempDir(), "", nil).Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Execution.ActivityTimeout != "45s" {
		t.Errorf("execution.activity_timeout = %q, want \"45s\"", cfg.Execution.ActivityTimeout)
	}
	if cfg.Execution.ActivityWarnAfter != "30s" {
		t.Errorf("execution.activity_warn_after = %q, want \"30s\"", cfg.Execution.ActivityWarnAfter)
	}
	timeout, err := time.ParseDuration(cfg.Execution.ActivityTimeout)
	if err != nil {
		t.Fatalf("activity_timeout unparseable: %v", err)
	}
	warn, err := time.ParseDuration(cfg.Execution.ActivityWarnAfter)
	if err != nil {
		t.Fatalf("activity_warn_after unparseable: %v", err)
	}
	if warn >= timeout {
		t.Errorf("shipped warning lead %s must precede the %s retry window", warn, timeout)
	}
	if want := timeout * 2 / 3; warn != want {
		t.Errorf("shipped warning lead = %s, want two thirds of the window (%s)", warn, want)
	}
}

// TestMergeExecution_ActivityWarnAfter verifies the key participates in the
// cascade like every other execution scalar: a layer that sets it wins, a layer
// that omits it keeps the inherited value.
func TestMergeExecution_ActivityWarnAfter(t *testing.T) {
	t.Run("higher layer overrides", func(t *testing.T) {
		dst := ExecutionConfig{ActivityTimeout: "45s", ActivityWarnAfter: "30s"}
		mergeExecution(&dst, &ExecutionConfig{ActivityWarnAfter: "20s"})
		if dst.ActivityWarnAfter != "20s" {
			t.Errorf("activity_warn_after = %q, want 20s", dst.ActivityWarnAfter)
		}
		if dst.ActivityTimeout != "45s" {
			t.Errorf("activity_timeout must stay inherited, got %q", dst.ActivityTimeout)
		}
	})

	t.Run("omitted key keeps the inherited value", func(t *testing.T) {
		dst := ExecutionConfig{ActivityTimeout: "45s", ActivityWarnAfter: "30s"}
		mergeExecution(&dst, &ExecutionConfig{ActivityTimeout: "60s"})
		if dst.ActivityWarnAfter != "30s" {
			t.Errorf("activity_warn_after = %q, want inherited 30s", dst.ActivityWarnAfter)
		}
		if dst.ActivityTimeout != "60s" {
			t.Errorf("activity_timeout = %q, want 60s", dst.ActivityTimeout)
		}
	})

	t.Run("cascade override through a full Config merge", func(t *testing.T) {
		base := &Config{Execution: ExecutionConfig{ActivityTimeout: "45s", ActivityWarnAfter: "30s"}}
		top := &Config{Execution: ExecutionConfig{ActivityWarnAfter: "15s"}}
		base.DeepMerge(top)
		if base.Execution.ActivityWarnAfter != "15s" || base.Execution.ActivityTimeout != "45s" {
			t.Errorf("merged = (%q, %q), want (45s, 15s)", base.Execution.ActivityTimeout, base.Execution.ActivityWarnAfter)
		}
	})
}

// TestValidate_ActivityWarnAfter covers the validation contract for the stall
// timing pair in BOTH scopes:
//
//   - a single cascade layer that explicitly sets both keys must be consistent
//     (warn < timeout) — a contradictory explicit pair in one file is a
//     configuration mistake and is reported at load time, naming the file;
//   - the MERGED config is only checked for parseability. The two keys cascade
//     independently, so a home/project pin of activity_timeout: 30s under the
//     shipped activity_warn_after: 30s is a legitimate install and must not
//     refuse to start (observed against a real ~/.goa/config.yaml). The
//     ordering invariant is enforced where the values are consumed instead:
//     agentic.Agent.effectiveStallWarnAfter derives two thirds of the effective
//     window whenever the configured lead is unset, at, or beyond it.
func TestValidate_ActivityWarnAfter(t *testing.T) {
	t.Run("merged config only checks parseability", testActivityWarnAfterMergedConfig)
	t.Run("one layer setting both keys must be consistent", testActivityWarnAfterLayerPair)
}

func testActivityWarnAfterMergedConfig(t *testing.T) {
	tests := []struct {
		name      string
		warn      string
		timeout   string
		wantError bool
	}{
		{name: "shipped pair", warn: "30s", timeout: "45s"},
		{name: "unparseable warn value rejected", warn: "not-a-duration", timeout: "45s", wantError: true},
		{name: "unparseable timeout rejected", warn: "30s", timeout: "soon", wantError: true},
		{name: "cross-layer lead equal to the window accepted", warn: "30s", timeout: "30s"},
		{name: "cross-layer lead beyond the window accepted", warn: "90s", timeout: "30s"},
		{name: "empty values accepted (provider/agent default window applies)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{Execution: ExecutionConfig{ActivityWarnAfter: tt.warn, ActivityTimeout: tt.timeout}}

			err := cfg.Validate()
			if tt.wantError && err == nil {
				t.Fatalf("Validate(%q/%q) = nil, want an error", tt.warn, tt.timeout)
			}
			if !tt.wantError && err != nil {
				t.Fatalf("Validate(%q/%q) = %v, want nil", tt.warn, tt.timeout, err)
			}
		})
	}
}

// testActivityWarnAfterLayerPair covers the per-layer pair check: a single
// config source that sets BOTH keys must be self-consistent, while a source
// that sets only one of them combines freely with the other layers.
func testActivityWarnAfterLayerPair(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantMsg string
	}{
		{
			name: "consistent explicit pair accepted",
			yaml: "execution:\n  activity_timeout: 45s\n  activity_warn_after: 30s\n",
		},
		{
			name:    "lead equal to the window rejected",
			yaml:    "execution:\n  activity_timeout: 30s\n  activity_warn_after: 30s\n",
			wantMsg: "activity_warn_after",
		},
		{
			name:    "lead beyond the window rejected",
			yaml:    "execution:\n  activity_timeout: 30s\n  activity_warn_after: 90s\n",
			wantMsg: "activity_warn_after",
		},
		{
			name: "only the window pinned (cross-layer combination) accepted",
			yaml: "execution:\n  activity_timeout: 30s\n",
		},
		{
			name:    "unparseable value still rejected (by Validate)",
			yaml:    "execution:\n  activity_timeout: soon\n  activity_warn_after: 30s\n",
			wantMsg: "activity_timeout",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadHomeLayerOnly(t, tt.yaml)
			if tt.wantMsg == "" {
				if err != nil {
					t.Fatalf("Load() = %v, want nil for %q", err, tt.yaml)
				}
				return
			}
			assertLoadErrorMentions(t, err, tt.wantMsg, tt.yaml)
		})
	}
}

// loadHomeLayerOnly writes the YAML as the ONLY config layer (isolated HOME and
// project dir) and loads the cascade.
func loadHomeLayerOnly(t *testing.T, yamlText string) (*Config, error) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.MkdirAll(filepath.Join(home, ".goa"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".goa", "config.yaml"), []byte(yamlText), 0o644); err != nil {
		t.Fatalf("write home config: %v", err)
	}
	return NewCascadeLoader(t.TempDir(), "", nil).Load()
}

// assertLoadErrorMentions requires a load failure whose message names the key.
func assertLoadErrorMentions(t *testing.T, err error, wantMsg, yamlText string) {
	t.Helper()
	if err == nil {
		t.Fatalf("Load() = nil, want an error for %q", yamlText)
	}
	if !strings.Contains(err.Error(), wantMsg) {
		t.Errorf("error must name %s, got: %v", wantMsg, err)
	}
}
