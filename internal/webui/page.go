// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

import (
	"embed"
	"html/template"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// assets holds the browser payload: one HTML shell, one stylesheet, one
// script. No framework, no CDN, no build step — the whole page is embedded in
// the binary so `goa server` works offline (spec §5).
//
//go:embed assets/index.html assets/app.css assets/app.js
var assets embed.FS

// PageData is the per-session data injected into the shell.
type PageData struct {
	SessionID string
	Theme     string   // "dark" | "light" — mirrors tui.TheTheme
	ThemeVars []CSSVar // CSS custom properties from tui.TheTheme.ColorHex
	// CSS is the rendered ":root{…}" block for ThemeVars (see cssVarsText).
	CSS template.CSS
	// Nonce is the per-response CSP nonce. The page's own inline <style> and
	// <script> carry it, so the served policy needs no 'unsafe-inline' and an
	// injected tag has nothing to borrow.
	Nonce string
}

// HTMLPage renders the embedded shell and serves its assets.
type HTMLPage struct {
	tpl  *template.Template
	data fs.FS
}

// NewHTMLPage parses the embedded shell once (a parse error is a build-time
// bug, so it panics loudly here rather than per request).
func NewHTMLPage() *HTMLPage {
	tpl := template.Must(template.ParseFS(assets, "assets/index.html"))
	sub, err := fs.Sub(assets, "assets")
	if err != nil {
		panic(err)
	}
	return &HTMLPage{tpl: tpl, data: sub}
}

// Render writes the session page.
func (p *HTMLPage) Render(w http.ResponseWriter, d PageData) error {
	if d.Theme == "" {
		d.Theme = "dark"
	}
	d.CSS = cssVarsText(d.ThemeVars)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	return p.tpl.Execute(w, d)
}

// Asset returns an embedded asset by file name ("app.css"), with its content
// type. Only files directly under assets/ are served — no path traversal.
func (p *HTMLPage) Asset(name string) ([]byte, string, bool) {
	name = path.Base(name)
	if name == "." || name == "/" {
		return nil, "", false
	}
	data, err := fs.ReadFile(p.data, name)
	if err != nil {
		return nil, "", false
	}
	ctype := ""
	switch {
	case strings.HasSuffix(name, ".html"):
		ctype = "text/html; charset=utf-8"
	case strings.HasSuffix(name, ".css"):
		ctype = "text/css; charset=utf-8"
	case strings.HasSuffix(name, ".js"):
		ctype = "text/javascript; charset=utf-8"
	default:
		ctype = "application/octet-stream"
	}
	return data, ctype, true
}

// AssetHandler serves the embedded assets under /assets/.
func (p *HTMLPage) AssetHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, ctype, ok := p.Asset(strings.TrimPrefix(r.URL.Path, "/assets/"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", ctype)
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		_, _ = w.Write(data)
	})
}
