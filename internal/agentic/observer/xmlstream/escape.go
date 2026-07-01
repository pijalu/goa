// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package xmlstream

import "strings"

// EscapeXML escapes special XML characters in text content.
// Handles: & → &amp;, < → &lt;, > → &gt;
func EscapeXML(s string) string {
	var sb strings.Builder
	sb.Grow(len(s) + 32) // Pre-allocate with buffer

	for _, r := range s {
		switch r {
		case '&':
			sb.WriteString("&amp;")
		case '<':
			sb.WriteString("&lt;")
		case '>':
			sb.WriteString("&gt;")
		default:
			sb.WriteRune(r)
		}
	}

	return sb.String()
}

// EscapeXMLAttr escapes special XML characters for attribute values.
// Handles: & → &amp;, < → &lt;, > → &gt;, " → &quot;
func EscapeXMLAttr(s string) string {
	var sb strings.Builder
	sb.Grow(len(s) + 32)

	for _, r := range s {
		switch r {
		case '&':
			sb.WriteString("&amp;")
		case '<':
			sb.WriteString("&lt;")
		case '>':
			sb.WriteString("&gt;")
		case '"':
			sb.WriteString("&quot;")
		default:
			sb.WriteRune(r)
		}
	}

	return sb.String()
}
