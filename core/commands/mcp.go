// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
	"github.com/pijalu/goa/internal/mcp"
)

// MCPCommand manages MCP (Model Context Protocol) servers. Goa acts as an MCP
// client: servers are connected and their tools exposed to the agent under the
// mcp__<server>__<tool> namespace, mirroring OpenCode's `opencode mcp` command.
type MCPCommand struct{}

func (c *MCPCommand) Name() string      { return "mcp" }
func (c *MCPCommand) Aliases() []string { return nil }
func (c *MCPCommand) ShortHelp() string {
	return "Manage MCP servers (list/add/remove/enable/disable/reconnect/debug)"
}
func (c *MCPCommand) LongHelp() string { return help.LongHelp(c.Name()) }

// mcpSubcommands drives both the dispatch table and /mcp:<tab> completion.
var mcpSubcommands = []struct {
	value string
	desc  string
}{
	{"list", "List MCP servers and their status"},
	{"add", "Add an MCP server (url or command) and connect it"},
	{"remove", "Remove an MCP server"},
	{"enable", "Enable and connect an MCP server"},
	{"disable", "Disable and disconnect an MCP server"},
	{"reconnect", "Drop and reconnect an MCP server"},
	{"debug", "Connect one-shot and show server info + tools"},
}

// CompleteArgs implements core.ArgCompleter for /mcp:<tab>. Arg 0 completes
// subcommands; later args complete server names for server-taking subcommands.
func (c *MCPCommand) CompleteArgs(ctx core.Context, prefix string) []core.ArgCompletion {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	var comps []core.ArgCompletion
	for _, sc := range mcpSubcommands {
		if prefix == "" || strings.HasPrefix(sc.value, prefix) {
			comps = append(comps, core.ArgCompletion{Value: sc.value, Description: sc.desc})
		}
	}
	return comps
}

// serverNameCompletions returns completions for configured MCP server names.
func serverNameCompletions(ctx core.Context, prefix string) []core.ArgCompletion {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	var comps []core.ArgCompletion
	for name := range ctx.Config.MCP {
		if prefix == "" || strings.HasPrefix(strings.ToLower(name), prefix) {
			comps = append(comps, core.ArgCompletion{Value: name, Description: "MCP server"})
		}
	}
	sort.Slice(comps, func(i, j int) bool { return comps[i].Value < comps[j].Value })
	return comps
}

// Run implements core.Command. The router passes colon-separated args, so
// /mcp:list arrives as args ["list"].
func (c *MCPCommand) Run(ctx core.Context, args []string) error {
	sub := "list"
	rest := args
	if len(args) > 0 {
		sub = strings.ToLower(args[0])
		rest = args[1:]
	}
	switch sub {
	case "list", "ls":
		return c.list(ctx)
	case "add":
		return c.add(ctx, rest)
	case "remove", "rm":
		return c.remove(ctx, rest)
	case "enable":
		return c.setEnabled(ctx, rest, true)
	case "disable":
		return c.setEnabled(ctx, rest, false)
	case "reconnect":
		return c.reconnect(ctx, rest)
	case "debug":
		return c.debug(ctx, rest)
	default:
		return fmt.Errorf("unknown /mcp subcommand %q (use list, add, remove, enable, disable, reconnect, debug)", sub)
	}
}

// list renders configured servers with their runtime status.
func (c *MCPCommand) list(ctx core.Context) error {
	if len(ctx.Config.MCP) == 0 {
		ctx.Writef("No MCP servers configured. Add one with /mcp:add\n")
		return nil
	}
	status := map[string]mcp.ServerStatus{}
	if ctx.MCP != nil {
		status = ctx.MCP.Status()
	}
	names := make([]string, 0, len(ctx.Config.MCP))
	for name := range ctx.Config.MCP {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		srv := ctx.Config.MCP[name]
		st, ok := status[name]
		icon, label := mcpStatusIcon(srv, st, ok)
		target := srv.URL
		if srv.Type != config.MCPTypeRemote {
			target = strings.Join(srv.Command, " ")
		}
		ctx.Writef("%s %-20s %-12s %s\n", icon, name, label, target)
	}
	return nil
}

// mcpStatusIcon maps a server's config + runtime status to a display icon.
func mcpStatusIcon(srv config.MCPServerConfig, st mcp.ServerStatus, known bool) (icon, label string) {
	if !srv.IsEnabled() {
		return "○", "disabled"
	}
	if !known {
		return "○", "not connected"
	}
	switch st.State {
	case mcp.StateConnected:
		return "✓", fmt.Sprintf("connected (%d tools)", st.Tools)
	case mcp.StateFailed:
		return "✗", "failed: " + st.Err
	default:
		return "○", string(st.State)
	}
}

// add parses and persists a new server, then connects it (OpenCode behavior).
func (c *MCPCommand) add(ctx core.Context, args []string) error {
	spec, err := parseMCPAdd(args)
	if err != nil {
		return err
	}
	if ctx.Config.MCP == nil {
		ctx.Config.MCP = map[string]config.MCPServerConfig{}
	}
	ctx.Config.MCP[spec.name] = spec.cfg
	if err := saveMCPFieldValue(ctx, spec.global, []string{"mcp", spec.name}, spec.cfg); err != nil {
		return fmt.Errorf("server added in memory but failed to persist: %w", err)
	}
	ctx.Writef("Added MCP server %q; connecting...\n", spec.name)
	if err := c.connectServer(ctx, spec.name, spec.cfg); err != nil {
		ctx.Writef("✗ %s: %v\n", spec.name, err)
		return nil
	}
	ctx.Writef("✓ %s connected\n", spec.name)
	return nil
}

// mcpAddSpec is the parsed result of /mcp:add.
type mcpAddSpec struct {
	name   string
	cfg    config.MCPServerConfig
	global bool
}

// mcpAddArgs holds the raw parsed arguments from /mcp:add.
type mcpAddArgs struct {
	url     string
	cmd     []string
	env     map[string]string
	headers map[string]string
	global  bool
}

// parseMCPAdd parses: <name> (--url <u> [--header K=V]... | -- <cmd...> [--env K=V]...).
func parseMCPAdd(args []string) (mcpAddSpec, error) {
	if len(args) < 2 {
		return mcpAddSpec{}, fmt.Errorf("usage: /mcp:add <name> (--url <url> [--header K=V]... | -- <cmd...> [--env K=V]...)")
	}
	parsed, err := parseMCPAddFlags(args[1:])
	if err != nil {
		return mcpAddSpec{}, err
	}
	return buildMCPAddSpec(args[0], parsed)
}

// parseMCPAddFlags extracts flags and positional args from /mcp:add arguments.
func parseMCPAddFlags(args []string) (mcpAddArgs, error) {
	var p mcpAddArgs
	p.env = map[string]string{}
	p.headers = map[string]string{}
	seenDashDash := false
	for _, a := range args {
		switch {
		case a == "--":
			seenDashDash = true
		case a == "--global" || a == "-g":
			p.global = true
		case a == "--url":
			return p, fmt.Errorf("--url requires a value; use --url=<url>")
		case strings.HasPrefix(a, "--url="):
			p.url = strings.TrimPrefix(a, "--url=")
		case strings.HasPrefix(a, "--env="):
			if err := putKV(p.env, strings.TrimPrefix(a, "--env=")); err != nil {
				return p, err
			}
		case strings.HasPrefix(a, "--header="):
			if err := putKV(p.headers, strings.TrimPrefix(a, "--header=")); err != nil {
				return p, err
			}
		case seenDashDash:
			p.cmd = append(p.cmd, a)
		default:
			return p, fmt.Errorf("unexpected argument %q (use --url=..., --env=..., --header=..., or -- <cmd...>)", a)
		}
	}
	return p, nil
}

// buildMCPAddSpec validates parsed args and constructs the final spec.
func buildMCPAddSpec(name string, p mcpAddArgs) (mcpAddSpec, error) {
	spec := mcpAddSpec{name: name, global: p.global}
	if (p.url == "") == (len(p.cmd) == 0) {
		return spec, fmt.Errorf("provide either --url <url> or a command after -- (not both, not neither)")
	}
	if p.url != "" {
		if len(p.env) > 0 {
			return spec, fmt.Errorf("--env is only valid for local (command) servers")
		}
		spec.cfg = config.MCPServerConfig{Type: config.MCPTypeRemote, URL: p.url, Headers: p.headers}
		return spec, nil
	}
	if len(p.headers) > 0 {
		return spec, fmt.Errorf("--header is only valid for remote (url) servers")
	}
	spec.cfg = config.MCPServerConfig{Type: config.MCPTypeLocal, Command: p.cmd, Environment: p.env}
	return spec, nil
}

// putKV parses KEY=VALUE into m.
func putKV(m map[string]string, kv string) error {
	i := strings.Index(kv, "=")
	if i < 1 {
		return fmt.Errorf("invalid KEY=VALUE: %q", kv)
	}
	m[kv[:i]] = kv[i+1:]
	return nil
}

// remove deletes a server from config and disconnects it.
func (c *MCPCommand) remove(ctx core.Context, args []string) error {
	args, global := stripGlobalFlag(args)
	name, err := requireServerName(ctx, args)
	if err != nil {
		return err
	}
	if err := deleteMCPField(ctx, global, []string{"mcp", name}); err != nil {
		return fmt.Errorf("failed to persist removal: %w", err)
	}
	delete(ctx.Config.MCP, name)
	if ctx.MCP != nil {
		ctx.MCP.Disconnect(name)
	}
	c.refreshAgentTools(ctx)
	ctx.Writef("Removed MCP server %q\n", name)
	return nil
}

// setEnabled flips a server's enabled flag in config and connects/disconnects.
func (c *MCPCommand) setEnabled(ctx core.Context, args []string, enabled bool) error {
	args, global := stripGlobalFlag(args)
	name, err := requireServerName(ctx, args)
	if err != nil {
		return err
	}
	srv := ctx.Config.MCP[name]
	srv.Enabled = &enabled
	ctx.Config.MCP[name] = srv
	if err := saveMCPFieldValue(ctx, global, []string{"mcp", name, "enabled"}, enabled); err != nil {
		return fmt.Errorf("failed to persist: %w", err)
	}
	if enabled {
		if err := c.connectServer(ctx, name, srv); err != nil {
			ctx.Writef("✗ %s: %v\n", name, err)
			return nil
		}
		ctx.Writef("✓ %s enabled and connected\n", name)
		return nil
	}
	if ctx.MCP != nil {
		ctx.MCP.Disconnect(name)
	}
	c.refreshAgentTools(ctx)
	ctx.Writef("○ %s disabled\n", name)
	return nil
}

// reconnect drops and re-establishes a server connection.
func (c *MCPCommand) reconnect(ctx core.Context, args []string) error {
	name, err := requireServerName(ctx, args)
	if err != nil {
		return err
	}
	if ctx.MCP != nil {
		ctx.MCP.Disconnect(name)
	}
	if err := c.connectServer(ctx, name, ctx.Config.MCP[name]); err != nil {
		ctx.Writef("✗ %s: %v\n", name, err)
		return nil
	}
	ctx.Writef("✓ %s reconnected\n", name)
	return nil
}

// debug connects one-shot and prints status + tools for a server.
func (c *MCPCommand) debug(ctx core.Context, args []string) error {
	name, err := requireServerName(ctx, args)
	if err != nil {
		return err
	}
	srv := ctx.Config.MCP[name]
	ctx.Writef("Connecting to %q (%s)...\n", name, srv.Type)
	sc := mcp.FromConfig(name, ctx.ProjectDir, srv)
	mgr := mcp.NewManager(nil)
	cctx, cancel := context.WithTimeout(context.Background(), sc.EffectiveTimeout())
	defer cancel()
	if err := mgr.Connect(cctx, sc); err != nil {
		ctx.Writef("✗ connect failed: %v\n", err)
		return nil
	}
	defer mgr.Disconnect(name)
	st := mgr.Status()[name]
	ctx.Writef("✓ connected: %d tools registered\n", st.Tools)
	return nil
}

// stripGlobalFlag removes a leading/trailing --global or -g from args.
func stripGlobalFlag(args []string) (rest []string, global bool) {
	for _, a := range args {
		if a == "--global" || a == "-g" {
			global = true
			continue
		}
		rest = append(rest, a)
	}
	return rest, global
}

// saveMCPFieldValue persists a value at path to the home (global) or project
// config layer. Nil saver is a no-op.
func saveMCPFieldValue(ctx core.Context, global bool, path []string, value any) error {
	if ctx.ConfigSaver == nil {
		return nil
	}
	if global {
		return ctx.ConfigSaver.SaveHomeFieldValue(path, value)
	}
	return ctx.ConfigSaver.SaveProjectFieldValue(path, value)
}

// deleteMCPField removes the key at path from the home (global) or project
// config layer. Nil saver is a no-op.
func deleteMCPField(ctx core.Context, global bool, path []string) error {
	if ctx.ConfigSaver == nil {
		return nil
	}
	if global {
		return ctx.ConfigSaver.DeleteHomeField(path)
	}
	return ctx.ConfigSaver.DeleteProjectField(path)
}

// requireServerName validates args and that the server is configured.
func requireServerName(ctx core.Context, args []string) (string, error) {
	if len(args) < 1 || args[0] == "" {
		return "", fmt.Errorf("usage: /mcp:<sub> <server-name>")
	}
	name := args[0]
	if _, ok := ctx.Config.MCP[name]; !ok {
		return "", fmt.Errorf("MCP server %q not configured (see /mcp:list)", name)
	}
	return name, nil
}

// connectServer connects a server through the shared manager and refreshes the
// agent's live toolset.
func (c *MCPCommand) connectServer(ctx core.Context, name string, srv config.MCPServerConfig) error {
	if ctx.MCP == nil {
		return fmt.Errorf("MCP manager unavailable")
	}
	sc := mcp.FromConfig(name, ctx.ProjectDir, srv)
	cctx, cancel := context.WithTimeout(context.Background(), sc.EffectiveTimeout())
	defer cancel()
	if err := ctx.MCP.Connect(cctx, sc); err != nil {
		return err
	}
	c.refreshAgentTools(ctx)
	return nil
}

// refreshAgentTools pushes the current registry (with MCP tools added/removed)
// to the running agent so changes take effect without a restart.
func (c *MCPCommand) refreshAgentTools(ctx core.Context) {
	if ctx.AgentManager == nil || ctx.ToolRegistry == nil {
		return
	}
	_ = ctx.AgentManager.SetTools(ctx.ToolRegistry.All())
}
