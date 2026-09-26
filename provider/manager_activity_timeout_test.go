// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/stretchr/testify/assert"
)

// activityTimeoutConfig builds a config with the given execution
// activity_timeout and optional provider idle_timeout.
func activityTimeoutConfig(activity, providerIdle string) *config.Config {
	return &config.Config{
		ActiveProvider: "openai",
		Execution:      config.ExecutionConfig{ActivityTimeout: activity},
		Providers: []config.ProviderConfig{
			{
				ID:          "openai",
				Endpoint:    "https://api.openai.com/v1",
				APIKey:      "key",
				IdleTimeout: providerIdle,
			},
		},
	}
}

// TestBuildStreamOptions_ActivityTimeoutIsConsumed is the F4 regression test:
// execution.activity_timeout used to be validated and merged but read nowhere,
// so a user configuring "30s" got no stall protection at all. It must become
// the stream idle/stall timeout when the provider declares none.
func TestBuildStreamOptions_ActivityTimeoutIsConsumed(t *testing.T) {
	tests := []struct {
		name         string
		activity     string
		providerIdle string
		wantIdle     time.Duration
	}{
		{name: "activity timeout applies when provider idle unset", activity: "30s", providerIdle: "", wantIdle: 30 * time.Second},
		{name: "provider idle_timeout wins over activity timeout", activity: "30s", providerIdle: "7m", wantIdle: 7 * time.Minute},
		{name: "no activity timeout leaves idle unset (2m default downstream)", activity: "", providerIdle: "", wantIdle: 0},
		{name: "invalid activity timeout ignored", activity: "not-a-duration", providerIdle: "", wantIdle: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pm := NewProviderManager(activityTimeoutConfig(tt.activity, tt.providerIdle))
			opts := pm.BuildStreamOptions()
			assert.Equal(t, tt.wantIdle, opts.IdleTimeout)
		})
	}
}

// TestBuildStreamOptions_ActivityWarnAfterIsConsumed pins the wiring of
// execution.activity_warn_after into StreamOptions.ActivityWarnAfter: the agent
// needs it to arm the stall warning at the configured lead (30s of the shipped
// 45s window). An unset or invalid value must leave 0 so the agent falls back to
// two thirds of the effective window.
func TestBuildStreamOptions_ActivityWarnAfterIsConsumed(t *testing.T) {
	tests := []struct {
		name     string
		warn     string
		wantWarn time.Duration
	}{
		{name: "configured lead is consumed", warn: "30s", wantWarn: 30 * time.Second},
		{name: "custom lead is consumed", warn: "10s", wantWarn: 10 * time.Second},
		{name: "unset leaves zero (agent derives 2/3)", warn: "", wantWarn: 0},
		{name: "invalid leaves zero (agent derives 2/3)", warn: "soon", wantWarn: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := activityTimeoutConfig("45s", "")
			cfg.Execution.ActivityWarnAfter = tt.warn

			opts := NewProviderManager(cfg).BuildStreamOptions()
			assert.Equal(t, tt.wantWarn, opts.ActivityWarnAfter)
			assert.Equal(t, 45*time.Second, opts.IdleTimeout,
				"the warning key must not disturb the retry window")
		})
	}
}
