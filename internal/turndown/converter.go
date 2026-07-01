// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package turndown

import (
	"fmt"
	"strings"

	"golang.org/x/net/html"
)

// Converter turns HTML into Markdown.
type Converter struct {
	rules []Rule
}

// New creates a Converter with the default rule set.
func New() *Converter {
	return &Converter{rules: DefaultRules()}
}

// AddRule appends a custom rule. Rules are evaluated in order; the first match wins.
func (c *Converter) AddRule(rule Rule) {
	c.rules = append(c.rules, rule)
}

// Convert parses html and returns Markdown.
func (c *Converter) Convert(input string) (string, error) {
	if strings.TrimSpace(input) == "" {
		return "", nil
	}

	doc, err := html.Parse(strings.NewReader(input))
	if err != nil {
		return "", fmt.Errorf("parse html: %w", err)
	}

	collapseWhitespace(doc)
	output := c.process(doc)
	output = postProcess(output)
	return output, nil
}

func (c *Converter) process(parent *html.Node) string {
	var output string
	for child := parent.FirstChild; child != nil; child = child.NextSibling {
		replacement := c.replacementForNode(child)
		output = join(output, replacement)
	}
	return output
}

func (c *Converter) replacementForNode(node *html.Node) string {
	if node.Type == html.TextNode {
		return escapeMarkdown(node.Data)
	}
	if node.Type != html.ElementNode {
		return ""
	}

	rule := c.ruleForNode(node)
	content := c.process(node)
	return rule.Replacement(node, content)
}

func (c *Converter) ruleForNode(node *html.Node) Rule {
	for _, rule := range c.rules {
		if rule.Filter.Match(node) {
			return rule
		}
	}
	return Rule{
		Filter: FuncFilter(func(*html.Node) bool { return true }),
		Replacement: func(node *html.Node, content string) string {
			if isBlock(node.Data) {
				return "\n\n" + content + "\n\n"
			}
			return content
		},
	}
}

func join(output, replacement string) string {
	if output == "" {
		return strings.TrimLeft(replacement, "\n")
	}
	if replacement == "" {
		return output
	}

	s1 := trimTrailingNewlines(output)
	s2 := trimLeadingNewlines(replacement)
	nls := maxInt(len(output)-len(s1), len(replacement)-len(s2))
	if nls > 2 {
		nls = 2
	}
	sep := "\n\n"[:nls]
	return s1 + sep + s2
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func postProcess(s string) string {
	s = trimTrailingNewlines(trimLeadingNewlines(s))
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return s
}
