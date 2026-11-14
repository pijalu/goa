// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pijalu/agent-mcp/client"
)

// MCP binary content (image/audio parts, resource blobs) arrives as base64
// inside the shaped JSON payload. Shipping it to the model burns the token
// budget on undecodable text, and vision models never get the pixels at all.
//
// The extraction itself lives in the agent-mcp client library
// (client.ApplyBinariesPolicy): the generic walk, base64 decoding, and the
// marker vocabulary have one implementation there, shared with the CLI.
// This file supplies goa's side of that contract — the persistence half of
// client.BinaryStore (files under a tool-owned directory) — plus the image
// routing the vision pipeline consumes.

// extractedBinary is one binary part the store persisted: what it is and
// where it went.
type extractedBinary struct {
	MIME    string
	Path    string
	IsImage bool
}

// artifactStore persists binary artifacts the client library extracts, and
// remembers what it saved so the tool can clean up on Close and attach
// images natively. It implements client.BinaryStore.
type artifactStore struct {
	dir   string
	files []string
	saved []extractedBinary
}

// Store persists one artifact under the store's directory with a
// deterministic sequential name (part-01.png, …); the extension derives
// from the MIME type via the shared library table, so markers always name
// files the model can open with a predictable suffix.
func (s *artifactStore) Store(artifact client.BinaryArtifact) (string, error) {
	name := fmt.Sprintf("part-%02d%s", len(s.files)+1, client.ExtensionFor(artifact.MimeType))
	path := filepath.Join(s.dir, name)
	if err := os.WriteFile(path, artifact.Data, 0o600); err != nil {
		return "", err
	}
	s.files = append(s.files, path)
	s.saved = append(s.saved, extractedBinary{
		MIME:    artifact.MimeType,
		Path:    path,
		IsImage: strings.HasPrefix(artifact.MimeType, "image/"),
	})
	return path, nil
}

// extract rewrites the binary content parts of result into files under
// the store's directory via the client library's file policy, replacing each
// payload with a short pointer marker, and returns the binaries persisted by
// this call (files accumulates across calls for Close; callers see one
// call's artifacts, in encounter order). Failures degrade to markers inside
// the library, never to a failed call.
func (s *artifactStore) extract(result any) []extractedBinary {
	before := len(s.saved)
	if _, err := client.ApplyBinariesPolicy(result, client.BinariesFile, s); err != nil {
		// The file policy degrades every per-part failure to a marker, so
		// an error here means an unusable store; keep whatever was saved
		// rather than failing a call whose result is already rewritten.
		return s.saved[before:]
	}
	return s.saved[before:]
}
