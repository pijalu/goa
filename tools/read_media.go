// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
)

// ReadMediaFileTool reads image and video files for vision/multimodal models.
type ReadMediaFileTool struct {
	agentic.BaseTool
	WorktreeMgr *internal.WorktreeManager
	// VisionModel reports whether the active model supports images.
	VisionModel func() bool
}

type readMediaInput struct {
	Path string `json:"path"`
}

// Schema returns the tool schema.
func (t *ReadMediaFileTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "read_media_file",
		Description: "Read an image or video file and return its content for vision-capable models.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path to the media file",
				},
			},
			"required": []string{"path"},
		},
	}
}

// Execute reads the media file.
func (t *ReadMediaFileTool) Execute(input string) (string, error) {
	var p readMediaInput
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		return "", &internal.ToolError{
			Tool: "read_media_file", Type: "invalid_input",
			Detail:   fmt.Sprintf("Cannot parse parameters: %v", err),
			HintText: "Provide valid JSON with a path field.",
		}
	}
	if strings.TrimSpace(p.Path) == "" {
		return "", &internal.ToolError{Tool: "read_media_file", Type: "missing_path", Detail: "path is required", HintText: "Provide a media file path."}
	}

	resolvedPath, err := ResolveToolPath(t.WorktreeMgr, p.Path)
	if err != nil {
		return "", &internal.ToolError{Tool: "read_media_file", Type: "protected_path", Detail: fmt.Sprintf("Cannot read %q", p.Path), HintText: "Choose a path outside protected directories."}
	}

	info, err := os.Stat(resolvedPath)
	if err != nil {
		return "", &internal.ToolError{Tool: "read_media_file", Type: "file_not_found", Detail: fmt.Sprintf("Cannot stat %q: %v", p.Path, err), HintText: "Check the path."}
	}
	if info.IsDir() {
		return "", &internal.ToolError{Tool: "read_media_file", Type: "is_directory", Detail: fmt.Sprintf("%q is a directory", p.Path), HintText: "Provide a file path."}
	}

	const maxSize = 20 * 1024 * 1024
	if info.Size() > maxSize {
		return "", &internal.ToolError{Tool: "read_media_file", Type: "too_large", Detail: fmt.Sprintf("File %q is too large (max 20MB)", p.Path), HintText: "Use a smaller image or video."}
	}

	mime := mimeType(resolvedPath)
	if isVideo(mime) && (t.VisionModel == nil || !t.VisionModel()) {
		return "", &internal.ToolError{Tool: "read_media_file", Type: "vision_required", Detail: "Video files require a vision-capable model", HintText: "Switch to a vision model or use an image."}
	}

	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		return "", &internal.ToolError{Tool: "read_media_file", Type: "read_error", Detail: err.Error(), HintText: "Ensure the file is readable."}
	}

	b64 := base64.StdEncoding.EncodeToString(data)
	return fmt.Sprintf("[read_media_file] %s\nMIME: %s\nSize: %d bytes\nData: data:%s;base64,%s", p.Path, mime, info.Size(), mime, b64), nil
}

func mimeType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".mp4":
		return "video/mp4"
	case ".mov":
		return "video/quicktime"
	case ".webm":
		return "video/webm"
	default:
		return "application/octet-stream"
	}
}

func isVideo(mime string) bool {
	return strings.HasPrefix(mime, "video/")
}

// IsRetryable returns false.
func (t *ReadMediaFileTool) IsRetryable(err error) bool { return false }

// ShortDoc returns a short doc string.
//
//go:embed read_media.short.md read_media.long.md
var read_mediaDocs embed.FS

func (t *ReadMediaFileTool) ShortDoc() string { return readDoc(read_mediaDocs, "read_media.short.md") }
func (t *ReadMediaFileTool) LongDoc() string  { return readDoc(read_mediaDocs, "read_media.long.md") }
func (t *ReadMediaFileTool) Examples() []string {
	return []string{`{"path": "screenshot.png"}`}
}
