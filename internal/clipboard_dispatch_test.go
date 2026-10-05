// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package internal

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The dispatch contract, pinned per platform against the reference agent (pi,
// packages/coding-agent/src/utils/clipboard-image.ts and its test file). The
// platform seam exists so these run on any host: the alternative is that a
// Windows or Wayland command shape ships without ever having executed.

// withPlatform pins the platform seam for one test: which GOOS is dispatched and
// which clipboard helpers "exist" on PATH. Backends are only executed here after
// this, so the three-state contract can be asserted exactly.
func withPlatform(t *testing.T, goos string, tools ...string) {
	t.Helper()
	present := make(map[string]bool, len(tools))
	for _, tool := range tools {
		present[tool] = true
	}
	oldGOOS, oldTool := clipboardGOOS, clipboardToolAvailable
	clipboardGOOS = goos
	clipboardToolAvailable = func(name string) bool { return present[name] }
	t.Cleanup(func() {
		clipboardGOOS = oldGOOS
		clipboardToolAvailable = oldTool
	})
}

// noSessionEnv clears every environment variable the clipboard code reads, so a
// test states its own session instead of inheriting the developer's.
func noSessionEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"WAYLAND_DISPLAY", "XDG_SESSION_TYPE", "DISPLAY",
		"TERMUX_VERSION", "WSL_DISTRO_NAME", "WSLENV",
	} {
		t.Setenv(k, "")
	}
}

// recordingClipboard swaps in a fake runner that records every command, so a
// test can assert which backends were *consulted*, not only what came back.
func recordingClipboard(t *testing.T, handler func(name string, args []string) ([]byte, bool)) *[]string {
	t.Helper()
	var calls []string
	fakeClipboard(t, func(name string, args []string) ([]byte, bool) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return handler(name, args)
	})
	return &calls
}

// listingClipboard answers a type interrogation with types and every payload
// request with payload — the shape a working Wayland or X11 backend has.
func listingClipboard(types string, payload []byte) func(string, []string) ([]byte, bool) {
	return func(_ string, args []string) ([]byte, bool) {
		for _, a := range args {
			if a == "--list-types" || a == "TARGETS" {
				return []byte(types), true
			}
		}
		return payload, true
	}
}

// commandNames reduces recorded calls to their program names.
func commandNames(calls []string) []string {
	names := make([]string, 0, len(calls))
	for _, c := range calls {
		names = append(names, strings.Fields(c)[0])
	}
	return names
}

func TestPlatformDispatch_WaylandNoImageStopsBeforeX11(t *testing.T) {
	noSessionEnv(t)
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	t.Setenv("DISPLAY", ":0") // an XWayland session exposes both
	withPlatform(t, "linux", "wl-paste", "xclip")
	want := []byte("stale x11 image the Wayland clipboard does not hold")
	calls := recordingClipboard(t, listingClipboard("text/plain\n", want))

	if data, ok := ReadClipboardImageBytes(); ok || data != nil {
		t.Fatalf("ReadClipboardImageBytes = (%q,%v), want no image", data, ok)
	}
	if names := commandNames(*calls); len(names) != 1 || names[0] != "wl-paste" {
		t.Errorf("backends consulted = %v, want only wl-paste: a Wayland answer must end the chain", names)
	}
}

func TestPlatformDispatch_WaylandFallsBackToX11WhenWlPasteMissing(t *testing.T) {
	noSessionEnv(t)
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	t.Setenv("DISPLAY", ":0")
	withPlatform(t, "linux", "xclip") // wl-clipboard not installed
	want := tinyPNG(t)
	calls := recordingClipboard(t, listingClipboard("image/png\n", want))

	data, ok := ReadClipboardImageBytes()
	if !ok || !bytes.Equal(data, want) {
		t.Fatalf("ReadClipboardImageBytes = (%q,%v), want the X11 tuple", data, ok)
	}
	if names := commandNames(*calls); len(names) == 0 || names[0] != "xclip" {
		t.Errorf("backends consulted = %v, want xclip", names)
	}
}

func TestPlatformDispatch_X11DoesNotProbeWhenTargetsFails(t *testing.T) {
	noSessionEnv(t)
	t.Setenv("DISPLAY", ":0")
	withPlatform(t, "linux", "wl-paste", "xclip")
	calls := recordingClipboard(t, func(_ string, args []string) ([]byte, bool) {
		if len(args) == 5 && args[3] == "TARGETS" {
			return nil, false // no clipboard owner / no X11
		}
		return []byte("unrelated"), true
	})

	if data, ok := ReadClipboardImageBytes(); ok || data != nil {
		t.Fatalf("ReadClipboardImageBytes = (%q,%v), want no image", data, ok)
	}
	if names := commandNames(*calls); len(names) != 1 || names[0] != "xclip" {
		t.Errorf("backends consulted = %v, want one xclip TARGETS probe and no image request", names)
	}
	if got := (*calls)[0]; !strings.HasSuffix(got, "TARGETS -o") {
		t.Errorf("only probe was %q, want the TARGETS interrogation", got)
	}
}

func TestPlatformDispatch_X11DoesNotProbeUnadvertisedTypes(t *testing.T) {
	noSessionEnv(t)
	t.Setenv("DISPLAY", ":0")
	withPlatform(t, "linux", "xclip")
	calls := recordingClipboard(t, func(_ string, args []string) ([]byte, bool) {
		switch {
		case len(args) == 5 && args[3] == "TARGETS":
			return []byte("image/png\n"), true
		case len(args) == 5 && args[3] == "image/png":
			return nil, false // advertised, but the owner cannot serve it
		}
		return []byte("unrelated"), true
	})

	if _, ok := ReadClipboardImageBytes(); ok {
		t.Error("an unservable target reported an image")
	}
	want := []string{"xclip -selection clipboard -t TARGETS -o", "xclip -selection clipboard -t image/png -o"}
	if len(*calls) != len(want) {
		t.Fatalf("backends consulted = %v, want %v", *calls, want)
	}
	for i := range want {
		if (*calls)[i] != want[i] {
			t.Errorf("call %d = %q, want %q", i, (*calls)[i], want[i])
		}
	}
}

func TestPlatformDispatch_X11ReadsAdvertisedImage(t *testing.T) {
	noSessionEnv(t)
	t.Setenv("DISPLAY", ":0")
	withPlatform(t, "linux", "wl-paste", "xclip")
	want := tinyPNG(t)
	calls := recordingClipboard(t, listingClipboard("text/html\nimage/png\n", want))

	data, ok := ReadClipboardImageBytes()
	if !ok || !bytes.Equal(data, want) {
		t.Fatalf("ReadClipboardImageBytes = (%q,%v), want the X11 image", data, ok)
	}
	for _, c := range *calls {
		if strings.HasPrefix(c, "wl-paste") {
			t.Errorf("wl-paste consulted without a Wayland session: %v", *calls)
		}
	}
}

func TestPlatformDispatch_WSLUsesWindowsClipboard(t *testing.T) {
	noSessionEnv(t)
	t.Setenv("WSL_DISTRO_NAME", "Ubuntu")
	withPlatform(t, "linux", "powershell.exe") // no wl-clipboard, no xclip
	want := tinyPNG(t)
	calls := recordingClipboard(t, func(name string, _ []string) ([]byte, bool) {
		if name != "powershell.exe" {
			t.Fatalf("unexpected command %q", name)
		}
		return []byte(base64.StdEncoding.EncodeToString(want)), true
	})

	data, ok := ReadClipboardImageBytes()
	if !ok || !bytes.Equal(data, want) {
		t.Fatalf("ReadClipboardImageBytes = (%q,%v), want the Windows clipboard image", data, ok)
	}
	if names := commandNames(*calls); len(names) != 1 || names[0] != "powershell.exe" {
		t.Errorf("backends consulted = %v, want PowerShell only", names)
	}
}

// TestPlatformDispatch_WSLTriesPowerShellAfterAnAbsentLinuxAnswer: on WSL the
// Windows clipboard is the last resort, because a Win+Shift+S screenshot lands
// only there. Note the X11 consultation is skipped: wl-paste answered (with "no
// image"), and a backend that answered ends its stage of the walk.
func TestPlatformDispatch_WSLTriesPowerShellAfterAnAbsentLinuxAnswer(t *testing.T) {
	noSessionEnv(t)
	t.Setenv("WSL_DISTRO_NAME", "Ubuntu")
	withPlatform(t, "linux", "wl-paste", "xclip", "powershell.exe")
	want := tinyPNG(t)
	calls := recordingClipboard(t, func(name string, args []string) ([]byte, bool) {
		if name == "powershell.exe" {
			return []byte(base64.StdEncoding.EncodeToString(want)), true
		}
		return []byte("text/plain\n"), true // the Linux clipboard answers "no image"
	})

	data, ok := ReadClipboardImageBytes()
	if !ok || !bytes.Equal(data, want) {
		t.Fatalf("ReadClipboardImageBytes = (%q,%v), want the Windows clipboard image", data, ok)
	}
	names := commandNames(*calls)
	if len(names) != 2 || names[0] != "wl-paste" || names[1] != "powershell.exe" {
		t.Errorf("backends consulted = %v, want wl-paste → PowerShell on WSL", names)
	}
}

// TestPlatformDispatch_WSLReachesPowerShellWhenLinuxToolsFail: with no Linux
// backend able to answer, WSL still reaches the Windows clipboard.
func TestPlatformDispatch_WSLReachesPowerShellWhenLinuxToolsFail(t *testing.T) {
	noSessionEnv(t)
	t.Setenv("WSL_DISTRO_NAME", "Ubuntu")
	withPlatform(t, "linux", "wl-paste", "xclip", "powershell.exe")
	want := tinyPNG(t)
	calls := recordingClipboard(t, func(name string, args []string) ([]byte, bool) {
		if name == "powershell.exe" {
			return []byte(base64.StdEncoding.EncodeToString(want)), true
		}
		return nil, false // no Wayland display, no X11 display
	})

	data, ok := ReadClipboardImageBytes()
	if !ok || !bytes.Equal(data, want) {
		t.Fatalf("ReadClipboardImageBytes = (%q,%v), want the Windows clipboard image", data, ok)
	}
	names := commandNames(*calls)
	if len(names) != 3 || names[0] != "wl-paste" || names[1] != "xclip" || names[2] != "powershell.exe" {
		t.Errorf("backends consulted = %v, want wl-paste → xclip → PowerShell", names)
	}
}

func TestPlatformDispatch_WSLDetectedFromProcVersion(t *testing.T) {
	noSessionEnv(t)
	old := clipboardProcVersion
	clipboardProcVersion = func() []byte {
		return []byte("Linux version 5.15.90.1-microsoft-standard-WSL2 (gcc ...)")
	}
	t.Cleanup(func() { clipboardProcVersion = old })

	if !isWSL() {
		t.Error("a Microsoft kernel string in /proc/version must read as WSL")
	}
}

func TestPlatformDispatch_PlainLinuxIsNotWSL(t *testing.T) {
	noSessionEnv(t)
	old := clipboardProcVersion
	clipboardProcVersion = func() []byte { return []byte("Linux version 6.1.0-generic (gcc ...)") }
	t.Cleanup(func() { clipboardProcVersion = old })

	if isWSL() {
		t.Error("an ordinary kernel string must not read as WSL")
	}
}

func TestPlatformDispatch_WindowsUsesPowerShellOnly(t *testing.T) {
	noSessionEnv(t)
	withPlatform(t, "windows", "powershell.exe")
	want := tinyPNG(t)
	calls := recordingClipboard(t, func(name string, _ []string) ([]byte, bool) {
		if name != "powershell.exe" {
			t.Fatalf("unexpected command %q", name)
		}
		return []byte(base64.StdEncoding.EncodeToString(want)), true
	})

	data, ok := ReadClipboardImageBytes()
	if !ok || !bytes.Equal(data, want) {
		t.Fatalf("ReadClipboardImageBytes = (%q,%v), want the Windows image", data, ok)
	}
	if names := commandNames(*calls); len(names) != 1 || names[0] != "powershell.exe" {
		t.Errorf("backends consulted = %v, want PowerShell only", names)
	}
}

func TestPlatformDispatch_TermuxDoesNotReadImages(t *testing.T) {
	noSessionEnv(t)
	t.Setenv("TERMUX_VERSION", "0.119")
	withPlatform(t, "linux", "wl-paste", "xclip", "powershell.exe")
	calls := recordingClipboard(t, func(string, []string) ([]byte, bool) {
		t.Fatal("no backend may be probed for an image on Termux")
		return nil, false
	})

	if _, ok := ReadClipboardImageBytes(); ok {
		t.Error("Termux reported an image")
	}
	if len(*calls) != 0 {
		t.Errorf("backends consulted = %v, want none", *calls)
	}
}

func TestPlatformDispatch_DarwinUsesOsascriptOnly(t *testing.T) {
	noSessionEnv(t)
	withPlatform(t, "darwin", "osascript", "xclip", "wl-paste")
	want := tinyPNG(t)
	calls := recordingClipboard(t, func(name string, args []string) ([]byte, bool) {
		if name != "osascript" {
			t.Fatalf("unexpected command %q", name)
		}
		if err := os.WriteFile(macScriptPath(t, args[1]), want, 0o600); err != nil {
			t.Fatal(err)
		}
		return nil, true
	})

	data, ok := ReadClipboardImageBytes()
	if !ok || !bytes.Equal(data, want) {
		t.Fatalf("ReadClipboardImageBytes = (%q,%v), want the pasteboard PNG", data, ok)
	}
	if names := commandNames(*calls); len(names) != 1 || names[0] != "osascript" {
		t.Errorf("backends consulted = %v, want osascript only", names)
	}
}

func TestPlatformDispatch_NoBackendIsSilent(t *testing.T) {
	noSessionEnv(t)
	withPlatform(t, "linux") // nothing installed
	calls := recordingClipboard(t, func(string, []string) ([]byte, bool) {
		t.Fatal("no backend exists, so none may be consulted")
		return nil, false
	})

	if _, ok := ReadClipboardImageBytes(); ok {
		t.Error("a platform with no clipboard backend reported an image")
	}
	if len(*calls) != 0 {
		t.Errorf("backends consulted = %v, want none", *calls)
	}
}

// assertCalls compares the recorded backends with the expected order.
func assertCalls(t *testing.T, calls []string, want []string) {
	t.Helper()
	got := commandNames(calls)
	if len(got) != len(want) {
		t.Fatalf("backends consulted = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("call %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// assertText compares a clipboard text read with the expected value.
func assertText(t *testing.T, text string, ok bool, want string) {
	t.Helper()
	if want == "" {
		if ok || text != "" {
			t.Fatalf("ReadClipboardText = (%q,%v), want no text", text, ok)
		}
		return
	}
	if !ok || text != want {
		t.Fatalf("ReadClipboardText = (%q,%v), want %q", text, ok, want)
	}
}

func TestPlatformDispatch_TextReadsThroughCopySideBackends(t *testing.T) {
	cases := []struct {
		name  string
		goos  string
		env   map[string]string
		tools []string
		want  string
		calls []string // program names, in order
	}{
		{
			name: "darwin pbpaste", goos: "darwin", tools: []string{"pbpaste"},
			want: "mac text", calls: []string{"pbpaste"},
		},
		{
			name: "windows Get-Clipboard", goos: "windows", tools: []string{"powershell.exe"},
			want: "win text", calls: []string{"powershell.exe"},
		},
		{
			name: "x11 xclip", goos: "linux", env: map[string]string{"DISPLAY": ":0"},
			tools: []string{"wl-paste", "xclip"}, want: "x11 text", calls: []string{"xclip"},
		},
		{
			name: "x11 xsel only", goos: "linux", env: map[string]string{"DISPLAY": ":0"},
			tools: []string{"xclip", "xsel"}, want: "xsel text", calls: []string{"xclip", "xsel"},
		},
		{
			name: "wayland wl-paste", goos: "linux", env: map[string]string{"WAYLAND_DISPLAY": "wayland-0"},
			tools: []string{"wl-paste"}, want: "wayland text", calls: []string{"wl-paste"},
		},
		{
			name: "termux", goos: "linux", env: map[string]string{"TERMUX_VERSION": "0.119"},
			tools: []string{"termux-clipboard-get"}, want: "termux text", calls: []string{"termux-clipboard-get"},
		},
		{
			name: "no display", goos: "linux", tools: []string{"wl-paste", "xclip", "xsel"},
			want: "", calls: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			noSessionEnv(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			withPlatform(t, tc.goos, tc.tools...)
			// xclip is present but cannot serve the "xsel only" case, which is
			// how the fallback to the second X11 tool is exercised.
			xclipFails := tc.want == "xsel text"
			calls := recordingClipboard(t, func(name string, _ []string) ([]byte, bool) {
				if xclipFails && name == "xclip" {
					return nil, false
				}
				return []byte(tc.want), true
			})

			text, ok := ReadClipboardText()
			assertText(t, text, ok, tc.want)
			assertCalls(t, *calls, tc.calls)
		})
	}
}

// TestPlatformDispatch_WaylandTextKeepsItsOrder: text resolution follows the
// session's own backend first, then the X11 tools for the case where Wayland has
// nothing to say (the reference agent resolves text the same way — unlike an
// image read, a text read has no stale-owner hazard).
func TestPlatformDispatch_WaylandTextKeepsItsOrder(t *testing.T) {
	noSessionEnv(t)
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	t.Setenv("DISPLAY", ":0")
	withPlatform(t, "linux", "wl-paste", "xclip", "xsel")
	calls := recordingClipboard(t, func(string, []string) ([]byte, bool) { return nil, false })

	if _, ok := ReadClipboardText(); ok {
		t.Error("an unavailable clipboard reported text")
	}
	want := []string{"wl-paste", "xclip", "xsel"}
	if got := commandNames(*calls); len(got) != len(want) {
		t.Fatalf("backends consulted = %v, want %v", got, want)
	} else {
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("call %d = %q, want %q", i, got[i], want[i])
			}
		}
	}
}

// filePathOutput writes one real file and returns its path plus the text/uri-list
// form a file manager would publish for it.
func filePathOutput(t *testing.T) (string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(path, tinyPNG(t), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, "file://" + path + "\n"
}

// assertOnlyPath checks that a file-path read produced exactly want.
func assertOnlyPath(t *testing.T, want string, calls []string) {
	t.Helper()
	if got := ReadClipboardFilePaths(); len(got) != 1 || got[0] != want {
		t.Errorf("ReadClipboardFilePaths = %v, want [%s] (%v)", got, want, calls)
	}
}

func TestPlatformDispatch_FilePathsFollowTheSession(t *testing.T) {
	real, uriList := filePathOutput(t)

	t.Run("wayland asks wl-paste", func(t *testing.T) {
		noSessionEnv(t)
		t.Setenv("WAYLAND_DISPLAY", "wayland-0")
		withPlatform(t, "linux", "wl-paste", "xclip")
		calls := recordingClipboard(t, wantURIList(t, "wl-paste", uriList))
		assertOnlyPath(t, real, *calls)
	})

	t.Run("x11 asks xclip", func(t *testing.T) {
		noSessionEnv(t)
		t.Setenv("DISPLAY", ":0")
		withPlatform(t, "linux", "wl-paste", "xclip")
		calls := recordingClipboard(t, wantURIList(t, "xclip", uriList))
		assertOnlyPath(t, real, *calls)
	})

	t.Run("wsl asks the Windows clipboard", func(t *testing.T) {
		noSessionEnv(t)
		t.Setenv("WSL_DISTRO_NAME", "Ubuntu")
		withPlatform(t, "linux", "powershell.exe")
		recordingClipboard(t, func(name string, _ []string) ([]byte, bool) {
			if name != "powershell.exe" {
				t.Fatalf("unexpected command %q", name)
			}
			return []byte(`C:\Users\me\shot.png`), true
		})
		// The Windows path does not exist here, so the filter drops it: what
		// matters is that PowerShell was asked at all.
		if got := ReadClipboardFilePaths(); got != nil {
			t.Errorf("ReadClipboardFilePaths = %v, want nil for a Windows-only path", got)
		}
	})
}

// wantURIList returns a fake runner handler that answers only cmd with the
// text/uri-list flavour of a file manager copy.
func wantURIList(t *testing.T, cmd string, uriList string) func(string, []string) ([]byte, bool) {
	t.Helper()
	return func(name string, args []string) ([]byte, bool) {
		if name != cmd {
			t.Fatalf("unexpected command %q", name)
		}
		if args[0] != "--type" && args[3] != "text/uri-list" {
			t.Fatalf("unexpected args %v", args)
		}
		return []byte(uriList), true
	}
}
