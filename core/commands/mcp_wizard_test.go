// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/tui"
)

// mcp_wizard_test.go — filmstrip-style tests for the shared MCP wizard.
//
// Each test drives the wizard through a scripted sequence of select-option /
// text-input responses (the "frames") and asserts on both the captured UI
// state (prompt titles, offered options) and the resulting config — verifying
// the actual rendered output, not just internal state (guideline #5).

// wizardStep is one scripted UI response: either a SelectOption choice or a
// ShowInput submission. Exactly one field is set.
type wizardStep struct {
	selectValue string // when non-empty, respond to the next SelectOption with this value
	inputValue  string // when selectValue is empty, respond to the next ShowInput with this
	cancel      bool   // when true, respond with ok=false (cancel)
}

// wizardDriver stubs SelectOptionFunc/ShowInputFunc and plays back a script.
// It records every prompt shown so tests assert the rendered UI.
type wizardDriver struct {
	steps     []wizardStep
	idx       int
	selects   []string          // titles of each SelectOption shown
	inputs    []string          // prompts of each ShowInput shown
	optionLog [][]string        // values of options offered at each SelectOption
	saver     *mcpRecordingSaver
}

func (d *wizardDriver) attach(ctx *core.Context) {
	ctx.SelectOptionFunc = func(title string, options []tui.SelectorItem, current string, onSelected func(string, bool)) {
		d.selects = append(d.selects, title)
		vals := make([]string, 0, len(options))
		for _, o := range options {
			vals = append(vals, o.Value)
		}
		d.optionLog = append(d.optionLog, vals)
		step := d.next()
		if step.cancel {
			onSelected("", false)
			return
		}
		onSelected(step.selectValue, true)
	}
	ctx.ShowInputFunc = func(prompt, current string, onSubmit func(string, bool)) {
		d.inputs = append(d.inputs, prompt)
		step := d.next()
		if step.cancel {
			onSubmit("", false)
			return
		}
		onSubmit(step.inputValue, true)
	}
}

func (d *wizardDriver) next() wizardStep {
	if d.idx >= len(d.steps) {
		// Out of script: cancel to unwind safely rather than loop.
		return wizardStep{cancel: true}
	}
	s := d.steps[d.idx]
	d.idx++
	return s
}

func newWizardCtx(t *testing.T, initial map[string]config.MCPServerConfig) (*core.Context, *wizardDriver) {
	t.Helper()
	if initial == nil {
		initial = map[string]config.MCPServerConfig{}
	}
	d := &wizardDriver{saver: &mcpRecordingSaver{}}
	ctx := &core.Context{
		Config:       &config.Config{MCP: initial},
		ConfigSaver:  d.saver,
		ProjectDir:   t.TempDir(),
		OutputBuffer: &strings.Builder{},
	}
	d.attach(ctx)
	return ctx, d
}

// TestMCPWizard_AddRemoteServer drives add → name → type=remote → url →
// headers and asserts the server lands in config with the right values, the
// rendered prompts match, and the value is persisted via the project saver.
func TestMCPWizard_AddRemoteServer(t *testing.T) {
	ctx, d := newWizardCtx(t, nil)
	d.steps = []wizardStep{
		{selectValue: "__add__"},        // server list → add
		{inputValue: "gh"},              // name
		{selectValue: "remote"},         // type
		{inputValue: "https://api.github.com/mcp/"}, // url
		{inputValue: "Authorization=Bearer tok"},    // headers
	}
	runMCPWizard(*ctx)

	srv, ok := ctx.Config.MCP["gh"]
	if !ok {
		t.Fatalf("server 'gh' not added; MCP=%v", ctx.Config.MCP)
	}
	if srv.Type != config.MCPTypeRemote {
		t.Errorf("Type = %q, want remote", srv.Type)
	}
	if srv.URL != "https://api.github.com/mcp/" {
		t.Errorf("URL = %q, want github mcp", srv.URL)
	}
	if srv.Headers["Authorization"] != "Bearer tok" {
		t.Errorf("Headers[Authorization] = %q, want Bearer tok", srv.Headers["Authorization"])
	}
	// Filmstrip: the wizard must have shown a type selector and a URL prompt.
	joinedSelects := strings.Join(d.selects, "|")
	if !strings.Contains(joinedSelects, "Server type for gh:") {
		t.Errorf("expected a 'Server type for gh:' selector; selects=%v", d.selects)
	}
	joinedInputs := strings.Join(d.inputs, "|")
	if !strings.Contains(joinedInputs, "Server URL:") {
		t.Errorf("expected a 'Server URL:' prompt; inputs=%v", d.inputs)
	}
	// Persisted through the saver at ["mcp","gh"].
	if len(d.saver.savedPaths) == 0 {
		t.Error("expected SaveProjectFieldValue to be called")
	}
}

// TestMCPWizard_AddLocalServer drives add → name → type=local → command → env.
func TestMCPWizard_AddLocalServer(t *testing.T) {
	ctx, d := newWizardCtx(t, nil)
	d.steps = []wizardStep{
		{selectValue: "__add__"},
		{inputValue: "fs"},
		{selectValue: "local"},
		{inputValue: "npx -y @modelcontextprotocol/server-filesystem /tmp"},
		{inputValue: "DEBUG=1,LOG=info"},
	}
	runMCPWizard(*ctx)

	srv, ok := ctx.Config.MCP["fs"]
	if !ok {
		t.Fatalf("server 'fs' not added")
	}
	if srv.Type != config.MCPTypeLocal {
		t.Errorf("Type = %q, want local", srv.Type)
	}
	if len(srv.Command) == 0 || srv.Command[0] != "npx" {
		t.Errorf("Command = %v, want argv starting with npx", srv.Command)
	}
	if srv.Environment["DEBUG"] != "1" || srv.Environment["LOG"] != "info" {
		t.Errorf("Environment = %v, want DEBUG=1 LOG=info", srv.Environment)
	}
}

// TestMCPWizard_EditServerChangeURL drives select-server → edit → type (keep
// remote) → new url → headers, asserting the existing values are replaced.
func TestMCPWizard_EditServerChangeURL(t *testing.T) {
	existing := map[string]config.MCPServerConfig{
		"gh": {Type: config.MCPTypeRemote, URL: "https://old.example.com/mcp"},
	}
	ctx, d := newWizardCtx(t, existing)
	d.steps = []wizardStep{
		{selectValue: "gh"},                 // pick the server
		{selectValue: "edit"},               // edit action
		{selectValue: "remote"},             // keep type
		{inputValue: "https://new.example.com/mcp"}, // new url
		{inputValue: ""},                    // clear headers
	}
	runMCPWizard(*ctx)

	srv := ctx.Config.MCP["gh"]
	if srv.URL != "https://new.example.com/mcp" {
		t.Errorf("URL = %q, want updated", srv.URL)
	}
	// Filmstrip: edit menu must offer toggle/edit/delete for the server.
	found := false
	for _, opts := range d.optionLog {
		joined := strings.Join(opts, ",")
		if strings.Contains(joined, "toggle") && strings.Contains(joined, "edit") && strings.Contains(joined, "delete") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an edit menu offering toggle/edit/delete; optionLog=%v", d.optionLog)
	}
}

// TestMCPWizard_DeleteServer drives select-server → delete → confirm=yes and
// asserts removal + a DeleteProjectField persist call.
func TestMCPWizard_DeleteServer(t *testing.T) {
	existing := map[string]config.MCPServerConfig{
		"gh": {Type: config.MCPTypeRemote, URL: "https://api.github.com/mcp/"},
	}
	ctx, d := newWizardCtx(t, existing)
	d.steps = []wizardStep{
		{selectValue: "gh"},
		{selectValue: "delete"},
		{selectValue: "yes"},
	}
	runMCPWizard(*ctx)

	if _, ok := ctx.Config.MCP["gh"]; ok {
		t.Error("server 'gh' should be deleted")
	}
	if len(d.saver.deletedPaths) == 0 {
		t.Error("expected DeleteProjectField to be called for removal")
	}
}

// TestMCPWizard_DeleteCancelled drives delete → confirm=no and asserts the
// server survives.
func TestMCPWizard_DeleteCancelled(t *testing.T) {
	existing := map[string]config.MCPServerConfig{
		"gh": {Type: config.MCPTypeRemote, URL: "https://api.github.com/mcp/"},
	}
	ctx, d := newWizardCtx(t, existing)
	d.steps = []wizardStep{
		{selectValue: "gh"},
		{selectValue: "delete"},
		{selectValue: "no"},
	}
	runMCPWizard(*ctx)
	if _, ok := ctx.Config.MCP["gh"]; !ok {
		t.Error("server 'gh' should survive a cancelled delete")
	}
}

// TestMCPWizard_ToggleFromEditMenu drives select-server → toggle and asserts
// the enabled flag flips (delegated to /mcp:disable).
func TestMCPWizard_ToggleFromEditMenu(t *testing.T) {
	existing := map[string]config.MCPServerConfig{
		"gh": {Type: config.MCPTypeRemote, URL: "https://api.github.com/mcp/"},
	}
	ctx, d := newWizardCtx(t, existing)
	d.steps = []wizardStep{
		{selectValue: "gh"},
		{selectValue: "toggle"},
	}
	runMCPWizard(*ctx)
	if ctx.Config.MCP["gh"].IsEnabled() {
		t.Error("server should be disabled after toggle")
	}
}

// TestMCPWizard_ConfigEntryParity proves the /config menu launches the SAME
// wizard as /mcp:wizard: both render the identical server-list selector title.
func TestMCPWizard_ConfigEntryParity(t *testing.T) {
	existing := map[string]config.MCPServerConfig{
		"gh": {Type: config.MCPTypeRemote, URL: "https://api.github.com/mcp/"},
	}

	// Via /mcp:wizard (standalone).
	ctx1, d1 := newWizardCtx(t, copyMCPMap(existing))
	d1.steps = []wizardStep{{cancel: true}} // immediately back out of server list
	runMCPWizard(*ctx1)

	// Via /config → MCP servers → wizard entry.
	ctx2, d2 := newWizardCtx(t, copyMCPMap(existing))
	menu := newConfigMenu(*ctx2)
	d2.steps = []wizardStep{
		{selectValue: "__wizard__"}, // /config MCP menu → wizard
		{cancel: true},              // back out of the wizard server list
	}
	runMCPWizardOnMenu(menu)

	if len(d1.selects) == 0 || len(d2.selects) == 0 {
		t.Fatalf("expected both entries to render a selector; mcp=%v config=%v", d1.selects, d2.selects)
	}
	if d1.selects[0] != d2.selects[0] {
		t.Errorf("wizard root title differs: /mcp=%q /config=%q (UX must match)", d1.selects[0], d2.selects[0])
	}
}

func copyMCPMap(in map[string]config.MCPServerConfig) map[string]config.MCPServerConfig {
	out := make(map[string]config.MCPServerConfig, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
