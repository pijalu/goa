// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package export

import (
	"strings"
	"testing"
)

func TestRedactYAML(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "api_key",
			in:   "openai:\n  api_key: sk-secret123\n  model: gpt-4o",
			want: "openai:\n  api_key: [REDACTED]\n  model: gpt-4o",
		},
		{
			name: "token",
			in:   "token: bearer-abc\nother: value",
			want: "token: [REDACTED]\nother: value",
		},
		{
			name: " preserves comment",
			in:   "api_key: secret # keep this note",
			want: "api_key: [REDACTED] # keep this note",
		},
		{
			name: "no secret",
			in:   "model: gpt-4o\nenabled: true",
			want: "model: gpt-4o\nenabled: true",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RedactYAML(tc.in)
			if got != tc.want {
				t.Errorf("RedactYAML() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRedactJSON(t *testing.T) {
	in := `{
  "provider": "openai",
  "api_key": "sk-secret",
  "token": "tok",
  "nested": {
    "secret": "hidden"
  }
}`
	got := RedactJSON(in)
	if strings.Contains(got, "sk-secret") || strings.Contains(got, "\"tok\"") || strings.Contains(got, "hidden") {
		t.Errorf("RedactJSON did not redact secrets: %s", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Errorf("RedactJSON missing redaction marker: %s", got)
	}
}

func TestRedactText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "bearer",
			in:   "Authorization: Bearer abc123",
			want: "Authorization: Bearer [REDACTED]",
		},
		{
			name: "api-key header",
			in:   "api-key: super-secret",
			want: "api-key: [REDACTED]",
		},
		{
			name: "no secret",
			in:   "model: gpt-4o",
			want: "model: gpt-4o",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RedactText(tc.in)
			if got != tc.want {
				t.Errorf("RedactText() = %q, want %q", got, tc.want)
			}
		})
	}
}
