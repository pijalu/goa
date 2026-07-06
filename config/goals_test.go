// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"strings"
	"testing"
)

func TestGoalsConfigValidate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     GoalsConfig
		wantSub string
	}{
		{
			name: "empty is valid",
			cfg:  GoalsConfig{},
		},
		{
			name: "zero retention days accepted",
			cfg: GoalsConfig{
				Retention: GoalsRetentionConfig{Enabled: true, Days: 0},
			},
		},
		{
			name: "negative retention days rejected",
			cfg: GoalsConfig{
				Retention: GoalsRetentionConfig{Enabled: true, Days: -1},
			},
			wantSub: "goals.retention.days",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{Goals: tc.cfg}
			err := c.Validate()
			if tc.wantSub == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error %q does not contain %q", errOrEmpty(err), tc.wantSub)
			}
		})
	}
}
