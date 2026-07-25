// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package catalog

import (
	"fmt"
)

// maxListPages bounds cursor pagination so a misbehaving server cannot loop
// forever (OpenCode parity).
const maxListPages = 1000

// Page is one page of a paginated MCP list result.
type Page[T any] struct {
	Items      []T
	NextCursor string
}

// Paginate follows an MCP cursor-paginated list until exhaustion. It guards
// against duplicate cursors (a server bug that would otherwise loop) and caps
// the page count. list is called with the cursor for the page to fetch (""
// for the first page) and must return that page's items and next cursor.
func Paginate[T any](list func(cursor string) (Page[T], error)) ([]T, error) {
	var out []T
	seen := make(map[string]struct{})
	cursor := ""
	for page := 0; page < maxListPages; page++ {
		p, err := list(cursor)
		if err != nil {
			return nil, err
		}
		out = append(out, p.Items...)
		if p.NextCursor == "" {
			return out, nil
		}
		if _, dup := seen[p.NextCursor]; dup {
			return nil, fmt.Errorf("mcp list returned duplicate cursor: %s", p.NextCursor)
		}
		seen[p.NextCursor] = struct{}{}
		cursor = p.NextCursor
	}
	return nil, fmt.Errorf("mcp list exceeded %d pages", maxListPages)
}
