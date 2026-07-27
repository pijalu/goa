// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package lsp

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

// installNpm installs a Node package into binDir using npm (or bun), returning
// the path to the package's binary in node_modules/.bin. Idempotent.
func installNpm(pkg, binary, binDir string) (string, error) {
	if p, ok := existingBin(binDir, binary); ok {
		return p, nil
	}
	if err := os.MkdirAll(binDir, 0755); err != nil {
		return "", err
	}
	tool, args := npmRunner(pkg)
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
func npmRunner(pkg string) (string, []string) {
	if _, err := lookPath("npm"); err == nil {
		return "npm", []string{"install", "--prefix", ".", pkg}
	}
	if _, err := lookPath("bun"); err == nil {
		return "bun", []string{"add", pkg}
	}
	return "", nil
}

// installDownload fetches and extracts a server archive into binDir.
// Currently a stub: download-based installs (jdtls) require an HTTP fetch +
// untar, which is environment-specific; we surface a clear error so the caller
// falls back to "server unavailable" rather than failing startup.
func installDownload(url, binary, binDir, kind string) (string, error) {
	if p, ok := existingBin(binDir, binary); ok {
		return p, nil
	}
	return "", fmt.Errorf("lsp: download install for %q not yet supported (url %s); install %s manually", binary, url, binary)
}
