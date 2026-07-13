// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/tuirender"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

func stripStyle(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}

func TestVerifyRenderer_ShowsResolvedCommandAndTimeout(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module test\n\ngo 1.21\n")

	r := NewVerifyRenderer()
	r.SetProjectDir(dir)

	// Auto-detected go + extra args + timeout → "go test -race ./pkg (timeout 30s)".
	got := stripStyle(r.RenderCall(map[string]any{
		"args":             []any{"-race", "./internal/app/..."},
		"timeout_seconds":  30,
	}, tuirender.RenderContext{}))
	if !strings.Contains(got, "go test -race ./internal/app/...") {
		t.Errorf("missing resolved command in %q", got)
	}
	if !strings.Contains(got, "timeout 30s") {
		t.Errorf("missing timeout in %q", got)
	}
}

func TestVerifyRenderer_ExplicitCommand(t *testing.T) {
	r := NewVerifyRenderer()
	got := stripStyle(r.RenderCall(map[string]any{
		"command":          "go test ./...",
		"timeout_seconds":  15,
	}, tuirender.RenderContext{}))
	if !strings.Contains(got, "go test ./...") {
		t.Errorf("missing explicit command in %q", got)
	}
	if !strings.Contains(got, "timeout 15s") {
		t.Errorf("missing timeout in %q", got)
	}
}

func TestVerifyRenderer_DefaultTimeoutWhenOmitted(t *testing.T) {
	r := NewVerifyRenderer()
	r.SetProjectDir(t.TempDir())
	got := stripStyle(r.RenderCall(map[string]any{}, tuirender.RenderContext{}))
	if !strings.Contains(got, "timeout 60s") {
		t.Errorf("expected default 60s timeout in %q", got)
	}
}
