// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"
)

func TestEditor_Submit_ExpandsPasteMarkers(t *testing.T) {
	var submitted string
	e := NewEditor()
	e.SetOnSubmit(func(text string) { submitted = text })
	e.SetFocused(true)
	e.pastes = map[int]string{1: "line one\nline two\nline three"}
	e.SetText("before [paste #1 +2 lines] after")
	e.HandleInput(KeyEnter)

	want := "before line one\nline two\nline three after"
	if submitted != want {
		t.Errorf("submitted = %q, want %q", submitted, want)
	}
}

func TestEditor_Submit_KeepsInlineText(t *testing.T) {
	var submitted string
	e := NewEditor()
	e.SetOnSubmit(func(text string) { submitted = text })
	e.SetFocused(true)
	e.SetText("plain text")
	e.HandleInput(KeyEnter)

	if submitted != "plain text" {
		t.Errorf("submitted = %q, want %q", submitted, "plain text")
	}
}

func TestEditor_ExpandPasteMarkers_MultipleMarkers(t *testing.T) {
	e := NewEditor()
	e.pastes = map[int]string{
		1: "first paste",
		2: "second paste",
	}
	got := e.expandPasteMarkers("[paste #1 +1 lines] and [paste #2 13 chars]")
	want := "first paste and second paste"
	if got != want {
		t.Errorf("expandPasteMarkers = %q, want %q", got, want)
	}
}

func TestEditor_ExpandPasteMarkers_UnknownMarker(t *testing.T) {
	e := NewEditor()
	got := e.expandPasteMarkers("text [paste #99 +1 lines] more")
	want := "text [paste #99 +1 lines] more"
	if got != want {
		t.Errorf("expandPasteMarkers = %q, want %q", got, want)
	}
}

func TestEditor_ExpandPasteMarkers_EmptyPastes(t *testing.T) {
	e := NewEditor()
	got := e.expandPasteMarkers("no pastes here")
	if got != "no pastes here" {
		t.Errorf("expandPasteMarkers = %q, want unchanged", got)
	}
}

func TestParsePasteMarkerID(t *testing.T) {
	tests := []struct {
		marker string
		wantID int
		wantOK bool
	}{
		{"[paste #1 +2 lines]", 1, true},
		{"[paste #12 100 chars]", 12, true},
		{"[paste #0 +1 lines]", 0, false},
		{"not a marker", 0, false},
		{"[paste #abc +1 lines]", 0, false},
	}
	for _, tc := range tests {
		gotID, gotOK := parsePasteMarkerID(tc.marker)
		if gotID != tc.wantID || gotOK != tc.wantOK {
			t.Errorf("parsePasteMarkerID(%q) = (%d, %v), want (%d, %v)", tc.marker, gotID, gotOK, tc.wantID, tc.wantOK)
		}
	}
}

func TestEditor_PasteMarkerContainsNewlines(t *testing.T) {
	var submitted string
	e := NewEditor()
	e.SetOnSubmit(func(text string) { submitted = text })
	e.SetFocused(true)
	e.pastes = map[int]string{1: "alpha\nbeta\ngamma"}
	e.SetText("start [paste #1 +2 lines] end")
	e.HandleInput(KeyEnter)

	if !strings.Contains(submitted, "\n") {
		t.Errorf("submitted text should preserve pasted newlines: %q", submitted)
	}
	if strings.Contains(submitted, "[paste #") {
		t.Errorf("submitted text should not contain paste marker: %q", submitted)
	}
}
