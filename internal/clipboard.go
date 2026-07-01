// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package internal provides cross-platform clipboard operations.
//
// CopyToClipboard copies text using the best available method:
//  1. Platform-native CLI tool (pbcopy, clip, wl-copy, xclip/xsel, termux-clipboard-set)
//  2. OSC 52 escape sequence (always attempted for remote sessions and as last-resort fallback)
package internal

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// maxOSC52Payload is the maximum encoded size for OSC 52 sequences.
// Sequences larger than this are silently skipped.
const maxOSC52Payload = 100_000

// ClipboardNativeHook, if non-nil, is used by CopyToClipboard instead of
// exec'ing a native clipboard tool. Intended for tests.
var ClipboardNativeHook func(string) error

// ClipboardOSC52Hook, if non-nil, is used by emitOSC52 instead of writing to
// stdout. Intended for tests.
var ClipboardOSC52Hook func(string) bool

// CopyToClipboard copies text to the system clipboard, trying methods in priority order.
// Returns nil on success, or an error if all methods fail.
func CopyToClipboard(text string) error {
	// 1. Try platform-native CLI tools on non-remote sessions
	if !isRemoteSession() {
		if err := copyNative(text); err == nil {
			return nil
		}
	}

	// 2. Try OSC 52 (always for remote, fallback for local)
	if emitOSC52(text) {
		return nil
	}

	// 3. Last resort: try native even if remote (some terminal emulators forward it)
	if isRemoteSession() {
		if err := copyNative(text); err == nil {
			return nil
		}
	}

	return fmt.Errorf("no clipboard method available")
}

// isRemoteSession returns true if running over SSH or MOSH.
func isRemoteSession() bool {
	return os.Getenv("SSH_CONNECTION") != "" ||
		os.Getenv("SSH_CLIENT") != "" ||
		os.Getenv("MOSH_CONNECTION") != ""
}

// copyNative tries to copy text using a platform-native CLI tool.
func copyNative(text string) error {
	if ClipboardNativeHook != nil {
		return ClipboardNativeHook(text)
	}
	cmd := nativeCopyCommand()
	if cmd == nil {
		return fmt.Errorf("no native clipboard tool found")
	}
	c := exec.Command(cmd[0], cmd[1:]...)
	c.Stdin = strings.NewReader(text)
	return c.Run()
}

// nativeCopyCommand returns the OS-specific copy command and args.
func nativeCopyCommand() []string {
	switch {
	case hasBinary("pbcopy"):
		return []string{"pbcopy"}
	case hasBinary("wl-copy"):
		return []string{"wl-copy"}
	case hasBinary("xclip"):
		return []string{"xclip", "-selection", "clipboard"}
	case hasBinary("xsel"):
		return []string{"xsel", "--input", "--clipboard"}
	case hasBinary("termux-clipboard-set"):
		return []string{"termux-clipboard-set"}
	case hasBinary("clip"):
		return []string{"clip"}
	}
	return nil
}

// hasBinary checks if an executable exists in PATH.
func hasBinary(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// emitOSC52 writes an OSC 52 escape sequence to stdout.
// Returns true if the sequence was emitted, false if skipped (too large).
func emitOSC52(text string) bool {
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	if len(encoded) > maxOSC52Payload {
		return false
	}
	if ClipboardOSC52Hook != nil {
		return ClipboardOSC52Hook(text)
	}
	// OSC 52: ESC ] 5 2 ; c ; <base64> ST
	// where c is the clipboard (c = clipboard, p = primary, s = secondary)
	fmt.Fprint(os.Stdout, "\x1b]52;c;"+encoded+"\x07")
	return true
}
