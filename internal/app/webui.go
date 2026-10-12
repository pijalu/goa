// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/pijalu/goa/internal/webui"
	"github.com/pijalu/goa/internal/webui/supervisor"
)

// webServerUsage documents the `goa server` subcommand.
const webServerUsage = `goa server — serve the Goa UI as a web page

Usage:
    goa server [--server-addr 127.0.0.1:8080] [--server-read-only]
               [--server-auth basic|token] [--insecure-no-auth]
               [--server-cells] [--server-projects-root DIR]

The browser renders the conversation as HTML blocks (native scrolling and
resize) and keeps the input line/status band as live terminal cells, so
typing behaves exactly like the TUI. --server-cells serves the legacy
plane instead: the whole screen as terminal cells (the browser is the
terminal).

Options:
    --server-addr       listen address (default 127.0.0.1:8080)
    --server-read-only  viewer mode: browsers see the session, keystrokes and
                        image uploads are refused (use for screen sharing)
    --server-max-clients  maximum attached browsers (0 = built-in default)
    --server-cells      legacy whole-screen-as-cells plane

Security:
    Without --server-auth the server only listens on loopback: a browser can
    drive the agent, so exposing it beyond this machine requires credentials.
    --server-auth=token   one bearer token, exchanged for an HttpOnly cookie at
                          /login; set GOA_SERVER_AUTH_TOKEN or --server-auth-token
    --server-auth=basic   HTTP Basic; set --server-auth-user and
                          GOA_SERVER_AUTH_PASSWORD (or --server-auth-password)
    --insecure-no-auth    serve without credentials on any address — anything
                          that can reach the port can drive the agent

    --server-projects-root DIR
                          multi-project mode: serve one session per project
                          directory under this root instead of the CWD's.
                          Sessions are child goa server processes, opened on
                          demand (a browser's index page, or goa attach
                          --path) and reaped when no client has touched them
                          for --server-session-idle
    --server-max-sessions N
                          maximum live project sessions (default 8)
    --server-session-idle DURATION
                          idle reaping for project sessions (default 30m;
                          0 = default, negative = keep until shutdown)

Attach:
    goa attach --server 127.0.0.1:8080 [--path DIR]
                          drive the served session from a real terminal: the
                          same screen, colours and keystrokes, with the
                          transcript in the terminal's own scrollback.
                          Ctrl+] detaches without stopping the session.

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

// maybeRunSupervisor reports whether this invocation is multi-project mode
// (`goa server --server-projects-root`) and runs it if so. The supervisor
// needs no session of its own: it fronts child processes and must not build
// this process's subsystems (or run the first-run wizard against the CWD).
func maybeRunSupervisor(opts RuntimeOptions) bool {
	if !opts.Server || opts.ServerProjectsRoot == "" {
		return false
	}
	runSupervisorServer(opts)
	return true
}

// runSupervisorServer runs `goa server --server-projects-root`: a
// multi-project front end with no session of its own. Every project session
// is an ordinary child `goa server` process on a private Unix socket — per
// project config, plugins and trust are exact because the child IS the
// single-project server — and the supervisor fronts them with one
// authenticated surface: the session index, the connect-by-path handshake
// (goa attach --path, the browser's open form), and a reverse proxy.
func runSupervisorServer(opts RuntimeOptions) {
	ctx, releaseSignals := shutdownSignals()
	defer releaseSignals()
	authCfg, err := webuiAuthConfig(opts)
	if err != nil {
		fatalExitf("Error: %v\n", err)
	}
	if err := webui.CheckExposure(serverAddr(opts), authCfg, opts.InsecureNoAuth); err != nil {
		fatalExitf("Error: %v\n", err)
	}
	sup, err := supervisor.New(supervisor.Options{
		Root:           opts.ServerProjectsRoot,
		Addr:           serverAddr(opts),
		Auth:           authCfg,
		InsecureNoAuth: opts.InsecureNoAuth,
		MaxSessions:    opts.ServerMaxSessions,
		IdleTimeout:    idleTimeoutFor(opts),
		Log:            log.New(os.Stderr, "", 0),
	})
	if err != nil {
		fatalExitf("Error: %v\n", err)
	}
	if err := sup.ListenAndServe(ctx); err != nil {
		fatalExitf("Error: %v\n", err)
	}
}

// idleTimeoutFor maps the flag onto the supervisor's policy: 0 keeps the
// built-in default, a negative value disables reaping.
func idleTimeoutFor(opts RuntimeOptions) time.Duration {
	if opts.ServerSessionIdle < 0 {
		return -1
	}
	return opts.ServerSessionIdle
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
	// Requests are held until the app has finished wiring itself (see
	// webui.ServerOptions.Ready): the listener binds before the engine exists, and
	// an early browser must be answered by a session that can act on its input
	// rather than have its keystrokes land in a half-built engine (bugs.md B7).
	subs.webReady = make(chan struct{})
	plane := webui.PlaneBlocks
	if opts.ServerCells {
		plane = webui.PlaneCells
	}
	return webui.NewServer(vt, cols, rows, webui.ServerOptions{
		Addr:           addr,
		SessionID:      webSessionID(subs),
		ReadOnly:       opts.ServerReadOnly,
		MaxClients:     opts.ServerMaxClients,
		Auth:           authCfg,
		InsecureNoAuth: opts.InsecureNoAuth,
		Plane:          plane,
		Ready:          subs.webReady,
	}), nil
}

// markWebReady releases the web server's readiness gate. It is called once the
// interactive session is fully wired (the engine is started, the input editor is
// focused and its submit path is installed), so every held browser request is
// then served by a session that can act on it. Safe to call more than once.
func (a *App) markWebReady() {
	if a.subs == nil {
		return
	}
	a.subs.webReadyOnce.Do(func() {
		if a.subs.webReady != nil {
			close(a.subs.webReady)
		}
	})
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
