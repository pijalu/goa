// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package swarm

import (
	"strings"
	"testing"
)

// TestReminders_StripComments guards the system-prompt injection path: the
// embedded reminder files carry SPDX headers on disk, but they are prepended
// to the system prompt and comments must never reach the LLM context.
func TestReminders_StripComments(t *testing.T) {
	for name, text := range map[string]string{"enter": EnterReminder(), "exit": ExitReminder()} {
		if text == "" {
			t.Errorf("%s reminder is empty after stripping", name)
		}
		for _, banned := range []string{"SPDX-License-Identifier", "Copyright (C)", "<!--"} {
			if strings.Contains(text, banned) {
				t.Errorf("%s reminder contains %q — comments must be stripped", name, banned)
			}
		}
	}
}
