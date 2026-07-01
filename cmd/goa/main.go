// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Goa — terminal-native AI coding agent.
package main

import (
	"github.com/pijalu/goa/internal/app"

	// Register LLM provider backends.
	_ "github.com/pijalu/goa/internal/agentic/provider/anthropic"
	_ "github.com/pijalu/goa/internal/agentic/provider/bedrock"
	_ "github.com/pijalu/goa/internal/agentic/provider/google"
	_ "github.com/pijalu/goa/internal/agentic/provider/mistral"
	_ "github.com/pijalu/goa/internal/agentic/provider/openai"
)

func main() {
	app.Main()
}
