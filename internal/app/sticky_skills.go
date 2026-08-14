// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"github.com/pijalu/goa/skills"
)

// stickySkillProvider adapts the skill registry to agentic.StickyProvider:
// it renders each sticky knowledge skill as a labelled, byte-stable
// instruction block. The agent persists the blocks into conversation history
// once per content change (deduped, user-role) and re-persists them after
// any context compression that may have dropped them.
type stickySkillProvider struct {
	registry *skills.SkillRegistry
}

// StickyInstructions implements agentic.StickyProvider.
func (p *stickySkillProvider) StickyInstructions() []string {
	if p.registry == nil {
		return nil
	}
	return p.registry.StickyBodies()
}
