// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
	"github.com/pijalu/goa/internal/telemetry"
)

// TelemetryCommand handles /telemetry for managing anonymous telemetry.
type TelemetryCommand struct {
	Client *telemetry.Client
}

// Name returns the command name.
func (c *TelemetryCommand) Name() string { return "telemetry" }

// Aliases returns command aliases.
func (c *TelemetryCommand) Aliases() []string { return nil }

// ShortHelp returns a short description.
func (c *TelemetryCommand) ShortHelp() string { return "Manage anonymous telemetry" }

// LongHelp returns usage help.
func (c *TelemetryCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

// CompleteArgs provides argument completions for on/off. Only the value that
// changes the current state is offered; the matching value is filtered out.
func (c *TelemetryCommand) CompleteArgs(ctx core.Context, prefix string) []core.ArgCompletion {
	enabled := c.Client != nil && c.Client.Enabled()
	next := "off"
	desc := "disable telemetry"
	if !enabled {
		next = "on"
		desc = "enable telemetry"
	}
	if prefix != "" && !strings.HasPrefix(next, prefix) {
		return nil
	}
	return []core.ArgCompletion{{Value: next, Description: desc}}
}

// Run executes the command.
func (c *TelemetryCommand) Run(ctx core.Context, args []string) error {
	if c.Client == nil {
		return fmt.Errorf("telemetry client not configured")
	}
	if len(args) == 0 {
		if c.Client.Enabled() {
			ctx.Writef("Telemetry is enabled.\n")
		} else {
			ctx.Writef("Telemetry is disabled.\n")
		}
		return nil
	}
	switch strings.ToLower(args[0]) {
	case "on":
		c.Client.SetEnabled(true)
		ctx.Writef("Telemetry enabled.\n")
	case "off":
		c.Client.SetEnabled(false)
		ctx.Writef("Telemetry disabled.\n")
	default:
		return fmt.Errorf("usage: /telemetry:[on|off]")
	}
	return nil
}
