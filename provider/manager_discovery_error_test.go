// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
)

// Bug B (2026-08-27): a model-discovery failure must never embed the raw
// response body (a multi-KB Cloudflare 403 HTML challenge page) in the error —
// the flash that renders it overflows the UI. The error carries the HTTP
// status plus a bounded, single-line snippet; HTML collapses to a note.

// newModelsTestManager points provider `pid`'s endpoint at the test server and
// returns a manager whose ListModels will hit it.
func newModelsTestManager(t *testing.T, srv *httptest.Server) *ProviderManager {
	t.Helper()
	cfg := &config.Config{
		Providers: []config.ProviderConfig{
			{ID: "openai-codex", Endpoint: srv.URL + "/v1/chat/completions"},
		},
	}
	return NewProviderManager(cfg)
}

func TestListModels_ErrorBodyHTMLIsSanitized(t *testing.T) {
	html := `<html><head><meta name="viewport" content="width=device-width, initial-scale=1"/>` +
		`<style>body{font-family:Arial}</style></head><body><div class="container">` +
		`<svg width="41" height="41"><path d="M37.5324 16.8707"/></svg>` +
		strings.Repeat("<p>Enable JavaScript and cookies to continue</p>", 50) +
		`</div></body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(html))
	}))
	defer srv.Close()

	pm := newModelsTestManager(t, srv)
	_, err := pm.ListModels("openai-codex")
	if err == nil {
		t.Fatal("expected an error for the 403 discovery response")
	}
	msg := err.Error()
	if !strings.Contains(msg, "403") {
		t.Errorf("error must carry the HTTP status, got %q", msg)
	}
	for _, frag := range []string{"<html", "<svg", "<style", "<p>", "JavaScript"} {
		if strings.Contains(msg, frag) {
			t.Errorf("error leaks raw HTML fragment %q: %q", frag, msg)
		}
	}
	if strings.ContainsAny(msg, "\n\r") {
		t.Errorf("error must be single-line, got %q", msg)
	}
}

func TestListModels_ErrorBodyPlaintextKept(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid api key"}`))
	}))
	defer srv.Close()

	pm := newModelsTestManager(t, srv)
	_, err := pm.ListModels("openai-codex")
	if err == nil {
		t.Fatal("expected an error for the 401 discovery response")
	}
	msg := err.Error()
	if !strings.Contains(msg, "401") {
		t.Errorf("error must carry the HTTP status, got %q", msg)
	}
	if !strings.Contains(msg, "invalid api key") {
		t.Errorf("short plaintext body should be preserved, got %q", msg)
	}
	if strings.ContainsAny(msg, "\n\r") {
		t.Errorf("error must be single-line, got %q", msg)
	}
}

func TestListModels_ErrorBodyLengthCapped(t *testing.T) {
	long := strings.Repeat("x", 5000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(long))
	}))
	defer srv.Close()

	pm := newModelsTestManager(t, srv)
	_, err := pm.ListModels("openai-codex")
	if err == nil {
		t.Fatal("expected an error for the 500 discovery response")
	}
	if len(err.Error()) > 400 {
		t.Errorf("error body not length-capped: len=%d", len(err.Error()))
	}
}
