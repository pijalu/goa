// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const cleanupConsoleHome = `providers:
  - id: duplicate
    endpoint: https://duplicate.example.invalid/v1
    api_key: home-duplicate-secret
  - id: conflict
    endpoint: https://conflict.example.invalid/v1
    api_key: home-conflict-secret
    headers:
      Authorization: Bearer home-header-secret
`

const cleanupConsoleProject = `providers:
  - id: duplicate
    endpoint: https://duplicate.example.invalid/v1
    api_key: home-duplicate-secret
  - id: conflict
    endpoint: https://conflict.example.invalid/v1
    api_key: project-conflict-secret
    headers:
      Authorization: Bearer project-header-secret
  - id: project-only
    api_key: project-only-secret
`

func cleanupConsoleFixture(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	project := filepath.Join(root, "project")
	homePath := filepath.Join(home, ".goa", "config.yaml")
	projectPath := filepath.Join(project, ".goa", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(homePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(projectPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(homePath, []byte(cleanupConsoleHome), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectPath, []byte(cleanupConsoleProject), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GOA_HOME", home)
	return project, homePath, projectPath
}

func assertCleanupSecretsHidden(t *testing.T, output string) {
	t.Helper()
	for _, secret := range []string{
		"home-duplicate-secret", "home-conflict-secret", "home-header-secret",
		"project-conflict-secret", "project-header-secret", "project-only-secret",
	} {
		if strings.Contains(output, secret) {
			t.Errorf("console output leaked %q:\n%s", secret, output)
		}
	}
	for _, sequence := range []string{"\x1b[", "\x1b]", "\x1bP"} {
		if strings.Contains(output, sequence) {
			t.Errorf("console output contains TUI control sequence %q", sequence)
		}
	}
}

func TestCleanupConsole_AcceptDeclineInvalidInputAndRedaction(t *testing.T) {
	project, homePath, projectPath := cleanupConsoleFixture(t)
	homeBefore, err := os.ReadFile(homePath)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := runCleanupConsole(project, strings.NewReader("invalid\ny\nn\n"), &output); err != nil {
		t.Fatalf("runCleanupConsole: %v", err)
	}
	text := output.String()
	assertCleanupSecretsHidden(t, text)
	for _, wanted := range []string{
		"Redundant project provider \"duplicate\"",
		"Please answer y or n.",
		"Conflicting project provider \"conflict\"",
		"api_key, headers",
		"(y/n) [n]:",
		"Removed 1 project item; kept 1.",
	} {
		if !strings.Contains(text, wanted) {
			t.Errorf("console output missing %q:\n%s", wanted, text)
		}
	}
	after, err := os.ReadFile(projectPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(after)
	if strings.Contains(content, "id: duplicate") {
		t.Errorf("accepted duplicate remains:\n%s", content)
	}
	for _, wanted := range []string{"id: conflict", "project-conflict-secret", "id: project-only"} {
		if !strings.Contains(content, wanted) {
			t.Errorf("declined/project-only value %q was removed:\n%s", wanted, content)
		}
	}
	homeAfter, err := os.ReadFile(homePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(homeBefore, homeAfter) {
		t.Error("home configuration changed")
	}
}

func TestCleanupConsole_EOFAfterApprovalLeavesEverythingUnchanged(t *testing.T) {
	project, _, projectPath := cleanupConsoleFixture(t)
	before, err := os.ReadFile(projectPath)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := runCleanupConsole(project, strings.NewReader("y\n"), &output); err != nil {
		t.Fatalf("runCleanupConsole: %v", err)
	}
	if !strings.Contains(output.String(), "Cleanup cancelled; configuration unchanged.") {
		t.Errorf("missing cancellation message:\n%s", output.String())
	}
	assertCleanupSecretsHidden(t, output.String())
	after, err := os.ReadFile(projectPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("EOF after an earlier approval changed the project file")
	}
}

func TestCleanupConsole_DeclineIsByteIdentical(t *testing.T) {
	project, _, projectPath := cleanupConsoleFixture(t)
	before, err := os.ReadFile(projectPath)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := runCleanupConsole(project, strings.NewReader("\nn\nn\n"), &output); err != nil {
		t.Fatalf("runCleanupConsole: %v", err)
	}
	if !strings.Contains(output.String(), "Removed 0 project items; kept 2.") {
		t.Errorf("missing decline summary:\n%s", output.String())
	}
	after, err := os.ReadFile(projectPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("declining every prompt rewrote the project file")
	}
}

func TestCleanupConsole_NoCandidatesDoesNotReadInput(t *testing.T) {
	project := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GOA_HOME", home)
	var output bytes.Buffer
	if err := runCleanupConsole(project, strings.NewReader(""), &output); err != nil {
		t.Fatalf("runCleanupConsole: %v", err)
	}
	if !strings.Contains(output.String(), "No redundant or conflicting project configuration found.") {
		t.Errorf("unexpected empty-cleanup output: %q", output.String())
	}
}
