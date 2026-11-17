// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditFileTool_LargeBatchOutputIsBoundedAndByteExact(t *testing.T) {
	dir := t.TempDir()
	path := abs(dir, "large.go")
	var original strings.Builder
	for i := 1; i <= 600; i++ {
		fmt.Fprintf(&original, "line %03d\n", i)
	}
	if err := os.WriteFile(path, []byte(original.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	edits := make([]map[string]any, 0, 12)
	expected := original.String()
	for i := 1; i <= 12; i++ {
		line := i * 45
		oldText := fmt.Sprintf("line %03d", line)
		newText := fmt.Sprintf("changed %03d", line)
		edits = append(edits, map[string]any{"old_string": oldText, "new_string": newText})
		expected = strings.Replace(expected, oldText, newText, 1)
	}
	input, err := json.Marshal(map[string]any{"path": path, "edits": edits})
	if err != nil {
		t.Fatal(err)
	}

	out, err := (&EditFileTool{ProjectDir: dir}).Execute(string(input))
	if err != nil {
		t.Fatalf("large batch failed: %v", err)
	}
	if got := readTestFile(t, path); got != expected {
		t.Fatal("large batch did not preserve the exact expected bytes")
	}
	if strings.Count(out, "@@ ") != len(edits) {
		t.Errorf("got %d diff hunks, want %d separated changed regions", strings.Count(out, "@@ "), len(edits))
	}
	if len(out) > 8000 {
		t.Errorf("large-file result is not bounded by changed lines: %d bytes", len(out))
	}
	if strings.Contains(out, " line 300\n") {
		t.Errorf("unchanged middle of the file leaked into the result")
	}
}

func TestGenerateUnifiedDiff_SeparatesDistantChanges(t *testing.T) {
	oldLines := make([]string, 600)
	newLines := make([]string, 600)
	for i := range oldLines {
		oldLines[i] = fmt.Sprintf("line %03d", i+1)
		newLines[i] = oldLines[i]
	}
	for _, i := range []int{49, 299, 549} {
		newLines[i] = "changed " + oldLines[i]
	}
	diff := generateUnifiedDiff(oldLines, newLines)
	if got := strings.Count(diff, "@@ "); got != 3 {
		t.Fatalf("got %d hunks, want 3:\n%s", got, diff)
	}
	if strings.Contains(diff, " line 100\n") || len(diff) > len(strings.Join(oldLines, "\n"))/4 {
		t.Errorf("diff includes unchanged content between distant edits: %d bytes", len(diff))
	}
}

func TestEditFileTool_StringDelete_ComposesWithContentEdits(t *testing.T) {
	dir := t.TempDir()
	path := abs(dir, "delete.txt")
	writeTestFiles(t, dir, map[string]string{"delete.txt": "keep-a\ndrop\nkeep-b\ntail\n"})
	input := fmt.Sprintf(`{"path":%q,"edits":[`+
		`{"operation":"delete","old_string":"drop"},`+
		`{"old_string":"keep-b","new_string":"keep-b changed"}]}`, path)
	out, err := (&EditFileTool{ProjectDir: dir}).Execute(input)
	if err != nil {
		t.Fatalf("string-keyed delete failed: %v", err)
	}
	if got, want := readTestFile(t, path), "keep-a\nkeep-b changed\ntail\n"; got != want {
		t.Errorf("content = %q, want %q", got, want)
	}
	if !strings.Contains(out, "2 edits applied") {
		t.Errorf("missing batch summary: %s", out)
	}
}

func TestEditFileTool_StringOccurrenceAndReplaceAll(t *testing.T) {
	cases := []struct {
		name     string
		op       string
		newValue string
		options  string
		want     string
	}{
		{name: "replace second", newValue: "y", options: `,"occurrence":2`, want: "a\nx\nb\ny\nc\nx\n"},
		{name: "replace all", newValue: "y", options: `,"replace_all":true`, want: "a\ny\nb\ny\nc\ny\n"},
		{name: "delete second", op: "delete", options: `,"occurrence":2`, want: "a\nx\nb\nc\nx\n"},
		{name: "delete all", op: "delete", options: `,"replace_all":true`, want: "a\nb\nc\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := abs(dir, "matches.txt")
			writeTestFiles(t, dir, map[string]string{"matches.txt": "a\nx\nb\nx\nc\nx\n"})
			op := tc.op
			if op == "" {
				op = "replace"
			}
			newField := ""
			if tc.op != "delete" {
				newField = fmt.Sprintf(`,"new_string":%q`, tc.newValue)
			}
			input := fmt.Sprintf(`{"path":%q,"operation":%q,"old_string":"x"%s%s}`, path, op, newField, tc.options)
			if _, err := (&EditFileTool{ProjectDir: dir}).Execute(input); err != nil {
				t.Fatalf("edit failed: %v", err)
			}
			if got := readTestFile(t, path); got != tc.want {
				t.Errorf("content = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEditFileTool_AmbiguousMatchReportsCountAndControls(t *testing.T) {
	dir := t.TempDir()
	path := abs(dir, "ambiguous.txt")
	writeTestFiles(t, dir, map[string]string{"ambiguous.txt": "x\nx\nx\n"})
	_, err := (&EditFileTool{ProjectDir: dir}).Execute(fmt.Sprintf(
		`{"path":%q,"old_string":"x","new_string":"y"}`, path))
	if err == nil {
		t.Fatal("ambiguous edit without selection must fail")
	}
	for _, want := range []string{"ambiguous_match", "3", "occurrence", "replace_all"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
	if got := readTestFile(t, path); got != "x\nx\nx\n" {
		t.Errorf("ambiguous edit changed the file: %q", got)
	}
}

func TestEditFileTool_ReplaceAllUsesNonOverlappingMatches(t *testing.T) {
	dir := t.TempDir()
	path := abs(dir, "overlap.txt")
	writeTestFiles(t, dir, map[string]string{"overlap.txt": "aaaa\n"})
	if _, err := (&EditFileTool{ProjectDir: dir}).Execute(fmt.Sprintf(
		`{"path":%q,"old_string":"aa","new_string":"b","replace_all":true}`, path)); err != nil {
		t.Fatal(err)
	}
	if got := readTestFile(t, path); got != "bb\n" {
		t.Errorf("non-overlapping replacement = %q, want %q", got, "bb\\n")
	}
	writeTestFiles(t, dir, map[string]string{"overlap.txt": "aaa\n"})
	if _, err := (&EditFileTool{ProjectDir: dir}).Execute(fmt.Sprintf(
		`{"path":%q,"old_string":"aa","new_string":"b","replace_all":true}`, path)); err != nil {
		t.Fatal(err)
	}
	if got := readTestFile(t, path); got != "ba\n" {
		t.Errorf("overlapping candidate must not be reapplied: %q", got)
	}
}

func TestEditFileTool_RejectsInvalidSelectionAndSingleFileAtomic(t *testing.T) {
	dir := t.TempDir()
	path := abs(dir, "guard.txt")
	writeTestFiles(t, dir, map[string]string{"guard.txt": "old\n"})
	cases := []string{
		fmt.Sprintf(`{"path":%q,"old_string":"old","new_string":"new","occurrence":1,"replace_all":true}`, path),
		fmt.Sprintf(`{"path":%q,"old_string":"old","new_string":"new","atomic":false}`, path),
		fmt.Sprintf(`{"path":%q,"old_string":"old","new_string":"new","atomic":null}`, path),
		fmt.Sprintf(`{"path":%q,"edits":[{"old_string":"old","new_string":"new","atomic":true}]}`, path),
		fmt.Sprintf(`{"path":%q,"operation":"delete","old_string":"old","new_string":"unexpected"}`, path),
	}
	for _, input := range cases {
		if _, err := (&EditFileTool{ProjectDir: dir}).Execute(input); err == nil {
			t.Errorf("invalid input succeeded: %s", input)
		}
		if got := readTestFile(t, path); got != "old\n" {
			t.Fatalf("invalid input changed file: %q", got)
		}
	}
}

func TestFuzzyEditEmptyReplacementStillWritesOneEmptyLine(t *testing.T) {
	result, err := fuzzyEdit("before\npattern\nafter\n", "pattern", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := result.NewContent, "before\n\nafter\n"; got != want {
		t.Errorf("empty replacement content = %q, want %q", got, want)
	}
}

func TestEditFileTool_MultiFileDefaultContinuesAfterFailures(t *testing.T) {
	dir := t.TempDir()
	writeTestFiles(t, dir, map[string]string{"a.txt": "old-a\n", "b.txt": "other\n", "c.txt": "old-c\n"})
	input := multiFileReplaceInput("old", "new", false,
		abs(dir, "a.txt"), abs(dir, "b.txt"), abs(dir, "c.txt"))
	out, err := (&EditFileTool{ProjectDir: dir}).Execute(input)
	if err == nil {
		t.Fatal("mixed batch must return an overall error")
	}
	for _, name := range []string{"a.txt", "c.txt"} {
		if got := readTestFile(t, abs(dir, name)); !strings.Contains(got, "new") {
			t.Errorf("%s was not independently written: %q", name, got)
		}
		if !strings.Contains(err.Error(), "ok   "+abs(dir, name)) {
			t.Errorf("overall error lacks successful per-file outcome for %s: %v", name, err)
		}
	}
	if !strings.Contains(err.Error(), "fail "+abs(dir, "b.txt")) {
		t.Errorf("overall error lacks failed per-file outcome: %v", err)
	}
	if out == "" {
		t.Error("successful file results should remain available with the aggregate error")
	}
}

func TestEditFileTool_MultiFileAtomicValidationFailureWritesNothing(t *testing.T) {
	dir := t.TempDir()
	writeTestFiles(t, dir, map[string]string{"a.txt": "old-a\n", "b.txt": "other\n"})
	input := multiFileReplaceInput("old", "new", true, abs(dir, "a.txt"), abs(dir, "b.txt"))
	_, err := (&EditFileTool{ProjectDir: dir}).Execute(input)
	if err == nil {
		t.Fatal("atomic mixed batch must fail")
	}
	for _, name := range []string{"a.txt", "b.txt"} {
		if got := readTestFile(t, abs(dir, name)); strings.Contains(got, "new") {
			t.Errorf("atomic validation failure wrote %s: %q", name, got)
		}
	}
}

func TestEditFileTool_MultiFileAtomicWriteFailureRollsBack(t *testing.T) {
	dir := t.TempDir()
	writeTestFiles(t, dir, map[string]string{"a.txt": "old-a\n", "b.txt": "old-b\n"})
	tool := &EditFileTool{ProjectDir: dir}
	tool.writeFile = failSelectedWrite(abs(dir, "b.txt"), "old-b\n")
	input := multiFileReplaceInput("old", "new", true, abs(dir, "a.txt"), abs(dir, "b.txt"))
	_, err := tool.Execute(input)
	if err == nil || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("atomic write failure must report rollback, got: %v", err)
	}
	if got := readTestFile(t, abs(dir, "a.txt")); got != "old-a\n" {
		t.Errorf("first file was not rolled back: %q", got)
	}
	if got := readTestFile(t, abs(dir, "b.txt")); got != "old-b\n" {
		t.Errorf("failed file changed: %q", got)
	}
}

func TestEditFileTool_MultiFileDefaultWriteFailureContinues(t *testing.T) {
	dir := t.TempDir()
	writeTestFiles(t, dir, map[string]string{"a.txt": "old-a\n", "b.txt": "old-b\n", "c.txt": "old-c\n"})
	tool := &EditFileTool{ProjectDir: dir}
	tool.writeFile = failSelectedWrite(abs(dir, "b.txt"), "old-b\n")
	input := multiFileReplaceInput("old", "new", false,
		abs(dir, "a.txt"), abs(dir, "b.txt"), abs(dir, "c.txt"))
	_, err := tool.Execute(input)
	if err == nil {
		t.Fatal("write failure must be returned")
	}
	for _, name := range []string{"a.txt", "c.txt"} {
		if got := readTestFile(t, abs(dir, name)); !strings.Contains(got, "new") {
			t.Errorf("%s was not written around another file's write failure: %q", name, got)
		}
	}
}

func TestEditFileTool_MultiFileAtomicRestoresFailedWriteBytes(t *testing.T) {
	dir := t.TempDir()
	writeTestFiles(t, dir, map[string]string{"a.txt": "old-a\r\nkeep\r\n", "b.txt": "old-b\r\n"})
	tool := &EditFileTool{ProjectDir: dir}
	tool.writeFile = func(path string, data []byte, mode os.FileMode) error {
		if path == abs(dir, "b.txt") && string(data) != "old-b\r\n" {
			if err := WriteFileVerified(path, data, mode); err != nil {
				return err
			}
			return errors.New("forced failure after bytes landed")
		}
		return WriteFileVerified(path, data, mode)
	}
	input := multiFileReplaceInput("old", "new", true, abs(dir, "a.txt"), abs(dir, "b.txt"))
	_, err := tool.Execute(input)
	if err == nil {
		t.Fatal("post-write failure must fail the atomic batch")
	}
	if got := readTestFile(t, abs(dir, "a.txt")); got != "old-a\r\nkeep\r\n" {
		t.Errorf("CRLF/original bytes were not restored: %q", got)
	}
	if got := readTestFile(t, abs(dir, "b.txt")); got != "old-b\r\n" {
		t.Errorf("failing target was not restored after its bytes landed: %q", got)
	}
}

func TestEditFileTool_MultiFileAtomicReportsRollbackFailure(t *testing.T) {
	dir := t.TempDir()
	writeTestFiles(t, dir, map[string]string{"a.txt": "old-a\n", "b.txt": "old-b\n"})
	tool := &EditFileTool{ProjectDir: dir}
	tool.writeFile = func(path string, data []byte, mode os.FileMode) error {
		switch {
		case path == abs(dir, "b.txt") && string(data) != "old-b\n":
			return errors.New("forced write failure")
		case path == abs(dir, "a.txt") && string(data) == "old-a\n":
			return errors.New("forced rollback failure")
		default:
			return WriteFileVerified(path, data, mode)
		}
	}
	input := multiFileReplaceInput("old", "new", true, abs(dir, "a.txt"), abs(dir, "b.txt"))
	_, err := tool.Execute(input)
	if toolErrOf(err) == nil || toolErrOf(err).Type != "rollback_failed" {
		t.Fatalf("rollback failure must be explicit, got: %v", err)
	}
	if !strings.Contains(err.Error(), "fail "+abs(dir, "a.txt")) || !strings.Contains(err.Error(), "forced rollback failure") {
		t.Errorf("rollback failure lacks its exact per-file outcome: %v", err)
	}
	if got := readTestFile(t, abs(dir, "a.txt")); got != "new-a\n" {
		t.Errorf("test expected the injected failed rollback to leave new bytes, got %q", got)
	}
	if got := readTestFile(t, abs(dir, "b.txt")); got != "old-b\n" {
		t.Errorf("successfully restored target changed: %q", got)
	}
}

func TestEditFileTool_MultiFileDefaultMalformedEntryDoesNotBlockSiblings(t *testing.T) {
	dir := t.TempDir()
	writeTestFiles(t, dir, map[string]string{"a.txt": "old\n", "b.txt": "old\n", "c.txt": "old\n"})
	input := map[string]any{"files": []map[string]any{
		{"path": abs(dir, "a.txt"), "edits": []map[string]any{{"old_string": "old", "new_string": "new"}}},
		{"path": abs(dir, "b.txt")},
		{"path": abs(dir, "c.txt"), "edits": []map[string]any{{"old_string": "old", "new_string": "new"}}},
	}}
	data, marshalErr := json.Marshal(input)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	_, err := (&EditFileTool{ProjectDir: dir}).Execute(string(data))
	if err == nil || !strings.Contains(err.Error(), "missing 'edits'") {
		t.Fatalf("malformed entry must be reported per file: %v", err)
	}
	for _, name := range []string{"a.txt", "c.txt"} {
		if got := readTestFile(t, abs(dir, name)); got != "new\n" {
			t.Errorf("valid sibling %s was blocked: %q", name, got)
		}
	}
	if got := readTestFile(t, abs(dir, "b.txt")); got != "old\n" {
		t.Errorf("malformed target changed: %q", got)
	}
}

func TestEditFileTool_StringOccurrenceOutOfRangeReportsCount(t *testing.T) {
	dir := t.TempDir()
	path := abs(dir, "occurrence.txt")
	writeTestFiles(t, dir, map[string]string{"occurrence.txt": "x\nx\n"})
	_, err := (&EditFileTool{ProjectDir: dir}).Execute(fmt.Sprintf(
		`{"path":%q,"old_string":"x","new_string":"y","occurrence":3}`, path))
	if toolErrOf(err) == nil || toolErrOf(err).Type != "occurrence_not_found" {
		t.Fatalf("want occurrence_not_found, got %v", err)
	}
	if !strings.Contains(err.Error(), "2") || !strings.Contains(err.Error(), "3") {
		t.Errorf("error lacks available/requested counts: %v", err)
	}
	if got := readTestFile(t, path); got != "x\nx\n" {
		t.Errorf("failed occurrence changed file: %q", got)
	}
}

func TestEditFileTool_MultiFileGlobAtomicFailureAndSuccess(t *testing.T) {
	dir := t.TempDir()
	writeTestFiles(t, dir, map[string]string{"pkg/a.txt": "old\n", "pkg/b.txt": "other\n"})
	tool := &EditFileTool{ProjectDir: dir}
	input := `{"atomic":true,"glob":"pkg/*.txt","edits":[{"old_string":"old","new_string":"new"}]}`
	if _, err := tool.Execute(input); err == nil {
		t.Fatal("atomic glob with one invalid target must fail")
	}
	for _, name := range []string{"pkg/a.txt", "pkg/b.txt"} {
		if got := readTestFile(t, abs(dir, name)); strings.Contains(got, "new") {
			t.Errorf("atomic glob failure wrote %s: %q", name, got)
		}
	}
	writeTestFiles(t, dir, map[string]string{"pkg/b.txt": "old\n"})
	if _, err := tool.Execute(input); err != nil {
		t.Fatalf("valid atomic glob must succeed: %v", err)
	}
	for _, name := range []string{"pkg/a.txt", "pkg/b.txt"} {
		if got := readTestFile(t, abs(dir, name)); got != "new\n" {
			t.Errorf("atomic glob success did not write %s: %q", name, got)
		}
	}
}

func multiFileReplaceInput(oldText, newText string, atomic bool, paths ...string) string {
	files := make([]map[string]any, 0, len(paths))
	for _, path := range paths {
		files = append(files, map[string]any{
			"path":  path,
			"edits": []map[string]any{{"old_string": oldText, "new_string": newText}},
		})
	}
	input := map[string]any{"files": files}
	if atomic {
		input["atomic"] = true
	}
	data, err := json.Marshal(input)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func failSelectedWrite(failPath, original string) func(string, []byte, os.FileMode) error {
	failed := false
	return func(path string, data []byte, mode os.FileMode) error {
		if path == failPath && !failed && string(data) != original {
			failed = true
			return errors.New("forced write failure")
		}
		return WriteFileVerified(path, data, mode)
	}
}

func TestEditFileTool_MultiFileGlobDefaultReportsPartialOutcome(t *testing.T) {
	dir := t.TempDir()
	writeTestFiles(t, dir, map[string]string{"pkg/a.txt": "old\n", "pkg/b.txt": "other\n"})
	tool := &EditFileTool{ProjectDir: dir}
	_, err := tool.Execute(`{"glob":"pkg/*.txt","edits":[{"old_string":"old","new_string":"new"}]}`)
	if err == nil {
		t.Fatal("partially matching glob must return an error")
	}
	if got := readTestFile(t, filepath.Join(dir, "pkg/a.txt")); got != "new\n" {
		t.Errorf("valid glob target not written: %q", got)
	}
	if !strings.Contains(err.Error(), "ok") || !strings.Contains(err.Error(), "fail") {
		t.Errorf("glob error lacks per-file outcomes: %v", err)
	}
}
