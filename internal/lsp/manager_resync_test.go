// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package lsp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// parseWire splits raw JSON-RPC traffic (Content-Length framed) into decoded
// notifications, keeping method and body for assertions.
func parseWire(t *testing.T, wire string) []wireNotification {
	t.Helper()
	var out []wireNotification
	for _, frame := range strings.Split(wire, "Content-Length:") {
		idx := strings.Index(frame, "\r\n\r\n")
		if idx < 0 {
			continue
		}
		body := strings.TrimSpace(frame[idx+4:])
		if body == "" {
			continue
		}
		var n wireNotification
		if err := json.Unmarshal([]byte(body), &n); err != nil {
			t.Fatalf("parse notification %q: %v", body, err)
		}
		out = append(out, n)
	}
	return out
}

type wireNotification struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type didChangeTextDocumentParams struct {
	TextDocument struct {
		URI     string `json:"uri"`
		Version int    `json:"version"`
	} `json:"textDocument"`
	ContentChanges []struct {
		Text string `json:"text"`
	} `json:"contentChanges"`
}

// TestResyncExternal_PushesDiskChange reproduces the stale-gopls incident: a
// document opened via didOpen is modified on disk behind the manager's back
// (sed -i / git checkout style), and ResyncExternal must push the fresh disk
// content as a didChange instead of leaving the server analyzing the stale
// overlay.
func TestResyncExternal_PushesDiskChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	stale := "// wrapCallTool stays here\npackage main\n"
	if err := os.WriteFile(path, []byte(stale), 0644); err != nil {
		t.Fatal(err)
	}

	sink := &syncBuffer{}
	mgr := startedManager(t, dir, &fakeServerRecorder{}, sink, testSpecs)
	openSync(t, mgr, path, stale)

	fresh := "// split moved the methods away\npackage main\n"
	if err := os.WriteFile(path, []byte(fresh), 0644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if got := mgr.ResyncExternal(ctx); got != 1 {
		t.Fatalf("ResyncExternal refreshed=%d, want 1", got)
	}

	msgs := parseWire(t, sink.String())
	changes := 0
	var last didChangeTextDocumentParams
	for _, m := range msgs {
		if m.Method != "textDocument/didChange" {
			continue
		}
		changes++
		if err := json.Unmarshal(m.Params, &last); err != nil {
			t.Fatalf("unmarshal didChange: %v", err)
		}
	}
	if changes != 1 {
		t.Fatalf("didChange notifications=%d, want 1 (wire=%s)", changes, sink.String())
	}
	if want := uriFor(path); last.TextDocument.URI != want {
		t.Errorf("didChange uri=%q, want %q", last.TextDocument.URI, want)
	}
	if len(last.ContentChanges) != 1 || last.ContentChanges[0].Text != fresh {
		t.Errorf("didChange content=%#v, want fresh disk content %q", last.ContentChanges, fresh)
	}
	if last.TextDocument.Version != 2 {
		t.Errorf("didChange version=%d, want 2 (open=1, sync bumps)", last.TextDocument.Version)
	}
}

// TestResyncExternal_SkipsUnchanged verifies the skip marker: once the overlay
// matches disk, repeated reconciliations push nothing (no diagnostic churn).
func TestResyncExternal_SkipsUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	content := "package main\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	sink := &syncBuffer{}
	mgr := startedManager(t, dir, &fakeServerRecorder{}, sink, testSpecs)
	openSync(t, mgr, path, content)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if got := mgr.ResyncExternal(ctx); got != 0 {
		t.Fatalf("first ResyncExternal refreshed=%d, want 0 (overlay matches disk)", got)
	}
	before := len(parseWire(t, sink.String()))
	if got := mgr.ResyncExternal(ctx); got != 0 {
		t.Fatalf("second ResyncExternal refreshed=%d, want 0", got)
	}
	if after := len(parseWire(t, sink.String())); after != before {
		t.Errorf("wire traffic grew after unchanged resync: %d → %d", before, after)
	}
}

// TestResyncExternal_DeletedDocumentIsSkipped checks the failure mode where an
// externally mutated workspace removes an open file entirely: reconciliation
// must not crash or fabricate pushes for it.
func TestResyncExternal_DeletedDocumentIsSkipped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	content := "package main\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	mgr := startedManager(t, dir, &fakeServerRecorder{}, &syncBuffer{}, testSpecs)
	openSync(t, mgr, path, content)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if got := mgr.ResyncExternal(ctx); got != 0 {
		t.Fatalf("ResyncExternal refreshed=%d, want 0 for deleted document", got)
	}
}

// TestResyncExternal_NilManagerAndNotStarted covers the degraded modes used by
// bootstrap paths without LSP support.
func TestResyncExternal_NilManagerAndNotStarted(t *testing.T) {
	ctx := context.Background()
	var nilMgr *Manager
	if got := nilMgr.ResyncExternal(ctx); got != 0 {
		t.Errorf("nil manager refreshed=%d, want 0", got)
	}
	unstarted := NewManager(t.TempDir(), WithServers(testSpecs))
	if got := unstarted.ResyncExternal(ctx); got != 0 {
		t.Errorf("unstarted manager refreshed=%d, want 0", got)
	}
}

// TestResyncExternal_InvalidatesDiagnostics asserts the pending contract: a
// refresh must invalidate stored diagnostics so pre-sync results cannot leak
// through DiagnosticsFor's fast path until the server republishes.
func TestResyncExternal_InvalidatesDiagnostics(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	content := "package main\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	mgr := startedManager(t, dir, &fakeServerRecorder{}, &syncBuffer{}, testSpecs)
	openSync(t, mgr, path, content)

	// Simulate a pre-sync publication so the store holds visible diagnostics.
	uri := uriFor(path)
	mgr.diags.SetVersion(uri, 1, []Diagnostic{{Message: "phantom duplicate", Severity: 1}})
	if diags := mgr.DiagnosticsFor(context.Background(), path); len(diags) != 1 {
		t.Fatalf("precondition: expected 1 stored diagnostic, got %d", len(diags))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if got := mgr.ResyncExternal(ctx); got != 0 {
		t.Fatalf("refreshed=%d, want 0 (no disk change)", got)
	}
	if diags := mgr.DiagnosticsFor(context.Background(), path); len(diags) != 1 {
		t.Fatalf("unchanged sync must keep stored diagnostics, got %d", len(diags))
	}

	// Mutate disk WITHOUT pushing content: nothing to reconcile (sent hash
	// unchanged), diagnostics stay. Then push a real change via DidChange and
	// confirm MarkPending invalidates until publication.
	diskChanged := "package changed\n"
	if err := os.WriteFile(path, []byte(diskChanged), 0644); err != nil {
		t.Fatal(err)
	}
	if err := mgr.DidChange(ctx, path, diskChanged); err != nil {
		t.Fatalf("DidChange: %v", err)
	}
	// MarkPending invalidated version-1 diagnostics; with no server
	// republication in this fake they must NOT resurface.
	if diags := mgr.DiagnosticsFor(ctx, path); diags != nil {
		t.Errorf("post-edit diagnostics should be pending (nil) until republish, got %+v", diags)
	}
}

// TestPathFromURI_RoundTrip pins the uriFor ⇄ pathFromURI contract that
// ResyncExternal depends on to locate files from their server URIs.
func TestPathFromURI_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub dir", "file name.go")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	back, ok := pathFromURI(uriFor(path))
	if !ok || back != path {
		t.Fatalf("pathFromURI(uriFor(%q)) = %q,%v — want identity", path, back, ok)
	}
	if _, ok := pathFromURI("untitled:Untitled-1"); ok {
		t.Error("untitled scheme must not resolve to a path")
	}
}
