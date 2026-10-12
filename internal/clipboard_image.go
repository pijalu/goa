// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package internal

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Image paste support. Goa reads the clipboard directly on the explicit paste
// key (Ctrl+V) rather than relying on the terminal to forward image bytes,
// because no terminal emulator does. Platform dispatch mirrors the reference
// agents (opencode, pi):
//
//	darwin : osascript / NSPasteboard (PNGf), pbpaste, «class furl» file list
//	windows: powershell.exe (System.Windows.Forms.Clipboard)
//	wsl    : powershell.exe reaches the *Windows* clipboard
//	linux  : wl-paste (Wayland), xclip (X11), xsel/termux for text
//
// Every helper runs under a timeout: a wedged clipboard daemon (a stalled
// wl-paste holds the selection lock) must not freeze the TUI input loop.
//
// Clipboard resolution runs on the command loop, so that timeout is the UI's
// worst-case stall. Real helpers answer in milliseconds; 1s is generous, and
// the paste precedence consults at most three flavours, bounding the worst case
// at roughly 3s.
const clipboardTimeout = 1 * time.Second

// The platform seam. Both are variables so the backends of *every* platform can
// be exercised by tests on any host — the reference agent (pi) takes
// {platform, env} the same way, and it is what keeps a Windows or Wayland
// command shape from shipping untested.
var (
	clipboardGOOS = runtime.GOOS

	// clipboardToolAvailable reports whether a clipboard helper exists on PATH,
	// which is how a backend distinguishes "could not answer" from "answered:
	// no image".
	clipboardToolAvailable = hasBinary

	// clipboardProcVersion reads /proc/version, the WSL fingerprint on Linux.
	clipboardProcVersion = func() []byte {
		b, _ := os.ReadFile("/proc/version")
		return b
	}
)

// clipResult is a backend's answer. The three states are load-bearing: "the
// backend answered and there is no image here" must *end* the chain, while "the
// backend could not answer" moves to the next one. Collapsing them into one bool
// is what lets a Wayland session fall through to the X11/XWayland selection and
// paste an image the user does not have there.
type clipResult int

const (
	clipUnavailable clipResult = iota // helper missing, or it failed / timed out
	clipAbsent                        // the backend answered: no image on the clipboard
	clipFound                         // the backend answered with image bytes
)

// runClipboardCommand executes a clipboard helper and returns its stdout.
// ok is false when the binary is missing, the command fails, or it times out.
//
// It is a var so the platform backends below can be unit-tested without a real
// clipboard (or a real osascript/wl-paste): the tests swap in a fake runner.
var runClipboardCommand = execClipboardCommand

// execClipboardCommand is the production clipboard command runner.
func execClipboardCommand(timeout time.Duration, name string, args ...string) ([]byte, bool) {
	if !clipboardToolAvailable(name) {
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

// isWaylandSession reports whether the session's own clipboard is Wayland's. The
// X11 clipboard a Wayland session also exposes is a bridge to the same
// compositor clipboard at best and a stale selection at worst, so it is only a
// fallback for when wl-paste cannot answer at all.
func isWaylandSession() bool {
	return os.Getenv("WAYLAND_DISPLAY") != "" || os.Getenv("XDG_SESSION_TYPE") == "wayland"
}

// isWSL reports whether this is Windows Subsystem for Linux, where Windows
// executables are reachable through interop. A Win+Shift+S screenshot lands only
// in the *Windows* clipboard there, so it is the one place PowerShell is a
// clipboard reader rather than the platform's own backend.
func isWSL() bool {
	if os.Getenv("WSL_DISTRO_NAME") != "" || os.Getenv("WSLENV") != "" {
		return true
	}
	release := strings.ToLower(string(clipboardProcVersion()))
	return strings.Contains(release, "microsoft") || strings.Contains(release, "wsl")
}

// ReadClipboardImageBytes returns the clipboard's image bytes, or ok=false when
// the clipboard holds no image at all, when no backend on this platform can
// answer, or when the bytes are not in a format the image store keeps.
//
// "No image" is never an error: a text paste and a file-manager copy are the
// normal cases, and the paste precedence moves on to those.
func ReadClipboardImageBytes() ([]byte, bool) {
	data, out := readClipboardImageBytes()
	if out != clipFound || len(data) == 0 {
		return nil, false
	}
	// A backend hands over whatever the owner published, so the format is
	// decided by the bytes, exactly as the web upload path decides it. This
	// keeps the store's accepted formats and the paste path's in step.
	if SniffImageExt(headOf(data)) == "" {
		return nil, false
	}
	return data, true
}

// headOf returns the leading bytes used for format sniffing.
func headOf(data []byte) []byte {
	if len(data) > SniffLen {
		return data[:SniffLen]
	}
	return data
}

// readClipboardImageBytes returns raw image bytes from the platform's backends,
// in the order that platform resolves its clipboard.
func readClipboardImageBytes() ([]byte, clipResult) {
	// Termux has no image clipboard and no display tools at all; probing one
	// there is a wasted (and inventable) backend call.
	if os.Getenv("TERMUX_VERSION") != "" {
		return nil, clipAbsent
	}
	switch clipboardGOOS {
	case "darwin":
		return readDarwinClipboardImage()
	case "windows":
		return readPowerShellClipboardImage()
	}
	return readLinuxClipboardImage()
}

// readLinuxClipboardImage walks the Linux backends in the reference order: the
// session's own clipboard first (Wayland, or the Windows clipboard on WSL), then
// X11, then — only on WSL, where a Windows screenshot never reaches the Linux
// clipboard — PowerShell.
//
// A backend that answers "no image" ends the walk unless an image has not been
// found and the Windows clipboard is still worth asking, so an empty Wayland
// clipboard never falls through to a stale X11 selection.
func readLinuxClipboardImage() ([]byte, clipResult) {
	wsl := isWSL()
	var data []byte
	out := clipUnavailable
	if isWaylandSession() || wsl {
		data, out = readWlPasteImage()
	}
	if out == clipUnavailable {
		data, out = readXclipImage()
	}
	if out != clipFound && wsl {
		data, out = readPowerShellClipboardImage()
	}
	return data, out
}

// readDarwinClipboardImage exports the clipboard's PNG flavour to a temporary
// file, the only lossless way to get bytes out of NSPasteboard via osascript.
// An empty or non-image clipboard makes the AppleScript raise, which osascript
// reports as a non-zero exit — that is "no image", not a broken backend.
func readDarwinClipboardImage() ([]byte, clipResult) {
	if !clipboardToolAvailable("osascript") {
		return nil, clipUnavailable
	}
	f, err := os.CreateTemp("", "goa-clip-*.png")
	if err != nil {
		return nil, clipUnavailable
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
		return nil, clipAbsent
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return nil, clipAbsent
	}
	return data, clipFound
}

// readPowerShellClipboardImage reads the Windows clipboard as a PNG and returns
// its bytes. Used natively on Windows and on WSL, where only PowerShell can see
// the Windows clipboard. The image is carried back as base64 on stdout, so no
// path has to be quoted into the script (a path with a quote in it would be an
// injection surface).
func readPowerShellClipboardImage() ([]byte, clipResult) {
	if !clipboardToolAvailable("powershell.exe") {
		return nil, clipUnavailable
	}
	psCmd := `Add-Type -AssemblyName System.Windows.Forms;` +
		`Add-Type -AssemblyName System.Drawing;` +
		`$img = [System.Windows.Forms.Clipboard]::GetImage();` +
		`if ($img -eq $null) { exit 1; }` +
		`$ms = New-Object System.IO.MemoryStream;` +
		`$img.Save($ms, [System.Drawing.Imaging.ImageFormat]::Png);` +
		`[System.Convert]::ToBase64String($ms.ToArray())`
	out, ok := runClipboardCommand(clipboardTimeout+3*time.Second, "powershell.exe", "-NoProfile", "-Command", psCmd)
	if !ok {
		// Either the clipboard holds no image (the script exits 1) or the host
		// is not answering. This is the last backend on every platform that
		// reaches it, so both are "no image".
		return nil, clipAbsent
	}
	b64 := strings.TrimSpace(string(out))
	if b64 == "" {
		return nil, clipAbsent
	}
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(data) == 0 {
		return nil, clipAbsent
	}
	return data, clipFound
}

// readWlPasteImage reads an image from a Wayland clipboard. The offered MIME
// types are inspected first so a PNG, JPEG, WebP or GIF is taken as-is; an
// unadvertised type is never probed, because the request would make the owning
// application perform a conversion it never offered.
func readWlPasteImage() ([]byte, clipResult) {
	if !clipboardToolAvailable("wl-paste") {
		return nil, clipUnavailable
	}
	out, ok := runClipboardCommand(clipboardTimeout, "wl-paste", "--list-types")
	if !ok {
		return nil, clipUnavailable
	}
	mime := preferredImageMime(splitLines(string(out)))
	if mime == "" {
		return nil, clipAbsent
	}
	// --no-newline keeps the trailing byte from being appended to the image
	// payload (a truncated PNG is an undecodable PNG).
	data, ok := runClipboardCommand(clipboardTimeout, "wl-paste", "--type", mime, "--no-newline")
	if !ok {
		return nil, clipUnavailable
	}
	if len(data) == 0 {
		return nil, clipAbsent
	}
	return data, clipFound
}

// readXclipImage reads an image from an X11 clipboard, selecting the best
// offered image target. As on Wayland, a target the owner did not advertise is
// never requested.
func readXclipImage() ([]byte, clipResult) {
	if !clipboardToolAvailable("xclip") {
		return nil, clipUnavailable
	}
	out, ok := runClipboardCommand(clipboardTimeout, "xclip", "-selection", "clipboard", "-t", "TARGETS", "-o")
	if !ok {
		return nil, clipUnavailable
	}
	mime := preferredImageMime(splitLines(string(out)))
	if mime == "" {
		return nil, clipAbsent
	}
	data, ok := runClipboardCommand(clipboardTimeout, "xclip", "-selection", "clipboard", "-t", mime, "-o")
	if !ok {
		return nil, clipUnavailable
	}
	if len(data) == 0 {
		return nil, clipAbsent
	}
	return data, clipFound
}

// preferredImageMime picks the best offered image MIME type. The four listed
// types are exactly the formats the image store keeps (internal.SniffImageExt),
// so a selected flavour is never one the store would have to drop: a clipboard
// that offers only something else reads as "no image" rather than as a paste
// that silently does nothing.
func preferredImageMime(types []string) string {
	for _, want := range []string{"image/png", "image/jpeg", "image/webp", "image/gif"} {
		for _, t := range types {
			base := strings.TrimSpace(strings.SplitN(t, ";", 2)[0])
			if strings.EqualFold(base, want) {
				return want
			}
		}
	}
	return ""
}

// ReadClipboardText reads plain text from the clipboard. ok=false when the
// clipboard is empty or no reader is available; callers then simply do nothing.
func ReadClipboardText() (string, bool) {
	if out, ok := readClipboardTextBytes(); ok && len(out) > 0 {
		return string(out), true
	}
	return "", false
}

// readClipboardTextBytes reads text through the same backends the copy side
// writes with (see nativeCopyCommand in clipboard.go): a box where goa can set
// the clipboard but not read it back would otherwise paste nothing.
func readClipboardTextBytes() ([]byte, bool) {
	switch clipboardGOOS {
	case "darwin":
		return runClipboardCommand(clipboardTimeout, "pbpaste")
	case "windows":
		return runClipboardCommand(clipboardTimeout, "powershell.exe", "-NoProfile", "-Command", "Get-Clipboard -Raw")
	}
	// Linux. Session order as pi's: Termux has no display at all, Wayland is the
	// session's own clipboard, and X11 needs DISPLAY to answer.
	for _, cmd := range linuxTextCommands() {
		if out, ok := runClipboardCommand(clipboardTimeout, cmd[0], cmd[1:]...); ok {
			return out, true
		}
	}
	return nil, false
}

// linuxTextCommands returns the text backends this Linux session can offer, in
// resolution order.
func linuxTextCommands() [][]string {
	var cmds [][]string
	if os.Getenv("TERMUX_VERSION") != "" {
		cmds = append(cmds, []string{"termux-clipboard-get"})
	}
	if isWaylandSession() {
		cmds = append(cmds, []string{"wl-paste", "--type", "text", "--no-newline"})
	}
	if os.Getenv("DISPLAY") != "" {
		cmds = append(cmds,
			[]string{"xclip", "-selection", "clipboard", "-out"},
			[]string{"xsel", "--clipboard", "--output"},
		)
	}
	return cmds
}

// ReadClipboardFilePaths returns absolute paths of files copied from a file
// manager (Finder ⇄ Explorer ⇄ Nautilus). Empty when the clipboard holds no
// file list — the common case of a screenshot copy returns a pixel image
// instead, which ReadClipboardImageBytes handles.
func ReadClipboardFilePaths() []string {
	switch clipboardGOOS {
	case "darwin":
		return darwinClipboardFilePaths()
	case "windows":
		return powershellClipboardFilePaths()
	}
	if paths := uriListClipboardFilePaths(); len(paths) > 0 {
		return paths
	}
	if isWSL() {
		// Explorer's FileDropList lives in the Windows clipboard, which only
		// interop can reach.
		return powershellClipboardFilePaths()
	}
	return nil
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
// where a file manager publishes file:// URIs. Which backend is asked follows
// the session, so a Wayland session is not answered from the X11 selection.
func uriListClipboardFilePaths() []string {
	var raw []byte
	if isWaylandSession() {
		if out, ok := runClipboardCommand(clipboardTimeout, "wl-paste", "--type", "text/uri-list", "--no-newline"); ok {
			raw = out
		}
	} else if os.Getenv("DISPLAY") != "" {
		if out, ok := runClipboardCommand(clipboardTimeout, "xclip", "-selection", "clipboard", "-t", "text/uri-list", "-o"); ok {
			raw = out
		}
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

// MaxClipboardImageBytes bounds one pasted image. A screenshot is a few
// megabytes; the limit exists so a hostile or broken clipboard owner cannot fill
// the disk through a paste.
const MaxClipboardImageBytes = 32 << 20 // 32 MiB

// SaveClipboardImageBytes stores clipboard image bytes in the durable image
// store and returns the stored path.
//
// The bytes are written *as they arrived*, through the same sniff-and-store
// primitive the web upload path uses (internal.StoreImage): the format is
// decided by the content, the stored file is byte-identical to the clipboard,
// and every format that can be uploaded can also be pasted. Re-encoding to PNG
// here would drop the formats the standard library has no decoder for (WebP,
// which both the sniffer and the provider accept) and would rewrite the user's
// pixels for no gain.
func SaveClipboardImageBytes(data []byte) (string, error) {
	if len(data) == 0 {
		return "", ErrNotImage
	}
	head := headOf(data)
	path, _, err := StoreImage(head, bytes.NewReader(data[len(head):]), MaxClipboardImageBytes)
	if err != nil {
		return "", err
	}
	return path, nil
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
	head := make([]byte, SniffLen)
	n, err := io.ReadFull(f, head)
	if n == 0 && err != nil {
		return false
	}
	return SniffImageExt(head[:n]) != ""
}
