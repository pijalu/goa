// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package internal

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeClipboard replaces the clipboard command runner for the duration of a
// test. handler receives the command name and args and returns stdout/ok.
func fakeClipboard(t *testing.T, handler func(name string, args []string) ([]byte, bool)) {
	t.Helper()
	old := runClipboardCommand
	runClipboardCommand = func(_ time.Duration, name string, args ...string) ([]byte, bool) {
		return handler(name, args)
	}
	t.Cleanup(func() { runClipboardCommand = old })
}

// tinyPNG returns valid PNG bytes.
func tinyPNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// macScriptPath extracts the destination path from the osascript source the
// darwin backend builds.
func macScriptPath(t *testing.T, script string) string {
	t.Helper()
	const marker = `POSIX file "`
	i := strings.Index(script, marker)
	if i < 0 {
		t.Fatalf("script has no POSIX file reference: %s", script)
	}
	rest := script[i+len(marker):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		t.Fatalf("unterminated POSIX file reference: %s", script)
	}
	return rest[:j]
}

// TestReadDarwinClipboardImage is the regression test for the missing macOS
// backend: before this, readClipboardImageBytes only knew wl-paste/xclip/
// powershell, so image paste failed outright on darwin.
//
// It also pins the file-creation order: AppleScript's `open for access … with
// write permission` fails if the file already exists, so the destination must be
// removed before osascript runs.
func TestReadDarwinClipboardImage(t *testing.T) {
	want := tinyPNG(t)
	var sawPath string
	fakeClipboard(t, func(name string, args []string) ([]byte, bool) {
		if name != "osascript" {
			t.Fatalf("unexpected command %q", name)
		}
		sawPath = macScriptPath(t, args[1])
		if _, err := os.Stat(sawPath); !os.IsNotExist(err) {
			t.Errorf("destination existed before osascript ran (%v); AppleScript would fail", err)
		}
		if err := os.WriteFile(sawPath, want, 0o600); err != nil {
			t.Fatal(err)
		}
		return nil, true
	})

	got, ok := readDarwinClipboardImage()
	if !ok {
		t.Fatal("readDarwinClipboardImage reported no image")
	}
	if !bytes.Equal(got, want) {
		t.Error("returned bytes are not the clipboard payload")
	}
	if _, err := os.Stat(sawPath); !os.IsNotExist(err) {
		t.Errorf("temporary clipboard file was not cleaned up: %v", err)
	}
}

// TestReadDarwinClipboardImage_NoImage: osascript fails when the clipboard holds
// text or a file, which must read as "no image", not as an error.
func TestReadDarwinClipboardImage_NoImage(t *testing.T) {
	fakeClipboard(t, func(string, []string) ([]byte, bool) { return nil, false })

	got, ok := readDarwinClipboardImage()
	if ok || got != nil {
		t.Errorf("readDarwinClipboardImage = (%v,%v), want (nil,false)", got, ok)
	}
}

// TestReadDarwinClipboardImage_EmptyOutput guards against reporting an image when
// the script exited 0 but wrote nothing.
func TestReadDarwinClipboardImage_EmptyOutput(t *testing.T) {
	fakeClipboard(t, func(string, []string) ([]byte, bool) { return nil, true })

	if _, ok := readDarwinClipboardImage(); ok {
		t.Error("an empty clipboard file must not count as an image")
	}
}

// TestReadWlPasteImage_SelectsOfferedType covers the Wayland path used on Linux:
// the offered MIME list is inspected so a JPEG is not requested as PNG (which
// wl-paste would refuse).
func TestReadWlPasteImage_SelectsOfferedType(t *testing.T) {
	want := []byte("jpeg-bytes")
	var requested string
	fakeClipboard(t, func(name string, args []string) ([]byte, bool) {
		if name != "wl-paste" {
			t.Fatalf("unexpected command %q", name)
		}
		switch {
		case len(args) == 1 && args[0] == "--list-types":
			return []byte("text/plain\nimage/jpeg\n"), true
		case len(args) == 3 && args[0] == "--type":
			requested = args[1]
			return want, true
		}
		t.Fatalf("unexpected wl-paste args %v", args)
		return nil, false
	})

	got, ok := readWlPasteImage()
	if !ok || !bytes.Equal(got, want) {
		t.Fatalf("readWlPasteImage = (%q,%v), want (%q,true)", got, ok, want)
	}
	if requested != "image/jpeg" {
		t.Errorf("requested type = %q, want image/jpeg", requested)
	}
}

// TestReadWlPasteImage_NoImageTypeStops: an offered non-image clipboard must not
// fall through to a blind PNG request (that is how a stale X11 clipboard leaked
// into a Wayland read).
func TestReadWlPasteImage_NoImageTypeStops(t *testing.T) {
	fakeClipboard(t, func(_ string, args []string) ([]byte, bool) {
		if len(args) == 1 && args[0] == "--list-types" {
			return []byte("text/plain\n"), true
		}
		t.Fatalf("blind image request issued despite a text-only clipboard: %v", args)
		return nil, false
	})

	if _, ok := readWlPasteImage(); ok {
		t.Error("text-only clipboard reported as an image")
	}
}

// TestReadWlPasteImage_LegacyNoListTypes keeps the old wl-clipboard working.
func TestReadWlPasteImage_LegacyNoListTypes(t *testing.T) {
	want := []byte("png-bytes")
	fakeClipboard(t, func(_ string, args []string) ([]byte, bool) {
		if len(args) == 1 && args[0] == "--list-types" {
			return nil, false
		}
		if len(args) == 3 && args[0] == "--type" && args[1] == "image/png" {
			return want, true
		}
		t.Fatalf("unexpected args %v", args)
		return nil, false
	})

	got, ok := readWlPasteImage()
	if !ok || !bytes.Equal(got, want) {
		t.Errorf("readWlPasteImage = (%q,%v), want the PNG fallback", got, ok)
	}
}

// TestReadXclipImage_SelectsOfferedTarget mirrors the Wayland test for X11.
func TestReadXclipImage_SelectsOfferedTarget(t *testing.T) {
	want := []byte("x11-png")
	var requested string
	fakeClipboard(t, func(name string, args []string) ([]byte, bool) {
		if name != "xclip" {
			t.Fatalf("unexpected command %q", name)
		}
		if len(args) == 5 && args[3] == "TARGETS" {
			return []byte("text/html\nimage/png\n"), true
		}
		if len(args) == 5 {
			requested = args[3]
			return want, true
		}
		t.Fatalf("unexpected xclip args %v", args)
		return nil, false
	})

	got, ok := readXclipImage()
	if !ok || !bytes.Equal(got, want) {
		t.Fatalf("readXclipImage = (%q,%v), want bytes", got, ok)
	}
	if requested != "image/png" {
		t.Errorf("requested target = %q, want image/png", requested)
	}
}

// TestReadClipboardFilePaths_DarwinKeepsOnlyExistingFiles covers the finder-copy
// path: osascript output is filtered to real files so a stale entry cannot inject
// a bogus token into the input line.
func TestReadClipboardFilePaths_DarwinKeepsOnlyExistingFiles(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(real, tinyPNG(t), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "gone.png")

	fakeClipboard(t, func(name string, args []string) ([]byte, bool) {
		if name != "osascript" {
			t.Fatalf("unexpected command %q", name)
		}
		if !strings.Contains(args[1], "«class furl»") {
			t.Errorf("script does not read the file URL flavour: %s", args[1])
		}
		return []byte(real + "\n" + missing + "\n"), true
	})

	got := darwinClipboardFilePaths()
	if len(got) != 1 || got[0] != real {
		t.Errorf("darwinClipboardFilePaths = %v, want [%s]", got, real)
	}
}

// TestReadClipboardFilePaths_URIParsing covers the Linux text/uri-list flavour.
func TestReadClipboardFilePaths_URIParsing(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "a file.png") // space forces percent-encoding
	if err := os.WriteFile(real, tinyPNG(t), 0o600); err != nil {
		t.Fatal(err)
	}
	uri := "file://" + strings.ReplaceAll(filepath.Dir(real), " ", "%20") + "/a%20file.png"

	fakeClipboard(t, func(name string, args []string) ([]byte, bool) {
		if name != "wl-paste" {
			t.Fatalf("unexpected command %q", name)
		}
		return []byte("# comment\n" + uri + "\n\n"), true
	})

	got := uriListClipboardFilePaths()
	if len(got) != 1 || got[0] != real {
		t.Errorf("uriListClipboardFilePaths = %v, want [%s]", got, real)
	}
}

// TestReadClipboardText_Darwin keeps the text branch of the paste precedence
// covered.
func TestReadClipboardText_Darwin(t *testing.T) {
	fakeClipboard(t, func(name string, args []string) ([]byte, bool) {
		if name != "pbpaste" {
			t.Fatalf("unexpected command %q", name)
		}
		return []byte("pasted\ntext"), true
	})

	text, ok := ReadClipboardText()
	if !ok || text != "pasted\ntext" {
		t.Errorf("ReadClipboardText = (%q,%v), want the clipboard text", text, ok)
	}
}

// TestReadClipboardText_UnavailableIsSilent: no reader must be a quiet no-op, not
// an error, so a paste on an exotic platform simply does nothing.
func TestReadClipboardText_UnavailableIsSilent(t *testing.T) {
	fakeClipboard(t, func(string, []string) ([]byte, bool) { return nil, false })

	if text, ok := ReadClipboardText(); ok || text != "" {
		t.Errorf("ReadClipboardText = (%q,%v), want (\"\",false)", text, ok)
	}
}
