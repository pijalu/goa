// SPDX-License-Identifier: GPL-3.0-or-later
package commands

import (
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
	"github.com/pijalu/goa/internal/event"
)

type SetupCommand struct{}

func (c *SetupCommand) Name() string      { return "setup" }
func (c *SetupCommand) Aliases() []string { return []string{} }
func (c *SetupCommand) IsInternal() bool  { return true }
func (c *SetupCommand) ShortHelp() string { return "Launch the setup wizard" }
func (c *SetupCommand) LongHelp() string  { return help.LongHelp(c.Name()) }
func (c *SetupCommand) Run(ctx core.Context, args []string) error {
	ctx.ControlEvent(event.ControlEvent{RunWizard: true})
	writeStr(ctx, "Launching setup wizard...\n")
	return nil
}
