// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

// stickySkillProvider adapts the skill registry to agentic.StickyProvider:
// it renders each sticky knowledge skill as a labelled, byte-stable
// instruction block. The agent persists the blocks into conversation history
// once per content change (deduped, user-role) and re-persists them after
// any context compression that may have dropped them.
//
// The provider holds the subsystems (not a registry copy): ReloadSkills
// replaces the live registry object, and a provider pinned to the old one
// would keep serving a stale sticky set after a /skill:sticky toggle.
type stickySkillProvider struct {
	subs *subsystems
}

// StickyInstructions implements agentic.StickyProvider.
func (p *stickySkillProvider) StickyInstructions() []string {
	if p.subs == nil || p.subs.skillRegistry == nil {
		return nil
	}
	return p.subs.skillRegistry.StickyBodies()
}
