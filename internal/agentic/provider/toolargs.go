// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import "github.com/pijalu/goa/internal/agentic/provider/schema"

// SafeToolArguments re-exports schema.SafeToolArguments for legacy provider
// backends that have not migrated to the schema package yet.
func SafeToolArguments(args string) string {
	return schema.SafeToolArguments(args)
}
