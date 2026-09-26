// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestValidateConfigBytes pins the writer-side backstop: marshaled bytes must
// parse AND carry no contradictory stall pair before any config write lands.
// Shape concerns (unparseable durations) are deliberately NOT rejected here —
// partial layer documents are legal and Config.Validate owns shapes.
func TestValidateConfigBytes(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr bool
		errPart string
	}{
		{
			name: "legal pair passes",
			body: "execution:\n  activity_timeout: 45s\n  activity_warn_after: 30s\n",
		},
		{
			name: "window only passes (half-set pair is legal)",
			body: "execution:\n  activity_timeout: 45s\n",
		},
		{
			name: "lead only passes (half-set pair is legal)",
			body: "execution:\n  activity_warn_after: 30s\n",
		},
		{
			name: "empty document passes",
			body: "",
		},
		{
			name:    "lead at the window is a contradiction",
			body:    "execution:\n  activity_timeout: 45s\n  activity_warn_after: 45s\n",
			wantErr: true,
			errPart: "activity_warn_after",
		},
		{
			name:    "lead beyond the window is a contradiction",
			body:    "execution:\n  activity_timeout: 45s\n  activity_warn_after: 1m\n",
			wantErr: true,
			errPart: "not shorter",
		},
		{
			name:    "unparseable yaml is refused",
			body:    "execution: [unclosed\n",
			wantErr: true,
			errPart: "does not parse",
		},
		{
			name: "unparseable durations are a shape concern, not a pair violation",
			body: "execution:\n  activity_timeout: soon\n  activity_warn_after: later\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateConfigBytes([]byte(tt.body), "home")
			if tt.wantErr != (err != nil) {
				t.Fatalf("validateConfigBytes(%q) error = %v, wantErr %v", tt.body, err, tt.wantErr)
			}
			if tt.wantErr && tt.errPart != "" && !strings.Contains(err.Error(), tt.errPart) {
				t.Fatalf("error %q does not mention %q", err, tt.errPart)
			}
		})
	}
}

// TestSaveHomeField_RejectsContradictoryPair: a field write that would leave
// the contradictory stall pair on disk must fail AND restore the previous
// bytes — goa never writes a config it cannot load (bugs.md).
func TestSaveHomeField_RejectsContradictoryPair(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	prev := "execution:\n  activity_timeout: 45s\n"
	cfgDir := filepath.Join(home, ".goa")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("seed config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(prev), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	cl := NewCascadeLoader(t.TempDir(), "", nil)

	// A stall-key write that would create the contradictory pair is refused
	// outright — nothing is written, so the previous bytes remain untouched.
	err := cl.SaveHomeField([]string{"execution", "activity_warn_after"}, "45s")
	if err == nil {
		t.Fatal("SaveHomeField accepted a lead equal to the window")
	}
	if !strings.Contains(err.Error(), "invalid") {
		t.Errorf("error %q does not say the written config is invalid", err)
	}
	raw, err := os.ReadFile(filepath.Join(cfgDir, "config.yaml"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(raw) != prev {
		t.Errorf("home config modified by the refused write:\n got %q\nwant %q", raw, prev)
	}

	// A legal lead still writes and loads back.
	if err := cl.SaveHomeField([]string{"execution", "activity_warn_after"}, "20s"); err != nil {
		t.Fatalf("SaveHomeField(20s): %v", err)
	}
	cfg, err := cl.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Execution.ActivityWarnAfter != "20s" || cfg.Execution.ActivityTimeout != "45s" {
		t.Errorf("loaded pair = (%s, %s), want (45s, 20s)",
			cfg.Execution.ActivityTimeout, cfg.Execution.ActivityWarnAfter)
	}
}

// assertHealedHomeFile verifies the healing write landed: new value present,
// stale lead gone, window kept, and the file still loads cleanly.
func assertHealedHomeFile(t *testing.T, cl *CascadeLoader, cfgDir string) {
	raw, err := os.ReadFile(filepath.Join(cfgDir, "config.yaml"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !strings.Contains(string(raw), "retries") {
		t.Errorf("retries missing after write:\n%s", raw)
	}
	if strings.Contains(string(raw), "activity_warn_after") {
		t.Errorf("stale lead survived the healing write:\n%s", raw)
	}
	if !strings.Contains(string(raw), "activity_timeout") {
		t.Errorf("window dropped by the healing write:\n%s", raw)
	}
	// Reload merges the cascade: with the home override dropped the lead
	// falls back to the shipped default 30s. What matters is the stale 45s
	// is gone from the FILE and the load succeeds.
	cfg, err := cl.Load()
	if err != nil {
		t.Errorf("healed config does not load: %v", err)
	}
	if cfg != nil && (cfg.Execution.Retries != 12 || cfg.Execution.ActivityTimeout != "45s" || cfg.Execution.ActivityWarnAfter == "45s") {
		t.Errorf("healed config = (retries %d, %s, lead %q), want (12, 45s, lead != 45s)",
			cfg.Execution.Retries, cfg.Execution.ActivityTimeout, cfg.Execution.ActivityWarnAfter)
	}
}

// TestSaveHomeField_HealsContradictoryPairOnUnrelatedWrite: the loader heals
// a hand-edited contradictory stall pair at startup — in memory only. If
// every unrelated field write then refused, a user with such a file could
// never persist a skill toggle or profile change. Unrelated writes therefore
// HEAL the pair exactly like the loader: the stale lead is dropped with a
// warning, and what lands on disk is valid.
func TestSaveHomeField_HealsContradictoryPairOnUnrelatedWrite(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	broken := "execution:\n  activity_timeout: 45s\n  activity_warn_after: 45s\n"
	cfgDir := filepath.Join(home, ".goa")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("seed config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(broken), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	cl := NewCascadeLoader(t.TempDir(), "", nil)
	// execution.retries is under the same execution section but is NOT a
	// stall key: the write must heal the pair and still land the new value.
	if err := cl.SaveHomeField([]string{"execution", "retries"}, 12); err != nil {
		t.Fatalf("unrelated write refused instead of healing: %v", err)
	}
	assertHealedHomeFile(t, cl, cfgDir)
}

// TestSave_RollsBackContradictoryConfig: a full home-config Save carrying the
// contradictory pair must be refused with the previous file restored byte for
// byte.
func TestSave_RollsBackContradictoryConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	cfgDir := filepath.Join(home, ".goa")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("seed config dir: %v", err)
	}

	cl := NewCascadeLoader(t.TempDir(), "", nil)
	cfg, err := cl.Load()
	if err != nil {
		t.Fatalf("load defaults: %v", err)
	}

	// First a legal save: establishes the previous-good bytes on disk.
	cfg.Execution.ActivityWarnAfter = "20s"
	if err := cl.Save(cfg); err != nil {
		t.Fatalf("legal Save: %v", err)
	}
	prev, err := os.ReadFile(cl.HomeConfigPath())
	if err != nil {
		t.Fatalf("read legal save: %v", err)
	}

	// Then the contradictory save: refused, previous bytes restored.
	cfg.Execution.ActivityWarnAfter = "45s"
	err = cl.Save(cfg)
	if err == nil {
		t.Fatal("Save accepted a lead equal to the window")
	}
	if !strings.Contains(err.Error(), "restored") {
		t.Errorf("error %q does not say the previous home config was restored", err)
	}
	raw, err := os.ReadFile(cl.HomeConfigPath())
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(raw) != string(prev) {
		t.Errorf("home config not restored byte for byte (got %d bytes, want %d)", len(raw), len(prev))
	}
}
