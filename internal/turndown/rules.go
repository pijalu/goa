// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package turndown

import (
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// Rule converts a matched HTML node into Markdown.
type Rule struct {
	Filter      Filter
	Replacement func(node *html.Node, content string) string
}

// Filter decides whether a rule applies to a node.
type Filter interface {
	Match(node *html.Node) bool
}

// tagFilter matches element nodes by tag name.
type tagFilter struct {
	tags map[string]bool
}

func (f tagFilter) Match(node *html.Node) bool {
	if node.Type != html.ElementNode {
		return false
	}
	return f.tags[strings.ToUpper(node.Data)]
}

// TagFilter returns a Filter that matches any of the supplied tag names.
func TagFilter(names ...string) Filter {
	tags := make(map[string]bool, len(names))
	for _, n := range names {
		tags[strings.ToUpper(n)] = true
	}
	return tagFilter{tags: tags}
}

// funcFilter matches nodes using a custom predicate.
type funcFilter struct {
	fn func(node *html.Node) bool
}

func (f funcFilter) Match(node *html.Node) bool { return f.fn(node) }

// FuncFilter returns a Filter from the given predicate.
func FuncFilter(fn func(node *html.Node) bool) Filter { return funcFilter{fn} }

func attr(node *html.Node, key string) string {
	for _, a := range node.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

func firstChildTag(node *html.Node, tag string) *html.Node {
	for c := node.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && strings.EqualFold(c.Data, tag) {
			return c
		}
	}
	return nil
}

func textContent(node *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(node)
	return b.String()
}

// DefaultRules returns the built-in CommonMark-style rule set.
func DefaultRules() []Rule {
	return []Rule{
		paragraphRule(),
		lineBreakRule(),
		headingRule(),
		blockquoteRule(),
		listRule(),
		listItemRule(),
		indentedCodeBlockRule(),
		fencedCodeBlockRule(),
		horizontalRule(),
		inlineLinkRule(),
		emphasisRule(),
		strongRule(),
		codeRule(),
		imageRule(),
		tableRule(),
		strikethroughRule(),
	}
}

func paragraphRule() Rule {
	return Rule{
		Filter:      TagFilter("p"),
		Replacement: func(node *html.Node, content string) string { return "\n\n" + content + "\n\n" },
	}
}

func lineBreakRule() Rule {
	return Rule{
		Filter: TagFilter("br"),
		Replacement: func(node *html.Node, content string) string {
			return "  \n"
		},
	}
}

func headingRule() Rule {
	return Rule{
		Filter: TagFilter("h1", "h2", "h3", "h4", "h5", "h6"),
		Replacement: func(node *html.Node, content string) string {
			level := int(node.Data[1] - '0')
			return fmt.Sprintf("\n\n%s %s\n\n", strings.Repeat("#", level), content)
		},
	}
}

func blockquoteRule() Rule {
	return Rule{
		Filter: TagFilter("blockquote"),
		Replacement: func(node *html.Node, content string) string {
			content = strings.TrimSpace(content)
			if content == "" {
				return ""
			}
			lines := strings.Split(content, "\n")
			for i, line := range lines {
				lines[i] = "> " + line
			}
			return "\n\n" + strings.Join(lines, "\n") + "\n\n"
		},
	}
}

func listRule() Rule {
	return Rule{
		Filter: TagFilter("ul", "ol"),
		Replacement: func(node *html.Node, content string) string {
			content = strings.TrimSpace(content)
			if content == "" {
				return ""
			}
			if node.Parent != nil && strings.EqualFold(node.Parent.Data, "li") {
				return "\n" + content
			}
			return "\n\n" + content + "\n\n"
		},
	}
}

func listItemRule() Rule {
	return Rule{
		Filter: TagFilter("li"),
		Replacement: func(node *html.Node, content string) string {
			content = strings.TrimSpace(content)
			prefix := "*   "
			if node.Parent != nil && strings.EqualFold(node.Parent.Data, "ol") {
				start := 1
				if s := attr(node.Parent, "start"); s != "" {
					fmt.Sscanf(s, "%d", &start)
				}
				idx := 0
				for sib := node.Parent.FirstChild; sib != nil && sib != node; sib = sib.NextSibling {
					if sib.Type == html.ElementNode && strings.EqualFold(sib.Data, "li") {
						idx++
					}
				}
				prefix = fmt.Sprintf("%d.  ", start+idx)
			}
			content = strings.ReplaceAll(content, "\n", "\n"+strings.Repeat(" ", len(prefix)))
			return prefix + content + "\n"
		},
	}
}

func indentedCodeBlockRule() Rule {
	return Rule{
		Filter: FuncFilter(func(node *html.Node) bool {
			return strings.EqualFold(node.Data, "pre") &&
				firstChildTag(node, "code") != nil
		}),
		Replacement: func(node *html.Node, content string) string {
			code := strings.TrimSpace(textContent(firstChildTag(node, "code")))
			lines := strings.Split(code, "\n")
			for i, line := range lines {
				lines[i] = "    " + line
			}
			return "\n\n" + strings.Join(lines, "\n") + "\n\n"
		},
	}
}

func fencedCodeBlockRule() Rule {
	return Rule{
		Filter:      FuncFilter(func(node *html.Node) bool { return false }),
		Replacement: func(node *html.Node, content string) string { return "" },
	}
}

func horizontalRule() Rule {
	return Rule{
		Filter:      TagFilter("hr"),
		Replacement: func(node *html.Node, content string) string { return "\n\n* * *\n\n" },
	}
}

func inlineLinkRule() Rule {
	return Rule{
		Filter: TagFilter("a"),
		Replacement: func(node *html.Node, content string) string {
			href := attr(node, "href")
			if href == "" {
				return content
			}
			title := attr(node, "title")
			if title != "" {
				title = ` "` + escapeLinkTitle(title) + `"`
			}
			return "[" + content + "](" + escapeLinkDestination(href) + title + ")"
		},
	}
}

func emphasisRule() Rule {
	return Rule{
		Filter: TagFilter("em", "i"),
		Replacement: func(node *html.Node, content string) string {
			if strings.TrimSpace(content) == "" {
				return ""
			}
			return "_" + content + "_"
		},
	}
}

func strongRule() Rule {
	return Rule{
		Filter: TagFilter("strong", "b"),
		Replacement: func(node *html.Node, content string) string {
			if strings.TrimSpace(content) == "" {
				return ""
			}
			return "**" + content + "**"
		},
	}
}

func codeRule() Rule {
	return Rule{
		Filter: FuncFilter(func(node *html.Node) bool {
			if !strings.EqualFold(node.Data, "code") {
				return false
			}
			if node.Parent != nil && strings.EqualFold(node.Parent.Data, "pre") {
				return false
			}
			return true
		}),
		Replacement: func(node *html.Node, content string) string {
			content = strings.ReplaceAll(content, "\n", " ")
			if content == "" {
				return ""
			}
			delimiter := "`"
			for strings.Contains(content, delimiter) {
				delimiter += "`"
			}
			space := ""
			if strings.HasPrefix(content, "`") || strings.HasSuffix(content, "`") ||
				(strings.HasPrefix(content, " ") && strings.HasSuffix(content, " ")) {
				space = " "
			}
			return delimiter + space + content + space + delimiter
		},
	}
}

func imageRule() Rule {
	return Rule{
		Filter: TagFilter("img"),
		Replacement: func(node *html.Node, content string) string {
			alt := escapeMarkdown(attr(node, "alt"))
			src := attr(node, "src")
			if src == "" {
				return ""
			}
			title := attr(node, "title")
			if title != "" {
				title = ` "` + escapeLinkTitle(title) + `"`
			}
			return "![" + alt + "](" + escapeLinkDestination(src) + title + ")"
		},
	}
}

var tableCellRe = regexp.MustCompile(`\s*\n\s*`)

func tableRule() Rule {
	return Rule{
		Filter: TagFilter("table"),
		Replacement: func(node *html.Node, content string) string {
			return "\n\n" + strings.TrimSpace(content) + "\n\n"
		},
	}
}

func strikethroughRule() Rule {
	return Rule{
		Filter:      TagFilter("s", "del", "strike"),
		Replacement: func(node *html.Node, content string) string { return "~~" + content + "~~" },
	}
}
