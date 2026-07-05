// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"testing"

	"github.com/pijalu/goa/core"
)

func TestConfigSubcommandCompletions_IncludesTemp(t *testing.T) {
	cases := []struct {
		prefix   string
		wantTemp bool
	}{
		{"", true},
		{"t", true},
		{"te", true},
		{"temp", true},
		{"s", false},
		{"set", false},
	}
	for _, c := range cases {
		got := configSubcommandCompletions(c.prefix)
		var found bool
		for _, comp := range got {
			if comp.Value == "temp" {
				found = true
				break
			}
		}
		if found != c.wantTemp {
			t.Errorf("prefix %q: temp found=%v, want=%v", c.prefix, found, c.wantTemp)
		}
	}
}

func TestConfigTempCompletions(t *testing.T) {
	enabledCtx := core.Context{LoopDetector: core.NewLoopDetector(core.DefaultLoopDetectorConfig())}
	disabledCtx := core.Context{LoopDetector: core.NewLoopDetector(core.DefaultLoopDetectorConfig())}
	disabledCtx.LoopDetector.SetTempOverride("think", true)
	disabledCtx.LoopDetector.SetTempOverride("tool", true)

	cases := []struct {
		name                       string
		ctx                        core.Context
		settingPrefix, valuePrefix string
		want                       []string
	}{
		{"both enabled", enabledCtx, "", "", []string{"temp:think_loop_detection:off", "temp:tool_loop_detection:off"}},
		{"think enabled", enabledCtx, "think", "", []string{"temp:think_loop_detection:off"}},
		{"value filter off", enabledCtx, "", "o", []string{"temp:think_loop_detection:off", "temp:tool_loop_detection:off"}},
		{"tool value of", enabledCtx, "tool", "of", []string{"temp:tool_loop_detection:off"}},
		{"both disabled", disabledCtx, "", "", []string{"temp:think_loop_detection:on", "temp:tool_loop_detection:on"}},
		{"think disabled", disabledCtx, "think", "", []string{"temp:think_loop_detection:on"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := configTempCompletions(c.ctx, c.settingPrefix, c.valuePrefix)
			gotVals := completionsToValues(got)
			if !sameSet(gotVals, c.want) {
				t.Errorf("configTempCompletions(%q, %q) = %v, want %v", c.settingPrefix, c.valuePrefix, gotVals, c.want)
			}
		})
	}
}

func TestConfigTempArgCompletions(t *testing.T) {
	enabledCtx := core.Context{LoopDetector: core.NewLoopDetector(core.DefaultLoopDetectorConfig())}

	cases := []struct {
		prefix string
		want   []string
	}{
		{"temp ", []string{"temp:think_loop_detection:off", "temp:tool_loop_detection:off"}},
		{"temp:think", []string{"temp:think_loop_detection:off"}},
		{"temp:think_loop_detection ", []string{"temp:think_loop_detection:off"}},
		{"temp:think_loop_detection:of", []string{"temp:think_loop_detection:off"}},
		{"temp:tool:of", []string{"temp:tool_loop_detection:off"}},
		{"te", []string{"temp:think_loop_detection:off", "temp:tool_loop_detection:off"}},
	}
	cmd := &ConfigCommand{}
	for _, c := range cases {
		got := cmd.CompleteArgs(enabledCtx, c.prefix)
		gotVals := completionsToValues(got)
		if !sameSet(gotVals, c.want) {
			t.Errorf("CompleteArgs(%q) = %v, want %v", c.prefix, gotVals, c.want)
		}
	}
}

func TestConfigTempArgCompletions_DisabledState(t *testing.T) {
	disabledCtx := core.Context{LoopDetector: core.NewLoopDetector(core.DefaultLoopDetectorConfig())}
	disabledCtx.LoopDetector.SetTempOverride("think", true)

	cases := []struct {
		prefix string
		want   []string
	}{
		{"temp ", []string{"temp:think_loop_detection:on", "temp:tool_loop_detection:off"}},
		{"temp:think", []string{"temp:think_loop_detection:on"}},
		{"temp:think_loop_detection:of", []string{}},
	}
	cmd := &ConfigCommand{}
	for _, c := range cases {
		got := cmd.CompleteArgs(disabledCtx, c.prefix)
		gotVals := completionsToValues(got)
		if !sameSet(gotVals, c.want) {
			t.Errorf("CompleteArgs(%q) = %v, want %v", c.prefix, gotVals, c.want)
		}
	}
}

func TestConfigTempArgCompletions_NoTemp(t *testing.T) {
	cmd := &ConfigCommand{}
	cases := []struct {
		prefix string
	}{
		{"s"},
		{"set:active"},
	}
	for _, c := range cases {
		got := cmd.CompleteArgs(core.Context{}, c.prefix)
		for _, comp := range got {
			if comp.Value == "temp" {
				t.Errorf("non-temp prefix %q should not return temp completion", c.prefix)
			}
		}
	}
}
