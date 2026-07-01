// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package skills implements the skill system: discovery, loading, and
// execution. Skills are defined as SKILL.md files with YAML frontmatter
// organized in <name>/SKILL.md directories under this package.
//
// Built-in skills are embedded at build time using //go:embed.
// The SkillRegistry discovers them by scanning the embedded filesystem,
// same as it scans filesystem directories — no manual registration needed.
package skills

import (
	"embed"
)

//go:embed */SKILL.md
var EmbeddedSkillsFS embed.FS
