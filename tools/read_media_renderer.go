// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/internal/tuirender"
)

// ReadMediaFileRenderer renders read_media_file calls and results.
type ReadMediaFileRenderer struct{}

var _ tuirender.ToolRenderer = (*ReadMediaFileRenderer)(nil)

func (r *ReadMediaFileRenderer) RenderCall(args map[string]any, ctx tuirender.RenderContext) string {
	path := stringArg(args, "path")
	if path == "" {
		path = "media file"
	}
	return fmt.Sprintf("🖼️ read media: %s", path)
}

func (r *ReadMediaFileRenderer) RenderResult(output string, ctx tuirender.RenderContext) string {
	if ctx.Expanded {
		return output
	}
	// Truncate base64 data in collapsed view.
	if idx := strings.Index(output, "Data: data:"); idx != -1 {
		return output[:idx] + "Data: <base64 embedded media>"
	}
	return output
}

func (r *ReadMediaFileRenderer) PreviewLines() int             { return 3 }
func (r *ReadMediaFileRenderer) HideResultWhenCollapsed() bool { return false }
