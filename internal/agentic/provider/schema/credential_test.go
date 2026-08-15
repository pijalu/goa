// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package schema

import (
	"errors"
	"strings"
	"testing"
)

// TestValidateAPIKey mirrors the deepseek-harness api-key.ts contract:
// surrounding whitespace is trimmed silently, then the key must be non-empty
// printable ASCII (0x21..0x7E) with no spaces.
func TestValidateAPIKey(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantKey string
		wantErr string // substring of the error reason, "" for success
	}{
		{name: "valid plain", raw: "sk-test-123", wantKey: "sk-test-123"},
		{name: "valid all printable", raw: "a!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~Z", wantKey: "a!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~Z"},
		{name: "trims leading whitespace", raw: "  sk-test", wantKey: "sk-test"},
		{name: "trims trailing whitespace", raw: "sk-test\n", wantKey: "sk-test"},
		{name: "trims both sides", raw: "\t sk-test-9 \n", wantKey: "sk-test-9"},
		{name: "empty", raw: "", wantErr: "empty"},
		{name: "whitespace only", raw: "   ", wantErr: "empty"},
		{name: "interior space", raw: "bad key", wantErr: "printable ASCII"},
		{name: "tab interior", raw: "bad\tkey", wantErr: "printable ASCII"},
		{name: "unicode", raw: "sk-ключ", wantErr: "printable ASCII"},
		{name: "control char", raw: "sk-test\x01", wantErr: "printable ASCII"},
		{name: "non-breaking space", raw: "sk\u00a0test", wantErr: "printable ASCII"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ValidateAPIKey("OPENAI_API_KEY", tt.raw)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateAPIKey(%q) unexpected error: %v", tt.raw, err)
				}
				if got != tt.wantKey {
					t.Errorf("ValidateAPIKey(%q) = %q, want %q", tt.raw, got, tt.wantKey)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateAPIKey(%q) expected error containing %q, got nil", tt.raw, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("ValidateAPIKey(%q) error = %q, want substring %q", tt.raw, err.Error(), tt.wantErr)
			}
		})
	}
}

// TestInvalidCredentialError_NeverIncludesKeyMaterial guards the DS5 rule:
// errors must name the config entry point and never echo the key itself.
func TestInvalidCredentialError_NeverIncludesKeyMaterial(t *testing.T) {
	secret := "sk-super-secret-material-12345"
	_, err := ValidateAPIKey("OPENAI_API_KEY", secret+" with space")
	var inv *InvalidCredentialError
	if !errors.As(err, &inv) {
		t.Fatalf("expected *InvalidCredentialError, got %T", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("error must never include the key material: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "OPENAI_API_KEY") {
		t.Errorf("error must name the config entry point: %q", err.Error())
	}
}

// TestInvalidCredentialError_EmptyReason verifies the empty-after-trim case is
// reported as an InvalidCredential (not Missing): the variable WAS set, but to
// nothing usable.
func TestInvalidCredentialError_EmptyReason(t *testing.T) {
	_, err := ValidateAPIKey("OPENAI_API_KEY", "   ")
	var inv *InvalidCredentialError
	if !errors.As(err, &inv) {
		t.Fatalf("expected *InvalidCredentialError, got %T", err)
	}
	if inv.Source != "OPENAI_API_KEY" {
		t.Errorf("Source = %q, want OPENAI_API_KEY", inv.Source)
	}
	if !strings.Contains(inv.Reason, "empty") {
		t.Errorf("Reason = %q, want mention of empty", inv.Reason)
	}
}

func TestMissingCredentialError_ListsSources(t *testing.T) {
	err := &MissingCredentialError{
		Provider: "deepseek",
		Sources:  []string{"options.api_key", "DEEPSEEK_API_KEY"},
	}
	msg := err.Error()
	if !strings.Contains(msg, "deepseek") {
		t.Errorf("message must name the provider: %q", msg)
	}
	for _, src := range []string{"options.api_key", "DEEPSEEK_API_KEY"} {
		if !strings.Contains(msg, src) {
			t.Errorf("message must list checked source %q: %q", src, msg)
		}
	}
	if strings.Contains(msg, "sk-") {
		t.Errorf("message must never include key material: %q", msg)
	}
}

// TestValidateAPIKey_EmptySourceNamesTheEntryPoint verifies that even the
// whitespace-only case names the source so the user knows which config knob
// to fix.
func TestValidateAPIKey_EmptySourceNamesTheEntryPoint(t *testing.T) {
	_, err := ValidateAPIKey("ANTHROPIC_API_KEY", " \n ")
	if err == nil || !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
		t.Fatalf("expected error naming ANTHROPIC_API_KEY, got %v", err)
	}
}
