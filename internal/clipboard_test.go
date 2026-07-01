// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package internal

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestIsRemoteSession_SSHConnection(t *testing.T) {
	t.Setenv("SSH_CONNECTION", "192.168.1.1 22 192.168.1.2 54321")
	if !isRemoteSession() {
		t.Error("expected remote session with SSH_CONNECTION set")
	}
}

func TestIsRemoteSession_SSHClient(t *testing.T) {
	t.Setenv("SSH_CLIENT", "192.168.1.1 22 54321")
	if !isRemoteSession() {
		t.Error("expected remote session with SSH_CLIENT set")
	}
}

func TestIsRemoteSession_MOSH(t *testing.T) {
	t.Setenv("MOSH_CONNECTION", "1.2.3.4 60001")
	if !isRemoteSession() {
		t.Error("expected remote session with MOSH_CONNECTION set")
	}
}

func TestIsRemoteSession_NotRemote(t *testing.T) {
	for _, env := range []string{"SSH_CONNECTION", "SSH_CLIENT", "MOSH_CONNECTION"} {
		os.Unsetenv(env)
	}
	if isRemoteSession() {
		t.Error("expected non-remote session with no SSH/MOSH vars")
	}
}

func TestEmitOSC52_SmallPayload(t *testing.T) {
	// Capture stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w

	result := emitOSC52("hello")
	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if !result {
		t.Error("expected true for small payload")
	}
	expected := "\x1b]52;c;" + base64Encode("hello") + "\x07"
	if buf.String() != expected {
		t.Errorf("got %q, want %q", buf.String(), expected)
	}
}

func TestEmitOSC52_LargePayload(t *testing.T) {
	// Create a payload that exceeds the max
	big := strings.Repeat("x", maxOSC52Payload) // base64 will be > max
	result := emitOSC52(big)
	if result {
		t.Error("expected false for payload that exceeds max encoded size")
	}
}

func TestNativeCopyCommand_Pbcopy(t *testing.T) {
	// Only test if pbcopy exists
	if !hasBinary("pbcopy") {
		t.Skip("pbcopy not available")
	}
	cmd := nativeCopyCommand()
	found := false
	for _, c := range cmd {
		if strings.Contains(c, "pbcopy") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected pbcopy in command, got %v", cmd)
	}
}

func TestHasBinary_Found(t *testing.T) {
	// go should always be in PATH
	if !hasBinary("go") {
		t.Error("expected 'go' binary to be found")
	}
}

func TestHasBinary_NotFound(t *testing.T) {
	if hasBinary("nonexistent-binary-xyzzy") {
		t.Error("expected nonexistent binary to not be found")
	}
}

func TestCopyToClipboard_NativeHookSucceeds(t *testing.T) {
	var captured string
	old := ClipboardNativeHook
	ClipboardNativeHook = func(text string) error {
		captured = text
		return nil
	}
	defer func() { ClipboardNativeHook = old }()

	if err := CopyToClipboard("hello hook"); err != nil {
		t.Fatalf("CopyToClipboard failed: %v", err)
	}
	if captured != "hello hook" {
		t.Errorf("captured = %q, want %q", captured, "hello hook")
	}
}

func TestCopyToClipboard_FallsBackToOSC52(t *testing.T) {
	oldNative := ClipboardNativeHook
	oldOSC52 := ClipboardOSC52Hook
	defer func() {
		ClipboardNativeHook = oldNative
		ClipboardOSC52Hook = oldOSC52
	}()

	ClipboardNativeHook = func(string) error { return fmt.Errorf("native unavailable") }
	var captured string
	ClipboardOSC52Hook = func(text string) bool {
		captured = text
		return true
	}

	if err := CopyToClipboard("fallback text"); err != nil {
		t.Fatalf("CopyToClipboard failed: %v", err)
	}
	if captured != "fallback text" {
		t.Errorf("OSC52 captured = %q, want %q", captured, "fallback text")
	}
}

func TestCopyToClipboard_AllMethodsFail(t *testing.T) {
	oldNative := ClipboardNativeHook
	oldOSC52 := ClipboardOSC52Hook
	defer func() {
		ClipboardNativeHook = oldNative
		ClipboardOSC52Hook = oldOSC52
	}()

	ClipboardNativeHook = func(string) error { return fmt.Errorf("native unavailable") }
	ClipboardOSC52Hook = func(string) bool { return false }

	if err := CopyToClipboard("nowhere"); err == nil {
		t.Fatal("expected error when all clipboard methods fail")
	}
}

// base64Encode is a test helper to duplicate base64 logic for assertions.
func base64Encode(s string) string {
	enc := make([]byte, base64.StdEncoding.EncodedLen(len(s)))
	base64.StdEncoding.Encode(enc, []byte(s))
	return string(enc)
}
