// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package lsp

import (
	"context"
	"fmt"
	"io"
	"net"
	"os/exec"
	"time"
)

// Server manages the lifecycle of a language server subprocess.
type Server struct {
	cmd    *exec.Cmd
	client *Client
	cancel context.CancelFunc
}

// ServerConfig configures a language server process.
type ServerConfig struct {
	// Command is the executable to run (e.g. "gopls").
	Command string
	// Args are arguments passed to the executable.
	Args []string
	// Env overrides the process environment. Empty uses the current environment.
	Env []string
	// Stderr, when non-nil, receives the server's stderr (useful for diagnosing
	// server crashes such as gopls panics). Nil discards it.
	Stderr io.Writer
	// RootDir is the working directory for the server process (the LSP project
	// root). Empty uses the current process working directory.
	RootDir string
	// Initialization is sent as initializationOptions at initialize (e.g.
	// pythonPath for pyright, or per-server overrides from config).
	Initialization map[string]any
}

// Start launches the language server and returns an LSP client connected to
// its stdin/stdout.
func Start(ctx context.Context, cfg ServerConfig) (*Server, error) {
	ctx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(ctx, cfg.Command, cfg.Args...)
	if len(cfg.Env) > 0 {
		cmd.Env = cfg.Env
	}
	if cfg.RootDir != "" {
		cmd.Dir = cfg.RootDir
	}
	if cfg.Stderr != nil {
		cmd.Stderr = cfg.Stderr
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("lsp server: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("lsp server: stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("lsp server: start %s: %w", cfg.Command, err)
	}

	conn := &pipeConn{stdin: stdin, stdout: stdout, cmd: cmd}
	client := NewClient(conn)
	go func() {
		_ = client.ReadNotifications(ctx)
	}()

	return &Server{cmd: cmd, client: client, cancel: cancel}, nil
}

// Client returns the LSP client connected to the server.
func (s *Server) Client() *Client {
	return s.client
}

// shutdownTimeout bounds the graceful shutdown request: a wedged server that
// never answers must not hang Close — the caller's ctx may be long-lived (the
// manager lifecycle ctx), and a hung Shutdown would leak the spawn goroutine
// and never publish the spawn outcome (Read stuckfollow-up).
const shutdownTimeout = 2 * time.Second

// Close shuts down the server gracefully and terminates the subprocess.
func (s *Server) Close(ctx context.Context) error {
	if s.client != nil {
		shCtx, cancel := context.WithTimeout(ctx, shutdownTimeout)
		_ = s.client.Shutdown(shCtx)
		cancel()
		_ = s.client.Exit()
		_ = s.client.Close()
	}
	if s.cancel != nil {
		s.cancel()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Wait()
	}
	return nil
}

// pipeConn adapts stdin/stdout pipes to net.Conn.
type pipeConn struct {
	stdin  io.WriteCloser
	stdout io.ReadCloser
	cmd    *exec.Cmd
}

func (p *pipeConn) Read(b []byte) (int, error) {
	return p.stdout.Read(b)
}

func (p *pipeConn) Write(b []byte) (int, error) {
	return p.stdin.Write(b)
}

func (p *pipeConn) Close() error {
	_ = p.stdin.Close()
	_ = p.stdout.Close()
	return nil
}

func (p *pipeConn) LocalAddr() net.Addr  { return nil }
func (p *pipeConn) RemoteAddr() net.Addr { return nil }

func (p *pipeConn) SetDeadline(t time.Time) error      { return nil }
func (p *pipeConn) SetReadDeadline(t time.Time) error  { return nil }
func (p *pipeConn) SetWriteDeadline(t time.Time) error { return nil }
