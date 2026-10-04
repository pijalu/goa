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
	ctx, releaseSignals := shutdownSignals()
	defer releaseSignals()
	if err := serveWebUI(subs, opts, New(subs), ctx); err != nil {
		fatalExitf("Error: %v\n", err)
	}
}

// webSession is the interactive session the web UI serves. *App implements it
// (RunContext). It is an interface so the shutdown wiring — the session ends
// because its context was cancelled, and only THEN does the listener close —
// is testable without a real binary and without a TTY.
type webSession interface {
	RunContext(ctx context.Context) bool
}

// serveWebUI binds the listener, serves the web UI, runs the interactive
// session, and returns once BOTH are over: the session has ended (a browser's
// /quit, a console SIGINT/SIGTERM through session's context, or a startup
// failure) and the listener is closed.
//
// The ordering matters and is the bug this fixes. The session owns the URL the
// browser is driving, so the listener must stay up for as long as the session
// runs and must be closed only after it has ended. The HTTP server therefore
// gets its own context, cancelled here — never the shutdown context — so a
// console Ctrl+C cannot shut the listener down underneath a still-running
// session; the deferred profiler flush after this returns still happens, so the
// process exits 0 with its --cpuprofile/--memprofile written.
func serveWebUI(subs *subsystems, opts RuntimeOptions, session webSession, ctx context.Context) error {
	srv, err := newWebServer(subs, opts)
	if err != nil {
		return err
	}

	ln, err := srv.Listen()
	if err != nil {
		return err
	}

	serveCtx, stopServing := context.WithCancel(context.Background())
	defer stopServing()
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(serveCtx, ln) }()

	fmt.Printf("goa web UI ready: %s\n", srv.URL())

	// The interactive session owns stdout-free rendering: it draws into the
	// virtual screen while browsers watch. It returns when the session ends.
	session.RunContext(ctx)

	stopServing()
	_ = srv.Close()
	<-serveErr
	return nil
}

// shutdownSignals returns a context cancelled by the first SIGINT/SIGTERM
// delivered to the server console, plus a function that releases the handlers.
//
// The handlers are installed here rather than with signal.NotifyContext so the
// SECOND signal can escalate to an immediate exit. NotifyContext alone both
// consumed Ctrl+C (removing the default die-on-SIGINT behaviour while nothing
// observed its context — the reported defect) and left a second Ctrl+C just as
// ignored, so a graceful stop that failed to finish would have left the process
// unstoppable. signalExitCode reports the signal that forced the exit.
func shutdownSignals() (context.Context, func()) {
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	go func() {
		if _, ok := <-sigCh; !ok {
			return
		}
		cancel()
		sig, ok := <-sigCh
		if !ok {
			return
		}
		exitAfterFlush(signalExitCode(sig))
	}()

	return ctx, func() {
		// Stop guarantees no further sends on sigCh, so closing it releases the
		// watcher goroutine.
		signal.Stop(sigCh)
		close(sigCh)
		cancel()
	}
}

// signalExitCode maps a signal to the conventional shell status (128+signum),
// so an escalated stop still reports which signal ended the process.
func signalExitCode(sig os.Signal) int {
	if s, ok := sig.(syscall.Signal); ok {
		return 128 + int(s)
	}
	return 1
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
