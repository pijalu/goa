// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package sandbox

import (
	"maps"
	"testing"
)

func TestBuildSafeEnvWhitelist(t *testing.T) {
	b := &EnvBuilder{}
	env := b.BuildSafeEnv("/tmp/sandbox")

	want := map[string]string{
		"PATH":             env["PATH"],
		"HOME":             "/tmp/sandbox",
		"TMPDIR":           "/tmp/sandbox",
		"LANG":             env["LANG"],
		"TERM":             "dumb",
		"PYTHONIOENCODING": "utf-8",
	}

	if !maps.Equal(env, want) {
		t.Fatalf("safe env contains extra keys: got %v, want %v", env, want)
	}
	if env["PATH"] == "" {
		t.Fatal("PATH must not be empty")
	}
}

func TestBuildBypassEnvStripsSecrets(t *testing.T) {
	getOSEnv = func() map[string]string {
		return map[string]string{
			"FOO":                       "bar",
			"OPENAI_API_KEY":            "sk-secret",
			"AWS_ACCESS_KEY_ID":         "AKIA",
			"DATABASE_URL":              "postgres://user:pass@host/db",
			"HF_HOME":                   "/home/user/.cache/huggingface",
			"AWS_EC2_METADATA_DISABLED": "true",
			"PATH":                      "/usr/bin",
			"HOME":                      "/home/user",
		}
	}
	t.Cleanup(func() { getOSEnv = defaultGetOSEnv })

	b := &EnvBuilder{}
	env := b.BuildBypassEnv("/tmp/sandbox")

	if env["FOO"] != "bar" {
		t.Errorf("FOO should be kept, got %q", env["FOO"])
	}
	if _, ok := env["OPENAI_API_KEY"]; ok {
		t.Error("OPENAI_API_KEY should be stripped")
	}
	if _, ok := env["AWS_ACCESS_KEY_ID"]; ok {
		t.Error("AWS_ACCESS_KEY_ID should be stripped")
	}
	if _, ok := env["DATABASE_URL"]; ok {
		t.Error("DATABASE_URL with embedded credentials should be stripped")
	}
	if _, ok := env["HF_HOME"]; ok {
		t.Error("HF_HOME should be stripped")
	}
	if env["AWS_EC2_METADATA_DISABLED"] != "true" {
		t.Error("AWS_EC2_METADATA_DISABLED hardening flag should be kept")
	}
	if env["HOME"] != "/tmp/sandbox" {
		t.Errorf("HOME should be repointed, got %q", env["HOME"])
	}
}

func TestIsSecretEnvName(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"OPENAI_API_KEY", true},
		{"HF_TOKEN", true},
		{"AWS_SECRET_ACCESS_KEY", true},
		{"FOO", false},
		{"PATH", false},
		{"AWS_EC2_METADATA_DISABLED", false},
	}
	for _, tc := range cases {
		if got := isSecretEnvName(tc.name); got != tc.want {
			t.Errorf("isSecretEnvName(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestIsSecretEnvValue(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"postgres://user:pass@host/db", true},
		{"https://token@example.com", true},
		{"AccountKey=abc123", true},
		{"plain text", false},
	}
	for _, tc := range cases {
		if got := isSecretEnvValue(tc.value); got != tc.want {
			t.Errorf("isSecretEnvValue(%q) = %v, want %v", tc.value, got, tc.want)
		}
	}
}

var defaultGetOSEnv = getOSEnv
