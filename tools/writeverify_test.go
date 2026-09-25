// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal"
)

// This file pins edit-tool Issue 3's mitigation end-to-end at the tool boundary:
//
//  1. A write whose read-back does NOT match what was sent must fail with
//     write_verify_failed — never report a success that does not exist on disk.
//  2. Successful results must show the resolved path whenever the write landed
//     somewhere other than the requested path, so the "which file did I edit?"
//     confusion that produced the original report cannot recur silently.

// unverifiableWriteTarget returns a path where a write SUCCEEDS but a read-back
// cannot observe the written bytes — /dev/null discards writes and reads back
// empty. This makes the verification-failure path deterministic without
// permission games, concurrent writers or filesystem races. Skips on platforms
// where /dev/null is unavailable or not writable (Windows).
func unverifiableWriteTarget(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("requires a Unix /dev/null")
	}
	if err := os.WriteFile("/dev/null", []byte("probe"), 0o644); err != nil {
		t.Skipf("/dev/null is not writable here: %v", err)
	}
	if data, err := os.ReadFile("/dev/null"); err != nil || len(data) != 0 {
		t.Skipf("/dev/null does not read back empty: %d bytes, err=%v", len(data), err)
	}
	return "/dev/null"
}

func wantToolError(t *testing.T, err error, tool, wantType string) *internal.ToolError {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s error, got nil", wantType)
	}
	var te *internal.ToolError
	if !errors.As(err, &te) {
		t.Fatalf("error %v (%T) is not a *internal.ToolError", err, err)
	}
	if te.Tool != tool || te.Type != wantType {
		t.Errorf("tool error = %s/%s, want %s/%s (detail: %s)", te.Tool, te.Type, tool, wantType, te.Detail)
	}
	return te
}

// TestWriteVerifyEditFailureNotReportedAsSuccess is the Issue-3 regression for
// the edit side. writeEditResult is the single write funnel every edit path
// (classic replace, operation, batch) goes through, so forcing it onto a target
// whose read-back cannot match must produce write_verify_failed and no result
// body: an edit must never report success for bytes that are not on disk.
func TestWriteVerifyEditFailureNotReportedAsSuccess(t *testing.T) {
	target := unverifiableWriteTarget(t)
	tool := &EditFileTool{}

	out, err := tool.writeEditResult(target, target, "verified content\n")
	te := wantToolError(t, err, "edit", "write_verify_failed")
	if out != "" {
		t.Errorf("failed edit must not return a result body, got %q", out)
	}
	if !strings.Contains(te.Detail, target) {
		t.Errorf("detail %q should name the resolved path %q", te.Detail, target)
	}
}

// TestVerifyWriteFunnelsUseVerifiedWrite guards the invariant the mitigation
// rests on: the two write funnels must persist through WriteFileVerified, with
// no remaining raw os.WriteFile call that could report an unverified success.
// Reading the package's own sources is deliberate — a behavioural test cannot
// see a newly added bypass that its inputs never reach.
func TestVerifyWriteFunnelsUseVerifiedWrite(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test file")
	}
	dir := filepath.Dir(thisFile)
	for _, file := range []string{"editfile.go", "writefile.go"} {
		src, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		code := string(src)
		if !strings.Contains(code, "WriteFileVerified(") {
			t.Errorf("%s must persist through WriteFileVerified", file)
		}
		if strings.Contains(code, "os.WriteFile(") {
			t.Errorf("%s contains a raw os.WriteFile call: writes must go through WriteFileVerified so every success is read-back verified", file)
		}
	}
}

// TestWriteVerifyWriteFailureNotReportedAsSuccess covers the write tool
// (same funnel semantics): a discarded write must not return a success preview.
func TestWriteVerifyWriteFailureNotReportedAsSuccess(t *testing.T) {
	target := unverifiableWriteTarget(t)
	tool := &WriteFileTool{}

	out, err := tool.Execute(`{"path":"` + target + `","content":"verified content"}`)
	wantToolError(t, err, "write", "write_verify_failed")
	if strings.Contains(out, "Written") {
		t.Errorf("failed write must not report success, got %q", out)
	}
}

// TestVerifyResolvedPathNote locks the note's exact contract: silence when the
// write landed where the caller asked, and the resolved destination otherwise.
func TestVerifyResolvedPathNote(t *testing.T) {
	cases := []struct {
		name      string
		resolved  string
		requested string
		want      string
	}{
		{"identical absolute path", "/a/b/c.txt", "/a/b/c.txt", ""},
		{"identical after cleaning", "/a/b/c.txt", "/a/b/../b/c.txt", ""},
		{"at-prefixed request matches", "/a/b/c.txt", "@/a/b/c.txt", ""},
		{"relative request resolves elsewhere", "/a/b/c.txt", "c.txt", " (resolved: /a/b/c.txt)"},
		{"fuzzy redirect", "/a/b/closest.txt", "/a/b/c.txt", " (resolved: /a/b/closest.txt)"},
		{"empty resolved path", "", "c.txt", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolvedPathNote(tc.resolved, tc.requested); got != tc.want {
				t.Errorf("resolvedPathNote(%q, %q) = %q, want %q", tc.resolved, tc.requested, got, tc.want)
			}
		})
	}
}

const resolvedNoteFixture = "alpha\nbeta\ngamma\n"

// editedFixture is the expected whole-file content after every edit mode below
// replaced line 2 ("beta") with "BETA".
const editedFixture = "alpha\nBETA\ngamma\n"

// editResultMode describes one edit path that writes the file: the payload
// builder takes the path to address.
type editResultMode struct {
	name  string
	input func(path string) string
}

// editResultModes exercises every edit path that writes: classic search/replace,
// a line operation, and a batch (executeMulti).
var editResultModes = []editResultMode{
	{"search_replace", func(p string) string {
		return fmt.Sprintf(`{"path":%q,"old_string":"beta","new_string":"BETA"}`, p)
	}},
	{"operation", func(p string) string {
		return fmt.Sprintf(`{"path":%q,"operation":"replace_lines","start_line":2,"end_line":2,"new_content":"BETA"}`, p)
	}},
	{"batch", func(p string) string {
		return fmt.Sprintf(`{"path":%q,"edits":[{"operation":"replace_lines","start_line":2,"end_line":2,"new_content":"BETA"}]}`, p)
	}},
}

func newEditFixture(t *testing.T) (dir, filePath string) {
	t.Helper()
	dir = t.TempDir()
	filePath = filepath.Join(dir, "fixture.txt")
	if err := os.WriteFile(filePath, []byte(resolvedNoteFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, filePath
}

// TestVerifyEditResultShowsResolvedPath: when the caller names a relative path
// (or a fuzzy redirect lands elsewhere), the result must state the absolute path
// that was actually written, on every edit path. When the request already names
// the file, no note is added.
func TestVerifyEditResultShowsResolvedPath(t *testing.T) {
	for _, mode := range editResultModes {
		t.Run(mode.name+"/relative_path", func(t *testing.T) { checkEditRelativePathNote(t, mode) })
		t.Run(mode.name+"/absolute_path", func(t *testing.T) { checkEditAbsolutePathNote(t, mode) })
	}
}

func checkEditRelativePathNote(t *testing.T, mode editResultMode) {
	t.Helper()
	dir, _ := newEditFixture(t)
	t.Chdir(dir)

	out, err := (&EditFileTool{ProjectDir: dir}).Execute(mode.input("fixture.txt"))
	if err != nil {
		t.Fatalf("edit failed: %v", err)
	}
	abs, err := filepath.Abs("fixture.txt")
	if err != nil {
		t.Fatal(err)
	}
	assertNoteAndContent(t, out, "(resolved: "+abs+")", filepath.Join(dir, "fixture.txt"))
}

func checkEditAbsolutePathNote(t *testing.T, mode editResultMode) {
	t.Helper()
	dir, filePath := newEditFixture(t)

	out, err := (&EditFileTool{ProjectDir: dir}).Execute(mode.input(filePath))
	if err != nil {
		t.Fatalf("edit failed: %v", err)
	}
	assertNoteAndContent(t, out, "", filePath)
}

// assertNoteAndContent asserts the resolved-path note (wantNote == "" means the
// note must be absent) and that the reported success is real: the file on disk
// holds exactly the edited content.
func assertNoteAndContent(t *testing.T, out, wantNote, path string) {
	t.Helper()
	if wantNote == "" {
		if strings.Contains(out, "(resolved:") {
			t.Errorf("result must not carry a resolved-path note when the request already named the file:\n%s", out)
		}
	} else if !strings.Contains(out, wantNote) {
		t.Errorf("result missing resolved path note %q:\n%s", wantNote, out)
	}
	if got := readFileT(t, path); got != editedFixture {
		t.Errorf("file content = %q, want %q", got, editedFixture)
	}
}

// TestVerifyWriteResultShowsResolvedPath also pins the renderer contract: the
// note goes on the stats line, so the "[write: <path>]" header the write
// renderer parses with parseResultHeader keeps the requested path intact.
func TestVerifyWriteResultShowsResolvedPath(t *testing.T) {
	t.Run("relative_path", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)

		out, err := (&WriteFileTool{}).Execute(`{"path":"out.txt","content":"hello\n"}`)
		if err != nil {
			t.Fatalf("write failed: %v", err)
		}
		if first := strings.SplitN(out, "\n", 2)[0]; first != "[write: out.txt]" {
			t.Errorf("header = %q, want %q (the renderer parses it for the path)", first, "[write: out.txt]")
		}
		abs, err := filepath.Abs("out.txt")
		if err != nil {
			t.Fatal(err)
		}
		if want := "(resolved: " + abs + ")"; !strings.Contains(out, want) {
			t.Errorf("result missing resolved path note %q:\n%s", want, out)
		}
		if got := readFileT(t, filepath.Join(dir, "out.txt")); got != "hello\n" {
			t.Errorf("content = %q, want %q", got, "hello\n")
		}
	})

	t.Run("absolute_path", func(t *testing.T) {
		filePath := filepath.Join(t.TempDir(), "out.txt")

		out, err := (&WriteFileTool{}).Execute(fmt.Sprintf(`{"path":%q,"content":"hello\n"}`, filePath))
		if err != nil {
			t.Fatalf("write failed: %v", err)
		}
		if strings.Contains(out, "(resolved:") {
			t.Errorf("result must not carry a resolved-path note when the request already named the file:\n%s", out)
		}
	})
}
