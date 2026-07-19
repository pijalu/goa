// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
)

// PTYCommand manages PTY sessions: list, kill, read, write.
// Usage: /pty[:ps|:kill:<id>|:read:<id>[:N[:ansi]]|:write:<id>:<text>]
type PTYCommand struct{}

func (c *PTYCommand) Name() string      { return "pty" }
func (c *PTYCommand) Aliases() []string { return []string{} }
func (c *PTYCommand) ShortHelp() string { return "Manage PTY sessions (list, kill, read, write)" }
func (c *PTYCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

func (c *PTYCommand) CompleteArgs(ctx core.Context, prefix string) []core.ArgCompletion {
	parts := strings.SplitN(prefix, ":", 2)
	sub := ""
	if len(parts) > 1 {
		sub = parts[0]
	}

	var comps []core.ArgCompletion
	for _, v := range []struct{ val, desc string }{
		{"ps", "list active sessions"},
		{"kill:", "terminate a session"},
		{"read:", "read session output"},
		{"monitor:", "live monitor overlay"},
		{"write:", "send input to session"},
	} {
		if sub == "" || strings.HasPrefix(v.val, sub) {
			comps = append(comps, core.ArgCompletion{Value: v.val, Description: v.desc})
		}
	}
	// Complete session IDs for kill:/read:
	if (sub == "kill:" || sub == "read:" || sub == "write:") && ctx.PTYManager != nil {
		for _, s := range ctx.PTYManager.List() {
			comps = append(comps, core.ArgCompletion{
				Value:       sub + s.ID,
				Description: fmt.Sprintf("%s (PID %d)", s.Command, s.PID),
			})
		}
	}
	return comps
}

func (c *PTYCommand) Run(ctx core.Context, args []string) error {
	if ctx.PTYManager == nil {
		writeStr(ctx, "PTY manager not available.\n")
		return nil
	}
	if len(args) == 0 {
		return ptyListSessions(ctx)
	}
	sub := args[0]
	if handler, ok := ptySubcommandHandlers[sub]; ok {
		return handler(ctx, sub, args)
	}
	if strings.HasPrefix(sub, "kill:") {
		return killSession(ctx, strings.TrimPrefix(sub, "kill:"))
	}
	if strings.HasPrefix(sub, "read:") {
		return readSession(ctx, strings.TrimPrefix(sub, "read:"), args[1:])
	}
	if strings.HasPrefix(sub, "monitor:") {
		return monitorSession(ctx, strings.TrimPrefix(sub, "monitor:"))
	}
	if strings.HasPrefix(sub, "write:") {
		return writeSession(ctx, strings.TrimPrefix(sub, "write:"))
	}
	return fmt.Errorf("unknown pty subcommand: %s (use ps, kill:<id>, read:<id>, or write:<id>:<text>)", sub)
}

var ptySubcommandHandlers = map[string]func(core.Context, string, []string) error{
	"ps":   func(ctx core.Context, _ string, _ []string) error { return ptyListSessions(ctx) },
	"list": func(ctx core.Context, _ string, _ []string) error { return ptyListSessions(ctx) },
	"kill": func(ctx core.Context, _ string, _ []string) error {
		writeStr(ctx, "Usage: /pty:kill:<session-id>\n")
		return nil
	},
	"read": func(ctx core.Context, _ string, _ []string) error {
		writeStr(ctx, "Usage: /pty:read:<session-id>[:<lines>[:ansi]]\n")
		return nil
	},
	"monitor": func(ctx core.Context, _ string, _ []string) error {
		writeStr(ctx, "Usage: /pty:monitor:<session-id>\n")
		return nil
	},
	"write": func(ctx core.Context, _ string, _ []string) error {
		writeStr(ctx, "Usage: /pty:write:<session-id>:<text>\n")
		return nil
	},
}

func ptyListSessions(ctx core.Context) error {
	sessions := ctx.PTYManager.List()
	if len(sessions) == 0 {
		writeStr(ctx, "No active PTY sessions.\n")
		return nil
	}
	writeFmt(ctx, "%d active PTY session(s):\n\n", len(sessions))
	for _, s := range sessions {
		status := "● running"
		if !s.Running {
			status = "○ stopped"
		}
		writeFmt(ctx, "  %s  %s  PID %d  %s\n", status, s.ID, s.PID, s.Command)
	}
	return nil
}

func killSession(ctx core.Context, id string) error {
	if id == "" {
		writeStr(ctx, "Usage: /pty:kill:<session-id>\n")
		return nil
	}
	if err := ctx.PTYManager.Stop(id); err != nil {
		writeFmt(ctx, "Error: %v\n", err)
		return nil
	}
	writeFmt(ctx, "Session %s terminated.\n", id)
	return nil
}

func readSession(ctx core.Context, spec string, extraArgs []string) error {
	if spec == "" {
		writeStr(ctx, "Usage: /pty:read:<session-id>[:<lines>[:ansi]]\n")
		return nil
	}
	parts := strings.SplitN(spec, ":", 3)
	id := parts[0]
	tail := 100
	keepANSI := false
	if len(parts) >= 2 && parts[1] != "" {
		if n, err := fmt.Sscanf(parts[1], "%d", &tail); err != nil || n != 1 {
			tail = 100
		}
	}
	if len(parts) >= 3 && parts[2] == "ansi" {
		keepANSI = true
	}

	output, err := ctx.PTYManager.Read(id, tail)
	if err != nil {
		writeFmt(ctx, "Error: %v\n", err)
		return nil
	}
	if output == "" {
		writeFmt(ctx, "Session %s: (no output available)\n", id)
		return nil
	}
	writeFmt(ctx, "Session %s (last %d lines):\n", id, tail)
	if !keepANSI {
		// Strip ANSI by default for readability
		writeStr(ctx, stripANSIEscape(output))
	} else {
		writeStr(ctx, output)
	}
	writeStr(ctx, "\n")
	return nil
}

func monitorSession(ctx core.Context, id string) error {
	if id == "" {
		writeStr(ctx, "Usage: /pty:monitor:<session-id>\n")
		return nil
	}
	if ctx.ShowPTYOverlay == nil {
		writeStr(ctx, "PTY monitor not available (no TUI reference).\n")
		return nil
	}
	ctx.ShowPTYOverlay(id)
	writeFmt(ctx, "Monitoring session %s. Press ctrl+c or Escape to close.\n", id)
	return nil
}

func writeSession(ctx core.Context, spec string) error {
	if spec == "" {
		writeStr(ctx, "Usage: /pty:write:<session-id>:<text>\n")
		return nil
	}
	parts := strings.SplitN(spec, ":", 2)
	if len(parts) < 2 || parts[1] == "" {
		writeFmt(ctx, "Usage: /pty:write:<session-id>:<text>\n")
		return nil
	}
	id := parts[0]
	text := parts[1]
	if err := ctx.PTYManager.Write(id, text+"\n"); err != nil {
		writeFmt(ctx, "Error: %v\n", err)
		return nil
	}
	writeFmt(ctx, "Sent %d bytes to session %s.\n", len(text), id)
	return nil
}

// stripANSIEscape removes ANSI escape sequences from a string.
func stripANSIEscape(s string) string {
	var result strings.Builder
	inEscape := false
	for _, c := range s {
		if c == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '~' {
				inEscape = false
			}
			continue
		}
		result.WriteRune(c)
	}
	return result.String()
}
