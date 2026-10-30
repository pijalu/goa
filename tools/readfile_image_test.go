// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFileSchemaIncludeImageIsExplicitOption(t *testing.T) {
	schema := (&ReadFileTool{}).Schema()
	properties, ok := schema.Schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties has type %T", schema.Schema["properties"])
	}
	option, ok := properties["include_image"].(map[string]any)
	if !ok {
		t.Fatal("include_image is missing from the read schema")
	}
	if option["type"] != "boolean" {
		t.Errorf("include_image type = %v, want boolean", option["type"])
	}
	required, _ := schema.Schema["required"].([]string)
	for _, name := range required {
		if name == "include_image" {
			t.Fatal("include_image must remain optional")
		}
	}
}

type readImageFormatCase struct {
	name     string
	ext      string
	mime     string
	contents func(t *testing.T) []byte
}

func TestReadFileIncludeImageAttachesSupportedFormats(t *testing.T) {
	tests := []readImageFormatCase{
		{name: "png", ext: ".png", mime: "image/png", contents: func(t *testing.T) []byte {
			return encodeReadImage(t, png.Encode)
		}},
		{name: "jpeg", ext: ".jpg", mime: "image/jpeg", contents: func(t *testing.T) []byte {
			return encodeReadImage(t, func(w io.Writer, img image.Image) error {
				return jpeg.Encode(w, img, nil)
			})
		}},
		{name: "gif", ext: ".gif", mime: "image/gif", contents: func(t *testing.T) []byte {
			return encodeReadImage(t, func(w io.Writer, img image.Image) error {
				return gif.Encode(w, img, nil)
			})
		}},
		{name: "webp", ext: ".webp", mime: "image/webp", contents: readWebPFixture},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { runReadImageFormatCase(t, tt) })
	}
}

func readWebPFixture(t *testing.T) []byte {
	t.Helper()
	// A valid 1x1 lossy WebP. WebP has no standard-library encoder.
	data, err := base64.StdEncoding.DecodeString("UklGRiQAAABXRUJQVlA4IBgAAAAwAQCdASoBAAEAAwA0JaQAA3AA/vuUAAA=")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func runReadImageFormatCase(t *testing.T, tt readImageFormatCase) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "image"+tt.ext)
	if err := os.WriteFile(path, tt.contents(t), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := (&ReadFileTool{}).ExecuteWithResult(readImageInput(t, path, true))
	if err != nil {
		t.Fatalf("ExecuteWithResult: %v", err)
	}
	if len(result.Images) != 1 || result.Images[0] != path {
		t.Fatalf("Images = %q, want [%q]", result.Images, path)
	}
	if !strings.Contains(result.Output, tt.mime) {
		t.Errorf("output %q does not identify %s", result.Output, tt.mime)
	}
	if strings.Contains(strings.ToLower(result.Output), "base64") {
		t.Errorf("tool output must carry an image path, not encoded bytes: %q", result.Output)
	}
}

func TestReadFileIncludeImageRejectsInvalidContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-an-image.png")
	if err := os.WriteFile(path, []byte("plain text, despite the extension"), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := &ReadFileTool{}
	result, err := tool.ExecuteWithResult(readImageInput(t, path, true))
	if err == nil {
		t.Fatal("include_image on invalid image content must fail")
	}
	if !strings.Contains(err.Error(), "invalid_image") {
		t.Errorf("error = %v, want invalid_image", err)
	}
	if len(result.Images) != 0 {
		t.Errorf("invalid image returned attachments: %q", result.Images)
	}
}

func TestReadFileImageDefaultKeepsBinaryNoticeAndNoAttachment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image.png")
	if err := os.WriteFile(path, encodeReadImage(t, png.Encode), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := &ReadFileTool{}
	result, err := tool.ExecuteWithResult(readImageInput(t, path, false))
	if err != nil {
		t.Fatalf("ExecuteWithResult: %v", err)
	}
	if !strings.Contains(result.Output, "binary file") {
		t.Errorf("default output = %q, want existing binary notice", result.Output)
	}
	if len(result.Images) != 0 {
		t.Errorf("default read attached images without explicit opt-in: %q", result.Images)
	}
}

func TestReadFileIncludeImageMissingPathKeepsReadError(t *testing.T) {
	tool := &ReadFileTool{}
	_, err := tool.ExecuteWithResult(readImageInput(t, filepath.Join(t.TempDir(), "missing.png"), true))
	if err == nil || !strings.Contains(err.Error(), "file_not_found") {
		t.Errorf("error = %v, want existing file_not_found result", err)
	}
}

func encodeReadImage(t *testing.T, encode func(io.Writer, image.Image) error) []byte {
	t.Helper()
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	if err := encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func readImageInput(t *testing.T, path string, include bool) string {
	t.Helper()
	input, err := json.Marshal(map[string]any{"path": path, "include_image": include})
	if err != nil {
		t.Fatal(err)
	}
	return string(input)
}
