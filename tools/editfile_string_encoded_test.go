// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestEditFileTool_StringEncodedBatchCompatibility(t *testing.T) {
	edits := []map[string]any{
		{"operation": "replace", "old_string": "one", "new_string": "ONE"},
		{"operation": "replace", "old_string": "three", "new_string": "THREE"},
	}
	arrayJSON, err := json.Marshal(edits)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		edits   any
		want    string
		wantErr bool
	}{
		{name: "native array", edits: edits, want: "ONE two THREE"},
		{name: "one encoded array", edits: string(arrayJSON), want: "ONE two THREE"},
		{name: "malformed encoded array", edits: "[{", wantErr: true},
		{name: "nested encoding", edits: string(mustJSON(t, string(arrayJSON))), wantErr: true},
		{name: "wrong element shape", edits: `["replace"]`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runEditFileBatchCompatibilityCase(t, tt.edits, tt.want, tt.wantErr)
		})
	}
}

func runEditFileBatchCompatibilityCase(t *testing.T, edits any, want string, wantErr bool) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.txt")
	if err := os.WriteFile(path, []byte("one two three"), 0o600); err != nil {
		t.Fatal(err)
	}
	input := mustJSON(t, map[string]any{"path": path, "edits": edits})
	_, err := (&EditFileTool{}).Execute(string(input))
	if wantErr {
		if err == nil {
			t.Fatal("expected invalid encoded batch to fail")
		}
		want = "one two three"
	} else if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertEditFileBatchContent(t, path, want)
}

func assertEditFileBatchContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("file = %q, want %q", data, want)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
