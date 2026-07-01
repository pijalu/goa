// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import "strings"

// expandCommandParams splits each argument on semicolons so commands can
// accept parameter lists in the form:
//
//	/<command>:<modifier>:<param1;param2;param3;...>
//
// Empty segments produced by trailing semicolons are dropped.
func expandCommandParams(args []string) []string {
	if len(args) == 0 {
		return args
	}
	out := make([]string, 0, len(args))
	for _, a := range args {
		parts := strings.Split(a, ";")
		for _, p := range parts {
			if p == "" {
				continue
			}
			out = append(out, p)
		}
	}
	return out
}
