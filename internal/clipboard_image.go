// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package internal

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	_ "image/gif"  // register GIF decoder
	_ "image/jpeg" // register JPEG decoder
)

// Image paste support. Goa reads the clipboard directly on the explicit paste
// key (Ctrl+V) rather than relying on the terminal to forward image bytes,
// because no terminal emulator does. Platform dispatch mirrors the reference
// agents (opencode, pi):
//
//	darwin : osascript / NSPasteboard (PNGf), pbpaste, «class furl» file list
//	windows: powershell.exe (System.Windows.Forms.Clipboard)
//	wsl    : powershell.exe reaches the *Windows* clipboard
//	linux  : wl-paste (Wayland), xclip (X11)
//
// Every helper runs under a timeout: a wedged clipboard daemon (a stalled
// wl-paste holds the selection lock) must not freeze the TUI input loop.
//
// Clipboard resolution runs on the command loop, so that timeout is the UI's
// worst-case stall. Real helpers answer in milliseconds; 1s is generous, and the
// paste precedence consults at most three flavours, bounding the worst case at
// roughly 3s.
const clipboardTimeout = 1 * time.Second

// runClipboardCommand executes a clipboard helper and returns its stdout.
// ok is false when the binary is missing, the command fails, or it times out.
//
// It is a var so the platform backends below can be unit-tested without a real
// clipboard (or a real osascript/wl-paste): the tests swap in a fake runner.
var runClipboardCommand = execClipboardCommand

// execClipboardCommand is the production clipboard command runner.
func execClipboardCommand(timeout time.Duration, name string, args ...string) ([]byte, bool) {
	if !hasBinary(name) {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return nil, false
	}
	return out, true
}

// ReadClipboardImage reads an image from the clipboard.
// Returns nil, nil when the clipboard doesn't contain an image (silent, may be
// a text or file paste). Returns nil, error only for genuine decode failures.
func ReadClipboardImage() (image.Image, error) {
	data, ok := readClipboardImageBytes()
	if !ok || len(data) == 0 {
		return nil, nil
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("clipboard image decode: %w", err)
	}
	return img, nil
}

// readClipboardImageBytes returns raw image bytes from the clipboard. ok=false
// means "no image present" (never an error: text and file pastes are normal).
func readClipboardImageBytes() ([]byte, bool) {
	switch runtime.GOOS {
	case "darwin":
		return readDarwinClipboardImage()
	case "windows":
		return readPowerShellClipboardImage()
	}
	// Linux, including WSL: Wayland first, then X11, then the Windows clipboard
	// (a WSL screenshot lands only in the Windows clipboard).
	if data, ok := readWlPasteImage(); ok {
		return data, true
	}
	if data, ok := readXclipImage(); ok {
		return data, true
	}
	return readPowerShellClipboardImage()
}

// readDarwinClipboardImage exports the clipboard's PNG flavour to a temporary
// file, the only lossless way to get bytes out of NSPasteboard via osascript.
// An empty or non-image clipboard makes the AppleScript raise, which osascript
// reports as a non-zero exit.
func readDarwinClipboardImage() ([]byte, bool) {
	f, err := os.CreateTemp("", "goa-clip-*.png")
	if err != nil {
		return nil, false
	}
	path := f.Name()
	f.Close()
	os.Remove(path) // AppleScript needs the name to be free to create it
	defer os.Remove(path)

	script := `set imageData to the clipboard as «class PNGf»` + "\n" +
		`set fileRef to open for access POSIX file "` + path + `" with write permission` + "\n" +
		`set eof fileRef to 0` + "\n" +
		`write imageData to fileRef` + "\n" +
		`close access fileRef`
	if _, ok := runClipboardCommand(clipboardTimeout, "osascript", "-e", script); !ok {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return nil, false
	}
	return data, true
}

// readPowerShellClipboardImage reads the Windows clipboard as a PNG and returns
// its bytes. Used natively on Windows and on WSL, where only PowerShell can see
// the Windows clipboard.
func readPowerShellClipboardImage() ([]byte, bool) {
	psCmd := `Add-Type -AssemblyName System.Windows.Forms;` +
		`Add-Type -AssemblyName System.Drawing;` +
		`$img = [System.Windows.Forms.Clipboard]::GetImage();` +
		`if ($img -eq $null) { exit 1; }` +
		`$ms = New-Object System.IO.MemoryStream;` +
		`$img.Save($ms, [System.Drawing.Imaging.ImageFormat]::Png);` +
		`[System.Convert]::ToBase64String($ms.ToArray())`
	out, ok := runClipboardCommand(clipboardTimeout+3*time.Second, "powershell.exe", "-NoProfile", "-Command", psCmd)
	if !ok {
		return nil, false
	}
	b64 := strings.TrimSpace(string(out))
	if b64 == "" {
		return nil, false
	}
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(data) == 0 {
		return nil, false
	}
	return data, true
}

// readWlPasteImage reads an image from a Wayland clipboard. The offered MIME
// types are inspected first so a PNG, JPEG, WebP or GIF is taken as-is; only
// when the type list is unavailable does it fall back to asking for PNG.
func readWlPasteImage() ([]byte, bool) {
	if out, ok := runClipboardCommand(clipboardTimeout, "wl-paste", "--list-types"); ok {
		if mime := preferredImageMime(splitLines(string(out))); mime != "" {
			// --no-newline keeps the trailing byte from being appended to the
			// image payload (a truncated PNG is an undecodable PNG).
			if data, ok := runClipboardCommand(clipboardTimeout, "wl-paste", "--type", mime, "--no-newline"); ok && len(data) > 0 {
				return data, true
			}
		}
		return nil, false
	}
	// Old wl-clipboard without --list-types.
	data, ok := runClipboardCommand(clipboardTimeout, "wl-paste", "--type", "image/png", "--no-newline")
	if !ok || len(data) == 0 {
		return nil, false
	}
	return data, true
}

// readXclipImage reads an image from an X11 clipboard, selecting the best
// offered image target.
func readXclipImage() ([]byte, bool) {
	if out, ok := runClipboardCommand(clipboardTimeout, "xclip", "-selection", "clipboard", "-t", "TARGETS", "-o"); ok {
		if mime := preferredImageMime(splitLines(string(out))); mime != "" {
			if data, ok := runClipboardCommand(clipboardTimeout, "xclip", "-selection", "clipboard", "-t", mime, "-o"); ok && len(data) > 0 {
				return data, true
			}
		}
		return nil, false
	}
	data, ok := runClipboardCommand(clipboardTimeout, "xclip", "-selection", "clipboard", "-t", "image/png", "-o")
	if !ok || len(data) == 0 {
		return nil, false
	}
	return data, true
}

// preferredImageMime picks the best offered image MIME type. Preference order
// matches what every vision API accepts universally; a non-image or empty offer
// yields "".
func preferredImageMime(types []string) string {
	var fallback string
	for _, want := range []string{"image/png", "image/jpeg", "image/webp", "image/gif"} {
		for _, t := range types {
			base := strings.TrimSpace(strings.SplitN(t, ";", 2)[0])
			if strings.EqualFold(base, want) {
				return base
			}
		}
	}
	for _, t := range types {
		base := strings.TrimSpace(strings.SplitN(t, ";", 2)[0])
		if fallback == "" && strings.HasPrefix(strings.ToLower(base), "image/") {
			fallback = base
		}
	}
	return fallback
}

// ReadClipboardText reads plain text from the clipboard. ok=false when the
// clipboard is empty or no reader is available; callers then simply do nothing.
func ReadClipboardText() (string, bool) {
	if out, ok := readClipboardTextBytes(); ok && len(out) > 0 {
		return string(out), true
	}
	return "", false
}

func readClipboardTextBytes() ([]byte, bool) {
	switch runtime.GOOS {
	case "darwin":
		return runClipboardCommand(clipboardTimeout, "pbpaste")
	case "windows":
		out, ok := runClipboardCommand(clipboardTimeout, "powershell.exe", "-NoProfile", "-Command", "Get-Clipboard -Raw")
		return out, ok
	}
	if out, ok := runClipboardCommand(clipboardTimeout, "wl-paste", "--no-newline"); ok {
		return out, true
	}
	if out, ok := runClipboardCommand(clipboardTimeout, "xclip", "-selection", "clipboard", "-o"); ok {
		return out, true
	}
	return nil, false
}

// ReadClipboardFilePaths returns absolute paths of files copied from a file
// manager (Finder ⇄ Explorer ⇄ Nautilus). Empty when the clipboard holds no
// file list — the common case of a screenshot copy returns a pixel image
// instead, which ReadClipboardImage handles.
func ReadClipboardFilePaths() []string {
	switch runtime.GOOS {
	case "darwin":
		return darwinClipboardFilePaths()
	case "windows":
		return powershellClipboardFilePaths()
	}
	if paths := uriListClipboardFilePaths(); len(paths) > 0 {
		return paths
	}
	return powershellClipboardFilePaths()
}

// darwinClipboardFilePaths resolves Finder's «class furl» clipboard flavour.
// A single file is the common case; a multi-selection arrives as a list.
func darwinClipboardFilePaths() []string {
	single := `try
	set p to POSIX path of (the clipboard as «class furl»)
	return p
on error
end try
try
	set acc to ""
	repeat with f in (the clipboard as list)
		try
			set acc to acc & (POSIX path of f) & linefeed
		end try
	end repeat
	return acc
on error
	return ""
end try`
	out, ok := runClipboardCommand(clipboardTimeout, "osascript", "-e", single)
	if !ok {
		return nil
	}
	return existingRegularFiles(splitLines(string(out)))
}

// powershellClipboardFilePaths resolves Windows Explorer's FileDropList. Older
// PowerShell hosts reject -Format, in which case there is nothing to report.
func powershellClipboardFilePaths() []string {
	out, ok := runClipboardCommand(clipboardTimeout+2*time.Second, "powershell.exe", "-NoProfile", "-Command", "Get-Clipboard -Format FileDropList | ForEach-Object { $_.FullName }")
	if !ok {
		return nil
	}
	return existingRegularFiles(splitLines(string(out)))
}

// uriListClipboardFilePaths resolves the X11/Wayland text/uri-list flavour,
// where a file manager publishes file:// URIs.
func uriListClipboardFilePaths() []string {
	var raw []byte
	if out, ok := runClipboardCommand(clipboardTimeout, "wl-paste", "--type", "text/uri-list", "--no-newline"); ok {
		raw = out
	} else if out, ok := runClipboardCommand(clipboardTimeout, "xclip", "-selection", "clipboard", "-t", "text/uri-list", "-o"); ok {
		raw = out
	}
	if len(raw) == 0 {
		return nil
	}
	var paths []string
	for _, line := range splitLines(string(raw)) {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		u, err := url.Parse(line)
		if err != nil || u.Scheme != "file" {
			continue
		}
		paths = append(paths, u.Path)
	}
	return existingRegularFiles(paths)
}

// existingRegularFiles keeps only paths that name an existing regular file, so
// a stale clipboard entry cannot inject a non-file token into the editor.
func existingRegularFiles(candidates []string) []string {
	var out []string
	for _, p := range candidates {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		info, err := os.Stat(p)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		out = append(out, p)
	}
	return out
}

func splitLines(s string) []string {
	return strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
}

// SaveClipboardImage saves an image into the durable image store and returns the
// path. See ImageStoreDir for why the store is not os.TempDir.
func SaveClipboardImage(img image.Image) (string, error) {
	f, err := NewImageFile(".png")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		os.Remove(f.Name())
		return "", fmt.Errorf("encode png: %w", err)
	}
	return f.Name(), nil
}

// IsImageFile reports whether path names an existing regular file whose bytes
// sniff as an image. This is the single predicate for "is this an attachment".
func IsImageFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 64)
	n, _ := f.Read(head)
	if n == 0 {
		return false
	}
	return sniffImageExt(head[:n]) != ""
}
