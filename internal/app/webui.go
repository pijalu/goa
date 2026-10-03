// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/pijalu/goa/internal/webui"
)

// webServerUsage documents the `goa server` subcommand.
const webServerUsage = `goa server — serve the Goa UI as a web page

Usage:
    goa server [--server-addr 127.0.0.1:8080] [--server-read-only]
               [--server-auth basic|token] [--insecure-no-auth]

The browser is the terminal: the same TUI engine renders into a virtual
screen and the resulting cells are streamed to the page over a WebSocket.
Keys typed in the browser reach the agent exactly as keystrokes do in a real
terminal.

Options:
    --server-addr       listen address (default 127.0.0.1:8080)
    --server-read-only  viewer mode: browsers see the session, keystrokes and
                        image uploads are refused (use for screen sharing)
    --server-max-clients  maximum attached browsers (0 = built-in default)

Security:
    Without --server-auth the server only listens on loopback: a browser can
    drive the agent, so exposing it beyond this machine requires credentials.
    --server-auth=token   one bearer token, exchanged for an HttpOnly cookie at
                          /login; set GOA_SERVER_AUTH_TOKEN or --server-auth-token
    --server-auth=basic   HTTP Basic; set --server-auth-user and
                          GOA_SERVER_AUTH_PASSWORD (or --server-auth-password)
    --insecure-no-auth    serve without credentials on any address — anything
                          that can reach the port can drive the agent

Repeated wrong credentials lock the client out for a minute. Every response
carries a strict Content-Security-Policy, and cross-origin writes are refused.
`

// runWebServer starts the web UI: the ordinary interactive session (same
// engine, same agent, same event paths) with a VirtualTerminal injected in
// place of the TTY, plus an HTTP server that hands the resulting cell grid to
// browsers.
//
// One wiring site, no second code path: everything the terminal UI can do,
// the page can do, because it *is* the terminal UI.
func runWebServer(subs *subsystems, opts RuntimeOptions) {
	srv, err := newWebServer(subs, opts)
	if err != nil {
		fatalExitf("Error: %v\n", err)
	}

	ln, err := srv.Listen()
	if err != nil {
		fatalExitf("Error: %v\n", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ctx, ln) }()

	fmt.Printf("goa web UI ready: %s\n", srv.URL())

	// The interactive session owns stdout-free rendering: it draws into the
	// virtual screen while browsers watch. It returns when the session ends.
	New(subs).Run()

	stop()
	_ = srv.Close()
	<-serveErr
}

// newWebServer builds the web UI's HTTP server for the given options, or
// explains why it refuses.
//
// The exposure check happens here, before any listener exists: an
// unauthenticated non-loopback bind must never have answered a single request,
// not even a health probe. Keeping this separate from runWebServer (which
// exits the process on error) is what lets the refusal be tested at all.
func newWebServer(subs *subsystems, opts RuntimeOptions) (*webui.Server, error) {
	authCfg, err := webuiAuthConfig(opts)
	if err != nil {
		return nil, err
	}
	addr := serverAddr(opts)
	if err := webui.CheckExposure(addr, authCfg, opts.InsecureNoAuth); err != nil {
		return nil, err
	}
	cols, rows := webui.DefaultCols, webui.DefaultRows
	vt := webui.NewVirtualTerminal(cols, rows)
	subs.terminal = vt
	return webui.NewServer(vt, cols, rows, webui.ServerOptions{
		Addr:           addr,
		SessionID:      webSessionID(subs),
		ReadOnly:       opts.ServerReadOnly,
		MaxClients:     opts.ServerMaxClients,
		Auth:           authCfg,
		InsecureNoAuth: opts.InsecureNoAuth,
	}), nil
}

// serverAddr resolves the listen address, applying the loopback default. The
// default is loopback precisely so the no-auth case is safe by default.
func serverAddr(opts RuntimeOptions) string {
	if opts.ServerAddr == "" {
		return webui.DefaultAddr
	}
	return opts.ServerAddr
}

// webuiAuthConfig turns the auth flags into a webui.AuthConfig, failing loudly
// on an unknown scheme so a typo does not silently leave the server open.
func webuiAuthConfig(opts RuntimeOptions) (webui.AuthConfig, error) {
	mode, err := webui.ParseAuthMode(opts.ServerAuth)
	if err != nil {
		return webui.AuthConfig{}, err
	}
	return webui.AuthConfig{
		Mode:     mode,
		Username: opts.ServerAuthUser,
		Password: opts.ServerAuthPass,
		Token:    opts.ServerAuthToken,
	}, nil
}

// webSessionID reports the current session id for the /s/<id> namespace. It is
// read lazily so a session created after startup is reflected in the URL.
func webSessionID(subs *subsystems) func() string {
	return func() string {
		if subs == nil || subs.sessionStore == nil {
			return ""
		}
		return subs.sessionStore.SessionID()
	}
}
