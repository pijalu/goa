// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package supervisor

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/pijalu/goa/internal/webui"
)

// processSpawner returns the production Spawner: it launches this same goa
// binary as a single-project `goa server` bound to a private Unix socket.
func (s *Supervisor) processSpawner() Spawner {
	binary := s.opts.Binary
	if binary == "" {
		// The child runs with its own working directory (the project), so a
		// relative argv[0] would not resolve: make it absolute.
		if abs, absErr := filepath.Abs(os.Args[0]); absErr == nil {
			binary = abs
		} else {
			binary = os.Args[0]
		}
	}
	return &processSpawner{
		binary:  binary,
		extra:   s.opts.ServerArgs,
		sockDir: s.sockDir,
		log:     s.log,
		timeout: s.opts.ReadyTimeout,
		next:    &s.nextSock,
	}
}

// processSpawner starts one child process per project.
type processSpawner struct {
	binary  string
	extra   []string
	sockDir string
	log     *log.Logger
	timeout time.Duration
	next    *int
	mu      sync.Mutex
}

// Start launches the child and waits for its session to be ready.
func (p *processSpawner) Start(ctx context.Context, dir string) (Child, error) {
	p.mu.Lock()
	sock := filepath.Join(p.sockDir, fmt.Sprintf("s%d.sock", *p.next))
	*p.next++
	p.mu.Unlock()

	token, err := newToken()
	if err != nil {
		return nil, fmt.Errorf("child token: %w", err)
	}
	args := append([]string{
		"server",
		"--server-addr", webui.UnixSocketPrefix + sock,
		"--server-auth", "token",
		"--server-auth-token", token,
	}, p.extra...)
	cmd := exec.Command(p.binary, args...)
	cmd.Dir = dir
	// The child's UI belongs to its sockets; its logs come to the supervisor
	// console prefixed with the project, where the operator can see them.
	cmd.Stdout = &prefixWriter{prefix: "child " + filepath.Base(dir) + ": ", log: p.log}
	cmd.Stderr = &prefixWriter{prefix: "child " + filepath.Base(dir) + ": ", log: p.log}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s in %s: %w", p.binary, dir, err)
	}

	c := &processChild{
		cmd:      cmd,
		sock:     sock,
		token:    token,
		dir:      dir,
		log:      p.log,
		proxy:    newChildProxy(sock, token),
		lastUse:  time.Now(),
		deadline: p.timeout,
		client: &http.Client{
			Timeout: p.timeout,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, "unix", sock)
				},
			},
		},
	}
	if err := c.waitReady(ctx); err != nil {
		_ = c.Stop()
		return nil, err
	}
	// If the child dies on its own, note it; the proxy's 502 tells the
	// client the rest.
	go func() {
		_ = cmd.Wait()
	}()
	return c, nil
}

// newToken mints a child's bearer token: one random value per child, shared
// with nobody but the supervisor's proxy.
func newToken() (string, error) {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// prefixWriter turns a child's stdout/stderr into supervisor log lines.
type prefixWriter struct {
	prefix string
	log    *log.Logger
	buf    []byte
	mu     sync.Mutex
}

func (w *prefixWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := indexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := string(w.buf[:i])
		w.buf = w.buf[i+1:]
		w.log.Printf("%s%s", w.prefix, line)
	}
	return len(p), nil
}

func indexByte(b []byte, c byte) int {
	for i := range b {
		if b[i] == c {
			return i
		}
	}
	return -1
}

// processChild is one live project session: a child goa process on its
// private Unix socket.
type processChild struct {
	mu     sync.Mutex
	cmd    *exec.Cmd
	sock   string
	token  string
	dir    string
	log    *log.Logger
	proxy  http.Handler
	client *http.Client
	health Health
	// retired holds session ids this child rotated away from, so clients
	// presenting one still resolve here.
	retired  map[string]bool
	lastUse  time.Time
	deadline time.Duration
	stopped  bool
}

func (c *processChild) Session() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.health.Session
}

// Serves reports whether id is the child's current session or one it
// rotated away from. Retired ids are remembered at Refresh time.
func (c *processChild) Serves(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if id != "" && id == c.health.Session {
		return true
	}
	return c.retired[id]
}

func (c *processChild) Clients() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.health.Clients
}

func (c *processChild) Path() string { return c.dir }

func (c *processChild) LastUse() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastUse
}

// Touch records that the supervisor just proxied a request (reaper input).
func (c *processChild) Touch() {
	c.mu.Lock()
	c.lastUse = time.Now()
	c.mu.Unlock()
}

// Proxy returns the reverse proxy to this child, touching the last-use
// clock: proxied traffic is liveness.
func (c *processChild) Proxy() http.Handler {
	c.Touch()
	return c.proxy
}

// Refresh re-reads the child's healthz (session id after a rotation, client
// count for the reaper). It reports whether the child answered.
func (c *processChild) Refresh(ctx context.Context) bool {
	h, err := c.healthz(ctx)
	if err != nil {
		return false
	}
	c.mu.Lock()
	if h.Session != "" && c.health.Session != "" && h.Session != c.health.Session {
		if c.retired == nil {
			c.retired = make(map[string]bool)
		}
		c.retired[c.health.Session] = true
	}
	c.health = h
	c.mu.Unlock()
	return true
}

// waitReady blocks until the child answers healthz with a session id — the
// child's own readiness gate holds that request until its session is wired,
// so this is exactly "the session can act on input".
func (c *processChild) waitReady(ctx context.Context) error {
	deadline := time.After(c.deadline)
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	for {
		if h, err := c.healthz(ctx); err == nil && h.Session != "" {
			c.mu.Lock()
			c.health = h
			c.mu.Unlock()
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("child for %s not ready: %w", c.dir, ctx.Err())
		case <-deadline:
			return fmt.Errorf("child for %s did not become ready within %s", c.dir, c.deadline)
		case <-ticker.C:
		}
		if c.cmd.ProcessState != nil && c.cmd.ProcessState.Exited() {
			return fmt.Errorf("child for %s exited during startup", c.dir)
		}
	}
}

// healthz asks the child for its status over its socket.
func (c *processChild) healthz(ctx context.Context) (Health, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://goa-child/healthz", nil)
	if err != nil {
		return Health{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.client.Do(req)
	if err != nil {
		return Health{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Health{}, fmt.Errorf("healthz: %s", resp.Status)
	}
	var h Health
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		return Health{}, err
	}
	return h, nil
}

// Stop ends the child: SIGTERM (its own graceful shutdown), then SIGKILL
// after a grace period, then the socket file goes away.
func (c *processChild) Stop() error {
	c.mu.Lock()
	if c.stopped {
		c.mu.Unlock()
		return nil
	}
	c.stopped = true
	cmd := c.cmd
	c.mu.Unlock()

	_ = cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		_, _ = cmd.Process.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
	_ = os.Remove(c.sock)
	c.log.Printf("supervisor: stopped session %s (%s)", c.Session(), c.dir)
	return nil
}

// io writer sanity guard: prefixWriter must satisfy io.Writer at compile
// time (it is handed to exec.Cmd).
var _ io.Writer = (*prefixWriter)(nil)
