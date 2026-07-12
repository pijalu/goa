// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package secrets

import (
	"regexp"
	"strings"
	"testing"
)

func TestScanner_AWSAccessKey(t *testing.T) {
	s := DefaultScanner()
	key := "AKIAIOSFODNN7EXAMPLE"
	matches := s.Scan("key=" + key)
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	if matches[0].SecretType != "aws_access_key_id" {
		t.Errorf("expected aws_access_key_id, got %q", matches[0].SecretType)
	}
	if matches[0].Value != key {
		t.Errorf("expected value %q, got %q", key, matches[0].Value)
	}
}

func TestScanner_GitHubToken(t *testing.T) {
	s := DefaultScanner()
	token := "ghp_1234567890abcdef1234567890abcdef123456"
	matches := s.Scan("token=" + token)
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	if matches[0].SecretType != "github_token" {
		t.Errorf("expected github_token, got %q", matches[0].SecretType)
	}
}

func TestScanner_OpenAIKey(t *testing.T) {
	s := DefaultScanner()
	key := "sk-" + strings.Repeat("a", 48)
	matches := s.Scan(key)
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	if matches[0].SecretType != "openai_api_key" {
		t.Errorf("expected openai_api_key, got %q", matches[0].SecretType)
	}
}

func TestScanner_SlackToken(t *testing.T) {
	s := DefaultScanner()
	token := "xoxb-1234567890123-1234567890123-AbCdEfGhIjKlMnOpQrStUvwx"
	matches := s.Scan("token=" + token)
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	if matches[0].SecretType != "slack_token" {
		t.Errorf("expected slack_token, got %q", matches[0].SecretType)
	}
}

func TestScanner_GoogleAPIKey(t *testing.T) {
	s := DefaultScanner()
	key := "AIza" + strings.Repeat("A", 35)
	matches := s.Scan(key)
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	if matches[0].SecretType != "google_api_key" {
		t.Errorf("expected google_api_key, got %q", matches[0].SecretType)
	}
}

func TestScanner_PrivateKey(t *testing.T) {
	s := DefaultScanner()
	key := "-----BEGIN RSA PRIVATE KEY-----\nMIIabc...\n-----END RSA PRIVATE KEY-----"
	matches := s.Scan(key)
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	if matches[0].SecretType != "private_key" {
		t.Errorf("expected private_key, got %q", matches[0].SecretType)
	}
}

func TestScanner_JWT(t *testing.T) {
	s := DefaultScanner()
	token := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	matches := s.Scan(token)
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	if matches[0].SecretType != "jwt" {
		t.Errorf("expected jwt, got %q", matches[0].SecretType)
	}
}

func TestScanner_NoSecrets(t *testing.T) {
	s := DefaultScanner()
	matches := s.Scan("hello world, no secrets here")
	if len(matches) != 0 {
		t.Errorf("expected 0 matches, got %d: %v", len(matches), matches)
	}
}

func TestScanner_HasSecrets(t *testing.T) {
	s := DefaultScanner()
	if !s.HasSecrets("key=AKIAIOSFODNN7EXAMPLE") {
		t.Error("expected HasSecrets to return true")
	}
	if s.HasSecrets("hello world") {
		t.Error("expected HasSecrets to return false")
	}
}

func TestScanner_MultipleSecrets(t *testing.T) {
	s := DefaultScanner()
	text := "aws=AKIAIOSFODNN7EXAMPLE and openai=sk-" + strings.Repeat("a", 48)
	matches := s.Scan(text)
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(matches))
	}
}

func TestScanner_OverlappingMatches_KeepLonger(t *testing.T) {
	s := DefaultScanner()
	// A private key string may also contain base64-like substrings that
	// could match other detectors; ensure the longer private_key match wins.
	key := "-----BEGIN OPENSSH PRIVATE KEY-----\n" +
		strings.Repeat("AbCdEfGhIjKlMnOpQrStUvWxYz1234567890+/=\n", 5) +
		"-----END OPENSSH PRIVATE KEY-----"
	matches := s.Scan(key)
	if len(matches) == 0 {
		t.Fatal("expected at least one match")
	}
	if matches[0].SecretType != "private_key" {
		t.Errorf("expected private_key to win over overlapping matches, got %q", matches[0].SecretType)
	}
}

func TestScanner_CustomPattern(t *testing.T) {
	s := NewScanner(nil)
	s.AddPattern(Pattern{Name: "custom", Regexp: regexpMust(`\bCUSTOM-[0-9]{4}\b`)})
	matches := s.Scan("token=CUSTOM-1234")
	if len(matches) != 1 || matches[0].SecretType != "custom" {
		t.Errorf("expected custom match, got %v", matches)
	}
}

func TestScanner_MinLenFilter(t *testing.T) {
	s := NewScanner([]Pattern{{
		Name:   "word",
		Regexp: regexpMust(`\b[A-Z]{3,20}\b`),
		MinLen: 8,
	}})
	matches := s.Scan("SHORT LONGERWORD")
	if len(matches) != 1 || matches[0].Value != "LONGERWORD" {
		t.Errorf("expected MinLen to keep only long match, got %v", matches)
	}
}

func TestRedactor_Default(t *testing.T) {
	r := DefaultRedactor()
	key := "AKIAIOSFODNN7EXAMPLE"
	redacted, matches := r.Redact("my key is " + key)
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	if strings.Contains(redacted, key) {
		t.Errorf("redacted output still contains secret: %q", redacted)
	}
	if !strings.Contains(redacted, "***") {
		t.Errorf("expected placeholder in redacted output: %q", redacted)
	}
}

func TestRedactor_WithTypeLabels(t *testing.T) {
	r := DefaultRedactor().WithTypeLabels(true)
	key := "AKIAIOSFODNN7EXAMPLE"
	redacted, _ := r.Redact("my key is " + key)
	if !strings.Contains(redacted, "<aws_access_key_id:***>") {
		t.Errorf("expected type label, got %q", redacted)
	}
}

func TestRedactor_NoSecrets(t *testing.T) {
	r := DefaultRedactor()
	original := "hello world"
	redacted, matches := r.Redact(original)
	if redacted != original {
		t.Errorf("expected unchanged text, got %q", redacted)
	}
	if len(matches) != 0 {
		t.Errorf("expected 0 matches, got %d", len(matches))
	}
}

func TestRedactString(t *testing.T) {
	key := "AKIAIOSFODNN7EXAMPLE"
	redacted := RedactString("key=" + key)
	if strings.Contains(redacted, key) {
		t.Errorf("RedactString should remove secret: %q", redacted)
	}
}

func TestRedactor_MultipleSecrets(t *testing.T) {
	r := DefaultRedactor()
	aws := "AKIAIOSFODNN7EXAMPLE"
	openai := "sk-" + strings.Repeat("a", 48)
	text := "aws=" + aws + " openai=" + openai
	redacted, matches := r.Redact(text)
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(matches))
	}
	if strings.Contains(redacted, aws) || strings.Contains(redacted, openai) {
		t.Errorf("redacted output still contains secrets: %q", redacted)
	}
}

func TestRedactor_NewRedactor_NilScanner(t *testing.T) {
	r := NewRedactor(nil, "[REDACTED]")
	key := "AKIAIOSFODNN7EXAMPLE"
	redacted, _ := r.Redact("key=" + key)
	if !strings.Contains(redacted, "[REDACTED]") {
		t.Errorf("expected default scanner fallback, got %q", redacted)
	}
}

func TestRedactor_NewRedactor_EmptyReplacement(t *testing.T) {
	r := NewRedactor(DefaultScanner(), "")
	key := "AKIAIOSFODNN7EXAMPLE"
	redacted, _ := r.Redact("key=" + key)
	if !strings.Contains(redacted, "***") {
		t.Errorf("expected default replacement fallback, got %q", redacted)
	}
}

func TestRedactor_HasSecrets(t *testing.T) {
	r := DefaultRedactor()
	if !r.HasSecrets("key=AKIAIOSFODNN7EXAMPLE") {
		t.Error("expected HasSecrets to be true")
	}
	if r.HasSecrets("hello world") {
		t.Error("expected HasSecrets to be false")
	}
}

func TestScanner_DedupeAndSort_Single(t *testing.T) {
	// Single match path in dedupeAndSort.
	s := DefaultScanner()
	matches := s.Scan("AKIAIOSFODNN7EXAMPLE")
	if len(matches) != 1 {
		t.Errorf("expected single match, got %d", len(matches))
	}
}


func regexpMust(expr string) *regexp.Regexp {
	return regexp.MustCompile(expr)
}

// TestScanner_GitSHANotFlagged verifies that a 40-char hex git commit SHA is
// not redacted as an AWS secret key. Previously the bare 40-char base64
// pattern matched any 40-char hex string, which clobbered git log output.
func TestScanner_GitSHANotFlagged(t *testing.T) {
	s := DefaultScanner()
	sha := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4" // 40 hex chars
	matches := s.Scan("commit " + sha + " (HEAD)")
	for _, m := range matches {
		if m.SecretType == "aws_secret_access_key" {
			t.Errorf("git SHA %q wrongly matched %s", sha, m.SecretType)
		}
	}
}

// TestScanner_AWSSecretWithContext verifies a real-looking AWS secret key is
// still caught when it appears with a contextual key name.
func TestScanner_AWSSecretWithContext(t *testing.T) {
	s := DefaultScanner()
	secret := "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY" // 40 base64 chars
	text := "aws_secret_access_key=" + secret
	matches := s.Scan(text)
	found := false
	for _, m := range matches {
		if m.SecretType == "aws_secret_access_key" && m.Value == secret {
			found = true
		}
	}
	if !found {
		t.Errorf("expected AWS secret with context to match; got %v", matches)
	}
	// And it should be redacted.
	red, _ := DefaultRedactor().Redact(text)
	if strings.Contains(red, secret) {
		t.Errorf("expected AWS secret redacted, got %q", red)
	}
}
