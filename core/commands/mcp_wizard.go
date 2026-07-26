// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"sort"
	"strings"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/tui"
)

// mcp_wizard.go — interactive add/edit/delete wizard for MCP servers.
//
// The SAME wizard backs two entry points so UX is identical:
//   - /mcp:wizard            (MCPCommand "wizard" subcommand)
//   - /config → MCP servers  (configMenu MCP sub-menu)
//
// The wizard never mutates config directly: every add/edit/delete/enable/
// disable is delegated to the corresponding MCPCommand subcommand, so
// persistence, live connect/disconnect, and agent-tool refresh behave exactly
// like the non-interactive /mcp:add, /mcp:remove, /mcp:enable, /mcp:disable.

// mcpWizard drives the interactive MCP server editor. It embeds the
// configMenu navigator so it can be layered onto the /config menu stack, or
// run standalone from /mcp:wizard with its own root.
type mcpWizard struct {
	ctx core.Context
	m   *configMenu
}

// runMCPWizard launches the shared MCP wizard on a fresh menu stack rooted at
// the wizard's server list. Used by /mcp:wizard.
func runMCPWizard(ctx core.Context) {
	m := newConfigMenu(ctx)
	w := &mcpWizard{ctx: ctx, m: m}
	m.current = w.serverList
	w.serverList()
}

// runMCPWizardOnMenu launches the shared MCP wizard on an existing /config
// menu stack, so Back returns to the /config MCP sub-menu. Used by configMenu.
func runMCPWizardOnMenu(m *configMenu) {
	w := &mcpWizard{ctx: m.ctx, m: m}
	m.open(w.serverList)
}

// serverList is the wizard root: lists configured servers and the actions.
// Selecting a server opens its edit menu; the trailing entries add a server
// or close.
func (w *mcpWizard) serverList() {
	w.m.current = w.serverList
	items := w.serverItems()
	items = append(items,
		tui.SelectorItem{Value: "__add__", Label: "＋ Add server", Description: "connect a new MCP server"},
	)
	w.ctx.SelectOption("MCP servers — select to edit, or add:", items, "", func(v string, ok bool) {
		if !ok {
			w.m.back()
			return
		}
		if v == "__add__" {
			w.m.open(w.addPromptName)
			return
		}
		w.m.open(func() { w.editMenu(v) })
	})
}

// serverItems returns one selector item per configured server with its
// enabled state and target, mirroring /mcp:list and the /config MCP sub-menu.
func (w *mcpWizard) serverItems() []tui.SelectorItem {
	names := make([]string, 0, len(w.ctx.Config.MCP))
	for name := range w.ctx.Config.MCP {
		names = append(names, name)
	}
	sort.Strings(names)
	items := make([]tui.SelectorItem, 0, len(names))
	for _, name := range names {
		srv := w.ctx.Config.MCP[name]
		items = append(items, tui.SelectorItem{
			Value:       name,
			Label:       name,
			Description: boolLabel(srv.IsEnabled()) + " · " + mcpTarget(srv),
		})
	}
	return items
}

// mcpTarget renders a server's connection target (URL for remote, command for
// local) for display, matching /mcp:list.
func mcpTarget(srv config.MCPServerConfig) string {
	if srv.Type == config.MCPTypeRemote {
		return srv.URL
	}
	return strings.Join(srv.Command, " ")
}

// editMenu shows the per-server action menu: toggle, edit, delete, back.
func (w *mcpWizard) editMenu(name string) {
	w.m.current = func() { w.editMenu(name) }
	srv, ok := w.ctx.Config.MCP[name]
	if !ok {
		w.m.flash(fmt.Sprintf("MCP server %q no longer configured", name))
		w.m.back()
		return
	}
	toggle := "Disable"
	if !srv.IsEnabled() {
		toggle = "Enable"
	}
	items := []tui.SelectorItem{
		{Value: "toggle", Label: toggle, Description: "connect/disconnect without deleting"},
		{Value: "edit", Label: "Edit", Description: mcpTarget(srv)},
		{Value: "delete", Label: "Delete", Description: "remove from config and disconnect"},
	}
	w.ctx.SelectOption(fmt.Sprintf("MCP server %q:", name), items, "", func(v string, ok bool) {
		if !ok {
			w.m.back()
			return
		}
		switch v {
		case "toggle":
			w.toggle(name, srv)
		case "edit":
			w.m.open(func() { w.editForm(name, srv) })
		case "delete":
			w.m.open(func() { w.confirmDelete(name) })
		}
	})
}

// toggle flips enabled state via the MCPCommand enable/disable path so the
// live connect/disconnect matches /mcp:enable and /mcp:disable exactly.
func (w *mcpWizard) toggle(name string, srv config.MCPServerConfig) {
	sub := "enable"
	if srv.IsEnabled() {
		sub = "disable"
	}
	if err := (&MCPCommand{}).Run(w.ctx, []string{sub, name}); err != nil {
		w.m.flash(fmt.Sprintf("MCP %s: %v", name, err))
	} else {
		w.m.flash(fmt.Sprintf("MCP server %s %s", name, toggleNextLabel(srv.IsEnabled())))
	}
	w.m.back()
}

// confirmDelete asks before removing, then delegates to /mcp:remove.
func (w *mcpWizard) confirmDelete(name string) {
	w.m.current = func() { w.confirmDelete(name) }
	items := []tui.SelectorItem{
		{Value: "no", Label: "No, keep it"},
		{Value: "yes", Label: "Yes, delete " + name, Description: "cannot be undone"},
	}
	w.ctx.SelectOption("Delete MCP server "+name+"?", items, "no", func(v string, ok bool) {
		if !ok || v != "yes" {
			w.m.back()
			return
		}
		if err := (&MCPCommand{}).Run(w.ctx, []string{"remove", name}); err != nil {
			w.m.flash(fmt.Sprintf("remove %s: %v", name, err))
		} else {
			w.m.flash("MCP server '" + name + "' deleted.")
		}
		// Return to the (now shorter) server list.
		w.m.returnTo(0)
	})
}

// addPromptName is the first add step: collect the server name, then the type.
func (w *mcpWizard) addPromptName() {
	w.m.current = w.addPromptName
	w.ctx.ShowInput("MCP server name (e.g. 'filesystem'):", "", func(name string, ok bool) {
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			w.m.back()
			return
		}
		if _, exists := w.ctx.Config.MCP[name]; exists {
			w.m.flash("MCP server '" + name + "' already exists — select it to edit.")
			w.m.back()
			return
		}
		w.m.open(func() { w.typeSelect(name, config.MCPServerConfig{}) })
	})
}

// typeSelect chooses local (stdio) vs remote (url), then routes to the form.
// existing carries the current values when editing (empty on add).
func (w *mcpWizard) typeSelect(name string, existing config.MCPServerConfig) {
	isEdit := existing.Type != ""
	current := config.MCPTypeLocal
	if isEdit {
		current = existing.Type
	}
	items := []tui.SelectorItem{
		{Value: config.MCPTypeLocal, Label: "Local (stdio command)", Description: "spawn a child process"},
		{Value: config.MCPTypeRemote, Label: "Remote (HTTP/SSE url)", Description: "connect over the network"},
	}
	w.ctx.SelectOption("Server type for "+name+":", items, current, func(v string, ok bool) {
		if !ok {
			w.m.back()
			return
		}
		existing.Type = v
		w.m.open(func() { w.form(name, existing) })
	})
}

// editForm re-enters the type select with the server's current values, so
// editing follows the exact same form flow as adding (UX parity).
func (w *mcpWizard) editForm(name string, existing config.MCPServerConfig) {
	w.typeSelect(name, existing)
}

// form collects the type-specific fields, then persists via /mcp:add.
func (w *mcpWizard) form(name string, srv config.MCPServerConfig) {
	if srv.Type == config.MCPTypeRemote {
		w.remoteForm(name, srv)
		return
	}
	w.localForm(name, srv)
}

// localForm collects command (space-separated argv) then optional env.
func (w *mcpWizard) localForm(name string, srv config.MCPServerConfig) {
	w.ctx.ShowInput("Command (argv, space-separated):", strings.Join(srv.Command, " "), func(cmdLine string, ok bool) {
		if !ok {
			w.m.back()
			return
		}
		cmd := strings.Fields(cmdLine)
		if len(cmd) == 0 {
			w.m.flash("command is required for a local server")
			w.m.back()
			return
		}
		srv.Command = cmd
		w.promptEnv(name, srv)
	})
}

// promptEnv collects optional KEY=VALUE pairs (comma-separated) for a local
// server, then saves. Editing pre-fills the current environment.
func (w *mcpWizard) promptEnv(name string, srv config.MCPServerConfig) {
	w.ctx.ShowInput("Env vars (K=V,K2=V2, optional):", joinKV(srv.Environment), func(envStr string, ok bool) {
		if !ok {
			w.m.back()
			return
		}
		env, err := parseKVList(envStr)
		if err != nil {
			w.m.flash(err.Error())
			w.m.back()
			return
		}
		srv.Environment = env
		w.save(name, srv)
	})
}

// remoteForm collects the URL then optional headers for a remote server.
func (w *mcpWizard) remoteForm(name string, srv config.MCPServerConfig) {
	w.ctx.ShowInput("Server URL:", srv.URL, func(url string, ok bool) {
		url = strings.TrimSpace(url)
		if !ok || url == "" {
			w.m.back()
			return
		}
		srv.URL = url
		w.ctx.ShowInput("Headers (K=V,K2=V2, optional):", joinKV(srv.Headers), func(hStr string, ok bool) {
			if !ok {
				w.m.back()
				return
			}
			headers, err := parseKVList(hStr)
			if err != nil {
				w.m.flash(err.Error())
				w.m.back()
				return
			}
			srv.Headers = headers
			w.save(name, srv)
		})
	})
}

// save persists the server by shelling to the /mcp:add equivalent, so connect
// + agent-tool refresh match the CLI. On edit, the existing entry is replaced
// (upsert), mirroring /mcp:add's behavior for an existing name.
func (w *mcpWizard) save(name string, srv config.MCPServerConfig) {
	if err := upsertMCPServer(w.ctx, name, srv); err != nil {
		w.m.flash(fmt.Sprintf("save %s: %v", name, err))
		w.m.back()
		return
	}
	w.m.flash("MCP server '" + name + "' saved.")
	w.m.returnTo(0)
}

// joinKV renders a K=V map as a comma-separated list for pre-filling inputs.
func joinKV(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+m[k])
	}
	return strings.Join(parts, ",")
}

// parseKVList parses a comma-separated K=V list into a map. Empty input
// yields nil (no env/headers).
func parseKVList(s string) (map[string]string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	out := map[string]string{}
	for _, pair := range strings.Split(s, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		if err := putKV(out, pair); err != nil {
			return nil, err
		}
	}
	return out, nil
}
