// SPDX-License-Identifier: GPL-3.0-or-later
package lsp

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func archiveBytes(t *testing.T, name string, body []byte, typ byte) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	h := &tar.Header{Name: name, Mode: 0755, Size: int64(len(body)), Typeflag: typ}
	if typ == tar.TypeSymlink {
		h.Linkname = string(body)
		h.Size = 0
	}
	if err := tw.WriteHeader(h); err != nil {
		t.Fatal(err)
	}
	if typ != tar.TypeSymlink {
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func withArchiveServer(t *testing.T, data []byte) string {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(data) }))
	t.Cleanup(srv.Close)
	old := downloadClient
	downloadClient = srv.Client()
	t.Cleanup(func() { downloadClient = old })
	return srv.URL
}

func TestInstallDownloadChecksumAndCache(t *testing.T) {
	data := archiveBytes(t, "server", []byte("#!/bin/sh\n"), tar.TypeReg)
	sum := sha256.Sum256(data)
	url := withArchiveServer(t, data)
	dir := t.TempDir()
	got, err := installDownload(url, "server", dir, hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, ".archive", "server", "server")
	if got != want {
		t.Fatalf("path=%q want %q", got, want)
	}
	if _, err := os.Stat(got); err != nil {
		t.Fatal(err)
	}
	cached := filepath.Join(dir, "server")
	if err := os.WriteFile(cached, []byte("cached"), 0755); err != nil {
		t.Fatal(err)
	}
	got, err = installDownload("not-a-url", "server", dir, "")
	if err != nil || got != cached {
		t.Fatalf("cache got %q, %v", got, err)
	}
}

func TestInstallDownloadRejectsChecksumAndLinks(t *testing.T) {
	data := archiveBytes(t, "server", []byte("x"), tar.TypeReg)
	url := withArchiveServer(t, data)
	if _, err := installDownload(url, "server", t.TempDir(), strings.Repeat("0", 64)); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("checksum error=%v", err)
	}
	link := archiveBytes(t, "server", []byte("target"), tar.TypeSymlink)
	url = withArchiveServer(t, link)
	if _, err := installDownload(url, "server", t.TempDir(), ""); err == nil || !strings.Contains(err.Error(), "links") {
		t.Fatalf("link error=%v", err)
	}
}
func TestInstallDotnetPassesArgs(t *testing.T) {
	binDir := t.TempDir()
	oldLook, oldRun := lookPath, runCmd
	lookPath = func(name string) (string, error) {
		if name == "dotnet" {
			return name, nil
		}
		return "", os.ErrNotExist
	}
	var args []string
	runCmd = func(_ string, got []string, _ []string) error {
		args = got
		return os.WriteFile(filepath.Join(binDir, "tool"), []byte("x"), 0755)
	}
	t.Cleanup(func() { lookPath, runCmd = oldLook, oldRun })
	if _, err := installDotnet("example.tool", "tool", binDir, "--prerelease"); err != nil {
		t.Fatal(err)
	}
	want := []string{"tool", "install", "--tool-path", binDir, "--prerelease", "example.tool"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args=%v want=%v", args, want)
	}
}

func TestInstallDotnetUsesToolPath(t *testing.T) {
	binDir := t.TempDir()
	oldLook, oldRun := lookPath, runCmd
	lookPath = func(name string) (string, error) {
		if name == "dotnet" {
			return "/usr/bin/dotnet", nil
		}
		return "", os.ErrNotExist
	}
	var gotName string
	var gotArgs []string
	runCmd = func(name string, args []string, _ []string) error {
		gotName, gotArgs = name, args
		return os.WriteFile(filepath.Join(binDir, "tool"), []byte("x"), 0755)
	}
	t.Cleanup(func() { lookPath, runCmd = oldLook, oldRun })
	got, err := installDotnet("example.tool", "tool", binDir)
	if err != nil {
		t.Fatal(err)
	}
	if gotName != "dotnet" || got != filepath.Join(binDir, "tool") {
		t.Fatalf("command=%s path=%s", gotName, got)
	}
	want := []string{"tool", "install", "--tool-path", binDir, "example.tool"}
	if !reflect.DeepEqual(gotArgs, want) {
		t.Fatalf("args=%v want=%v", gotArgs, want)
	}
}

func TestInstallDownloadRejectsNonHTTPS(t *testing.T) {
	if _, err := installDownload("http://example.invalid/archive", "server", t.TempDir(), ""); err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("error=%v", err)
	}
}
