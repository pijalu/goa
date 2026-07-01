// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package swarm

import _ "embed"

// enterReminder is injected into the system prompt while swarm mode is active
// for a manual/task trigger. Ported from kimi-code
// (packages/agent-core/src/agent/swarm/enter-reminder.md). The tool trigger
// omits this reminder because the model is already calling agent_swarm.
//
//go:embed enter_reminder.md
var enterReminder string

// exitReminder is injected once when swarm mode ends after a manual/task
// trigger, so the model does not keep applying the swarm workflow to ordinary
// follow-up requests. Ported from kimi-code
// (packages/agent-core/src/agent/swarm/exit-reminder.md).
//
//go:embed exit_reminder.md
var exitReminder string

// EnterReminder returns the swarm-mode enter reminder text. It is the text the
// reminder provider prepends to the system prompt each turn while swarm mode
// is active under a manual or task trigger.
func EnterReminder() string { return enterReminder }

// ExitReminder returns the one-shot reminder emitted when swarm mode ends.
func ExitReminder() string { return exitReminder }
