// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package netutil

// Response carries the result of a successful Fetch call.
type Response struct {
	URL         string
	StatusCode  int
	ContentType string
	Body        []byte
}
