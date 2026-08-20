// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pijalu/goa/tools/common"
)

func newTestSpillPolicy(t *testing.T, limit int) (*SpillPolicy, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "spill", "sess-1")
	return &SpillPolicy{MaxInlineBytes: limit, Store: common.NewSpillStore(dir)}, dir
}

func TestSpillPolicy_OverCapResultSpills(t *testing.T) {
	p, dir := newTestSpillPolicy(t, 512)
	original := strings.Repeat("a", 400) + strings.Repeat("b", 400)

	got := p.ApplySpill("bash", original)
	if got == original {
		t.Fatal("over-cap result should be replaced by a preview + notice")
	}
	if len(got) > 512 {
		t.Errorf("model-facing content must never exceed the cap: len=%d > 512", len(got))
	}
	if !strings.Contains(got, "(Omitted ") {
		t.Errorf("replacement should carry the omission notice, got: %q", got)
	}
	if !strings.Contains(got, "Full result stored at: ") {
		t.Errorf("replacement should carry the spill locator, got: %q", got)
	}
	if !strings.Contains(got, "Use read with offset/limit") {
		t.Errorf("replacement should carry the retrieval hint, got: %q", got)
	}
	// Head and tail of the original survive in the preview.
	if !strings.HasPrefix(got, strings.Repeat("a", 50)) {
		t.Error("preview should keep the head of the result")
	}
	if !strings.Contains(got, strings.Repeat("b", 50)) {
		t.Error("preview should keep the tail of the result")
	}

	// The full text is spilled verbatim under the session dir.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("session spill dir should exist: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly one spill file, got %d", len(entries))
	}
	spilled, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatalf("spill file should be readable: %v", err)
	}
	if string(spilled) != original {
		t.Error("spill file must hold the original result verbatim")
	}
	if !strings.Contains(got, entries[0].Name()) {
		t.Errorf("notice should reference the spill file %q", entries[0].Name())
	}
}

func TestSpillPolicy_OmittedCountIsExact(t *testing.T) {
	p, dir := newTestSpillPolicy(t, 512)
	original := strings.Repeat("x", 1000)
	got := p.ApplySpill("bash", original)

	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected one spill file: entries=%d err=%v", len(entries), err)
	}
	path := filepath.Join(dir, entries[0].Name())
	notice := fmt.Sprintf("(Omitted %d bytes. Full result stored at: %s. Use read with offset/limit, or grep this path to search within it.)",
		len(original)-len(previewPortion(got, path)), path)
	if !strings.Contains(got, notice) {
		t.Errorf("notice mismatch.\nwant substring: %q\ngot: %q", notice, got)
	}
	// Replacement = preview + "\n\n" + notice, all within cap.
	if len(got) > 512 {
		t.Errorf("replacement over cap: %d", len(got))
	}
}

// previewPortion strips the trailing blank line + notice to recover the preview.
func previewPortion(replaced, path string) string {
	_ = path
	idx := strings.LastIndex(replaced, "\n\n(Omitted ")
	if idx < 0 {
		// Notice-only replacement.
		return ""
	}
	return replaced[:idx]
}


func TestSpillPolicy_UnderCapUnchanged(t *testing.T) {
	p, dir := newTestSpillPolicy(t, 512)
	result := "small result"
	if got := p.ApplySpill("bash", result); got != result {
		t.Errorf("under-cap result must pass through unchanged, got %q", got)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("no spill file should be written for an under-cap result")
	}
}

func TestSpillPolicy_ReadToolNeverSpilled(t *testing.T) {
	p, dir := newTestSpillPolicy(t, 64)
	result := strings.Repeat("r", 1000)
	if got := p.ApplySpill("read", result); got != result {
		t.Error("read results must never be spilled (read → spill → read-again loop)")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("no spill file should be written for read results")
	}
}

func TestSpillPolicy_SaveFailureKeepsOriginal(t *testing.T) {
	// A store with an empty dir fails every Save; the policy is best-effort.
	p := &SpillPolicy{MaxInlineBytes: 64, Store: common.NewSpillStore("")}
	result := strings.Repeat("z", 1000)
	if got := p.ApplySpill("bash", result); got != result {
		t.Error("storage failure must leave the original result inline")
	}
}

func TestSpillPolicy_NilStoreKeepsOriginal(t *testing.T) {
	p := &SpillPolicy{MaxInlineBytes: 64}
	result := strings.Repeat("z", 1000)
	if got := p.ApplySpill("bash", result); got != result {
		t.Error("without a store backend the original result stays inline")
	}
}

func TestSpillPolicy_DisabledWithoutCap(t *testing.T) {
	p, _ := newTestSpillPolicy(t, 0)
	result := strings.Repeat("z", 1000)
	if got := p.ApplySpill("bash", result); got != result {
		t.Error("max_inline_bytes=0 disables the policy entirely")
	}
}

func TestSpillPolicy_TinyCapKeepsOriginal(t *testing.T) {
	// A cap too small for the notice itself: the policy never emits a
	// replacement over the cap, so the oversized original stays inline.
	p, _ := newTestSpillPolicy(t, 10)
	result := strings.Repeat("z", 1000)
	if got := p.ApplySpill("bash", result); got != result {
		t.Error("when even the notice exceeds the cap, keep the original inline")
	}
}

func TestSpillPolicy_PreviewKeepsUTF8Boundaries(t *testing.T) {
	p, _ := newTestSpillPolicy(t, 256)
	// Multi-byte runes at the cut points must survive intact.
	original := strings.Repeat("héllo-", 100) // 7 bytes per repeat
	got := p.ApplySpill("bash", original)
	if strings.ContainsRune(got, '\uFFFD') {
		t.Errorf("preview must not split UTF-8 runes: %q", got[:min(64, len(got))])
	}
	if len(got) > 256 {
		t.Errorf("replacement over cap: %d", len(got))
	}
}
