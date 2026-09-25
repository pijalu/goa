// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package common

import (
	"bytes"
	"errors"
	"fmt"
	"os"
)

// ErrWriteVerifyFailed identifies a failed post-write read-back verification.
// The errors returned by WriteFileVerified/verifyWrite wrap it, so callers can
// use errors.Is to tell a verification failure — the write reported success but
// the bytes on disk differ — apart from a plain write error such as a
// permission or disk-full failure.
var ErrWriteVerifyFailed = errors.New("write verification failed")

// WriteFileVerified writes content to path with perm and immediately reads the
// file back, confirming that the bytes on disk are the bytes written. Every
// mutating file tool should write through it: reporting success while the file
// still holds its previous content is the worst failure mode a coding agent can
// hit — the model then edits stale content, or believes a change landed that
// never did (edit-tool Issue 3: "the edit said it worked, the file never
// changed").
//
// The read-back is a cheap guard, not a durability barrier: it proves the write
// landed in this exact file (catching wrong-path writes, redirects, and an
// external writer racing the tool), not that the kernel flushed the blocks to
// stable storage. The content is already in the page cache from the write
// itself, and on the edit path the file has just been read in full, so the extra
// copy is negligible next to the cost of a false success.
//
// perm only applies when the file is created; os.WriteFile leaves the mode of an
// existing file untouched, so overwriting a 0600 file keeps 0600.
func WriteFileVerified(path string, content []byte, perm os.FileMode) error {
	if err := os.WriteFile(path, content, perm); err != nil {
		return err
	}
	return verifyWrite(path, content)
}

// verifyWrite reads path back and compares it byte-for-byte with want. A read
// failure or any mismatch is reported as an error wrapping
// ErrWriteVerifyFailed that names the path and both byte counts, so the tool
// layer can turn it into a write_verify_failed error the model can act on
// instead of trusting an unconfirmed write.
func verifyWrite(path string, want []byte) error {
	got, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%w: cannot read %s back: %v", ErrWriteVerifyFailed, path, err)
	}
	if !bytes.Equal(got, want) {
		return fmt.Errorf("%w: %s holds %d byte(s) after writing %d",
			ErrWriteVerifyFailed, path, len(got), len(want))
	}
	return nil
}
