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
// whole "clipboard bytes → decode → store → stored path" chain can run on any
// OS without touching the developer's real clipboard: the darwin script writes
// the payload to the file it was told to use, wl-paste/xclip answer the MIME
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

// TestReadClipboardImage_FromClipboardBytesToStoredPath is the whole-chain test
// for image paste: a fake clipboard backend hands over real PNG bytes (no OS
// clipboard involved), the real decoder turns them into an image, and the real
// image store persists them under a durable path.
func TestReadClipboardImage_FromClipboardBytesToStoredPath(t *testing.T) {
	fakeImageClipboard(t, tinyPNG(t))
	store := isolateImageStore(t)

	img, err := ReadClipboardImage()
	if err != nil {
		t.Fatalf("ReadClipboardImage: %v", err)
	}
	if img == nil {
		t.Fatal("clipboard image bytes did not decode into an image")
	}
	if b := img.Bounds(); b.Dx() != 2 || b.Dy() != 2 {
		t.Errorf("decoded bounds = %v, want the 2x2 clipboard image", b)
	}

	path, err := SaveClipboardImage(img)
	if err != nil {
		t.Fatalf("SaveClipboardImage: %v", err)
	}
	if filepath.Dir(path) != store {
		t.Errorf("stored at %q, want a file inside the image store %q", path, store)
	}
	if !IsImageFile(path) {
		t.Errorf("IsImageFile(%q) = false: the stored path would not be sent as an attachment", path)
	}
}

// TestReadClipboardImage_NoImageOnClipboardIsSilent: a clipboard holding text or
// a file list (or nothing) must read as "no image", never as an error — the
// paste then falls through to the text branch.
func TestReadClipboardImage_NoImageOnClipboardIsSilent(t *testing.T) {
	fakeClipboard(t, func(string, []string) ([]byte, bool) { return nil, false })

	img, err := ReadClipboardImage()
	if img != nil || err != nil {
		t.Errorf("ReadClipboardImage = (%v,%v), want (nil,nil)", img, err)
	}
}

// TestReadClipboardImage_UndecodableBytesReportsDecodeFailure: a backend that
// answers with non-image bytes is a genuine decode failure, distinct from the
// silent no-image case above (the editor turns it into "nothing pasted").
func TestReadClipboardImage_UndecodableBytesReportsDecodeFailure(t *testing.T) {
	fakeImageClipboard(t, []byte("this is text, not an image"))

	img, err := ReadClipboardImage()
	if img != nil {
		t.Errorf("undecodable bytes decoded into %v", img)
	}
	if err == nil {
		t.Error("undecodable clipboard bytes must report a decode failure")
	}
}
