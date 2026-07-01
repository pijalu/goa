// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package netutil

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFetchSuccess(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "test-agent" {
			t.Errorf("User-Agent = %q, want %q", r.Header.Get("User-Agent"), "test-agent")
		}
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html><body>hi</body></html>"))
	}))
	defer ts.Close()

	f := &Fetcher{UserAgent: "test-agent"}
	resp, err := f.Fetch(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("Fetch error: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if !strings.Contains(string(resp.Body), "hi") {
		t.Errorf("Body = %q, want to contain 'hi'", string(resp.Body))
	}
}

func TestFetchNon2xx(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer ts.Close()

	f := &Fetcher{}
	_, err := f.Fetch(context.Background(), ts.URL)
	if err == nil {
		t.Fatal("expected error for 404")
	}
	var he *HTTPError
	if ok := err.(*HTTPError); ok == nil {
		t.Fatalf("expected *HTTPError, got %T", err)
	}
	_ = he
}

func TestFetchRedirectLimit(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/", http.StatusFound)
	}))
	defer ts.Close()

	f := &Fetcher{MaxRedirects: 2}
	_, err := f.Fetch(context.Background(), ts.URL)
	if err == nil {
		t.Fatal("expected error for redirect loop")
	}
}

func TestFetchTimeout(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	f := &Fetcher{Timeout: 10 * time.Millisecond}
	_, err := f.Fetch(context.Background(), ts.URL)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestFetchRetry(t *testing.T) {
	attempts := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 2 {
			conn, _, _ := w.(http.Hijacker).Hijack()
			_ = conn.Close()
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}))
	defer ts.Close()

	f := &Fetcher{RetryCount: 2, RetryBackoff: 5 * time.Millisecond}
	resp, err := f.Fetch(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("Fetch error: %v", err)
	}
	if string(resp.Body) != "ok" {
		t.Errorf("Body = %q, want 'ok'", string(resp.Body))
	}
	if attempts < 2 {
		t.Errorf("attempts = %d, want at least 2", attempts)
	}
}

func TestFetchMaxBody(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, strings.Repeat("x", 100))
	}))
	defer ts.Close()

	f := &Fetcher{MaxBodyBytes: 50}
	_, err := f.Fetch(context.Background(), ts.URL)
	if err == nil {
		t.Fatal("expected body limit error")
	}
}
