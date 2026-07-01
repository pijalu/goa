// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package turndown

import (
	"strings"

	"golang.org/x/net/html"
)

// collapseWhitespace normalizes whitespace in the DOM tree in place.
// It preserves single spaces between inline elements, trims spaces around
// block/void boundaries, and leaves PRE content untouched.
func collapseWhitespace(root *html.Node) {
	if root == nil {
		return
	}
	collapseNode(root)
}

func collapseNode(n *html.Node) {
	if n.Type == html.ElementNode && strings.EqualFold(n.Data, "pre") {
		return
	}

	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.TextNode {
			child.Data = normalizeText(child, child.Data)
		} else if child.Type == html.ElementNode {
			collapseNode(child)
		}
	}
}

func normalizeText(node *html.Node, text string) string {
	text = collapseRuns(text)

	prevBlock := node.PrevSibling == nil || isBlockBoundary(node.PrevSibling)
	nextBlock := node.NextSibling == nil || isBlockBoundary(node.NextSibling)

	if prevBlock {
		text = strings.TrimLeft(text, " ")
	}
	if nextBlock {
		text = strings.TrimRight(text, " ")
	}
	return text
}

func collapseRuns(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inSpace := false
	for _, r := range s {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			if !inSpace {
				b.WriteByte(' ')
				inSpace = true
			}
		} else {
			b.WriteRune(r)
			inSpace = false
		}
	}
	return b.String()
}

func isBlockBoundary(n *html.Node) bool {
	if n.Type != html.ElementNode {
		return false
	}
	return isBlock(n.Data) || isVoid(n.Data) || strings.EqualFold(n.Data, "br")
}
