// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/event"
)

func TestCopyCommand_NameAndAliases(t *testing.T) {
	cmd := &CopyCommand{}
	if cmd.Name() != "copy" {
		t.Errorf("Name() = %q, want 'copy'", cmd.Name())
	}
	if len(cmd.Aliases()) != 0 {
		t.Errorf("Aliases() = %v, want nil", cmd.Aliases())
	}
}

func TestCopyCommand_IsInternal(t *testing.T) {
	cmd := &CopyCommand{}
	if !cmd.IsInternal() {
		t.Error("IsInternal() should be true for /copy")
	}
}

func TestCopyCommand_Run_WithText(t *testing.T) {
	t.Skip("disabled per user request")
	cmd := &CopyCommand{}
	bus := event.MakeBus(0, 0, 10, 0)
	ctx := core.Context{
		EventBus:      bus,
		AssistantText: "Hello, this is a test response.",
	}

	err := cmd.Run(ctx, nil)
	if err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	select {
	case msg := <-bus.Chat:
		if msg.Flash == nil {
			t.Errorf("expected flash event, got %+v", msg)
		}
	default:
		t.Error("expected a FlashMsg event to be sent")
	}
}

func TestCopyCommand_Run_NoText(t *testing.T) {
	cmd := &CopyCommand{}
	bus := event.MakeBus(0, 0, 10, 0)
	ctx := core.Context{
		EventBus:      bus,
		AssistantText: "",
	}

	err := cmd.Run(ctx, nil)
	if err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	select {
	case msg := <-bus.Chat:
		if msg.Flash == nil {
			t.Errorf("expected flash event, got %+v", msg)
		}
		if msg.Flash.Text != "No agent messages to copy yet." {
			t.Errorf("expected 'No agent messages...', got %q", msg.Flash.Text)
		}
	default:
		t.Error("expected a FlashMsg event to be sent")
	}
}

func TestCopyCommand_CopiesLastAssistantMessage(t *testing.T) {
	cmd := &CopyCommand{}
	bus := event.MakeBus(0, 0, 10, 0)

	var captured string
	oldHook := internal.ClipboardNativeHook
	internal.ClipboardNativeHook = func(text string) error {
		captured = text
		return nil
	}
	defer func() { internal.ClipboardNativeHook = oldHook }()

	ctx := core.Context{
		EventBus:      bus,
		AssistantText: "Assistant says hello.",
	}

	err := cmd.Run(ctx, nil)
	if err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	if captured != "Assistant says hello." {
		t.Errorf("clipboard captured = %q, want %q", captured, "Assistant says hello.")
	}

	select {
	case msg := <-bus.Chat:
		if msg.Flash == nil {
			t.Errorf("expected flash event, got %+v", msg)
		}
		if !strings.Contains(msg.Flash.Text, "Copied") {
			t.Errorf("expected success flash, got %q", msg.Flash.Text)
		}
	default:
		t.Error("expected a FlashMsg event to be sent")
	}
}
