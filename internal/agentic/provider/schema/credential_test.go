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
	tests := []struct{ name, raw, wantKey, wantErr string }{
		{"valid plain", "sk-test-123", "sk-test-123", ""},
		{"valid all printable", "a!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~Z", "a!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~Z", ""},
		{"trims leading whitespace", "  sk-test", "sk-test", ""}, {"trims trailing whitespace", "sk-test\n", "sk-test", ""}, {"trims both sides", "\t sk-test-9 \n", "sk-test-9", ""},
		{"empty", "", "", "empty"}, {"whitespace only", "   ", "", "empty"}, {"interior space", "bad key", "", "printable ASCII"}, {"tab interior", "bad\tkey", "", "printable ASCII"}, {"unicode", "sk-ключ", "", "printable ASCII"}, {"control char", "sk-test\x01", "", "printable ASCII"}, {"non-breaking space", "sk\u00a0test", "", "printable ASCII"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { assertAPIKeyCase(t, tt.raw, tt.wantKey, tt.wantErr) })
	}
}

func assertAPIKeyCase(t *testing.T, raw, wantKey, wantErr string) {
	got, err := ValidateAPIKey("OPENAI_API_KEY", raw)
	if wantErr == "" {
		if err != nil {
			t.Fatalf("ValidateAPIKey(%q) unexpected error: %v", raw, err)
		}
		if got != wantKey {
			t.Errorf("ValidateAPIKey(%q) = %q, want %q", raw, got, wantKey)
		}
		return
	}
	if err == nil {
		t.Fatalf("ValidateAPIKey(%q) expected error containing %q", raw, wantErr)
	}
	if !strings.Contains(err.Error(), wantErr) {
		t.Errorf("ValidateAPIKey(%q) error = %q, want %q", raw, err, wantErr)
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
