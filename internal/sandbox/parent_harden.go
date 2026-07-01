// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package sandbox

import "sync"

// hardenFn is the platform-specific parent hardener, assigned in platform files.
var hardenFn HardenParent

// hardenOnce ensures the hardener runs at most once.
var hardenOnce sync.Once

// hardenOK stores the result of the once-only hardening attempt.
var hardenOK bool

// HardenParent makes the current process's /proc/<pid>/environ unreadable to
// same-UID children on Linux.  On other platforms it is a no-op.
//
// Returns true when hardening is applied or unnecessary.  Callers that need
// bypass execution must refuse the exec when this returns false.
type HardenParent func() bool

// RegisterHardenParent registers the platform-specific hardener.  It is
// called from platform-specific init functions.
func RegisterHardenParent(fn HardenParent) {
	hardenFn = fn
}

// Harden runs the registered parent hardener at most once.
func Harden() bool {
	hardenOnce.Do(func() {
		if hardenFn != nil {
			hardenOK = hardenFn()
		} else {
			hardenOK = true
		}
	})
	return hardenOK
}
