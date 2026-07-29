// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import "testing"

// TestIsLocalEndpoint pins the endpoint classification used by the model
// list to paint local-provider models green: any localhost / 127.0.0.1
// endpoint (LM Studio, llama.cpp, Ollama, ...) is local; everything else
// is remote.
func TestIsLocalEndpoint(t *testing.T) {
	cases := []struct {
		name     string
		endpoint string
		want     bool
	}{
		{"lm studio localhost", "http://localhost:1234/v1", true},
		{"lm studio loopback", "http://127.0.0.1:1234/v1", true},
		{"ollama localhost", "http://localhost:11434/v1", true},
		{"ollama loopback", "http://127.0.0.1:11434/v1", true},
		{"llama.cpp custom port", "http://localhost:8080/v1", true},
		{"localhost uppercase", "HTTP://LOCALHOST:1234/V1", true},
		{"openai", "https://api.openai.com/v1", false},
		{"z.ai", "https://api.z.ai/api/coding/paas/v4", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsLocalEndpoint(tc.endpoint); got != tc.want {
				t.Errorf("IsLocalEndpoint(%q) = %v, want %v", tc.endpoint, got, tc.want)
			}
		})
	}
}
