// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/pijalu/agent-mcp/server"
)

// runAgentMCPDaemon adapts Goa's CLI to agent-mcp's embedded server library.
// Auto-start re-executes Goa with this command; no agent-mcp installation is
// involved. The library owns all config, session, and MCP daemon execution.
func runAgentMCPDaemon(args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("goa mcp daemon", flag.ContinueOnError)
	fs.SetOutput(stderr)
	listen := fs.String("listen", server.DefaultListenAddress, "daemon listen address host:port")
	var apiKeys mcpAPIKeys
	fs.Var(&apiKeys, "api-key", "accepted client API key (repeatable)")
	idleTime := fs.Duration("server-idle-time", 0, "idle timeout; 0 disables")
	grace := fs.Duration("shutdown-grace", server.DefaultShutdownGrace, "in-flight drain grace")
	configPath := fs.String("config", "", "path to agent-mcp config.toml")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %v", fs.Args())
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return server.Run(ctx, server.Options{
		Listen:        *listen,
		ConfigPath:    *configPath,
		APIKeys:       apiKeys,
		IdleTimeout:   *idleTime,
		ShutdownGrace: *grace,
	}, stderr)
}

// mcpAPIKeys collects repeatable API-key flags.
type mcpAPIKeys []string

func (keys *mcpAPIKeys) String() string { return fmt.Sprint([]string(*keys)) }

func (keys *mcpAPIKeys) Set(value string) error {
	*keys = append(*keys, value)
	return nil
}
