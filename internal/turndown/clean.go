// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package turndown

import (
	"strings"

	"golang.org/x/net/html"
)

// nonContentTags lists HTML elements that carry navigation, chrome, styling,
// or metadata rather than page content.
var nonContentTags = map[string]bool{
	"script": true, "style": true, "nav": true, "header": true,
	"footer": true, "aside": true, "form": true, "iframe": true,
	"noscript": true, "svg": true, "canvas": true, "template": true,
	"link": true, "meta": true, "base": true,
}

// allowedAttrs are the only attributes worth keeping after content extraction.
var allowedAttrs = map[string]bool{
	"href": true, "src": true, "alt": true, "title": true,
}

// ExtractMainContent parses HTML, removes boilerplate/non-content nodes, strips
// presentation attributes, and serializes the cleaned content back to HTML.
func ExtractMainContent(input string) (string, error) {
	if strings.TrimSpace(input) == "" {
		return "", nil
	}

	doc, err := html.Parse(strings.NewReader(input))
	if err != nil {
		return "", err
	}

	root := findContentRoot(doc)
	removeNonContent(root)
	stripAttributes(root)

	var b strings.Builder
	if err := html.Render(&b, root); err != nil {
		return "", err
	}
	return b.String(), nil
}

// findContentRoot returns the most content-rich node in the document.
// Preference order: <main>, <article>, <div role="main">, common content IDs
// or classes, <body>, and finally the document root.
func findContentRoot(doc *html.Node) *html.Node {
	if n := findFirstTag(doc, "main"); n != nil {
		return n
	}
	if n := findFirstTag(doc, "article"); n != nil {
		return n
	}
	if n := findContentDiv(doc); n != nil {
		return n
	}
	if n := findFirstTag(doc, "body"); n != nil {
		return n
	}
	return doc
}

// findFirstTag returns the first element node with the given tag name.
func findFirstTag(root *html.Node, name string) *html.Node {
	var found *html.Node
	walk(root, func(n *html.Node) bool {
		if n.Type == html.ElementNode && strings.EqualFold(n.Data, name) {
			found = n
			return false
		}
		return true
	})
	return found
}

// findContentDiv returns the first <div> that looks like a primary content
// container based on role, id, or class.
func findContentDiv(root *html.Node) *html.Node {
	var found *html.Node
	walk(root, func(n *html.Node) bool {
		if n.Type == html.ElementNode && strings.EqualFold(n.Data, "div") && isContentContainer(n) {
			found = n
			return false
		}
		return true
	})
	return found
}

// isContentContainer reports whether a <div> looks like the primary content
// container based on common IDs/classes.
func isContentContainer(n *html.Node) bool {
	id := strings.ToLower(attr(n, "id"))
	class := strings.ToLower(attr(n, "class"))
	role := strings.ToLower(attr(n, "role"))

	if role == "main" {
		return true
	}

	contentIDs := []string{"content", "main-content", "maincontent", "article", "post", "entry"}
	for _, cand := range contentIDs {
		if id == cand {
			return true
		}
	}

	contentClasses := []string{"content", "main-content", "maincontent", "article", "post", "entry", "page-content"}
	for _, cand := range contentClasses {
		if strings.Contains(class, cand) {
			return true
		}
	}

	return false
}

// removeNonContent recursively removes script/style/nav/header/footer/etc.
func removeNonContent(n *html.Node) {
	if n == nil {
		return
	}

	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		if c.Type == html.ElementNode && nonContentTags[strings.ToLower(c.Data)] {
			n.RemoveChild(c)
		} else {
			removeNonContent(c)
		}
		c = next
	}
}

// stripAttributes removes class, style, id, and event attributes from all
// elements, keeping only the small allow-list needed for Markdown conversion.
func stripAttributes(n *html.Node) {
	if n == nil {
		return
	}

	if n.Type == html.ElementNode {
		filtered := n.Attr[:0]
		for _, a := range n.Attr {
			key := strings.ToLower(a.Key)
			if allowedAttrs[key] {
				filtered = append(filtered, a)
			}
		}
		n.Attr = filtered
	}

	for c := n.FirstChild; c != nil; c = c.NextSibling {
		stripAttributes(c)
	}
}

// walk traverses the tree depth-first, calling fn on each node. If fn returns
// false for an element, its children are skipped.
func walk(n *html.Node, fn func(*html.Node) bool) {
	if n == nil {
		return
	}
	if !fn(n) {
		return
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, fn)
	}
}
