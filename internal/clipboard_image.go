// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package internal

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"strings"

	_ "image/jpeg" // register JPEG decoder
)

// ReadClipboardImage reads an image from the clipboard.
// Returns nil, nil when the clipboard doesn't contain an image (silent, may be text paste).
// Returns nil, error for actual errors (binary not found, etc.).
func ReadClipboardImage() (image.Image, error) {
	data, err := readClipboardImageBytes()
	if err != nil || len(data) == 0 {
		return nil, err
	}

	// Try to decode as PNG, JPEG, or BMP
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("clipboard image decode: %w", err)
	}
	return img, nil
}

// readClipboardImageBytes returns raw image bytes from the clipboard.
// Uses platform-specific tools to extract image data.
func readClipboardImageBytes() ([]byte, error) {
	switch {
	case hasBinary("wl-paste"):
		return exec.Command("wl-paste", "--type", "image/png").Output()
	case hasBinary("xclip"):
		return exec.Command("xclip", "-selection", "clipboard", "-t", "image/png", "-o").Output()
	case hasBinary("powershell.exe"):
		// WSL: use PowerShell to get clipboard as BMP, convert to PNG
		return readWindowsClipboardImage()
	}
	return nil, fmt.Errorf("no clipboard image tool found")
}

// readWindowsClipboardImage uses PowerShell to read clipboard as BMP,
// then returns PNG bytes.
func readWindowsClipboardImage() ([]byte, error) {
	psCmd := `Add-Type -AssemblyName System.Windows.Forms;
$img = [System.Windows.Forms.Clipboard]::GetImage();
if ($img -eq $null) { exit 1; }
$ms = New-Object System.IO.MemoryStream;
$img.Save($ms, [System.Drawing.Imaging.ImageFormat]::Png);
[System.Convert]::ToBase64String($ms.ToArray())`
	out, err := exec.Command("powershell.exe", "-NoProfile", "-Command", psCmd).Output()
	if err != nil {
		return nil, fmt.Errorf("powershell clipboard: %w", err)
	}
	b64 := strings.TrimSpace(string(out))
	if b64 == "" {
		return nil, fmt.Errorf("empty clipboard image")
	}
	return base64.StdEncoding.DecodeString(b64)
}

// SaveClipboardImage saves an image to a temp PNG file and returns the path.
func SaveClipboardImage(img image.Image) (string, error) {
	f, err := os.CreateTemp("", "goa-clipboard-*.png")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	defer f.Close()

	if err := png.Encode(f, img); err != nil {
		os.Remove(f.Name())
		return "", fmt.Errorf("encode png: %w", err)
	}
	return f.Name(), nil
}
