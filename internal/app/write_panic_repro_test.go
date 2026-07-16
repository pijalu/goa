// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"encoding/json"
	"os"
	"runtime/debug"
	"testing"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/lsp"
	"github.com/pijalu/goa/tools"
)

// TestWritePanicRepro probes the WriteFileTool for the nil-pointer panic seen
// in production (2026-07-16, writing internal/app/*.go). Each sub-case runs
// with a recover that prints the full stack so the failing line is visible.
func TestWritePanicRepro(t *testing.T) {
	projectDir := "/Users/muaddib/dev/goa"
	content, _ := os.ReadFile(projectDir + "/internal/app/write_stream_lag_repro_test.go")
	goPayload := func(path string) string {
		in, _ := json.Marshal(map[string]string{"path": path, "content": string(content)})
		return string(in)
	}
	run := func(name string, tool *tools.WriteFileTool, input string) {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("PANIC: %v\n%s", r, debug.Stack())
				}
			}()
			out, err := tool.Execute(input)
			t.Logf("err=%v out=%.120q", err, out)
		})
	}

	// A: typed-nil *lsp.Manager in the interface field (guard passes, nil deref).
	var typedNil *lsp.Manager
	run("typed_nil_lsp", &tools.WriteFileTool{LSPManager: typedNil}, goPayload("/tmp/zz_a.go"))

	// B: nil interface entirely (no LSP).
	run("nil_lsp", &tools.WriteFileTool{}, goPayload("/tmp/zz_b.go"))

	// C: live LSP, real content, .go file in the repo.
	live := newLSPManager(projectDir)
	t.Logf("newLSPManager: %v (nil=%v)", live, live == nil)
	wm := internal.NewWorktreeManager(projectDir, internal.WorktreeMode(""))
	run("live_lsp_go", &tools.WriteFileTool{
		WorktreeMgr: wm, ProjectDir: projectDir,
		GitStager: tools.NewGitStager(projectDir), LSPManager: live,
	}, goPayload(t.TempDir()+"/zz_c_test.go"))

	// D: live LSP, non-Go file.
	run("live_lsp_non_go", &tools.WriteFileTool{
		WorktreeMgr: wm, ProjectDir: projectDir, LSPManager: live,
	}, goPayload("/tmp/zz_d.txt"))

	// E: no worktree manager (nil) — ResolveFileToolPath with nil receiver.
	run("nil_worktree", &tools.WriteFileTool{LSPManager: live}, goPayload("/tmp/zz_e.go"))
}
