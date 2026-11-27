// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func idleTestInt(v int) *int { return &v }

func TestIdleSummarizePerModelYAMLAndDeepCopy(t *testing.T) {
	var cfg Config
	err := yaml.Unmarshal([]byte(`
context_compression:
  per_model:
    local-fast:
      idle_summarize:
        trigger_percent: 0
        delay_seconds: 0
`), &cfg)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	ov := cfg.ContextCompression.PerModel["local-fast"].IdleSummarize
	if ov.TriggerPercent == nil || *ov.TriggerPercent != 0 || ov.DelaySeconds == nil || *ov.DelaySeconds != 0 {
		t.Fatalf("per-model idle settings = %+v, want two explicit zeroes", ov)
	}

	copied := cfg.DeepCopy()
	*copied.ContextCompression.PerModel["local-fast"].IdleSummarize.TriggerPercent = 35
	*copied.ContextCompression.PerModel["local-fast"].IdleSummarize.DelaySeconds = 90
	if *ov.TriggerPercent != 0 || *ov.DelaySeconds != 0 {
		t.Fatalf("deep copy aliases per-model idle pointers: original = %+v", ov)
	}
}

func TestMergeCompressionPerModelIdleSummarize(t *testing.T) {
	base := &Config{ContextCompression: ContextCompressionConfig{
		PerModel: map[string]ModelCompressionOverride{
			"local-fast": {IdleSummarize: IdleSummarizeSettings{
				TriggerPercent: idleTestInt(20), DelaySeconds: idleTestInt(30),
			}},
		},
	}}
	overlay := &Config{ContextCompression: ContextCompressionConfig{
		PerModel: map[string]ModelCompressionOverride{
			"local-fast": {IdleSummarize: IdleSummarizeSettings{TriggerPercent: idleTestInt(0)}},
		},
	}}
	base.DeepMerge(overlay)
	got := base.ContextCompression.PerModel["local-fast"].IdleSummarize
	if got.TriggerPercent == nil || *got.TriggerPercent != 0 {
		t.Fatalf("TriggerPercent = %v, want explicit zero from higher layer", got.TriggerPercent)
	}
	if got.DelaySeconds == nil || *got.DelaySeconds != 30 {
		t.Fatalf("DelaySeconds = %v, want inherited lower-layer value 30", got.DelaySeconds)
	}
}

func TestValidateIdleSummarizePerModelEvenWhenCompressionDisabled(t *testing.T) {
	disabled := false
	for _, tc := range []struct {
		name     string
		settings IdleSummarizeSettings
		want     string
	}{
		{"trigger range", IdleSummarizeSettings{TriggerPercent: idleTestInt(101)}, "idle_summarize.trigger_percent"},
		{"negative delay", IdleSummarizeSettings{DelaySeconds: idleTestInt(-1)}, "idle_summarize.delay_seconds"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{
				Models: []ModelConfig{{ID: "local-fast"}},
				ContextCompression: ContextCompressionConfig{
					Enabled: &disabled,
					PerModel: map[string]ModelCompressionOverride{
						"local-fast": {IdleSummarize: tc.settings},
					},
				},
			}
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), "context_compression.per_model.local-fast."+tc.want) {
				t.Fatalf("Validate() = %v, want per-model path containing %q", err, tc.want)
			}
		})
	}
}
