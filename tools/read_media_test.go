// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadMediaFileSchema(t *testing.T) {
	tool := &ReadMediaFileTool{}
	schema := tool.Schema()
	if schema.Name != "read_media_file" {
		t.Errorf("name = %q", schema.Name)
	}
}

func TestReadMediaFileMissingPath(t *testing.T) {
	tool := &ReadMediaFileTool{}
	_, err := tool.Execute(`{}`)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestReadMediaFileImage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "img.png")
	if err := os.WriteFile(path, []byte("pngdata"), 0644); err != nil {
		t.Fatal(err)
	}
	tool := &ReadMediaFileTool{}
	out, err := tool.Execute(`{"path": "` + path + `"}`)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !contains(out, "image/png") {
		t.Errorf("output missing mime: %q", out)
	}
}

func TestReadMediaFileVideoRejection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vid.mp4")
	if err := os.WriteFile(path, []byte("mp4data"), 0644); err != nil {
		t.Fatal(err)
	}
	tool := &ReadMediaFileTool{VisionModel: func() bool { return false }}
	_, err := tool.Execute(`{"path": "` + path + `"}`)
	if err == nil {
		t.Fatal("expected error for video without vision")
	}
}

func contains(s, sub string) bool {
	return len(sub) <= len(s) && s[:len(s)-len(sub)+1] != "" && findSub(s, sub)
}

func findSub(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
