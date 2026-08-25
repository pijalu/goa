// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package lsp

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// binName returns the platform-specific binary name (.exe on Windows).
func binName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// existingBin returns the binary path inside binDir if it already exists.
func existingBin(binDir, binary string) (string, bool) {
	p := filepath.Join(binDir, binName(binary))
	if fileExists(p) {
		return p, true
	}
	return "", false
}

// runCmd is exec.Command run, swappable in tests.
var downloadClient = &http.Client{Timeout: 2 * time.Minute}

var runCmd = func(name string, args []string, env []string) error {
	cmd := exec.Command(name, args...)
	if len(env) > 0 {
		cmd.Env = env
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v: %v: %s", name, args, err, out)
	}
	return nil
}

// installGo runs `go install <pkg>@latest` with GOBIN=binDir (OpenCode's gopls
// mechanism). Idempotent: reuses an existing binary.
func installGo(pkg, binary, binDir string) (string, error) {
	if p, ok := existingBin(binDir, binary); ok {
		return p, nil
	}
	if _, err := lookPath("go"); err != nil {
		return "", fmt.Errorf("lsp: go toolchain not found, cannot install %s", pkg)
	}
	if err := os.MkdirAll(binDir, 0755); err != nil {
		return "", err
	}
	env := append(os.Environ(), "GOBIN="+binDir)
	if err := runCmd("go", []string{"install", pkg}, env); err != nil {
		return "", err
	}
	if p, ok := existingBin(binDir, binary); ok {
		return p, nil
	}
	return "", fmt.Errorf("lsp: %s installed but %s not found in %s", pkg, binary, binDir)
}

// installDotnet installs a .NET tool into the Goa-managed bin directory.
// Tool installation is explicit and idempotent, preserving workspace isolation.
func installDotnet(pkg, binary, binDir string, extraArgs ...string) (string, error) {
	if p, ok := existingBin(binDir, binary); ok {
		return p, nil
	}
	if _, err := lookPath("dotnet"); err != nil {
		return "", fmt.Errorf("lsp: dotnet toolchain not found, cannot install %s", pkg)
	}
	if err := os.MkdirAll(binDir, 0755); err != nil {
		return "", err
	}
	args := []string{"tool", "install", "--tool-path", binDir}
	args = append(args, extraArgs...)
	args = append(args, pkg)
	if err := runCmd("dotnet", args, nil); err != nil {
		return "", err
	}
	if p, ok := existingBin(binDir, binary); ok {
		return p, nil
	}
	return "", fmt.Errorf("lsp: %s installed but %s not found in %s", pkg, binary, binDir)
}

// installNpm installs a Node package into binDir using npm (or bun), returning
// the path to the package's binary in node_modules/.bin. Idempotent.
func installNpm(pkg, binary, binDir string) (string, error) {
	if p, ok := existingBin(binDir, binary); ok {
		return p, nil
	}
	if err := os.MkdirAll(binDir, 0755); err != nil {
		return "", err
	}
	tool, args := npmRunner(pkg, binDir)
	if tool == "" {
		return "", fmt.Errorf("lsp: neither npm nor bun found, cannot install %s", pkg)
	}
	if err := runCmd(tool, args, nil); err != nil {
		return "", err
	}
	// npm/bun install into binDir/node_modules/.bin.
	cand := filepath.Join(binDir, "node_modules", ".bin", binName(binary))
	if fileExists(cand) {
		return cand, nil
	}
	if p, ok := existingBin(binDir, binary); ok {
		return p, nil
	}
	return "", fmt.Errorf("lsp: %s installed but %s not found", pkg, binary)
}

// npmRunner picks npm or bun and the install argv for a package into binDir.
func npmRunner(pkg, prefix string) (string, []string) {
	if _, err := lookPath("npm"); err == nil {
		return "npm", []string{"install", "--prefix", prefix, pkg}
	}
	if _, err := lookPath("bun"); err == nil {
		return "bun", []string{"add", "--cwd", prefix, pkg}
	}
	return "", nil
}

// installDownload fetches a bounded gzip/tar archive and extracts only safe
// paths. The launcher is copied into binDir; sibling runtime files are kept in
// an isolated archive directory so multi-file servers remain usable.
// installDownload fetches a bounded gzip/tar archive and extracts only safe
// paths. The launcher is copied into binDir; sibling runtime files are kept in
// an isolated archive directory so multi-file servers remain usable.
func installDownload(url, binary, binDir, checksum string) (string, error) {
	if p, ok := existingBin(binDir, binary); ok {
		return p, nil
	}
	if err := validateDownloadURL(url); err != nil {
		return "", err
	}
	archive, err := fetchArchive(url, binDir, checksum)
	if err != nil {
		return "", err
	}
	defer os.Remove(archive)
	return extractArchive(archive, binary, binDir)
}

func validateDownloadURL(url string) error {
	if !strings.HasPrefix(strings.ToLower(url), "https://") {
		return fmt.Errorf("lsp: download URL must use HTTPS")
	}
	return nil
}

func fetchArchive(url, binDir, checksum string) (string, error) {
	resp, err := downloadClient.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("lsp: download status %s", resp.Status)
	}
	if err := os.MkdirAll(binDir, 0755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(binDir, ".lsp-archive-*")
	if err != nil {
		return "", err
	}
	name := tmp.Name()
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(tmp, hash), io.LimitReader(resp.Body, 256<<20+1))
	closeErr := tmp.Close()
	if copyErr != nil || closeErr != nil {
		return name, firstError(copyErr, closeErr)
	}
	if written > 256<<20 {
		return name, fmt.Errorf("lsp: download exceeds 256 MiB limit")
	}
	if checksum != "" && strings.TrimPrefix(strings.ToLower(strings.TrimSpace(checksum)), "sha256:") != hex.EncodeToString(hash.Sum(nil)) {
		return name, fmt.Errorf("lsp: archive checksum mismatch")
	}
	return name, nil
}

func firstError(a, b error) error {
	if a != nil {
		return a
	}
	return b
}

func extractArchive(archive, binary, binDir string) (string, error) {
	f, err := os.Open(archive)
	if err != nil {
		return "", err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("lsp: invalid gzip: %w", err)
	}
	defer gz.Close()
	dest := filepath.Join(binDir, ".archive", binary)
	if err := os.MkdirAll(dest, 0755); err != nil {
		return "", err
	}
	if err := extractTar(gz, dest); err != nil {
		return "", err
	}
	candidate := locateArchiveBinary(dest, binary)
	if candidate == "" {
		return "", fmt.Errorf("lsp: archive contains no %s", binary)
	}
	if err := os.Chmod(candidate, 0755); err != nil {
		return "", fmt.Errorf("lsp: make %s executable: %w", binary, err)
	}
	return candidate, nil
}

func extractTar(r io.Reader, dest string) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := extractTarEntry(tr, dest, hdr); err != nil {
			return err
		}
	}
}

func extractTarEntry(r io.Reader, dest string, hdr *tar.Header) error {
	out, err := archivePath(dest, hdr.Name)
	if err != nil {
		return err
	}
	if hdr.Typeflag == tar.TypeSymlink || hdr.Typeflag == tar.TypeLink {
		return fmt.Errorf("lsp: archive links are not allowed: %s", hdr.Name)
	}
	if hdr.FileInfo().IsDir() {
		return os.MkdirAll(out, 0755)
	}
	if err := os.MkdirAll(filepath.Dir(out), 0755); err != nil {
		return err
	}
	o, err := os.OpenFile(out, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0755)
	if err != nil {
		return err
	}
	_, copyErr := io.CopyN(o, r, hdr.Size)
	closeErr := o.Close()
	return firstError(copyErr, closeErr)
}

func archivePath(dest, name string) (string, error) {
	rel := filepath.Clean(name)
	if rel == "." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("lsp: archive path traversal: %s", name)
	}
	out := filepath.Join(dest, rel)
	if !strings.HasPrefix(out, dest+string(filepath.Separator)) {
		return "", fmt.Errorf("lsp: archive path traversal: %s", name)
	}
	return out, nil
}

func locateArchiveBinary(dest, binary string) string {
	candidate := filepath.Join(dest, binary)
	if fileExists(candidate) {
		return candidate
	}
	var found string
	_ = filepath.Walk(dest, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() || found != "" {
			return err
		}
		if strings.EqualFold(info.Name(), filepath.Base(binary)) {
			found = path
		}
		return nil
	})
	return found
}
