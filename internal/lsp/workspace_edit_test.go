package lsp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceEditDocumentChangesAndMultiline(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "unicode.txt")
	if err := osWrite(p, "one\n世界\nthree\n"); err != nil {
		t.Fatal(err)
	}
	e := &WorkspaceEdit{DocumentChanges: []TextDocumentEdit{{TextDocument: VersionedTextDocumentIdentifier{URI: uriFor(p)}, Edits: []TextEdit{{Range: Range{Start: Position{Line: 0, Character: 0}, End: Position{Line: 2, Character: 5}}, NewText: "replaced"}}}}}
	preview, err := ApplyWorkspaceEdit(e, WorkspaceEditPolicy{Root: d})
	if err != nil {
		t.Fatal(err)
	}
	if preview.EditCount != 1 {
		t.Fatalf("preview: %+v", preview)
	}
	b, _ := osRead(p)
	if string(b) != "replaced\n" {
		t.Fatalf("got %q", b)
	}
	if _, err := os.Stat(filepath.Join(d, ".goa", "backups", "unicode.txt.bak")); err != nil {
		t.Fatalf("backup: %v", err)
	}
}

func TestWorkspaceEditProtectedPath(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, ".goa", "config.yaml")
	_ = os.MkdirAll(filepath.Dir(p), 0700)
	_ = osWrite(p, "old")
	e := &WorkspaceEdit{Changes: map[string][]TextEdit{uriFor(p): {{NewText: "new"}}}}
	if _, err := PreviewWorkspaceEdit(e, WorkspaceEditPolicy{Root: d}); err == nil {
		t.Fatal("expected protected path error")
	}
}

func TestWorkspaceEditPreviewAndApply(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "a.go")
	if err := osWrite(p, "hello\n"); err != nil {
		t.Fatal(err)
	}
	e := &WorkspaceEdit{Changes: map[string][]TextEdit{uriFor(p): {{Range: Range{Start: Position{Line: 0, Character: 0}, End: Position{Line: 0, Character: 5}}, NewText: "world"}}}}
	preview, err := PreviewWorkspaceEdit(e, WorkspaceEditPolicy{Root: d})
	if err != nil || preview.EditCount != 1 {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	if _, err := ApplyWorkspaceEdit(e, WorkspaceEditPolicy{Root: d}); err != nil {
		t.Fatal(err)
	}
	b, _ := osRead(p)
	if string(b) != "world\n" {
		t.Fatalf("got %q", b)
	}
}

func osWrite(p, s string) error       { return os.WriteFile(p, []byte(s), 0600) }
func osRead(p string) ([]byte, error) { return os.ReadFile(p) }
