// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/mcp"
)

// --- helpers ---

// mcpRecordingSaver extends fakeConfigSaver to record SaveProjectFieldValue
// and DeleteProjectField calls for assertion.
type mcpRecordingSaver struct {
	fakeConfigSaver
	savedPaths   [][]string
	savedValues  []any
	deletedPaths [][]string
}

func (m *mcpRecordingSaver) SaveProjectFieldValue(path []string, value any) error {
	m.savedPaths = append(m.savedPaths, path)
	m.savedValues = append(m.savedValues, value)
	return nil
}

func (m *mcpRecordingSaver) DeleteProjectField(path []string) error {
	m.deletedPaths = append(m.deletedPaths, path)
	return nil
}

// mcpTestContext builds a minimal core.Context for MCP command tests.
// It does NOT set MCP (the manager) — individual tests set it as needed.
func mcpTestContext(buf *strings.Builder) core.Context {
	return core.Context{
		Config:       &config.Config{MCP: map[string]config.MCPServerConfig{}},
		OutputBuffer: buf,
		ProjectDir:   "/tmp/test",
	}
}

// --- parseMCPAdd tests ---

func TestParseMCPAdd_RemoteURL(t *testing.T) {
	spec, err := parseMCPAdd([]string{"gh", "--url=https://api.github.com/mcp/"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.name != "gh" {
		t.Errorf("expected name gh, got %s", spec.name)
	}
	if spec.cfg.Type != config.MCPTypeRemote {
		t.Errorf("expected type remote, got %s", spec.cfg.Type)
	}
	if spec.cfg.URL != "https://api.github.com/mcp/" {
		t.Errorf("expected url, got %s", spec.cfg.URL)
	}
}

func TestParseMCPAdd_RemoteWithHeaders(t *testing.T) {
	spec, err := parseMCPAdd([]string{"gh", "--url=https://api.github.com/mcp/", "--header=Authorization=Bearer tok", "--header=X-Custom=val"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.cfg.Headers["Authorization"] != "Bearer tok" {
		t.Errorf("expected Authorization header, got %v", spec.cfg.Headers)
	}
	if spec.cfg.Headers["X-Custom"] != "val" {
		t.Errorf("expected X-Custom header, got %v", spec.cfg.Headers)
	}
}

func TestParseMCPAdd_LocalCommand(t *testing.T) {
	spec, err := parseMCPAdd([]string{"chrome", "--", "npx", "-y", "chrome-devtools-mcp@latest"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.cfg.Type != config.MCPTypeLocal {
		t.Errorf("expected type local, got %s", spec.cfg.Type)
	}
	if len(spec.cfg.Command) != 3 || spec.cfg.Command[0] != "npx" {
		t.Errorf("expected command [npx -y chrome-devtools-mcp@latest], got %v", spec.cfg.Command)
	}
}

func TestParseMCPAdd_LocalWithEnv(t *testing.T) {
	spec, err := parseMCPAdd([]string{"srv", "--", "--env=FOO=bar", "--env=BAZ=qux", "my-cmd"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// env vars before the command are parsed as env, not command args
	if spec.cfg.Environment["FOO"] != "bar" {
		t.Errorf("expected FOO=bar, got %v", spec.cfg.Environment)
	}
	if spec.cfg.Environment["BAZ"] != "qux" {
		t.Errorf("expected BAZ=qux, got %v", spec.cfg.Environment)
	}
}

func TestParseMCPAdd_UrlAndCommandMutuallyExclusive(t *testing.T) {
	_, err := parseMCPAdd([]string{"x", "--url=http://x", "--", "cmd"})
	if err == nil {
		t.Fatal("expected error for both url and command")
	}
	if !strings.Contains(err.Error(), "not both") {
		t.Errorf("expected 'not both' error, got: %v", err)
	}
}

func TestParseMCPAdd_NeitherUrlNorCommand(t *testing.T) {
	_, err := parseMCPAdd([]string{"x"})
	if err == nil {
		t.Fatal("expected error for neither url nor command")
	}
}

func TestParseMCPAdd_EnvOnlyValidForLocal(t *testing.T) {
	_, err := parseMCPAdd([]string{"x", "--url=http://x", "--env=K=V"})
	if err == nil {
		t.Fatal("expected error for --env with remote")
	}
	if !strings.Contains(err.Error(), "only valid for local") {
		t.Errorf("expected 'only valid for local' error, got: %v", err)
	}
}

func TestParseMCPAdd_HeaderOnlyValidForRemote(t *testing.T) {
	_, err := parseMCPAdd([]string{"x", "--", "--header=K=V", "cmd"})
	if err == nil {
		t.Fatal("expected error for --header with local")
	}
	if !strings.Contains(err.Error(), "only valid for remote") {
		t.Errorf("expected 'only valid for remote' error, got: %v", err)
	}
}

func TestParseMCPAdd_BareUrlFlagRejected(t *testing.T) {
	_, err := parseMCPAdd([]string{"x", "--url", "http://x"})
	if err == nil {
		t.Fatal("expected error for bare --url flag")
	}
}

func TestParseMCPAdd_InvalidKV(t *testing.T) {
	_, err := parseMCPAdd([]string{"x", "--url=http://x", "--header=NOEQUALS"})
	if err == nil {
		t.Fatal("expected error for invalid K=V")
	}
}

func TestParseMCPAdd_MissingName(t *testing.T) {
	_, err := parseMCPAdd([]string{})
	if err == nil {
		t.Fatal("expected error for no args")
	}
	_, err = parseMCPAdd([]string{"x"})
	if err == nil {
		t.Fatal("expected error for name only")
	}
}

// --- putKV tests ---

func TestPutKV_Valid(t *testing.T) {
	m := map[string]string{}
	if err := putKV(m, "KEY=val"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m["KEY"] != "val" {
		t.Errorf("expected KEY=val, got %v", m)
	}
}

func TestPutKV_EqualsInValue(t *testing.T) {
	m := map[string]string{}
	if err := putKV(m, "KEY=val=ue"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m["KEY"] != "val=ue" {
		t.Errorf("expected KEY=val=ue, got %v", m)
	}
}

func TestPutKV_NoEquals(t *testing.T) {
	m := map[string]string{}
	if err := putKV(m, "NOEQUALS"); err == nil {
		t.Fatal("expected error for missing =")
	}
}

func TestPutKV_EmptyKey(t *testing.T) {
	m := map[string]string{}
	if err := putKV(m, "=val"); err == nil {
		t.Fatal("expected error for empty key")
	}
}

// --- list tests ---

func TestMCPList_Empty(t *testing.T) {
	var buf strings.Builder
	ctx := mcpTestContext(&buf)
	cmd := &MCPCommand{}
	if err := cmd.Run(ctx, []string{"list"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No MCP servers configured") {
		t.Errorf("expected 'No MCP servers configured', got: %s", buf.String())
	}
}

func TestMCPList_WithServers(t *testing.T) {
	var buf strings.Builder
	ctx := mcpTestContext(&buf)
	enabled := true
	disabled := false
	ctx.Config.MCP["alpha"] = config.MCPServerConfig{
		Type:    config.MCPTypeRemote,
		URL:     "http://alpha:8080",
		Enabled: &enabled,
	}
	ctx.Config.MCP["beta"] = config.MCPServerConfig{
		Type:    config.MCPTypeLocal,
		Command: []string{"beta-cmd", "--flag"},
		Enabled: &disabled,
	}
	cmd := &MCPCommand{}
	if err := cmd.Run(ctx, []string{"list"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "alpha") {
		t.Error("expected alpha in list output")
	}
	if !strings.Contains(out, "beta") {
		t.Error("expected beta in list output")
	}
	if !strings.Contains(out, "http://alpha:8080") {
		t.Error("expected alpha URL in output")
	}
	if !strings.Contains(out, "beta-cmd --flag") {
		t.Error("expected beta command in output")
	}
	// beta is disabled → should show ○
	lines := strings.Split(out, "\n")
	for _, l := range lines {
		if strings.Contains(l, "beta") && !strings.Contains(l, "○") {
			t.Errorf("expected disabled icon ○ for beta, got: %s", l)
		}
	}
}

func TestMCPList_DefaultsToList(t *testing.T) {
	var buf strings.Builder
	ctx := mcpTestContext(&buf)
	ctx.Config.MCP["srv"] = config.MCPServerConfig{
		Type:    config.MCPTypeLocal,
		Command: []string{"cmd"},
	}
	cmd := &MCPCommand{}
	if err := cmd.Run(ctx, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "srv") {
		t.Errorf("expected list output with srv, got: %s", buf.String())
	}
}

// --- status icon tests ---

func TestMCPStatusIcon_Disabled(t *testing.T) {
	disabled := false
	srv := config.MCPServerConfig{Enabled: &disabled}
	icon, label := mcpStatusIcon(srv, mcp.ServerStatus{}, false)
	if icon != "○" || label != "disabled" {
		t.Errorf("expected ○ disabled, got %s %s", icon, label)
	}
}

func TestMCPStatusIcon_NotConnected(t *testing.T) {
	srv := config.MCPServerConfig{}
	icon, label := mcpStatusIcon(srv, mcp.ServerStatus{}, false)
	if icon != "○" || label != "not connected" {
		t.Errorf("expected ○ not connected, got %s %s", icon, label)
	}
}

func TestMCPStatusIcon_Connected(t *testing.T) {
	srv := config.MCPServerConfig{}
	st := mcp.ServerStatus{State: mcp.StateConnected, Tools: 5}
	icon, label := mcpStatusIcon(srv, st, true)
	if icon != "✓" || !strings.Contains(label, "5 tools") {
		t.Errorf("expected ✓ connected (5 tools), got %s %s", icon, label)
	}
}

func TestMCPStatusIcon_Failed(t *testing.T) {
	srv := config.MCPServerConfig{}
	st := mcp.ServerStatus{State: mcp.StateFailed, Err: "connection refused"}
	icon, label := mcpStatusIcon(srv, st, true)
	if icon != "✗" || !strings.Contains(label, "connection refused") {
		t.Errorf("expected ✗ failed, got %s %s", icon, label)
	}
}

// --- add tests ---

func TestMCPAdd_PersistsToConfig(t *testing.T) {
	var buf strings.Builder
	ctx := mcpTestContext(&buf)
	saver := &mcpRecordingSaver{}
	ctx.ConfigSaver = saver

	cmd := &MCPCommand{}
	err := cmd.Run(ctx, []string{"add", "test-srv", "--url=http://localhost:9090"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Check in-memory config was updated
	if _, ok := ctx.Config.MCP["test-srv"]; !ok {
		t.Fatal("expected test-srv in config.MCP")
	}
	if ctx.Config.MCP["test-srv"].URL != "http://localhost:9090" {
		t.Errorf("expected url http://localhost:9090, got %s", ctx.Config.MCP["test-srv"].URL)
	}
	// Check persistence was called
	if len(saver.savedPaths) == 0 {
		t.Fatal("expected SaveProjectFieldValue to be called")
	}
	path := saver.savedPaths[0]
	if len(path) != 2 || path[0] != "mcp" || path[1] != "test-srv" {
		t.Errorf("expected path [mcp test-srv], got %v", path)
	}
}

func TestMCPAdd_NoSaverStillWorks(t *testing.T) {
	var buf strings.Builder
	ctx := mcpTestContext(&buf)
	ctx.ConfigSaver = nil

	cmd := &MCPCommand{}
	err := cmd.Run(ctx, []string{"add", "srv", "--", "echo", "hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := ctx.Config.MCP["srv"]; !ok {
		t.Fatal("expected srv in config.MCP")
	}
}

// --- remove tests ---

func TestMCPRemove_DeletesFromConfig(t *testing.T) {
	var buf strings.Builder
	ctx := mcpTestContext(&buf)
	saver := &mcpRecordingSaver{}
	ctx.ConfigSaver = saver
	ctx.Config.MCP["srv"] = config.MCPServerConfig{Type: config.MCPTypeLocal, Command: []string{"cmd"}}

	cmd := &MCPCommand{}
	if err := cmd.Run(ctx, []string{"remove", "srv"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := ctx.Config.MCP["srv"]; ok {
		t.Error("expected srv removed from config.MCP")
	}
	if len(saver.deletedPaths) == 0 {
		t.Fatal("expected DeleteProjectField to be called")
	}
	path := saver.deletedPaths[0]
	if len(path) != 2 || path[0] != "mcp" || path[1] != "srv" {
		t.Errorf("expected path [mcp srv], got %v", path)
	}
}

func TestMCPRemove_NotConfigured(t *testing.T) {
	var buf strings.Builder
	ctx := mcpTestContext(&buf)
	cmd := &MCPCommand{}
	err := cmd.Run(ctx, []string{"remove", "nonexistent"})
	if err == nil {
		t.Fatal("expected error for unconfigured server")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Errorf("expected 'not configured' error, got: %v", err)
	}
}

// --- enable/disable tests ---

func TestMCPDisable_PersistsEnabledFalse(t *testing.T) {
	var buf strings.Builder
	ctx := mcpTestContext(&buf)
	saver := &mcpRecordingSaver{}
	ctx.ConfigSaver = saver
	enabled := true
	ctx.Config.MCP["srv"] = config.MCPServerConfig{
		Type:    config.MCPTypeLocal,
		Command: []string{"cmd"},
		Enabled: &enabled,
	}

	cmd := &MCPCommand{}
	if err := cmd.Run(ctx, []string{"disable", "srv"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	srv := ctx.Config.MCP["srv"]
	if srv.IsEnabled() {
		t.Error("expected srv to be disabled")
	}
	if len(saver.savedPaths) == 0 {
		t.Fatal("expected SaveProjectFieldValue to be called")
	}
	path := saver.savedPaths[0]
	if len(path) != 3 || path[0] != "mcp" || path[1] != "srv" || path[2] != "enabled" {
		t.Errorf("expected path [mcp srv enabled], got %v", path)
	}
	if saver.savedValues[0] != false {
		t.Errorf("expected value false, got %v", saver.savedValues[0])
	}
}

func TestMCPEnable_PersistsEnabledTrue(t *testing.T) {
	var buf strings.Builder
	ctx := mcpTestContext(&buf)
	saver := &mcpRecordingSaver{}
	ctx.ConfigSaver = saver
	disabled := false
	ctx.Config.MCP["srv"] = config.MCPServerConfig{
		Type:    config.MCPTypeLocal,
		Command: []string{"cmd"},
		Enabled: &disabled,
	}

	cmd := &MCPCommand{}
	// enable will try to connect which will fail (no real server), but the
	// config flip and persistence should still happen before that.
	_ = cmd.Run(ctx, []string{"enable", "srv"})
	srv := ctx.Config.MCP["srv"]
	if !srv.IsEnabled() {
		t.Error("expected srv to be enabled")
	}
	if len(saver.savedPaths) == 0 {
		t.Fatal("expected SaveProjectFieldValue to be called")
	}
}

// --- unknown subcommand ---

func TestMCPUnknownSubcommand(t *testing.T) {
	var buf strings.Builder
	ctx := mcpTestContext(&buf)
	cmd := &MCPCommand{}
	err := cmd.Run(ctx, []string{"bogus"})
	if err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
	if !strings.Contains(err.Error(), "unknown /mcp subcommand") {
		t.Errorf("expected 'unknown /mcp subcommand' error, got: %v", err)
	}
}

// --- completion tests ---

func TestMCPCompleteArgs_AllSubcommands(t *testing.T) {
	ctx := mcpTestContext(&strings.Builder{})
	cmd := &MCPCommand{}
	comps := cmd.CompleteArgs(ctx, "")
	if len(comps) != len(mcpSubcommands) {
		t.Errorf("expected %d completions, got %d", len(mcpSubcommands), len(comps))
	}
}

func TestMCPCompleteArgs_FiltersPrefix(t *testing.T) {
	ctx := mcpTestContext(&strings.Builder{})
	cmd := &MCPCommand{}
	comps := cmd.CompleteArgs(ctx, "re")
	// should match "remove" and "reconnect"
	values := make([]string, len(comps))
	for i, c := range comps {
		values[i] = c.Value
	}
	found := map[string]bool{}
	for _, v := range values {
		found[v] = true
	}
	if !found["remove"] || !found["reconnect"] {
		t.Errorf("expected remove+reconnect for prefix 're', got %v", values)
	}
}

func TestServerNameCompletions(t *testing.T) {
	ctx := mcpTestContext(&strings.Builder{})
	ctx.Config.MCP["alpha"] = config.MCPServerConfig{Type: config.MCPTypeLocal, Command: []string{"a"}}
	ctx.Config.MCP["beta"] = config.MCPServerConfig{Type: config.MCPTypeRemote, URL: "http://b"}

	comps := serverNameCompletions(ctx, "")
	if len(comps) != 2 {
		t.Errorf("expected 2 completions, got %d", len(comps))
	}
	// sorted
	if comps[0].Value != "alpha" || comps[1].Value != "beta" {
		t.Errorf("expected sorted [alpha beta], got [%s %s]", comps[0].Value, comps[1].Value)
	}

	comps = serverNameCompletions(ctx, "al")
	if len(comps) != 1 || comps[0].Value != "alpha" {
		t.Errorf("expected [alpha] for prefix 'al', got %v", comps)
	}
}

// --- requireServerName tests ---

func TestRequireServerName_Missing(t *testing.T) {
	ctx := mcpTestContext(&strings.Builder{})
	_, err := requireServerName(ctx, []string{})
	if err == nil {
		t.Fatal("expected error for empty args")
	}
}

func TestRequireServerName_NotConfigured(t *testing.T) {
	ctx := mcpTestContext(&strings.Builder{})
	_, err := requireServerName(ctx, []string{"ghost"})
	if err == nil {
		t.Fatal("expected error for unconfigured server")
	}
}

func TestRequireServerName_Valid(t *testing.T) {
	ctx := mcpTestContext(&strings.Builder{})
	ctx.Config.MCP["srv"] = config.MCPServerConfig{Type: config.MCPTypeLocal, Command: []string{"cmd"}}
	name, err := requireServerName(ctx, []string{"srv"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "srv" {
		t.Errorf("expected srv, got %s", name)
	}
}
