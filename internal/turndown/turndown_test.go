// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package turndown

import (
	"strings"
	"testing"
)

func TestEmpty(t *testing.T) {
	c := New()
	got, err := c.Convert("")
	if err != nil {
		t.Fatalf("Convert error: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestParagraph(t *testing.T) {
	c := New()
	got, err := c.Convert("<p>Hello world</p>")
	if err != nil {
		t.Fatalf("Convert error: %v", err)
	}
	want := "Hello world"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestHeadings(t *testing.T) {
	c := New()
	got, err := c.Convert("<h1>One</h1><h2>Two</h2><h3>Three</h3>")
	if err != nil {
		t.Fatalf("Convert error: %v", err)
	}
	want := "# One\n\n## Two\n\n### Three"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestEmphasis(t *testing.T) {
	c := New()
	got, err := c.Convert("<p><strong>bold</strong> and <em>italic</em></p>")
	if err != nil {
		t.Fatalf("Convert error: %v", err)
	}
	want := "**bold** and _italic_"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestLink(t *testing.T) {
	c := New()
	got, err := c.Convert(`<a href="https://example.com" title="Example">link</a>`)
	if err != nil {
		t.Fatalf("Convert error: %v", err)
	}
	want := `[link](https://example.com "Example")`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestImage(t *testing.T) {
	c := New()
	got, err := c.Convert(`<img src="pic.png" alt="pic" title="Photo">`)
	if err != nil {
		t.Fatalf("Convert error: %v", err)
	}
	want := `![pic](pic.png "Photo")`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestList(t *testing.T) {
	c := New()
	got, err := c.Convert("<ul><li>one</li><li>two</li></ul>")
	if err != nil {
		t.Fatalf("Convert error: %v", err)
	}
	want := "*   one\n*   two"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestOrderedList(t *testing.T) {
	c := New()
	got, err := c.Convert("<ol><li>one</li><li>two</li></ol>")
	if err != nil {
		t.Fatalf("Convert error: %v", err)
	}
	want := "1.  one\n2.  two"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCodeBlock(t *testing.T) {
	c := New()
	got, err := c.Convert("<pre><code>func main() {\n    fmt.Println(\"hi\")\n}</code></pre>")
	if err != nil {
		t.Fatalf("Convert error: %v", err)
	}
	want := "    func main() {\n        fmt.Println(\"hi\")\n    }"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestInlineCode(t *testing.T) {
	c := New()
	got, err := c.Convert("<p>Use <code>go run</code></p>")
	if err != nil {
		t.Fatalf("Convert error: %v", err)
	}
	want := "Use `go run`"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBlockquote(t *testing.T) {
	c := New()
	got, err := c.Convert("<blockquote><p>Quote</p></blockquote>")
	if err != nil {
		t.Fatalf("Convert error: %v", err)
	}
	want := "> Quote"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStrikethrough(t *testing.T) {
	c := New()
	got, err := c.Convert("<p><del>deleted</del></p>")
	if err != nil {
		t.Fatalf("Convert error: %v", err)
	}
	want := "~~deleted~~"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWhitespaceCollapse(t *testing.T) {
	c := New()
	got, err := c.Convert("<p>  a   b\n\nc  </p>")
	if err != nil {
		t.Fatalf("Convert error: %v", err)
	}
	want := "a b c"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFullPage(t *testing.T) {
	html := `<!DOCTYPE html>
<html><head><title>T</title></head>
<body>
<h1>Title</h1>
<p>This is a <a href="https://go.dev">link</a> and <strong>bold</strong>.</p>
<ul><li>item one</li><li>item two</li></ul>
</body></html>`
	c := New()
	got, err := c.Convert(html)
	if err != nil {
		t.Fatalf("Convert error: %v", err)
	}
	if !strings.Contains(got, "# Title") {
		t.Errorf("missing heading in %q", got)
	}
	if !strings.Contains(got, "[link](https://go.dev)") {
		t.Errorf("missing link in %q", got)
	}
	if !strings.Contains(got, "**bold**") {
		t.Errorf("missing bold in %q", got)
	}
	if !strings.Contains(got, "*   item one") {
		t.Errorf("missing list item in %q", got)
	}
}
