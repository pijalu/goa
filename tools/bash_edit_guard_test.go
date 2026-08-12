// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDetectShellFileEdit covers the bash→edit guardrail detector:
// commands that modify project files must be caught; read-only commands must pass.
func TestDetectShellFileEdit(t *testing.T) {
	cases := []struct {
		name    string
		cmd     string
		wantHit bool
	}{
		// --- should be caught (file modifications) ---
		{"node writeFileSync", `node -e 'const fs=require("fs");let h=fs.readFileSync("/p/f.html","utf8");fs.writeFileSync("/p/f.html",h.replace("a","b"))'`, true},
		{"python open write", `python3 -c "open('/p/f.txt','w').write('x')"`, true},
		{"python Path.write_text", `python3 -c "from pathlib import Path; Path('/p/f.txt').write_text('x')"`, true},
		{"redirect write", `echo hello > /p/f.txt`, true},
		{"redirect append", `echo hello >> /p/f.txt`, true},
		{"heredoc into file", "cat > /p/f.js << 'EOF'\ncode\nEOF", true},
		{"tee", `echo x | tee /p/f.txt`, true},
		{"sed -i", `sed -i 's/a/b/' /p/f.txt`, true},
		{"perl -pi", `perl -pi -e 's/a/b/' /p/f.txt`, true},

		// --- should be allowed (read-only / scratch) ---
		{"node read-only", `node -e "const fs=require('fs');console.log(fs.readFileSync('/p/f.html','utf8').length)"`, false},
		{"grep", `grep -n foo /p/f.txt`, false},
		{"cat", `cat /p/f.txt`, false},
		{"redirect to /dev/null", `make build > /dev/null 2>&1`, false},
		{"stderr dup", `cmd 2>&1 | less`, false},
		{"redirect to /tmp scratch", "cat > /tmp/scratch.js << 'EOF'\nx\nEOF", false},
		{"wc", `wc -l /p/f.txt`, false},
		{"awk print", `awk '{print $1}' /p/f.txt`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := detectShellFileEdit(c.cmd)
			if c.wantHit {
				assert.NotEmpty(t, got, "expected detection for: %s", c.cmd)
			} else {
				assert.Empty(t, got, "unexpected detection (%q) for: %s", got, c.cmd)
			}
		})
	}
}

// TestBashTool_WarnFileEdits verifies the file-edit nudge is NON-BLOCKING
// (never block, only hint): the command still runs and its output is
// produced, with a hint prepended when the command edits a file via the shell.
func TestBashTool_WarnFileEdits(t *testing.T) {
	readCmd := `{"command": "echo hello"}`

	t.Run("hint prepended but command runs", func(t *testing.T) {
		dir := t.TempDir()
		// redirect-write inside a temp dir: detected as a file edit, still executes.
		cmd := `{"command": "echo hi > guard_scratch.txt && cat guard_scratch.txt", "workdir": "` + dir + `"}`
		tool := &BashTool{WarnFileEdits: true}
		out, err := tool.Execute(cmd)
		require.NoError(t, err, "must never block file-edit commands")
		assert.Contains(t, out, "Prefer the 'edit' tool", "expected non-blocking hint, got: %s", out)
		assert.Contains(t, out, "hi", "command output must still be produced")
	})

	t.Run("no hint for read-only", func(t *testing.T) {
		tool := &BashTool{WarnFileEdits: true}
		out, err := tool.Execute(readCmd)
		require.NoError(t, err)
		assert.NotContains(t, out, "Prefer the 'edit' tool")
		assert.Contains(t, out, "hello")
	})

	t.Run("hint disabled", func(t *testing.T) {
		dir := t.TempDir()
		cmd := `{"command": "echo hi > guard_scratch.txt && cat guard_scratch.txt", "workdir": "` + dir + `"}`
		tool := &BashTool{WarnFileEdits: false}
		out, err := tool.Execute(cmd)
		require.NoError(t, err)
		assert.NotContains(t, out, "Prefer the 'edit' tool")
	})

	t.Run("resolver overrides static field", func(t *testing.T) {
		dir := t.TempDir()
		cmd := `{"command": "echo hi > guard_scratch.txt && cat guard_scratch.txt", "workdir": "` + dir + `"}`
		tool := &BashTool{WarnFileEdits: true, WarnFileEditsResolver: func() bool { return false }}
		out, err := tool.Execute(cmd)
		require.NoError(t, err)
		assert.NotContains(t, out, "Prefer the 'edit' tool", "resolver=false must silence the hint")
	})
}

// TestEditNotFound_LineMatchDiagnostic verifies the not-found error now reports
// how many old_string lines matched and steers toward smaller anchored edits
// (items 2+3), so the model doesn't assume the tool is broken and use bash.
func TestEditNotFound_LineMatchDiagnostic(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "f.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("alpha\nbeta\ngamma\ndelta\n"), 0644))

	tool := &EditFileTool{AllowFuzz: true}
	// old_string partially matches (alpha/beta present, ZZZ/YYY absent).
	input := `{"path": "` + filePath + `", "old_string": "alpha\nbeta\nZZZ\nYYY\n", "new_string": "alpha\nbeta\ngamma2\ndelta2\n"}`
	_, err := tool.Execute(input)
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, "not_found")
	// item 3: line-match count surfaced (2 of the 4 non-empty lines match).
	assert.Contains(t, msg, "2/4", "expected line-match diagnostic, got: %s", msg)
	assert.Contains(t, msg, "lines of old_string matched")
	// item 2: recovery guidance mentions replace_lines, re-read, and not using bash.
	assert.Contains(t, msg, "replace_lines")
	assert.Contains(t, msg, "read")
	assert.True(t, strings.Contains(msg, "bash"), "expected hint to discourage bash, got: %s", msg)
}
