// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"reflect"
	"testing"
)

func TestExtractImagePaths(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []string
	}{
		{
			name: "text with image path",
			text: "describe this file /tmp/screenshot.png please",
			want: []string{"/tmp/screenshot.png"},
		},
		{
			name: "multiple image paths",
			text: "/tmp/a.png /tmp/b.jpg /tmp/c.webp",
			want: []string{"/tmp/a.png", "/tmp/b.jpg", "/tmp/c.webp"},
		},
		{
			name: "no images",
			text: "just some regular text",
			want: nil,
		},
		{
			name: "case insensitive",
			text: "/tmp/photo.JPEG",
			want: []string{"/tmp/photo.JPEG"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractImagePaths(tc.text)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("extractImagePaths(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

func TestStripImagePaths(t *testing.T) {
	text := "describe this file /tmp/screenshot.png please"
	got := stripImagePaths(text)
	want := "describe this file please"
	if got != want {
		t.Errorf("stripImagePaths(%q) = %q, want %q", text, got, want)
	}
}

func TestStripImagePaths_PreservesNewlines(t *testing.T) {
	text := "line one\nline two /tmp/img.png\nline three"
	got := stripImagePaths(text)
	want := "line one\nline two\nline three"
	if got != want {
		t.Errorf("stripImagePaths(%q) = %q, want %q", text, got, want)
	}
}

func TestSplitUserInput(t *testing.T) {
	text := "compare /tmp/a.png and /tmp/b.png"
	msg, images := splitUserInput(text)
	if msg != "compare and" {
		t.Errorf("message = %q, want %q", msg, "compare and")
	}
	want := []string{"/tmp/a.png", "/tmp/b.png"}
	if !reflect.DeepEqual(images, want) {
		t.Errorf("images = %v, want %v", images, want)
	}
}

func TestSplitUserInput_PreservesNewlines(t *testing.T) {
	text := "first line\nsecond line /tmp/a.png\nthird line"
	msg, images := splitUserInput(text)
	want := "first line\nsecond line\nthird line"
	if msg != want {
		t.Errorf("message = %q, want %q", msg, want)
	}
	wantImages := []string{"/tmp/a.png"}
	if !reflect.DeepEqual(images, wantImages) {
		t.Errorf("images = %v, want %v", images, wantImages)
	}
}

func TestHandlePendingMainInput_AcceptsSlashPrefixedText(t *testing.T) {
	var received string
	a := &App{pendingInput: &inputRequest{
		prompt:   "objective",
		onSubmit: func(s string) { received = s },
	}}

	if !a.handlePendingMainInput("/src/main.go fix the bug") {
		t.Fatal("expected handlePendingMainInput to consume the input")
	}
	if received != "/src/main.go fix the bug" {
		t.Errorf("received = %q, want the slash-prefixed objective", received)
	}
	if a.pendingInput != nil {
		t.Error("pendingInput should be cleared after handling")
	}
}

func TestHandlePendingMainInput_NoPending(t *testing.T) {
	a := &App{}
	if a.handlePendingMainInput("anything") {
		t.Error("expected false when no pending request")
	}
}
