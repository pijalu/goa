// SPDX-License-Identifier: GPL-3.0-or-later

package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
)

func TestConfigSettersIdleSummarizeMaterializeExplicitZero(t *testing.T) {
	cfg := &config.Config{}
	for _, tc := range []struct {
		key   string
		value string
		get   func() (int, bool)
	}{
		{
			key: "context_compression.idle_summarize.trigger_percent", value: "0",
			get: func() (int, bool) {
				v := cfg.ContextCompression.IdleSummarize.TriggerPercent
				if v == nil {
					return 0, false
				}
				return *v, true
			},
		},
		{
			key: "context_compression.idle_summarize.delay_seconds", value: "0",
			get: func() (int, bool) {
				v := cfg.ContextCompression.IdleSummarize.DelaySeconds
				if v == nil {
					return 0, false
				}
				return *v, true
			},
		},
	} {
		setter, ok := configSetters[tc.key]
		if !ok {
			t.Fatalf("config setter %q is not registered", tc.key)
		}
		if err := setter(cfg, tc.value); err != nil {
			t.Fatalf("setter(%s, %s): %v", tc.key, tc.value, err)
		}
		got, materialized := tc.get()
		if !materialized || got != 0 {
			t.Fatalf("%s = %d (materialized %v), want explicit zero", tc.key, got, materialized)
		}
	}
}

func TestIdleSummarizeNumericPersistence(t *testing.T) {
	for _, tc := range []struct {
		key   string
		value string
		yaml  string
	}{
		{"context_compression.idle_summarize.delay_seconds", "0", "delay_seconds: 0"},
		{"context_compression.idle_summarize.trigger_percent", "1", "trigger_percent: 1"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			assertIdleNumericPersistence(t, tc.key, tc.value, tc.yaml)
		})
	}
}

func assertIdleNumericPersistence(t *testing.T, key, value, wantYAML string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("GOA_HOME", home)
	loader := config.NewCascadeLoader(t.TempDir(), "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := setConfigField(cfg, strings.Split(key, "."), value); err != nil {
		t.Fatalf("setConfigField: %v", err)
	}
	ctx := core.Context{Config: cfg, ConfigSaver: loader}
	if err := persistConfigValue(ctx, key, strings.Split(key, "."), value); err != nil {
		t.Fatalf("persistConfigValue(%s): %v", value, err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".goa", "config.yaml"))
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	if !strings.Contains(string(data), wantYAML) {
		t.Fatalf("saved YAML does not contain numeric %q:\n%s", wantYAML, data)
	}
	if _, err := loader.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
}

func TestConfigSettersIdleSummarizeValidation(t *testing.T) {
	for _, tc := range []struct {
		key   string
		value string
		valid bool
	}{
		{"context_compression.idle_summarize.trigger_percent", "10", true},
		{"context_compression.idle_summarize.trigger_percent", "101", false},
		{"context_compression.idle_summarize.trigger_percent", "-1", false},
		{"context_compression.idle_summarize.delay_seconds", "240", true},
		{"context_compression.idle_summarize.delay_seconds", "-1", false},
	} {
		cfg := &config.Config{}
		setter, ok := configSetters[tc.key]
		if !ok {
			t.Fatalf("config setter %q is not registered", tc.key)
		}
		err := setter(cfg, tc.value)
		if (err == nil) != tc.valid {
			t.Errorf("setter(%s, %s) error = %v, valid = %v", tc.key, tc.value, err, tc.valid)
		}
	}
}
