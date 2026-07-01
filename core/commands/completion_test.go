// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"strings"
	"testing"
)

func TestProfileCompletionValues(t *testing.T) {
	ctx := newModeTestContext()
	vals := profileCompletionValues(ctx, "")
	if len(vals) == 0 {
		t.Error("expected at least one profile completion value")
	}
}

func TestModeCompletionValues(t *testing.T) {
	vals := modeCompletionValues("")
	if len(vals) == 0 {
		t.Error("expected at least one mode completion value")
	}
}

func TestThemeCompletionValues(t *testing.T) {
	vals := themeCompletionValues("")
	if len(vals) == 0 {
		t.Error("expected at least one theme completion value")
	}
}

func TestBoolCompletionValues(t *testing.T) {
	vals := boolCompletionValues("")
	if len(vals) != 2 {
		t.Errorf("expected 2 bool values (true/false), got %d", len(vals))
	}
}

func TestThinkingLevelCompletionValues(t *testing.T) {
	vals := thinkingLevelCompletionValues("")
	if len(vals) == 0 {
		t.Error("expected at least one thinking level value")
	}
}

func TestProfileCompletionValues_PrefixFilter(t *testing.T) {
	ctx := newModeTestContext()
	vals := profileCompletionValues(ctx, "no")
	for _, v := range vals {
		if !strings.HasPrefix(v.Value, "no") {
			t.Errorf("expected all values to start with 'no', got %q", v.Value)
		}
	}
}

func TestConfigCommand_IsInternal(t *testing.T) {
	cmd := &ConfigCommand{}
	if !cmd.IsInternal() {
		t.Error("expected ConfigCommand to be internal")
	}
}
