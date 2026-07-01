// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package common

import (
	"embed"

	"github.com/pijalu/goa/internal/embeddoc"
)

// ReadDoc reads a doc file from an embedded filesystem and returns its
// trimmed content. Used by ShortDoc() and LongDoc() methods across tools.
func ReadDoc(fs embed.FS, name string) string {
	return embeddoc.LoadText(fs, name)
}
