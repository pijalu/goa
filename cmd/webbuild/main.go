// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Command webbuild renders the Markdown documentation under docs/ into
// self-contained HTML pages under web/, so the GitHub Pages site serves
// rendered documentation instead of linking to raw GitHub source.
//
// Usage:
//
//	go run ./cmd/webbuild -in docs -out web
//
// It is invoked by the pages workflow at build time (see Makefile target
// `web-build`). Landing page (index.html) and assets are left untouched.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/template"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

const repoURL = "https://github.com/pijalu/goa"

// tocEntry is a collected heading used to build the in-page table of contents.
type tocEntry struct {
	ID    string
	Title string
	Level int
}

// headingAnchorer is a goldmark AST transformer that slugs every H2/H3
// heading into a stable id and records it for the table of contents.
type headingAnchorer struct {
	source []byte
	toc    *[]tocEntry
}

// Transform implements parser.ASTTransformer.
func (h headingAnchorer) Transform(doc *ast.Document, reader text.Reader, _ parser.Context) {
	seen := map[string]int{}
	ast.Walk(doc, func(n ast.Node, enter bool) (ast.WalkStatus, error) {
		if !enter {
			return ast.WalkContinue, nil
		}
		hd, ok := n.(*ast.Heading)
		if !ok || hd.Level < 2 {
			return ast.WalkContinue, nil
		}
		title := nodeText(hd, h.source)
		slug := slugify(title)
		if slug == "" {
			return ast.WalkContinue, nil
		}
		if c, dup := seen[slug]; dup {
			seen[slug] = c + 1
			slug = fmt.Sprintf("%s-%d", slug, c+1)
		} else {
			seen[slug] = 1
		}
		hd.SetAttribute([]byte("id"), []byte(slug))
		*h.toc = append(*h.toc, tocEntry{ID: slug, Title: title, Level: hd.Level})
		return ast.WalkContinue, nil
	})
}

// nodeText returns the concatenated literal text under a node.
func nodeText(n ast.Node, source []byte) string {
	var b strings.Builder
	ast.Walk(n, func(nn ast.Node, enter bool) (ast.WalkStatus, error) {
		if !enter {
			return ast.WalkContinue, nil
		}
		if t, ok := nn.(*ast.Text); ok {
			b.Write(t.Segment.Value(source))
		}
		return ast.WalkContinue, nil
	})
	return strings.TrimSpace(b.String())
}

// slugify produces a URL-safe slug from free-form heading text.
func slugify(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// attrRe matches href="..." or src="..." attributes in rendered HTML.
var attrRe = regexp.MustCompile(`(href|src)="([^"]*)"`)

// rewriteLinks rewrites markdown-relative links in rendered HTML so they
// resolve correctly on the published site: internal doc links point to the
// generated HTML page, everything else (source files, READMEs outside docs/)
// points to the GitHub blob/tree URL.
func rewriteLinks(html string, docSet map[string]bool) string {
	return attrRe.ReplaceAllStringFunc(html, func(match string) string {
		parts := attrRe.FindStringSubmatch(match)
		return parts[1] + "=\"" + rewriteURL(parts[2], docSet) + "\""
	})
}

// isExternal reports whether a URL should be left untouched.
func isExternal(val string) bool {
	switch {
	case strings.HasPrefix(val, "http://"), strings.HasPrefix(val, "https://"):
		return true
	case strings.HasPrefix(val, "mailto:"), strings.HasPrefix(val, "tel:"):
		return true
	case strings.HasPrefix(val, "#"), strings.HasPrefix(val, "data:"):
		return true
	}
	return false
}

// rewriteURL maps a single markdown-relative URL to its web destination.
func rewriteURL(val string, docSet map[string]bool) string {
	if isExternal(val) {
		return val
	}
	anchor := ""
	if i := strings.Index(val, "#"); i >= 0 {
		anchor = val[i:]
		val = val[:i]
	}
	if val == "" {
		return anchor
	}
	resolved := path.Clean(path.Join("docs", val))
	if strings.HasSuffix(strings.ToLower(val), ".md") {
		if strings.HasPrefix(resolved, "docs/") {
			stem := strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(resolved, "docs/"), ".md"))
			stem = strings.ToLower(path.Base(stem))
			if docSet[stem] {
				return stem + ".html" + anchor
			}
		}
		return repoURL + "/blob/main/" + resolved + anchor
	}
	kind := "blob"
	if strings.HasSuffix(val, "/") || resolved == "." {
		kind = "tree"
	}
	return repoURL + "/" + kind + "/main/" + resolved + anchor
}

// rendered holds the output for one documentation page.
type rendered struct {
	Stem  string
	Title string
	HTML  string
	TOC   []tocEntry
}

// renderDoc converts one Markdown document to site-ready HTML.
func renderDoc(source []byte, docSet map[string]bool) (string, []tocEntry) {
	var toc []tocEntry
	md := goldmark.New(
		goldmark.WithExtensions(
			extension.NewTable(),
			extension.Strikethrough,
			extension.TaskList,
			extension.Linkify,
		),
		goldmark.WithParserOptions(parser.WithASTTransformers(
			util.Prioritized(&headingAnchorer{source: source, toc: &toc}, 100),
		)),
		goldmark.WithExtensions(highlighting.NewHighlighting(
			highlighting.WithStyle("github-dark"),
			highlighting.WithFormatOptions(chromahtml.WithClasses(false)),
		)),
	)
	var buf bytes.Buffer
	if err := md.Convert(source, &buf); err != nil {
		fmt.Fprintf(os.Stderr, "webbuild: render error: %v\n", err)
		return "<pre><code>" + string(source) + "</code></pre>", toc
	}
	return rewriteLinks(buf.String(), docSet), toc
}

// titleFromSource returns the first "# Heading" line, else the fallback.
func titleFromSource(source []byte, fallback string) string {
	for _, line := range strings.Split(string(source), "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(l, "# "))
		}
	}
	return fallback
}

// blurbFromSource returns the first non-heading, non-comment paragraph line,
// with inline markdown formatting stripped. HTML comment blocks (such as the
// SPDX header) are skipped entirely.
func blurbFromSource(source []byte) string {
	inComment := false
	for _, raw := range strings.Split(string(source), "\n") {
		l := strings.TrimSpace(raw)
		if strings.Contains(l, "<!--") {
			inComment = true
		}
		if inComment {
			if strings.Contains(l, "-->") {
				inComment = false
			}
			continue
		}
		if l == "" {
			continue
		}
		// Skip structural markdown: headings, tables, fences, rules.
		if strings.HasPrefix(l, "#") || strings.HasPrefix(l, "|") ||
			strings.HasPrefix(l, "```") || l == "---" || l == "***" {
			continue
		}
		return cleanBlurb(l)
	}
	return ""
}

var (
	reLink   = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
	reBold   = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	reCode   = regexp.MustCompile("`([^`]+)`")
	reItalic = regexp.MustCompile(`\*([^*]+)\*`)
	reUnder  = regexp.MustCompile(`_([^_]+)_`)
	reHTML   = regexp.MustCompile(`<[^>]+>`)
	reSpace  = regexp.MustCompile(`\s+`)
)

// stripMarkdownInline removes inline markdown emphasis, code spans, links and
// raw HTML tags, leaving plain text suitable for a card subtitle.
func stripMarkdownInline(s string) string {
	s = reLink.ReplaceAllString(s, "$1")
	s = reBold.ReplaceAllString(s, "$1")
	s = reCode.ReplaceAllString(s, "$1")
	s = reItalic.ReplaceAllString(s, "$1")
	s = reUnder.ReplaceAllString(s, "$1")
	s = reHTML.ReplaceAllString(s, "")
	return s
}

// cleanBlurb normalizes a single source line into a presentable subtitle.
func cleanBlurb(s string) string {
	s = stripMarkdownInline(s)
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "- ")
	s = strings.TrimPrefix(s, "* ")
	s = strings.TrimSpace(s)
	s = reSpace.ReplaceAllString(s, " ")
	if len(s) > 160 {
		s = strings.TrimSpace(s[:157]) + "..."
	}
	return s
}

// curatedBlurb returns a hand-written one-line description for key docs, so the
// index is informative even when a document's first paragraph is dense.
func curatedBlurb(stem string) string {
	switch stem {
	case "architecture":
		return "How the TUI, core engine, agent SDK and provider layer fit together."
	case "setup":
		return "Install Goa, run the first-run wizard and connect a provider."
	case "configuration":
		return "The config cascade: embedded, home, project, local, env, CLI."
	case "commands":
		return "Reference for every slash command in the TUI."
	case "tools":
		return "The composable tool registry: read, edit, bash, search, SSH and more."
	case "skills":
		return "Reusable prompt templates, inline or as sub-agents."
	case "profiles":
		return "Built-in coder, planner and reviewer profiles, plus custom ones."
	case "tui":
		return "The ANSI-native UI: components, rendering and keybindings."
	case "agentic-sdk":
		return "The merged Agent SDK: agents, sessions and the event model."
	case "plugins":
		return "Extend Goa with JavaScript plugins via the Goja runtime."
	case "providers":
		return "OpenAI-compatible endpoints: OpenAI, llama.cpp, LM Studio, Ollama."
	case "workflows":
		return "Pair (planner to coder) and reviewer collaboration pipelines."
	case "orchestration-design":
		return "Design notes for multi-agent orchestration."
	case "skill-execution":
		return "How skills run: inline injection vs sub-agent dispatch."
	case "development":
		return "Building, testing and contributing to Goa."
	case "profiling":
		return "Profiling and performance tooling for Goa."
	case "goals":
		return "Project goals, roadmap and direction."
	}
	return ""
}

// excludeDoc lists filename stems that should not be published as pages.
func excludeDoc(name string) bool {
	base := strings.ToLower(filepath.Base(name))
	switch {
	case strings.HasPrefix(base, "fix-plan-"):
		return true
	}
	return false
}

// docEntry is an item in the documentation index.
type docEntry struct {
	Stem  string
	Title string
	Blurb string
	Href  string
}

func main() {
	inDir := flag.String("in", "docs", "input directory containing Markdown docs")
	outDir := flag.String("out", "web", "output directory for generated HTML")
	flag.Parse()

	if err := run(*inDir, *outDir); err != nil {
		fmt.Fprintf(os.Stderr, "webbuild: %v\n", err)
		os.Exit(1)
	}
}

func run(inDir, outDir string) error {
	matches, err := filepath.Glob(filepath.Join(inDir, "*.md"))
	if err != nil {
		return fmt.Errorf("glob docs: %w", err)
	}
	sort.Strings(matches)

	pages := make([]rendered, 0, len(matches))
	docSet := map[string]bool{}
	// Pre-pass: collect the complete doc set so cross-links resolve
	// regardless of alphabetical processing order.
	for _, p := range matches {
		if excludeDoc(p) {
			continue
		}
		stem := strings.ToLower(strings.TrimSuffix(filepath.Base(p), ".md"))
		docSet[stem] = true
	}

	entries := []docEntry{}
	for _, p := range matches {
		if excludeDoc(p) {
			continue
		}
		stem := strings.ToLower(strings.TrimSuffix(filepath.Base(p), ".md"))
		source, err := os.ReadFile(p)
		if err != nil {
			return fmt.Errorf("read %s: %w", p, err)
		}
		title := titleFromSource(source, prettify(stem))
		body, toc := renderDoc(source, docSet)
		pages = append(pages, rendered{Stem: stem, Title: title, HTML: body, TOC: toc})
		blurb := curatedBlurb(stem)
		if blurb == "" {
			blurb = blurbFromSource(source)
		}
		entries = append(entries, docEntry{Stem: stem, Title: title, Blurb: blurb, Href: stem + ".html"})
	}
	sort.Slice(entries, func(i, j int) bool { return docWeight(entries[i].Stem) < docWeight(entries[j].Stem) })

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", outDir, err)
	}
	for _, pg := range pages {
		if err := writePage(outDir, pg, entries); err != nil {
			return err
		}
	}
	if err := writeIndex(outDir, entries); err != nil {
		return err
	}
	fmt.Printf("webbuild: wrote %d doc pages to %s\n", len(pages), outDir)
	return nil
}

// writePage renders a single documentation page from the embedded template.
func writePage(outDir string, pg rendered, entries []docEntry) error {
	tmpl, err := template.New("doc").Parse(docPageTmpl)
	if err != nil {
		return fmt.Errorf("parse doc template: %w", err)
	}
	data := struct {
		Title   string
		HTML    string
		TOC     []tocEntry
		Entries []docEntry
	}{Title: pg.Title, HTML: pg.HTML, TOC: pg.TOC, Entries: entries}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("execute doc template: %w", err)
	}
	return os.WriteFile(filepath.Join(outDir, pg.Stem+".html"), buf.Bytes(), 0o644)
}

// writeIndex renders the documentation index page.
func writeIndex(outDir string, entries []docEntry) error {
	tmpl, err := template.New("index").Parse(indexPageTmpl)
	if err != nil {
		return fmt.Errorf("parse index template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, entries); err != nil {
		return fmt.Errorf("execute index template: %w", err)
	}
	return os.WriteFile(filepath.Join(outDir, "docs.html"), buf.Bytes(), 0o644)
}

// prettify turns "agentic-sdk" into "Agentic SDK".
func prettify(stem string) string {
	words := strings.FieldsFunc(strings.ReplaceAll(stem, "-", " "), func(r rune) bool { return r == ' ' })
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

// docWeight gives a stable, intentional ordering for the docs index.
func docWeight(stem string) int {
	order := []string{
		"architecture", "setup", "configuration", "commands", "tools",
		"skills", "profiles", "tui", "agentic-sdk", "plugins", "providers",
		"workflows", "orchestration-design", "skill-execution",
		"development", "profiling", "goals",
	}
	for i, s := range order {
		if s == stem {
			return i
		}
	}
	return len(order)
}
