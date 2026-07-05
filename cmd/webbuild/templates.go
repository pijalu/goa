// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package main

const faviconSVG = `data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 32 32'%3E%3Crect width='32' height='32' rx='7' fill='%230d0e14'/%3E%3Cpath d='M16 6 L24 16 L16 26 L8 16 Z' fill='none' stroke='%237c5cfc' stroke-width='2.2'/%3E%3Ccircle cx='16' cy='16' r='3' fill='%235fc3e4'/%3E%3C/svg%3E`

// logoMark is the inline goa diamond logo used in the nav and footer.
const logoMark = `<svg class="logo" viewBox="0 0 32 32" aria-hidden="true">
<rect width="32" height="32" rx="7" fill="#11131c" stroke="#262a40"/>
<path d="M16 6 L24 16 L16 26 L8 16 Z" fill="none" stroke="#7c5cfc" stroke-width="2.2"/>
<circle cx="16" cy="16" r="3" fill="#5fc3e4"/>
</svg>`

const docCSS = `
:root {
  --bg: #0d0e14; --bg-soft: #11131c; --surface: #161826; --surface2: #1c1f30;
  --border: #262a40; --border-soft: #1e2236;
  --text: #e7e9f1; --text-dim: #9aa3b8; --text-faint: #646c84;
  --accent: #7c5cfc; --accent-hi: #8f72ff; --accent2: #5fc3e4;
  --green: #4ade80; --amber: #fbbf24;
  --maxw: 1080px;
  --mono: 'SF Mono','JetBrains Mono','Fira Code',ui-monospace,Consolas,monospace;
  --sans: 'Inter',-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;
}
*,*::before,*::after { margin:0; padding:0; box-sizing:border-box; }
html { scroll-behavior:smooth; }
body { font-family:var(--sans); background:var(--bg); color:var(--text); line-height:1.7;
  -webkit-font-smoothing:antialiased; text-rendering:optimizeLegibility; }
a { color:var(--accent2); text-decoration:none; }
a:hover { text-decoration:underline; }

.topnav { position:sticky; top:0; z-index:50; background:rgba(13,14,20,0.82);
  backdrop-filter:saturate(160%) blur(12px); -webkit-backdrop-filter:saturate(160%) blur(12px);
  border-bottom:1px solid var(--border-soft); }
.nav-inner { max-width:var(--maxw); margin:0 auto; padding:0.7rem 1.5rem;
  display:flex; align-items:center; gap:1.5rem; }
.brand { display:flex; align-items:center; gap:0.55rem; font-weight:700; font-size:1.05rem; color:var(--text); }
.brand:hover { text-decoration:none; }
.brand .logo { width:26px; height:26px; }
.nav-links { display:flex; gap:1.4rem; align-items:center; }
.nav-links a { color:var(--text-dim); font-size:0.9rem; font-weight:500; }
.nav-links a:hover { color:var(--text); text-decoration:none; }
.nav-links a.active { color:var(--text); }
.nav-cta { margin-left:auto; }
.pill { display:inline-flex; align-items:center; gap:0.4rem; padding:0.42rem 0.85rem;
  border-radius:999px; font-size:0.85rem; font-weight:600; border:1px solid var(--border);
  background:var(--surface); color:var(--text); transition:border-color .15s,transform .12s; }
.pill:hover { border-color:var(--accent); text-decoration:none; transform:translateY(-1px); }

.shell { max-width:var(--maxw); margin:0 auto; padding:2rem 1.5rem 4rem;
  display:grid; grid-template-columns:230px minmax(0,1fr); gap:2.5rem; align-items:start; }
.doc-side { position:sticky; top:84px; max-height:calc(100vh - 100px); overflow:auto; }
.doc-side h5 { font-size:0.72rem; text-transform:uppercase; letter-spacing:0.08em;
  color:var(--text-faint); margin:0.4rem 0 0.5rem; }
.doc-side ul { list-style:none; }
.doc-side li a { display:block; padding:0.22rem 0.6rem; border-radius:6px; font-size:0.84rem;
  color:var(--text-dim); border-left:2px solid transparent; }
.doc-side li a:hover { color:var(--text); text-decoration:none; background:var(--surface); border-left-color:var(--accent); }
.doc-side .lvl3 { padding-left:1.1rem; font-size:0.8rem; }
.doc-side .home { margin-bottom:1rem; font-size:0.84rem; color:var(--accent2); display:inline-block; }
.doc-side.empty + .doc-main { grid-column:1 / -1; }

.breadcrumb { font-size:0.82rem; color:var(--text-faint); margin-bottom:1.25rem; font-family:var(--mono); }
.breadcrumb a { color:var(--text-dim); }
.breadcrumb .sep { margin:0 0.5rem; opacity:0.6; }

.doc-main article { min-width:0; }
.doc-main article h1 { font-size:2.1rem; font-weight:800; letter-spacing:-0.025em; margin-bottom:0.6rem;
  padding-bottom:0.8rem; border-bottom:1px solid var(--border-soft); }
.doc-main article h2 { font-size:1.4rem; font-weight:700; letter-spacing:-0.015em;
  margin:2.2rem 0 0.9rem; padding-top:0.5rem; border-top:1px solid var(--border-soft); scroll-margin-top:5rem; }
.doc-main article h3 { font-size:1.12rem; font-weight:650; margin:1.6rem 0 0.6rem; scroll-margin-top:5rem; }
.doc-main article h4 { font-size:1rem; font-weight:650; margin:1.3rem 0 0.5rem; color:var(--text); }
.doc-main article p, .doc-main article li { color:var(--text-dim); margin:0.55rem 0; }
.doc-main article strong { color:var(--text); font-weight:650; }
.doc-main article ul, .doc-main article ol { padding-left:1.4rem; }
.doc-main article li { margin:0.3rem 0; }
.doc-main article blockquote {
  border-left:3px solid var(--accent); background:var(--surface); padding:0.6rem 1rem;
  border-radius:0 8px 8px 0; margin:1rem 0; color:var(--text-dim);
}
.doc-main article hr { border:none; border-top:1px solid var(--border-soft); margin:2rem 0; }
.doc-main article table { width:100%; border-collapse:collapse; margin:1rem 0; font-size:0.9rem; }
.doc-main article th, .doc-main article td { border:1px solid var(--border); padding:0.5rem 0.75rem; text-align:left; }
.doc-main article th { background:var(--surface); color:var(--text); font-weight:650; }
.doc-main article td { color:var(--text-dim); }
.doc-main article img { max-width:100%; border-radius:8px; }
.doc-main article code { font-family:var(--mono); background:var(--surface2); padding:0.12rem 0.4rem;
  border-radius:5px; font-size:0.86em; color:var(--green); }
.doc-main article pre { background:#0a0b11; border:1px solid var(--border); border-radius:10px;
  padding:1rem 1.1rem; overflow-x:auto; margin:1rem 0; line-height:1.55; }
.doc-main article pre code { background:none; padding:0; color:#c9d1e0; font-size:0.86rem; }
.doc-main article a { color:var(--accent2); text-decoration:none; border-bottom:1px solid transparent; }
.doc-main article a:hover { border-bottom-color:var(--accent2); text-decoration:none; }

.pager { display:flex; justify-content:space-between; gap:1rem; margin-top:3rem;
  padding-top:1.5rem; border-top:1px solid var(--border-soft); flex-wrap:wrap; }
.pager a { color:var(--text-dim); font-size:0.88rem; }
.pager a:hover { color:var(--accent2); }

footer { border-top:1px solid var(--border-soft); background:var(--bg-soft); padding:2rem 1.5rem; }
.footer-inner { max-width:var(--maxw); margin:0 auto; display:flex; flex-wrap:wrap; gap:1rem;
  align-items:center; justify-content:space-between; color:var(--text-faint); font-size:0.82rem; }
.footer-inner a { color:var(--text-dim); }

@media (max-width: 860px) {
  .shell { grid-template-columns:1fr; gap:0; }
  .doc-side { position:static; max-height:none; margin-bottom:1.5rem; display:none; }
}
`

const docPageTmpl = `<!DOCTYPE html>
<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>{{.Title}} &mdash; Goa docs</title>
<meta name="description" content="{{.Title}} &mdash; Goa documentation">
<link rel="icon" href="` + faviconSVG + `">
<style>` + docCSS + `</style>
</head>
<body>
<nav class="topnav">
  <div class="nav-inner">
    <a class="brand" href="index.html">` + logoMark + ` goa</a>
    <div class="nav-links">
      <a href="index.html#features">Features</a>
      <a href="index.html#architecture">Architecture</a>
      <a href="index.html#quickstart">Quick Start</a>
      <a href="docs.html" class="active">Docs</a>
    </div>
    <div class="nav-cta">
      <a class="pill" href="` + repoURL + `">GitHub</a>
    </div>
  </div>
</nav>

<div class="shell">
  <aside class="doc-side{{ if not .TOC }} empty{{ end }}">
    <a class="home" href="docs.html">&larr; All documentation</a>
    {{ if .TOC }}<h5>On this page</h5>
    <ul>{{ range .TOC }}<li><a class="{{ if eq .Level 3 }}lvl3{{ end }}" href="#{{ .ID }}">{{ .Title }}</a></li>{{ end }}</ul>{{ end }}
  </aside>
  <main class="doc-main">
    <nav class="breadcrumb">
      <a href="index.html">goa</a><span class="sep">/</span>
      <a href="docs.html">docs</a><span class="sep">/</span>
      <span>{{ .Title }}</span>
    </nav>
    <article>{{ .HTML }}</article>
    <nav class="pager">
      <a href="docs.html">&larr; All documentation</a>
      <a href="` + repoURL + `">Edit on GitHub &rarr;</a>
    </nav>
  </main>
</div>

<footer>
  <div class="footer-inner">
    <span><strong style="color:var(--text)">goa</strong> &mdash; terminal-native AI coding agent</span>
    <span>Copyright &copy; 2026 Pierre Poissinger &middot; <a href="` + repoURL + `/blob/main/LICENSE">GNU GPLv3</a></span>
  </div>
</footer>
</body>
</html>
`

const indexCSS = docCSS + `
.doc-hero { max-width:var(--maxw); margin:0 auto; padding:3.5rem 1.5rem 1.5rem; }
.doc-hero h1 { font-size:2.4rem; font-weight:800; letter-spacing:-0.025em; margin-bottom:0.5rem; }
.doc-hero p { color:var(--text-dim); max-width:640px; }
.doc-grid { max-width:var(--maxw); margin:0 auto; padding:1rem 1.5rem 4rem;
  display:grid; gap:1px; background:var(--border-soft); border:1px solid var(--border-soft);
  border-radius:14px; overflow:hidden; grid-template-columns:repeat(auto-fill,minmax(280px,1fr)); }
.doc-card { background:var(--bg-soft); padding:1.3rem 1.4rem; color:var(--text);
  transition:background .18s; display:block; }
.doc-card:hover { background:var(--surface); text-decoration:none; }
.doc-card .t { font-weight:650; color:var(--text); font-size:1rem; }
.doc-card .b { font-size:0.85rem; color:var(--text-faint); margin-top:0.3rem; line-height:1.5; }
.doc-card .s { font-family:var(--mono); font-size:0.72rem; color:var(--accent2); margin-top:0.6rem; text-transform:lowercase; }
`

const indexPageTmpl = `<!DOCTYPE html>
<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Documentation &mdash; Goa</title>
<meta name="description" content="Goa documentation: architecture, setup, configuration, commands, tools, skills, and more.">
<link rel="icon" href="` + faviconSVG + `">
<style>` + indexCSS + `</style>
</head>
<body>
<nav class="topnav">
  <div class="nav-inner">
    <a class="brand" href="index.html">` + logoMark + ` goa</a>
    <div class="nav-links">
      <a href="index.html#features">Features</a>
      <a href="index.html#architecture">Architecture</a>
      <a href="index.html#quickstart">Quick Start</a>
      <a href="docs.html" class="active">Docs</a>
    </div>
    <div class="nav-cta">
      <a class="pill" href="` + repoURL + `">GitHub</a>
    </div>
  </div>
</nav>

<section class="doc-hero">
  <h1>Documentation</h1>
  <p>Every layer of Goa is documented &mdash; from the high-level architecture down to individual tools, skills, and profiles.</p>
</section>

<div class="doc-grid">{{ range . }}
  <a class="doc-card" href="{{ .Href }}">
    <div class="t">{{ .Title }}</div>
    {{ if .Blurb }}<div class="b">{{ .Blurb }}</div>{{ end }}
    <div class="s">{{ .Stem }}</div>
  </a>{{ end }}
</div>

<footer>
  <div class="footer-inner">
    <span><strong style="color:var(--text)">goa</strong> &mdash; terminal-native AI coding agent</span>
    <span>Copyright &copy; 2026 Pierre Poissinger &middot; <a href="` + repoURL + `/blob/main/LICENSE">GNU GPLv3</a></span>
  </div>
</footer>
</body>
</html>
`
