// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package perms

import "strings"

// Match reports whether name matches pattern. The pattern may contain:
//   - '*' matching one non-empty segment, or any tool name when used alone
//   - '**' matching zero or more segments
//
// Segments are separated by '__' (MCP style) or '/'.
func Match(pattern, name string) bool {
	if pattern == "" {
		return false
	}
	if pattern == "*" || pattern == "**" {
		return true
	}
	patSegs := splitSegments(pattern)
	nameSegs := splitSegments(name)
	return matchSegments(patSegs, nameSegs)
}

// splitSegments splits a name or pattern into segments using '__' and '/' as
// separators. Consecutive separators are collapsed. Single underscores inside
// a segment are preserved (e.g. read_file stays one segment).
func splitSegments(s string) []string {
	s = strings.ReplaceAll(s, "__", "/")
	var segs []string
	var cur strings.Builder
	for _, r := range s {
		if r == '/' {
			if cur.Len() > 0 {
				segs = append(segs, cur.String())
				cur.Reset()
			}
			continue
		}
		cur.WriteRune(r)
	}
	if cur.Len() > 0 {
		segs = append(segs, cur.String())
	}
	return segs
}

// matchSegments matches segmented patterns against segmented names using
// a recursive '*'/'**' wildcard algorithm. matchEmptyPat and matchStar
// are small helpers to keep cognitive complexity within budget.
func matchSegments(pat, name []string) bool {
	for len(pat) > 0 {
		switch pat[0] {
		case "*":
			if len(name) == 0 {
				return false
			}
			pat, name = pat[1:], name[1:]
		case "**":
			return matchDoubleStar(pat[1:], name)
		default:
			if len(name) == 0 || pat[0] != name[0] {
				return false
			}
			pat, name = pat[1:], name[1:]
		}
	}
	return len(name) == 0
}

// matchDoubleStar handles the '**' wildcard: it matches zero or more name
// segments and then continues with the remainder of the pattern.
func matchDoubleStar(pat, name []string) bool {
	for i := 0; i <= len(name); i++ {
		if matchSegments(pat, name[i:]) {
			return true
		}
	}
	return false
}
