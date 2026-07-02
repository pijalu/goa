// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package schema

// HTTPResponseError is implemented by provider errors that carry the raw HTTP
// response status and body. Consumers can use it to surface richer, more
// actionable error messages to users.
type HTTPResponseError interface {
	error
	StatusCode() int
	ResponseBody() string
}
