// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import _ "embed"

//go:embed goal.md
var goalDescription string

// GoalDescription returns the LLM-facing description for the unified goal tool.
func GoalDescription() string { return goalDescription }
