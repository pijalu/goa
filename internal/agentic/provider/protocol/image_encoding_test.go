// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package protocol

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

// imageBlock builds the content blocks a pasted image produces: one text block
// and one image block whose ImageData is a filesystem *path* (how agent history
// carries an attachment).
func imageBlock(t *testing.T) ([]schema.ContentBlock, string) {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 3, 3))); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "paste.png")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	blocks := []schema.ContentBlock{
		{Type: schema.ContentBlockText, Text: "what is this"},
		{Type: schema.ContentBlockImage, ImageData: path},
	}
	return blocks, base64.StdEncoding.EncodeToString(buf.Bytes())
}

// marshalled flattens a conversion result into JSON for assertions.
func marshalled(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestImageEncoding_AllProtocolsEmbedBase64 is the regression test for the
// Anthropic/Google/Bedrock image bug: their converters put the raw filesystem
// path into the Base64 payload and left media_type empty, so every vision
// request on those APIs was malformed.
func TestImageEncoding_AllProtocolsEmbedBase64(t *testing.T) {
	blocks, wantB64 := imageBlock(t)

	tests := []struct {
		name       string
		j          string
		wantType   []string // substrings that must appear
		wantAbsent []string // substrings that must NOT appear
	}{
		{
			name:     "anthropic",
			j:        marshalled(t, convertAnthropicContentBlocks(blocks)),
			wantType: []string{`"type":"base64"`, `"media_type":"image/png"`, wantB64},
		},
		{
			name:     "google",
			j:        marshalled(t, convertGoogleParts(blocks, schema.RoleUser)),
			wantType: []string{`"mimeType":"image/png"`, wantB64},
		},
		{
			name:     "openai completions",
			j:        marshalled(t, buildUserContent(blocks)),
			wantType: []string{"data:image/png;base64," + wantB64},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if strings.Contains(tc.j, `"data":"/`) || strings.Contains(tc.j, `"data": "/`) {
				t.Errorf("payload still carries a path instead of Base64: %s", tc.j)
			}
			for _, want := range tc.wantType {
				if !strings.Contains(tc.j, want) {
					t.Errorf("missing %q in %s", want, tc.j)
				}
			}
		})
	}
}

// TestImageEncoding_UnreadableBecomesPlaceholder means an attachment that cannot
// be read is visible in the transcript instead of being dropped (an empty image
// part is rejected by every provider, which used to hide the loss entirely).
func TestImageEncoding_UnreadableBecomesPlaceholder(t *testing.T) {
	blocks := []schema.ContentBlock{
		{Type: schema.ContentBlockImage, ImageData: filepath.Join(t.TempDir(), "gone.png")},
	}

	tests := []struct {
		name string
		j    string
	}{
		{"anthropic", marshalled(t, convertAnthropicContentBlocks(blocks))},
		{"google", marshalled(t, convertGoogleParts(blocks, schema.RoleUser))},
		{"openai completions", marshalled(t, buildUserContent(blocks))},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(tc.j, schema.ImageUnavailablePlaceholder) {
				t.Errorf("expected the unavailable placeholder in %s", tc.j)
			}
		})
	}
}
