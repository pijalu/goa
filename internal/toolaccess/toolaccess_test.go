// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package toolaccess

import (
	"testing"
)

func TestConflict(t *testing.T) {
	tests := []struct {
		name string
		a, b Access
		want bool
	}{
		{
			name: "same read path — no conflict",
			a:    Access{ReadPaths: []string{"/a.go"}},
			b:    Access{ReadPaths: []string{"/a.go"}},
			want: false,
		},
		{
			name: "different read paths — no conflict",
			a:    Access{ReadPaths: []string{"/a.go"}},
			b:    Access{ReadPaths: []string{"/b.go"}},
			want: false,
		},
		{
			name: "read + write same path — conflict",
			a:    Access{ReadPaths: []string{"/a.go"}},
			b:    Access{WritePaths: []string{"/a.go"}},
			want: true,
		},
		{
			name: "write + write same path — conflict",
			a:    Access{WritePaths: []string{"/a.go"}},
			b:    Access{WritePaths: []string{"/a.go"}},
			want: true,
		},
		{
			name: "write different paths — no conflict",
			a:    Access{WritePaths: []string{"/a.go"}},
			b:    Access{WritePaths: []string{"/b.go"}},
			want: false,
		},
		{
			name: "same shell category — conflict",
			a:    Access{Category: "shell"},
			b:    Access{Category: "shell"},
			want: true,
		},
		{
			name: "different categories — no conflict",
			a:    Access{Category: "shell"},
			b:    Access{Category: "network"},
			want: false,
		},
		{
			name: "shell + file read — no conflict",
			a:    Access{Category: "shell"},
			b:    Access{ReadPaths: []string{"/a.go"}},
			want: false,
		},
		{
			name: "write + shell — no conflict",
			a:    Access{WritePaths: []string{"/a.go"}},
			b:    Access{Category: "shell"},
			want: false,
		},
		{
			name: "empty accesses — no conflict",
			a:    Access{},
			b:    Access{},
			want: false,
		},
		{
			name: "multiple reads same file — no conflict",
			a:    Access{ReadPaths: []string{"/a.go", "/b.go"}},
			b:    Access{ReadPaths: []string{"/a.go", "/c.go"}},
			want: false,
		},
		{
			name: "read multi + write one overlapping — conflict",
			a:    Access{ReadPaths: []string{"/a.go", "/b.go", "/c.go"}},
			b:    Access{WritePaths: []string{"/b.go"}},
			want: true,
		},
		{
			name: "empty category string (same) — no conflict",
			a:    Access{Category: ""},
			b:    Access{Category: ""},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Conflict(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("Conflict() = %v, want %v\n  a=%+v\n  b=%+v", got, tt.want, tt.a, tt.b)
			}
			// Conflict is symmetric.
			gotRev := Conflict(tt.b, tt.a)
			if gotRev != tt.want {
				t.Errorf("Conflict(b, a) = %v, want %v (symmetric)", gotRev, tt.want)
			}
		})
	}
}

func TestAccessorInterface(t *testing.T) {
	// Verify that a concrete type can implement Accessor.
	var _ Accessor = (*mockAccessor)(nil)
}

type mockAccessor struct {
	access Access
}

func (m *mockAccessor) Access(_ string) Access {
	return m.access
}
