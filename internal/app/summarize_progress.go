// SPDX-License-Identifier: GPL-3.0-or-later

package app

import "github.com/pijalu/goa/internal/agentic"

// handleSummarizeProgress consumes auxiliary model telemetry without updating
// the status bar, footer, or session token totals. Compression completion is
// reported by the compaction bubble.
func (a *App) handleSummarizeProgress(_ *agentic.OutputEvent) {}
