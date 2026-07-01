// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"bytes"
	"runtime"
	"strconv"
)

// goroutineID returns the runtime ID of the calling goroutine.
//
// It is used solely by ApplySync to detect re-entrant calls made from inside
// the commandLoop (e.g. a shortcut callback, running as a Command on the loop,
// calls ShowSelector → ApplySync). Without this detection ApplySync would
// enqueue onto cmds and block forever waiting for the loop — which is busy
// running the very Command that issued the call (self-deadlock). When the
// caller IS the commandLoop, ApplySync runs the Command inline instead.
//
// Parsing runtime.Stack output is the standard Go technique for obtaining a
// goroutine ID, since the runtime does not expose them directly. ApplySync is
// never on a hot path (it serves user-initiated overlays/prompts), so the cost
// is negligible.
func goroutineID() uint64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	b := bytes.TrimSpace(buf[:n])
	// Format: "goroutine 42 [running]:\n..."
	const prefix = "goroutine "
	b = bytes.TrimPrefix(b, []byte(prefix))
	b = bytes.SplitN(b, []byte(" "), 2)[0]
	id, _ := strconv.ParseUint(string(b), 10, 64)
	return id
}
