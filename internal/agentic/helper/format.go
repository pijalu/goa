// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package helper

import (
	"github.com/pijalu/goa/internal/agentic"
)

// FormatFunc returns the prefix string to emit when entering a given state.
type FormatFunc func(state agentic.OutputState) string

// DefaultFormat returns the standard prefix format.
func DefaultFormat() FormatFunc {
	return func(state agentic.OutputState) string {
		switch state {
		case agentic.StateThinking:
			return "[thinking] "
		case agentic.StateContent:
			return "[content] "
		case agentic.StateToolResult:
			return "[tool_result] "
		case agentic.StateToolCall:
			return "" // handled specially in OnEvent
		default:
			return ""
		}
	}
}

// rolePrefix returns a prefix based on the message role for readable output.
func rolePrefix(role agentic.Role) string {
	switch role {
	case agentic.System:
		return "[system] "
	case agentic.User:
		return "[user] "
	case agentic.Assistant:
		return "" // Assistant content uses state-based prefix from DefaultFormat
	default:
		return ""
	}
}
