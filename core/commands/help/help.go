// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package help provides embedded long-help text for all slash commands.
package help

import (
	"embed"

	"github.com/pijalu/goa/internal/embeddoc"
)

//go:embed *.md
var helpDocs embed.FS

// LongHelp returns the detailed help text for a command by name. If no help
// file exists for the command, an empty string is returned.
func LongHelp(name string) string {
	if name == "" {
		return ""
	}
	return embeddoc.LoadText(helpDocs, name+".long.md")
}
