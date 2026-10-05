// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package internal

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

// fakeImageClipboard makes every platform backend hand back payload, so the
// whole "clipboard bytes → sniff → store → stored path" chain can run on any OS
// without touching the developer's real clipboard: the darwin script writes the
// payload to the file it was told to use, wl-paste/xclip answer the MIME
// interrogation and then the payload, and PowerShell returns its base64 form.
// It is the single clipboard fake the image-paste chain tests are built on.
func fakeImageClipboard(t *testing.T, payload []byte) {
	t.Helper()
	fakeClipboard(t, func(name string, args []string) ([]byte, bool) {
		switch name {
		case "osascript":
			if err := os.WriteFile(macScriptPath(t, args[1]), payload, 0o600); err != nil {
				t.Fatal(err)
			}
			return nil, true
		case "wl-paste":
			if len(args) == 1 && args[0] == "--list-types" {
				return []byte("text/plain\nimage/png\n"), true
			}
			return payload, true
		case "xclip":
			if len(args) == 2+3 && args[3] == "TARGETS" {
				return []byte("image/png\n"), true
			}
			return payload, true
		case "powershell.exe":
			return []byte(base64.StdEncoding.EncodeToString(payload)), true
		}
		t.Fatalf("unexpected clipboard command %q %v", name, args)
		return nil, false
	})
}

// isolateImageStore points the durable image store at a temp dir, so a test
// never writes into the developer's real cache, and returns that directory.
func isolateImageStore(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)                                   // darwin: $HOME/Library/Caches
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache")) // linux
	t.Setenv("LocalAppData", filepath.Join(home, "local"))   // windows
	dir, err := ImageStoreDir()
	if err != nil {
		t.Fatalf("ImageStoreDir: %v", err)
	}
	return dir
}

// TestReadClipboardImageBytes_ToStoredPath is the whole-chain test for image
// paste on this host: a fake clipboard backend hands over real PNG bytes (no OS
// clipboard involved) and the real image store persists them under a durable
// path that IsImageFile accepts — the same predicate the submit path uses to
// decide "this token is an attachment".
func TestReadClipboardImageBytes_ToStoredPath(t *testing.T) {
	noSessionEnv(t)
	withPlatform(t, clipboardGOOS, "osascript", "wl-paste", "xclip", "powershell.exe")
	payload := tinyPNG(t)
	fakeImageClipboard(t, payload)
	store := isolateImageStore(t)

	data, ok := ReadClipboardImageBytes()
	if !ok {
		t.Fatal("clipboard image bytes were not read")
	}
	if string(data) != string(payload) {
		t.Error("returned bytes are not the clipboard payload")
	}

	path, err := SaveClipboardImageBytes(data)
	if err != nil {
		t.Fatalf("SaveClipboardImageBytes: %v", err)
	}
	if filepath.Dir(path) != store {
		t.Errorf("stored at %q, want a file inside the image store %q", path, store)
	}
	if !IsImageFile(path) {
		t.Errorf("IsImageFile(%q) = false: the stored path would not be sent as an attachment", path)
	}
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(stored) != string(payload) {
		t.Error("stored bytes differ from the clipboard: the paste must not re-encode")
	}
	if filepath.Ext(path) != ".png" {
		t.Errorf("stored extension = %q, want .png", filepath.Ext(path))
	}
}

// TestSaveClipboardImageBytes_KeepsWebP is the regression test for the format
// gap between the terminal paste path and the web upload path: WebP is a format
// the sniffer, the store and the provider all accept, but the clipboard path used
// to decode and re-encode to PNG with no WebP decoder registered, so a clipboard
// offering only WebP pasted nothing at all.
func TestSaveClipboardImageBytes_KeepsWebP(t *testing.T) {
	noSessionEnv(t)
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	withPlatform(t, "linux", "wl-paste")
	store := isolateImageStore(t)
	// A minimal RIFF/WEBP container: the bytes are opaque to us, and that is the
	// point — the store must keep what the clipboard published.
	payload := []byte("RIFF\x24\x00\x00\x00WEBPVP8 \x18\x00\x00\x00webp-payload")
	fakeClipboard(t, func(_ string, args []string) ([]byte, bool) {
		if args[0] == "--list-types" {
			return []byte("image/webp\n"), true
		}
		if args[1] != "image/webp" {
			t.Fatalf("requested %q, want image/webp", args[1])
		}
		return payload, true
	})

	data, ok := ReadClipboardImageBytes()
	if !ok {
		t.Fatal("a clipboard offering WebP read as no image")
	}
	path, err := SaveClipboardImageBytes(data)
	if err != nil {
		t.Fatalf("SaveClipboardImageBytes: %v", err)
	}
	if filepath.Dir(path) != store {
		t.Errorf("stored at %q, want a file inside %q", path, store)
	}
	if filepath.Ext(path) != ".webp" {
		t.Errorf("stored extension = %q, want .webp", filepath.Ext(path))
	}
	if !IsImageFile(path) {
		t.Errorf("IsImageFile(%q) = false: the stored WebP would not be sent as an attachment", path)
	}
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(stored) != string(payload) {
		t.Error("stored WebP differs from the clipboard bytes")
	}
}

// TestSaveClipboardImageBytes_RejectsNonImage: the store is the same one the web
// upload path writes to, so a non-image is refused by content, and no file is
// left behind.
func TestSaveClipboardImageBytes_RejectsNonImage(t *testing.T) {
	store := isolateImageStore(t)

	if _, err := SaveClipboardImageBytes([]byte("this is text, not an image")); err == nil {
		t.Error("non-image bytes were stored")
	}
	if _, err := SaveClipboardImageBytes(nil); err == nil {
		t.Error("empty bytes were stored")
	}
	if entries, _ := os.ReadDir(store); len(entries) != 0 {
		t.Errorf("image store holds %d files after a refused paste, want none", len(entries))
	}
}

// TestReadClipboardImageBytes_NoImageIsSilent: a clipboard holding text, a file
// list, or nothing must read as "no image", never as an error — the paste then
// falls through to the text branch.
func TestReadClipboardImageBytes_NoImageIsSilent(t *testing.T) {
	noSessionEnv(t)
	withPlatform(t, "linux", "wl-paste", "xclip", "powershell.exe")
	fakeClipboard(t, func(string, []string) ([]byte, bool) { return nil, false })

	if data, ok := ReadClipboardImageBytes(); ok || data != nil {
		t.Errorf("ReadClipboardImageBytes = (%q,%v), want (nil,false)", data, ok)
	}
}

// TestReadClipboardImageBytes_UnknownFormatIsSilent: a backend that answers with
// bytes in a format the store cannot keep must read as "no image" here, so the
// paste never inserts a path that the agent cannot attach.
func TestReadClipboardImageBytes_UnknownFormatIsSilent(t *testing.T) {
	noSessionEnv(t)
	t.Setenv("DISPLAY", ":0")
	withPlatform(t, "linux", "xclip")
	fakeClipboard(t, func(_ string, args []string) ([]byte, bool) {
		if args[3] == "TARGETS" {
			return []byte("image/png\n"), true
		}
		return []byte("bytes that sniff as nothing"), true
	})

	if data, ok := ReadClipboardImageBytes(); ok || data != nil {
		t.Errorf("ReadClipboardImageBytes = (%q,%v), want (nil,false)", data, ok)
	}
}
