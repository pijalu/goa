// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

import (
	"html/template"
	"net/http"
	"strings"

	"github.com/pijalu/goa/tui"
)

// The no-JS page is the ultimate fallback (spec §11.3): a browser with scripting
// disabled or blocked by policy, a text browser, a `curl` invocation — anything
// that cannot open a WebSocket *or* run the client script still renders the live
// screen and can post input back.
//
// It is deliberately simple and deliberately server-rendered:
//
//   - the current screen as a coloured <pre> (the same cell runs the WebSocket
//     would ship, rendered as inline-styled spans);
//   - a plain <form method="post" action="/input"> with a textarea and a Send
//     button (the browser posts it, the server 303s back to a fresh render);
//   - a <meta http-equiv="refresh"> so the view polls.
//
// Nothing here depends on JavaScript, an EventSource, or fetch. The only client
// behaviour it needs is "submit a form" and "follow a meta refresh", both of
// which every browser has had since the 1990s.

// plainRow is one screen row rendered as a list of styled spans.
type plainRow struct {
	Runs []plainRun
}

// plainRun is a styled span. Class carries the styling flags (bold/italic/…)
// and Style carries the inline colours, so the template emits the cheapest
// markup the run needs.
type plainRun struct {
	Class string
	Style template.CSS
	Text  string
}

// plainPageData is the server-rendered payload. It is a pure value object: no
// JS runs to fill it in, so the server must compute everything up front.
type plainPageData struct {
	SessionID string
	Theme     string
	ThemeVars []CSSVar
	CSS       template.CSS
	Rows      []plainRow
	// Text is the whole screen as plain text — the <pre> fallback for the
	// colour path, and the shape a `curl` user actually wants.
	Text string
	// ReadOnly disables the form and explains why: a viewer must be told, not
	// left wondering why input does nothing.
	ReadOnly   bool
	FormAction string
	Refresh    int
	// Title mirrors the terminal title when the engine set one.
	Title string
}

// plainTpl is the no-JS page template. It uses only what every browser renders:
// a stylesheet block, a <pre> of styled spans, and a form. No <script> tag
// appears anywhere, so the page is inert — and correct — with scripting off.
var plainTpl = template.Must(template.New("plain").Parse(`<!DOCTYPE html>
<html lang="en" data-theme="{{.Theme}}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="refresh" content="{{.Refresh}}">
<title>{{if .Title}}{{.Title}}{{else}}goa{{end}}</title>
<style>
:root{color-scheme:dark}
body{background:#0b0d12;color:#e7e9f1;font:14px/1.35 ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;margin:0;padding:12px}
{{.CSS}}
pre.grid{margin:0 0 12px;padding:10px;background:var(--bg,#11131a);border:1px solid var(--border,#262a36);border-radius:6px;overflow-x:auto;white-space:pre}
pre.grid .b{font-weight:700}.grid .i{font-style:italic}.grid .u{text-decoration:underline}.grid .d{opacity:.65}.grid .s{text-decoration:line-through}
form{display:flex;gap:8px;align-items:flex-start}
textarea{flex:1;min-height:64px;background:#11131a;color:#e7e9f1;border:1px solid var(--border,#262a36);border-radius:6px;padding:8px;font:inherit}
button{background:var(--accent,#7c5cfc);color:#0b0d12;border:0;border-radius:6px;padding:8px 18px;font:inherit;font-weight:700;cursor:pointer}
button[disabled]{opacity:.5;cursor:not-allowed}
.acts{display:flex;flex-direction:column;gap:6px}
.note{color:var(--dim,#646c84);margin:0 0 10px}
.ro{color:var(--warning,#f0a35e);font-weight:700}
</style>
</head>
<body>
<p class="note">No-JavaScript view{{if .SessionID}} — session <code>{{.SessionID}}</code>{{end}} — refreshing every {{.Refresh}}s. <a href="/s/{{.SessionID}}">rich view</a></p>
<pre class="grid">{{range .Rows}}{{range .Runs}}<span{{if .Class}} class="{{.Class}}"{{end}}{{if .Style}} style="{{.Style}}"{{end}}>{{.Text}}</span>
{{end}}{{end}}</pre>
{{if .ReadOnly}}
<p class="ro">read-only — this server is viewer-only, input is refused.</p>
{{else}}
<form method="post" action="{{.FormAction}}">
<textarea name="data" autofocus aria-label="input" placeholder="type a message, then send"></textarea>
<input type="hidden" name="s" value="{{.SessionID}}">
<div class="acts">
<button type="submit">Send</button>
<button type="submit" name="key" value="Enter" title="insert, then press Enter">⏎ Send line</button>
</div>
</form>
{{end}}
</body>
</html>
`))

// plainRefreshSeconds is how often the meta refresh re-renders the screen. Two
// seconds keeps the view live without a chatty server, matching the "polling"
// fallback the spec describes.
const plainRefreshSeconds = 2

// handlePlainPage renders the no-JS fallback: the current screen plus a form that
// posts back to /input (spec §11.3). It is reachable at /s/<id>/page, and at
// /s/<id>?mode=plain.
func (s *Server) handlePlainPage(w http.ResponseWriter, r *http.Request, id string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := plainTpl.Execute(w, s.plainData(id)); err != nil {
		s.log.Printf("webui: render plain page: %v", err)
	}
}

// plainData snapshots the current screen into the template payload. Everything
// is computed server-side; the page does no work on load.
func (s *Server) plainData(id string) plainPageData {
	th := tui.TheTheme
	grid := s.term.Grid()
	_, rows := grid.Size()
	vars := ThemeVars(th)
	d := plainPageData{
		SessionID: id,
		Theme:     ThemeName(th),
		ThemeVars: vars,
		CSS:       cssVarsText(vars),
		Text:      grid.Text(),
		ReadOnly:  s.opts.ReadOnly,
		// The form action carries the session id so /input can validate the
		// target even when scripting is off (spec §10.3 CSRF note).
		FormAction: "/input?s=" + id,
		Refresh:    plainRefreshSeconds,
		Title:      grid.Title(),
	}
	d.Rows = make([]plainRow, 0, rows)
	for r := 0; r < rows; r++ {
		d.Rows = append(d.Rows, plainRow{Runs: plainRuns(grid.Cells(r))})
	}
	return d
}

// plainRuns converts one row of cells into styled spans for the <pre>.
func plainRuns(cells []tui.CellAttrs) []plainRun {
	runs := RowRuns(cells)
	out := make([]plainRun, 0, len(runs))
	for _, r := range runs {
		// Trim the trailing blank padding a fixed-width grid always carries —
		// on a server-rendered page it is pure noise and breaks text selection.
		if strings.TrimSpace(r.Text) == "" {
			continue
		}
		out = append(out, plainRun{
			Class: plainClass(r.Flags),
			Style: plainStyle(r),
			Text:  strings.TrimRight(r.Text, " "),
		})
	}
	return out
}

// plainClass maps the emulator's flag bits to the CSS classes the template
// defines. Class styling keeps the common case free of inline style noise.
func plainClass(f tui.AttrFlags) string {
	var b strings.Builder
	for _, m := range []struct {
		bit  tui.AttrFlags
		name string
	}{
		{tui.AttrBold, "b"},
		{tui.AttrItalic, "i"},
		{tui.AttrUnderline, "u"},
		{tui.AttrDim, "d"},
		{tui.AttrStrike, "s"},
	} {
		if f&m.bit != 0 {
			b.WriteString(m.name)
			b.WriteString(" ")
		}
	}
	return strings.TrimSpace(b.String())
}

// plainStyle renders a run's colours as an inline style. Values come from the
// emulator's own SGR→hex conversion, so they are validated #rrggbb — never
// attacker-chosen — and the template can emit them verbatim. An inverse run swaps
// fg/bg so the visual result matches the terminal.
func plainStyle(r Run) template.CSS {
	fg, bg := r.FG, r.BG
	if r.Flags&tui.AttrInverse != 0 {
		fg, bg = bg, fg
	}
	if fg == "" && bg == "" {
		return ""
	}
	var b strings.Builder
	if fg != "" {
		b.WriteString("color:")
		b.WriteString(fg)
		b.WriteString(";")
	}
	if bg != "" {
		b.WriteString("background-color:")
		b.WriteString(bg)
	}
	return template.CSS(b.String())
}
