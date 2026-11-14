// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"encoding/base64"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// MCP binary content (image/audio parts, resource blobs) arrives as base64
// inside the shaped JSON payload. Shipping it to the model burns the token
// budget on undecodable text, and vision models never get the pixels at all.
// The extractor turns each binary part into a file and rewrites the part in
// place to a short marker, so the JSON output stays small and the agent
// pipeline can attach images natively (ToolResult.Images) or let the model
// read artifacts via read_media/shell.
//
// The walk is deliberately generic: anything that looks like a base64 part
// (a `data`/`blob` string next to a `mimeType`) is extracted regardless of
// which container holds it, so new MCP content shapes keep working without
// changes here (open/closed).

// extractedBinary is one decoded binary part: what it is and where it went.
type extractedBinary struct {
	MIME    string
	Path    string
	Size    int
	IsImage bool
}

// marker renders the inline text that replaces the base64 in the JSON output.
func (b extractedBinary) marker() string {
	return fmt.Sprintf("[binary: %s → %s (%s)]", b.MIME, b.Path, humanSize(b.Size))
}

// binaryExtractor decodes base64 content parts of one MCP result into files.
// Files land in dir, which the tool owns and cleans up on Close.
type binaryExtractor struct {
	dir   string
	files []string
	seq   int
}

// newBinaryExtractor targets dir for extracted artifacts.
func newBinaryExtractor(dir string) *binaryExtractor {
	return &binaryExtractor{dir: dir}
}

// extract walks the result value, extracting every base64 binary part it
// finds (mutating the maps in place) and returns the extracted binaries in
// encounter order. A part that cannot be decoded is replaced by an error
// marker rather than failing the call: the payload is already malformed and
// the raw base64 has no consumer.
func (x *binaryExtractor) extract(result any) []extractedBinary {
	var found []extractedBinary
	x.walk(result, &found)
	return found
}

// walk recurses through decoded JSON containers; leaf objects may carry
// binary parts.
func (x *binaryExtractor) walk(value any, found *[]extractedBinary) {
	switch v := value.(type) {
	case map[string]any:
		if part, ok := x.part(v); ok {
			bin, marker := x.store(part)
			*found = append(*found, bin)
			rewritePart(v, marker)
			return
		}
		for _, key := range walkKeys(v) {
			x.walk(v[key], found)
		}
	case []any:
		for _, item := range v {
			x.walk(item, found)
		}
	}
}

// part recognizes a decoded JSON object as a base64 binary part: a `data`
// (content part) or `blob` (resource contents) string plus a mimeType.
func (x *binaryExtractor) part(entry map[string]any) (binaryPart, bool) {
	for _, dataKey := range []string{"data", "blob"} {
		payload, ok := entry[dataKey].(string)
		if !ok || payload == "" {
			continue
		}
		mimeType, ok := entry["mimeType"].(string)
		if !ok || mimeType == "" {
			continue
		}
		return binaryPart{dataKey: dataKey, payload: payload, mimeType: mimeType}, true
	}
	return binaryPart{}, false
}

// binaryPart is one recognized base64 carrier in the payload.
type binaryPart struct {
	dataKey  string
	payload  string
	mimeType string
}

// store decodes the part, writes it under the extractor's dir, and returns
// the extracted binary plus the marker text. Undecodable payloads produce a
// zero-size error marker instead of a file.
func (x *binaryExtractor) store(p binaryPart) (extractedBinary, string) {
	raw, err := decodeBase64(p.payload)
	if err != nil {
		bin := extractedBinary{MIME: p.mimeType}
		return bin, fmt.Sprintf("[binary: %s undecodable: %v]", p.mimeType, err)
	}
	path := filepath.Join(x.dir, fmt.Sprintf("%s%s", x.name(), extensionFor(p.mimeType)))
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		bin := extractedBinary{MIME: p.mimeType}
		return bin, fmt.Sprintf("[binary: %s not saved: %v]", p.mimeType, err)
	}
	x.files = append(x.files, path)
	bin := extractedBinary{
		MIME:    p.mimeType,
		Path:    path,
		Size:    len(raw),
		IsImage: strings.HasPrefix(p.mimeType, "image/"),
	}
	return bin, bin.marker()
}

// name returns a short unique file stem; the extractor never inspects the
// payload, so collision avoidance rests on the random suffix of dir.
func (x *binaryExtractor) name() string {
	x.seq++
	return fmt.Sprintf("part-%02d", x.seq)
}

// canonicalExt pins the well-known extensions: the OS mime database
// disagrees across platforms (macOS hands out .jfif for image/jpeg), and
// markers must name files the model can open with a predictable suffix.
var canonicalExt = map[string]string{
	"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif",
	"image/webp": ".webp", "audio/wav": ".wav", "audio/mpeg": ".mp3",
	"audio/ogg": ".ogg", "video/mp4": ".mp4", "video/webm": ".webm",
	"application/pdf": ".pdf",
}

// extensionFor maps a MIME type to a file extension, falling back to the OS
// mime database and then to ".bin".
func extensionFor(mimeType string) string {
	if ext, ok := canonicalExt[strings.ToLower(mimeType)]; ok {
		return ext
	}
	exts, err := mime.ExtensionsByType(mimeType)
	if err != nil || len(exts) == 0 {
		return ".bin"
	}
	return exts[0]
}

// decodeBase64 accepts both standard and URL-safe encodings, with or without
// padding: MCP servers disagree on which variant they ship.
func decodeBase64(payload string) ([]byte, error) {
	if raw, err := base64.StdEncoding.DecodeString(payload); err == nil {
		return raw, nil
	}
	return base64.RawURLEncoding.DecodeString(payload)
}

// rewritePart replaces one binary part with a text part carrying marker, so
// the JSON output stays small and points at the artifact.
func rewritePart(entry map[string]any, marker string) {
	for _, key := range []string{"data", "blob"} {
		delete(entry, key)
	}
	entry["type"] = "text"
	entry["text"] = marker
}

// walkKeys returns the map's keys in sorted order: range over maps is
// randomized in Go, and a deterministic walk keeps extracted file numbering
// stable across runs.
func walkKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// humanSize renders a byte count the way ls -h does, for markers.
func humanSize(n int) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := unit, 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
