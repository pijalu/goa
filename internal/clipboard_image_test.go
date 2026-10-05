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

// Per-backend unit tests. The platform *dispatch* they feed is covered in
// clipboard_dispatch_test.go; these pin what each backend does with the bytes it
// is given.

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
	noSessionEnv(t)
	withPlatform(t, "darwin", "osascript")
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

	got, out := readDarwinClipboardImage()
	if out != clipFound {
		t.Fatalf("readDarwinClipboardImage reported %v, want clipFound", out)
	}
	if !bytes.Equal(got, want) {
		t.Error("returned bytes are not the clipboard payload")
	}
	if _, err := os.Stat(sawPath); !os.IsNotExist(err) {
		t.Errorf("temporary clipboard file was not cleaned up: %v", err)
	}
}

// TestReadDarwinClipboardImage_NoImage: osascript fails when the clipboard holds
// text or a file, which must read as "no image here" — the chain may move on to
// the next flavour, but no image was found.
func TestReadDarwinClipboardImage_NoImage(t *testing.T) {
	noSessionEnv(t)
	withPlatform(t, "darwin", "osascript")
	fakeClipboard(t, func(string, []string) ([]byte, bool) { return nil, false })

	got, out := readDarwinClipboardImage()
	if out != clipAbsent || got != nil {
		t.Errorf("readDarwinClipboardImage = (%v,%v), want (nil,clipAbsent)", got, out)
	}
}

// TestReadDarwinClipboardImage_EmptyOutput guards against reporting an image when
// the script exited 0 but wrote nothing.
func TestReadDarwinClipboardImage_EmptyOutput(t *testing.T) {
	noSessionEnv(t)
	withPlatform(t, "darwin", "osascript")
	fakeClipboard(t, func(string, []string) ([]byte, bool) { return nil, true })

	if _, out := readDarwinClipboardImage(); out != clipAbsent {
		t.Errorf("empty clipboard file reported %v, want clipAbsent", out)
	}
}

// TestReadDarwinClipboardImage_NoOsascript: without the helper the backend cannot
// answer at all, which is what lets the chain try another backend instead of
// concluding "no image".
func TestReadDarwinClipboardImage_NoOsascript(t *testing.T) {
	noSessionEnv(t)
	withPlatform(t, "darwin") // osascript not on PATH
	fakeClipboard(t, func(string, []string) ([]byte, bool) {
		t.Fatal("a missing helper must not be run")
		return nil, false
	})

	if _, out := readDarwinClipboardImage(); out != clipUnavailable {
		t.Errorf("missing osascript reported %v, want clipUnavailable", out)
	}
}

// TestReadWlPasteImage_SelectsOfferedType covers the Wayland path used on Linux:
// the offered MIME list is inspected so a JPEG is not requested as PNG (which
// wl-paste would refuse).
func TestReadWlPasteImage_SelectsOfferedType(t *testing.T) {
	withPlatform(t, "linux", "wl-paste")
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

	got, out := readWlPasteImage()
	if out != clipFound || !bytes.Equal(got, want) {
		t.Fatalf("readWlPasteImage = (%q,%v), want (%q,clipFound)", got, out, want)
	}
	if requested != "image/jpeg" {
		t.Errorf("requested type = %q, want image/jpeg", requested)
	}
}

// TestReadWlPasteImage_NoImageTypeStops: an offered non-image clipboard must not
// fall through to a blind PNG request (that is how a stale X11 clipboard leaked
// into a Wayland read).
func TestReadWlPasteImage_NoImageTypeStops(t *testing.T) {
	withPlatform(t, "linux", "wl-paste")
	fakeClipboard(t, func(_ string, args []string) ([]byte, bool) {
		if len(args) == 1 && args[0] == "--list-types" {
			return []byte("text/plain\n"), true
		}
		t.Fatalf("blind image request issued despite a text-only clipboard: %v", args)
		return nil, false
	})

	if _, out := readWlPasteImage(); out != clipAbsent {
		t.Errorf("text-only clipboard reported %v, want clipAbsent", out)
	}
}

// TestReadWlPasteImage_FailedListIsUnavailable: a wl-paste that cannot list types
// (no Wayland display, or an ancient wl-clipboard) is a backend that could not
// answer. Requesting an unadvertised type instead would ask the owning app for a
// conversion it never offered, so the read moves to another backend.
func TestReadWlPasteImage_FailedListIsUnavailable(t *testing.T) {
	withPlatform(t, "linux", "wl-paste")
	fakeClipboard(t, func(_ string, args []string) ([]byte, bool) {
		if len(args) == 1 && args[0] == "--list-types" {
			return nil, false
		}
		t.Fatalf("unadvertised type requested: %v", args)
		return nil, false
	})

	if _, out := readWlPasteImage(); out != clipUnavailable {
		t.Errorf("failed --list-types reported %v, want clipUnavailable", out)
	}
}

// TestReadWlPasteImage_AdvertisedButUnserved: the owner advertises an image it
// cannot actually serve. That is the backend failing, not "no image" — the X11
// clipboard may still have one.
func TestReadWlPasteImage_AdvertisedButUnserved(t *testing.T) {
	withPlatform(t, "linux", "wl-paste")
	fakeClipboard(t, func(_ string, args []string) ([]byte, bool) {
		if args[0] == "--list-types" {
			return []byte("image/png\n"), true
		}
		return nil, false
	})

	if _, out := readWlPasteImage(); out != clipUnavailable {
		t.Errorf("unserved type reported %v, want clipUnavailable", out)
	}
}

// TestReadXclipImage_SelectsOfferedTarget mirrors the Wayland test for X11.
func TestReadXclipImage_SelectsOfferedTarget(t *testing.T) {
	withPlatform(t, "linux", "xclip")
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

	got, out := readXclipImage()
	if out != clipFound || !bytes.Equal(got, want) {
		t.Fatalf("readXclipImage = (%q,%v), want bytes", got, out)
	}
	if requested != "image/png" {
		t.Errorf("requested target = %q, want image/png", requested)
	}
}

// TestReadXclipImage_EmptyTargetsIsAbsent: TARGETS answers with no image type, so
// the X11 clipboard holds no image — distinct from the backend being unavailable.
func TestReadXclipImage_EmptyTargetsIsAbsent(t *testing.T) {
	withPlatform(t, "linux", "xclip")
	fakeClipboard(t, func(_ string, args []string) ([]byte, bool) {
		if args[3] != "TARGETS" {
			t.Fatalf("unadvertised target requested: %v", args)
		}
		return []byte("TEXT\nUTF8_STRING\n"), true
	})

	if _, out := readXclipImage(); out != clipAbsent {
		t.Errorf("text-only TARGETS reported %v, want clipAbsent", out)
	}
}

// TestReadXclipImage_NoXclip: without the helper the X11 backend cannot answer.
func TestReadXclipImage_NoXclip(t *testing.T) {
	withPlatform(t, "linux") // xclip not installed
	fakeClipboard(t, func(string, []string) ([]byte, bool) {
		t.Fatal("a missing helper must not be run")
		return nil, false
	})

	if _, out := readXclipImage(); out != clipUnavailable {
		t.Errorf("missing xclip reported %v, want clipUnavailable", out)
	}
}

// TestReadClipboardFilePaths_DarwinKeepsOnlyExistingFiles covers the finder-copy
// path: osascript output is filtered to real files so a stale entry cannot inject
// a bogus token into the input line.
func TestReadClipboardFilePaths_DarwinKeepsOnlyExistingFiles(t *testing.T) {
	noSessionEnv(t)
	withPlatform(t, "darwin", "osascript")
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
	noSessionEnv(t)
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	withPlatform(t, "linux", "wl-paste")
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
		if args[0] != "--type" || args[1] != "text/uri-list" {
			t.Fatalf("unexpected args %v", args)
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
	noSessionEnv(t)
	withPlatform(t, "darwin", "pbpaste")
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
	noSessionEnv(t)
	withPlatform(t, "darwin", "pbpaste")
	fakeClipboard(t, func(string, []string) ([]byte, bool) { return nil, false })

	if text, ok := ReadClipboardText(); ok || text != "" {
		t.Errorf("ReadClipboardText = (%q,%v), want (\"\",false)", text, ok)
	}
}
