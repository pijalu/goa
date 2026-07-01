// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package turndown

import (
	"strings"
	"testing"
)

func TestExtractMainContent_RemovesScriptsAndStyles(t *testing.T) {
	html := `<html><head><style>.x{color:red}</style></head>
<body>
<nav>skip me</nav>
<script>alert('hi')</script>
<main><p>keep me</p></main>
<footer>skip me too</footer>
</body></html>`

	out, err := ExtractMainContent(html)
	if err != nil {
		t.Fatalf("ExtractMainContent error: %v", err)
	}
	if strings.Contains(out, "alert") {
		t.Errorf("script content leaked into output: %q", out)
	}
	if strings.Contains(out, "<nav>") {
		t.Errorf("nav element leaked into output: %q", out)
	}
	if strings.Contains(out, "<footer>") {
		t.Errorf("footer element leaked into output: %q", out)
	}
	if !strings.Contains(out, "keep me") {
		t.Errorf("main content missing: %q", out)
	}
}

func TestExtractMainContent_PrefersArticle(t *testing.T) {
	html := `<body>
<div class="sidebar"><p>noise</p></div>
<article><h1>Article title</h1><p>body</p></article>
<div class="ads"><p>buy</p></div>
</body>`

	out, err := ExtractMainContent(html)
	if err != nil {
		t.Fatalf("ExtractMainContent error: %v", err)
	}
	if strings.Contains(out, "noise") {
		t.Errorf("sidebar noise leaked into output: %q", out)
	}
	if strings.Contains(out, "buy") {
		t.Errorf("ads leaked into output: %q", out)
	}
	if !strings.Contains(out, "Article title") {
		t.Errorf("article content missing: %q", out)
	}
}

func TestExtractMainContent_StripsAttributes(t *testing.T) {
	html := `<body><main><p class="lead" style="color:red" id="p1">text</p></main></body>`

	out, err := ExtractMainContent(html)
	if err != nil {
		t.Fatalf("ExtractMainContent error: %v", err)
	}
	if strings.Contains(out, "class=") || strings.Contains(out, "style=") || strings.Contains(out, "id=") {
		t.Errorf("presentation attributes leaked into output: %q", out)
	}
	if !strings.Contains(out, "text") {
		t.Errorf("content missing: %q", out)
	}
}

func TestExtractMainContent_KeepsLinkAttributes(t *testing.T) {
	html := `<body><main><a href="https://example.com" title="Example" class="link">link</a></main></body>`

	out, err := ExtractMainContent(html)
	if err != nil {
		t.Fatalf("ExtractMainContent error: %v", err)
	}
	if !strings.Contains(out, `href="https://example.com"`) {
		t.Errorf("href attribute missing: %q", out)
	}
	if !strings.Contains(out, `title="Example"`) {
		t.Errorf("title attribute missing: %q", out)
	}
	if strings.Contains(out, "class=") {
		t.Errorf("class attribute leaked into output: %q", out)
	}
}

func TestExtractMainContent_Empty(t *testing.T) {
	out, err := ExtractMainContent("")
	if err != nil {
		t.Fatalf("ExtractMainContent error: %v", err)
	}
	if out != "" {
		t.Errorf("expected empty output, got %q", out)
	}
}
