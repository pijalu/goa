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

	"github.com/pijalu/goa/internal/attach"
	"github.com/pijalu/goa/tui"
)

// attachUsage documents the `goa attach` subcommand.
const attachUsage = `goa attach — drive a goa server session from this terminal

Usage:
    goa attach --server 127.0.0.1:8080 [options]

The terminal becomes a client of a running ` + "`goa server`" + ` session: the
server's screen is rendered here with full colour and styling, local
keystrokes drive the remote session, and the session keeps running when the
terminal goes away. Reconnect is automatic after a network drop; the
transcript lands in this terminal's native scrollback.

On a multi-project server (--server-projects-root), --path opens (or
re-attaches to) the session for a project directory.

Options:
    --server URL or HOST:PORT   the goa server to attach to (required)
    --session ID                attach to a specific session (default: ask
                                the server for its live session)
    --path DIR                  open the session for this project directory
                                (multi-project servers)
    --plane cells|blocks        rendering plane to request (default cells:
                                the whole screen as terminal cells)
    --server-auth-token TOKEN   bearer token for --server-auth=token servers
                                (env GOA_SERVER_AUTH_TOKEN)
    --server-auth-user USER     username for --server-auth=basic servers
    --server-auth-password PASS password for --server-auth=basic servers
                                (env GOA_SERVER_AUTH_PASSWORD)

Keys:
    Ctrl+]                      detach: the session keeps running on the
                                server; everything else is sent to it
                                verbatim, exactly as a local session would
                                read it (type /quit inside the session to
                                end it).

The session's own Ctrl+C/Ctrl+D semantics are the server's, not this
terminal's: ` + "`goa server`" + ` sessions are stopped from their console, so an
empty-input Ctrl+C flashes a hint instead of quitting.
`

// runAttachCLI answers `goa attach ...` before the global flag parser sees
// the verb. It reports whether the invocation was an attach.
func runAttachCLI(argv []string) bool {
	if len(argv) == 0 || argv[0] != "attach" {
		return false
	}
	fs := flag.NewFlagSet("attach", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}

	server := fs.String("server", "", "goa server to attach to (URL or host:port)")
	session := fs.String("session", "", "session id to attach to")
	path := fs.String("path", "", "project directory to open on a multi-project server")
	plane := fs.String("plane", "cells", "rendering plane to request (cells or blocks)")
	token := fs.String("server-auth-token", "", "bearer token (prefer env GOA_SERVER_AUTH_TOKEN)")
	user := fs.String("server-auth-user", "", "username for basic auth")
	password := fs.String("server-auth-password", "", "password for basic auth (prefer env GOA_SERVER_AUTH_PASSWORD)")

	if err := fs.Parse(argv[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "goa attach: %v\n\n%s", err, attachUsage)
		exitAfterFlush(2)
	}
	if *server == "" {
		fmt.Fprintf(os.Stderr, "goa attach: --server is required\n\n%s", attachUsage)
		exitAfterFlush(2)
	}

	opts := attach.Options{
		Server:   *server,
		Session:  *session,
		Path:     *path,
		Plane:    *plane,
		Token:    serverAuthSecret(*token, "GOA_SERVER_AUTH_TOKEN"),
		User:     *user,
		Password: serverAuthSecret(*password, "GOA_SERVER_AUTH_PASSWORD"),
	}

	// A signal detaches cleanly: the terminal must be restored, not left in
	// raw mode with the session's pixels on it.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := attach.Run(ctx, opts, tui.NewProcessTerminal()); err != nil {
		if end, ok := err.(*attach.SessionEnded); ok {
			// The server said goodbye: not a failure.
			fmt.Fprintf(os.Stderr, "goa attach: %s\n", end.Reason)
			exitAfterFlush(0)
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		exitAfterFlush(1)
	}
	return true
}
