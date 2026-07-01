// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
	"github.com/pijalu/goa/tui"
)

// HotkeysCommand shows all keyboard shortcuts as a grouped table.
type HotkeysCommand struct{}

func (c *HotkeysCommand) Name() string      { return "hotkeys" }
func (c *HotkeysCommand) Aliases() []string { return []string{} }
func (c *HotkeysCommand) ShortHelp() string {
	return "Show all keyboard shortcuts"
}
func (c *HotkeysCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

// Run writes the hotkey table to the command output.
func (c *HotkeysCommand) Run(ctx core.Context, args []string) error {
	writeStr(ctx, hotkeysTable())
	return nil
}

// hotkeysGroup is one section of the /hotkeys output.
type hotkeysGroup struct {
	title string
	// entries maps a keybinding name (from tui.Kb* constants) to a human
	// description. The key display is derived from DefaultKeybindings().
	entries []hotkeysEntry
}

type hotkeysEntry struct {
	name string // keybinding name, or "" for a raw key
	key  string // raw key display when name is ""
	desc string
}

// hotkeysGroups returns the ordered, curated list of shortcut groups shown by
// /hotkeys. Kept table-driven to stay under the complexity budget.
func hotkeysGroups() []hotkeysGroup {
	return []hotkeysGroup{
		{
			title: "Navigation",
			entries: []hotkeysEntry{
				{name: tui.KbCursorUp, desc: "Move cursor / browse history up"},
				{name: tui.KbCursorDown, desc: "Move cursor / browse history down"},
				{name: tui.KbCursorLeft, desc: "Move cursor left"},
				{name: tui.KbCursorRight, desc: "Move cursor right"},
				{name: tui.KbCursorWordLeft, desc: "Move cursor by word left"},
				{name: tui.KbCursorWordRight, desc: "Move cursor by word right"},
				{name: tui.KbCursorLineStart, desc: "Move to start of line"},
				{name: tui.KbCursorLineEnd, desc: "Move to end of line"},
				{name: tui.KbPageUp, desc: "Scroll up by page"},
				{name: tui.KbPageDown, desc: "Scroll down by page"},
			},
		},
		{
			title: "Editing",
			entries: []hotkeysEntry{
				{name: tui.KbSubmit, desc: "Send message / submit input"},
				{name: tui.KbNewLine, desc: "Insert a newline"},
				{name: tui.KbTab, desc: "Accept completion / path completion"},
				{name: tui.KbDeleteWordBack, desc: "Delete word backwards"},
				{name: tui.KbDeleteWordFwd, desc: "Delete word forwards"},
				{name: tui.KbDeleteLineStart, desc: "Delete to start of line"},
				{name: tui.KbDeleteLineEnd, desc: "Delete to end of line"},
				{name: tui.KbYank, desc: "Paste most-recently deleted text"},
				{name: tui.KbYankPop, desc: "Cycle through deleted text after pasting"},
				{name: tui.KbUndo, desc: "Undo"},
			},
		},
		{
			title: "Application",
			entries: []hotkeysEntry{
				{key: "ctrl+g", desc: "Toggle the goal bubble"},
				{name: tui.KbCycleThinkingLevel, desc: "Cycle thinking level"},
				{name: tui.KbChangeMode, desc: "Cycle major mode"},
				{name: tui.KbOpenModeSelector, desc: "Open the mode selector"},
				{name: tui.KbCycleAutonomy, desc: "Cycle autonomy level"},
				{name: tui.KbChangeModel, desc: "Open the model selector"},
				{name: tui.KbToggleThinkingBlocks, desc: "Toggle thinking block visibility"},
				{name: tui.KbToggleExpand, desc: "Toggle tool/output expand"},
				{name: tui.KbDeleteLastMsg, desc: "Delete the last chat message"},
				{key: "ctrl+c", desc: "Cancel input request / quit when empty"},
				{key: "esc", desc: "Cancel completion / selection"},
				{key: "/", desc: "Slash commands"},
				{key: "!", desc: "Run a bash command"},
			},
		},
	}
}

// hotkeysTable renders the groups as a markdown table.
func hotkeysTable() string {
	bindings := tui.DefaultKeybindings()
	var b strings.Builder
	for _, g := range hotkeysGroups() {
		b.WriteString(fmt.Sprintf("**%s**\n", g.title))
		b.WriteString("| Key | Action |\n|-----|--------|\n")
		for _, e := range g.entries {
			keys := e.key
			if e.name != "" {
				keys = joinKeys(bindings[e.name].DefaultKeys)
			}
			if keys == "" {
				continue
			}
			b.WriteString(fmt.Sprintf("| `%s` | %s |\n", keys, e.desc))
		}
		b.WriteString("\n")
	}
	b.WriteString("Customize keybindings in your theme/config; see /docs:CONFIGURATION.")
	return b.String()
}

// joinKeys renders a list of key aliases as a single display string.
func joinKeys(keys []string) string {
	if len(keys) == 0 {
		return ""
	}
	display := make([]string, 0, len(keys))
	for _, k := range keys {
		display = append(display, prettyKey(k))
	}
	return strings.Join(display, " / ")
}

// prettyKey formats a raw key name for display (e.g. "shift+tab" stays, but
// arrow keys are left as-is). Centralised so future remapping is easy.
func prettyKey(k string) string { return k }
