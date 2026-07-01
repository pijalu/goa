// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package swarm

import (
	"embed"
	"strings"
)

// readDoc reads a doc file from an embedded filesystem and returns its
// trimmed content. Used by ShortDoc() and LongDoc() methods.
func readDoc(fs embed.FS, name string) string {
	data, err := fs.ReadFile(name)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
