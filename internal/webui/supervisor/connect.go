// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// connectResponse is what POST /connect answers a JSON client (goa attach)
// with.
type connectResponse struct {
	Session string `json:"session"`
	Path    string `json:"path"`
	Error   string `json:"error,omitempty"`

	// code is the HTTP status the test helpers read back; not serialised.
	code int
}

// handleConnect opens (or reuses) the session for a project directory. Both
// the machine client (JSON body, from `goa attach --path`) and the index
// page's browser form (urlencoded) land here: the JSON client gets a JSON
// document, the browser a redirect to the session page.
//
// This handler is the security boundary of the multi-project server: the
// path is validated against the projects root BEFORE any child is spawned,
// because a session is a keyboard attached to an agent — where it runs is
// the whole permission question.
func (s *Supervisor) handleConnect(w http.ResponseWriter, r *http.Request) {
	path := r.PostFormValue("path")
	if path == "" {
		var body struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
			path = body.Path
		}
	}
	dir, err := s.ValidatePath(path)
	if err != nil {
		s.fail(w, r, err.Error(), http.StatusBadRequest)
		return
	}
	child, created, err := s.sessionFor(r.Context(), dir)
	if err != nil {
		s.fail(w, r, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = created
	if isBrowserForm(r) {
		// A browser submitted the index form: land it on the session page.
		target := "/s/" + url.PathEscape(child.Session())
		if target == "/s/" {
			target = "/"
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(connectResponse{Session: child.Session(), Path: child.Path()})
}

// fail answers both content types with the same explanation.
func (s *Supervisor) fail(w http.ResponseWriter, r *http.Request, msg string, code int) {
	if isBrowserForm(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(code)
		fmt.Fprintf(w, "<!DOCTYPE html><html><body><h1>goa</h1><p>%s</p><p><a href=\"/\">back</a></p></body></html>", htmlEscape(msg))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(connectResponse{Error: msg})
}

// isBrowserForm reports whether the request came from the index page's form
// rather than a JSON client.
func isBrowserForm(r *http.Request) bool {
	ct := r.Header.Get("Content-Type")
	return strings.HasPrefix(ct, "application/x-www-form-urlencoded")
}

// ValidatePath applies the projects-root policy: absolute, exists, a
// directory, and — after symlink resolution — inside the root. Every
// comparison happens on resolved paths so a symlink inside the tree cannot
// smuggle a session outside it.
func (s *Supervisor) ValidatePath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("no path given")
	}
	if !filepath.IsAbs(raw) {
		return "", fmt.Errorf("path must be absolute: %q", raw)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(raw))
	if err != nil {
		return "", fmt.Errorf("path %q: %w", raw, err)
	}
	fi, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("path %q: %w", raw, err)
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("path %q is not a directory", raw)
	}
	rel, err := filepath.Rel(s.opts.Root, resolved)
	if err != nil {
		return "", fmt.Errorf("path %q: %w", raw, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q is outside the projects root %s", raw, s.opts.Root)
	}
	return resolved, nil
}

// sessionFor returns the live child for dir, spawning one when none exists.
// created reports whether this call spawned it.
func (s *Supervisor) sessionFor(ctx context.Context, dir string) (Child, bool, error) {
	key := keyOf(dir)
	s.mu.Lock()
	if c, ok := s.children[key]; ok {
		s.mu.Unlock()
		// Refresh so a rotated session id is answered with the CURRENT one.
		c.Refresh(ctx)
		return c, false, nil
	}
	if len(s.children) >= s.opts.MaxSessions {
		s.mu.Unlock()
		return nil, false, fmt.Errorf("session limit reached (%d) — the oldest sessions are reaped when idle", s.opts.MaxSessions)
	}
	s.mu.Unlock()

	spawner := s.currentSpawner()
	child, err := spawner.Start(ctx, dir)
	if err != nil {
		return nil, false, err
	}
	s.mu.Lock()
	// Another connect may have raced us to the same directory; keep one.
	if existing, ok := s.children[key]; ok {
		s.mu.Unlock()
		_ = child.Stop()
		return existing, false, nil
	}
	s.children[key] = child
	s.mu.Unlock()
	s.log.Printf("supervisor: opened session %s for %s", child.Session(), dir)
	return child, true, nil
}

// resolve maps a session id to its child, matching current ids and ids the
// child rotated away from (a /new inside a session retires its old id; the
// page or client that still holds the old one must land on the same child).
func (s *Supervisor) resolve(id string) Child {
	if id == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.children {
		if c.Serves(id) {
			return c
		}
	}
	return nil
}

// resolveFresh is resolve with a refresh pass: it re-reads every child's
// status once, so a client presenting an id the session rotated away from
// still lands on the right child.
func (s *Supervisor) resolveFresh(ctx context.Context, id string) Child {
	if c := s.resolve(id); c != nil {
		return c
	}
	s.mu.Lock()
	children := make([]Child, 0, len(s.children))
	for _, c := range s.children {
		children = append(children, c)
	}
	s.mu.Unlock()
	for _, c := range children {
		c.Refresh(ctx)
	}
	return s.resolve(id)
}
