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
	"sync"
	"testing"
	"time"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/ansi"
)

// --- BUG-01: editfile out-of-range end_line must return a ToolError, not panic. ---

func TestEditFile_ReplaceLines_EndLineOverflow_ReturnsToolError(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "ten.txt")
	// 10-line file
	var b strings.Builder
	for i := 1; i <= 10; i++ {
		b.WriteString("line")
		b.WriteByte('\n')
	}
	if err := os.WriteFile(filePath, []byte(b.String()), 0644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFileTool{WorktreeMgr: nil, ProjectDir: dir}
	_, err := tool.Execute(`{"path": "` + filePath + `", "operation": "replace_lines", "start_line": 5, "end_line": 1000, "new_content": "x"}`)

	if err == nil {
		t.Fatal("expected error for end_line overflow, got nil")
	}
	var te *internal.ToolError
	if !errors.As(err, &te) {
		t.Fatalf("expected *internal.ToolError, got %T: %v", err, err)
	}
	if te.Type != "invalid_range" {
		t.Errorf("expected type invalid_range, got %q", te.Type)
	}
}

func TestEditFile_DeleteLines_EndLineOverflow_ReturnsToolError(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "ten.txt")
	if err := os.WriteFile(filePath, []byte("a\nb\nc\nd\ne\nf\ng\nh\ni\nj\n"), 0644); err != nil {
		t.Fatal(err)
	}
	tool := &EditFileTool{WorktreeMgr: nil, ProjectDir: dir}
	_, err := tool.Execute(`{"path": "` + filePath + `", "operation": "delete_lines", "start_line": 3, "end_line": 99999}`)
	if err == nil {
		t.Fatal("expected error for end_line overflow")
	}
	var te *internal.ToolError
	if !errors.As(err, &te) || te.Type != "invalid_range" {
		t.Fatalf("expected invalid_range ToolError, got %v", err)
	}
}

// --- STUB-04: bash env parameter must reach the child process. ---

func TestBash_EnvParameter_AppliedToCommand(t *testing.T) {
	t.Setenv("GOA_TEST_VAR", "")
	tool := &BashTool{}
	out, err := tool.Execute(`{"command":"printf %s $GOA_TEST_VAR","env":{"GOA_TEST_VAR":"xyz"}}`)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(out, "xyz") {
		t.Errorf("expected output to contain env value %q, got %q", "xyz", out)
	}
}

// --- BUG-02: todo ids must not collide after remove. ---

func TestTodoList_NoDuplicateIDAfterRemove(t *testing.T) {
	tool := &TodoListTool{}
	add := func() string {
		out, err := tool.Execute(`{"action":"add","description":"task"}`)
		if err != nil {
			t.Fatalf("add: %v", err)
		}
		// out looks like "[todo_list] Added todo-N: task"
		i := strings.Index(out, "todo-")
		j := strings.Index(out[i:], ":")
		if i < 0 || j < 0 {
			t.Fatalf("cannot parse id from %q", out)
		}
		return out[i : i+j]
	}

	id1 := add() // todo-1
	id2 := add() // todo-2
	if _, err := tool.Execute(`{"action":"remove","id":"` + id1 + `"}`); err != nil {
		t.Fatalf("remove: %v", err)
	}
	id3 := add()

	if id3 == "todo-2" {
		t.Errorf("id collision: new add produced %s (should be todo-3)", id3)
	}
	if id3 != "todo-3" {
		t.Errorf("expected todo-3, got %s", id3)
	}

	seen := map[string]int{}
	for _, it := range tool.Items() {
		seen[it.ID]++
	}
	for id, n := range seen {
		if n > 1 {
			t.Errorf("duplicate live id %s appears %d times", id, n)
		}
	}
	if id1 == id2 {
		t.Errorf("first two ids collided: %s", id1)
	}
}

// --- BUG-03/04/STUB-07: bgexec race, already-exited stop, long line preserved. ---

func TestBGExec_RaceReaperVsStatusList(t *testing.T) {
	tool := NewBGExecTool()
	startOut, err := tool.Execute(`{"action":"start","command":"printf done; exit 0"}`)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	// extract proc id
	id := parseProcID(t, startOut)

	var wg sync.WaitGroup
	const hammer = 50
	wg.Add(hammer * 2)
	for i := 0; i < hammer; i++ {
		go func() {
			defer wg.Done()
			_, _ = tool.Execute(`{"action":"status","id":"` + id + `"}`)
		}()
		go func() {
			defer wg.Done()
			_, _ = tool.Execute(`{"action":"list"}`)
		}()
	}
	wg.Wait()
	// cleanup (process may have exited)
	_, _ = tool.Execute(`{"action":"stop","id":"` + id + `"}`)
}

func TestBGExec_StopAlreadyExited_NoPanic(t *testing.T) {
	tool := NewBGExecTool()
	startOut, err := tool.Execute(`{"action":"start","command":"true"}`)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	id := parseProcID(t, startOut)
	// wait for it to exit
	proc := getProc(t, tool, id)
	select {
	case <-proc.done:
	case <-time.After(3 * time.Second):
		t.Fatal("process did not exit")
	}
	out, err := tool.Execute(`{"action":"stop","id":"` + id + `"}`)
	if err != nil {
		t.Fatalf("stop on exited process returned error: %v", err)
	}
	if !strings.Contains(out, "already exited") {
		t.Errorf("expected already-exited message, got %q", out)
	}
}

func TestBGExec_ReadOutput_LongLinePreserved(t *testing.T) {
	dir := t.TempDir()
	// 200 KiB single line
	long := strings.Repeat("A", 200*1024)
	cmdFile := filepath.Join(dir, "line.txt")
	if err := os.WriteFile(cmdFile, []byte(long+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	tool := NewBGExecTool()
	startOut, err := tool.Execute(fmt.Sprintf(`{"action":"start","command":"cat %s"}`, cmdFile))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	id := parseProcID(t, startOut)
	proc := getProc(t, tool, id)
	select {
	case <-proc.done:
	case <-time.After(3 * time.Second):
		t.Fatal("cat did not exit")
	}
	out, err := tool.Execute(`{"action":"read","id":"` + id + `","tail_lines":5}`)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(out, long) {
		// Check for scanner error marker to distinguish truncation from tool failure
		if strings.Contains(out, "[bg_exec: read error:") {
			t.Errorf("200KiB line truncated due to scanner error (len out=%d, full output includes error marker):\n%s", len(out), out)
		} else {
			t.Errorf("200KiB line was not preserved (len out=%d)", len(out))
		}
	}
	// A spurious scanner error (e.g. from cmd.Wait() racing with the pipe
	// reader) would insert this marker even when the line is otherwise present.
	if strings.Contains(out, "[bg_exec: read error:") {
		t.Errorf("read output contains scanner error marker:\n%s", out)
	}
	_, _ = tool.Execute(`{"action":"stop","id":"` + id + `"}`)
}

// --- BUG-09: path traversal hardening moved to autonomy policy layer. ---

func TestResolveToolPath_ResolvesOutsidePaths(t *testing.T) {
	project := t.TempDir()
	wm := internal.NewWorktreeManager(project, internal.WorktreeAlways)

	cases := []struct {
		name string
		path string
	}{
		{"relative parent escape", filepath.Join("..", "escape.txt")},
		{"absolute outside root", "/etc/passwd"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolved, err := ResolveToolPath(wm, tc.path)
			if err != nil {
				t.Errorf("expected path resolution for %q, got error: %v", tc.path, err)
			}
			if resolved == "" {
				t.Errorf("expected non-empty resolved path for %q", tc.path)
			}
		})
	}
}

func TestResolveToolPath_NewFileUnderSymlinkedRoot(t *testing.T) {
	// macOS commonly uses symlinked temp directories (/tmp -> /private/tmp).
	// EvalSymlinks fails for non-existent files, which previously caused
	// assertWithinRoot to compare a symlink-resolved root against an
	// unresolved target and falsely report that the path escaped the root.
	if runtime.GOOS == "windows" {
		t.Skip("symlink test skipped on windows")
	}

	realDir := t.TempDir()
	linkDir := filepath.Join(realDir, "link")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Fatal(err)
	}

	wm := internal.NewWorktreeManager(linkDir, internal.WorktreeAlways)
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(linkDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldwd)

	resolved, err := ResolveToolPath(wm, "new_file.html")
	if err != nil {
		t.Fatalf("expected new file under symlinked root to resolve, got %v", err)
	}
	// Abs may return the symlink-resolved real path; the important thing is
	// that the new file is inside the project root, not that the exact
	// symlinked form is preserved.
	want := filepath.Join(linkDir, "new_file.html")
	if dir, err := filepath.EvalSymlinks(filepath.Dir(want)); err == nil {
		want = filepath.Join(dir, filepath.Base(want))
	}
	if resolved != want {
		t.Errorf("resolved = %q, want %q", resolved, want)
	}
}

func TestResolveToolPath_AcceptsInsideRoot(t *testing.T) {
	project := t.TempDir()
	// Create a file inside the project so the absolute path resolves within root.
	inner := filepath.Join(project, "src", "main.go")
	if err := os.MkdirAll(filepath.Dir(inner), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inner, []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}
	wm := internal.NewWorktreeManager(project, internal.WorktreeAlways)
	// Run from inside the project so the relative path resolves within root.
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldwd)

	resolved, err := ResolveToolPath(wm, filepath.Join("src", "main.go"))
	if err != nil {
		t.Fatalf("expected inner path to resolve, got %v", err)
	}
	// Compare via symlink-resolved forms (macOS /var -> /private/var).
	want, _ := filepath.EvalSymlinks(inner)
	if want == "" {
		want = inner
	}
	if resolved != want {
		t.Errorf("resolved = %q, want %q", resolved, want)
	}
}

func TestFuzzyFindFile_SkipsGoaDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".goa"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".goa", "config.yaml"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	// a legitimate non-hidden file to compare against
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("y"), 0644); err != nil {
		t.Fatal(err)
	}
	path, _ := FuzzyFindFile(dir, "config.yaml")
	if strings.Contains(filepath.ToSlash(path), ".goa/") {
		t.Errorf("fuzzy find should not descend into .goa, got %q", path)
	}
}

// --- BUG-08: pty ansi stripping handled via internal/ansi (tested here indirectly
// via ansi.Strip; the pty_exec test that uses the PTY manager is separate). ---
// (Direct pty_exec strip behavior is covered by internal/ansi tests + a unit
// test on the shared stripper below.)

func TestAnsiStripBugsFromPtyInput(t *testing.T) {
	// Uses the shared internal/ansi stripper now wired into pty_exec.
	got := ansi.Strip("\x1b]0;hello\x07visible\x1b[31mred\x1b[0m")
	if got != "visiblered" {
		t.Errorf("strip = %q, want %q", got, "visiblered")
	}
}

// --- AP-04: bgexec Execute errors must be *internal.ToolError. ---

func TestBGExec_ErrorsAreToolErrors(t *testing.T) {
	tool := NewBGExecTool()
	cases := []struct {
		name  string
		input string
	}{
		{"status missing id", `{"action":"status"}`},
		{"read missing id", `{"action":"read"}`},
		{"write missing id", `{"action":"write"}`},
		{"stop missing id", `{"action":"stop"}`},
		{"status not found", `{"action":"status","id":"nope"}`},
		{"start missing command", `{"action":"start"}`},
		{"unknown action", `{"action":"bogus"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tool.Execute(tc.input)
			if err == nil {
				t.Fatalf("expected error for %s", tc.input)
			}
			var te *internal.ToolError
			if !errors.As(err, &te) {
				t.Errorf("expected *internal.ToolError, got %T: %v", err, err)
			}
		})
	}
}

// --- helpers ---

func parseProcID(t *testing.T, startOut string) string {
	t.Helper()
	// startOut: "[bg_exec: start] Process proc-1 started — PID 123\n..."
	i := strings.Index(startOut, "Process ")
	j := strings.Index(startOut, " started")
	if i < 0 || j < 0 {
		t.Fatalf("cannot parse proc id from %q", startOut)
	}
	return startOut[i+len("Process ") : j]
}

func getProc(t *testing.T, tool *BGExecTool, id string) *BGProcess {
	t.Helper()
	tool.mu.RLock()
	defer tool.mu.RUnlock()
	p, ok := tool.procs[id]
	if !ok {
		t.Fatalf("proc %q not found", id)
	}
	return p
}
