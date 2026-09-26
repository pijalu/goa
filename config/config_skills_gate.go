// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

// SkillGateLists partitions the merged skills gate for the skill registry.
//
// A skills.enabled list that came solely from the embedded shipped defaults
// is embedded-scoped: it opts the shipped-on built-in (telegram) in WITHOUT
// activating the global allowlist, which gates every source and would
// silently suppress the user's home/project/plugin file skills. It is merged
// with the explicit embedded opt-ins (skills.embedded_enabled).
//
// An explicit skills.enabled pin in any config layer is a real allowlist: it
// applies to every source exactly as documented on SkillsConfig.Enabled.
func (s SkillsConfig) SkillGateLists() (allowlist, embeddedScoped []string) {
	if len(s.Enabled) == 0 || !s.EnabledFromDefaults {
		return s.Enabled, s.EmbeddedEnabled
	}
	combined := make([]string, 0, len(s.EmbeddedEnabled)+len(s.Enabled))
	combined = append(combined, s.EmbeddedEnabled...)
	combined = append(combined, s.Enabled...)
	return nil, uniqueStrings(combined)
}
