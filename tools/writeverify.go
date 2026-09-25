// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/pijalu/goa/internal"
)

// This file carries the two cross-cutting halves of the write-durability
// mitigation (edit-tool Issue 3): turning a failed post-write read-back into a
// loud tool error instead of a false success, and making the path a write
// actually landed on visible when it differs from the path the caller named.
// Shared by the edit funnel (writeEditResult) and the write tool, so both report
// identically.

// verifyFailed reports whether err came from post-write read-back verification
// (common.WriteFileVerified) rather than from the write syscall itself.
func verifyFailed(err error) bool {
	return errors.Is(err, ErrWriteVerifyFailed)
}

// writeVerifyFailedError converts a failed read-back verification into the
// write_verify_failed tool error. The write reported success but the file on
// disk is not the content that was sent, so reporting success would be a false
// success — the exact failure Issue 3 describes. The error names the resolved
// path because a mismatch is most often a path confusion (the content landed in
// another file) or an external process rewriting the file.
func writeVerifyFailedError(tool, resolvedPath string, wrote int, err error) *internal.ToolError {
	return &internal.ToolError{
		Tool: tool,
		Type: "write_verify_failed",
		Detail: fmt.Sprintf("wrote %d byte(s) to %s but read-back verification failed: %v",
			wrote, resolvedPath, err),
		HintText: "The write did not persist as sent — the file may be modified externally, or another copy of it was written. Check the resolved path above, inspect the file, then retry.",
	}
}

// resolvedPathNote renders the " (resolved: <path>)" suffix that edit and write
// results carry when the file actually touched is not the path the caller named.
// Relative paths, "~" expansion, worktree redirects and fuzzy filename matches
// all land the write somewhere other than the literal request; Issue 3 showed a
// session where the model was working with two paths for the same content and
// could not tell which file had been written. The suffix makes the resolved
// destination visible on the result's summary line.
func resolvedPathNote(resolvedPath, requestedPath string) string {
	if resolvedPath == "" {
		return ""
	}
	if filepath.Clean(resolvedPath) == filepath.Clean(NormalizeFileToolPath(requestedPath)) {
		return ""
	}
	return fmt.Sprintf(" (resolved: %s)", resolvedPath)
}
