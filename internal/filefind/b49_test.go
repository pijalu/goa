// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package filefind

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// B49 regression tests: the @-completion search must be bounded so it
// cannot dive the whole tree on every keystroke:
//
//   - a shallow os.ReadDir listing of the directory the user is typing in
//     answers first; the fd deep search only rescues queries the shallow
//     pass cannot answer;
//   - when fd does run, its depth is capped and symlinks are not followed;
//   - a leading ~ expands to $HOME;
//   - file:// and percent-encoded paths (%20) decode before matching, with
//     the raw form retried so literal "%20" file names still complete.

// fakeFDLogging installs an fd stub that appends one line per argv element
// to a log file and prints output (baseDir-relative lines, real fd's
// contract). The log lets tests observe when and how the engine invokes
// fd. Returns the log path.
func fakeFDLogging(t *testing.T, output string) string {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh available for fd stub")
	}
	log := filepath.Join(t.TempDir(), "fd-argv.log")
	script := filepath.Join(t.TempDir(), "fd")
	body := "#!/bin/sh\n" +
		"for a in \"$@\"; do echo \"$a\" >> " + log + "; done\n"
	if output != "" {
		body += "cat <<'EOF'\n" + output + "\nEOF\n"
	}
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	oldAvail, oldPath := fdAvailable, fdPath
	fdAvailable, fdPath = true, script
	t.Cleanup(func() { fdAvailable, fdPath = oldAvail, oldPath })
	return log
}

// fdArgv returns the argv lines the stubbed fd recorded; nil when fd was
// never invoked.
func fdArgv(t *testing.T, log string) []string {
	t.Helper()
	data, err := os.ReadFile(log)
	if err != nil {
		return nil
	}
	var argv []string
	for _, line := range strings.Split(string(data), "\n") {
		if line != "" {
			argv = append(argv, line)
		}
	}
	return argv
}

// fdArgvHasPair reports whether argv contains flag immediately followed by
// value.
func fdArgvHasPair(argv []string, flag, value string) bool {
	for i, a := range argv {
		if a == flag && i+1 < len(argv) && argv[i+1] == value {
			return true
		}
	}
	return false
}

// fdArgvHas reports whether argv contains flag at all.
func fdArgvHas(argv []string, flag string) bool {
	for _, a := range argv {
		if a == flag {
			return true
		}
	}
	return false
}

// TestComplete_ShallowReadDirAnswersFirst: when the directory listing
// answers the query, fd must not be invoked at all — a per-keystroke deep
// search is what made @ completion burn CPU (B49).
func TestComplete_ShallowReadDirAnswersFirst(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "alpha.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	log := fakeFDLogging(t, "alpha.txt\n")

	got := New(root).Complete("alpha")
	if len(got) != 1 || got[0].Path != "alpha.txt" {
		t.Fatalf("got %#v, want [alpha.txt]", got)
	}
	if argv := fdArgv(t, log); argv != nil {
		t.Errorf("fd invoked for a shallow query the ReadDir pass answers: argv %v", argv)
	}
}

// TestComplete_FDRescueBounded: when the shallow pass cannot answer, fd
// rescues the query — but bounded: depth capped, no symlink following, the
// 50-result cap still in force.
func TestComplete_FDRescueBounded(t *testing.T) {
	root := t.TempDir()
	seedDeepB49Tree(t, root)
	log := fakeFDLogging(t, "a/b/c/target.txt\n")

	got := New(root).Complete("target")
	if len(got) != 1 || got[0].Path != "a/b/c/target.txt" {
		t.Fatalf("got %#v, want [a/b/c/target.txt]", got)
	}

	argv := fdArgv(t, log)
	if len(argv) == 0 {
		t.Fatal("fd not invoked for a query the shallow pass cannot answer")
	}
	// Depth formula: 3 + number of "/" in the query ("target" has none).
	if !fdArgvHasPair(argv, "--max-depth", "3") {
		t.Errorf("fd argv missing --max-depth 3: %v", argv)
	}
	if fdArgvHas(argv, "--follow") {
		t.Errorf("fd must not follow symlinks (bounded search): %v", argv)
	}
	if !fdArgvHasPair(argv, "--max-results", "50") {
		t.Errorf("fd argv missing --max-results 50: %v", argv)
	}
	checkFDBaseDirectory(t, argv, root)
}

// seedDeepB49Tree builds root/a/b/c/{target,other}.txt — a hit only fd's
// rescue pass can reach.
func seedDeepB49Tree(t *testing.T, root string) {
	t.Helper()
	deep := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"target.txt", "other.txt"} {
		if err := os.WriteFile(filepath.Join(deep, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// checkFDBaseDirectory asserts fd searched root (path may carry a trailing
// slash).
func checkFDBaseDirectory(t *testing.T, argv []string, root string) {
	t.Helper()
	for i, a := range argv {
		if a != "--base-directory" || i+1 >= len(argv) {
			continue
		}
		if clean := filepath.Clean(argv[i+1]); clean != filepath.Clean(root) {
			t.Errorf("fd base directory = %q, want %q", clean, filepath.Clean(root))
		}
	}
}

// TestComplete_TildeExpandsToHome: a leading ~ must resolve to $HOME, not
// to a literal "~" directory under the workdir.
func TestComplete_TildeExpandsToHome(t *testing.T) {
	workdir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	seedTildeTree(t, home)
	s := New(workdir).WithFD(false)

	cases := []struct {
		name   string
		prefix string
		want   []string
	}{
		{"bare ~ lists home", "~", []string{home + "/plans/", home + "/todo.txt"}},
		{"tilde slash lists home", "~/", []string{home + "/plans/", home + "/todo.txt"}},
		{"tilde dir drills down", "~/plans/", []string{home + "/plans/plan.md"}},
		{"tilde partial filters", "~/plan", []string{home + "/plans/"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotPaths := paths(s.Complete(tc.prefix))
			if !reflect.DeepEqual(gotPaths, tc.want) {
				t.Errorf("Complete(%q) = %v, want %v", tc.prefix, gotPaths, tc.want)
			}
		})
	}

	t.Run("no HOME yields nothing without panicking", func(t *testing.T) {
		t.Setenv("HOME", "")
		if got := s.Complete("~/x"); len(got) != 0 {
			t.Errorf("Complete(\"~/x\") with empty HOME = %#v, want none", got)
		}
	})
}

// seedTildeTree fills dir with plans/plan.md and todo.txt.
func seedTildeTree(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "plans"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"plans/plan.md", "todo.txt"} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestComplete_EncodedPathDecodes: file:// URLs and percent-escapes decode
// before matching; the raw form is retried so literal "%xx" names survive.
func TestComplete_EncodedPathDecodes(t *testing.T) {
	workdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workdir, "f g.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(workdir).WithFD(false)
	abs := filepath.ToSlash(workdir)

	literal := t.TempDir()
	if err := os.WriteFile(filepath.Join(literal, "f%20g.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	sLiteral := New(literal).WithFD(false)

	invalid := t.TempDir()
	if err := os.WriteFile(filepath.Join(invalid, "f%zz.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	sInvalid := New(invalid).WithFD(false)

	cases := []struct {
		name  string
		s     *Search
		query string
		want  []string
	}{
		{"percent-encoded space", s, "f%20g", []string{"f g.md"}},
		{"file URL", s, "file://" + abs + "/f%20g", []string{"f g.md"}},
		{"file URL localhost", s, "file://localhost" + abs + "/f%20g", []string{"f g.md"}},
		{"encoded exact file suppresses popup", s, "file://" + abs + "/f%20g.md", nil},
		{"literal percent name still matches", sLiteral, "f%20g", []string{"f%20g.md"}},
		{"invalid escape falls back to raw", sInvalid, "f%zz", []string{"f%zz.md"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotPaths := paths(tc.s.Complete(tc.query))
			if tc.want == nil {
				if len(gotPaths) != 0 {
					t.Errorf("Complete(%q) = %v, want none", tc.query, gotPaths)
				}
				return
			}
			if !reflect.DeepEqual(gotPaths, tc.want) {
				t.Errorf("Complete(%q) = %v, want %v", tc.query, gotPaths, tc.want)
			}
		})
	}
}
