// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	_ "embed"

	"github.com/pijalu/goa/internal/embeddoc"
)

//go:embed goal.md
var goalDescription string

// GoalDescription returns the LLM-facing description for the unified goal tool.
// The SPDX license header is stripped: it must not consume LLM context.
func GoalDescription() string {
	doc, err := embeddoc.ParseDocument([]byte(goalDescription))
	if err != nil {
		return goalDescription
	}
	return doc.Body
}
