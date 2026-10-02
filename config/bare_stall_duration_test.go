// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"strings"
	"testing"
)

// TestValidate_BareDurationsShowUnitsHint pins the error-message contract
// (bugs.md 2026-09-26: "if units are required — show them"): a duration value
// without a unit is rejected, and the message must SAY the file format
// expects units and show an example — the /config UI itself speaks plain
// seconds, so "missing unit in duration" alone reads as goa being broken.
func TestValidate_BareDurationsShowUnitsHint(t *testing.T) {
	tests := []struct {
		name      string
		timeout   string
		warn      string
		wantErr   bool
		wantParts []string
	}{
		{
			name:    "canonical values accepted",
			timeout: "60s",
			warn:    "45s",
		},
		{
			name:      "bare timeout rejected with units hint",
			timeout:   "60",
			warn:      "45s",
			wantErr:   true,
			wantParts: []string{`cannot parse "60" as duration`, "requires a unit", `did you mean "60s"?`},
		},
		{
			name:      "bare warn rejected with units hint",
			timeout:   "60s",
			warn:      "45",
			wantErr:   true,
			wantParts: []string{`cannot parse "45" as duration`, "requires a unit", `did you mean "45s"?`},
		},
		{
			name:      "garbage rejected with the generic unit examples",
			timeout:   "soon",
			wantErr:   true,
			wantParts: []string{`cannot parse "soon" as duration`, "requires a unit", `"60s"`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkUnitsHintCase(t, tt.timeout, tt.warn, tt.wantErr, tt.wantParts)
		})
	}
}

// checkUnitsHintCase validates one units-hint expectation.
func checkUnitsHintCase(t *testing.T, timeout, warn string, wantErr bool, wantParts []string) {
	t.Helper()
	cfg := &Config{Execution: ExecutionConfig{ActivityTimeout: timeout, ActivityWarnAfter: warn}}
	err := cfg.Validate()
	if !wantErr {
		if err != nil {
			t.Fatalf("Validate(%q/%q) = %v, want nil", timeout, warn, err)
		}
		return
	}
	if err == nil {
		t.Fatalf("Validate(%q/%q) = nil, want an error", timeout, warn)
	}
	for _, part := range wantParts {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("error must contain %q, got:\n%v", part, err)
		}
	}
}

// TestSanitizeBareStallDurations pins the layer-level heal for stall values
// written without a unit: bare integers have exactly one sane reading
// (seconds) and are corrected in memory with a visible warning; anything
// else passes through untouched ("abc" stays Config.Validate's problem).
func TestSanitizeBareStallDurations(t *testing.T) {
	tests := []sanitizeCase{
		{name: "bare timeout and warn healed", timeout: "60", warn: "45", wantTime: "60s", wantWarn: "45s", wantHeald: true},
		{name: "bare zero healed", timeout: "0", wantTime: "0s", wantHeald: true},
		{name: "unit-ed values untouched", timeout: "60s", warn: "2m", wantTime: "60s", wantWarn: "2m"},
		{name: "empty values untouched", wantTime: "", wantWarn: ""},
		{name: "garbage untouched (Validate reports it)", timeout: "soon", warn: "abc", wantTime: "soon", wantWarn: "abc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkSanitizeCase(t, tt)
		})
	}
}

// sanitizeCase mirrors one table row of TestSanitizeBareStallDurations.
type sanitizeCase struct {
	name      string
	timeout   string
	warn      string
	wantTime  string
	wantWarn  string
	wantHeald bool
}

// checkSanitizeCase validates one sanitize expectation (values + warnings).
func checkSanitizeCase(t *testing.T, tt sanitizeCase) {
	t.Helper()
	restore := captureStderr(t)
	exec := &ExecutionConfig{ActivityTimeout: tt.timeout, ActivityWarnAfter: tt.warn}
	heals := sanitizeBareStallDurations(exec, "/tmp/fake/config.yaml")
	captured := restore()

	if gotHealed := len(heals) > 0; gotHealed != tt.wantHeald {
		t.Errorf("sanitizeBareStallDurations(%q/%q) healed = %v, want %v", tt.timeout, tt.warn, gotHealed, tt.wantHeald)
	}
	requireBareHealRecords(t, heals)
	if exec.ActivityTimeout != tt.wantTime {
		t.Errorf("activity_timeout = %q, want %q", exec.ActivityTimeout, tt.wantTime)
	}
	if exec.ActivityWarnAfter != tt.wantWarn {
		t.Errorf("activity_warn_after = %q, want %q", exec.ActivityWarnAfter, tt.wantWarn)
	}
	requireHealWarnings(t, captured, tt.wantHeald)
}

// requireBareHealRecords asserts each bare-value heal names the layer it came
// from and both the bad and the corrected value, so the UI can show them.
func requireBareHealRecords(t *testing.T, heals []Heal) {
	t.Helper()
	for _, h := range heals {
		if h.Source != "/tmp/fake/config.yaml" {
			t.Errorf("heal Source = %q, want the layer path", h.Source)
		}
		if h.Bad == "" || h.Fixed == "" {
			t.Errorf("bare-value heal must name the bad and fixed values, got %+v", h)
		}
	}
}

// requireHealWarnings asserts the stderr warnings match the expectation: the
// heal names the file and the missing unit, or nothing is printed at all.
func requireHealWarnings(t *testing.T, captured string, wantHealed bool) {
	t.Helper()
	if !wantHealed {
		if captured != "" {
			t.Errorf("no heal expected, but stderr said:\n%s", captured)
		}
		return
	}
	for _, want := range []string{"has no time unit", "config.yaml"} {
		if !strings.Contains(captured, want) {
			t.Errorf("heal warning must contain %q, got:\n%s", want, captured)
		}
	}
}

// TestLoad_BareStallValuesHealed drives the full cascade with the reported
// broken home config (activity_timeout "60" / activity_warn_after "45" — the
// exact pair that killed startup): load must SUCCEED with the corrected
// values and warn on stderr naming the file, not refuse to start. The heal
// runs before the pair check, so bare values that form a LEGAL pair are not
// misreported as contradictory, while non-integer garbage stays an error.
func TestLoad_BareStallValuesHealed(t *testing.T) {
	tests := []loadHealCase{
		{
			name:       "reported pair (quoted bare values) healed",
			yaml:       "execution:\n  activity_timeout: \"60\"\n  activity_warn_after: \"30\"\n",
			wantTime:   "60s",
			wantWarn:   "30s",
			wantHealed: true,
		},
		{
			name:       "unquoted bare scalars healed the same way",
			yaml:       "execution:\n  activity_timeout: 60\n  activity_warn_after: 30\n",
			wantTime:   "60s",
			wantWarn:   "30s",
			wantHealed: true,
		},
		{
			name:     "bare window pinned, warn supplied by the cascade default",
			yaml:     "execution:\n  activity_timeout: 60\n",
			wantTime: "60s",
			wantWarn: "3m20s",
			// The layer's bare window is healed; the warn default (3m20s) is
			// supplied by the cascade after the merge.
			wantHealed: true,
		},
		{
			name:    "non-integer garbage falls back to defaults",
			yaml:    "execution:\n  activity_timeout: soon\n",
			wantErr: "activity_timeout",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkLoadHealCase(t, tt)
		})
	}
}

// loadHealCase mirrors one table row of TestLoad_BareStallValuesHealed.
type loadHealCase struct {
	name       string
	yaml       string
	wantTime   string
	wantWarn   string
	wantHealed bool
	wantErr    string
}

// checkLoadHealCase validates one full-cascade heal expectation (load
// outcome, corrected values, visible warning).
func checkLoadHealCase(t *testing.T, tt loadHealCase) {
	t.Helper()
	restore := captureStderr(t)
	cfg, rep, err := loadHomeLayerOnly(t, tt.yaml)
	captured := restore()

	if tt.wantErr != "" {
		requireFallbackNames(t, rep, err, tt.wantErr, tt.yaml)
		return
	}
	requireNoLoadError(t, err, tt.yaml)
	if cfg.Execution.ActivityTimeout != tt.wantTime {
		t.Errorf("activity_timeout = %q, want %q", cfg.Execution.ActivityTimeout, tt.wantTime)
	}
	if cfg.Execution.ActivityWarnAfter != tt.wantWarn {
		t.Errorf("activity_warn_after = %q, want %q", cfg.Execution.ActivityWarnAfter, tt.wantWarn)
	}
	if tt.wantHealed {
		if !strings.Contains(captured, "has no time unit") || !strings.Contains(captured, "config.yaml") {
			t.Errorf("heal must warn on stderr naming the file, got:\n%s", captured)
		}
	} else if strings.Contains(captured, "has no time unit") {
		t.Errorf("no bare-value heal expected, but stderr said:\n%s", captured)
	}
}
