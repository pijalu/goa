// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"strings"
	"testing"
)

func TestGoaCommandTool_Schema(t *testing.T) {
	router := NewCommandRouter(GlobalRegistry(), nil)
	tool := NewGoaCommandTool(router, Context{})
	schema := tool.Schema()
	if schema.Name != "goa" {
		t.Errorf("name = %q, want goa", schema.Name)
	}
}

type testHelloCommand struct{}

func (c *testHelloCommand) Name() string      { return "hello" }
func (c *testHelloCommand) Aliases() []string { return nil }
func (c *testHelloCommand) ShortHelp() string { return "say hello" }
func (c *testHelloCommand) LongHelp() string  { return "usage: /hello" }
func (c *testHelloCommand) Run(ctx Context, args []string) error {
	ctx.Writef("Hello from goa tool")
	return nil
}

func TestGoaCommandTool_ExecuteCommand(t *testing.T) {
	reg := NewCommandRegistry()
	_ = reg.Register(&testHelloCommand{})
	cmdRouter := NewCommandRouter(reg, nil)
	tool := NewGoaCommandTool(cmdRouter, Context{})

	out, err := tool.Execute(`{"command_string":"/hello"}`)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(out, "Hello from goa tool") {
		t.Errorf("expected hello output, got %q", out)
	}
}

func TestGoaCommandTool_ExecuteUnknown(t *testing.T) {
	reg := NewCommandRegistry()
	_ = reg.Register(&testHelloCommand{})
	cmdRouter := NewCommandRouter(reg, nil)
	tool := NewGoaCommandTool(cmdRouter, Context{})

	out, err := tool.Execute(`{"command_string":"/notacommand"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Unknown command") {
		t.Errorf("expected unknown command message, got %q", out)
	}
}

func TestGoaCommandTool_AddsLeadingSlash(t *testing.T) {
	reg := NewCommandRegistry()
	_ = reg.Register(&testHelloCommand{})
	cmdRouter := NewCommandRouter(reg, nil)
	tool := NewGoaCommandTool(cmdRouter, Context{})

	out, err := tool.Execute(`{"command_string":"hello"}`)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(out, "Hello from goa tool") {
		t.Errorf("expected hello output, got %q", out)
	}
}
